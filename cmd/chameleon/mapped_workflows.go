package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func isMappedWorkflow(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[0] {
	case "mfc", "type2":
		switch args[1] {
		case "eload", "esave", "eview", "econfig", "edetect":
			return true
		}
	case "slot":
		switch args[1] {
		case "list", "prng", "openall", "nickname-get", "nickname-set", "nickname-delete":
			return true
		}
	case "settings":
		return args[1] == "view" || args[1] == "configure"
	case "emulator":
		switch args[1] {
		case "config", "configure", "counter", "reset-auth", "password-log":
			return true
		}
	case "lf":
		switch args[1] {
		case "read", "clone", "econfig", "encode", "decode", "formats":
			return true
		}
	}
	return false
}
func fileMemoryFormat(path, format string) (string, error) {
	if format != "" {
		switch format {
		case "hex":
			return "eml", nil
		case "bin", "eml", "json":
			return format, nil
		default:
			return "", fmt.Errorf("unknown memory format; use --format bin|eml|json")
		}
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".bin":
		return "bin", nil
	case ".eml", ".hex":
		return "eml", nil
	case ".json":
		return "json", nil
	}
	return "", fmt.Errorf("unknown file suffix; use --format bin|eml|json")
}
func runMappedWorkflow(args []string) (retErr error) {
	group, action := args[0], args[1]
	f := flag.NewFlagSet(group+" "+action, flag.ContinueOnError)
	port := f.String("port", "", "application USB/BLE/TCP port; otherwise select one USB device")
	timeout := f.Duration("timeout", 5*time.Second, "per-command timeout")
	slot := f.Int("slot", 0, "0=active, or temporarily select slot 1..8 (requires --yes)")
	input := f.String("input", "", "binary/EML/image/config/LF input file")
	output := f.String("output", "", "new atomic output file")
	format := f.String("format", "", "memory format bin|eml|json, or HID format name")
	start := f.Int("start", 0, "first memory unit or password log record")
	limit := f.Int("limit", 10000, "maximum password log records")
	yes := f.Bool("yes", false, "allow explicit changes or temporary slot switching")
	persist := f.Bool("save", false, "save configuration after original active slot is restored")
	value := f.Int64("value", -1, "optional PRNG 0..2 / counter 0..16777215")
	index := f.Int("index", 0, "Type2 counter index 0..2")
	resetTearing := f.Bool("reset-tearing", false, "request counter tearing reset when writing a value")
	name := f.String("name", "", "explicit nickname for nickname-set; UTF-8 <=32 bytes")
	sense := f.String("sense", "hf", "nickname HF/LF")
	protocol := f.String("protocol", "em410x", "LF protocol")
	password := f.Uint64("password", 0, "explicit EM4x05 login password (uint32)")
	newPassword := f.String("new-password", "", "explicit four-byte T55xx password")
	oldPasswords := f.String("old-passwords", "", "comma-separated explicit four-byte old T55xx passwords")
	dataHex := f.String("data", "", "encoded LF bytes for offline decode")
	tagType := f.Int("tag-type", 100, "LF type metadata for decode")
	hfType := f.Int("hf-type", 1001, "openall HF type")
	lfType := f.Int("lf-type", 100, "openall LF type")
	backup := f.String("backup", "", "mandatory new pre-openall backup path")
	plan := f.Bool("plan", false, "print openall plan without accessing hardware")
	secrets := f.Bool("show-secrets", false, "include BLE PIN in settings view")
	if e := f.Parse(args[2:]); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	if f.NArg() != 0 || *timeout <= 0 || *slot < 0 || *slot > 8 || *start < 0 || uint64(*start) > 0xffffffff {
		return fmt.Errorf("invalid arguments")
	}
	if e := checkNewOutput(*output); e != nil {
		return e
	}
	if *output != "" && (sameFilePath(*output, *input) || sameFilePath(*output, *backup)) {
		return fmt.Errorf("output must differ from source and backup files")
	}
	if action == "formats" {
		return result(chameleon.HIDFormats(), *output, false)
	}
	if action == "decode" {
		b, e := decodeHex(*dataHex)
		if e != nil {
			return e
		}
		if *tagType < 0 || *tagType > 65535 {
			return fmt.Errorf("invalid tag type")
		}
		v, e := chameleon.DecodeLFRecord(*protocol, uint16(*tagType), b)
		if e != nil {
			return e
		}
		return result(v, *output, false)
	}
	if action == "openall" {
		if *hfType < 0 || *hfType > 65535 || *lfType < 0 || *lfType > 65535 {
			return fmt.Errorf("invalid slot default type")
		}
		p, e := chameleon.PlanSlotInitialization(uint16(*hfType), uint16(*lfType))
		if e != nil {
			return e
		}
		if *plan {
			return result(p, *output, false)
		}
	}
	writing := action == "eload" || action == "configure" || action == "reset-auth" || action == "openall" || action == "nickname-delete" || action == "nickname-set" || action == "clone" || action == "prng" && *value >= 0 || action == "counter" && *value >= 0 || action == "econfig" && *input != ""
	selecting := *slot != 0 && ((group == "mfc" || group == "type2" || group == "emulator") || group == "lf" && action == "econfig" || action == "prng")
	if (writing || selecting || *persist) && !*yes {
		return fmt.Errorf("operation requires --yes for mutation, temporary slot selection or save")
	}
	if (action == "eload" || action == "configure" || action == "clone") && *input == "" {
		return fmt.Errorf("this action requires --input")
	}
	if action == "esave" && *output == "" {
		return fmt.Errorf("esave requires --output")
	}
	var memoryFormat string
	if (group == "mfc" || group == "type2") && (action == "esave" || action == "eload") {
		path := *output
		if action == "eload" {
			path = *input
		}
		var err error
		memoryFormat, err = fileMemoryFormat(path, *format)
		if err != nil {
			return err
		}
	}
	if action == "openall" {
		if err := checkNewOutput(*backup); err != nil {
			return err
		}
		if *backup == "" {
			return fmt.Errorf("openall overwrites eight slots and requires --backup NEW_FILE")
		}
		if _, e := os.Lstat(*backup); !os.IsNotExist(e) {
			return fmt.Errorf("backup file must not exist")
		}
		if *hfType < 0 || *hfType > 65535 || *lfType < 0 || *lfType > 65535 {
			return fmt.Errorf("invalid tag type")
		}
	}
	if *password > 0xffffffff {
		return fmt.Errorf("password must fit uint32")
	}
	if *value < -1 {
		return fmt.Errorf("value must be -1 (read) or nonnegative")
	}
	var inputBytes []byte
	var e error
	if *input != "" {
		inputBytes, e = readLimitedFile(*input)
		if e != nil {
			return e
		}
	}
	var settingsPatch chameleon.SettingsPatch
	if group == "settings" && action == "configure" {
		decoder := json.NewDecoder(bytes.NewReader(inputBytes))
		decoder.DisallowUnknownFields()
		if e = decoder.Decode(&settingsPatch); e != nil {
			return e
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return fmt.Errorf("settings patch must be exactly one JSON object")
		}
		if e = settingsPatch.Validate(); e != nil {
			return e
		}
		if (strings.HasPrefix(*port, "ble:") || strings.HasPrefix(*port, "tcp:")) && (settingsPatch.BLEKey != nil || settingsPatch.BLEPairing != nil) {
			return fmt.Errorf("pairing changes require a direct serial connection for readback")
		}
	}
	var patch chameleon.EmulatorPatch
	if action == "configure" && group != "settings" || (action == "econfig" && group != "lf" && *input != "") {
		decoder := json.NewDecoder(bytes.NewReader(inputBytes))
		decoder.DisallowUnknownFields()
		if e = decoder.Decode(&patch); e != nil {
			return e
		}
		if group == "mfc" || group == "type2" {
			family := "classic"
			if group == "type2" {
				family = "type2"
			}
			if patch.Family != "" && patch.Family != family {
				return fmt.Errorf("patch family conflicts with command group")
			}
			patch.Family = family
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return fmt.Errorf("patch must contain exactly one JSON object")
		}
		if e = patch.Validate(); e != nil {
			return e
		}
	}
	if group == "lf" && action == "encode" {
		var v chameleon.LFIdentityInput
		if e = json.Unmarshal(inputBytes, &v); e != nil {
			return e
		}
		if !strings.EqualFold(v.Protocol, "ioprox") {
			record, err := chameleon.EncodeLFIdentityOffline(v)
			if err != nil {
				return err
			}
			return result(record, *output, false)
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *port == "" {
		*port, e = autoPort()
		if e != nil {
			return e
		}
	}
	c, e := chameleon.OpenContext(ctx, *port, *timeout)
	if e != nil {
		return e
	}
	defer c.Close()
	defer func() { retErr = reportDeviceOutputError(retErr) }()
	var out any
	if group == "lf" {
		var v chameleon.LFIdentityInput
		if *input != "" {
			if e = json.Unmarshal(inputBytes, &v); e != nil {
				return e
			}
		}
		if action == "encode" {
			out, e = c.EncodeLFIdentity(ctx, v)
		} else if action == "read" {
			hidFormat := byte(0)
			if *format != "" {
				hidFormat, e = chameleon.ParseHIDFormat(*format)
				if e != nil {
					return e
				}
			}
			out, e = c.ReadLFRecord(ctx, *protocol, hidFormat, uint32(*password))
		} else if action == "clone" {
			np, err := decodeHex(*newPassword)
			if err != nil {
				return err
			}
			old := [][]byte{}
			for _, text := range strings.Split(*oldPasswords, ",") {
				if strings.TrimSpace(text) == "" {
					continue
				}
				b, err := decodeHex(text)
				if err != nil {
					return err
				}
				old = append(old, b)
			}
			record, err := c.CloneLF(ctx, v, np, old)
			e = err
			out = map[string]any{"record": record, "device_write_acknowledged": e == nil, "readback_verified": false}
		} else {
			var p *chameleon.LFIdentityInput
			if *input != "" {
				p = &v
			}
			out, e = c.SlotLFIdentity(ctx, *slot, *protocol, p, *persist)
		}
	} else {
		switch action {
		case "list":
			out, e = c.SlotDetails(ctx)
		case "nickname-get", "nickname-set", "nickname-delete":
			s := chameleon.SenseHF
			if strings.EqualFold(*sense, "lf") {
				s = chameleon.SenseLF
			} else if !strings.EqualFold(*sense, "hf") {
				return fmt.Errorf("sense must be hf/lf")
			}
			if *slot == 0 {
				return fmt.Errorf("nickname action requires explicit --slot 1..8")
			}
			if action == "nickname-set" {
				provided := false
				f.Visit(func(v *flag.Flag) {
					if v.Name == "name" {
						provided = true
					}
				})
				if !provided {
					return fmt.Errorf("nickname-set requires explicit --name")
				}
				e = c.UpdateSlotNickname(ctx, *slot, s, name)
			} else if action == "nickname-delete" {
				e = c.UpdateSlotNickname(ctx, *slot, s, nil)
			} else {
				nick, err := c.ReadSlotNickname(ctx, *slot, s)
				out = map[string]any{"nickname": nick, "present": nick != nil}
				e = err
			}
		case "openall":
			snapshot, err := c.BackupDevice(ctx)
			if err != nil {
				return fmt.Errorf("pre-initialization backup failed; initialization not started: %w", err)
			}
			b, err := json.MarshalIndent(snapshot, "", "  ")
			if err != nil {
				return err
			}
			if err = atomicFile(*backup, append(b, '\n')); err != nil {
				return err
			}
			out, e = c.InitializeAllSlots(ctx, uint16(*hfType), uint16(*lfType))
		case "prng":
			var p *byte
			if *value >= 0 {
				if *value > 2 {
					return fmt.Errorf("PRNG must be 0..2")
				}
				v := byte(*value)
				p = &v
			}
			out, e = c.SlotPRNG(ctx, *slot, p, *persist)
		case "view":
			out, e = c.SettingsView(ctx, *secrets)
		case "esave", "eview":
			image, err := c.ExportSlotMemory(ctx, *slot)
			e = err
			out = image
			if e == nil && (group == "mfc" && image.UnitBytes != 16 || group == "type2" && image.UnitBytes != 4) {
				return fmt.Errorf("selected slot memory family does not match command")
			}
			if e == nil && action == "esave" {
				b, err := chameleon.EncodeMemoryFile(image, memoryFormat)
				if err != nil {
					return err
				}
				return saveResultFile(*output, b)
			}
		case "eload":
			family := "classic"
			if group == "type2" {
				family = "type2"
			}
			out, e = c.LoadSlotMemoryFile(ctx, *slot, *start, inputBytes, memoryFormat, family, *persist)
		case "config", "econfig", "configure":
			if group == "settings" {
				out, e = c.ConfigureSettings(ctx, settingsPatch, *persist)
				break
			}
			if *input != "" {
				out, e = c.ConfigureSlotEmulator(ctx, *slot, patch, *persist)
			} else {
				v, err := c.SlotEmulatorConfig(ctx, *slot)
				out, e = v, err
				if e == nil && (group == "mfc" && v.Classic == nil || group == "type2" && v.Type2 == nil) {
					e = fmt.Errorf("slot does not match requested emulator family")
				}
			}
		case "counter":
			var p *uint32
			if *value >= 0 {
				if *value > 0xffffff {
					return fmt.Errorf("counter outside uint24")
				}
				v := uint32(*value)
				p = &v
			}
			out, e = c.SlotCounter(ctx, *slot, *index, p, *resetTearing, *persist)
		case "reset-auth":
			old, err := c.ResetSlotAuthAttempts(ctx, *slot, *persist)
			out = map[string]any{"previous": old, "reset": err == nil}
			e = err
		case "edetect", "password-log":
			if group == "mfc" {
				out, e = c.ClassicDetectionLogs(ctx, *slot, uint32(*start), *limit)
			} else {
				out, e = c.Type2DetectionLogs(ctx, *slot, uint32(*start), *limit)
			}
		default:
			return fmt.Errorf("unsupported workflow action")
		}
	}
	if out != nil {
		return errors.Join(e, result(out, *output, false))
	}
	if e != nil {
		return e
	}
	return result(map[string]any{"ok": true, "operation": group + " " + action}, *output, false)
}
