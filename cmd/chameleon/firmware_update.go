package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// newFirmwareUpdater is replaced in tests so no test reaches the network.
var newFirmwareUpdater = chameleon.NewFirmwareUpdater

// isFirmwareUpdate routes the GUI "check for updates" and firmware download;
// inspect, enter and flash stay with the extended commands.
func isFirmwareUpdate(args []string) bool {
	return len(args) >= 2 && args[0] == "firmware" && (args[1] == "check" || args[1] == "download")
}

func runFirmwareUpdate(args []string) (retErr error) {
	action := args[1]
	f := flag.NewFlagSet("firmware "+action, flag.ContinueOnError)
	model := f.String("model", "", "ultra or lite; read from the device when omitted")
	version := f.String("version", "", "check: device git version, e.g. v2.2.0-64-g5c99d4a; read from the device when omitted")
	source := f.String("source", "auto", "download: auto, release (github.com) or actions (via nightly.link)")
	tag := f.String("tag", "", "download: a specific release tag, e.g. v2.3.0, instead of the latest build")
	output := f.String("output", "", "check: optional result file; download: new firmware ZIP (never overwritten)")
	port := f.String("port", "", "application USB/BLE/TCP port; otherwise select one USB device")
	timeout := f.Duration("timeout", 5*time.Second, "per-command device timeout")
	if e := f.Parse(args[2:]); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if *timeout <= 0 {
		return fmt.Errorf("invalid timeout")
	}
	if e := checkNewOutput(*output); e != nil {
		return e
	}
	if action == "download" {
		if *output == "" {
			return fmt.Errorf("firmware download requires --output for the new ZIP")
		}
		if *version != "" {
			return fmt.Errorf("--version applies to firmware check")
		}
		switch *source {
		case "auto", "release", "actions":
		default:
			return fmt.Errorf("--source must be auto, release or actions")
		}
		if *tag != "" && *source == "actions" {
			return fmt.Errorf("--tag selects a release; it cannot be combined with --source actions")
		}
	} else if *tag != "" || *source != "auto" {
		return fmt.Errorf("--source and --tag apply to firmware download")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	// The device is only read, and only for what the flags left open.
	if *model == "" || action == "check" && *version == "" {
		if *port == "" {
			var e error
			if *port, e = autoPort(); e != nil {
				return fmt.Errorf("%w; or pass --model (and --version for check) to work without a device", e)
			}
		}
		c, e := chameleon.OpenContext(ctx, *port, *timeout)
		if e != nil {
			return e
		}
		if *model == "" {
			_, *model, e = c.Model(ctx)
		}
		if e == nil && action == "check" && *version == "" {
			*version, e = c.GitVersion(ctx)
		}
		if closeErr := c.Close(); e == nil {
			e = closeErr
		}
		if e != nil {
			return e
		}
	}
	name, e := chameleon.FirmwareModel(*model)
	if e != nil {
		return e
	}
	u := newFirmwareUpdater()
	if action == "check" {
		v, e := u.CheckFirmwareUpdate(ctx, name, *version)
		if e != nil {
			return e
		}
		return result(v, *output, false)
	}
	var build chameleon.FirmwareBuild
	switch {
	case *tag != "" || *source == "release":
		build, e = u.ReleaseBuild(ctx, name, *tag)
	case *source == "actions":
		build, e = u.ActionsBuild(ctx, name)
	default:
		build, e = u.LatestBuild(ctx, name)
	}
	if e != nil {
		return e
	}
	fmt.Fprintf(os.Stderr, "Downloading %s (%s, commit %s) from %s\n", build.Asset, build.Source, build.Commit, build.URL)
	v, e := u.DownloadFirmware(ctx, build, *output)
	if e != nil {
		return e
	}
	return result(v, "", false)
}
