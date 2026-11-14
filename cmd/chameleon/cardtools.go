package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// newDictionaryHTTP is swapped in tests so none reaches the network.
var newDictionaryHTTP = func() *http.Client { return &http.Client{Timeout: 60 * time.Second} }

// isCardTool routes the offline card analysis tools that mirror the upstream
// GUI: NDEF read/write, Classic access-condition decode/encode, Vanity SAK and
// dictionary download. All but the download run fully offline.
func isCardTool(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[0] {
	case "ndef":
		return args[1] == "read" || args[1] == "write"
	case "acl":
		return args[1] == "decode" || args[1] == "encode"
	case "vanity":
		return args[1] == "sak"
	case "dictionary":
		return args[1] == "download"
	}
	return false
}

func runCardTool(args []string) error {
	group, action := args[0], args[1]
	f := flag.NewFlagSet(group+" "+action, flag.ContinueOnError)
	// Common flag, then only the current group's flags, so --help lists just
	// what this command accepts instead of every card tool's options.
	output := f.String("output", "", "new output file; the input is never modified")
	var input, text, lang, uri, mime, external, value, messageHex, recordsFile, access, conditions, uid, name *string
	var sector *int
	var list *bool
	var timeout *time.Duration
	switch group {
	case "ndef":
		input = f.String("input", "", "GUI card JSON (Type 2 or MIFARE Classic)")
		text = f.String("text", "", "write: add a UTF-8 Text record")
		lang = f.String("language", "en", "write: Text record language")
		uri = f.String("uri", "", "write: add a URI record")
		mime = f.String("mime", "", "write: media type for a MIME record (with --value)")
		external = f.String("external", "", "write: external type (with --value)")
		value = f.String("value", "", "write: value for --mime or --external")
		messageHex = f.String("message-hex", "", "write: store this raw NDEF message verbatim")
		recordsFile = f.String("records", "", "write: JSON array of records to store")
	case "acl":
		input = f.String("input", "", "GUI card JSON (decode --sector reads its trailer)")
		access = f.String("access", "", "decode: access hex (bytes 7-8 or 6-8)")
		sector = f.Int("sector", -1, "decode: read the trailer of this sector from --input")
		conditions = f.String("conditions", "", "encode: four C1C2C3 conditions 0..7, e.g. 0,0,0,1")
	case "vanity":
		input = f.String("input", "", "GUI card JSON; block 0 is read for the SAK")
		uid = f.String("uid", "", "UID hex when no --input")
	case "dictionary":
		name = f.String("name", "", "source name; also overrides the saved name")
		list = f.Bool("list", false, "list the known sources and exit")
		timeout = f.Duration("timeout", 60*time.Second, "HTTP timeout")
	}
	if e := f.Parse(args[2:]); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if input != nil && *output != "" && sameFilePath(*output, *input) {
		return fmt.Errorf("output must be separate from the input file")
	}
	switch group {
	case "ndef":
		return cardToolNdef(action, *input, *output, ndefInputs{*text, *lang, *uri, *mime, *external, *value, *messageHex, *recordsFile})
	case "acl":
		return cardToolACL(action, *input, *output, *access, *sector, *conditions)
	case "vanity":
		return cardToolVanity(*input, *output, *uid)
	case "dictionary":
		return cardToolDictionary(*output, *name, *list, *timeout)
	}
	return fmt.Errorf("unknown card tool")
}

type ndefInputs struct {
	text, lang, uri, mime, external, value, messageHex, recordsFile string
}

type ndefRecords []chameleon.NdefRecord

func (records *ndefRecords) UnmarshalJSON(data []byte) error {
	var decoded []chameleon.NdefRecord
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&decoded); err != nil {
		return err
	}
	if len(decoded) == 0 {
		return fmt.Errorf("records must contain at least one NDEF record")
	}
	*records = decoded
	return nil
}

func cardToolNdef(action, input, output string, in ndefInputs) error {
	b, e := readLimitedFile(input)
	if e != nil {
		return e
	}
	card, e := chameleon.ParseGUICard(b)
	if e != nil {
		return e
	}
	container, e := chameleon.DetectNdef(card.Tag, card.Data)
	if e != nil {
		return e
	}
	if action == "read" {
		records, e := chameleon.DecodeNdefMessage(container.Message())
		if e != nil {
			return e
		}
		if records == nil {
			records = []chameleon.NdefRecord{}
		}
		return result(map[string]any{"mapping": container.Mapping, "capacity": container.Capacity(), "message_hex": strings.ToUpper(hex.EncodeToString(container.Message())), "records": records}, output, false)
	}
	if output == "" {
		return fmt.Errorf("ndef write requires --output for the updated card")
	}
	message, e := buildNdefMessage(in)
	if e != nil {
		return e
	}
	if e = container.WriteMessage(card.Data, message); e != nil {
		return e
	}
	return result(card, output, false)
}

func buildNdefMessage(in ndefInputs) ([]byte, error) {
	var records ndefRecords
	if in.recordsFile != "" {
		b, e := readLimitedFile(in.recordsFile)
		if e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &records); e != nil {
			return nil, e
		}
	}
	return encodeNdefInputs(records, in.messageHex, in.text, in.lang, in.uri, in.mime, in.external, in.value)
}

func cardToolACL(action, input, output, access string, sector int, conditions string) error {
	if action == "encode" {
		parts := strings.Split(conditions, ",")
		if len(parts) != 4 {
			return fmt.Errorf("acl encode needs four conditions, e.g. --conditions 0,0,0,1")
		}
		var values [4]int
		for i, p := range parts {
			v, e := strconv.Atoi(strings.TrimSpace(p))
			if e != nil {
				return fmt.Errorf("conditions must be integers 0..7")
			}
			values[i] = v
		}
		bytes, e := chameleon.EncodeClassicAccess(values)
		if e != nil {
			return e
		}
		decoded, e := chameleon.DecodeClassicAccess(bytes[:])
		if e != nil {
			return e
		}
		return result(map[string]any{"access_hex": strings.ToUpper(hex.EncodeToString(bytes[:])), "decoded": decoded}, output, false)
	}
	var decoded chameleon.ClassicAccess
	var e error
	switch {
	case access != "" && sector >= 0:
		return fmt.Errorf("use either --access HEX or --input with --sector")
	case access != "":
		decoded, e = chameleon.DecodeClassicAccessHex(access)
	case sector >= 0:
		b, re := readLimitedFile(input)
		if re != nil {
			return re
		}
		card, re := chameleon.ParseGUICard(b)
		if re != nil {
			return re
		}
		first, count, re := chameleon.ClassicSectorGeometry(sector)
		if re != nil {
			return re
		}
		trailer := first + count - 1
		if trailer >= len(card.Data) || len(card.Data[trailer]) != 16 {
			return fmt.Errorf("card has no 16-byte trailer for sector %d", sector)
		}
		decoded, e = chameleon.DecodeClassicAccess(card.Data[trailer][6:9])
	default:
		return fmt.Errorf("acl decode needs --access HEX or --input with --sector")
	}
	if e != nil {
		return e
	}
	return result(decoded, output, false)
}

func cardToolVanity(input, output, uid string) error {
	var vanity *bool
	var detail map[string]any
	switch {
	case uid != "" && input != "":
		return fmt.Errorf("use either --uid HEX or --input CARD.json")
	case uid != "":
		vanity = chameleon.VanitySAKFromUIDHex(uid)
		if vanity == nil {
			return fmt.Errorf("UID must be 4 or 7 hexadecimal bytes")
		}
		detail = map[string]any{"uid": strings.ToUpper(strings.Join(strings.Fields(uid), "")), "source": "uid-length"}
	case input != "":
		b, e := readLimitedFile(input)
		if e != nil {
			return e
		}
		card, e := chameleon.ParseGUICard(b)
		if e != nil {
			return e
		}
		if len(card.Data) == 0 {
			return fmt.Errorf("card has no block 0")
		}
		uidBytes, e := decodeHex(card.UID)
		if e != nil {
			return fmt.Errorf("card UID is not hexadecimal")
		}
		vanity = chameleon.VanitySAKFromData(uidBytes, card.SAK, card.Data[0])
		detail = map[string]any{"uid": strings.ToUpper(card.UID), "sak": card.SAK, "source": "block0"}
	default:
		return fmt.Errorf("vanity sak needs --input CARD.json or --uid HEX")
	}
	detail["vanity_sak"] = vanity
	if vanity == nil {
		detail["note"] = "indeterminate: block 0 stores neither the plain nor the vanity SAK, or the UID length is unsupported"
	}
	return result(detail, output, false)
}

func cardToolDictionary(output, name string, list bool, timeout time.Duration) error {
	if list {
		return result(chameleon.DictionarySources, output, false)
	}
	if name == "" {
		return fmt.Errorf("dictionary download needs --name; use --list to see the sources")
	}
	if output == "" {
		return fmt.Errorf("dictionary download requires --output")
	}
	if timeout <= 0 {
		return fmt.Errorf("invalid timeout")
	}
	src, e := chameleon.DictionarySourceByName(name)
	if e != nil {
		return e
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	client := newDictionaryHTTP()
	client.Timeout = timeout
	fmt.Fprintf(os.Stderr, "Downloading %s from %s\n", src.Name, src.URL)
	d, e := chameleon.DownloadDictionary(ctx, client, src, name)
	if e != nil {
		return e
	}
	fmt.Fprintf(os.Stderr, "Parsed %d keys of %d hex digits\n", len(d.Keys), d.KeyLength)
	return result(d, output, false)
}

func encodeNdefInputs(records []chameleon.NdefRecord, messageHex, text, language, uri, mime, external, value string) ([]byte, error) {
	constructed := text != "" || uri != "" || mime != "" || external != ""
	if records != nil && (messageHex != "" || constructed) || messageHex != "" && constructed {
		return nil, fmt.Errorf("use records, message_hex, or record fields as separate input modes")
	}
	if records != nil {
		return chameleon.EncodeNdefMessage(records)
	}
	if messageHex != "" {
		return decodeHex(messageHex)
	}
	var built []chameleon.NdefRecord
	add := func(rec chameleon.NdefRecord, e error) error {
		if e != nil {
			return e
		}
		built = append(built, rec)
		return nil
	}
	if text != "" {
		if e := add(chameleon.NewNdefText(text, language)); e != nil {
			return nil, e
		}
	}
	if uri != "" {
		if e := add(chameleon.NewNdefURI(uri)); e != nil {
			return nil, e
		}
	}
	if mime != "" {
		if e := add(chameleon.NewNdefMime(mime, value)); e != nil {
			return nil, e
		}
	}
	if external != "" {
		if e := add(chameleon.NewNdefExternal(external, value)); e != nil {
			return nil, e
		}
	}
	if len(built) == 0 {
		return nil, fmt.Errorf("provide text, uri, mime, external, message_hex or records")
	}
	return chameleon.EncodeNdefMessage(built)
}
