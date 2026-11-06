package switcher

import "encoding/binary"

// Layout of WAVEFORMATEX, the header every audio format starts with.
const (
	offFormatTag     = 0
	offCbSize        = 16
	waveFormatExSize = 18

	waveFormatPCM = 0x0001
)

// formatSize returns how many bytes the format starting with header
// occupies; header must hold at least a WAVEFORMATEX.
func formatSize(header []byte) int {
	if binary.LittleEndian.Uint16(header[offFormatTag:]) == waveFormatPCM {
		return waveFormatExSize // cbSize is not meaningful for plain PCM
	}
	return waveFormatExSize + int(binary.LittleEndian.Uint16(header[offCbSize:]))
}
