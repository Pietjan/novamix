# novamix

ChatMix for the SteelSeries Arctis Nova 7 on Windows, without SteelSeries GG
or Sonar.

The Nova 7 dial is not mixed in the headset: the dongle only reports the dial
position and SteelSeries' software does the mixing. novamix reads that
position straight from the dongle over HID and applies it to the per-app
volumes of the headset's playback device (the same ones in the Windows volume
mixer). Applications on the chat list follow the chat side of the dial, every
other application follows the game side. No driver, no virtual audio devices,
a single exe.

## Usage

```
novamix [flags]
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `-chat` | Discord, Teams, Slack, Zoom, Telegram, WhatsApp, Signal, Webex, TeamSpeak, Mumble, Element, Skype, Guilded | Comma-separated executables mixed on the chat side. A name without extension means `.exe`. |
| `-device` | `Arctis Nova` | Substring of the playback device's name to mix on. Empty uses the default playback device. |
| `-poll` | `1s` | How often the dongle is asked for its status, which gives the initial dial position and headset on/off. Dial moves are pushed by the dongle instantly regardless. |
| `-refresh` | `2s` | How often the dial is re-applied, so an app started afterwards gets the right volume. |
| `-switch` | `true` | Make the headset the default playback and recording device while it is on, and put the previous defaults back when it goes off or the dongle is unplugged. `-switch=false` leaves the defaults alone. |
| `-fallback` | | Substring of the device to switch to when the headset goes off while it already was the default when novamix started, so there is no previous device to go back to. Without it novamix logs the available devices and leaves the default alone. |
| `-log` | | Append logs to a file instead of stderr. Use this with the console-less build. |
| `-v` | | Debug logging: every volume change and the collections opened on the dongle. |
| `-dump` | | Log every raw HID report. Use it when the dial does nothing, to see what the dongle sends. |

Leave the headset as the default playback device and start novamix; it waits
for the dongle, follows it through unplugs and sleeps, and on Ctrl+C puts the
per-app volumes back the way they were.

`make build` produces `bin/novamix.exe` without a console window (start it
from Task Scheduler or a Startup shortcut, with `-log`). `make build/debug`
produces `bin/novamix-debug.exe` with a console, and `make run` launches that
from WSL2.

## How it works

- `internal/hid` enumerates HID collections with cfgmgr32 and hid.dll and
  reads/writes raw reports. Pure Go, no cgo.
- `internal/nova` knows the dongle: vendor and product IDs, the `0xb0` status
  request and report (game at byte 4, chat at byte 5, 0-100) and the pushed
  `0x45` dial report. Protocol taken from
  [HeadsetControl](https://github.com/Sapd/HeadsetControl) and the Linux
  `hid-steelseries` ChatMix patches.
- `internal/mixer` drives Core Audio through COM (`IAudioSessionManager2`,
  `ISimpleAudioVolume`): resolves the playback device, classifies each session
  by its process image and sets its master volume.
- `internal/switcher` follows the headset's on/off state from the status
  report and sets the default devices through `IPolicyConfig`, the
  undocumented interface every default-device switcher on Windows uses. It
  remembers what each role (default, multimedia, communications) pointed at
  before and restores exactly that.

Known limits:

- Per-app volume is the mechanism, so novamix overwrites what you set in the
  Windows volume mixer for those apps while it runs. Originals are restored on
  a clean exit.
- An app that plays through a different device than the one matched by
  `-device` is not touched.
- Device switching relies on an undocumented interface. It has been stable
  since Windows 7, but a Windows update could in principle break it; the rest
  of novamix keeps working if it does.

Verified on an Arctis Nova 7 (PID 0x2202): the dongle pushes a `0x45` dial
report on interface 5 as the dial moves and answers the `0xb0` status request
on interface 3; with the headset off it reports no headset and `0xff` for the
dial, which is ignored.
