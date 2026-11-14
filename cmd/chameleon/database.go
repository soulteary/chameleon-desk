package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func readGUIDB(path string) (chameleon.GUIDatabase, error) {
	info, e := os.Stat(path)
	if e != nil {
		return chameleon.GUIDatabase{}, e
	}
	if info.Size() > 32<<20 {
		return chameleon.GUIDatabase{}, fmt.Errorf("database exceeds 32MiB")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return chameleon.GUIDatabase{}, e
	}
	if len(b) > 32<<20 {
		return chameleon.GUIDatabase{}, fmt.Errorf("database exceeds 32MiB")
	}
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] != '{' {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		b, e = exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "json", "-o", "-", path).Output()
		if e != nil {
			return chameleon.GUIDatabase{}, fmt.Errorf("input must be SharedPreferences JSON or macOS plist: %w", e)
		}
	}
	return chameleon.ParseGUIDatabase(b)
}
func runDatabase(args []string) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Println("Actions: inspect|export|merge|import-folder|export-folder|list|show|add|edit|delete")
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("database actions: inspect, export, merge, import-folder, export-folder, list, show, add, edit, delete")
	}
	action := args[0]
	f := flag.NewFlagSet("database", flag.ContinueOnError)
	input := f.String("input", "", "SharedPreferences JSON or macOS plist snapshot")
	other := f.String("source", "", "second database or folder bundle")
	format := f.String("format", "json", "json or macOS plist (export only)")
	output := f.String("output", "", "new atomic JSON output; source is never modified")
	folder := f.String("folder", "", "root folder ID")
	dictionary := f.Bool("dictionaries", false, "dictionary folder bundle")
	if e := f.Parse(args[1:]); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if *output != "" && (sameFilePath(*output, *input) || (*other != "" && sameFilePath(*output, *other))) {
		return fmt.Errorf("output must be separate from source files")
	}
	d, e := readGUIDB(*input)
	if e != nil {
		return e
	}
	switch action {
	case "inspect":
		return result(d.CollectionCounts(), *output, false)
	case "export":
		if *format == "plist" {
			if *output == "" {
				return fmt.Errorf("plist export requires --output")
			}
			raw, e := json.Marshal(d)
			if e != nil {
				return e
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "xml1", "-o", "-", "-")
			cmd.Stdin = bytes.NewReader(raw)
			data, e := cmd.Output()
			if e != nil {
				return e
			}
			return atomicFile(*output, data)
		}
		if *format != "json" {
			return fmt.Errorf("unknown export format")
		}
		return result(d, *output, false)
	case "merge":
		v, e := readGUIDB(*other)
		if e != nil {
			return e
		}
		d, e = d.Merge(v)
		if e != nil {
			return e
		}
		return result(d, *output, false)
	case "import-folder":
		b, e := os.ReadFile(*other)
		if e != nil {
			return e
		}
		d, e = d.ImportFolder(b)
		if e != nil {
			return e
		}
		return result(d, *output, false)
	case "export-folder":
		b, e := d.ExportFolder(*folder, *dictionary)
		if e != nil {
			return e
		}
		if *output != "" {
			return atomicFile(*output, append(b, '\n'))
		}
		_, e = os.Stdout.Write(append(b, '\n'))
		return e
	default:
		return fmt.Errorf("unknown database action")
	}
}

func sameFilePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	aa, e1 := filepath.Abs(a)
	bb, e2 := filepath.Abs(b)
	if e1 == nil && e2 == nil && aa == bb {
		return true
	}
	x, e1 := os.Stat(a)
	y, e2 := os.Stat(b)
	return e1 == nil && e2 == nil && os.SameFile(x, y)
}
