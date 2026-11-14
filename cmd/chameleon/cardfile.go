package main

import (
	"encoding/json"
	"flag"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"io"
	"os"
)

// Indented numeric arrays for 100,000 maximum-width keys exceed 32MiB.
const dictionaryJSONLimit = 64 << 20
const dictionaryTextLimit = 8 << 20

func readLimitedFile(path string) ([]byte, error) {
	return readFileWithLimit(path, 4<<20)
}

func readDictionaryFile(path string) ([]byte, error) {
	return readFileWithLimit(path, dictionaryJSONLimit)
}

func readFileWithLimit(path string, limit int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	if info, e := f.Stat(); e != nil {
		return nil, e
	} else if info.Size() > limit {
		return nil, fmt.Errorf("input exceeds %dMiB", limit>>20)
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("input exceeds %dMiB", limit>>20)
	}
	return b, e
}
func runCardfile(args []string) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Println("Usage: chameleon cardfile inspect|to-image|from-image|from-bin|to-bin|dict-to-text|dict-from-text --input FILE [--template FILE] [--output FILE]")
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("cardfile actions: inspect, to-image, from-image, from-bin, to-bin, dict-to-text, dict-from-text")
	}
	action := args[0]
	f := flag.NewFlagSet("cardfile", flag.ContinueOnError)
	input := f.String("input", "", "GUI card input; emulator image for from-image")
	template := f.String("template", "", "existing GUI card with matching identity/type")
	output := f.String("output", "", "atomic JSON output file")
	if e := f.Parse(args[1:]); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if sameFilePath(*output, *input) || sameFilePath(*output, *template) {
		return fmt.Errorf("output must be separate from input and template")
	}
	readInput := readLimitedFile
	if action == "dict-to-text" {
		readInput = readDictionaryFile
	} else if action == "dict-from-text" {
		readInput = func(path string) ([]byte, error) { return readFileWithLimit(path, dictionaryTextLimit) }
	}
	b, e := readInput(*input)
	if e != nil {
		return e
	}
	if action == "dict-to-text" || action == "dict-from-text" {
		var d chameleon.GUIDictionary
		if action == "dict-to-text" {
			if e = json.Unmarshal(b, &d); e != nil {
				return e
			}
			text, e := d.Text()
			if e != nil {
				return e
			}
			return result([]byte(text), *output, true)
		}
		tb, e := readDictionaryFile(*template)
		if e != nil {
			return e
		}
		if e = json.Unmarshal(tb, &d); e != nil {
			return e
		}
		d, e = d.WithText(string(b))
		if e != nil {
			return e
		}
		return result(d, *output, false)
	}
	if action == "from-image" {
		var image chameleon.EmulatorImage
		if e = json.Unmarshal(b, &image); e != nil {
			return e
		}
		tb, e := readLimitedFile(*template)
		if e != nil {
			return e
		}
		card, e := chameleon.ParseGUICard(tb)
		if e != nil {
			return e
		}
		card, e = card.WithImage(image)
		if e != nil {
			return e
		}
		return result(card, *output, false)
	}
	card, e := chameleon.ParseGUICard(b)
	if e != nil {
		return e
	}
	switch action {
	case "inspect":
		return result(card, *output, false)
	case "to-image":
		image, e := card.EmulatorImage()
		if e != nil {
			return e
		}
		return result(image, *output, false)
	default:
		return fmt.Errorf("unknown cardfile action %s", action)
	}
}
