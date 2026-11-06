package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"strings"
	"testing"
	"time"
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

func TestRetryWhile(t *testing.T) {
	busy := errors.New("busy")
	fatal := errors.New("fatal")
	isBusy := func(err error) bool { return errors.Is(err, busy) }

	// Succeeds once the file is released.
	calls := 0
	err := retryWhile(time.Second, time.Millisecond, isBusy, func() error {
		calls++
		if calls < 4 {
			return busy
		}
		return nil
	})
	if err != nil || calls != 4 {
		t.Errorf("released after 3 busy: err = %v, calls = %d, want nil, 4", err, calls)
	}

	// An error that is not "busy" is returned at once.
	calls = 0
	err = retryWhile(time.Second, time.Millisecond, isBusy, func() error { calls++; return fatal })
	if !errors.Is(err, fatal) || calls != 1 {
		t.Errorf("fatal error: err = %v, calls = %d, want fatal, 1", err, calls)
	}

	// Still busy at the deadline: gives up with the busy error.
	calls = 0
	start := time.Now()
	err = retryWhile(30*time.Millisecond, 5*time.Millisecond, isBusy, func() error { calls++; return busy })
	if !errors.Is(err, busy) || calls < 2 {
		t.Errorf("never released: err = %v, calls = %d, want busy after several calls", err, calls)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("gave up after %v, want about 30ms", elapsed)
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

func TestOpenedFromExplorer(t *testing.T) {
	tests := []struct {
		args    []string
		console bool
		want    bool
	}{
		{nil, false, true},                    // double-click on the windowless build
		{nil, true, false},                    // console build started bare
		{[]string{"-v"}, false, false},        // any flag runs it as usual
		{[]string{"-log", "x"}, false, false}, // the logon task
	}
	for _, tt := range tests {
		if got := openedFromExplorer(tt.args, tt.console); got != tt.want {
			t.Errorf("openedFromExplorer(%q, %v) = %v, want %v", tt.args, tt.console, got, tt.want)
		}
	}
}
