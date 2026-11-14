package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"os"
)

func runSamples(args []string) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Println("Actions: hexsamples|csv|plot|modulation|manrawdecode")
		return nil
	}
	if len(args) < 1 {
		return fmt.Errorf("data actions: hexsamples,csv,plot,modulation,manrawdecode")
	}
	action := args[0]
	f := flag.NewFlagSet("data "+action, flag.ContinueOnError)
	input := f.String("input", "", "LF capture JSON or raw file")
	output := f.String("output", "", "new output file")
	raw := f.Bool("raw", false, "input contains binary ADC samples")
	clock := f.Int("clock", 64, "Manchester RF divider")
	phase := f.Int("phase", 0, "Manchester half-bit phase 0/1")
	invert := f.Bool("invert", false, "invert Manchester levels")
	count := f.Int("num", 512, "maximum hex sample bytes")
	if e := f.Parse(args[1:]); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if sameFilePath(*input, *output) {
		return fmt.Errorf("output must differ from input")
	}
	b, e := readLimitedFile(*input)
	if e != nil {
		return e
	}
	samples := chameleon.LFSamples{RateHz: 125000}
	if *raw {
		samples.Data = b
	} else if e = json.Unmarshal(b, &samples); e != nil {
		return e
	}
	if samples.RateHz != 0 && samples.RateHz != 125000 && (action == "modulation" || action == "manrawdecode") {
		return fmt.Errorf("RF divider analysis requires 125000 Hz samples")
	}
	switch action {
	case "modulation":
		v, e := chameleon.AnalyzeLFSamples(samples.Data)
		if e != nil {
			return e
		}
		return result(v, *output, false)
	case "manrawdecode":
		v, e := chameleon.DecodeManchester(samples.Data, *clock, *invert, *phase)
		if e != nil {
			return e
		}
		return result(v, *output, false)
	case "hexsamples":
		if *count < 0 {
			return fmt.Errorf("num must be nonnegative")
		}
		return result(map[string]any{"samples": len(samples.Data), "hex": hex.EncodeToString(samples.Data[:min(*count, len(samples.Data))])}, *output, false)
	case "plot", "csv":
		var text string
		if action == "plot" {
			text, e = chameleon.LFSamplesSVG(samples.Data)
		} else {
			rate := samples.RateHz
			if rate == 0 {
				rate = 125000
			}
			text, e = chameleon.LFSamplesCSV(samples.Data, rate)
		}
		if e != nil {
			return e
		}
		if *output != "" {
			return atomicFile(*output, []byte(text))
		}
		_, e = os.Stdout.Write([]byte(text))
		return e
	default:
		return fmt.Errorf("unknown data action")
	}
}
