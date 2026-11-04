package mixer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
	"golang.org/x/sys/windows"
)

// Levels is a dial position: game and chat volumes, 0-100 each.
type Levels struct {
	Game uint8
	Chat uint8
}

// Mixer owns the Core Audio objects. All methods must run on the goroutine
// that called Run: COM objects are bound to the thread that created them.
type Mixer struct {
	log      *slog.Logger
	classify *Classifier
	// deviceMatch is a case-insensitive substring of the playback device's
	// friendly name; empty means the default playback device.
	deviceMatch string
	dryRun      bool

	enum       *wca.IMMDeviceEnumerator
	device     *wca.IMMDevice
	deviceName string
	manager    *wca.IAudioSessionManager2

	// original remembers each session's volume before the first time it
	// was touched, keyed by session instance identifier, so Restore can put
	// the Windows mixer back the way the user had it.
	original map[string]float32
	images   map[uint32]string // process ID -> image path, cleared on refresh
}

// New returns a mixer that is not yet attached to COM; Run does that.
func New(log *slog.Logger, classify *Classifier, deviceMatch string, dryRun bool) *Mixer {
	return &Mixer{
		log:         log,
		classify:    classify,
		deviceMatch: deviceMatch,
		dryRun:      dryRun,
		original:    make(map[string]float32),
		images:      make(map[uint32]string),
	}
}

// Run applies every dial position received on levels until ctx is done,
// re-applying the last position every refresh so sessions that appeared
// since (a game just launched) pick it up. On exit it restores the
// per-app volumes it changed.
func (m *Mixer) Run(ctx context.Context, levels <-chan Levels, refresh time.Duration) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		return fmt.Errorf("CoInitializeEx: %w", err)
	}
	defer ole.CoUninitialize()

	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &m.enum); err != nil {
		return fmt.Errorf("creating MMDeviceEnumerator: %w", err)
	}
	defer m.enum.Release()
	defer m.detach()

	ticker := time.NewTicker(refresh)
	defer ticker.Stop()

	var last *Levels
	for {
		select {
		case <-ctx.Done():
			if err := m.restore(); err != nil {
				m.log.Warn("restoring per-app volumes", "err", err)
			}
			return nil
		case l := <-levels:
			last = &l
			if err := m.apply(l); err != nil {
				m.log.Warn("applying dial", "levels", l, "err", err)
			}
		case <-ticker.C:
			clear(m.images)
			if last == nil {
				continue
			}
			if err := m.apply(*last); err != nil {
				m.log.Warn("refreshing sessions", "err", err)
			}
		}
	}
}

// attach resolves the playback device and its session manager, keeping
// them across calls until something fails.
func (m *Mixer) attach() error {
	if m.manager != nil {
		return nil
	}

	device, name, err := m.findDevice()
	if err != nil {
		return err
	}

	var manager *wca.IAudioSessionManager2
	if err := device.Activate(wca.IID_IAudioSessionManager2, wca.CLSCTX_ALL, nil, &manager); err != nil {
		device.Release()
		return fmt.Errorf("activating session manager on %q: %w", name, err)
	}

	m.device, m.deviceName, m.manager = device, name, manager
	m.log.Info("mixing on playback device", "device", name)
	return nil
}

func (m *Mixer) detach() {
	if m.manager != nil {
		m.manager.Release()
		m.manager = nil
	}
	if m.device != nil {
		m.device.Release()
		m.device = nil
	}
}

// findDevice returns the active render endpoint whose friendly name
// contains deviceMatch, or the default endpoint when nothing matches.
func (m *Mixer) findDevice() (*wca.IMMDevice, string, error) {
	if m.deviceMatch != "" {
		var coll *wca.IMMDeviceCollection
		if err := m.enum.EnumAudioEndpoints(wca.ERender, wca.DEVICE_STATE_ACTIVE, &coll); err != nil {
			return nil, "", fmt.Errorf("enumerating playback devices: %w", err)
		}
		defer coll.Release()

		var count uint32
		if err := coll.GetCount(&count); err != nil {
			return nil, "", err
		}
		want := strings.ToLower(m.deviceMatch)
		for i := range count {
			var dev *wca.IMMDevice
			if err := coll.Item(i, &dev); err != nil {
				continue
			}
			name := friendlyName(dev)
			if strings.Contains(strings.ToLower(name), want) {
				return dev, name, nil
			}
			dev.Release()
		}
		m.log.Warn("no playback device matches, using the default one", "match", m.deviceMatch)
	}

	var dev *wca.IMMDevice
	if err := m.enum.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &dev); err != nil {
		return nil, "", fmt.Errorf("getting default playback device: %w", err)
	}
	return dev, friendlyName(dev), nil
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

// session is one entry of the endpoint's session list.
type session struct {
	id      string
	pid     uint32
	system  bool
	control *wca.IAudioSessionControl
	volume  *wca.ISimpleAudioVolume
}

func (s *session) release() {
	if s.volume != nil {
		s.volume.Release()
	}
	if s.control != nil {
		s.control.Release()
	}
}

// forEachSession walks the live (non-expired) sessions on the endpoint.
func (m *Mixer) forEachSession(fn func(s *session) error) error {
	if err := m.attach(); err != nil {
		return err
	}

	var enum *wca.IAudioSessionEnumerator
	if err := m.manager.GetSessionEnumerator(&enum); err != nil {
		m.detach() // the device probably went away; re-resolve next time
		return fmt.Errorf("enumerating sessions: %w", err)
	}
	defer enum.Release()

	var count int
	if err := enum.GetCount(&count); err != nil {
		return err
	}

	for i := range count {
		s, ok := m.openSession(enum, i)
		if !ok {
			continue
		}
		err := fn(s)
		s.release()
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Mixer) openSession(enum *wca.IAudioSessionEnumerator, i int) (*session, bool) {
	var control *wca.IAudioSessionControl
	if err := enum.GetSession(i, &control); err != nil {
		return nil, false
	}
	s := &session{control: control}

	var state uint32
	if err := control.GetState(&state); err != nil || state == wca.AudioSessionStateExpired {
		s.release()
		return nil, false
	}

	disp, err := control.QueryInterface(wca.IID_IAudioSessionControl2)
	if err != nil {
		s.release()
		return nil, false
	}
	c2 := (*wca.IAudioSessionControl2)(unsafe.Pointer(disp))
	defer c2.Release()

	if err := c2.GetSessionInstanceIdentifier(&s.id); err != nil {
		s.release()
		return nil, false
	}
	if err := c2.GetProcessId(&s.pid); err != nil {
		s.pid = 0
	}
	// S_OK means "is the system sounds session"; S_FALSE comes back as an error.
	s.system = c2.IsSystemSoundsSession() == nil

	disp, err = control.QueryInterface(wca.IID_ISimpleAudioVolume)
	if err != nil {
		s.release()
		return nil, false
	}
	s.volume = (*wca.ISimpleAudioVolume)(unsafe.Pointer(disp))
	return s, true
}

// apply sets every session's volume from the dial.
func (m *Mixer) apply(l Levels) error {
	seen := make(map[string]struct{})
	err := m.forEachSession(func(s *session) error {
		seen[s.id] = struct{}{}

		image := ""
		if !s.system && s.pid != 0 {
			image = m.imagePath(s.pid)
		}
		channel := m.classify.Classify(image)
		target := float32(l.Game) / 100
		if channel == Chat {
			target = float32(l.Chat) / 100
		}

		var current float32
		if err := s.volume.GetMasterVolume(&current); err != nil {
			return nil
		}
		if _, known := m.original[s.id]; !known {
			m.original[s.id] = current
			m.log.Debug("session", "channel", channel, "pid", s.pid, "image", image, "system", s.system, "volume", current)
		}
		if math.Abs(float64(current-target)) < 0.002 {
			return nil
		}
		if m.dryRun {
			m.log.Info("would set volume", "channel", channel, "pid", s.pid, "image", image, "from", current, "to", target)
			return nil
		}
		if err := s.volume.SetMasterVolume(target, nil); err != nil {
			m.log.Debug("set volume failed", "pid", s.pid, "image", image, "err", err)
			return nil
		}
		m.log.Debug("volume", "channel", channel, "pid", s.pid, "image", image, "volume", target)
		return nil
	})

	// Forget sessions that expired so a reused instance id starts fresh.
	for id := range m.original {
		if _, ok := seen[id]; !ok {
			delete(m.original, id)
		}
	}
	return err
}

// restore puts back the volumes recorded before the first change.
func (m *Mixer) restore() error {
	if m.dryRun || len(m.original) == 0 {
		return nil
	}
	err := m.forEachSession(func(s *session) error {
		v, ok := m.original[s.id]
		if !ok {
			return nil
		}
		if err := s.volume.SetMasterVolume(v, nil); err == nil {
			delete(m.original, s.id)
		}
		return nil
	})
	m.log.Info("restored per-app volumes", "unrestored", len(m.original))
	clear(m.original)
	return err
}

// imagePath returns the executable path of a process, cached until the
// next refresh tick.
func (m *Mixer) imagePath(pid uint32) string {
	if p, ok := m.images[pid]; ok {
		return p
	}
	p, err := queryImagePath(pid)
	if err != nil {
		m.log.Debug("process image unknown", "pid", pid, "err", err)
	}
	m.images[pid] = p
	return p
}

func queryImagePath(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, windows.MAX_PATH)
	for {
		size := uint32(len(buf))
		err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size)
		if err == nil {
			return windows.UTF16ToString(buf[:size]), nil
		}
		if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || len(buf) >= 32768 {
			return "", err
		}
		buf = make([]uint16, len(buf)*2)
	}
}
