package chameleon

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// GUICard preserves unknown fields, including GUI-only metadata and extra data.
// Byte arrays use JSON numbers, as required by CardSave, never base64 strings.
type GUICard struct {
	fields        map[string]json.RawMessage
	ID, Name, UID string
	Tag           uint16
	SAK           byte
	ATQA, ATS     HexBytes
	Data          [][]byte
}

func numericBytes(raw json.RawMessage) ([]byte, error) {
	var ints []int
	if len(raw) == 0 {
		return []byte{}, nil
	}
	if e := json.Unmarshal(raw, &ints); e != nil {
		return nil, e
	}
	out := make([]byte, len(ints))
	for i, n := range ints {
		if n < 0 || n > 255 {
			return nil, fmt.Errorf("byte value out of range")
		}
		out[i] = byte(n)
	}
	return out, nil
}
func (c *GUICard) UnmarshalJSON(data []byte) error {
	var f map[string]json.RawMessage
	if e := json.Unmarshal(data, &f); e != nil {
		return e
	}
	if f == nil {
		return fmt.Errorf("card must be an object")
	}
	var v GUICard
	v.fields = f
	for name, dst := range map[string]any{"id": &v.ID, "name": &v.Name, "uid": &v.UID, "tag": &v.Tag, "sak": &v.SAK} {
		if f[name] == nil {
			return fmt.Errorf("missing GUI card field %s", name)
		}
		if e := json.Unmarshal(f[name], dst); e != nil {
			return e
		}
	}
	var e error
	v.ATQA, e = numericBytes(f["atqa"])
	if e != nil {
		return e
	}
	v.ATS, e = numericBytes(f["ats"])
	if e != nil {
		return e
	}
	var rows []json.RawMessage
	if e = json.Unmarshal(f["data"], &rows); e != nil {
		return e
	}
	for _, r := range rows {
		b, e := numericBytes(r)
		if e != nil {
			return e
		}
		v.Data = append(v.Data, b)
	}
	if e = v.Validate(); e != nil {
		return e
	}
	*c = v
	return nil
}
func ints(b []byte) []int {
	v := make([]int, len(b))
	for i, n := range b {
		v[i] = int(n)
	}
	return v
}
func (c GUICard) MarshalJSON() ([]byte, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	f := map[string]json.RawMessage{}
	for k, v := range c.fields {
		f[k] = v
	}
	put := func(k string, v any) { b, _ := json.Marshal(v); f[k] = b }
	put("id", c.ID)
	put("name", c.Name)
	put("uid", c.UID)
	put("tag", c.Tag)
	put("sak", c.SAK)
	put("atqa", ints(c.ATQA))
	put("ats", ints(c.ATS))
	rows := make([][]int, len(c.Data))
	for i, r := range c.Data {
		rows[i] = ints(r)
	}
	put("data", rows)
	if f["extra"] == nil {
		put("extra", map[string]any{})
	}
	return json.Marshal(f)
}
func (c GUICard) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("GUI card ID required")
	}
	uid, e := hex.DecodeString(strings.NewReplacer(" ", "", ":", "").Replace(c.UID))
	if e != nil {
		return fmt.Errorf("invalid GUI UID: %w", e)
	}
	if c.Tag >= 1000 {
		if len(uid) != 4 && len(uid) != 7 && len(uid) != 10 {
			return fmt.Errorf("HF UID must be 4,7,10 bytes")
		}
		if len(c.ATQA) != 2 || len(c.ATS) > 254 {
			return fmt.Errorf("invalid HF identity")
		}
	}
	total := 0
	for _, r := range c.Data {
		total += len(r)
		if total > 1<<20 {
			return fmt.Errorf("GUI card memory exceeds limit")
		}
	}
	_, e = c.ReadDraft()
	return e
}

// EmulatorImage converts supported HF memory without dropping GUI metadata in
// the original card. ATS, UID, signatures, counters and settings are separate.
func (c GUICard) EmulatorImage() (EmulatorImage, error) {
	if e := c.RequireCardImage(); e != nil {
		return EmulatorImage{}, e
	}
	i := EmulatorImage{Schema: 1, TagType: c.Tag, UnitBytes: 16}
	if c.Tag >= 1100 && c.Tag <= 1108 {
		i.UnitBytes = 4
	}
	for _, r := range c.Data {
		if len(r) != i.UnitBytes {
			return i, fmt.Errorf("GUI row width mismatch")
		}
		i.Data = append(i.Data, r...)
	}
	i.SHA256 = imageDigest(i.Data)
	return i, i.Validate()
}

// WithImage replaces memory in an existing GUI template, preserving identity
// and extras. Types must match, preventing accidental identity fabrication.
func (c GUICard) WithImage(i EmulatorImage) (GUICard, error) {
	if e := c.RequireCardImage(); e != nil {
		return c, e
	}
	if e := i.Validate(); e != nil {
		return c, e
	}
	if i.TagType != c.Tag {
		return c, fmt.Errorf("GUI template and image tag types differ")
	}
	c.Data = nil
	for pos := 0; pos < len(i.Data); pos += i.UnitBytes {
		c.Data = append(c.Data, append([]byte(nil), i.Data[pos:pos+i.UnitBytes]...))
	}
	return c, nil
}
func ParseGUICard(data []byte) (GUICard, error) {
	var c GUICard
	if len(data) > 4<<20 {
		return c, fmt.Errorf("GUI card file exceeds limit")
	}
	e := json.Unmarshal(data, &c)
	return c, e
}
