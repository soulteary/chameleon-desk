package main

import (
	"bytes"
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

type offlineRecoveryCheckpoint struct {
	chameleon.OfflineRecovery
	Completed bool   `json:"completed"`
	Error     string `json:"error,omitempty"`
}

func (p offlineRecoveryCheckpoint) matchesCapture(b chameleon.RecoveryBundle) bool {
	if !p.Completed || p.Error != "" || p.VerifiedOnCard || p.Tool.Truncated || p.Tool.Tool != b.Tool ||
		p.CaptureSHA256 != b.Checksum || p.Kind != b.Kind || !bytes.Equal(p.UID, b.UID) ||
		p.Block != b.TargetBlock || p.KeyType != b.TargetType {
		return false
	}
	for _, key := range p.Candidates {
		if len(key) != 6 {
			return false
		}
	}
	return true
}

func isWorkflow(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "ulc", "rf", "seos", "emv", "des":
		return true
	case "type2":
		if len(args) < 2 {
			return false
		}
		switch args[1] {
		case "password-read", "password-write", "version", "signature", "counter", "authnonce":
			return true
		}
		return false
	case "mfc":
		if len(args) < 2 {
			return false
		}
		switch args[1] {
		case "clone", "auth-log", "recover-log", "dictionary", "autopwn", "dump-keys", "value-get", "value-set", "value-inc", "value-dec", "value-copy", "value-decode", "recover-collect", "recover-compute", "recover-verify":
			return true
		}
	}
	return false
}
func classicDictionary(path, keyText string) ([]chameleon.HexBytes, error) {
	keys := []chameleon.HexBytes{}
	if keyText != "" {
		key, e := decodeHex(keyText)
		if e != nil || len(key) != 6 {
			return nil, fmt.Errorf("--key must be six bytes")
		}
		keys = append(keys, key)
	}
	if path != "" {
		b, e := readLimitedFile(path)
		if e != nil {
			return nil, e
		}
		var d chameleon.GUIDictionary
		if strings.HasPrefix(strings.TrimSpace(string(b)), "{") {
			if e = json.Unmarshal(b, &d); e != nil {
				return nil, e
			}
		} else {
			d = chameleon.GUIDictionary{ID: "cli", Name: "dictionary", KeyLength: 12}
			d, e = d.WithText(string(b))
			if e != nil {
				return nil, e
			}
		}
		if d.KeyLength != 12 {
			return nil, fmt.Errorf("Classic dictionary requires six-byte keys")
		}
		keys = append(keys, d.Keys...)
	}
	if len(keys) == 0 {
		keys = []chameleon.HexBytes{{255, 255, 255, 255, 255, 255}, {0, 0, 0, 0, 0, 0}, {0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5}, {0xd3, 0xf7, 0xd3, 0xf7, 0xd3, 0xf7}}
	}
	return keys, nil
}
func runWorkflow(args []string) (retErr error) {
	group := args[0]
	if group == "type2" {
		group = "ulc"
	}
	if len(args) > 1 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Println("Workflow groups: mfc dictionary|autopwn|dump-keys|clone|value-*|auth-log|recover-log|recover-collect|recover-compute|recover-verify; ulc auth|read|write|set-key|password-*|ulcg-*; rf info|config|config-set|auth-trace|lf-samples|adc; seos get|set|keys; emv scan|debug|load|tlv; des info|auth|check. Run GROUP ACTION --help for options.")
		return nil
	}
	if len(args) < 2 {
		return fmt.Errorf("%s requires an action", group)
	}
	action := args[1]
	f := flag.NewFlagSet(group+" "+action, flag.ContinueOnError)
	port := f.String("port", "", "USB, ble:UUID, tcp:HOST:PORT; defaults to one USB device")
	timeout := f.Duration("timeout", 60*time.Second, "per-command timeout")
	limit := f.Duration("limit", 20*time.Minute, "total operation time limit")
	input := f.String("input", "", "JSON input")
	output := f.String("output", "", "atomic output file")
	dictionary := f.String("dictionary", "", "text or GUI dictionary file")
	keysFile := f.String("keys-file", "", "Classic key map JSON")
	tools := f.String("tools-dir", "tools/bin", "trusted recovery tool directory")
	strategy := f.String("strategy", "auto", "auto|nested|hardnested|darkside")
	aidText := f.String("aid", "000000", "DESFire application ID bytes")
	algorithm := f.String("algorithm", "aes", "aes|des|3des")
	keyNo := f.Int("key-number", 0, "DESFire key number 0..13")
	keyText := f.String("key", "", "known Classic/UL-C key or Type2 password")
	newKeyText := f.String("new-key", "", "explicit replacement key")
	backdoor := f.String("backdoor", "", "opt-in six-byte RF08S backdoor")
	dataText := f.String("data", "", "hexadecimal data")
	keyType := f.String("key-type", "A", "Classic A/B key")
	dstKeyText := f.String("dst-key", "", "value operation destination key; defaults to --key")
	dstType := f.String("dst-key-type", "A", "destination A/B key")
	block := f.Int("block", 0, "Classic block / Type2 page")
	count := f.Int("count", 1, "page count")
	maxLogs := f.Int("max-logs", 10000, "maximum detection records to download")
	maxPairs := f.Int("max-pairs", 1000, "maximum offline log pairs")
	dst := f.Int("dst-block", 0, "value operation destination")
	sectors := f.Int("sectors", 16, "number of contiguous Classic sectors")
	value := f.Int64("value", 0, "signed value or nonnegative increment/decrement operand")
	duration := f.Duration("duration", time.Second, "capture/poll duration")
	eraseKey := f.Bool("erase-key", false, "ULCG collection intentionally overwrites key pages 47..44")
	challenges := f.Int("challenges", 1000, "ULCG maximum nonce collision search")
	threads := f.Int("threads", 1, "offline ULCG helper threads")
	kindCapture := f.String("kind", "nested", "recovery capture kind: nested|darkside|hardnested")
	maxRuns := f.Int("max-runs", 100, "bounded RF acquisition runs")
	slow := f.Bool("slow", false, "use slow hardnested acquisition")
	resume := f.Bool("resume", false, "reuse a completed recovery result only when its capture checksum matches")
	writeTrailers := f.Bool("trailers", false, "clone sector trailers and keys after data; manufacturer always skipped")
	preserveAccess := f.Bool("preserve-access", false, "clone original ACL instead of generic writable ACL")
	yes := f.Bool("yes", false, "explicitly permit state-changing commands or card writes")
	binaryOutput := f.Bool("binary", false, "raw sample/dump output")
	bcc := f.Int("bcc", -1, "BCC override -1/0/1/2")
	cl2 := f.Int("cl2", -1, "CL2 override -1/0/1/2")
	cl3 := f.Int("cl3", -1, "CL3 override -1/0/1/2")
	rats := f.Int("rats", -1, "RATS override -1/0/1/2")
	if e := f.Parse(args[2:]); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	if f.NArg() != 0 || *timeout <= 0 || *limit <= 0 {
		return fmt.Errorf("invalid arguments or timeouts")
	}
	if !(group == "mfc" && action == "recover-compute" && *resume) {
		if e := checkNewOutput(*output); e != nil {
			return e
		}
	}
	for _, source := range []string{*input, *dictionary, *keysFile} {
		if *output != "" && sameFilePath(*output, source) {
			return fmt.Errorf("output must differ from input files")
		}
	}
	key, e := decodeHex(*keyText)
	if e != nil {
		return e
	}
	data, e := decodeHex(*dataText)
	if e != nil {
		return e
	}
	kind := chameleon.KeyA
	if strings.EqualFold(*keyType, "B") {
		kind = chameleon.KeyB
	} else if !strings.EqualFold(*keyType, "A") {
		return fmt.Errorf("key-type must be A/B")
	}
	if group == "mfc" && action == "value-decode" {
		v, a, e := chameleon.DecodeClassicValue(data)
		if e != nil {
			return e
		}
		return result(map[string]any{"value": v, "address": a}, *output, false)
	}
	if group == "ulc" && action == "ulcg-recover" {
		v, e := readULCGCheckpoint(*input)
		if e != nil {
			return e
		}
		ctx, cancel := context.WithTimeout(context.Background(), *limit)
		defer cancel()
		out, e := chameleon.RecoverULCG(ctx, v, *tools, *threads, *limit)
		return errors.Join(e, result(out, *output, false))
	}
	if group == "ulc" && action == "ulcg-collect" && (*challenges < 2 || *challenges > 10000) {
		return fmt.Errorf("ULCG challenges must be 2..10000")
	}
	if group == "ulc" && action == "ulcg-collect" && *output != "" {
		if _, err := os.Stat(*output); !os.IsNotExist(err) {
			return fmt.Errorf("ULCG checkpoint output must be a new file")
		}
	}
	if group == "ulc" && action == "ulcg-collect" && (!*yes || !*eraseKey || *output == "") {
		return fmt.Errorf("ULCG collection erases the card key; requires --yes --erase-key and --output checkpoint file")
	}
	if group == "emv" && action == "tlv" {
		v, e := chameleon.ParseBERTLV(data)
		if e != nil {
			return e
		}
		return result(v, *output, false)
	}
	if group == "mfc" && action == "recover-log" {
		b, e := readLimitedFile(*input)
		if e != nil {
			return e
		}
		var logs []chameleon.AuthLog
		if e = json.Unmarshal(b, &logs); e != nil {
			return e
		}
		ctx, cancel := context.WithTimeout(context.Background(), *limit)
		defer cancel()
		v, e := chameleon.RecoverDetectionLogs(ctx, logs, *tools, *limit, *maxPairs)
		return errors.Join(e, result(v, *output, false))
	}
	if group == "mfc" && action == "recover-compute" {
		b, err := chameleon.ReadRecoveryBundle(*input)
		if err != nil {
			return err
		}
		if *resume && *output != "" {
			if cached, readErr := readLimitedFile(*output); readErr == nil {
				var prior offlineRecoveryCheckpoint
				if json.Unmarshal(cached, &prior) == nil && prior.matchesCapture(b) {
					fmt.Fprintf(os.Stderr, "Reused cached offline result for capture %s\n", b.Checksum)
					return result(prior, "", false)
				}
				return fmt.Errorf("cached result is incomplete or does not match capture; use a new output file to recompute")
			} else if !os.IsNotExist(readErr) {
				return fmt.Errorf("read cached result: %w", readErr)
			}
		}
		if err := checkNewOutput(*output); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), *limit)
		defer cancel()
		v, err := chameleon.ComputeRecovery(ctx, *tools, b)
		checkpoint := offlineRecoveryCheckpoint{OfflineRecovery: v, Completed: err == nil}
		if err != nil {
			checkpoint.Error = err.Error()
		}
		return errors.Join(err, result(checkpoint, *output, false))
	}
	if group == "mfc" && action == "recover-collect" {
		if *output == "" || *input != "" {
			return fmt.Errorf("recover-collect requires a new --output file and does not accept --input")
		}
		if _, err := os.Stat(*output); !os.IsNotExist(err) {
			return fmt.Errorf("capture output must be a new file")
		}
		if *kindCapture != "nested" && *kindCapture != "hardnested" && *kindCapture != "darkside" {
			return fmt.Errorf("--kind must be nested, hardnested, or darkside")
		}
	}
	if group == "mfc" && action == "recover-verify" {
		b, err := chameleon.ReadRecoveryBundle(*input)
		if err != nil {
			return err
		}
		candidate, err := decodeHex(*keyText)
		if err != nil {
			return err
		}
		if *port == "" {
			*port, err = autoPort()
			if err != nil {
				return err
			}
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		ctx, cancel := context.WithTimeout(ctx, *limit)
		defer cancel()
		c, err := chameleon.OpenContext(ctx, *port, *timeout)
		if err != nil {
			return err
		}
		defer c.Close()
		defer func() { retErr = reportDeviceOutputError(retErr) }()
		v, err := c.VerifyRecoveryCandidate(ctx, b.UID, b.TargetBlock, b.TargetType, candidate)
		return errors.Join(err, result(v, *output, false))
	}
	mutation := group == "emv" && action == "load" || group == "mfc" && (action == "clone" || action == "value-set" || action == "value-inc" || action == "value-dec" || action == "value-copy") || group == "ulc" && (action == "write" || action == "set-key" || action == "password-write") || group == "rf" && action == "config-set" || group == "seos" && (action == "set" || action == "keys")
	if mutation && !*yes {
		return fmt.Errorf("%s %s requires --yes", group, action)
	}
	var journal *ulcgJournal
	if group == "ulc" && action == "ulcg-collect" {
		journal, e = newULCGJournal(*output)
		if e != nil {
			return e
		}
		defer func() { retErr = errors.Join(retErr, journal.Close()) }()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *limit)
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
	switch group {
	case "mfc":
		switch action {
		case "recover-collect":
			if len(key) != 6 {
				return fmt.Errorf("recover-collect requires --key (six-byte known key)")
			}
			bundle, collectErr := c.CollectRecoveryBundle(ctx, *block, kind, key, *dst, func() chameleon.KeyType {
				if strings.EqualFold(*dstType, "B") {
					return chameleon.KeyB
				}
				return chameleon.KeyA
			}(), *kindCapture, *maxRuns, *slow, 3)
			e = collectErr
			if e == nil {
				e = chameleon.WriteRecoveryBundle(*output, bundle)
				if e != nil {
					e = &outputSaveError{*output, e}
				}
				if e == nil {
					fmt.Fprintf(os.Stderr, "Saved acquisition bundle to %s\n", *output)
					return nil
				}
			}
		case "clone":
			b, err := readLimitedFile(*input)
			if err != nil {
				return err
			}
			var dump chameleon.ClassicCardDump
			if err = json.Unmarshal(b, &dump); err != nil {
				return err
			}
			b, err = readLimitedFile(*keysFile)
			if err != nil {
				return err
			}
			var target chameleon.ClassicKeyMap
			if err = json.Unmarshal(b, &target); err != nil {
				return err
			}
			out, e = c.CloneClassic(ctx, dump, target, chameleon.ClassicCloneOptions{WriteTrailers: *writeTrailers, PreserveAccess: *preserveAccess})
		case "auth-log":
			out, e = c.DetectionLogs(ctx, *maxLogs)
		case "dictionary", "autopwn":
			keys, err := classicDictionary(*dictionary, *keyText)
			if err != nil {
				return err
			}
			if action == "dictionary" {
				out, e = c.CheckClassicDictionary(ctx, *sectors, keys)
			} else {
				bd, err := decodeHex(*backdoor)
				if err != nil {
					return err
				}
				out, e = c.Autopwn(ctx, chameleon.AutopwnOptions{Sectors: *sectors, Dictionary: keys, Backdoor: bd, Recovery: chameleon.AdvancedRecoveryOptions{Strategy: *strategy, ToolsDir: *tools, Limit: *limit, MaxRuns: 100, MaxAttempts: 3}})
			}
		case "dump-keys":
			b, err := readLimitedFile(*keysFile)
			if err != nil {
				return err
			}
			var km chameleon.ClassicKeyMap
			if err = json.Unmarshal(b, &km); err != nil {
				return err
			}
			out, e = c.DumpClassicWithKeys(ctx, km)
			if *binaryOutput && e == nil {
				out = out.(chameleon.ClassicCardDump).Data
			}
		case "value-get":
			v, a, err := c.ReadClassicValue(ctx, *block, kind, key)
			out = map[string]any{"value": v, "address": a}
			e = err
		case "value-set":
			if *value < -2147483648 || *value > 2147483647 {
				return fmt.Errorf("value outside int32")
			}
			e = c.WriteClassicValue(ctx, *block, kind, key, int32(*value))
		case "value-inc", "value-dec", "value-copy":
			if *value < 0 || *value > 2147483647 {
				return fmt.Errorf("operand outside nonnegative int32")
			}
			dk := key
			if *dstKeyText != "" {
				dk, e = decodeHex(*dstKeyText)
				if e != nil {
					return e
				}
			}
			dt := chameleon.KeyA
			if strings.EqualFold(*dstType, "B") {
				dt = chameleon.KeyB
			} else if !strings.EqualFold(*dstType, "A") {
				return fmt.Errorf("dst-key-type must be A/B")
			}
			op := byte(0xc2)
			if action == "value-inc" {
				op = 0xc1
			}
			if action == "value-dec" {
				op = 0xc0
			}
			e = c.ManipulateClassicValue(ctx, *block, kind, key, op, int32(*value), *dst, dt, dk)
		}
	case "ulc":
		switch action {
		case "ulcg-collect":
			out, e = c.CollectULCG(ctx, *challenges, *eraseKey, journal.Write)
			// The checkpoint file is already durable. Emit the summary only to
			// stdout; republishing it would collide with our own journal.
			return errors.Join(e, result(out, "", false))
		case "auth":
			e = c.AuthenticateULC(ctx, key)
		case "read":
			out, e = c.ReadULC(ctx, key, *block, *count)
		case "write":
			e = c.WriteULCPage(ctx, key, *block, data)
		case "set-key":
			newKey, err := decodeHex(*newKeyText)
			if err != nil {
				return err
			}
			e = c.SetULCKey(ctx, key, newKey)
		case "version", "signature", "counter", "authnonce":
			if *block < 0 || *block > 2 {
				return fmt.Errorf("counter index must be 0..2")
			}
			out, e = c.Type2Info(ctx, action, byte(*block))
		case "password-read":
			v, pack, err := c.ReadType2Authenticated(ctx, key, *block, *count)
			out = map[string]any{"data": v, "pack": pack}
			e = err
		case "password-write":
			e = c.WriteType2Authenticated(ctx, key, *block, data)
		default:
			return fmt.Errorf("ulc actions: auth,read,write,set-key,password-read,password-write,version,signature,counter,authnonce,ulcg-collect,ulcg-recover")
		}
	case "rf":
		switch action {
		case "info":
			out, e = c.InspectHFCard(ctx)
		case "config":
			out, e = c.ReaderConfig(ctx)
		case "config-set":
			for _, n := range []int{*bcc, *cl2, *cl3, *rats} {
				if n < -1 || n > 2 {
					return fmt.Errorf("overrides must be -1..2")
				}
			}
			e = c.SetReaderConfig(ctx, chameleon.ReaderConfig{BCC: int8(*bcc), CL2: int8(*cl2), CL3: int8(*cl3), RATS: int8(*rats)})
		case "auth-trace":
			out, e = c.AuthTrace(ctx, *block, kind, key, *duration)
		case "lf-samples":
			v, err := c.CaptureLF(ctx, *duration)
			out, e = v, err
			if *binaryOutput && e == nil {
				out = v.Data
			}
		case "adc":
			out, e = c.ReadLFADC(ctx)
		default:
			return fmt.Errorf("rf actions: config,config-set,auth-trace,lf-samples,adc")
		}
	case "des":
		switch action {
		case "info":
			out, e = c.DESFireInfo(ctx)
		case "auth", "check":
			aid, err := decodeHex(*aidText)
			if err != nil {
				return err
			}
			if *keyNo < 0 || *keyNo > 13 {
				return fmt.Errorf("key number must be 0..13")
			}
			if action == "check" {
				keys := []chameleon.HexBytes{}
				if len(key) > 0 {
					keys = append(keys, key)
				}
				if *dictionary != "" {
					b, err := readLimitedFile(*dictionary)
					if err != nil {
						return err
					}
					for _, line := range strings.Split(string(b), "\n") {
						line = strings.TrimSpace(line)
						if line == "" || strings.HasPrefix(line, "#") {
							continue
						}
						v, err := decodeHex(line)
						if err != nil {
							return err
						}
						keys = append(keys, v)
					}
				}
				out, e = c.CheckDESFireDictionary(ctx, aid, byte(*keyNo), keys)
				break
			}
			ok, err := c.AuthenticateDESFire(ctx, aid, byte(*keyNo), *algorithm, key)
			out = map[string]bool{"authenticated": ok}
			e = err
		default:
			return fmt.Errorf("des actions: info,auth,check")
		}
	case "emv":
		switch action {
		case "debug":
			out, e = c.ISO4Debug(ctx)
		case "scan":
			out, e = c.ScanEMV(ctx)
		case "load":
			b, err := readLimitedFile(*input)
			if err != nil {
				return err
			}
			var p chameleon.ISO4Profile
			if err = json.Unmarshal(b, &p); err != nil {
				return err
			}
			e = c.LoadISO4Profile(ctx, p)
		default:
			return fmt.Errorf("emv actions: scan,load,tlv")
		}
	case "seos":
		switch action {
		case "get":
			out, e = c.SEOSData(ctx)
		case "set":
			b, err := readLimitedFile(*input)
			if err != nil {
				return err
			}
			var v chameleon.SEOSData
			if err = json.Unmarshal(b, &v); err != nil {
				return err
			}
			e = c.SetSEOSData(ctx, v)
		case "keys":
			b, err := readLimitedFile(*input)
			if err != nil {
				return err
			}
			var keys struct {
				Authentication chameleon.HexBytes `json:"authentication"`
				Encryption     chameleon.HexBytes `json:"encryption"`
				MAC            chameleon.HexBytes `json:"mac"`
			}
			if err = json.Unmarshal(b, &keys); err != nil {
				return err
			}
			e = c.SetSEOSKeys(ctx, keys.Authentication, keys.Encryption, keys.MAC)
		default:
			return fmt.Errorf("seos actions: get,set,keys")
		}
	}
	if out != nil {
		oe := result(out, *output, *binaryOutput && e == nil)
		return errors.Join(e, oe)
	}
	if e != nil {
		return e
	}
	return result(map[string]any{"ok": true, "operation": group + " " + action}, *output, false)
}
