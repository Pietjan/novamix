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
			got, ok := Nova7.ParseStatus(tt.report)
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
			got, ok := Nova7.Parse(tt.report)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// The Nova 5 and Arctis 7+ layouts come from HeadsetControl, not from a
// headset: these reports are built from its description of them.
func TestParseStatusOtherFamilies(t *testing.T) {
	tests := []struct {
		name   string
		family *Family
		report []byte
		want   Status
		ok     bool
	}{
		{
			name:   "nova 5 on",
			family: Nova5,
			report: []byte{0x00, 0xb0, 0x00, 0x00, 87, 0x00, 100, 40},
			want:   Status{Levels: Levels{Game: 100, Chat: 40}, Dial: true, Headset: true, Battery: 87},
			ok:     true,
		},
		{
			name:   "nova 5 charging",
			family: Nova5,
			report: []byte{0xb0, 0x00, 0x00, 12, 0x01, 55, 100},
			want:   Status{Levels: Levels{Game: 55, Chat: 100}, Dial: true, Headset: true, Battery: 12, Charging: true},
			ok:     true,
		},
		{
			name:   "nova 5 headset off",
			family: Nova5,
			report: []byte{0x00, 0xb0, 0x02, 0x00, 0, 0x00, 100, 100},
			want:   Status{Levels: Levels{Game: 100, Chat: 100}, Dial: true},
			ok:     true,
		},
		{name: "nova 5 one byte short", family: Nova5, report: []byte{0x00, 0xb0, 0, 0, 87, 0, 100}},
		{
			name:   "arctis 7+ on",
			family: Arctis7Plus,
			report: []byte{0x00, 0xb0, 0x03, 0x04, 0x00, 70, 100},
			want:   Status{Levels: Levels{Game: 70, Chat: 100}, Dial: true, Headset: true, Battery: 4},
			ok:     true,
		},
		{
			name:   "arctis 7+ charging",
			family: Arctis7Plus,
			report: []byte{0x00, 0xb0, 0x03, 0x02, 0x01, 100, 100},
			want:   Status{Levels: Levels{Game: 100, Chat: 100}, Dial: true, Headset: true, Battery: 2, Charging: true},
			ok:     true,
		},
		{
			name:   "arctis 7+ headset off without a dial position",
			family: Arctis7Plus,
			report: []byte{0x00, 0xb0, 0x01, 0x00, 0x00, 0xff, 0xff},
			want:   Status{},
			ok:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.family.ParseStatus(tt.report)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// The same bytes mean different things per family, which is why a dongle
// is parsed with the layout of its own.
func TestFamiliesDisagree(t *testing.T) {
	// A Nova 7 that is on and not charging has state 3 at byte 3; read as
	// an Arctis 7+ that byte would be "charging: no" and byte 1 decides.
	report := []byte{0xb0, 0x01, 0x04, 0x03, 80, 100}
	if st, _ := Nova7.ParseStatus(report); !st.Headset {
		t.Error("Nova 7 layout: headset should be on")
	}
	if st, _ := Arctis7Plus.ParseStatus(report); st.Headset {
		t.Error("Arctis 7+ layout: headset should be off")
	}
}

func TestProductsHaveFamilies(t *testing.T) {
	for id, p := range Products {
		if p.Name == "" || p.Family == nil {
			t.Errorf("product 0x%04x: %+v is incomplete", id, p)
		}
	}
}
