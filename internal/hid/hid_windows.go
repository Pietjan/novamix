// Package hid is a minimal pure-Go HID layer for Windows: enumerate the
// HID device interfaces, read their vendor/product IDs and top-level
// collection capabilities, and open them for raw input/output reports.
//
// It covers exactly what the Nova dongle needs and nothing more, so the
// program cross-compiles from anywhere without cgo or hidapi.
package hid

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modCfgMgr32 = windows.NewLazySystemDLL("cfgmgr32.dll")
	modHid      = windows.NewLazySystemDLL("hid.dll")

	procCMGetDeviceInterfaceListSize = modCfgMgr32.NewProc("CM_Get_Device_Interface_List_SizeW")
	procCMGetDeviceInterfaceList     = modCfgMgr32.NewProc("CM_Get_Device_Interface_ListW")

	procHidDGetAttributes     = modHid.NewProc("HidD_GetAttributes")
	procHidDGetPreparsedData  = modHid.NewProc("HidD_GetPreparsedData")
	procHidDFreePreparsedData = modHid.NewProc("HidD_FreePreparsedData")
	procHidDGetProductString  = modHid.NewProc("HidD_GetProductString")
	procHidPGetCaps           = modHid.NewProc("HidP_GetCaps")
)

// GUID_DEVINTERFACE_HID from hidclass.h.
var guidDevInterfaceHID = windows.GUID{
	Data1: 0x4d1e55b2, Data2: 0xf16f, Data3: 0x11cf,
	Data4: [8]byte{0x88, 0xcb, 0x00, 0x11, 0x11, 0x00, 0x00, 0x30},
}

const (
	crSuccess     = 0
	crBufferSmall = 0x1a

	hidpStatusSuccess = 0x00110000
)

// hiddAttributes mirrors HIDD_ATTRIBUTES.
type hiddAttributes struct {
	Size          uint32
	VendorID      uint16
	ProductID     uint16
	VersionNumber uint16
}

// hidpCaps mirrors HIDP_CAPS (64 bytes).
type hidpCaps struct {
	Usage                     uint16
	UsagePage                 uint16
	InputReportByteLength     uint16
	OutputReportByteLength    uint16
	FeatureReportByteLength   uint16
	Reserved                  [17]uint16
	NumberLinkCollectionNodes uint16
	NumberInputButtonCaps     uint16
	NumberInputValueCaps      uint16
	NumberInputDataIndices    uint16
	NumberOutputButtonCaps    uint16
	NumberOutputValueCaps     uint16
	NumberOutputDataIndices   uint16
	NumberFeatureButtonCaps   uint16
	NumberFeatureValueCaps    uint16
	NumberFeatureDataIndices  uint16
}

// Info describes one HID top-level collection (one device interface path).
type Info struct {
	Path      string
	VendorID  uint16
	ProductID uint16
	Product   string
	UsagePage uint16
	Usage     uint16
	// Interface is the USB interface number parsed from the path's "&mi_NN"
	// segment, or -1 when the device is not a composite USB device.
	Interface int
	// Collection is the "&colNN" index parsed from the path, or -1.
	Collection      int
	InputReportLen  int
	OutputReportLen int
}

// Enumerate returns every present HID collection whose vendor and product
// IDs pass the filter. Collections that cannot be opened at all (some
// system-exclusive keyboards and mice) are skipped silently.
func Enumerate(match func(vendor, product uint16) bool) ([]Info, error) {
	paths, err := interfaceList(&guidDevInterfaceHID)
	if err != nil {
		return nil, fmt.Errorf("listing HID interfaces: %w", err)
	}

	var infos []Info
	for _, p := range paths {
		info, ok := describe(p)
		if !ok || !match(info.VendorID, info.ProductID) {
			continue
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func interfaceList(class *windows.GUID) ([]string, error) {
	const flags = 0 // CM_GET_DEVICE_INTERFACE_LIST_PRESENT

	for {
		var size uint32
		ret, _, _ := procCMGetDeviceInterfaceListSize.Call(
			uintptr(unsafe.Pointer(&size)),
			uintptr(unsafe.Pointer(class)),
			0, // NULL device instance ID: all devices
			flags,
		)
		if ret != crSuccess {
			return nil, fmt.Errorf("CM_Get_Device_Interface_List_Size: CONFIGRET 0x%x", ret)
		}
		if size == 0 {
			return nil, nil
		}

		buf := make([]uint16, size)
		ret, _, _ = procCMGetDeviceInterfaceList.Call(
			uintptr(unsafe.Pointer(class)),
			0,
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(size),
			flags,
		)
		switch ret {
		case crSuccess:
			return splitMultiSZ(buf), nil
		case crBufferSmall:
			continue // a device arrived between the two calls; retry
		default:
			return nil, fmt.Errorf("CM_Get_Device_Interface_List: CONFIGRET 0x%x", ret)
		}
	}
}

func splitMultiSZ(buf []uint16) []string {
	var out []string
	for i := 0; i < len(buf); {
		j := i
		for j < len(buf) && buf[j] != 0 {
			j++
		}
		if j > i {
			out = append(out, windows.UTF16ToString(buf[i:j]))
		}
		i = j + 1
	}
	return out
}

// describe opens the path with no access rights, which is allowed even for
// exclusively-owned devices, and reads its attributes and capabilities.
func describe(path string) (Info, bool) {
	h, err := open(path, 0)
	if err != nil {
		return Info{}, false
	}
	defer windows.CloseHandle(h)

	attrs := hiddAttributes{Size: uint32(unsafe.Sizeof(hiddAttributes{}))}
	if ok, _, _ := procHidDGetAttributes.Call(uintptr(h), uintptr(unsafe.Pointer(&attrs))); ok == 0 {
		return Info{}, false
	}

	info := Info{
		Path:       path,
		VendorID:   attrs.VendorID,
		ProductID:  attrs.ProductID,
		Interface:  pathSegment(path, "&mi_"),
		Collection: pathSegment(path, "&col"),
	}

	var preparsed uintptr
	if ok, _, _ := procHidDGetPreparsedData.Call(uintptr(h), uintptr(unsafe.Pointer(&preparsed))); ok != 0 {
		var caps hidpCaps
		if st, _, _ := procHidPGetCaps.Call(preparsed, uintptr(unsafe.Pointer(&caps))); st == hidpStatusSuccess {
			info.UsagePage = caps.UsagePage
			info.Usage = caps.Usage
			info.InputReportLen = int(caps.InputReportByteLength)
			info.OutputReportLen = int(caps.OutputReportByteLength)
		}
		procHidDFreePreparsedData.Call(preparsed)
	}

	name := make([]uint16, 128)
	if ok, _, _ := procHidDGetProductString.Call(uintptr(h), uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)*2)); ok != 0 {
		info.Product = windows.UTF16ToString(name)
	}

	return info, true
}

// pathSegment extracts the hexadecimal number following key (e.g. "&mi_")
// in a device interface path, or -1 when absent.
func pathSegment(path, key string) int {
	lower := strings.ToLower(path)
	_, after, ok := strings.Cut(lower, key)
	if !ok {
		return -1
	}
	rest := after
	end := 0
	for end < len(rest) && isHex(rest[end]) {
		end++
	}
	if end == 0 {
		return -1
	}
	n, err := strconv.ParseInt(rest[:end], 16, 32)
	if err != nil {
		return -1
	}
	return int(n)
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}

func open(path string, access uint32) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(p, access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		windows.OPEN_EXISTING, 0, 0)
}

// Device is an open HID collection. Reads and writes use separate handles
// so a blocking read never serialises behind a write on the same file
// object (synchronous handles process one I/O request at a time).
type Device struct {
	Info
	read  windows.Handle
	write windows.Handle
}

// Open opens the collection for reading input reports and, when it exposes
// output reports, for writing them.
func Open(info Info) (*Device, error) {
	r, err := open(info.Path, windows.GENERIC_READ)
	if err != nil {
		return nil, fmt.Errorf("opening %s for reading: %w", info.Path, err)
	}
	d := &Device{Info: info, read: r}

	if info.OutputReportLen > 0 {
		w, err := open(info.Path, windows.GENERIC_WRITE)
		if err != nil {
			windows.CloseHandle(r)
			return nil, fmt.Errorf("opening %s for writing: %w", info.Path, err)
		}
		d.write = w
	}
	return d, nil
}

// Read blocks until the next input report arrives and returns it as the
// driver delivers it: byte 0 is the report ID, 0x00 when the collection
// does not use numbered reports.
func (d *Device) Read() ([]byte, error) {
	buf := make([]byte, max(d.InputReportLen, 1))
	var n uint32
	if err := windows.ReadFile(d.read, buf, &n, nil); err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// Write sends one output report. The report must start with the report
// ID (0x00 for unnumbered collections); it is zero-padded to the
// collection's output report length as Windows requires.
func (d *Device) Write(report []byte) error {
	if d.write == 0 {
		return errors.New("collection has no output reports")
	}
	buf := make([]byte, d.OutputReportLen)
	copy(buf, report)
	var n uint32
	return windows.WriteFile(d.write, buf, &n, nil)
}

// Close cancels any blocked Read and releases both handles. It is safe to
// call from another goroutine while Read is pending.
func (d *Device) Close() error {
	// Cancel first so the reader's ReadFile returns ERROR_OPERATION_ABORTED
	// instead of racing a closed handle.
	_ = windows.CancelIoEx(d.read, nil)
	err := windows.CloseHandle(d.read)
	if d.write != 0 {
		if werr := windows.CloseHandle(d.write); err == nil {
			err = werr
		}
	}
	return err
}
