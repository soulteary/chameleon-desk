package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"strconv"
	"strings"

	chameleon "github.com/soulteary/chameleon-desk"
)

type evidenceResolutions []string

func (v *evidenceResolutions) String() string     { return strings.Join(*v, ",") }
func (v *evidenceResolutions) Set(s string) error { *v = append(*v, s); return nil }

func runEvidenceCLI(args []string) error {
	f := flag.NewFlagSet("read-draft "+args[1], flag.ContinueOnError)
	input := f.String("input", "", "source draft JSON")
	other := f.String("other", "", "other Classic draft")
	repair := f.String("repair", "", "selected repair result/envelope")
	output := f.String("output", "", "new JSON output file")
	plan := f.Bool("plan", false, "preview without generating a new draft ID")
	confirm := f.Bool("confirm-same-card", false, "independently confirmed same physical card")
	var resolutions evidenceResolutions
	f.Var(&resolutions, "resolution", "block:byte:left|right; repeat for each conflict")
	if err := f.Parse(args[2:]); err != nil {
		return err
	}
	if *input == "" || f.NArg() != 0 {
		return fmt.Errorf("--input is required")
	}
	if err := readerWorkflowOutputPreflight(*output, *input, *other, *repair); err != nil {
		return err
	}
	raw, err := readLimitedFile(*input)
	if err != nil {
		return err
	}
	source, err := chameleon.ParseGUICard(raw)
	if err != nil {
		return err
	}
	options := chameleon.ClassicMergeOptions{ConfirmSameCard: *confirm}
	for _, item := range resolutions {
		parts := strings.Split(item, ":")
		if len(parts) != 3 {
			return fmt.Errorf("resolution must be block:byte:left|right")
		}
		block, e := strconv.Atoi(parts[0])
		if e != nil {
			return e
		}
		offset, e := strconv.Atoi(parts[1])
		if e != nil {
			return e
		}
		options.Resolutions = append(options.Resolutions, chameleon.ClassicMergeResolution{Block: block, Byte: offset, Source: parts[2]})
	}
	var value any
	if args[1] == "merge" {
		if *other == "" || *repair != "" {
			return fmt.Errorf("merge requires --other only")
		}
		raw, err = readLimitedFile(*other)
		if err != nil {
			return err
		}
		right, e := chameleon.ParseGUICard(raw)
		if e != nil {
			return e
		}
		if *plan {
			value, err = source.ClassicMergePreview(right)
		} else {
			value, err = source.MergeClassicDraft(right, options)
		}
	} else {
		if *repair == "" || *other != "" {
			return fmt.Errorf("apply-repair requires --repair only")
		}
		raw, err = readLimitedFile(*repair)
		if err != nil {
			return err
		}
		var envelope struct {
			Schema int             `json:"schema"`
			Result json.RawMessage `json:"result"`
			Error  *string         `json:"error"`
		}
		if err = json.Unmarshal(raw, &envelope); err != nil {
			return err
		}
		var selected chameleon.ClassicSelectedRepairResult
		if len(envelope.Result) > 0 {
			if err = decodeReaderJSON(raw, &envelope); err != nil {
				return err
			}
			if envelope.Schema != 1 {
				return fmt.Errorf("expected a schema-1 selected repair envelope")
			}
			raw = envelope.Result
		}
		if err = decodeReaderJSON(raw, &selected); err != nil {
			return err
		}
		if envelope.Error != nil && *envelope.Error != "" {
			selected.Complete = false
			selected.Error = *envelope.Error
		}
		if *plan {
			value, err = source.ClassicSelectedRepairPreview(selected)
		} else {
			value, err = source.ApplyClassicSelectedRepair(selected, options)
		}
	}
	if err != nil {
		return err
	}
	return result(value, *output, false)
}
