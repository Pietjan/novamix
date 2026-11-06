# novamix

ChatMix for the SteelSeries Arctis Nova 7, Nova 5 and Arctis 7+ on Windows,
without SteelSeries GG or Sonar.

On these headsets the dial is not mixed in the headset: the dongle only
reports the dial position and SteelSeries' software does the mixing. novamix reads that
position straight from the dongle over HID and applies it to the per-app
volumes of the headset's playback device (the same ones in the Windows volume
mixer). Applications on the chat list follow the chat side of the dial, every
other application follows the game side. No driver, no virtual audio devices,
a single exe.

## Supported headsets

| Headset | Status |
| --- | --- |
| Arctis Nova 7, Nova 7 Wireless Gen 2, Nova 7X, Nova 7 Diablo IV and WoW Edition | The Nova 7 (PID 0x2202) is verified on a real headset; the others share its protocol |
| Arctis Nova 5, Nova 5X | Untested: report layout taken from HeadsetControl |
| Arctis 7+, 7+ PS5, 7+ Xbox, 7+ Destiny | Untested: report layout taken from HeadsetControl |

For an untested one, run the console build with `-dry-run -v` first: it logs
the headset going on and off and the dial position without changing any
volume, which shows whether the layout is right. `novamix -list` prints the
product ID of any attached SteelSeries device, supported or not.

Not supported, on purpose: the Nova 7P (its dial sets sidetone), the older
Arctis 7, 9 and Pro (they present a game and a chat playback device and mix
in hardware, so there is nothing to do), and the Nova Pro Wireless (its base
station has to be switched into ChatMix mode first, which is not
implemented).

## Usage

```
novamix [flags]
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `-chat` | Discord, Teams, Slack, Zoom, Telegram, WhatsApp, Signal, Webex, TeamSpeak, Mumble, Element, Skype, Guilded | Comma-separated executables mixed on the chat side. A name without extension means `.exe`. |
| `-device` | `Arctis` | Substring of the playback device's name to mix on. Empty uses the default playback device. |
| `-poll` | `1s` | How often the dongle is asked for its status, which gives the initial dial position and headset on/off. Dial moves are pushed by the dongle instantly regardless. |
| `-refresh` | `2s` | How often the dial is re-applied, so an app started afterwards gets the right volume. |
| `-switch` | `true` | Make the headset the default playback and recording device while it is on, and put the previous defaults back when it goes off or the dongle is unplugged. `-switch=false` leaves the defaults alone. |
| `-fallback` | | Substring of the device to switch to when the headset goes off while it already was the default when novamix started, so there is no previous device to go back to. Without it novamix logs the available devices and leaves the default alone. |
| `-kick` | `true` | After a switch, close the streams of applications still playing on the old device a second later, so they reopen on the new default. Needed for applications that pick their device once and ignore a change of default, as many games do. `-kick=false` leaves them where they are. |
| `-log` | | Append logs to a file instead of stderr. Use this with the console-less build. |
| `-v` | | Debug logging: every volume change and the collections opened on the dongle. |
| `-dump` | | Log every raw HID report. Use it when the dial does nothing, to see what the dongle sends. |

| `-install` | | Copy the exe to `%LOCALAPPDATA%\Programs\novamix`, register a Task Scheduler task that runs it at your logon with the other flags given on the same command line, start it, and exit. |
| `-uninstall` | | Stop and remove that task and the installed copy, and exit. |

Start novamix and it waits for the dongle, follows it through unplugs and
sleeps, and on Ctrl+C puts the per-app volumes and default devices back the
way they were.

To have it run at every logon:

```
novamix.exe -install
```

Any other flag on that command line is baked into the task, for example
`novamix.exe -install -fallback Speakers -chat discord,teams`. The task runs
in your session without elevation, has no time limit, ignores battery state,
restarts on a crash, and logs to `%LOCALAPPDATA%\Programs\novamix\novamix.log`.
Re-run `-install` to update the exe or change flags; `-uninstall` removes it.

Without a terminal: open `novamix.exe` from Explorer. Started without any
flag, the windowless build asks in a dialog whether to install (the same as
`-install` with no other flags) and confirms when it is running; opened
again once installed, it asks whether to remove it. To run that build
without installing, start it with any flag, for example `novamix.exe -v`.
The console build always just runs.

`make build` produces `bin/novamix.exe` without a console window and
`make build/debug` produces `bin/novamix-debug.exe` with one; `make run`,
`make install` and `make uninstall` drive those from WSL2 through Windows
interop.

Both builds carry Windows file properties (description, product, company,
version) and an application manifest, generated from
`cmd/novamix/winres/winres.json` by `make resource`, which the build targets
run first. The exe is unsigned, and an unsigned exe with no properties at all
that starts itself at logon is what antivirus heuristics distrust most; the
properties make it look like the ordinary program it is, without any
guarantee. A plain `go build` on a fresh checkout leaves them out.

GitHub Actions builds every push to `main` and every pull request
(`.github/workflows/build.yml`): it runs `make audit`, then `make build
build/debug`, and uploads both exes as the `novamix` artifact of the run.

Pushing a version tag publishes a release: `make release version=v0.1.0`
tags the commit and pushes the tag, and the same workflow then attaches the
two exes it built and their SHA-256 checksums to a GitHub release named
after the tag, with notes generated from the commits since the previous
one. The tag is also the version the exe reports.

## How it works

- `internal/hid` enumerates HID collections with cfgmgr32 and hid.dll and
  reads/writes raw reports. Pure Go, no cgo.
- `internal/nova` knows the dongles: vendor and product IDs, the `0xb0` status
  request and report, and the pushed `0x45` dial report. The report holds the
  game and chat levels (0-100), the battery and whether the headset is on, at
  offsets that differ per family (game and chat at bytes 4 and 5 on the Nova 7
  and Arctis 7+, at 5 and 6 on the Nova 5). Protocol taken from
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

  Changing the default only moves applications that follow it. Many games
  open the device that is default when their sound starts and stay on it,
  and Windows has no call to move a stream another process opened (the
  per-app output setting of the volume mixer does not move one either).
  Such an application does reopen on the default device when its stream is
  closed under it, so a second after a switch, if something is still
  playing on the device switched away from, novamix sets that device's
  format to the format it already has, through the same `IPolicyConfig`.
  Windows closes every stream on a device whose format is set, even to the
  same one, so the device itself is left exactly as it was. Nothing is
  done when nothing is left playing there.

Known limits:

- Per-app volume is the mechanism, so novamix overwrites what you set in the
  Windows volume mixer for those apps while it runs. Originals are restored on
  a clean exit.
- An app that plays through a different device than the one matched by
  `-device` is not touched.
- The kick closes every stream on the old device, so an application that
  stayed there on purpose and is playing at that moment (one set to that
  device in its own settings) has its sound interrupted for an instant and
  reopens where it was. An application that does not recover from a closed
  stream stays silent until its sound is restarted; `-kick=false` avoids
  both. An application that is not playing at the moment of the switch is
  not moved.
- Device switching relies on an undocumented interface. It has been stable
  since Windows 7, but a Windows update could in principle break it; the rest
  of novamix keeps working if it does.

Verified on an Arctis Nova 7 (PID 0x2202): the dongle pushes a `0x45` dial
report on interface 5 as the dial moves and answers the `0xb0` status request
on interface 3; with the headset off it reports no headset and `0xff` for the
dial, which is ignored. World of Warcraft is the game the kick was made for:
it stays on the old device through a change of default and reopens on the new
one when kicked.
