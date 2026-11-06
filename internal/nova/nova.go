// Package nova speaks the SteelSeries dongle protocol far enough to read
// the ChatMix dial of the headsets whose dial is mixed in software: the
// Arctis Nova 7 and Nova 5 families and the Arctis 7+.
//
// The dongle exposes a vendor HID collection (usage page 0xffc0, usage 1,
// USB interface 3). Writing the two-byte status request [0x00 0xb0] makes
// it answer with a status report that holds the game and chat levels,
// 0-100 each, next to the battery and whether the headset is on; the same
// report also arrives unsolicited when something changes. Where in the
// report each of those sits differs per family, see Family. On the Nova 7
// the dongle also pushes a short [0x45 game chat] report on a second
// interface (USB interface 5) whenever the dial moves.
//
// Byte offsets are given for the report as it appears on the wire. Windows
// prefixes every report with its report ID (0x00 for these collections),
// which Parse strips.
//
// Protocol reference: HeadsetControl (steelseries_arctis_nova_7.hpp,
// steelseries_arctis_nova_5.hpp, steelseries_arctis_7_plus.hpp). Only the
// Nova 7 layout has been checked against a real headset here.
package nova

import "fmt"

// VendorID is SteelSeries' USB vendor ID.
const VendorID = 0x1038

// Family is a group of dongles that lay out their status report the same
// way.
type Family struct {
	name string
	// size is the shortest status report that holds every field.
	size int
	// decode reads a status report of at least size bytes, opcode first.
	decode func(b []byte) Status
}

func (f *Family) String() string { return f.name }

// The families, by the offsets in their status report after the opcode
// at byte 0.
var (
	// Nova7: battery at 2 (0-4, or 0-100 on newer firmware), state at 3
	// (0 headset off, 1 and 2 charging, 3 on), game at 4, chat at 5.
	Nova7 = &Family{name: "Nova 7", size: 6, decode: func(b []byte) Status {
		return Status{
			Headset:  b[3] != 0,
			Battery:  b[2],
			Charging: b[3] == 1 || b[3] == 2,
			Game:     b[4],
			Chat:     b[5],
		}
	}}

	// Nova5: connection at 1 (2 headset off), battery percentage at 3,
	// charging at 4, game at 5, chat at 6.
	Nova5 = &Family{name: "Nova 5", size: 7, decode: func(b []byte) Status {
		return Status{
			Headset:  b[1] != 2,
			Battery:  b[3],
			Charging: b[4] == 1,
			Game:     b[5],
			Chat:     b[6],
		}
	}}

	// Arctis7Plus: connection at 1 (1 headset off), battery at 2 (0-4),
	// charging at 3, game at 4, chat at 5.
	Arctis7Plus = &Family{name: "Arctis 7+", size: 6, decode: func(b []byte) Status {
		return Status{
			Headset:  b[1] != 1,
			Battery:  b[2],
			Charging: b[3] == 1,
			Game:     b[4],
			Chat:     b[5],
		}
	}}
)

// Product is one dongle model.
type Product struct {
	Name   string
	Family *Family
}

// Products lists the dongles known to report a ChatMix dial that is not
// mixed by the headset itself, by USB product ID.
//
// Left out on purpose: the Nova 7P, whose dial sets sidetone; the older
// Arctis 7, 9 and Pro, which present a game and a chat playback device and
// mix in hardware; and the Nova Pro Wireless, whose base station has to be
// switched into ChatMix mode first.
var Products = map[uint16]Product{
	0x2202: {"Arctis Nova 7", Nova7},
	0x22a1: {"Arctis Nova 7", Nova7},
	0x227e: {"Arctis Nova 7 Wireless Gen 2", Nova7},
	0x2206: {"Arctis Nova 7X", Nova7},
	0x2258: {"Arctis Nova 7X v2", Nova7},
	0x229e: {"Arctis Nova 7X v2", Nova7},
	0x22ad: {"Arctis Nova 7X v2", Nova7},
	0x22a4: {"Arctis Nova 7X", Nova7},
	0x22a5: {"Arctis Nova 7X", Nova7},
	0x223a: {"Arctis Nova 7 Diablo IV", Nova7},
	0x22a9: {"Arctis Nova 7 Diablo IV", Nova7},
	0x227a: {"Arctis Nova 7 WoW Edition", Nova7},

	0x2232: {"Arctis Nova 5", Nova5},
	0x2253: {"Arctis Nova 5X", Nova5},

	0x220e: {"Arctis 7+", Arctis7Plus},
	0x2212: {"Arctis 7+ PS5", Arctis7Plus},
	0x2216: {"Arctis 7+ Xbox", Arctis7Plus},
	0x2236: {"Arctis 7+ Destiny", Arctis7Plus},
}

// Vendor collection identifiers of the command/status interface.
const (
	VendorUsagePage = 0xffc0
	VendorUsage     = 0x01
)

// Report opcodes.
const (
	opStatus  = 0xb0 // status request / status report
	opChatMix = 0x45 // async ChatMix report (Nova 7 second interface, Nova Pro)
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
	// Battery is the raw battery byte: 0-4 on older models, 0-100 on
	// newer ones.
	Battery uint8
	// Charging is true when the report says the headset is charging.
	Charging bool
}

// Parse decodes one input report as delivered by Windows (report ID
// first). It returns ok=false for reports that are not ChatMix related,
// so callers can read every collection of the dongle and ignore the rest.
func (f *Family) Parse(report []byte) (levels Levels, ok bool) {
	if st, ok := f.ParseStatus(report); ok {
		return st.Levels, st.Dial
	}
	return parseChatMix(strip(report))
}

// ParseStatus decodes a 0xb0 status report.
func (f *Family) ParseStatus(report []byte) (Status, bool) {
	b := strip(report)
	if len(b) < f.size || b[0] != opStatus {
		return Status{}, false
	}
	st := f.decode(b)
	st.Dial = st.Game <= 100 && st.Chat <= 100
	if !st.Dial {
		st.Levels = Levels{}
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
