package chameleon

import (
	"encoding/json"
	"testing"
)

func dictionaryDB(t *testing.T) GUIDatabase {
	t.Helper()
	d := NewGUIDatabase()
	d.Preferences["theme"] = json.RawMessage(`"dark"`)
	d.setCollection("dictionary_folders", []map[string]json.RawMessage{{"id": json.RawMessage(`"root"`), "name": json.RawMessage(`"Keys"`)}, {"id": json.RawMessage(`"child"`), "name": json.RawMessage(`"Sub"`), "parentId": json.RawMessage(`"root"`)}})
	d.setCollection("dictionaries", []map[string]json.RawMessage{{"id": json.RawMessage(`"d1"`), "name": json.RawMessage(`"Test"`), "keys": json.RawMessage(`[[255,255,255,255,255,255]]`), "folderId": json.RawMessage(`"child"`)}})
	if e := d.Validate(); e != nil {
		t.Fatal(e)
	}
	return d
}
func TestGUIDatabaseBundleAndMerge(t *testing.T) {
	d := dictionaryDB(t)
	b, e := json.Marshal(d)
	if e != nil {
		t.Fatal(e)
	}
	copy, e := ParseGUIDatabase(b)
	if e != nil {
		t.Fatal(e)
	}
	bundle, e := copy.ExportFolder("root", true)
	if e != nil {
		t.Fatal(e)
	}
	imported, e := NewGUIDatabase().ImportFolder(bundle)
	if e != nil || imported.CollectionCounts()["dictionaries"] != 1 {
		t.Fatal(imported, e)
	}
	if _, e = d.Merge(imported); e == nil {
		t.Fatal("duplicate IDs accepted")
	}
	if rawString(copy.Preferences, "theme") != "dark" {
		t.Fatal("settings lost")
	}
}

func TestGUIDatabaseRejectsCyclesAndBrokenRefs(t *testing.T) {
	d := dictionaryDB(t)
	rows, _ := d.collection("dictionary_folders")
	putString(rows[0], "parentId", "child")
	d.setCollection("dictionary_folders", rows)
	if e := d.Validate(); e == nil {
		t.Fatal("cycle accepted")
	}
	if _, e := ParseGUIDatabase([]byte(`{"cards":[],"flutter.cards":[]}`)); e == nil {
		t.Fatal("mixed namespace accepted")
	}
}
func TestImportOfficialNestedFolderBundle(t *testing.T) {
	d := dictionaryDB(t)
	b, e := d.ExportFolder("child", true)
	if e != nil {
		t.Fatal(e)
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(b, &raw)
	var folders []map[string]json.RawMessage
	_ = json.Unmarshal(raw["folders"], &folders)
	putString(folders[0], "parentId", "external-parent")
	raw["folders"], _ = json.Marshal(folders)
	b, _ = json.Marshal(raw)
	v, e := NewGUIDatabase().ImportFolder(b)
	if e != nil || v.CollectionCounts()["dictionaries"] != 1 {
		t.Fatal(v, e)
	}
}
