package main

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
	"unicode/utf16"
)

func decodeUTF16(t *testing.T, b []byte) string {
	t.Helper()
	if len(b) < 2 || b[0] != 0xff || b[1] != 0xfe {
		t.Fatalf("missing UTF-16LE BOM: % x", b[:2])
	}
	b = b[2:]
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

func TestTaskXML(t *testing.T) {
	raw, err := taskXML(`DESKTOP\pietjan`, `C:\Users\pietjan\AppData\Local\Programs\novamix\novamix.exe`, []string{"-fallback", "Speakers (Realtek)", "-log", `C:\x\novamix.log`})
	if err != nil {
		t.Fatal(err)
	}
	text := decodeUTF16(t, raw)
	if !strings.HasPrefix(text, `<?xml version="1.0" encoding="UTF-16"?>`) {
		t.Fatalf("bad prolog: %q", text[:60])
	}

	// Round-trip through the decoder to prove it is well-formed and that
	// the elements sit where the Task Scheduler schema expects them.
	var got struct {
		LogonUser string `xml:"Triggers>LogonTrigger>UserId"`
		UserID    string `xml:"Principals>Principal>UserId"`
		LogonType string `xml:"Principals>Principal>LogonType"`
		TimeLimit string `xml:"Settings>ExecutionTimeLimit"`
		Batteries string `xml:"Settings>DisallowStartIfOnBatteries"`
		Restarts  int    `xml:"Settings>RestartOnFailure>Count"`
		Command   string `xml:"Actions>Exec>Command"`
		Arguments string `xml:"Actions>Exec>Arguments"`
		Multiple  string `xml:"Settings>MultipleInstancesPolicy"`
	}
	body := text[strings.Index(text, "?>")+2:]
	dec := xml.NewDecoder(bytes.NewReader([]byte(body)))
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decoding: %v\n%s", err, body)
	}
	checks := map[string][2]string{
		"logon user": {got.LogonUser, `DESKTOP\pietjan`},
		"principal":  {got.UserID, `DESKTOP\pietjan`},
		"logon type": {got.LogonType, "InteractiveToken"},
		"time limit": {got.TimeLimit, "PT0S"},
		"batteries":  {got.Batteries, "false"},
		"command":    {got.Command, `C:\Users\pietjan\AppData\Local\Programs\novamix\novamix.exe`},
		"arguments":  {got.Arguments, `-fallback "Speakers (Realtek)" -log C:\x\novamix.log`},
		"instances":  {got.Multiple, "IgnoreNew"},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", name, c[0], c[1])
		}
	}
	if got.Restarts != 3 {
		t.Errorf("restart count = %d, want 3", got.Restarts)
	}
	if !strings.Contains(body, `<Principal id="Author">`) {
		t.Errorf("principal id attribute missing")
	}
}

func TestHasFlagAndJoinArgs(t *testing.T) {
	if !hasFlag([]string{"-v", "--log=x"}, "log") || hasFlag([]string{"-logfile", "x"}, "log") {
		t.Error("hasFlag")
	}
	if got := joinArgs([]string{"-chat", `a.exe,b.exe`, "-device", `Arctis "Nova"`}); got != `-chat a.exe,b.exe -device "Arctis \"Nova\""` {
		t.Errorf("joinArgs = %q", got)
	}
}
