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
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"
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

// kickDelay is how long applications get to follow a switch on their own
// before the ones still playing on the old device are kicked off it.
const kickDelay = time.Second

// policyConfig wraps the COM object and the methods used of it.
type policyConfig struct {
	unk                *ole.IUnknown
	getMixFormat       uintptr
	getDeviceFormat    uintptr
	setDeviceFormat    uintptr
	setDefaultEndpoint uintptr
}

func newPolicyConfig() (*policyConfig, error) {
	// Vtable slots after the three IUnknown methods. Windows 10 inserted
	// ResetDeviceFormat after GetDeviceFormat, shifting everything behind.
	const getMixFormat, getDeviceFormat = 3 + 0, 3 + 1

	unk, err := ole.CreateInstance(clsidPolicyConfigClient, iidPolicyConfig)
	setDeviceFormat, setDefaultEndpoint := 3+3, 3+10
	if err != nil {
		unk, err = ole.CreateInstance(clsidPolicyConfigClient, iidPolicyConfigVista)
		setDeviceFormat, setDefaultEndpoint = 3+2, 3+9
	}
	if err != nil {
		return nil, fmt.Errorf("creating PolicyConfig client: %w", err)
	}
	vtable := (*[16]uintptr)(unsafe.Pointer(unk.RawVTable))
	return &policyConfig{
		unk:                unk,
		getMixFormat:       vtable[getMixFormat],
		getDeviceFormat:    vtable[getDeviceFormat],
		setDeviceFormat:    vtable[setDeviceFormat],
		setDefaultEndpoint: vtable[setDefaultEndpoint],
	}, nil
}

// Formats returns the format the endpoint runs at and the engine mix
// format feeding it, as WAVEFORMATEX blobs.
func (p *policyConfig) Formats(deviceID string) (device, mix []byte, err error) {
	id, err := windows.UTF16PtrFromString(deviceID)
	if err != nil {
		return nil, nil, err
	}
	var ptr unsafe.Pointer
	hr, _, _ := syscall.SyscallN(p.getDeviceFormat,
		uintptr(unsafe.Pointer(p.unk)), uintptr(unsafe.Pointer(id)), 0, uintptr(unsafe.Pointer(&ptr)))
	if hr != 0 {
		return nil, nil, fmt.Errorf("device format: %w", ole.NewError(hr))
	}
	device = takeFormat(ptr)

	hr, _, _ = syscall.SyscallN(p.getMixFormat,
		uintptr(unsafe.Pointer(p.unk)), uintptr(unsafe.Pointer(id)), uintptr(unsafe.Pointer(&ptr)))
	if hr != 0 {
		return nil, nil, fmt.Errorf("mix format: %w", ole.NewError(hr))
	}
	return device, takeFormat(ptr), nil
}

// takeFormat copies a WAVEFORMATEX returned by COM and frees the original.
func takeFormat(ptr unsafe.Pointer) []byte {
	if ptr == nil {
		return nil
	}
	defer ole.CoTaskMemFree(uintptr(ptr))
	size := formatSize(unsafe.Slice((*byte)(ptr), waveFormatExSize))
	return slices.Clone(unsafe.Slice((*byte)(ptr), size))
}

// SetDeviceFormat sets the format of an endpoint and of the engine mix
// feeding it. Windows closes every stream open on the endpoint, also when
// the format is the one it already had.
func (p *policyConfig) SetDeviceFormat(deviceID string, device, mix []byte) error {
	id, err := windows.UTF16PtrFromString(deviceID)
	if err != nil {
		return err
	}
	if len(device) < waveFormatExSize || len(mix) < waveFormatExSize {
		return errors.New("truncated format")
	}
	hr, _, _ := syscall.SyscallN(p.setDeviceFormat,
		uintptr(unsafe.Pointer(p.unk)), uintptr(unsafe.Pointer(id)),
		uintptr(unsafe.Pointer(&device[0])), uintptr(unsafe.Pointer(&mix[0])))
	if hr != 0 {
		return ole.NewError(hr)
	}
	return nil
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
	// kick closes the streams still playing on a playback device a moment
	// after the default moved away from it. An application that opened
	// the device itself (most games) ignores the new default and Windows
	// cannot move its stream, but it does reopen on the default device
	// when its stream is closed under it.
	kick bool
	// stale are the playback endpoints the default last moved away from,
	// due a kick at kickAt.
	stale  []string
	kickAt time.Time
	enum   *wca.IMMDeviceEnumerator
	policy *policyConfig
	flows  [2]*flow
}

// New returns a switcher for the endpoints whose friendly name contains
// deviceMatch (case-insensitive), falling back to the endpoints matching
// fallbackMatch when the headset goes off with no previous default known.
// With kick, applications that keep playing on the device switched away
// from are made to reopen on the new default.
func New(log *slog.Logger, deviceMatch, fallbackMatch string, kick bool) *Switcher {
	return &Switcher{
		log:           log,
		deviceMatch:   deviceMatch,
		fallbackMatch: fallbackMatch,
		kick:          kick,
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

	var kick <-chan time.Time
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
			if len(s.stale) > 0 {
				kick = time.After(time.Until(s.kickAt))
			}
		case <-kick:
			kick = nil
			s.kickStale()
		}
	}
}

// markStale schedules a kick of the playback endpoints the default just
// moved away from.
func (s *Switcher) markStale(f *flow, ids ...string) {
	if !s.kick || f.dataFlow != wca.ERender || len(ids) == 0 {
		return
	}
	slices.Sort(ids)
	s.stale = slices.Compact(ids)
	s.kickAt = time.Now().Add(kickDelay)
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

		var replaced []string
		for role, id := range previous {
			if err := s.policy.SetDefaultEndpoint(headset, role); err != nil {
				s.log.Warn("setting default endpoint", "flow", f.name, "role", role, "err", err)
				delete(previous, role)
				continue
			}
			replaced = append(replaced, id)
		}
		f.previous = previous
		s.markStale(f, replaced...)
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
		if headset, _, err := s.findHeadset(f.dataFlow); err == nil {
			s.markStale(f, headset)
		}
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
	s.markStale(f, headset)
}

// kickStale closes the streams still playing on the endpoints the default
// moved away from, so their applications reopen on the default device.
// An endpoint nothing plays on, or that is the default again, is left
// alone.
func (s *Switcher) kickStale() {
	stale := s.stale
	s.stale = nil

	current, err := s.defaultID(wca.ERender, wca.EConsole)
	if err != nil {
		s.log.Debug("no default endpoint to move streams to", "err", err)
		return
	}
	for _, id := range stale {
		if id == current {
			continue
		}
		name := s.nameOf(id)
		pids, err := s.playing(id)
		if err != nil {
			s.log.Debug("listing streams on old device", "device", name, "err", err)
			continue
		}
		if len(pids) == 0 {
			s.log.Debug("nothing left playing on old device", "device", name)
			continue
		}
		if err := s.closeStreams(id); err != nil {
			s.log.Warn("could not move the applications still playing on the old device",
				"device", name, "pids", pids, "err", err)
			continue
		}
		s.log.Info("closed streams left on old device so they reopen on the default one",
			"device", name, "pids", pids)
	}
}

// playing returns the processes with a stream running on the endpoint,
// leaving out the system sounds session.
func (s *Switcher) playing(id string) ([]uint32, error) {
	dev, err := getDevice(s.enum, id)
	if err != nil {
		return nil, err
	}
	defer dev.Release()

	var manager *wca.IAudioSessionManager2
	if err := dev.Activate(wca.IID_IAudioSessionManager2, wca.CLSCTX_ALL, nil, &manager); err != nil {
		return nil, err
	}
	defer manager.Release()

	var sessions *wca.IAudioSessionEnumerator
	if err := manager.GetSessionEnumerator(&sessions); err != nil {
		return nil, err
	}
	defer sessions.Release()

	var count int
	if err := sessions.GetCount(&count); err != nil {
		return nil, err
	}
	var pids []uint32
	for i := range count {
		var control *wca.IAudioSessionControl
		if err := sessions.GetSession(i, &control); err != nil {
			continue
		}
		if pid, ok := playingProcess(control); ok && !slices.Contains(pids, pid) {
			pids = append(pids, pid)
		}
		control.Release()
	}
	return pids, nil
}

// playingProcess returns the process of a session that is rendering
// right now and is not the system sounds.
func playingProcess(control *wca.IAudioSessionControl) (uint32, bool) {
	var state uint32
	if err := control.GetState(&state); err != nil || state != wca.AudioSessionStateActive {
		return 0, false
	}
	disp, err := control.QueryInterface(wca.IID_IAudioSessionControl2)
	if err != nil {
		return 0, false
	}
	c2 := (*wca.IAudioSessionControl2)(unsafe.Pointer(disp))
	defer c2.Release()

	// S_OK means "is the system sounds session"; S_FALSE comes back as an error.
	if c2.IsSystemSoundsSession() == nil {
		return 0, false
	}
	var pid uint32
	if err := c2.GetProcessId(&pid); err != nil {
		return 0, false
	}
	return pid, true
}

// closeStreams makes Windows close every stream on the endpoint by
// setting the format it already has: there is no call that only closes
// streams, but setting a format does, and the same one leaves the device
// as it was.
func (s *Switcher) closeStreams(id string) error {
	device, mix, err := s.policy.Formats(id)
	if err != nil {
		return err
	}
	return s.policy.SetDeviceFormat(id, device, mix)
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
