// Package nova speaks the SteelSeries Arctis Nova 7 dongle protocol far
// enough to read the ChatMix dial.
//
// The dongle exposes a vendor HID collection (usage page 0xffc0, usage 1,
// USB interface 3). Writing the two-byte status request [0x00 0xb0] makes
// it answer with a status report whose bytes 4 and 5 hold the game and
// chat levels, 0-100 each; the same report also arrives unsolicited when
// something changes. Whenever the dial moves the dongle also pushes a short
// [0x45 game chat] report on a second interface (USB interface 5).
//
// Byte offsets are given for the report as it appears on the wire. Windows
// prefixes every report with its report ID (0x00 for these collections),
// which Parse strips.
//
// Protocol references: HeadsetControl (steelseries_arctis_nova_7.hpp) and
// the Linux hid-steelseries ChatMix series.
package nova

import "fmt"

// VendorID is SteelSeries' USB vendor ID.
const VendorID = 0x1038

// ProductIDs lists the Nova 7 family dongles known to use this protocol.
var ProductIDs = map[uint16]string{
	0x2202: "Arctis Nova 7",
	0x22a1: "Arctis Nova 7",
	0x227e: "Arctis Nova 7 Wireless Gen 2",
	0x2206: "Arctis Nova 7X",
	0x2258: "Arctis Nova 7X v2",
	0x229e: "Arctis Nova 7X v2",
	0x22ad: "Arctis Nova 7X v2",
	0x22a4: "Arctis Nova 7X",
	0x22a5: "Arctis Nova 7X",
	0x223a: "Arctis Nova 7 Diablo IV",
	0x22a9: "Arctis Nova 7 Diablo IV",
	0x227a: "Arctis Nova 7 WoW Edition",
}

// Vendor collection identifiers of the command/status interface.
const (
	VendorUsagePage = 0xffc0
	VendorUsage     = 0x01
)

// Report opcodes.
const (
	opStatus  = 0xb0 // status request / status report
	opChatMix = 0x45 // async ChatMix report (Gen 2 second interface, Nova Pro)
)

// StatusRequest is the output report that asks the dongle for a status
// report. Byte 0 is the (unnumbered) report ID required by Windows.
var StatusRequest = []byte{0x00, opStatus}

// Levels is a ChatMix dial position: the game and chat volumes the dial
// asks for, 0-100 each. At the centre both are 100.
type Levels struct {
	Game uint8
	Chat uint8
}

func (l Levels) String() string {
	return fmt.Sprintf("game=%d chat=%d", l.Game, l.Chat)
}

// Status is a parsed 0xb0 status report.
type Status struct {
	// Levels is the dial position, valid only when Dial is true.
	Levels
	// Dial is false when the report carries no usable dial position: the
	// dongle sends 0xff for both levels while the headset is off.
	Dial bool
	// Headset is false when the dongle reports no connected headset.
	Headset bool
	// Battery is the raw battery byte: 0-4 on original models, 0-100 on
	// Gen 2 / updated firmware.
	Battery uint8
	// Charging is true when the status byte reports charging.
	Charging bool
}

// Parse decodes one input report as delivered by Windows (report ID
// first). It returns ok=false for reports that are not ChatMix related,
// so callers can read every collection of the dongle and ignore the rest.
func Parse(report []byte) (levels Levels, ok bool) {
	if st, ok := ParseStatus(report); ok {
		return st.Levels, st.Dial
	}
	return parseChatMix(strip(report))
}

// ParseStatus decodes a 0xb0 status report.
func ParseStatus(report []byte) (Status, bool) {
	b := strip(report)
	if len(b) < 6 || b[0] != opStatus {
		return Status{}, false
	}
	game, chat := b[4], b[5]
	st := Status{
		Headset:  b[3] != 0,
		Battery:  b[2],
		Charging: b[3] == 1 || b[3] == 2,
	}
	if game <= 100 && chat <= 100 {
		st.Levels, st.Dial = Levels{Game: game, Chat: chat}, true
	}
	return st, true
}

// parseChatMix decodes the async [0x45 game chat] report, also accepting
// the Nova Pro framing [0x07 0x45 game chat].
func parseChatMix(b []byte) (Levels, bool) {
	if len(b) >= 2 && b[0] == 0x07 && b[1] == opChatMix {
		b = b[1:]
	}
	if len(b) < 3 || b[0] != opChatMix {
		return Levels{}, false
	}
	game, chat := b[1], b[2]
	if game > 100 || chat > 100 {
		return Levels{}, false
	}
	return Levels{Game: game, Chat: chat}, true
}

// strip removes the leading 0x00 report ID Windows adds to reports of
// collections that do not use numbered reports.
func strip(report []byte) []byte {
	if len(report) > 0 && report[0] == 0 {
		return report[1:]
	}
	return report
}
