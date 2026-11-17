package chameleon

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestLibraryChangesAtomicMovesFoldersAndDictionaryMetadata(t *testing.T) {
	db, _ := ParseGUIDatabase([]byte(`{"flutter.cards":[],"custom":{"keep":true}}`))
	source, _ := NewGUICardFromRead("source", slotPromotionResult(t, 1001), "")
	db, _ = db.AddCard(source)
	var err error
	db, err = db.ApplyLibraryChange(LibraryChange{Action: "folder-create", Name: "Parent"})
	if err != nil {
		t.Fatal(err)
	}
	folders, _ := db.Folders(false)
	parent := folders[0].ID
	db, err = db.ApplyLibraryChange(LibraryChange{Action: "folder-create", Name: "Child", ParentID: parent})
	if err != nil {
		t.Fatal(err)
	}
	folders, _ = db.Folders(false)
	child := folders[1].ID
	db, err = db.ApplyLibraryChange(LibraryChange{Action: "move", IDs: []string{source.ID}, FolderID: child})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(db)
	for _, change := range []LibraryChange{{Action: "folder-update", ID: parent, Name: "Cycle", ParentID: child}, {Action: "folder-delete", ID: parent}, {Action: "folder-delete", ID: child}, {Action: "move", IDs: []string{source.ID, "missing"}, FolderID: ""}, {Action: "move", IDs: []string{source.ID}, FolderID: "missing"}} {
		if _, err := db.ApplyLibraryChange(change); err == nil {
			t.Fatal("invalid change accepted", change)
		}
		after, _ := json.Marshal(db)
		if !bytes.Equal(before, after) {
			t.Fatal("failed change mutated source")
		}
	}
	db, err = db.ApplyLibraryChange(LibraryChange{Action: "dictionary-create", Dictionaries: true, Name: "keys", Text: "FFFFFFFFFFFF\nA0A1A2A3A4A5\nFFFFFFFFFFFF\n", KeyLength: 12})
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := db.Records(true, "", "all")
	if len(rows) != 1 || rows[0].Keys != 2 {
		t.Fatal(rows)
	}
	v, _ := db.Dictionary(rows[0].ID)
	v.fields["vendor"] = json.RawMessage(`{"keep":true}`)
	db, err = db.ReplaceDictionary(v)
	if err != nil {
		t.Fatal(err)
	}
	db, err = db.ApplyLibraryChange(LibraryChange{Action: "dictionary-update", Dictionaries: true, ID: v.ID, Name: "edited", Text: "000000000000"})
	if err != nil {
		t.Fatal(err)
	}
	v, _ = db.Dictionary(v.ID)
	if string(v.fields["vendor"]) != `{"keep":true}` || v.Name != "edited" || len(v.Keys) != 1 {
		t.Fatal(v)
	}
	raw, _ := json.Marshal(db)
	if !bytes.Contains(raw, []byte(`"custom":{"keep":true}`)) {
		t.Fatal("library metadata lost")
	}
	c, _ := db.Card(source.ID)
	draft, _ := c.ReadDraft()
	if draft.Result.Slot.SHA256 != slotPromotionResult(t, 1001).Slot.SHA256 || c.FolderID() != child {
		t.Fatal("moving changed source snapshot")
	}
}

func TestExternalFileImportPreservesMasksDeclarationsAndOriginalText(t *testing.T) {
	cases := []struct {
		format, text string
		width        int
	}{
		{"pm3-json", `{"FileType":"mfc v2","Card":{"UID":"01020304","ATQA":"0400","SAK":"08"},"blocks":{"0":"01020304000000000000000000000000","2":"????????????????????????????????"},"vendor":"keep"}`, 16},
		{"mct", "+Sector: 0\n01020304000000000000000000000000\n--------------------------------\n", 16},
		{"flipper-nfc", "Filetype: Flipper NFC device\nVersion: 4\nDevice type: Mifare Classic\nUID: 01 02 03 04\nATQA: 00 04\nSAK: 08\nBlock 0: 01020304000000000000000000000000\nBlock 2: ????????????????????????????????\n", 16},
		{"flipper-nfc", "Filetype: Flipper NFC device\nVersion: 4\nDevice type: NTAG/Ultralight\nUID: 04 01 02 03 04 05 06\nATQA: 00 44\nSAK: 00\nPage 0: 04 01 02 ??\nPage 2: ?? ?? ?? ??\n", 4},
		{"flipper-rfid", "Filetype: Flipper RFID key\nVersion: 1\nKey type: EM4100\nData: 01 02 03 04 05\n", 5},
	}
	for _, tc := range cases {
		t.Run(tc.format+string(rune(tc.width)), func(t *testing.T) {
			f, err := ParseCardFile("auto", "source", tc.text)
			if err != nil {
				t.Fatal(err)
			}
			if f.Format != tc.format || f.Raw != tc.text || f.UnitBytes != tc.width || f.ATS != nil {
				t.Fatal(f)
			}
			read, err := f.ReadResult()
			if err != nil {
				t.Fatal(err)
			}
			card, err := NewGUICardFromRead("file", read, "")
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(card)
			card, err = ParseGUICard(raw)
			if err != nil {
				t.Fatal(err)
			}
			draft, _ := card.ReadDraft()
			if draft.Result.File.Raw != tc.text || card.RequireCardImage() == nil || card.ClassicPromotionError() == nil || card.SlotPromotionError() == nil {
				t.Fatal("file declarations became physical evidence")
			}
			if f.UnknownBytes > 0 {
				rows := append([]string(nil), draft.Rows...)
				for i, row := range rows {
					rows[i] = strings.ReplaceAll(row, "??", "00")
				}
				if _, err = card.EditData("bad", rows); err == nil {
					t.Fatal("unknown file bytes filled")
				}
			}
			read.Kind = "classic"
			if _, err = NewGUICardFromRead("fake", read, ""); err == nil {
				t.Fatal("file metadata used as RF evidence")
			}
		})
	}
	for _, text := range []string{"Filetype: Flipper NFC device\nVersion: 4\nVersion: 4", "+Sector: 99\n00", `{"blocks":{"0":"00000000000000000000000000000000","00":"00000000000000000000000000000000"}}`} {
		if _, err := ParseCardFile("auto", "bad", text); err == nil {
			t.Fatal("ambiguous or malformed file accepted")
		}
	}
}

func TestCardComparisonKeepsUnknownBytesSeparateFromEqualZeros(t *testing.T) {
	r := slotPromotionResult(t, 1001)
	r.Read = 32
	r.Complete = false
	r.Slot.Read = 32
	r.Slot.Complete = false
	r.Slot.Data = r.Slot.Data[:512]
	r.Slot.Unknown = slotUnknown(32, 64)
	r.Slot.SHA256 = r.Slot.digest()
	left, _ := NewGUICardFromRead("left", r, "")
	draft, _ := left.ReadDraft()
	rows := append([]string(nil), draft.Rows...)
	rows[1] = "AA" + rows[1][2:]
	right, err := left.EditData("right", rows)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(left)
	diff, err := CompareCards(left, right)
	if err != nil || !diff.Compatible || diff.UnknownComparisons != 512 {
		t.Fatal(diff, err)
	}
	if diff.Differences[0].Unit != 1 || diff.Differences[0].State != "changed" {
		t.Fatal(diff.Differences[0])
	}
	after, _ := json.Marshal(left)
	if !bytes.Equal(before, after) {
		t.Fatal("comparison changed source")
	}
}

func TestExternalFileRejectsDuplicateJSONPropertiesAndInvalidUTF8(t *testing.T) {
	for _, raw := range []string{
		`{"blocks":{"0":"00000000000000000000000000000000"},"blocks":{"1":"00000000000000000000000000000000"}}`,
		`{"blocks":{"0":"00000000000000000000000000000000"},"vendor":{"x":1,"x":2}}`,
		"+Sector: 0\n" + string([]byte{0xff}),
	} {
		if _, err := ParseCardFile("auto", "invalid", raw); err == nil {
			t.Fatal("ambiguous file accepted")
		}
	}
}
