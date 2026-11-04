package nova

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"text/tabwriter"
	"time"

	"golang.org/x/sys/windows"

	"github.com/pietjan/novamix/internal/hid"
)

// Device is an attached Nova 7 dongle: every readable HID collection it
// exposes, plus the vendor collection used for status requests.
type Device struct {
	Name   string
	vendor *hid.Device   // command/status collection
	all    []*hid.Device // every collection we listen on, vendor included
}

// Find enumerates HID collections and opens the first Nova 7 dongle. It
// returns ErrNotFound when none is attached.
func Find(log *slog.Logger) (*Device, error) {
	infos, err := hid.Enumerate(func(vendor, product uint16) bool {
		_, ok := ProductIDs[product]
		return vendor == VendorID && ok
	})
	if err != nil {
		return nil, err
	}
	if len(infos) == 0 {
		return nil, ErrNotFound
	}

	// All collections of one dongle share a product ID; stick to the first
	// product seen in case two dongles are plugged in.
	pid := infos[0].ProductID
	d := &Device{Name: ProductIDs[pid]}

	for _, info := range infos {
		if info.ProductID != pid || info.InputReportLen == 0 {
			continue
		}
		isVendor := info.UsagePage == VendorUsagePage && info.Usage == VendorUsage && info.OutputReportLen > 0
		if isVendor && d.vendor != nil {
			continue // one command collection is enough
		}

		dev, err := hid.Open(info)
		if err != nil {
			log.Debug("skipping collection", "path", info.Path, "err", err)
			continue
		}
		log.Debug("opened collection",
			"interface", info.Interface, "collection", info.Collection,
			"usage_page", fmt.Sprintf("0x%04x", info.UsagePage), "usage", info.Usage,
			"in", info.InputReportLen, "out", info.OutputReportLen, "vendor", isVendor)

		d.all = append(d.all, dev)
		if isVendor {
			d.vendor = dev
		}
	}

	if d.vendor == nil {
		d.Close()
		return nil, fmt.Errorf("%s (pid 0x%04x): vendor collection (usage page 0x%04x) not found or not writable",
			d.Name, pid, VendorUsagePage)
	}
	return d, nil
}

// ErrNotFound reports that no supported dongle is attached.
var ErrNotFound = errors.New("no Arctis Nova 7 dongle found")

// Close releases every collection, unblocking any reader.
func (d *Device) Close() {
	for _, dev := range d.all {
		dev.Close()
	}
	d.all = nil
	d.vendor = nil
}

// RequestStatus asks the dongle for a status report, which arrives
// through Run like any other report.
func (d *Device) RequestStatus() error {
	return d.vendor.Write(StatusRequest)
}

// Run reads reports from every collection until ctx is done or the dongle
// goes away, calling onDial on every change of the dial and onHeadset on
// every change of the headset's on/off state. When poll is positive it
// also requests a status report at that interval, which seeds the initial
// position and keeps the headset state current. dump logs every raw
// report at debug level.
//
// Run returns nil when ctx ends and an error when the device failed.
func (d *Device) Run(ctx context.Context, poll time.Duration, dump bool, log *slog.Logger, onDial func(Levels), onHeadset func(bool)) error {
	type report struct {
		dev  *hid.Device
		data []byte
		err  error
	}
	reports := make(chan report, 16)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, dev := range d.all {
		go func() {
			for {
				data, err := dev.Read()
				select {
				case reports <- report{dev, data, err}:
				case <-ctx.Done():
					return
				}
				if err != nil {
					return
				}
			}
		}()
	}

	var ticker *time.Ticker
	var tick <-chan time.Time
	if poll > 0 {
		ticker = time.NewTicker(poll)
		defer ticker.Stop()
		tick = ticker.C
	}
	if err := d.RequestStatus(); err != nil {
		return fmt.Errorf("status request: %w", err)
	}

	var last *Levels
	var headset *bool
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick:
			if err := d.RequestStatus(); err != nil {
				return fmt.Errorf("status request: %w", err)
			}
		case r := <-reports:
			if r.err != nil {
				if ctx.Err() != nil || errors.Is(r.err, windows.ERROR_OPERATION_ABORTED) {
					return nil
				}
				return fmt.Errorf("reading interface %d: %w", r.dev.Interface, r.err)
			}
			if dump {
				log.Debug("report", "interface", r.dev.Interface, "collection", r.dev.Collection, "data", fmt.Sprintf("% x", r.data))
			}
			if st, ok := ParseStatus(r.data); ok && (headset == nil || *headset != st.Headset) {
				headset = &st.Headset
				if st.Headset {
					log.Info("headset on", "battery", st.Battery, "charging", st.Charging)
				} else {
					log.Info("headset off")
				}
				onHeadset(st.Headset)
			}
			levels, ok := Parse(r.data)
			if !ok || (last != nil && *last == levels) {
				continue
			}
			last = &levels
			onDial(levels)
		}
	}
}

// List prints every HID collection of attached SteelSeries devices, known
// Nova 7 dongle or not, for diagnosing an unrecognised product ID.
func List(w io.Writer) error {
	infos, err := hid.Enumerate(func(vendor, _ uint16) bool { return vendor == VendorID })
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		fmt.Fprintln(w, "no SteelSeries HID devices found")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PID\tNAME\tIFACE\tCOL\tUSAGE PAGE\tUSAGE\tIN\tOUT\tKNOWN")
	for _, i := range infos {
		_, known := ProductIDs[i.ProductID]
		fmt.Fprintf(tw, "0x%04x\t%s\t%d\t%d\t0x%04x\t0x%02x\t%d\t%d\t%v\n",
			i.ProductID, i.Product, i.Interface, i.Collection, i.UsagePage, i.Usage, i.InputReportLen, i.OutputReportLen, known)
	}
	return tw.Flush()
}
