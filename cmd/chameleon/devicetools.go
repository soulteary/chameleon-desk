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

// isDeviceTool routes the two device-backed GUI tools that write physical
// cards: Gen4 (Ultimate Magic Card) configuration and the T55xx password
// cleaner. Every state-changing action requires --yes.
func isDeviceTool(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[0] {
	case "gen4":
		switch args[1] {
		case "config", "version", "dump", "write-block", "write-config", "set-uid", "set-password":
			return true
		}
	case "gen1a":
		switch args[1] {
		case "detect", "read", "write-block", "set-uid":
			return true
		}
	case "gen3":
		switch args[1] {
		case "detect", "block0", "set-uid", "lock":
			return true
		}
	case "t55":
		return args[1] == "clean"
	}
	return false
}

func runDeviceTool(args []string) (retErr error) {
	group, action := args[0], args[1]
	f := flag.NewFlagSet(group+" "+action, flag.ContinueOnError)
	// Common flags, then only the current group's flags, so --help lists just
	// what this command accepts instead of every device tool's options.
	output := f.String("output", "", "atomic JSON output file")
	port := f.String("port", "", applicationPortHelp)
	timeout := f.Duration("timeout", 5*time.Second, "per-command timeout")
	yes := f.Bool("yes", false, "confirm writing the card")
	var password, newPassword, uid, data, config, probeID, dictionary, passwords *string
	var block, first, count, maxAttempts *int
	var permanent, permanentLock *bool
	switch group {
	case "gen4":
		password = f.String("password", "00000000", "4-byte access password (hex)")
		newPassword = f.String("new-password", "", "set-password: new 4-byte password (hex)")
		uid = f.String("uid", "", "set-uid: new UID hex")
		block = f.Int("block", 0, "write-block: block 0..255")
		first = f.Int("first", 0, "dump: first block")
		count = f.Int("count", 64, "dump: block count")
		data = f.String("data", "", "write-block: 16-byte block hex")
		config = f.String("config", "", "write-config: 30-byte configuration hex")
		permanent = f.Bool("permanent", false, "write-config/set-password: make it non-volatile (command F1)")
	case "gen1a":
		block = f.Int("block", 0, "read/write-block: block 0..255")
		data = f.String("data", "", "write-block: 16-byte block hex")
		uid = f.String("uid", "", "set-uid: new 4- or 7-byte UID hex")
	case "gen3":
		data = f.String("data", "", "block0: 16-byte block 0 hex")
		uid = f.String("uid", "", "set-uid: new 4- or 7-byte UID hex")
		permanentLock = f.Bool("permanent-lock", false, "lock: required extra confirmation; the UID lock is irreversible")
	case "t55":
		probeID = f.String("probe-id", "", "clean: 5-byte EM410x probe ID written on a correct password")
		newPassword = f.String("new-password", "", "clean: new 4-byte password (0 clears protection)")
		dictionary = f.String("dictionary", "", "clean: GUI dictionary JSON of 4-byte passwords")
		passwords = f.String("passwords", "", "clean: comma-separated 4-byte passwords instead of --dictionary")
		maxAttempts = f.Int("max-attempts", 0, "clean: stop after this many candidates (0 = all)")
	}
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
	readOnly := action == "detect" || action == "read" || action == "config" || action == "version" || action == "dump"
	writes := !readOnly
	if writes && !*yes {
		return fmt.Errorf("%s %s writes the card; review the parameters, then pass --yes", group, action)
	}
	if group == "gen3" && action == "lock" && !*permanentLock {
		return fmt.Errorf("gen3 lock permanently freezes the UID and cannot be undone; pass --permanent-lock as well to proceed")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	p := *port
	if p == "" {
		var e error
		if p, e = autoPort(); e != nil {
			return e
		}
	}
	c, e := chameleon.OpenContext(ctx, p, *timeout)
	if e != nil {
		return e
	}
	defer c.Close()
	defer func() { retErr = reportDeviceOutputError(retErr) }()
	switch group {
	case "gen4":
		return runGen4(ctx, c, action, gen4Flags{*password, *newPassword, *uid, *block, *first, *count, *data, *config, *permanent}, *output)
	case "gen1a":
		return runGen1A(ctx, c, action, *block, *data, *uid, *output)
	case "gen3":
		return runGen3(ctx, c, action, *data, *uid, *output)
	}
	return runT55Clean(ctx, c, *probeID, *newPassword, *dictionary, *passwords, *maxAttempts, *output)
}

func runGen1A(ctx context.Context, c *chameleon.Client, action string, block int, dataHex, uidHex, output string) error {
	switch action {
	case "detect":
		magic, e := c.DetectGen1A(ctx)
		if e != nil {
			return e
		}
		return result(map[string]any{"gen1a_magic": magic}, output, false)
	case "read":
		v, e := c.Gen1AReadBlock(ctx, block)
		if e != nil {
			return e
		}
		return result(map[string]any{"block": block, "data": v.String()}, output, false)
	case "write-block":
		d, e := decodeHex(dataHex)
		if e != nil || len(d) != 16 {
			return fmt.Errorf("--data must be 16 hex bytes")
		}
		if e := c.Gen1AWriteBlock(ctx, block, d); e != nil {
			return e
		}
		return result(map[string]any{"ok": true, "operation": "gen1a write-block", "block": block}, output, false)
	case "set-uid":
		u, e := decodeHex(uidHex)
		if e != nil || (len(u) != 4 && len(u) != 7) {
			return fmt.Errorf("--uid must be 4 or 7 hex bytes")
		}
		if e := c.Gen1ASetUID(ctx, u); e != nil {
			return e
		}
		return result(map[string]any{"ok": true, "operation": "gen1a set-uid", "uid": strings.ToUpper(uidHex)}, output, false)
	}
	return fmt.Errorf("gen1a actions: detect,read,write-block,set-uid")
}

func runGen3(ctx context.Context, c *chameleon.Client, action, dataHex, uidHex, output string) error {
	switch action {
	case "detect":
		classic, e := c.DetectGen3(ctx)
		if e != nil {
			return e
		}
		return result(map[string]any{"classic_readable": classic, "note": "Gen3 cannot be positively identified without writing"}, output, false)
	case "block0":
		d, e := decodeHex(dataHex)
		if e != nil || len(d) != 16 {
			return fmt.Errorf("--data must be 16 hex bytes")
		}
		if e := c.Gen3WriteBlock0(ctx, d); e != nil {
			return e
		}
		return result(map[string]any{"ok": true, "operation": "gen3 block0"}, output, false)
	case "set-uid":
		u, e := decodeHex(uidHex)
		if e != nil || (len(u) != 4 && len(u) != 7) {
			return fmt.Errorf("--uid must be 4 or 7 hex bytes")
		}
		readBack, e := c.Gen3SetUID(ctx, u)
		if e != nil {
			return e
		}
		return result(map[string]any{"ok": true, "operation": "gen3 set-uid", "uid": readBack.String()}, output, false)
	case "lock":
		if e := c.Gen3Lock(ctx); e != nil {
			return e
		}
		return result(map[string]any{"ok": true, "operation": "gen3 lock", "warning": "UID permanently locked"}, output, false)
	}
	return fmt.Errorf("gen3 actions: detect,block0,set-uid,lock")
}

type gen4Flags struct {
	password, newPassword, uid string
	block, first, count        int
	data, config               string
	permanent                  bool
}

func runGen4(ctx context.Context, c *chameleon.Client, action string, fl gen4Flags, output string) error {
	pw, e := decodeHex(fl.password)
	if e != nil || len(pw) != 4 {
		return fmt.Errorf("--password must be 4 hex bytes")
	}
	switch action {
	case "config":
		v, e := c.Gen4ReadConfig(ctx, pw)
		if e != nil {
			return e
		}
		return result(v, output, false)
	case "version":
		v, e := c.Gen4ReadVersion(ctx, pw)
		if e != nil {
			return e
		}
		return result(map[string]any{"version": v.String()}, output, false)
	case "dump":
		v, e := c.Gen4Dump(ctx, pw, fl.first, fl.count)
		if e != nil {
			return e
		}
		rows := make([]string, len(v))
		for i, b := range v {
			rows[i] = b.String()
		}
		return result(map[string]any{"first": fl.first, "count": len(rows), "blocks": rows}, output, false)
	case "write-block":
		d, e := decodeHex(fl.data)
		if e != nil || len(d) != 16 {
			return fmt.Errorf("--data must be 16 hex bytes")
		}
		if e := c.Gen4WriteBlock(ctx, pw, fl.block, d); e != nil {
			return e
		}
		return result(map[string]any{"ok": true, "operation": "gen4 write-block", "block": fl.block}, output, false)
	case "write-config":
		cfg, e := decodeHex(fl.config)
		if e != nil {
			return fmt.Errorf("--config must be hex")
		}
		if e := c.Gen4WriteConfig(ctx, pw, cfg, fl.permanent); e != nil {
			return e
		}
		return result(map[string]any{"ok": true, "operation": "gen4 write-config", "permanent": fl.permanent}, output, false)
	case "set-uid":
		u, e := decodeHex(fl.uid)
		if e != nil {
			return fmt.Errorf("--uid must be hex")
		}
		if e := c.Gen4SetUID(ctx, pw, u); e != nil {
			return e
		}
		return result(map[string]any{"ok": true, "operation": "gen4 set-uid", "uid": strings.ToUpper(fl.uid)}, output, false)
	case "set-password":
		np, e := decodeHex(fl.newPassword)
		if e != nil || len(np) != 4 {
			return fmt.Errorf("--new-password must be 4 hex bytes")
		}
		if e := c.Gen4SetPassword(ctx, pw, np, fl.permanent); e != nil {
			return e
		}
		return result(map[string]any{"ok": true, "operation": "gen4 set-password", "permanent": fl.permanent}, output, false)
	}
	return fmt.Errorf("gen4 actions: config,version,dump,write-block,write-config,set-uid,set-password")
}

func runT55Clean(ctx context.Context, c *chameleon.Client, probeHex, newHex, dictPath, passwordsCSV string, maxAttempts int, output string) error {
	probe, e := decodeHex(probeHex)
	if e != nil || len(probe) != 5 {
		return fmt.Errorf("--probe-id must be a 5-byte EM410x ID in hex")
	}
	np, e := decodeHex(newHex)
	if e != nil || len(np) != 4 {
		return fmt.Errorf("--new-password must be 4 hex bytes (0 clears protection)")
	}
	candidates, e := t55Candidates(dictPath, passwordsCSV)
	if e != nil {
		return e
	}
	if maxAttempts > 0 && maxAttempts < len(candidates) {
		candidates = candidates[:maxAttempts]
	}
	progress := func(attempt int, password []byte) error {
		fmt.Fprintf(os.Stderr, "\rTrying password %d/%d...", attempt, len(candidates))
		return nil
	}
	v, e := c.CleanT55Password(ctx, candidates, probe, np, true, progress)
	fmt.Fprintln(os.Stderr)
	if e != nil {
		return e
	}
	if v.Found {
		fmt.Fprintf(os.Stderr, "Password found after %d attempts; card now uses the new password\n", v.Attempts)
	} else {
		fmt.Fprintf(os.Stderr, "No password matched after %d attempts\n", v.Attempts)
	}
	return result(v, output, false)
}

func t55Candidates(dictPath, passwordsCSV string) ([][]byte, error) {
	var candidates [][]byte
	if passwordsCSV != "" {
		for _, p := range strings.Split(passwordsCSV, ",") {
			b, e := decodeHex(strings.TrimSpace(p))
			if e != nil || len(b) != 4 {
				return nil, fmt.Errorf("each --passwords entry must be 4 hex bytes")
			}
			candidates = append(candidates, b)
		}
		return candidates, nil
	}
	if dictPath == "" {
		return nil, fmt.Errorf("t55 clean needs --dictionary DICT.json or --passwords A,B,...")
	}
	b, e := readDictionaryFile(dictPath)
	if e != nil {
		return nil, e
	}
	var d chameleon.GUIDictionary
	if e = json.Unmarshal(b, &d); e != nil {
		return nil, e
	}
	for _, k := range d.Keys {
		if len(k) != 4 {
			return nil, fmt.Errorf("dictionary %q holds %d-digit keys; T55xx passwords are 8 hex digits", d.Name, d.KeyLength)
		}
		candidates = append(candidates, append([]byte(nil), k...))
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("dictionary has no passwords")
	}
	return candidates, nil
}
