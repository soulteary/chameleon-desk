package main

import (
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

// isLibrary routes the saved-card management that mirrors the upstream GUI
// "Saved Cards" page and slot manager; older database actions stay unchanged.
func isLibrary(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[0] {
	case "database":
		switch args[1] {
		case "list", "show", "add", "edit", "delete":
			return true
		}
	case "cardfile":
		return args[1] == "from-bin" || args[1] == "to-bin"
	case "slot":
		return args[1] == "load-card"
	}
	return false
}

func cardPatch(f *flag.FlagSet, name, uid, color, folder, atqa, ats *string, sak, tag *int) (chameleon.GUICardPatch, error) {
	p := chameleon.GUICardPatch{}
	var e error
	f.Visit(func(fl *flag.Flag) {
		switch fl.Name {
		case "name":
			p.Name = name
		case "uid":
			p.UID = uid
		case "color":
			p.Color = color
		case "folder":
			p.FolderID = folder
		case "sak":
			if *sak < 0 || *sak > 255 {
				e = fmt.Errorf("--sak must be 0..255")
			}
			v := byte(*sak)
			p.SAK = &v
		case "type":
			if *tag < 1 || *tag > 65535 {
				e = fmt.Errorf("--type must be a tag type ID")
			}
			v := uint16(*tag)
			p.Tag = &v
		case "atqa":
			b, err := decodeHex(*atqa)
			if err != nil || len(b) != 2 {
				e = fmt.Errorf("--atqa must be two bytes in GUI numeric order, e.g. 0004")
			}
			p.ATQA = b
		case "ats":
			b, err := decodeHex(*ats)
			if err != nil {
				e = fmt.Errorf("--ats must be hexadecimal")
			}
			if len(b) == 0 {
				p.ClearATS = true
			} else {
				p.ATS = b
			}
		}
	})
	return p, e
}

func runLibrary(args []string) error {
	group, action := args[0], args[1]
	f := flag.NewFlagSet(group+" "+action, flag.ContinueOnError)
	input := f.String("input", "", "database snapshot (database), card JSON / .bin dump (cardfile, slot)")
	source := f.String("source", "", "GUI card or dictionary JSON to add")
	output := f.String("output", "", "new output file; the input is never modified")
	database := f.String("database", "", "slot load-card: database snapshot to take --id from")
	id := f.String("id", "", "record ID, unique ID prefix or unique exact name")
	dictionaries := f.Bool("dictionaries", false, "operate on dictionaries instead of cards")
	query := f.String("query", "", "list: case-insensitive match on name, type, UID or ID")
	sense := f.String("sense", "all", "list: hf, lf or all")
	newID := f.Bool("new-id", false, "add: assign a fresh ID instead of failing on a duplicate")
	name := f.String("name", "", "card name")
	uid := f.String("uid", "", "UID / LF ID hex")
	color := f.String("color", "", "GUI color #rrggbb")
	folder := f.String("folder", "", "folder ID; empty moves to the root")
	atqa := f.String("atqa", "", "ATQA hex in GUI numeric order, e.g. 0004")
	ats := f.String("ats", "", "ATS hex; empty clears it")
	sak := f.Int("sak", 8, "SAK, e.g. 0x08")
	tag := f.Int("type", 1001, "tag type ID")
	slot := f.Int("slot", 0, "slot load-card: target slot 1..8")
	plan := f.Bool("plan", false, "slot load-card: print the write plan without a device")
	activate := f.Bool("activate", false, "slot load-card: leave the loaded slot active (GUI behaviour)")
	save := f.Bool("save", true, "slot load-card: save slot data to flash after verification")
	yes := f.Bool("yes", false, "confirm replacing the slot content")
	port := f.String("port", "", "USB serial or TCP application port (Python bridge for BLE); otherwise select one USB device")
	timeout := f.Duration("timeout", 5*time.Second, "per-command timeout")
	if e := f.Parse(args[2:]); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	for _, p := range []string{*input, *source, *database} {
		if *output != "" && sameFilePath(*output, p) {
			return fmt.Errorf("output must be separate from source files")
		}
	}
	switch group {
	case "cardfile":
		b, e := readLimitedFile(*input)
		if e != nil {
			return e
		}
		if action == "to-bin" {
			card, e := chameleon.ParseGUICard(b)
			if e != nil {
				return e
			}
			dump, e := card.Dump()
			if e != nil {
				return e
			}
			if *output == "" {
				return fmt.Errorf("to-bin requires --output")
			}
			return result(dump, *output, true)
		}
		p, e := cardPatch(f, name, uid, color, folder, atqa, ats, sak, tag)
		if e != nil {
			return e
		}
		if *tag < 1 || *tag > 65535 {
			return fmt.Errorf("--type must be a tag type ID")
		}
		p.Name = nil
		card, e := chameleon.NewGUICardFromDump(*name, uint16(*tag), b, p)
		if e != nil {
			return e
		}
		return result(card, *output, false)
	case "slot":
		return loadCard(f, *input, *database, *id, *output, *port, *slot, *timeout, *plan, *activate, *save, *yes)
	}
	d, e := readGUIDB(*input)
	if e != nil {
		return e
	}
	switch action {
	case "list":
		v, e := d.Records(*dictionaries, *query, *sense)
		if e != nil {
			return e
		}
		return result(v, *output, false)
	case "show":
		if *dictionaries {
			v, e := d.Dictionary(*id)
			if e != nil {
				return e
			}
			return result(v, *output, false)
		}
		v, e := d.Card(*id)
		if e != nil {
			return e
		}
		return result(v, *output, false)
	case "add":
		readSource := readLimitedFile
		if *dictionaries {
			readSource = readDictionaryFile
		}
		b, err := readSource(*source)
		if err != nil {
			return err
		}
		fresh := ""
		if *newID {
			if fresh, e = chameleon.NewGUIID(); e != nil {
				return e
			}
		}
		if *dictionaries {
			var v chameleon.GUIDictionary
			if e = json.Unmarshal(b, &v); e != nil {
				return e
			}
			if fresh != "" {
				v.ID = fresh
			}
			d, e = d.AddDictionary(v)
		} else {
			v, err := chameleon.ParseGUICard(b)
			if err != nil {
				return err
			}
			if fresh != "" {
				v = v.WithID(fresh)
			}
			if f.Lookup("folder").Value.String() != "" {
				if v, e = v.Edit(chameleon.GUICardPatch{FolderID: folder}); e != nil {
					return e
				}
			}
			d, e = d.AddCard(v)
		}
	case "edit":
		if *dictionaries {
			v, err := d.Dictionary(*id)
			if err != nil {
				return err
			}
			changed := false
			f.Visit(func(fl *flag.Flag) {
				if fl.Name == "name" {
					v.Name, changed = *name, true
				}
			})
			if !changed {
				return fmt.Errorf("dictionary edit supports --name; use cardfile dict-from-text for keys")
			}
			d, e = d.ReplaceDictionary(v)
			break
		}
		v, err := d.Card(*id)
		if err != nil {
			return err
		}
		p, err := cardPatch(f, name, uid, color, folder, atqa, ats, sak, tag)
		if err != nil {
			return err
		}
		if v, e = v.Edit(p); e != nil {
			return e
		}
		d, e = d.ReplaceCard(v)
	case "delete":
		var removed string
		if d, removed, e = d.DeleteRecord(*dictionaries, *id); e == nil {
			fmt.Fprintf(os.Stderr, "Removed %s\n", removed)
		}
	}
	if e != nil {
		return e
	}
	if *output == "" {
		return fmt.Errorf("%s requires --output for the updated database", action)
	}
	return result(d, *output, false)
}

func loadCard(f *flag.FlagSet, input, database, id, output, port string, slot int, timeout time.Duration, plan, activate, save, yes bool) (retErr error) {
	if e := checkNewOutput(output); e != nil {
		return e
	}
	var card chameleon.GUICard
	switch {
	case input != "" && database != "":
		return fmt.Errorf("use either --input CARD.json or --database DB --id ID")
	case database != "":
		d, e := readGUIDB(database)
		if e != nil {
			return e
		}
		if card, e = d.Card(id); e != nil {
			return e
		}
	case input != "":
		b, e := readLimitedFile(input)
		if e != nil {
			return e
		}
		if card, e = chameleon.ParseGUICard(b); e != nil {
			return e
		}
	default:
		return fmt.Errorf("slot load-card requires --input CARD.json or --database DB --id ID")
	}
	p, e := chameleon.PlanGUICardLoad(card, slot)
	if e != nil {
		return e
	}
	if plan {
		return result(p, output, false)
	}
	if !yes {
		return fmt.Errorf("loading replaces slot %d %s content; review with --plan, then pass --yes", slot, strings.ToUpper(p.Sense))
	}
	if timeout <= 0 {
		return fmt.Errorf("invalid timeout")
	}
	if port == "" {
		if port, e = autoPort(); e != nil {
			return e
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	c, e := chameleon.OpenContext(ctx, port, timeout)
	if e != nil {
		return e
	}
	defer c.Close()
	defer func() { retErr = reportDeviceOutputError(retErr) }()
	v, e := c.LoadGUICard(ctx, slot, card, chameleon.GUICardLoadOptions{Persist: save, Activate: activate})
	return errors.Join(e, result(v, output, false))
}
