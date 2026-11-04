package mixer

import "testing"

func TestClassify(t *testing.T) {
	c := NewClassifier([]string{"Discord.exe", " teams ", "", "ts3client_win64.exe"})

	tests := []struct {
		image string
		want  Channel
	}{
		{`C:\Users\me\AppData\Local\Discord\app-1.0.9\Discord.exe`, Chat},
		{`C:\Program Files\Teams\teams.exe`, Chat},
		{`C:\TeamSpeak\ts3client_win64.exe`, Chat},
		{`C:\Games\Doom\DOOMEternalx64vk.exe`, Game},
		{`C:\Users\me\discord-helper.exe`, Game},
		{"", Game},
		{"discord.exe", Chat},
	}
	for _, tt := range tests {
		if got := c.Classify(tt.image); got != tt.want {
			t.Errorf("Classify(%q) = %v, want %v", tt.image, got, tt.want)
		}
	}
}

func TestParseList(t *testing.T) {
	got := ParseList(" a.exe, b ;c.exe,,")
	want := []string{"a.exe", "b", "c.exe"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
