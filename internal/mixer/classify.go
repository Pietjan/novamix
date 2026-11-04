// Package mixer applies a ChatMix dial position to the audio sessions on
// the headset's Windows playback device: sessions owned by chat
// applications follow the chat level, every other session follows the
// game level.
package mixer

import (
	"path"
	"strings"
)

// Channel is the side of the dial a session follows.
type Channel int

const (
	Game Channel = iota
	Chat
)

func (c Channel) String() string {
	if c == Chat {
		return "chat"
	}
	return "game"
}

// DefaultChatApps are the executables treated as chat out of the box.
var DefaultChatApps = []string{
	"discord.exe", "discordptb.exe", "discordcanary.exe", "vesktop.exe",
	"teams.exe", "ms-teams.exe",
	"slack.exe",
	"zoom.exe",
	"telegram.exe",
	"whatsapp.exe",
	"signal.exe",
	"webex.exe", "atmgr.exe",
	"teamspeak.exe", "ts3client_win64.exe", "ts3client_win32.exe",
	"mumble.exe",
	"element.exe",
	"skype.exe",
	"guilded.exe",
}

// Classifier maps a process image path to a channel.
type Classifier struct {
	chat map[string]struct{}
}

// NewClassifier builds a classifier from executable names. Names are
// matched case-insensitively against the base name of the process image;
// a name without an extension matches the ".exe" of the same stem.
func NewClassifier(chatApps []string) *Classifier {
	c := &Classifier{chat: make(map[string]struct{}, len(chatApps))}
	for _, app := range chatApps {
		app = strings.ToLower(strings.TrimSpace(app))
		if app == "" {
			continue
		}
		if path.Ext(app) == "" {
			app += ".exe"
		}
		c.chat[app] = struct{}{}
	}
	return c
}

// Classify returns the channel for a process image path (or bare name).
// An empty path, such as the system sounds session, is Game.
func (c *Classifier) Classify(image string) Channel {
	// Windows paths arrive with backslashes whatever the build host.
	name := strings.ToLower(path.Base(strings.ReplaceAll(image, `\`, "/")))
	if _, ok := c.chat[name]; ok {
		return Chat
	}
	return Game
}

// ParseList splits a comma- or semicolon-separated flag value into names.
func ParseList(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' }) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
