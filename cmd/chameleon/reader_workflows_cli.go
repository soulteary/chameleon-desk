package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

// Tests replace this application-transport opener; there is no native fallback.
var openReaderWorkflowClient = chameleon.OpenContext

func isReaderWorkflowCLI(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == "reader" || args[0] == "read-draft" {
		return true
	}
	return len(args) > 1 && args[0] == "database" && (args[1] == "import-read" || args[1] == "promote-read")
}
func readerWorkflowUsage() {
	fmt.Println("Usage: chameleon reader classic|lf|type2 --output FILE --port tcp:HOST:PORT [options]")
	fmt.Println("       chameleon reader classic-repair|classic-verify|classic-dictionary|classic-recover --input FILE --output FILE --port tcp:HOST:PORT")
	fmt.Println("       chameleon read-draft import|promote --input FILE [--kind auto|classic|lf|type2|slot] [--plan] [--output FILE]")
	fmt.Println("       chameleon read-draft merge|apply-repair --input FILE --other FILE|--repair FILE [--plan] [--confirm-same-card] [--resolution BLOCK:BYTE:left|right] [--output FILE]")
	fmt.Println("       chameleon database import-read|promote-read --input DATABASE [--source READ] [--id ID] [--output FILE]")
}
func runReaderWorkflowCLI(args []string) error {
	if len(args) < 2 || args[1] == "--help" || args[1] == "-h" {
		readerWorkflowUsage()
		return nil
	}
	if args[0] == "read-draft" && (args[1] == "merge" || args[1] == "apply-repair") {
		return runEvidenceCLI(args)
	}
	if args[0] == "reader" {
		return runReaderCaptureCLI(args[1:])
	}
	return runReadDraftCLI(args)
}
func decodeReaderJSON(data []byte, value any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("expected one JSON value")
	}
	return nil
}

type readerWorkflowEnvelope struct {
	Schema int                   `json:"schema"`
	Result *chameleon.ReadResult `json:"read_result"`
	Error  *string               `json:"error"`
}

func readWorkflowEnvelope(path string) (readerWorkflowEnvelope, error) {
	var envelope readerWorkflowEnvelope
	raw, err := readLimitedFile(path)
	if err != nil {
		return envelope, err
	}
	if err = decodeReaderJSON(raw, &envelope); err != nil {
		return envelope, err
	}
	if envelope.Schema != 1 || envelope.Result == nil {
		return envelope, fmt.Errorf("expected a schema-1 reader result envelope")
	}
	return envelope, nil
}
func draftFromWorkflowEnvelope(envelope readerWorkflowEnvelope, name string) (chameleon.GUICard, error) {
	readError := ""
	if envelope.Error != nil {
		readError = *envelope.Error
	}
	if name == "" {
		name = envelope.Result.Kind + " read " + envelope.Result.UID.String()
		if envelope.Result.Record != nil {
			name = envelope.Result.Record.Protocol + " read"
		}
	}
	return chameleon.NewGUICardFromRead(name, *envelope.Result, readError)
}
func readerWorkflowOutputPreflight(output string, inputs ...string) error {
	for _, input := range inputs {
		if output != "" && sameFilePath(output, input) {
			return fmt.Errorf("output must be separate from every source file")
		}
	}
	return checkNewOutput(output)
}
func readDraftPromotion(c chameleon.GUICard, kind string, plan bool, options chameleon.Type2PromotionOptions) (any, error) {
	draft, err := c.ReadDraft()
	if err != nil {
		return nil, err
	}
	if draft == nil {
		return nil, fmt.Errorf("input is not a reader draft")
	}
	if kind == "auto" {
		kind = draft.Result.Kind
	}
	if kind != draft.Result.Kind {
		return nil, fmt.Errorf("selected conversion kind differs from the draft")
	}
	var validate func() error
	var promote func() (chameleon.GUICard, error)
	switch kind {
	case "slot":
		validate = c.SlotPromotionError
		promote = c.PromoteSlotDraft
	case "classic":
		validate = c.ClassicPromotionError
		promote = c.PromoteClassicDraft
	case "type2":
		validate = func() error { return c.Type2PromotionError(options) }
		promote = func() (chameleon.GUICard, error) { return c.PromoteType2Draft(options) }
	case "lf":
		validate = c.LFPromotionError
		promote = c.PromoteLFDraft
	default:
		return nil, fmt.Errorf("reader draft kind %q has no conversion in this command", kind)
	}
	if err = validate(); err != nil {
		return nil, err
	}
	if plan {
		return map[string]any{"schema": 1, "kind": kind, "available": true, "source_card_id": c.ID}, nil
	}
	return promote()
}
func runReadDraftCLI(args []string) error {
	group, action := args[0], args[1]
	if group == "read-draft" && action != "import" && action != "promote" || group == "database" && action != "import-read" && action != "promote-read" {
		return fmt.Errorf("unknown reader-draft operation")
	}
	f := flag.NewFlagSet(group+" "+action, flag.ContinueOnError)
	input := f.String("input", "", "reader envelope/draft, or database snapshot")
	source := f.String("source", "", "database import-read: reader envelope")
	id := f.String("id", "", "database promote-read: source card ID or unambiguous reference")
	name := f.String("name", "", "imported draft name")
	kind := f.String("kind", "auto", "conversion kind: auto, classic, lf, type2 or slot")
	emulatorPassword := f.String("emulator-password", "", "Type2 conversion only: explicit four-byte emulator password in hex")
	emulatorPACK := f.String("emulator-pack", "", "Type2 conversion only: explicit two-byte emulator PACK in hex")
	confirmCredentials := f.Bool("confirm-emulator-credentials", false, "assign emulator credentials; source hidden credentials remain unknown")
	plan := f.Bool("plan", false, "validate conversion without generating a new ID")
	output := f.String("output", "", "new JSON output file; omit for stdout")
	if err := f.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.NArg() != 0 || *input == "" {
		return fmt.Errorf("--input is required and positional arguments are not supported")
	}
	if err := readerWorkflowOutputPreflight(*output, *input, *source); err != nil {
		return err
	}
	importing := action == "import" || action == "import-read"
	options := chameleon.Type2PromotionOptions{ConfirmEmulatorCredentials: *confirmCredentials}
	for _, item := range []struct {
		raw    string
		size   int
		target *chameleon.HexBytes
	}{{*emulatorPassword, 4, &options.EmulatorPassword}, {*emulatorPACK, 2, &options.EmulatorPACK}} {
		if item.raw != "" {
			b, e := decodeHex(item.raw)
			if e != nil || len(b) != item.size {
				return fmt.Errorf("explicit emulator credentials have incorrect hexadecimal width")
			}
			*item.target = b
		}
	}
	hasCredentials := *emulatorPassword != "" || *emulatorPACK != "" || *confirmCredentials
	if importing && hasCredentials {
		return fmt.Errorf("emulator credential options apply only to Type2 conversion")
	}
	if importing && *plan {
		return fmt.Errorf("--plan applies only to conversion")
	}
	if !importing && *name != "" {
		return fmt.Errorf("edit the draft name before conversion; --name applies only to import")
	}
	var db chameleon.GUIDatabase
	var card chameleon.GUICard
	var err error
	if group == "database" {
		db, err = readGUIDB(*input)
		if err != nil {
			return err
		}
	}
	if importing {
		path := *input
		if group == "database" {
			path = *source
			if path == "" {
				return fmt.Errorf("database import-read requires --source")
			}
		}
		envelope, e := readWorkflowEnvelope(path)
		if e != nil {
			return e
		}
		card, err = draftFromWorkflowEnvelope(envelope, *name)
		if err != nil {
			return err
		}
	} else {
		if group == "database" {
			if *id == "" {
				return fmt.Errorf("database promote-read requires --id")
			}
			card, err = db.Card(*id)
		} else {
			var raw []byte
			raw, err = readLimitedFile(*input)
			if err == nil {
				card, err = chameleon.ParseGUICard(raw)
			}
		}
		if err != nil {
			return err
		}
		if hasCredentials {
			draft, e := card.ReadDraft()
			if e != nil {
				return e
			}
			if draft == nil || draft.Result.Kind != "type2" {
				return fmt.Errorf("emulator credential options apply only to Type2 conversion")
			}
		}
		value, e := readDraftPromotion(card, *kind, *plan, options)
		if e != nil {
			return e
		}
		if *plan {
			return result(value, *output, false)
		}
		card = value.(chameleon.GUICard)
	}
	if group == "database" {
		next, e := db.AddCard(card)
		if e != nil {
			return e
		}
		return result(next, *output, false)
	}
	return result(card, *output, false)
}
func runReaderCaptureCLI(args []string) error {
	if isAdvancedReaderAction(args[0]) {
		return runAdvancedReaderCLI(args)
	}
	action := args[0]
	if action != "classic" && action != "lf" && action != "type2" {
		return fmt.Errorf("reader action must be classic, lf or type2")
	}
	f := flag.NewFlagSet("reader "+action, flag.ContinueOnError)
	port := f.String("port", "", applicationPortHelp)
	timeout := f.Duration("timeout", 5*time.Second, "per-command timeout")
	operationTimeout := f.Duration("operation-timeout", 30*time.Second, "whole reader operation timeout")
	output := f.String("output", "", "mandatory new reader-envelope file")
	input := f.String("input", "", "Classic per-sector or Type2 workflow request JSON")
	protocol := f.String("protocol", "em410x", "LF protocol")
	format := f.Uint("format", 0, "LF HID format ID (0=automatic)")
	password := f.String("password", "", "LF EM4x05 four-byte password as eight hex digits")
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.NArg() != 0 || *output == "" || strings.TrimSpace(*port) == "" || *timeout <= 0 || *operationTimeout <= 0 {
		return fmt.Errorf("a bridge --port, new --output and positive timeouts are required")
	}
	var wrongFlag string
	f.Visit(func(fl *flag.Flag) {
		if action != "lf" && (fl.Name == "protocol" || fl.Name == "format" || fl.Name == "password") || action == "lf" && fl.Name == "input" {
			wrongFlag = fl.Name
		}
	})
	if wrongFlag != "" {
		return fmt.Errorf("--%s is not used by reader %s", wrongFlag, action)
	}
	if err := readerWorkflowOutputPreflight(*output, *input); err != nil {
		return err
	}
	var classic chameleon.ClassicRead
	var type2 chameleon.Type2WorkflowRead
	var lf chameleon.LFRead
	feature := "reader.classic-keyed"
	request := chameleon.FeatureRequest{}
	if action == "classic" {
		if *input == "" {
			return fmt.Errorf("reader classic requires --input with UID and per-sector keys")
		}
		raw, err := readLimitedFile(*input)
		if err != nil {
			return err
		}
		if err = decodeReaderJSON(raw, &classic); err != nil {
			return err
		}
		if _, err = classic.Requirements(); err != nil {
			return err
		}
		request.ClassicRead = &classic
	} else if action == "type2" {
		if *input == "" {
			return fmt.Errorf("reader type2 requires --input with expected UID and range/full selection")
		}
		raw, e := readLimitedFile(*input)
		if e != nil {
			return e
		}
		if e = decodeReaderJSON(raw, &type2); e != nil {
			return e
		}
		if _, e = type2.Requirements(); e != nil {
			return e
		}
		feature = "reader.type2-workflow"
		request.Type2Workflow = &type2
	} else {
		feature = "reader.lf"
		if *format > 255 {
			return fmt.Errorf("format must fit one byte")
		}
		lf = chameleon.LFRead{Protocol: *protocol, Format: byte(*format)}
		if *password != "" {
			b, err := decodeHex(*password)
			if err != nil || len(b) != 4 {
				return fmt.Errorf("password requires exactly four hexadecimal bytes")
			}
			value := binary.BigEndian.Uint32(b)
			lf.Password = &value
		}
		if _, err := lf.Requirements(); err != nil {
			return err
		}
		request.LFRead = &lf
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *operationTimeout)
	defer cancel()
	client, err := openReaderWorkflowClient(ctx, *port, *timeout)
	if err != nil {
		return err
	}
	defer client.Close()
	device, err := client.FeatureDevice(ctx)
	if err != nil {
		return err
	}
	state, err := chameleon.EvaluateFeature(feature, "cli", chameleon.FeatureContext{Device: &device}, &request)
	if err != nil {
		return err
	}
	if state.State != "available" {
		return fmt.Errorf("reader unavailable: %v", state.Reasons)
	}
	var read chameleon.ReadResult
	if action == "classic" {
		read, err = client.ReadClassicWithIdentity(ctx, classic)
	} else if action == "type2" {
		read, err = client.ReadType2Workflow(ctx, type2)
	} else {
		read, err = readerCaptureLF(ctx, client, lf)
	}
	var message *string
	if err != nil {
		text := err.Error()
		message = &text
	}
	saveErr := result(readerWorkflowEnvelope{Schema: 1, Result: &read, Error: message}, *output, false)
	if saveErr != nil {
		return errors.Join(err, reportDeviceOutputError(saveErr))
	}
	if err != nil {
		return fmt.Errorf("reader operation incomplete; result saved to %s: %w", *output, err)
	}
	return nil
}
func readerCaptureLF(ctx context.Context, c *chameleon.Client, input chameleon.LFRead) (chameleon.ReadResult, error) {
	record, err := c.ReadLF(ctx, input)
	read := chameleon.ReadResult{Schema: 1, Kind: "lf", Requested: 1, Complete: err == nil}
	present := false
	var cleanup *chameleon.ModeRestoreError
	if errors.Is(err, chameleon.ErrNoCard) && !errors.As(err, &cleanup) {
		read.Complete = true
		read.Present = &present
		return read, nil
	}
	if err == nil {
		present = true
		read.Present = &present
		read.Read = 1
		read.Record = &record
	}
	return read, err
}
