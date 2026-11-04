package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

// taskName is the Task Scheduler task novamix registers for itself.
const taskName = "novamix"

// installDir is where the running executable is copied to, so the task
// does not depend on wherever the exe was launched from.
func installDir() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return "", fmt.Errorf("LOCALAPPDATA is not set")
	}
	return filepath.Join(base, "Programs", "novamix"), nil
}

// install copies the executable next to its log, registers a logon task
// for the current user that runs it with args, and starts it now.
func install(args []string) error {
	dir, err := installDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	exe := filepath.Join(dir, "novamix.exe")
	logFile := filepath.Join(dir, "novamix.log")

	self, err := os.Executable()
	if err != nil {
		return err
	}
	if !strings.EqualFold(self, exe) {
		// A running copy cannot be overwritten; stop the task first.
		_ = schtasks("/End", "/TN", taskName)
		if err := copyFile(self, exe); err != nil {
			return fmt.Errorf("copying executable: %w", err)
		}
	}

	if !hasFlag(args, "log") {
		args = append(args, "-log", logFile)
	}

	u, err := user.Current()
	if err != nil {
		return err
	}
	definition, err := taskXML(u.Username, exe, args)
	if err != nil {
		return err
	}
	xmlPath := filepath.Join(dir, "task.xml")
	if err := os.WriteFile(xmlPath, definition, 0o644); err != nil {
		return err
	}
	defer os.Remove(xmlPath)

	if err := schtasks("/Create", "/TN", taskName, "/XML", xmlPath, "/F"); err != nil {
		return err
	}
	if err := schtasks("/Run", "/TN", taskName); err != nil {
		return err
	}
	fmt.Printf("installed %s\n  task:  %s (runs at logon)\n  args:  %s\n  log:   %s\n", exe, taskName, strings.Join(args, " "), logFile)
	return nil
}

// uninstall stops and deletes the task and removes the installed copy,
// keeping the log.
func uninstall() error {
	_ = schtasks("/End", "/TN", taskName)
	if err := schtasks("/Delete", "/TN", taskName, "/F"); err != nil {
		return err
	}
	dir, err := installDir()
	if err != nil {
		return err
	}
	exe := filepath.Join(dir, "novamix.exe")
	if self, err := os.Executable(); err == nil && strings.EqualFold(self, exe) {
		fmt.Printf("task %s removed; delete %s yourself, it is the running executable\n", taskName, exe)
		return nil
	}
	if err := os.Remove(exe); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Printf("task %s removed, %s deleted\n", taskName, exe)
	return nil
}

func schtasks(args ...string) error {
	cmd := exec.Command("schtasks.exe", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
