package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func runBackup(args []string) (retErr error) {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Println("Actions: create|inspect|restore")
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("backup actions: create, inspect, restore")
	}
	action := args[0]
	f := flag.NewFlagSet("backup", flag.ContinueOnError)
	input := f.String("input", "", "backup JSON")
	output := f.String("output", "", "new backup/result file")
	rollback := f.String("rollback-output", "", "mandatory pre-restore snapshot path")
	port := f.String("port", "", "explicit USB serial or TCP application port (Python bridge for BLE)")
	yes := f.Bool("yes", false, "allow temporary slot switching or full restore")
	commit := f.Bool("commit", false, "explicit final save of slot configuration and settings")
	timeout := f.Duration("timeout", 5*time.Second, "per-command timeout")
	if e := f.Parse(args[1:]); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if e := checkNewOutput(*output); e != nil {
		return e
	}
	var b chameleon.DeviceBackup
	if action == "inspect" || action == "restore" {
		data, e := readLimitedFile(*input)
		if e != nil {
			return e
		}
		if e = json.Unmarshal(data, &b); e != nil {
			return e
		}
		if e = b.Validate(); e != nil {
			return e
		}
		if action == "inspect" {
			plan, e := b.RestorePlan()
			if e != nil {
				return e
			}
			return result(plan, *output, false)
		}
	}
	if action != "create" && action != "restore" {
		return fmt.Errorf("unknown backup action")
	}
	if !*yes || *port == "" || *timeout <= 0 {
		return fmt.Errorf("backup requires --yes, explicit --port and positive --timeout; slot switches may save existing RAM to flash")
	}
	if action == "create" && *output == "" {
		return fmt.Errorf("create requires --output; backup includes card keys and BLE PIN")
	}
	if action == "restore" {
		if strings.HasPrefix(*port, "ble:") || strings.HasPrefix(*port, "tcp:") {
			return fmt.Errorf("restore includes BLE pairing settings; use a direct serial connection")
		}
		if *rollback == "" || sameFilePath(*rollback, *input) || sameFilePath(*rollback, *output) || sameFilePath(*output, *input) {
			return fmt.Errorf("restore requires a separate --rollback-output file")
		}
		if e := checkNewOutput(*rollback); e != nil {
			return fmt.Errorf("rollback output: %w", e)
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	c, e := chameleon.OpenContext(ctx, *port, *timeout)
	if e != nil {
		return e
	}
	defer c.Close()
	defer func() { retErr = reportDeviceOutputError(retErr) }()
	if action == "create" {
		b, e = c.BackupDevice(ctx)
		if e != nil {
			return e
		}
		data, e := json.MarshalIndent(b, "", "  ")
		if e != nil {
			return e
		}
		return saveResultFile(*output, append(data, '\n'))
	}
	original, e := c.BackupDevice(ctx)
	if e != nil {
		return fmt.Errorf("pre-restore backup failed; restore not started: %w", e)
	}
	data, e := json.MarshalIndent(original, "", "  ")
	if e != nil {
		return e
	}
	if e = saveResultFile(*rollback, append(data, '\n')); e != nil {
		return fmt.Errorf("rollback could not be saved; restore not started: %w", e)
	}
	v, e := c.RestoreDevice(ctx, b, chameleon.RestoreOptions{Persist: *commit})
	return errors.Join(e, result(v, *output, false))
}
