// Package switcher makes the headset the default Windows audio device
// while it is on and puts the previous defaults back when it goes off,
// the way SteelSeries GG did.
//
// Windows has no public API for setting the default device. Like every
// tool that does it (SoundSwitch, nircmd, AudioSwitcher), this uses the
// undocumented IPolicyConfig interface of the audio policy service, which
// has kept its layout from Windows 7 through 11.
package switcher

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
	"golang.org/x/sys/windows"
)

var (
	clsidPolicyConfigClient = ole.NewGUID("{870af99c-171d-4f9e-af0d-e63df40c2bc9}")
	// Windows 10 and later layout (has ResetDeviceFormat).
	iidPolicyConfig = ole.NewGUID("{f8679f50-850a-41cf-9c72-430f290290c8}")
	// Vista through 8.1 layout.
	iidPolicyConfigVista = ole.NewGUID("{568b9108-44bf-40b4-9006-86afe5b5a620}")
)

// roles are the ERole values a default is set for: console, multimedia,
// communications.
var roles = [...]uint32{wca.EConsole, wca.EMultimedia, wca.ECommunications}

// policyConfig wraps the COM object; only SetDefaultEndpoint is used.
type policyConfig struct {
	unk                *ole.IUnknown
	setDefaultEndpoint uintptr
}

func newPolicyConfig() (*policyConfig, error) {
	// Vtable slot of SetDefaultEndpoint after the three IUnknown methods.
	const slotWin10, slotVista = 3 + 10, 3 + 9

	unk, err := ole.CreateInstance(clsidPolicyConfigClient, iidPolicyConfig)
	slot := slotWin10
	if err != nil {
		unk, err = ole.CreateInstance(clsidPolicyConfigClient, iidPolicyConfigVista)
		slot = slotVista
	}
	if err != nil {
		return nil, fmt.Errorf("creating PolicyConfig client: %w", err)
	}
	vtable := (*[16]uintptr)(unsafe.Pointer(unk.RawVTable))
	return &policyConfig{unk: unk, setDefaultEndpoint: vtable[slot]}, nil
}

func (p *policyConfig) SetDefaultEndpoint(deviceID string, role uint32) error {
	id, err := windows.UTF16PtrFromString(deviceID)
	if err != nil {
		return err
	}
	hr, _, _ := syscall.SyscallN(p.setDefaultEndpoint,
		uintptr(unsafe.Pointer(p.unk)), uintptr(unsafe.Pointer(id)), uintptr(role))
	if hr != 0 {
		return ole.NewError(hr)
	}
	return nil
}

func (p *policyConfig) Release() { p.unk.Release() }

// flow is one data direction with what was default before the headset
// took over, per role.
type flow struct {
	dataFlow uint32
	name     string
	previous map[uint32]string // role -> endpoint ID, empty while not switched
}

// Switcher owns the COM objects; all methods run on the Run goroutine.
type Switcher struct {
	log         *slog.Logger
	deviceMatch string
	// fallbackMatch names the device to make default when the headset goes
	// off and nothing was remembered, because the headset already was the
	// default when novamix started. Empty leaves the defaults alone then.
	fallbackMatch string
	enum          *wca.IMMDeviceEnumerator
	policy        *policyConfig
	flows         [2]*flow
}

// New returns a switcher for the endpoints whose friendly name contains
// deviceMatch (case-insensitive), falling back to the endpoints matching
// fallbackMatch when the headset goes off with no previous default known.
func New(log *slog.Logger, deviceMatch, fallbackMatch string) *Switcher {
	return &Switcher{
		log:           log,
		deviceMatch:   deviceMatch,
		fallbackMatch: fallbackMatch,
		flows: [2]*flow{
			{dataFlow: wca.ERender, name: "playback", previous: map[uint32]string{}},
			{dataFlow: wca.ECapture, name: "recording", previous: map[uint32]string{}},
		},
	}
}

// Run applies every headset state received on states until ctx is done.
// On the way out the previous devices are put back, so a clean exit
// leaves Windows as it was.
func (s *Switcher) Run(ctx context.Context, states <-chan bool) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		return fmt.Errorf("CoInitializeEx: %w", err)
	}
	defer ole.CoUninitialize()

	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &s.enum); err != nil {
		return fmt.Errorf("creating MMDeviceEnumerator: %w", err)
	}
	defer s.enum.Release()

	policy, err := newPolicyConfig()
	if err != nil {
		return err
	}
	s.policy = policy
	defer policy.Release()

	for {
		select {
		case <-ctx.Done():
			s.restore()
			return nil
		case on := <-states:
			if on {
				s.takeOver()
			} else {
				s.restore()
			}
		}
	}
}

// takeOver makes the headset endpoints default, remembering what they
// replace. A flow whose default already is the headset is left alone.
func (s *Switcher) takeOver() {
	for _, f := range s.flows {
		if len(f.previous) > 0 {
			continue // already switched
		}
		headset, name, err := s.findHeadset(f.dataFlow)
		if err != nil {
			s.log.Warn("no headset endpoint to switch to", "flow", f.name, "err", err)
			continue
		}

		previous := map[uint32]string{}
		for _, role := range roles {
			id, err := s.defaultID(f.dataFlow, role)
			if err != nil {
				s.log.Debug("no default endpoint", "flow", f.name, "role", role, "err", err)
				continue
			}
			if id != headset {
				previous[role] = id
			}
		}
		if len(previous) == 0 {
			s.log.Debug("headset already default", "flow", f.name)
			continue
		}

		for role := range previous {
			if err := s.policy.SetDefaultEndpoint(headset, role); err != nil {
				s.log.Warn("setting default endpoint", "flow", f.name, "role", role, "err", err)
				delete(previous, role)
			}
		}
		f.previous = previous
		s.log.Info("switched default device to headset", "flow", f.name, "device", name)
	}
}

// restore puts the remembered defaults back, or the fallback device when
// nothing was remembered.
func (s *Switcher) restore() {
	for _, f := range s.flows {
		if len(f.previous) == 0 {
			s.fallBack(f)
			continue
		}
		for role, id := range f.previous {
			if err := s.policy.SetDefaultEndpoint(id, role); err != nil {
				s.log.Warn("restoring default endpoint", "flow", f.name, "role", role, "err", err)
			}
		}
		s.log.Info("restored previous default device", "flow", f.name, "device", s.nameOf(f.previous[wca.EConsole]))
		clear(f.previous)
	}
}

// fallBack makes the fallback device default for a flow whose default is
// the (now off) headset. Without a fallback it only reports the choice.
func (s *Switcher) fallBack(f *flow) {
	headset, _, err := s.findHeadset(f.dataFlow)
	if err != nil {
		return
	}
	current, err := s.defaultID(f.dataFlow, wca.EConsole)
	if err != nil || current != headset {
		return // something else is default already
	}
	if s.fallbackMatch == "" {
		s.log.Info("headset is the default device and no previous one is known; set -fallback to switch away",
			"flow", f.name, "available", s.endpointNames(f.dataFlow, headset))
		return
	}
	id, name, err := s.findEndpoint(f.dataFlow, s.fallbackMatch, headset)
	if err != nil {
		s.log.Warn("no fallback endpoint", "flow", f.name, "err", err)
		return
	}
	for _, role := range roles {
		if err := s.policy.SetDefaultEndpoint(id, role); err != nil {
			s.log.Warn("setting fallback endpoint", "flow", f.name, "role", role, "err", err)
		}
	}
	s.log.Info("switched default device to fallback", "flow", f.name, "device", name)
}

func (s *Switcher) defaultID(dataFlow, role uint32) (string, error) {
	var dev *wca.IMMDevice
	if err := s.enum.GetDefaultAudioEndpoint(dataFlow, role, &dev); err != nil {
		return "", err
	}
	defer dev.Release()
	var id string
	err := dev.GetId(&id)
	return id, err
}

// findHeadset returns the ID and name of the active endpoint of the given
// flow whose friendly name contains deviceMatch.
func (s *Switcher) findHeadset(dataFlow uint32) (id, name string, err error) {
	return s.findEndpoint(dataFlow, s.deviceMatch, "")
}

// findEndpoint returns the first active endpoint of the flow whose
// friendly name contains match (case-insensitive), skipping the endpoint
// with ID exclude.
func (s *Switcher) findEndpoint(dataFlow uint32, match, exclude string) (id, name string, err error) {
	want := strings.ToLower(match)
	err = s.eachEndpoint(dataFlow, func(devID, devName string) bool {
		if devID != exclude && strings.Contains(strings.ToLower(devName), want) {
			id, name = devID, devName
			return false
		}
		return true
	})
	if err == nil && id == "" {
		err = fmt.Errorf("no active endpoint matches %q", match)
	}
	return id, name, err
}

// endpointNames lists the active endpoints of a flow other than exclude.
func (s *Switcher) endpointNames(dataFlow uint32, exclude string) []string {
	var names []string
	_ = s.eachEndpoint(dataFlow, func(id, name string) bool {
		if id != exclude {
			names = append(names, name)
		}
		return true
	})
	return names
}

// eachEndpoint calls fn for every active endpoint of the flow until fn
// returns false.
func (s *Switcher) eachEndpoint(dataFlow uint32, fn func(id, name string) bool) error {
	var coll *wca.IMMDeviceCollection
	if err := s.enum.EnumAudioEndpoints(dataFlow, wca.DEVICE_STATE_ACTIVE, &coll); err != nil {
		return err
	}
	defer coll.Release()

	var count uint32
	if err := coll.GetCount(&count); err != nil {
		return err
	}
	for i := range count {
		var dev *wca.IMMDevice
		if err := coll.Item(i, &dev); err != nil {
			continue
		}
		var id string
		err := dev.GetId(&id)
		name := friendlyName(dev)
		dev.Release()
		if err != nil {
			continue
		}
		if !fn(id, name) {
			return nil
		}
	}
	return nil
}

// nameOf resolves an endpoint ID to its friendly name for logging.
func (s *Switcher) nameOf(id string) string {
	if id == "" {
		return ""
	}
	dev, err := getDevice(s.enum, id)
	if err != nil {
		return id
	}
	defer dev.Release()
	return friendlyName(dev)
}

// getDevice is IMMDeviceEnumerator::GetDevice, which go-wca leaves
// unimplemented.
func getDevice(enum *wca.IMMDeviceEnumerator, id string) (*wca.IMMDevice, error) {
	id16, err := windows.UTF16PtrFromString(id)
	if err != nil {
		return nil, err
	}
	var dev *wca.IMMDevice
	hr, _, _ := syscall.SyscallN(enum.VTable().GetDevice,
		uintptr(unsafe.Pointer(enum)), uintptr(unsafe.Pointer(id16)), uintptr(unsafe.Pointer(&dev)))
	if hr != 0 {
		return nil, ole.NewError(hr)
	}
	return dev, nil
}

func friendlyName(dev *wca.IMMDevice) string {
	var store *wca.IPropertyStore
	if err := dev.OpenPropertyStore(wca.STGM_READ, &store); err != nil {
		return ""
	}
	defer store.Release()

	var pv wca.PROPVARIANT
	if err := store.GetValue(&wca.PKEY_Device_FriendlyName, &pv); err != nil {
		return ""
	}
	return pv.String()
}
