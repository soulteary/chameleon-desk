package chameleon

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// GUIDictionary preserves metadata and numeric-array key encoding used by GUI.
type GUIDictionary struct {
	fields    map[string]json.RawMessage
	ID, Name  string
	KeyLength int
	Keys      []HexBytes
}

func (d *GUIDictionary) UnmarshalJSON(b []byte) error {
	var f map[string]json.RawMessage
	if e := json.Unmarshal(b, &f); e != nil {
		return e
	}
	v := GUIDictionary{fields: f, KeyLength: 12}
	for name, dst := range map[string]any{"id": &v.ID, "name": &v.Name} {
		if e := json.Unmarshal(f[name], dst); e != nil {
			return e
		}
	}
	if raw := f["keyLength"]; raw != nil {
		if e := json.Unmarshal(raw, &v.KeyLength); e != nil {
			return e
		}
	}
	var keys []json.RawMessage
	if e := json.Unmarshal(f["keys"], &keys); e != nil {
		return e
	}
	for _, raw := range keys {
		k, e := numericBytes(raw)
		if e != nil {
			return e
		}
		v.Keys = append(v.Keys, k)
	}
	if e := v.Validate(); e != nil {
		return e
	}
	*d = v
	return nil
}
func (d GUIDictionary) Validate() error {
	if d.ID == "" || d.KeyLength < 2 || d.KeyLength > 64 || d.KeyLength%2 != 0 {
		return fmt.Errorf("invalid dictionary ID or hexadecimal key length")
	}
	if len(d.Keys) > 100000 {
		return fmt.Errorf("too many dictionary keys")
	}
	for _, k := range d.Keys {
		if len(k)*2 != d.KeyLength {
			return fmt.Errorf("dictionary key length mismatch")
		}
	}
	return nil
}
func (d GUIDictionary) MarshalJSON() ([]byte, error) {
	if e := d.Validate(); e != nil {
		return nil, e
	}
	f := map[string]json.RawMessage{}
	for k, v := range d.fields {
		f[k] = v
	}
	put := func(k string, v any) { b, _ := json.Marshal(v); f[k] = b }
	put("id", d.ID)
	put("name", d.Name)
	put("keyLength", d.KeyLength)
	keys := make([][]int, len(d.Keys))
	for i, k := range d.Keys {
		keys[i] = ints(k)
	}
	put("keys", keys)
	return json.Marshal(f)
}
func (d GUIDictionary) Text() (string, error) {
	if e := d.Validate(); e != nil {
		return "", e
	}
	var b strings.Builder
	for _, k := range d.Keys {
		b.WriteString(k.String())
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// WithText replaces keys, preserves metadata, and deduplicates in input order.
func (d GUIDictionary) WithText(text string) (GUIDictionary, error) {
	if len(text) > 8<<20 {
		return d, fmt.Errorf("dictionary text exceeds 8MiB")
	}
	var keys []HexBytes
	seen := map[string]bool{}
	s := bufio.NewScanner(strings.NewReader(text))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(line) != d.KeyLength {
			return d, fmt.Errorf("dictionary key must have %d hexadecimal digits", d.KeyLength)
		}
		k, e := hex.DecodeString(line)
		if e != nil {
			return d, e
		}
		id := hex.EncodeToString(k)
		if !seen[id] {
			seen[id] = true
			keys = append(keys, k)
		}
	}
	if e := s.Err(); e != nil {
		return d, e
	}
	d.Keys = keys
	return d, d.Validate()
}
