package switcher

import (
	"encoding/binary"
	"testing"
)

func TestFormatSize(t *testing.T) {
	const waveFormatExtensible, extensibleTail = 0xfffe, 22

	pcm := make([]byte, waveFormatExSize)
	binary.LittleEndian.PutUint16(pcm[offFormatTag:], waveFormatPCM)
	binary.LittleEndian.PutUint16(pcm[offCbSize:], 0xbeef) // garbage past a PCMWAVEFORMAT
	if got := formatSize(pcm); got != waveFormatExSize {
		t.Errorf("plain PCM size = %d, want %d", got, waveFormatExSize)
	}

	ext := make([]byte, waveFormatExSize)
	binary.LittleEndian.PutUint16(ext[offFormatTag:], waveFormatExtensible)
	binary.LittleEndian.PutUint16(ext[offCbSize:], extensibleTail)
	if got, want := formatSize(ext), waveFormatExSize+extensibleTail; got != want {
		t.Errorf("extensible size = %d, want %d", got, want)
	}
}
