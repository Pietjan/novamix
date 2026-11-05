package main

import (
	"bytes"
	"encoding/xml"
	"os"
	"strings"
	"time"
	"unicode/utf16"
)

// retryWhile calls fn until it succeeds, fails with an error that
// retryable rejects, or timeout has passed, sleeping interval between
// attempts. It returns fn's last error.
func retryWhile(timeout, interval time.Duration, retryable func(error) bool, fn func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := fn()
		if err == nil || !retryable(err) || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(interval)
	}
}

// hasFlag reports whether args sets the named flag in any spelling.
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		a = strings.TrimLeft(a, "-")
		if a == name || strings.HasPrefix(a, name+"=") {
			return true
		}
	}
	return false
}

// taskXML renders a Task Scheduler definition: run at this user's logon
// in their interactive session, least privilege, never time-limited,
// unaffected by battery power, restarted if it crashes.
func taskXML(username, exe string, args []string) ([]byte, error) {
	type principal struct {
		ID        string `xml:"id,attr"`
		UserID    string `xml:"UserId"`
		LogonType string `xml:"LogonType"`
		RunLevel  string `xml:"RunLevel"`
	}
	type restart struct {
		Interval string `xml:"Interval"`
		Count    int    `xml:"Count"`
	}
	type settings struct {
		MultipleInstancesPolicy    string  `xml:"MultipleInstancesPolicy"`
		DisallowStartIfOnBatteries bool    `xml:"DisallowStartIfOnBatteries"`
		StopIfGoingOnBatteries     bool    `xml:"StopIfGoingOnBatteries"`
		AllowHardTerminate         bool    `xml:"AllowHardTerminate"`
		StartWhenAvailable         bool    `xml:"StartWhenAvailable"`
		RunOnlyIfNetworkAvailable  bool    `xml:"RunOnlyIfNetworkAvailable"`
		AllowStartOnDemand         bool    `xml:"AllowStartOnDemand"`
		Enabled                    bool    `xml:"Enabled"`
		Hidden                     bool    `xml:"Hidden"`
		RunOnlyIfIdle              bool    `xml:"RunOnlyIfIdle"`
		WakeToRun                  bool    `xml:"WakeToRun"`
		ExecutionTimeLimit         string  `xml:"ExecutionTimeLimit"`
		RestartOnFailure           restart `xml:"RestartOnFailure"`
	}
	type exec struct {
		Command   string `xml:"Command"`
		Arguments string `xml:"Arguments"`
	}
	type task struct {
		XMLName     xml.Name  `xml:"Task"`
		Version     string    `xml:"version,attr"`
		XMLNS       string    `xml:"xmlns,attr"`
		Description string    `xml:"RegistrationInfo>Description"`
		LogonUser   string    `xml:"Triggers>LogonTrigger>UserId"`
		Principal   principal `xml:"Principals>Principal"`
		Settings    settings  `xml:"Settings"`
		Exec        exec      `xml:"Actions>Exec"`
	}

	t := task{
		Version:     "1.2",
		XMLNS:       "http://schemas.microsoft.com/windows/2004/02/mit/task",
		Description: "ChatMix for the Arctis Nova 7 without SteelSeries GG",
		LogonUser:   username,
		Principal:   principal{ID: "Author", UserID: username, LogonType: "InteractiveToken", RunLevel: "LeastPrivilege"},
		Settings: settings{
			MultipleInstancesPolicy: "IgnoreNew",
			AllowHardTerminate:      true,
			AllowStartOnDemand:      true,
			Enabled:                 true,
			ExecutionTimeLimit:      "PT0S",
			RestartOnFailure:        restart{Interval: "PT1M", Count: 3},
		},
		Exec: exec{Command: exe, Arguments: joinArgs(args)},
	}

	body, err := xml.MarshalIndent(t, "", "  ")
	if err != nil {
		return nil, err
	}
	// schtasks reads exported-style task files: UTF-16LE with a BOM.
	text := `<?xml version="1.0" encoding="UTF-16"?>` + "\r\n" + string(body) + "\r\n"
	var buf bytes.Buffer
	buf.Write([]byte{0xff, 0xfe})
	for _, c := range utf16.Encode([]rune(text)) {
		buf.WriteByte(byte(c))
		buf.WriteByte(byte(c >> 8))
	}
	return buf.Bytes(), nil
}

// joinArgs quotes arguments the way the Windows command line expects.
func joinArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t\"") {
			a = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		}
		quoted[i] = a
	}
	return strings.Join(quoted, " ")
}

// serviceArgs returns the command-line arguments to pass on to the
// installed task: everything except the install flag itself.
func serviceArgs() []string {
	var out []string
	for _, a := range os.Args[1:] {
		name := strings.TrimLeft(a, "-")
		if name == "install" || strings.HasPrefix(name, "install=") {
			continue
		}
		out = append(out, a)
	}
	return out
}
