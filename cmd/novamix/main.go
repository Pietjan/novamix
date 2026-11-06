//go:build windows

// Command novamix turns the ChatMix dial of a SteelSeries Arctis Nova 7,
// Nova 5 or Arctis 7+ into per-application volume on Windows, without
// SteelSeries GG.
//
// It reads the dial over HID from the wireless dongle and sets the volume
// of every audio session on the headset's playback device: sessions of
// chat applications follow the chat side of the dial, everything else the
// game side.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/pietjan/novamix/internal/mixer"
	"github.com/pietjan/novamix/internal/nova"
	"github.com/pietjan/novamix/internal/switcher"
)

var version = "dev"

func main() {
	var (
		chatApps    = flag.String("chat", strings.Join(mixer.DefaultChatApps, ","), "comma-separated executables mixed on the chat side")
		device      = flag.String("device", "Arctis", "substring of the playback device name to mix on; empty for the default device")
		poll        = flag.Duration("poll", time.Second, "how often to ask the dongle for its status (initial dial position, headset on/off); dial moves are pushed regardless")
		refresh     = flag.Duration("refresh", 2*time.Second, "how often to re-apply the dial to audio sessions that appeared since")
		autoSwitch  = flag.Bool("switch", true, "make the headset the default playback and recording device while it is on, and restore the previous ones when it goes off")
		fallback    = flag.String("fallback", "", "substring of the device to switch to when the headset goes off and it already was the default when novamix started")
		kick        = flag.Bool("kick", true, "after switching, close the streams of applications still playing on the old device so they reopen on the new one (games that ignore a change of default device)")
		logPath     = flag.String("log", "", "append log output to this file instead of stderr")
		verbose     = flag.Bool("v", false, "debug logging")
		dump        = flag.Bool("dump", false, "log every raw HID report from the dongle (implies -v)")
		dryRun      = flag.Bool("dry-run", false, "log the volumes that would be set without changing anything")
		list        = flag.Bool("list", false, "list the HID collections of attached SteelSeries devices and exit")
		doInstall   = flag.Bool("install", false, "copy the exe to %LOCALAPPDATA%\\Programs\\novamix, register a logon task that runs it with the other flags given here, start it, and exit")
		doUninstall = flag.Bool("uninstall", false, "stop and remove the logon task and the installed copy, and exit")
		showVersion = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("novamix", version)
		return
	}
	if openedFromExplorer(os.Args[1:], hasConsole()) {
		setup()
		return
	}
	if *doInstall || *doUninstall {
		var err error
		if *doInstall {
			err = install(serviceArgs())
		} else {
			err = uninstall()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	var out io.Writer = os.Stderr
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		out = f
	}
	level := slog.LevelInfo
	if *verbose || *dump {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: level}))
	log.Info("novamix starting", "version", version)

	if *list {
		if err := nova.List(os.Stdout); err != nil {
			log.Error("listing devices", "err", err)
			os.Exit(1)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	levels := make(chan mixer.Levels, 1)
	mixerDone := make(chan error, 1)
	mx := mixer.New(log, mixer.NewClassifier(mixer.ParseList(*chatApps)), *device, *dryRun)
	go func() { mixerDone <- mx.Run(ctx, levels, *refresh) }()

	headset := make(chan bool, 1)
	switchDone := make(chan error, 1)
	if *autoSwitch && !*dryRun {
		sw := switcher.New(log, *device, *fallback, *kick)
		go func() { switchDone <- sw.Run(ctx, headset) }()
	} else {
		switchDone <- nil
	}

	// Latest value wins on both channels: a stale one still queued is
	// worthless.
	onDial := func(l nova.Levels) {
		log.Info("dial", "game", l.Game, "chat", l.Chat)
		select {
		case <-levels:
		default:
		}
		levels <- mixer.Levels{Game: l.Game, Chat: l.Chat}
	}
	onHeadset := func(on bool) {
		select {
		case <-headset:
		default:
		}
		headset <- on
	}

	runDevices(ctx, log, *poll, *dump, onDial, onHeadset)

	stop()
	failed := false
	for _, done := range []chan error{mixerDone, switchDone} {
		if err := <-done; err != nil {
			log.Error("exiting", "err", err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

// runDevices keeps a dongle open until ctx ends, waiting for one to be
// plugged in and reopening after it disappears. A dongle going away
// counts as the headset going off.
func runDevices(ctx context.Context, log *slog.Logger, poll time.Duration, dump bool, onDial func(nova.Levels), onHeadset func(bool)) {
	const retry = 3 * time.Second
	waiting := false

	for ctx.Err() == nil {
		dev, err := nova.Find(log)
		if err != nil {
			if !waiting || !errors.Is(err, nova.ErrNotFound) {
				log.Info("waiting for dongle", "err", err)
			}
			waiting = true
			sleep(ctx, retry)
			continue
		}
		waiting = false
		log.Info("dongle connected", "device", dev.Name)

		err = dev.Run(ctx, poll, dump, log, onDial, onHeadset)
		dev.Close()
		onHeadset(false)
		if err != nil {
			log.Warn("dongle lost", "err", err)
			sleep(ctx, retry)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
