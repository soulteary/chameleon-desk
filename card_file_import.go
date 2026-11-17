package chameleon

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// CardFileObservation retains the original file and partial bytes. File identity
// fields are declarations from that file, never observations of physical RF.
type CardFileObservation struct {
	Schema       int       `json:"schema"`
	Format       string    `json:"format"`
	Name         string    `json:"name"`
	Sense        Sense     `json:"sense"`
	TagType      uint16    `json:"tag_type"`
	UID          HexBytes  `json:"uid,omitempty"`
	ATQA         HexBytes  `json:"atqa,omitempty"`
	SAK          *byte     `json:"sak,omitempty"`
	ATS          *HexBytes `json:"ats,omitempty"`
	UnitBytes    int       `json:"unit_bytes"`
	Rows         []string  `json:"rows"`
	UnknownBytes int       `json:"unknown_bytes"`
	Warnings     []string  `json:"warnings"`
	Raw          string    `json:"raw"`
	SHA256       string    `json:"sha256"`
}

func fileHex(text string, width int) (string, error) {
	s := strings.ToUpper(strings.Join(strings.Fields(text), ""))
	if len(s) != width*2 {
		return "", fmt.Errorf("file row requires %d bytes", width)
	}
	for i := 0; i < len(s); i += 2 {
		if s[i:i+2] != "??" {
			if _, err := hex.DecodeString(s[i : i+2]); err != nil {
				return "", fmt.Errorf("bytes must be hexadecimal pairs or ??")
			}
		}
	}
	return s, nil
}

func ParseCardFile(format, name, text string) (CardFileObservation, error) {
	out := CardFileObservation{Schema: 1, Format: format, Name: name, Sense: SenseHF, UnitBytes: 16, Warnings: []string{"File declarations are not physical-card observations; keep the source file and unknown markers."}, Raw: text}
	if len(text) == 0 || len(text) > 2<<20 || len(name) > 256 || !utf8.ValidString(text) || !utf8.ValidString(name) {
		return out, fmt.Errorf("card file must contain 1..2MiB and name at most 256 bytes")
	}
	if format == "auto" {
		switch {
		case strings.HasPrefix(strings.TrimSpace(text), "{"):
			format = "pm3-json"
		case strings.Contains(text, "Flipper NFC device"):
			format = "flipper-nfc"
		case strings.Contains(text, "Flipper RFID key"):
			format = "flipper-rfid"
		case strings.Contains(text, "+Sector:"):
			format = "mct"
		default:
			return out, fmt.Errorf("unrecognized file format; choose PM3 JSON, MCT or Flipper")
		}
		out.Format = format
	}
	indexed := map[int]string{}
	put := func(index int, value string) error {
		if index < 0 || index > 255 {
			return fmt.Errorf("file address must be 0..255")
		}
		if _, ok := indexed[index]; ok {
			return fmt.Errorf("repeated file address %d", index)
		}
		row, err := fileHex(value, out.UnitBytes)
		if err != nil {
			return err
		}
		indexed[index] = row
		return nil
	}
	parseHex := func(raw string) (HexBytes, error) {
		b, err := hex.DecodeString(strings.Join(strings.Fields(raw), ""))
		return b, err
	}
	switch format {
	case "pm3-json":
		if err := uniqueFileJSON(text); err != nil {
			return out, err
		}
		var doc struct {
			Card   map[string]string `json:"Card"`
			Blocks map[string]string `json:"blocks"`
		}
		if err := json.Unmarshal([]byte(text), &doc); err != nil {
			return out, err
		}
		var err error
		if v := doc.Card["UID"]; v != "" {
			out.UID, err = parseHex(v)
			if err != nil {
				return out, err
			}
		}
		if v := doc.Card["ATQA"]; v != "" {
			out.ATQA, err = parseHex(v)
			if err != nil || len(out.ATQA) != 2 {
				return out, fmt.Errorf("invalid PM3 ATQA")
			}
			out.ATQA[0], out.ATQA[1] = out.ATQA[1], out.ATQA[0]
		}
		if v := doc.Card["SAK"]; v != "" {
			b, e := parseHex(v)
			if e != nil || len(b) != 1 {
				return out, fmt.Errorf("invalid PM3 SAK")
			}
			out.SAK = &b[0]
		}
		for key, value := range doc.Blocks {
			i, err := strconv.Atoi(key)
			if err != nil {
				return out, fmt.Errorf("invalid block address")
			}
			if err = put(i, value); err != nil {
				return out, err
			}
		}
	case "mct":
		sector, pos := -1, 0
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.HasPrefix(line, "+Sector:") {
				v, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "+Sector:")))
				if err != nil || v < 0 || v > 39 {
					return out, fmt.Errorf("invalid MCT sector")
				}
				sector = v
				pos = 0
				continue
			}
			if sector < 0 {
				return out, fmt.Errorf("MCT data requires a sector header")
			}
			first, count, _ := ClassicSectorGeometry(sector)
			if pos >= count {
				return out, fmt.Errorf("too many blocks in MCT sector")
			}
			if err := put(first+pos, strings.ReplaceAll(line, "-", "?")); err != nil {
				return out, err
			}
			pos++
		}
		out.Warnings = append(out.Warnings, "MCT does not declare a trustworthy RF identity; manufacturer bytes are not used to invent ATQA, SAK or ATS.")
	case "flipper-nfc", "flipper-rfid":
		fields := map[string]string{}
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				return out, fmt.Errorf("malformed Flipper field")
			}
			key, value = strings.TrimSpace(key), strings.TrimSpace(value)
			if _, exists := fields[key]; exists {
				return out, fmt.Errorf("repeated Flipper field %s", key)
			}
			fields[key] = value
		}
		if fields["Version"] != "4" && format == "flipper-nfc" || fields["Version"] != "1" && format == "flipper-rfid" {
			return out, fmt.Errorf("unsupported Flipper format version")
		}
		if format == "flipper-rfid" {
			out.Sense = SenseLF
			if fields["Key type"] != "EM4100" {
				return out, fmt.Errorf("Flipper RFID import currently supports EM4100 only")
			}
			out.TagType, out.UnitBytes = 100, 5
			if err := put(0, fields["Data"]); err != nil {
				return out, err
			}
		} else {
			if !strings.Contains(fields["Device type"], "Mifare Classic") && !strings.Contains(fields["Device type"], "NTAG/Ultralight") {
				return out, fmt.Errorf("unsupported Flipper NFC device type")
			}
			if strings.Contains(fields["Device type"], "Ultralight") {
				out.UnitBytes = 4
			}
			var err error
			if fields["UID"] != "" {
				out.UID, err = parseHex(fields["UID"])
				if err != nil {
					return out, err
				}
			}
			if fields["ATQA"] != "" {
				out.ATQA, err = parseHex(fields["ATQA"])
				if err != nil || len(out.ATQA) != 2 {
					return out, fmt.Errorf("invalid Flipper ATQA")
				}
			}
			if fields["SAK"] != "" {
				b, e := parseHex(fields["SAK"])
				if e != nil || len(b) != 1 {
					return out, fmt.Errorf("invalid Flipper SAK")
				}
				out.SAK = &b[0]
			}
			for key, value := range fields {
				prefix := "Block "
				if out.UnitBytes == 4 {
					prefix = "Page "
				}
				if strings.HasPrefix(key, prefix) {
					i, err := strconv.Atoi(strings.TrimPrefix(key, prefix))
					if err != nil {
						return out, err
					}
					if err = put(i, value); err != nil {
						return out, err
					}
				}
			}
		}
	default:
		return out, fmt.Errorf("unsupported card file format")
	}
	if len(indexed) == 0 {
		return out, fmt.Errorf("file has no supported data rows")
	}
	last := 0
	for i := range indexed {
		last = max(last, i)
	}
	out.Rows = make([]string, last+1)
	for i := range out.Rows {
		row, ok := indexed[i]
		if !ok {
			row = strings.Repeat("??", out.UnitBytes)
		}
		out.Rows[i] = row
		out.UnknownBytes += strings.Count(row, "??")
	}
	if len(out.UID) != 0 && len(out.UID) != 4 && len(out.UID) != 7 && len(out.UID) != 10 {
		return out, fmt.Errorf("invalid declared UID length")
	}
	out.SHA256 = imageDigest([]byte(text))
	return out, nil
}

func (f CardFileObservation) Validate() error {
	parsed, err := ParseCardFile(f.Format, f.Name, f.Raw)
	if err != nil {
		return err
	}
	a, _ := json.Marshal(parsed)
	b, _ := json.Marshal(f)
	if !bytes.Equal(a, b) {
		return fmt.Errorf("file observation differs from the original file")
	}
	return nil
}

func (f CardFileObservation) ReadResult() (ReadResult, error) {
	if err := f.Validate(); err != nil {
		return ReadResult{}, err
	}
	return ReadResult{Schema: 1, Kind: "file", Requested: len(f.Rows), Read: len(f.Rows), Complete: f.UnknownBytes == 0, UID: append(HexBytes(nil), f.UID...), File: &f}, nil
}

// Reject ambiguous duplicate properties, including ignored vendor fields, before
// decoding a source file into typed maps. Bounds apply to hostile local files too.
func uniqueFileJSON(text string) error {
	decoder := json.NewDecoder(strings.NewReader(text))
	nodes := 0
	var value func(int) error
	value = func(depth int) error {
		nodes++
		if depth > 64 || nodes > 100000 {
			return fmt.Errorf("JSON file nesting/node limit exceeded")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				token, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := token.(string)
				if !ok || seen[key] {
					return fmt.Errorf("duplicate or invalid JSON property")
				}
				seen[key] = true
				if err = value(depth + 1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return fmt.Errorf("invalid JSON object")
			}
		case '[':
			for decoder.More() {
				if err = value(depth + 1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return fmt.Errorf("invalid JSON array")
			}
		default:
			return fmt.Errorf("invalid JSON delimiter")
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("expected one JSON file object")
	}
	return nil
}
