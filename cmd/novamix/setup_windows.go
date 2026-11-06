package main

import (
	"golang.org/x/sys/windows"
)

// idYes is what MessageBox returns for the Yes button.
const idYes = 6

var procGetConsoleWindow = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")

// hasConsole reports whether the process has a console to print to. The
// windowless build has none, however it was started.
func hasConsole() bool {
	window, _, _ := procGetConsoleWindow.Call()
	return window != 0
}

// setup is what opening the exe from Explorer does: nobody is there to
// pass -install or read its output, so it asks in a dialog whether to
// install, or to remove novamix when it is installed already, and says
// how that went.
func setup() {
	if installed() {
		if !ask("novamix is already installed and starts by itself when you sign in to Windows.\n\n"+
			"Do you want to remove it?", windows.MB_DEFBUTTON2) {
			return
		}
		if err := uninstall(); err != nil {
			tell("Removing novamix failed.\n\n"+err.Error(), windows.MB_ICONERROR)
			return
		}
		tell("novamix has been removed and no longer starts when you sign in.", windows.MB_ICONINFORMATION)
		return
	}

	if !ask("Install novamix "+version+"?\n\n"+
		"It makes the ChatMix dial of a SteelSeries Arctis Nova 7, Nova 5 or Arctis 7+ work without SteelSeries GG. "+
		"It runs in the background, without a window, and starts by itself when you sign in to Windows.", 0) {
		return
	}
	if err := install(nil); err != nil {
		tell("Installing novamix failed.\n\n"+err.Error(), windows.MB_ICONERROR)
		return
	}
	tell("novamix is installed and running.\n\n"+
		"You can delete the file you just opened. To remove novamix later, open that file again.",
		windows.MB_ICONINFORMATION)
}

// installed reports whether the logon task exists.
func installed() bool {
	return schtasks("/Query", "/TN", taskName) == nil
}

// ask shows a Yes/No question and reports whether the answer was Yes.
func ask(text string, flags uint32) bool {
	return messageBox(text, windows.MB_YESNO|windows.MB_ICONQUESTION|flags) == idYes
}

// tell shows a message with an OK button.
func tell(text string, icon uint32) {
	messageBox(text, windows.MB_OK|icon)
}

func messageBox(text string, flags uint32) int32 {
	t, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return 0
	}
	caption, _ := windows.UTF16PtrFromString("novamix")
	ret, _ := windows.MessageBox(0, t, caption, flags|windows.MB_SETFOREGROUND)
	return ret
}
