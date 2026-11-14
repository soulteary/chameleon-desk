package chameleon

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GUIDatabase is the SharedPreferences key/value representation. Unknown keys,
// folder/card metadata and GUI settings survive a read/write cycle.
type GUIDatabase struct {
	Preferences map[string]json.RawMessage `json:"-"`
	Prefix      string                     `json:"-"`
}

var guiCollections = []string{"cards", "card_folders", "dictionaries", "dictionary_folders"}

func ParseGUIDatabase(data []byte) (GUIDatabase, error) {
	d := GUIDatabase{}
	if len(data) > 32<<20 {
		return d, fmt.Errorf("GUI database exceeds 32MiB")
	}
	if e := json.Unmarshal(data, &d.Preferences); e != nil {
		return d, e
	}
	if d.Preferences == nil {
		return d, fmt.Errorf("database must be an object")
	}
	hasPlain, hasFlutter := false, false
	for _, k := range guiCollections {
		hasPlain = hasPlain || d.Preferences[k] != nil
		hasFlutter = hasFlutter || d.Preferences["flutter."+k] != nil
	}
	if hasPlain && hasFlutter {
		return d, fmt.Errorf("mixed flutter-prefixed and unprefixed collections")
	}
	if hasFlutter {
		d.Prefix = "flutter."
	}
	return d, d.Validate()
}
func (d GUIDatabase) collection(name string) ([]map[string]json.RawMessage, error) {
	raw := d.Preferences[d.Prefix+name]
	if raw == nil {
		return []map[string]json.RawMessage{}, nil
	}
	var stringsList []string
	if e := json.Unmarshal(raw, &stringsList); e != nil {
		return nil, fmt.Errorf("%s must be a SharedPreferences string list: %w", name, e)
	}
	out := make([]map[string]json.RawMessage, 0, len(stringsList))
	for _, s := range stringsList {
		var m map[string]json.RawMessage
		if e := json.Unmarshal([]byte(s), &m); e != nil || m == nil {
			return nil, fmt.Errorf("invalid %s record", name)
		}
		out = append(out, m)
	}
	return out, nil
}
func rawString(m map[string]json.RawMessage, k string) string {
	var s string
	_ = json.Unmarshal(m[k], &s)
	return s
}
func putString(m map[string]json.RawMessage, k, v string) { m[k], _ = json.Marshal(v) }
func (d *GUIDatabase) setCollection(name string, rows []map[string]json.RawMessage) {
	list := make([]string, len(rows))
	for i, m := range rows {
		b, _ := json.Marshal(m)
		list[i] = string(b)
	}
	d.Preferences[d.Prefix+name], _ = json.Marshal(list)
}
func (d GUIDatabase) Validate() error {
	for _, pair := range [][2]string{{"card_folders", "cards"}, {"dictionary_folders", "dictionaries"}} {
		folders, e := d.collection(pair[0])
		if e != nil {
			return e
		}
		parents := map[string]string{}
		for _, f := range folders {
			id := rawString(f, "id")
			var folderName string
			if e := json.Unmarshal(f["name"], &folderName); e != nil {
				return fmt.Errorf("folder name must be a string")
			}
			if id == "" {
				return fmt.Errorf("folder ID required")
			}
			if _, ok := parents[id]; ok {
				return fmt.Errorf("duplicate folder ID %s", id)
			}
			parents[id] = rawString(f, "parentId")
		}
		for id := range parents {
			seen := map[string]bool{}
			for p := id; p != ""; p = parents[p] {
				if seen[p] {
					return fmt.Errorf("folder cycle")
				}
				seen[p] = true
				if _, ok := parents[p]; !ok {
					return fmt.Errorf("missing parent folder %s", p)
				}
			}
		}
		rows, e := d.collection(pair[1])
		if e != nil {
			return e
		}
		seen := map[string]bool{}
		for _, m := range rows {
			id := rawString(m, "id")
			if id == "" || seen[id] {
				return fmt.Errorf("missing/duplicate record ID")
			}
			seen[id] = true
			if f := rawString(m, "folderId"); f != "" {
				if _, ok := parents[f]; !ok {
					return fmt.Errorf("record references missing folder %s", f)
				}
			}
			b, _ := json.Marshal(m)
			if pair[1] == "cards" {
				if _, e = ParseGUICard(b); e != nil {
					return e
				}
			} else {
				var v GUIDictionary
				if e = json.Unmarshal(b, &v); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
func (d GUIDatabase) MarshalJSON() ([]byte, error) {
	if e := d.Validate(); e != nil {
		return nil, e
	}
	return json.Marshal(d.Preferences)
}

// Merge is atomic in memory: duplicate IDs and broken references abort before
// a result is returned. Unknown destination settings win on key conflicts.
func (d GUIDatabase) Merge(other GUIDatabase) (GUIDatabase, error) {
	b, e := json.Marshal(d)
	if e != nil {
		return d, e
	}
	out, e := ParseGUIDatabase(b)
	if e != nil {
		return d, e
	}
	if e = other.Validate(); e != nil {
		return d, e
	}
	for _, name := range guiCollections {
		a, _ := out.collection(name)
		b, _ := other.collection(name)
		out.setCollection(name, append(a, b...))
	}
	for k, v := range other.Preferences {
		known := false
		for _, n := range guiCollections {
			known = known || k == other.Prefix+n
		}
		if !known && out.Preferences[k] == nil {
			out.Preferences[k] = append(json.RawMessage(nil), v...)
		}
	}
	return out, out.Validate()
}

// ExportFolder emits the exact version-1 card/dictionary folder bundle schema.
func (d GUIDatabase) ExportFolder(root string, dictionaries bool) ([]byte, error) {
	if e := d.Validate(); e != nil {
		return nil, e
	}
	folderName, recordName, format := "card_folders", "cards", "chameleon-ultra-gui-folder"
	if dictionaries {
		folderName, recordName, format = "dictionary_folders", "dictionaries", "chameleon-ultra-gui-dictionary-folder"
	}
	folders, _ := d.collection(folderName)
	records, _ := d.collection(recordName)
	included := map[string]bool{root: true}
	found := false
	for _, f := range folders {
		found = found || rawString(f, "id") == root
	}
	if !found {
		return nil, fmt.Errorf("folder not found")
	}
	for changed := true; changed; {
		changed = false
		for _, f := range folders {
			if included[rawString(f, "parentId")] && !included[rawString(f, "id")] {
				included[rawString(f, "id")] = true
				changed = true
			}
		}
	}
	fs := []map[string]json.RawMessage{}
	rs := []map[string]json.RawMessage{}
	for _, f := range folders {
		if included[rawString(f, "id")] {
			copy := map[string]json.RawMessage{}
			for k, v := range f {
				copy[k] = v
			}
			if rawString(f, "id") == root {
				delete(copy, "parentId")
			}
			fs = append(fs, copy)
		}
	}
	for _, r := range records {
		if included[rawString(r, "folderId")] {
			rs = append(rs, r)
		}
	}
	return json.Marshal(map[string]any{"format": format, "version": 1, "rootFolderId": root, "folders": fs, recordName: rs})
}
func (d GUIDatabase) ImportFolder(data []byte) (GUIDatabase, error) {
	if len(data) > 32<<20 {
		return d, fmt.Errorf("bundle exceeds limit")
	}
	var b struct {
		Format       string                       `json:"format"`
		Version      int                          `json:"version"`
		Root         string                       `json:"rootFolderId"`
		Folders      []map[string]json.RawMessage `json:"folders"`
		Cards        []map[string]json.RawMessage `json:"cards"`
		Dictionaries []map[string]json.RawMessage `json:"dictionaries"`
	}
	if e := json.Unmarshal(data, &b); e != nil {
		return d, e
	}
	if b.Version != 1 {
		return d, fmt.Errorf("unsupported bundle version")
	}
	ids := map[string]bool{}
	for _, f := range b.Folders {
		ids[rawString(f, "id")] = true
	}
	for _, f := range b.Folders {
		if rawString(f, "id") == b.Root {
			if p := rawString(f, "parentId"); p != "" && !ids[p] {
				delete(f, "parentId")
			}
		}
	}
	other := GUIDatabase{Preferences: map[string]json.RawMessage{}}
	kind := "cards"
	switch b.Format {
	case "chameleon-ultra-gui-folder":
		other.setCollection("cards", b.Cards)
		other.setCollection("card_folders", b.Folders)
	case "chameleon-ultra-gui-dictionary-folder":
		kind = "dictionaries"
		other.setCollection("dictionaries", b.Dictionaries)
		other.setCollection("dictionary_folders", b.Folders)
	default:
		return d, fmt.Errorf("unsupported bundle format")
	}
	if e := other.Validate(); e != nil {
		return d, e
	}
	exported, e := other.ExportFolder(b.Root, kind == "dictionaries")
	if e != nil {
		return d, e
	}
	var check struct {
		Folders []any `json:"folders"`
	}
	_ = json.Unmarshal(exported, &check)
	if len(check.Folders) != len(b.Folders) {
		return d, fmt.Errorf("bundle contains folders outside root")
	}
	return d.Merge(other)
}
func NewGUIDatabase() GUIDatabase { return GUIDatabase{Preferences: map[string]json.RawMessage{}} }

// GUICollectionCounts provides summaries without exposing card data or keys.
func (d GUIDatabase) CollectionCounts() map[string]int {
	m := map[string]int{}
	for _, k := range guiCollections {
		v, _ := d.collection(k)
		m[strings.TrimPrefix(k, d.Prefix)] = len(v)
	}
	return m
}
