package chameleon

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// ReadResult describes one reader operation, not a complete emulator image.
type ReadResult struct {
	Schema    int              `json:"schema"`
	Kind      string           `json:"kind"`
	Complete  bool             `json:"complete"`
	Start     int              `json:"start"`
	Requested int              `json:"requested_units"`
	Read      int              `json:"units_read"`
	UID       HexBytes         `json:"uid,omitempty"`
	Data      HexBytes         `json:"data,omitempty"`
	Dump      *ClassicCardDump `json:"dump,omitempty"`
	Record    *LFRecord        `json:"record,omitempty"`
	Present   *bool            `json:"present,omitempty"`
}

// ReadCardData keeps the original observation separate from the editable draft.
// Rows use hex byte pairs and ?? for unknown bytes. Neither edits nor a clean
// read completion establish tag capacity or an emulator identity.
type ReadCardData struct {
	Schema int        `json:"schema"`
	Result ReadResult `json:"read_result"`
	Error  string     `json:"error,omitempty"`
	Rows   []string   `json:"rows"`
}

func (r ReadResult) draftRows() ([]string, error) {
	if r.Schema != 1 || r.Requested < 1 || r.Requested > 256 || r.Read < 1 || r.Read > r.Requested || r.Start < 0 || r.Start > 255 || (r.Complete && r.Read != r.Requested) {
		return nil, fmt.Errorf("read result has no confirmed data or invalid counters")
	}
	var data []byte
	width := 0
	switch r.Kind {
	case "classic":
		d := r.Dump
		if r.Start != 0 || d == nil || d.Schema != 2 || d.TagType < 1000 || d.TagType > 1003 || guiClassicBlocks[d.TagType] != r.Requested || !bytes.Equal(r.UID, d.UID) || d.SHA256 != imageDigest(d.Data) || r.Record != nil || len(r.Data) != 0 || r.Present != nil {
			return nil, fmt.Errorf("invalid Classic read result")
		}
		data, width = d.Data, 16
	case "type2":
		if r.Start+r.Requested > 256 || r.Dump != nil || r.Record != nil || r.Present != nil {
			return nil, fmt.Errorf("invalid Type2 range")
		}
		data, width = r.Data, 4
	case "lf":
		if r.Start != 0 || r.Requested != 1 || !r.Complete || r.Present == nil || !*r.Present || r.Record == nil || r.Dump != nil || len(r.UID) != 0 || len(r.Data) != 0 {
			return nil, fmt.Errorf("LF import requires a successful present-card result")
		}
		rec := r.Record
		if _, ok := lfScanCommands[rec.Protocol]; !ok {
			return nil, fmt.Errorf("unsupported reader protocol")
		}
		if _, err := DecodeLFRecord(rec.Protocol, rec.TagType, rec.ID); err != nil {
			return nil, err
		}
		return []string{strings.ToUpper(hex.EncodeToString(rec.ID))}, nil
	default:
		return nil, fmt.Errorf("unsupported reader result kind")
	}
	if len(r.UID) != 4 && len(r.UID) != 7 && len(r.UID) != 10 {
		return nil, fmt.Errorf("invalid read UID")
	}
	if len(data) != r.Read*width {
		return nil, fmt.Errorf("read byte count mismatch")
	}
	rows := make([]string, r.Start+r.Requested)
	for i := range rows {
		rows[i] = strings.Repeat("??", width)
	}
	for i := 0; i < r.Read; i++ {
		rows[r.Start+i] = strings.ToUpper(hex.EncodeToString(data[i*width : (i+1)*width]))
	}
	if r.Kind == "classic" {
		seen := map[ClassicUnknownKey]bool{}
		sectors := map[uint16]int{1000: 5, 1001: 16, 1002: 32, 1003: 40}[r.Dump.TagType]
		for _, key := range r.Dump.UnknownKeys {
			if key.Sector < 0 || key.Sector >= sectors || (key.Type != KeyA && key.Type != KeyB) || seen[key] {
				return nil, fmt.Errorf("invalid unknown-key metadata")
			}
			seen[key] = true
			start, count, _ := ClassicSectorGeometry(key.Sector)
			trailer := start + count - 1
			if trailer >= r.Read {
				continue
			}
			offset := 0
			if key.Type == KeyB {
				offset = 20
			}
			row := rows[trailer]
			rows[trailer] = row[:offset] + strings.Repeat("??", 6) + row[offset+12:]
		}
	}
	return rows, nil
}

func cleanReadRows(rows, original []string) ([]string, error) {
	if len(rows) != len(original) {
		return nil, fmt.Errorf("row count cannot change; unknown ranges must remain explicit")
	}
	out := make([]string, len(rows))
	for i, row := range rows {
		row = strings.ToUpper(strings.Join(strings.Fields(row), ""))
		if len(row) != len(original[i]) {
			return nil, fmt.Errorf("row %d has incorrect width", i)
		}
		for j := 0; j < len(row); j += 2 {
			pair := row[j : j+2]
			if original[i][j:j+2] == "??" {
				if pair != "??" {
					return nil, fmt.Errorf("row %d contains unknown bytes that cannot be filled by this editor", i)
				}
			} else if _, err := hex.DecodeString(pair); err != nil {
				return nil, fmt.Errorf("row %d requires hexadecimal bytes", i)
			}
		}
		out[i] = row
	}
	return out, nil
}

// NewGUICardFromRead creates a Desk read draft. Tag 0 and empty data avoid
// presenting an incomplete observation as a loadable upstream GUI card. The
// source, edits, offsets and unknown bytes live in the desk_read extension.
func NewGUICardFromRead(name string, result ReadResult, readError string) (GUICard, error) {
	rows, err := result.draftRows()
	if err != nil {
		return GUICard{}, err
	}
	id, err := NewGUIID()
	if err != nil {
		return GUICard{}, err
	}
	source := ReadCardData{Schema: 1, Result: result, Error: readError, Rows: rows}
	c := GUICard{ID: id, Name: name, UID: strings.ToUpper(hex.EncodeToString(result.UID)), ATQA: HexBytes{}, ATS: HexBytes{}, Data: [][]byte{}}
	c = c.withField("desk_read", source)
	return c, c.Validate()
}

// ReadDraft returns nil for an ordinary saved card. An invalid extension fails
// closed instead of silently treating it as an ordinary card.
func (c GUICard) ReadDraft() (*ReadCardData, error) {
	raw, ok := c.fields["desk_read"]
	if !ok {
		return nil, nil
	}
	var v ReadCardData
	if err := json.Unmarshal(raw, &v); err != nil || v.Schema != 1 {
		return nil, fmt.Errorf("invalid Desk read draft")
	}
	original, err := v.Result.draftRows()
	if err != nil {
		return nil, err
	}
	rows, err := cleanReadRows(v.Rows, original)
	if err != nil {
		return nil, err
	}
	if v.Result.Kind == "lf" {
		b, _ := hex.DecodeString(rows[0])
		if _, err := DecodeLFRecord(v.Result.Record.Protocol, v.Result.Record.TagType, b); err != nil {
			return nil, err
		}
	}
	if c.Tag != 0 || len(c.Data) != 0 || c.SAK != 0 || len(c.ATQA) != 0 || len(c.ATS) != 0 || c.UID != strings.ToUpper(hex.EncodeToString(v.Result.UID)) {
		return nil, fmt.Errorf("read draft identity and memory must remain in desk_read")
	}
	v.Rows = rows
	return &v, nil
}

// RequireCardImage prevents exports and loads which would discard read masks,
// offsets or identity uncertainty. Draft JSON remains exportable losslessly.
func (c GUICard) RequireCardImage() error {
	if _, ok := c.fields["desk_read"]; ok {
		return fmt.Errorf("Desk read drafts cannot be loaded or converted to binary/images: identity, capacity and unknown-byte markers must be preserved; export Desk JSON instead")
	}
	return nil
}

// EditData edits stored bytes without changing geometry, identity or extras.
// Unknown bytes remain unknown; the original read observation is preserved.
func (c GUICard) EditData(name string, rows []string) (GUICard, error) {
	draft, err := c.ReadDraft()
	if err != nil {
		return c, err
	}
	if draft != nil {
		original, _ := draft.Result.draftRows()
		draft.Rows, err = cleanReadRows(rows, original)
		if err != nil {
			return c, err
		}
		// Replace only editable rows; preserve unknown extension/source fields.
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(c.fields["desk_read"], &fields); err != nil {
			return c, err
		}
		fields["rows"], _ = json.Marshal(draft.Rows)
		c = c.withField("desk_read", fields)
	} else {
		_, ok := guiClassicBlocks[c.Tag]
		unit := 16
		if _, type2 := guiType2Pages[c.Tag]; type2 {
			ok = true
			unit = 4
		}
		if !ok {
			return c, fmt.Errorf("data editing supports HF memory cards and Desk read drafts")
		}
		if len(rows) > 256 {
			return c, fmt.Errorf("data editor supports at most 256 memory rows")
		}
		if len(rows) != len(c.Data) {
			return c, fmt.Errorf("memory row count cannot change")
		}
		data := make([][]byte, len(rows))
		for i, row := range rows {
			value := strings.Join(strings.Fields(row), "")
			if len(c.Data[i]) == 0 {
				if value != "" {
					return c, fmt.Errorf("unknown row %d must remain empty", i)
				}
				continue
			}
			b, e := hex.DecodeString(value)
			if e != nil || len(b) != unit {
				return c, fmt.Errorf("row %d must contain %d bytes", i, unit)
			}
			data[i] = b
		}
		c.Data = data
	}
	c.Name = name
	return c, c.Validate()
}
