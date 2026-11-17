package main

import (
	"flag"
	"fmt"
	"strings"

	chameleon "github.com/soulteary/chameleon-desk"
)

func runLibraryOrganizer(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("use library view|change|import-file|diff")
	}
	action := args[1]
	f := flag.NewFlagSet("library "+action, flag.ContinueOnError)
	input := f.String("input", "", "source database or left Desk card")
	source := f.String("source", "", "external file, dictionary text or right Desk card")
	output := f.String("output", "", "new JSON output file")
	format := f.String("format", "auto", "auto|pm3-json|mct|flipper-nfc|flipper-rfid")
	preview := f.Bool("preview", false, "preview external file without adding a draft")
	name := f.String("name", "", "record/folder name")
	kind := f.Bool("dictionaries", false, "dictionary collection")
	change := f.String("action", "", "folder-create|folder-update|folder-delete|move|dictionary-create|dictionary-update|dictionary-delete")
	id := f.String("id", "", "full existing record/folder ID")
	ids := f.String("ids", "", "comma-separated IDs for atomic move")
	parent := f.String("parent", "", "parent folder ID")
	folder := f.String("folder", "", "record destination folder ID; empty means root")
	keyLength := f.Int("key-length", 0, "dictionary hexadecimal key length; 0 uses existing/default")
	if err := f.Parse(args[2:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("positional arguments are not accepted")
	}
	if err := readerWorkflowOutputPreflight(*output, *input, *source); err != nil {
		return err
	}
	if action == "import-file" {
		if *source == "" {
			return fmt.Errorf("--source file is required")
		}
		raw, err := readLimitedFile(*source)
		if err != nil {
			return err
		}
		observation, err := chameleon.ParseCardFile(*format, *name, string(raw))
		if err != nil {
			return err
		}
		if *preview {
			return result(observation, *output, false)
		}
		db, err := readGUIDB(*input)
		if err != nil {
			return err
		}
		read, err := observation.ReadResult()
		if err != nil {
			return err
		}
		card, err := chameleon.NewGUICardFromRead(*name, read, "")
		if err != nil {
			return err
		}
		db, err = db.AddCard(card)
		if err != nil {
			return err
		}
		return result(db, *output, false)
	}
	if action == "diff" {
		a, err := readLimitedFile(*input)
		if err != nil {
			return err
		}
		b, err := readLimitedFile(*source)
		if err != nil {
			return err
		}
		left, err := chameleon.ParseGUICard(a)
		if err != nil {
			return err
		}
		right, err := chameleon.ParseGUICard(b)
		if err != nil {
			return err
		}
		diff, err := chameleon.CompareCards(left, right)
		if err != nil {
			return err
		}
		return result(diff, *output, false)
	}
	db, err := readGUIDB(*input)
	if err != nil {
		return err
	}
	if action == "view" {
		folders, err := db.Folders(*kind)
		if err != nil {
			return err
		}
		records, err := db.Records(*kind, "", "all")
		if err != nil {
			return err
		}
		return result(map[string]any{"folders": folders, "records": records}, *output, false)
	}
	if action != "change" {
		return fmt.Errorf("use library view|change|import-file|diff")
	}
	p := chameleon.LibraryChange{Action: *change, Dictionaries: *kind, ID: *id, Name: *name, ParentID: *parent, FolderID: *folder, KeyLength: *keyLength}
	if *ids != "" {
		p.IDs = strings.Split(*ids, ",")
	}
	if *source != "" {
		raw, err := readLimitedFile(*source)
		if err != nil {
			return err
		}
		p.Text = string(raw)
	}
	next, err := db.ApplyLibraryChange(p)
	if err != nil {
		return err
	}
	return result(next, *output, false)
}
