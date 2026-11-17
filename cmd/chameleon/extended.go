package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
	"github.com/soulteary/chameleon-desk/dfu"
)

func extendedCommand(s string) bool {
	switch s {
	case "commands", "call", "settings", "slot", "mode", "emulator", "lf", "mfc", "type2", "raw", "apdu", "doctor", "recover", "image", "sniff", "firmware", "t55", "ble", "usb":
		return true
	}
	return false
}
func decodeHex(s string) ([]byte, error) {
	s = strings.NewReplacer(" ", "", ":", "", "\n", "", "\t", "", "\r", "").Replace(strings.TrimSpace(s))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	b, e := hex.DecodeString(s)
	if e != nil {
		return nil, fmt.Errorf("invalid hexadecimal data: %w", e)
	}
	return b, nil
}
func atomicFile(path string, data []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".chameleon-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, e = f.Write(data); e != nil {
		_ = f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		_ = f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	// Publish without replacing an existing path, including symlinks.
	if e = os.Link(tmp, path); e != nil {
		return e
	}
	return syncOutputDirectory(path)
}
func result(v any, output string, binary bool) error {
	var data []byte
	var e error
	if binary {
		switch b := v.(type) {
		case chameleon.HexBytes:
			data = b
		case []byte:
			data = b
		default:
			return fmt.Errorf("binary output requires byte data")
		}
	} else {
		data, e = json.MarshalIndent(v, "", "  ")
		if e != nil {
			return e
		}
		data = append(data, '\n')
	}
	if output != "" {
		if e = saveResultFile(output, data); e != nil {
			return e
		}
	}
	if !binary || output == "" {
		if binary {
			_, e = os.Stdout.Write(data)
		} else {
			_, e = os.Stdout.Write(data)
		}
	} else {
		fmt.Fprintf(os.Stderr, "Saved %d bytes to %s\n", len(data), output)
	}
	if e != nil {
		return &outputSaveError{"stdout", e}
	}
	return nil
}
func runExtended(args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return runExtendedWithContext(ctx, args, func(p string, t time.Duration) (*chameleon.Client, error) { return chameleon.OpenContext(ctx, p, t) })
}
func runExtendedWith(args []string, openDevice func(string, time.Duration) (*chameleon.Client, error)) error {
	return runExtendedWithContext(context.Background(), args, openDevice)
}
func runExtendedWithContext(ctx context.Context, args []string, openDevice func(string, time.Duration) (*chameleon.Client, error)) (retErr error) {
	group := args[0]
	rest := args[1:]
	action := "get"
	switch group {
	case "mode":
		action = "set"
	case "lf":
		action = "scan"
	case "raw", "apdu":
		action = "exchange"
	case "t55":
		action = "write"
	case "sniff":
		action = "capture"
	}
	needsAction := group == "settings" || group == "slot" || group == "emulator" || group == "mfc" || group == "type2" || group == "call" || group == "image" || group == "firmware" || group == "ble" || group == "usb"
	if needsAction && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		action = rest[0]
		rest = rest[1:]
	}
	f := flag.NewFlagSet(group, flag.ContinueOnError)
	port := f.String("port", "", "tcp:HOST:PORT; --native-usb requires a local maintenance port; firmware flash uses a physical DFU port")
	nativeUSB := f.Bool("native-usb", false, "explicit native USB maintenance; pairing/reset/bootloader actions only")
	defaultTimeout := 3 * time.Second
	if group == "ble" || group == "usb" {
		defaultTimeout = 30 * time.Second
	}
	if group == "mfc" && (action == "recover" || action == "senested" || action == "collect") {
		defaultTimeout = 40 * time.Second
	}
	timeout := f.Duration("timeout", defaultTimeout, "per-command timeout; increase for sniff/acquisition")
	jsonOut := f.Bool("json", false, "JSON output (extended commands always emit JSON)")
	_ = jsonOut
	output := f.String("output", "", "output file (written atomically)")
	input := f.String("input", "", "binary payload / image input file")
	dataHex := f.String("data", "", "hexadecimal payload")
	binaryOut := f.Bool("binary", false, "save/output raw bytes for reads or dumps")
	yes := f.Bool("yes", false, "execute an explicit state-changing/card-writing operation")
	var slot, tag, atqa, sak, start, count, block, value, format int
	var sense, kind, name, keyText, keyType, uid, ats, protocol string
	var enabled, long, showSecrets, supported, force bool
	var describe, newPassword, oldPasswords string
	var strategy string
	var sectors int
	var maxRuns, maxAttempts int
	var slow bool
	var targetBlock int
	var targetType string
	var readerMode bool
	var rfTimeout time.Duration
	var bridgeURL, bridgeTokenFile string
	var appendCRC, autoSelect, keepField, checkCRC bool
	switch group {
	case "commands":
		f.StringVar(&describe, "describe", "", "show upstream parameters, formats and source locations for one command")
		f.BoolVar(&supported, "supported", false, "query and filter by connected device capability list")
	case "call":
		f.BoolVar(&readerMode, "reader", false, "temporarily use reader mode and restore it afterwards")
		f.BoolVar(&force, "force", false, "skip runtime capability preflight (catalog still required)")
	case "settings":
		f.StringVar(&name, "name", "", "animation|sleep|long-press|button|ble-key|ble-pairing")
		f.StringVar(&keyText, "key", "", "BLE PIN (six digits)")
		f.IntVar(&value, "value", 0, "setting value")
		f.StringVar(&kind, "button", "A", "button A/B")
		f.BoolVar(&long, "long", false, "long button press")
		f.BoolVar(&enabled, "enabled", false, "pairing enabled")
		f.BoolVar(&showSecrets, "show-secrets", false, "include BLE PIN and raw settings")
	case "slot":
		f.IntVar(&slot, "slot", 1, "slot number 1..8")
		f.IntVar(&tag, "type", 1001, "tag type ID")
		f.StringVar(&sense, "sense", "hf", "hf or lf")
		f.BoolVar(&enabled, "enabled", true, "slot enable state")
		f.StringVar(&name, "name", "", "UTF-8 nickname, at most 32 bytes")
	case "mode":
		f.BoolVar(&enabled, "reader", false, "set reader mode; false means emulator")
	case "emulator":
		f.StringVar(&kind, "kind", "classic", "classic or type2")
		f.IntVar(&start, "start", 0, "first block/page")
		f.IntVar(&count, "count", 1, "number of blocks/pages")
		f.StringVar(&uid, "uid", "", "identity UID hex")
		f.IntVar(&atqa, "atqa", 4, "identity ATQA numeric, e.g. 0x0004")
		f.IntVar(&sak, "sak", 8, "identity SAK numeric, e.g. 0x08")
		f.StringVar(&ats, "ats", "", "identity ATS hex")
		f.StringVar(&name, "name", "", "emulator flag name")
		f.BoolVar(&enabled, "enabled", true, "flag value")
		f.IntVar(&value, "value", 0, "write mode or counter value")
		f.StringVar(&protocol, "protocol", "em410x", "LF emulation protocol")
	case "t55":
		f.StringVar(&protocol, "protocol", "em410x", "em410x|electra|hidprox|ioprox|viking|pac|jablotron|idteck")
		f.StringVar(&newPassword, "new-key", "", "four-byte new password hex")
		f.StringVar(&oldPasswords, "old-keys", "", "comma-separated known four-byte old passwords")
	case "lf":
		f.StringVar(&protocol, "protocol", "em410x", "em410x|hidprox|ioprox|viking|pac|jablotron|pyramid|em4x05")
		f.IntVar(&format, "format", 1, "HID format ID 0..255")
	case "mfc":
		f.IntVar(&sectors, "sectors", 16, "senested sectors (1..40; starts at zero)")
		f.StringVar(&strategy, "strategy", "auto", "auto|nested|hardnested|darkside")
		f.IntVar(&maxRuns, "max-runs", 100, "hardnested acquisition runs per attempt (1..1000)")
		f.IntVar(&maxAttempts, "max-attempts", 3, "recovery attempts (1..10)")
		f.BoolVar(&slow, "slow", false, "slow hardnested acquisition")
		f.IntVar(&targetBlock, "target-block", 4, "recovery target block")
		f.StringVar(&targetType, "target-key-type", "A", "recovery target A/B")
		f.StringVar(&name, "tools-dir", chameleon.DefaultRecoveryToolsDir(), "trusted recovery helper directory")
		f.DurationVar(&rfTimeout, "limit", 5*time.Minute, "recovery total time limit")
		f.IntVar(&block, "block", 0, "block index 0..255")
		f.IntVar(&count, "count", 64, "dump block count: 20,64,128,256")
		f.StringVar(&keyText, "key", "", "known six-byte key hex")
		f.StringVar(&keyType, "key-type", "A", "A or B")
	case "type2":
		f.IntVar(&start, "start", 0, "first page index")
		f.IntVar(&count, "count", 4, "page count")
	case "raw":
		f.DurationVar(&rfTimeout, "rf-timeout", 500*time.Millisecond, "RF response timeout, up to 65535ms")
		f.BoolVar(&appendCRC, "append-crc", true, "append ISO14443-A CRC")
		f.BoolVar(&autoSelect, "auto-select", true, "select card before transmit")
		f.BoolVar(&keepField, "keep-field", false, "keep RF field (use SDK Reader sessions for multi-step operations)")
		f.BoolVar(&checkCRC, "check-crc", true, "check/strip response CRC")
	case "sniff":
		f.DurationVar(&rfTimeout, "duration", time.Second, "capture duration (1ms..30s); use --timeout longer than duration")
	case "ble", "usb":
		flagName := "ble-bridge"
		if group == "usb" {
			flagName = "usb-bridge"
		}
		f.StringVar(&bridgeURL, flagName, "", "HTTP loopback origin of the matching Python device bridge")
		f.StringVar(&bridgeTokenFile, "bridge-token-file", "", "private token file shared with the Python device bridge")
	case "recover":
		f.StringVar(&kind, "tool", "", "offline recovery tool name")
		f.StringVar(&name, "tools-dir", chameleon.DefaultRecoveryToolsDir(), "trusted tool directory")
		f.DurationVar(&rfTimeout, "limit", 5*time.Minute, "offline process time limit")
	}
	f.Usage = func() {
		fmt.Fprintf(f.Output(), "Usage: chameleon %s", group)
		if needsAction {
			fmt.Fprint(f.Output(), " ACTION")
		}
		fmt.Fprintln(f.Output(), " [options]")
		f.PrintDefaults()
	}
	if e := f.Parse(rest); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	if *nativeUSB && !nativeUSBAction(group, action, name) {
		return fmt.Errorf("--native-usb is limited to pairing, settings reset and bootloader maintenance; use the Python bridge for application commands")
	}
	if *nativeUSB && *port == "" {
		return fmt.Errorf("--native-usb requires an explicit local --port")
	}
	if e := checkNewOutput(*output); e != nil {
		return e
	}
	if *timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}

	if group == "recover" {
		if kind == "" {
			return result(chameleon.RecoveryTools(), *output, false)
		}
		if rfTimeout <= 0 {
			return fmt.Errorf("limit must be positive")
		}
		ctx, stop := context.WithTimeout(ctx, rfTimeout)
		defer stop()
		v, e := chameleon.RunRecovery(ctx, name, kind, f.Args()...)
		return errors.Join(e, result(v, *output, false))
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", f.Args())
	}
	if group == "ble" || group == "usb" {
		if action != "scan" {
			return fmt.Errorf("device bridge action: scan")
		}
		if bridgeURL == "" || bridgeTokenFile == "" {
			return fmt.Errorf("%s scanning requires --%s-bridge and --bridge-token-file; configure the Python bridge management endpoint", group, group)
		}
		if *port != "" {
			return fmt.Errorf("bridge scanning uses its management URL, not --port")
		}
		token, e := readBridgeToken(bridgeTokenFile)
		if e != nil {
			return e
		}
		var bridge managedBridge
		if group == "usb" {
			bridge, e = chameleon.NewUSBBridge(bridgeURL, token)
		} else {
			bridge, e = chameleon.NewBLEBridge(bridgeURL, token)
		}
		if e != nil {
			return e
		}
		scanCtx, stop := context.WithTimeout(ctx, *timeout)
		defer stop()
		v, e := bridge.Scan(scanCtx)
		if e != nil {
			return e
		}
		return result(v, *output, false)
	}
	if group == "firmware" && action == "flash" {
		if !*yes {
			return fmt.Errorf("firmware flash requires --yes and an explicit USB DFU serial --port")
		}
		defer func() { retErr = reportDeviceOutputError(retErr) }()
		v, e := chameleon.FlashFirmware(ctx, *port, *input, dfu.Options{Timeout: *timeout})
		return errors.Join(e, result(v, *output, false))
	}
	if group == "firmware" && action == "inspect" {
		if *input == "" {
			return fmt.Errorf("firmware inspect requires --input ZIP")
		}
		v, e := chameleon.InspectFirmware(*input)
		if e != nil {
			return e
		}
		return result(v, *output, false)
	}
	if group == "commands" && describe != "" {
		cmd, e := chameleon.ParseCommand(describe)
		if e != nil {
			return e
		}
		v, e := chameleon.DescribeCommand(cmd)
		if e != nil {
			return e
		}
		return result(v, *output, false)
	}
	if group == "commands" && !supported {
		return result(chameleon.Commands(), *output, false)
	}
	payload, e := decodeHex(*dataHex)
	if e != nil {
		return e
	}
	if *input != "" && group != "image" {
		if *dataHex != "" {
			return fmt.Errorf("use --input or --data, not both")
		}
		payload, e = os.ReadFile(*input)
		if e != nil {
			return e
		}
		if len(payload) > 1<<20 {
			return fmt.Errorf("input exceeds 1 MiB")
		}
	}
	if *binaryOut && !(group == "mfc" && (action == "read" || action == "dump") || group == "type2" && action == "read" || group == "emulator" && (action == "read" || action == "export") || group == "raw" || group == "apdu") {
		return fmt.Errorf("--binary requires a byte-returning read/raw command")
	}
	requireWrite := func() error {
		if !*yes {
			return fmt.Errorf("operation changes device/card state; pass --yes to execute")
		}
		return nil
	}
	mutation := group == "sniff" || group == "t55" || group == "firmware" || group == "image" && action == "load" || group == "mode" || group == "raw" || group == "apdu" || group == "settings" && action != "get" || group == "slot" && action != "get" || group == "emulator" && (action == "write" || action == "import" || action == "flag" || action == "write-mode" || action == "lf-set" || action == "identity" && uid != "") || (group == "mfc" || group == "type2") && action == "write"
	if group == "slot" && action != "get" && action != "save" && (slot < 1 || slot > 8) {
		return fmt.Errorf("slot must be 1..8")
	}
	if mutation {
		if e = requireWrite(); e != nil {
			return e
		}
	}
	if *nativeUSB && group == "settings" && action == "set" && name == "ble-key" {
		patch := chameleon.SettingsPatch{BLEKey: &keyText}
		if err := patch.Validate(); err != nil {
			return err
		}
	}
	// Validate raw-call intent before opening any hardware.
	var cmd chameleon.Command
	if group == "call" {
		cmd, e = chameleon.ParseCommand(action)
		if e != nil {
			return e
		}
		if readerMode && cmd == chameleon.CmdEnterBootloader {
			return fmt.Errorf("bootloader cannot run inside reader session")
		}
		if len(payload) > chameleon.MaxPayload {
			return fmt.Errorf("payload exceeds %d bytes", chameleon.MaxPayload)
		}
		if cmd == chameleon.CmdEnterBootloader && len(payload) != 0 {
			return fmt.Errorf("bootloader command requires an empty payload")
		}
		meta, _ := chameleon.LookupCommand(cmd)
		if !force && !meta.Registered {
			hint := map[chameleon.Command]string{
				chameleon.CmdGenericRead:     "use ADC_GENERIC_READ, the registered generic LF read",
				chameleon.CmdCorrGenericRead: "use ADC_GENERIC_READ, the registered generic LF read",
				chameleon.CmdEm4x05Readsniff: "use EM4X05_SCAN, the registered EM4x05 read",
			}[cmd]
			if hint == "" {
				hint = "no firmware alternative is known"
			}
			return fmt.Errorf("%s is defined but has no handler in the pinned firmware, so the device cannot execute it; %s (or pass --force to send it anyway for a firmware that does register it)", action, hint)
		}
		if meta.Effect != "read" && meta.Effect != "rf-read" {
			if e = requireWrite(); e != nil {
				return e
			}
		}
	}
	if *port == "" {
		*port, e = autoPort()
		if e != nil {
			return e
		}
	}
	var c *chameleon.Client
	if *nativeUSB {
		c, e = openNativeUSB(ctx, *port, *timeout)
	} else {
		c, e = openDevice(*port, *timeout)
	}
	if e != nil {
		return e
	}
	defer c.Close()
	defer func() { retErr = reportDeviceOutputError(retErr) }()
	var out any
	switch group {
	case "commands":
		caps, e := c.Capabilities(ctx)
		if e != nil {
			return e
		}
		have := map[uint16]bool{}
		for _, n := range caps {
			have[n] = true
		}
		items := []chameleon.CommandInfo{}
		for _, m := range chameleon.Commands() {
			if have[uint16(m.ID)] {
				items = append(items, m)
			}
		}
		out = items
	case "call":
		if !force {
			caps, e := c.Capabilities(ctx)
			if e != nil {
				return e
			}
			found := false
			for _, v := range caps {
				if uint16(cmd) == v {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("device does not advertise %s; use --force only with a known compatible firmware", cmd)
			}
		}
		var r chameleon.Response
		options := chameleon.CommandOptions{AllowUnregistered: force}
		if readerMode {
			e = c.Reader(ctx, func(s *chameleon.ReaderSession) error {
				var err error
				r, err = s.DoWithOptions(ctx, cmd, payload, options)
				return err
			})
		} else {
			r, e = c.ExecuteWithOptions(ctx, cmd, payload, options)
		}
		if e != nil {
			return e
		}
		if e = result(r, *output, false); e != nil {
			return e
		}
		if r.NoResponse {
			return nil
		}
		return r.Check(0, 0x40, 0x68)
	case "settings":
		switch action {
		case "get":
			v, err := c.Settings(ctx)
			e = err
			if !showSecrets {
				v.BLEKey = ""
				v.Raw = nil
			}
			out = v
		case "save":
			e = c.SaveSettings(ctx)
		case "reset":
			e = c.ResetSettings(ctx)
		case "factory-reset":
			e = c.FactoryReset(ctx)
		case "bootloader":
			e = c.EnterBootloader(ctx)
		case "clear-bonds":
			e = c.ClearBLEBonds(ctx)
		case "set":
			switch name {
			case "animation":
				if value < 0 || value > 3 {
					return fmt.Errorf("animation must be 0..3")
				}
				e = c.SetAnimation(ctx, byte(value))
			case "sleep":
				if value < 0 || value > 255 {
					return fmt.Errorf("sleep must be 0..255")
				}
				e = c.SetSleepTimeout(ctx, byte(value))
			case "long-press":
				if value < 0 || value > 65535 {
					return fmt.Errorf("duration must be 0..65535")
				}
				e = c.SetLongPressThreshold(ctx, uint16(value))
			case "button":
				if len(kind) != 1 || value < 0 || value > 5 {
					return fmt.Errorf("invalid button/action")
				}
				e = c.SetButton(ctx, kind[0], long, byte(value))
			case "ble-key":
				e = c.SetBLEKey(ctx, keyText)
			case "ble-pairing":
				e = c.SetBLEPairing(ctx, enabled)
			default:
				return fmt.Errorf("unknown setting %q", name)
			}
		default:
			return fmt.Errorf("settings actions: get,set,save,reset,factory-reset,bootloader,clear-bonds")
		}
	case "slot":
		field := chameleon.SenseHF
		if sense == "lf" {
			field = chameleon.SenseLF
		} else if sense != "hf" {
			return fmt.Errorf("sense must be hf or lf")
		}
		switch action {
		case "get":
			out, e = c.Slots(ctx)
		case "select":
			e = c.SetActiveSlot(ctx, slot)
		case "type":
			if tag < 0 || tag > 65535 {
				return fmt.Errorf("type must be 0..65535")
			}
			e = c.SetSlotType(ctx, slot, uint16(tag))
		case "init":
			if tag < 0 || tag > 65535 {
				return fmt.Errorf("type must be 0..65535")
			}
			e = c.InitializeSlot(ctx, slot, uint16(tag))
		case "enable":
			e = c.EnableSlot(ctx, slot, field, enabled)
		case "delete":
			e = c.ClearSlot(ctx, slot, field)
		case "nickname":
			e = c.SetSlotNickname(ctx, slot, field, name)
		case "save":
			e = c.SaveSlotData(ctx)
		default:
			return fmt.Errorf("slot actions: get,select,type,init,enable,delete,nickname,save")
		}
	case "mode":
		e = c.SetReaderMode(ctx, enabled)
	case "emulator":
		switch action {
		case "get":
			out, e = c.EmulatorConfig(ctx)
		case "read", "export":
			if kind == "classic" {
				out, e = c.ReadEmulatorBlocks(ctx, start, count)
			} else if kind == "type2" {
				out, e = c.ReadEmulatorPages(ctx, start, count)
			} else {
				return fmt.Errorf("kind must be classic or type2")
			}
		case "write", "import":
			if kind == "classic" {
				e = c.WriteEmulatorBlocks(ctx, start, payload)
			} else if kind == "type2" {
				e = c.WriteEmulatorPages(ctx, start, payload)
			} else {
				return fmt.Errorf("kind must be classic or type2")
			}
		case "identity":
			if uid == "" {
				out, e = c.AntiCollision(ctx)
			} else {
				u, err := decodeHex(uid)
				if err != nil {
					return err
				}
				a, err := decodeHex(ats)
				if err != nil {
					return err
				}
				if atqa < 0 || atqa > 65535 || sak < 0 || sak > 255 {
					return fmt.Errorf("ATQA/SAK out of range")
				}
				e = c.SetAntiCollision(ctx, chameleon.Card{UID: u, ATQA: uint16(atqa), SAK: byte(sak), ATS: a})
			}
		case "flag":
			e = c.SetEmulatorFlag(ctx, name, enabled)
		case "write-mode":
			if value < 0 || value > 4 {
				return fmt.Errorf("mode must be 0..4")
			}
			e = c.SetEmulatorWriteMode(ctx, kind == "type2", byte(value))
		case "lf-get":
			out, e = c.EmulatorLFID(ctx, protocol)
		case "lf-set":
			e = c.SetEmulatorLFID(ctx, protocol, payload)
		default:
			return fmt.Errorf("emulator actions: get,read,export,write,import,identity,flag,write-mode,lf-get,lf-set")
		}
	case "t55":
		newKey, err := decodeHex(newPassword)
		if err != nil {
			return err
		}
		if oldPasswords == "" {
			return fmt.Errorf("--old-keys is required")
		}
		keys := [][]byte{}
		for _, k := range strings.Split(oldPasswords, ",") {
			b, err := decodeHex(k)
			if err != nil {
				return err
			}
			keys = append(keys, b)
		}
		e = c.WriteT55(ctx, protocol, payload, newKey, keys)
	case "lf":
		if format < 0 || format > 255 {
			return fmt.Errorf("format must be 0..255")
		}
		out, e = c.ScanLF(ctx, strings.ToLower(protocol), byte(format))
	case "mfc":
		key, err := decodeHex(keyText)
		if err != nil {
			return err
		}
		if len(key) != 6 && !(action == "recover" && strategy == "darkside") {
			return fmt.Errorf("--key requires six known key bytes")
		}
		kt := chameleon.KeyA
		if strings.EqualFold(keyType, "B") {
			kt = chameleon.KeyB
		} else if !strings.EqualFold(keyType, "A") {
			return fmt.Errorf("key-type must be A or B")
		}
		switch action {
		case "senested":
			out, e = c.RecoverStaticEncrypted(ctx, key, sectors, name, rfTimeout)
		case "recover", "collect":
			tkt := chameleon.KeyA
			if strings.EqualFold(targetType, "B") {
				tkt = chameleon.KeyB
			} else if !strings.EqualFold(targetType, "A") {
				return fmt.Errorf("target-key-type must be A or B")
			}
			if action == "collect" {
				out, e = c.CollectNested(ctx, block, kt, key, targetBlock, tkt)
			} else {
				out, e = c.RecoverClassic(ctx, block, kt, key, targetBlock, tkt, chameleon.AdvancedRecoveryOptions{Strategy: strategy, ToolsDir: name, Limit: rfTimeout, MaxRuns: maxRuns, MaxAttempts: maxAttempts, Slow: slow})
			}
		case "read":
			out, e = c.ReadClassicBlock(ctx, block, kt, key)
		case "write":
			e = c.WriteClassicBlock(ctx, block, kt, key, payload)
		case "auth":
			e = c.AuthenticateClassic(ctx, block, kt, key)
		case "dump":
			out, e = c.DumpClassic(ctx, count, kt, key)
		default:
			return fmt.Errorf("mfc actions: read,write,auth,dump,collect,recover,senested")
		}
	case "type2":
		switch action {
		case "read":
			out, e = c.ReadType2Pages(ctx, start, count)
		case "write":
			e = c.WriteType2Page(ctx, start, payload)
		default:
			return fmt.Errorf("type2 actions: read,write")
		}
	case "raw":
		ms := rfTimeout.Milliseconds()
		if ms < 1 || ms > 65535 {
			return fmt.Errorf("rf-timeout must be 1..65535ms")
		}
		out, e = c.Raw14A(ctx, payload, chameleon.RawOptions{ActivateField: true, WaitResponse: true, AppendCRC: appendCRC, AutoSelect: autoSelect, KeepField: keepField, CheckResponseCRC: checkCRC, TimeoutMS: uint16(ms)})
	case "apdu":
		if len(payload) < 1 || len(payload) > 61 {
			return fmt.Errorf("this firmware APDU command accepts 1..61 bytes")
		}
		e = c.Reader(ctx, func(s *chameleon.ReaderSession) error {
			r, err := s.Do(ctx, chameleon.CmdHf14a4ReaderApdu, payload)
			if err != nil {
				return err
			}
			if err = r.Check(0); err != nil {
				return err
			}
			out = r.Data
			return nil
		})
	case "firmware":
		if action != "enter" {
			return fmt.Errorf("firmware actions: check,download,inspect,enter,flash")
		}
		e = c.EnterBootloader(ctx)
	case "image":
		switch action {
		case "save":
			out, e = c.ExportEmulator(ctx)
		case "load":
			if *input == "" {
				return fmt.Errorf("image load requires --input")
			}
			file, err := os.Open(*input)
			if err != nil {
				return err
			}
			defer file.Close()
			var image chameleon.EmulatorImage
			decoder := json.NewDecoder(io.LimitReader(file, 2<<20))
			decoder.DisallowUnknownFields()
			if err = decoder.Decode(&image); err != nil {
				return err
			}
			e = c.ImportEmulator(ctx, image)
		default:
			return fmt.Errorf("image actions: save,load")
		}
	case "sniff":
		ms := rfTimeout.Milliseconds()
		if ms < 1 || ms > 30000 {
			return fmt.Errorf("duration must be 1..30000ms")
		}
		out, e = c.SniffHF(ctx, uint16(ms))
	case "doctor":
		out, e = c.Diagnose(ctx)
	}
	if e != nil {
		if (group == "doctor" || group == "mfc" && (action == "recover" || action == "senested")) && out != nil {
			if outputErr := result(out, *output, false); outputErr != nil {
				return errors.Join(e, outputErr)
			}
		}
		return e
	}
	if out == nil {
		out = map[string]any{"ok": true, "operation": group + " " + action}
	}
	return result(out, *output, *binaryOut)
}

func nativeUSBAction(group, action, name string) bool {
	if group == "firmware" {
		return action == "enter"
	}
	if group != "settings" {
		return false
	}
	switch action {
	case "get", "save", "reset", "factory-reset", "bootloader", "clear-bonds":
		return true
	case "set":
		return name == "ble-key" || name == "ble-pairing"
	}
	return false
}
