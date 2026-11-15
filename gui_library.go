package chameleon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// GUIRecordSummary lists a saved card or dictionary without memory or keys.
type GUIRecordSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"` // card or dictionary
	Frequency string `json:"frequency,omitempty"`
	Tag       uint16 `json:"tag,omitempty"`
	TagName   string `json:"tag_name,omitempty"`
	UID       string `json:"uid,omitempty"`
	Units     int    `json:"units,omitempty"`
	Keys      int    `json:"keys,omitempty"`
	KeyLength int    `json:"key_length,omitempty"`
	Color     string `json:"color,omitempty"`
	FolderID  string `json:"folder_id,omitempty"`
}

// NewGUIID returns a random UUIDv4, the identifier form used by the GUI.
func NewGUIID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

var guiColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func guiFrequency(tag uint16) string {
	if tag >= 1000 {
		return "hf"
	}
	return "lf"
}

func (d GUIDatabase) clone() (GUIDatabase, error) {
	if e := d.Validate(); e != nil {
		return d, e
	}
	out := GUIDatabase{Preferences: map[string]json.RawMessage{}, Prefix: d.Prefix}
	for k, v := range d.Preferences {
		out.Preferences[k] = append(json.RawMessage(nil), v...)
	}
	return out, nil
}

func guiCollectionName(dictionaries bool) (records, folders, kind string) {
	if dictionaries {
		return "dictionaries", "dictionary_folders", "dictionary"
	}
	return "cards", "card_folders", "card"
}

// Records summarizes saved cards or dictionaries. query matches the name, tag
// name, UID or ID case-insensitively; sense is "hf", "lf" or ""/"all".
func (d GUIDatabase) Records(dictionaries bool, query, sense string) ([]GUIRecordSummary, error) {
	if sense != "" && sense != "all" && sense != "hf" && sense != "lf" {
		return nil, fmt.Errorf("sense filter must be hf, lf or all")
	}
	name, _, kind := guiCollectionName(dictionaries)
	rows, e := d.collection(name)
	if e != nil {
		return nil, e
	}
	q := strings.ToLower(strings.TrimSpace(query))
	out := []GUIRecordSummary{}
	for _, m := range rows {
		raw, _ := json.Marshal(m)
		s := GUIRecordSummary{Kind: kind, Color: rawString(m, "color"), FolderID: rawString(m, "folderId")}
		if dictionaries {
			var v GUIDictionary
			if e = json.Unmarshal(raw, &v); e != nil {
				return nil, e
			}
			s.ID, s.Name, s.Keys, s.KeyLength = v.ID, v.Name, len(v.Keys), v.KeyLength
		} else {
			v, e := ParseGUICard(raw)
			if e != nil {
				return nil, e
			}
			s.ID, s.Name, s.Tag, s.TagName, s.UID, s.Units, s.Frequency = v.ID, v.Name, v.Tag, TagName(v.Tag), v.UID, len(v.Data), guiFrequency(v.Tag)
			if draft, _ := v.ReadDraft(); draft != nil {
				s.TagName = "Desk " + draft.Result.Kind + " read draft"
				s.Units = len(draft.Rows)
				s.Frequency = "hf"
				if draft.Result.Kind == "lf" {
					s.Frequency = "lf"
					s.UID = draft.Rows[0]
				}
			}
			if sense == "hf" || sense == "lf" {
				if s.Frequency != sense {
					continue
				}
			}
		}
		if q != "" && !strings.Contains(strings.ToLower(s.Name+"\n"+s.TagName+"\n"+s.UID+"\n"+s.ID), q) {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// find resolves an exact ID, then a unique ID prefix, then a unique exact name.
func (d GUIDatabase) find(dictionaries bool, ref string) (int, []map[string]json.RawMessage, error) {
	name, _, kind := guiCollectionName(dictionaries)
	rows, e := d.collection(name)
	if e != nil {
		return 0, nil, e
	}
	if ref == "" {
		return 0, nil, fmt.Errorf("%s ID required", kind)
	}
	for i, m := range rows {
		if rawString(m, "id") == ref {
			return i, rows, nil
		}
	}
	for _, match := range []func(map[string]json.RawMessage) bool{
		func(m map[string]json.RawMessage) bool { return strings.HasPrefix(rawString(m, "id"), ref) },
		func(m map[string]json.RawMessage) bool { return rawString(m, "name") == ref },
	} {
		found := []int{}
		for i, m := range rows {
			if match(m) {
				found = append(found, i)
			}
		}
		if len(found) == 1 {
			return found[0], rows, nil
		}
		if len(found) > 1 {
			return 0, nil, fmt.Errorf("%q matches %d %s records; use the full ID", ref, len(found), kind)
		}
	}
	return 0, nil, fmt.Errorf("%s %q not found", kind, ref)
}

func (d GUIDatabase) Card(ref string) (GUICard, error) {
	i, rows, e := d.find(false, ref)
	if e != nil {
		return GUICard{}, e
	}
	raw, _ := json.Marshal(rows[i])
	return ParseGUICard(raw)
}

func (d GUIDatabase) Dictionary(ref string) (GUIDictionary, error) {
	var v GUIDictionary
	i, rows, e := d.find(true, ref)
	if e != nil {
		return v, e
	}
	raw, _ := json.Marshal(rows[i])
	e = json.Unmarshal(raw, &v)
	return v, e
}

func guiRecordMap(v any) (map[string]json.RawMessage, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	var m map[string]json.RawMessage
	e = json.Unmarshal(raw, &m)
	return m, e
}

func (d GUIDatabase) put(dictionaries bool, record any, replace bool) (GUIDatabase, error) {
	m, e := guiRecordMap(record)
	if e != nil {
		return d, e
	}
	out, e := d.clone()
	if e != nil {
		return d, e
	}
	name, _, kind := guiCollectionName(dictionaries)
	rows, _ := out.collection(name)
	id, at := rawString(m, "id"), -1
	for i, r := range rows {
		if rawString(r, "id") == id {
			at = i
		}
	}
	switch {
	case replace && at < 0:
		return d, fmt.Errorf("%s %s not found", kind, id)
	case !replace && at >= 0:
		return d, fmt.Errorf("%s ID %s already exists; nothing changed", kind, id)
	case replace:
		rows[at] = m
	default:
		rows = append(rows, m)
	}
	out.setCollection(name, rows)
	return out, out.Validate()
}

// AddCard appends a card; duplicate IDs and missing folders are rejected.
func (d GUIDatabase) AddCard(c GUICard) (GUIDatabase, error) { return d.put(false, c, false) }

// ReplaceCard overwrites the record with the same ID.
func (d GUIDatabase) ReplaceCard(c GUICard) (GUIDatabase, error) { return d.put(false, c, true) }
func (d GUIDatabase) AddDictionary(v GUIDictionary) (GUIDatabase, error) {
	return d.put(true, v, false)
}
func (d GUIDatabase) ReplaceDictionary(v GUIDictionary) (GUIDatabase, error) {
	return d.put(true, v, true)
}

// DeleteRecord removes one card or dictionary and returns its full ID.
func (d GUIDatabase) DeleteRecord(dictionaries bool, ref string) (GUIDatabase, string, error) {
	out, e := d.clone()
	if e != nil {
		return d, "", e
	}
	i, rows, e := out.find(dictionaries, ref)
	if e != nil {
		return d, "", e
	}
	id := rawString(rows[i], "id")
	name, _, _ := guiCollectionName(dictionaries)
	out.setCollection(name, append(rows[:i:i], rows[i+1:]...))
	return out, id, out.Validate()
}

// GUICardPatch changes only the fields that are set, like the GUI edit menu.
// ATQA uses the GUI's numeric byte order (0004 for MIFARE Classic 1K).
type GUICardPatch struct {
	Name, UID, Color, FolderID *string
	Tag                        *uint16
	SAK                        *byte
	ATQA, ATS                  []byte
	ClearATS                   bool
}

func (c GUICard) withField(k string, v any) GUICard {
	f := map[string]json.RawMessage{}
	for name, raw := range c.fields {
		f[name] = raw
	}
	if v == nil {
		delete(f, k)
	} else {
		f[k], _ = json.Marshal(v)
	}
	c.fields = f
	return c
}

// Color returns the GUI color (#rrggbb) or "" for the GUI default.
func (c GUICard) Color() string { return rawString(c.fields, "color") }

// FolderID returns the containing folder ID or "" for the root.
func (c GUICard) FolderID() string { return rawString(c.fields, "folderId") }

// WithID returns the card under another ID, e.g. for importing a copy.
func (c GUICard) WithID(id string) GUICard { c.ID = id; return c }

func (c GUICard) Edit(p GUICardPatch) (GUICard, error) {
	if p.Name != nil {
		c.Name = *p.Name
	}
	if p.UID != nil {
		b, e := guiUIDBytes(*p.UID)
		if e != nil || len(b) == 0 {
			return c, fmt.Errorf("UID must be hexadecimal")
		}
		c.UID = strings.ToUpper(hex.EncodeToString(b))
	}
	if p.Tag != nil {
		if strings.HasPrefix(TagName(*p.Tag), "Unknown") || *p.Tag == 0 {
			return c, fmt.Errorf("unknown tag type %d", *p.Tag)
		}
		c.Tag = *p.Tag
	}
	if p.SAK != nil {
		c.SAK = *p.SAK
	}
	if p.ATQA != nil {
		c.ATQA = append(HexBytes(nil), p.ATQA...)
	}
	if p.ATS != nil {
		c.ATS = append(HexBytes(nil), p.ATS...)
	}
	if p.ClearATS {
		c.ATS = HexBytes{}
	}
	if p.Color != nil {
		if !guiColor.MatchString(*p.Color) {
			return c, fmt.Errorf("color must be #rrggbb")
		}
		c = c.withField("color", strings.ToLower(*p.Color))
	}
	if p.FolderID != nil {
		if *p.FolderID == "" {
			c = c.withField("folderId", nil)
		} else {
			c = c.withField("folderId", *p.FolderID)
		}
	}
	if c.Tag >= 1000 {
		if _, e := PlanGUICardLoad(c, 1); e != nil && c.Tag != 3001 {
			return c, fmt.Errorf("edited card is not loadable: %w", e)
		}
	}
	return c, c.Validate()
}

// NewGUICardFromDump builds a saved card from a raw memory dump, like the GUI
// "import .bin" dialog. Classic identity defaults come from block 0 (4-byte
// UID) and the type; pass explicit values for 7-byte UIDs or Type 2 tags.
func NewGUICardFromDump(name string, tag uint16, dump []byte, p GUICardPatch) (GUICard, error) {
	c := GUICard{fields: map[string]json.RawMessage{}, Name: name, Tag: tag, ATS: HexBytes{}}
	id, e := NewGUIID()
	if e != nil {
		return c, e
	}
	c.ID = id
	width := 0
	units := 0
	if n, ok := guiClassicBlocks[tag]; ok {
		width, units = 16, n
		c.ATQA, c.SAK = HexBytes{0x00, 0x04}, 0x08
		if tag == 1000 {
			c.SAK = 0x09
		}
		if tag == 1003 {
			c.ATQA, c.SAK = HexBytes{0x00, 0x02}, 0x18
		}
		if len(dump) >= 4 {
			c.UID = strings.ToUpper(hex.EncodeToString(dump[:4]))
		}
	} else if n, ok := guiType2Pages[tag]; ok {
		width, units = 4, n
		c.ATQA, c.SAK = HexBytes{0x00, 0x44}, 0x00
		if len(dump) >= 8 {
			c.UID = strings.ToUpper(hex.EncodeToString(append(append([]byte{}, dump[0:3]...), dump[4:8]...)))
		}
	} else {
		return c, fmt.Errorf("dump import supports MIFARE Classic and Ultralight/NTAG types only")
	}
	if len(dump) == 0 || len(dump)%width != 0 || len(dump)/width > units {
		return c, fmt.Errorf("%s dump must be 1..%d units of %d bytes; got %d bytes", TagName(tag), units, width, len(dump))
	}
	for pos := 0; pos < len(dump); pos += width {
		c.Data = append(c.Data, append([]byte(nil), dump[pos:pos+width]...))
	}
	p.Tag = nil
	return c.Edit(p)
}

// Dump concatenates memory rows; empty (unknown) rows become zero bytes so
// offsets are preserved, matching the GUI .bin export.
func (c GUICard) Dump() (HexBytes, error) {
	if e := c.RequireCardImage(); e != nil {
		return nil, e
	}
	width := 16
	if _, ok := guiType2Pages[c.Tag]; ok {
		width = 4
	} else if _, ok := guiClassicBlocks[c.Tag]; !ok {
		return nil, fmt.Errorf("%s has no memory dump", TagName(c.Tag))
	}
	out := HexBytes{}
	for i, r := range c.Data {
		switch len(r) {
		case 0:
			out = append(out, make([]byte, width)...)
		case width:
			out = append(out, r...)
		default:
			return nil, fmt.Errorf("memory row %d has %d bytes", i, len(r))
		}
	}
	return out, nil
}
