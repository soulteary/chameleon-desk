package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

type advancedReaderOperation struct {
	feature    string
	request    chameleon.FeatureRequest
	readResult bool
	run        func(context.Context, *chameleon.Client) (any, error)
}
type advancedReaderEnvelope struct {
	Schema int     `json:"schema"`
	Result any     `json:"result"`
	Error  *string `json:"error"`
}

func isAdvancedReaderAction(action string) bool {
	switch action {
	case "classic-repair", "classic-verify", "classic-dictionary", "classic-recover":
		return true
	}
	return false
}
func prepareAdvancedReader(action string, raw []byte, timeout time.Duration, tools string) (advancedReaderOperation, error) {
	var op advancedReaderOperation
	op.feature = "reader." + action
	switch action {
	case "classic-repair":
		var input chameleon.ClassicRepairRequest
		if err := decodeReaderJSON(raw, &input); err != nil {
			return op, err
		}
		if _, err := input.Requirements(); err != nil {
			return op, err
		}
		op.request.ClassicRepair = &input
		op.readResult = len(input.Blocks) == 0
		op.run = func(ctx context.Context, c *chameleon.Client) (any, error) {
			if len(input.Blocks) > 0 {
				return c.ReadClassicSelectedRepair(ctx, input)
			}
			return c.ReadClassicRepair(ctx, input)
		}
	case "classic-verify":
		var input chameleon.ClassicKeyVerificationRequest
		if err := decodeReaderJSON(raw, &input); err != nil {
			return op, err
		}
		if _, err := input.Requirements(); err != nil {
			return op, err
		}
		op.request.ClassicVerify = &input
		op.run = func(ctx context.Context, c *chameleon.Client) (any, error) { return c.VerifyClassicKeys(ctx, input) }
	case "classic-dictionary":
		var input chameleon.ClassicDictionaryRequest
		if err := decodeReaderJSON(raw, &input); err != nil {
			return op, err
		}
		if _, err := input.Requirements(); err != nil {
			return op, err
		}
		minimum := time.Second + time.Duration(2*input.Sectors+1)*100*time.Millisecond
		if timeout < minimum {
			return op, fmt.Errorf("dictionary requires --timeout at least %s for the selected geometry", minimum)
		}
		op.request.ClassicDictionary = &input
		op.run = func(ctx context.Context, c *chameleon.Client) (any, error) {
			return c.CheckClassicDictionaryBound(ctx, input)
		}
	case "classic-recover":
		var input chameleon.ClassicRecoveryRequest
		if err := decodeReaderJSON(raw, &input); err != nil {
			return op, err
		}
		if _, err := input.Requirements(); err != nil {
			return op, err
		}
		if err := chameleon.ValidateClassicRecoveryHelpers(tools, input.Strategy); err != nil {
			return op, err
		}
		op.request.ClassicRecovery = &input
		op.run = func(ctx context.Context, c *chameleon.Client) (any, error) {
			return c.RecoverClassicBound(ctx, input, tools)
		}
	default:
		return op, fmt.Errorf("unsupported advanced reader action")
	}
	return op, nil
}
func runAdvancedReaderCLI(args []string) error {
	action := args[0]
	f := flag.NewFlagSet("reader "+action, flag.ContinueOnError)
	input := f.String("input", "", "typed reader request JSON")
	port := f.String("port", "", applicationPortHelp)
	output := f.String("output", "", "mandatory new result file")
	timeout := f.Duration("timeout", 5*time.Second, "per-command timeout")
	operationTimeout := f.Duration("operation-timeout", 30*time.Second, "whole operation timeout")
	tools := f.String("recovery-tools-dir", chameleon.DefaultRecoveryToolsDir(), "trusted local recovery helpers; classic-recover only")
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.NArg() != 0 || *input == "" || *output == "" || strings.TrimSpace(*port) == "" || *timeout <= 0 || *operationTimeout <= 0 {
		return fmt.Errorf("--input, bridge --port, new --output and positive timeouts are required")
	}
	invalidFlag := false
	f.Visit(func(fl *flag.Flag) {
		if fl.Name == "recovery-tools-dir" && action != "classic-recover" {
			invalidFlag = true
		}
	})
	if invalidFlag {
		return fmt.Errorf("--recovery-tools-dir applies only to classic-recover")
	}
	if err := readerWorkflowOutputPreflight(*output, *input); err != nil {
		return err
	}
	raw, err := readLimitedFile(*input)
	if err != nil {
		return err
	}
	op, err := prepareAdvancedReader(action, raw, *timeout, *tools)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *operationTimeout)
	defer cancel()
	c, err := openReaderWorkflowClient(ctx, *port, *timeout)
	if err != nil {
		return err
	}
	defer c.Close()
	device, err := c.FeatureDevice(ctx)
	if err != nil {
		return err
	}
	state, err := chameleon.EvaluateFeature(op.feature, "cli", chameleon.FeatureContext{Device: &device}, &op.request)
	if err != nil {
		return err
	}
	if state.State != "available" {
		return fmt.Errorf("reader unavailable: %v", state.Reasons)
	}
	value, err := op.run(ctx, c)
	var message *string
	if err != nil {
		text := err.Error()
		message = &text
	}
	var envelope any = advancedReaderEnvelope{Schema: 1, Result: value, Error: message}
	if op.readResult {
		read := value.(chameleon.ReadResult)
		envelope = readerWorkflowEnvelope{Schema: 1, Result: &read, Error: message}
	}
	if saveErr := result(envelope, *output, false); saveErr != nil {
		return errors.Join(err, reportDeviceOutputError(saveErr))
	}
	if err != nil {
		return fmt.Errorf("reader operation incomplete; result saved to %s: %w", *output, err)
	}
	return nil
}
