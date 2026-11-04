package nova

import "testing"

func TestParseStatus(t *testing.T) {
	tests := []struct {
		name   string
		report []byte
		want   Status
		ok     bool
	}{
		{
			name:   "windows framing with report id",
			report: []byte{0x00, 0xb0, 0x00, 0x03, 0x01, 100, 37, 0, 0, 0},
			want:   Status{Levels: Levels{Game: 100, Chat: 37}, Dial: true, Headset: true, Battery: 3, Charging: true},
			ok:     true,
		},
		{
			name:   "wire framing without report id",
			report: []byte{0xb0, 0x00, 0x04, 0x03, 42, 100},
			want:   Status{Levels: Levels{Game: 42, Chat: 100}, Dial: true, Headset: true, Battery: 4},
			ok:     true,
		},
		{
			name:   "headset offline still carries dial",
			report: []byte{0x00, 0xb0, 0x00, 0x00, 0x00, 100, 100},
			want:   Status{Levels: Levels{Game: 100, Chat: 100}, Dial: true},
			ok:     true,
		},
		{
			name:   "headset off: dongle sends 0xff for the dial",
			report: []byte{0x00, 0xb0, 0x02, 0x00, 0x00, 0xff, 0xff},
			want:   Status{},
			ok:     true,
		},
		{name: "too short", report: []byte{0x00, 0xb0, 0x00, 0x03, 0x01, 100}},
		{name: "other opcode", report: []byte{0x00, 0x20, 0, 0, 0, 0, 0}},
		{name: "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseStatus(tt.report)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name   string
		report []byte
		want   Levels
		ok     bool
	}{
		{name: "status report", report: []byte{0x00, 0xb0, 0, 3, 1, 80, 100}, want: Levels{80, 100}, ok: true},
		{name: "gen2 async with report id", report: []byte{0x00, 0x45, 55, 100, 0, 0, 0}, want: Levels{55, 100}, ok: true},
		{name: "gen2 async bare", report: []byte{0x45, 100, 10}, want: Levels{100, 10}, ok: true},
		{name: "nova pro framing", report: []byte{0x07, 0x45, 64, 64, 0}, want: Levels{64, 64}, ok: true},
		{name: "nova pro framing with report id", report: []byte{0x00, 0x07, 0x45, 64, 64, 0}, want: Levels{64, 64}, ok: true},
		{name: "async out of range", report: []byte{0x45, 101, 0}},
		{name: "status without dial", report: []byte{0x00, 0xb0, 2, 0, 0, 0xff, 0xff}},
		{name: "power report ignored", report: []byte{0xb9, 3, 0, 0}},
		{name: "consumer control", report: []byte{0x01, 0x00}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Parse(tt.report)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
