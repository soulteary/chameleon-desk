package chameleon

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

type LibraryFolder struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
}

func (d GUIDatabase) Folders(dictionaries bool) ([]LibraryFolder, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	_, name, _ := guiCollectionName(dictionaries)
	rows, err := d.collection(name)
	if err != nil {
		return nil, err
	}
	out := make([]LibraryFolder, 0, len(rows))
	for _, row := range rows {
		out = append(out, LibraryFolder{rawString(row, "id"), rawString(row, "name"), rawString(row, "parentId")})
	}
	return out, nil
}

// LibraryChange is an explicit, atomic change to a local library. Empty folder
// IDs mean the root. Folder deletion is allowed only after its contents move out.
type LibraryChange struct {
	Action       string   `json:"action"`
	Dictionaries bool     `json:"dictionaries"`
	ID           string   `json:"id,omitempty"`
	IDs          []string `json:"ids,omitempty"`
	Name         string   `json:"name,omitempty"`
	ParentID     string   `json:"parent_id,omitempty"`
	FolderID     string   `json:"folder_id,omitempty"`
	Text         string   `json:"text,omitempty"`
	KeyLength    int      `json:"key_length,omitempty"`
}

func (d GUIDatabase) ApplyLibraryChange(p LibraryChange) (GUIDatabase, error) {
	out, err := d.clone()
	if err != nil {
		return d, err
	}
	recordName, folderName, _ := guiCollectionName(p.Dictionaries)
	folders, _ := out.collection(folderName)
	records, _ := out.collection(recordName)
	find := func(rows []map[string]json.RawMessage, id string) int {
		for i, row := range rows {
			if rawString(row, "id") == id {
				return i
			}
		}
		return -1
	}
	name := func() error {
		if strings.TrimSpace(p.Name) == "" || len(p.Name) > 256 || !utf8.ValidString(p.Name) {
			return fmt.Errorf("name must contain 1..256 bytes")
		}
		return nil
	}
	switch p.Action {
	case "folder-create", "folder-update":
		if err = name(); err != nil {
			return d, err
		}
		var row map[string]json.RawMessage
		if p.Action == "folder-create" {
			if p.ID != "" {
				return d, fmt.Errorf("new folders allocate a fresh ID")
			}
			p.ID, err = NewGUIID()
			if err != nil {
				return d, err
			}
			row = map[string]json.RawMessage{}
			putString(row, "id", p.ID)
			folders = append(folders, row)
		} else {
			i := find(folders, p.ID)
			if i < 0 {
				return d, fmt.Errorf("folder not found")
			}
			row = folders[i]
		}
		putString(row, "name", p.Name)
		putString(row, "parentId", p.ParentID)
	case "folder-delete":
		i := find(folders, p.ID)
		if i < 0 {
			return d, fmt.Errorf("folder not found")
		}
		for _, row := range folders {
			if rawString(row, "parentId") == p.ID {
				return d, fmt.Errorf("move child folders before deleting this folder")
			}
		}
		for _, row := range records {
			if rawString(row, "folderId") == p.ID {
				return d, fmt.Errorf("move records before deleting this folder")
			}
		}
		folders = append(folders[:i], folders[i+1:]...)
	case "move":
		if len(p.IDs) < 1 || len(p.IDs) > 1000 {
			return d, fmt.Errorf("select 1..1000 record IDs")
		}
		seen := map[string]bool{}
		for _, id := range p.IDs {
			i := find(records, id)
			if i < 0 || seen[id] {
				return d, fmt.Errorf("missing or repeated record ID")
			}
			seen[id] = true
			putString(records[i], "folderId", p.FolderID)
		}
	case "dictionary-create", "dictionary-update":
		if !p.Dictionaries {
			return d, fmt.Errorf("dictionary action requires dictionary collection")
		}
		if err = name(); err != nil {
			return d, err
		}
		var v GUIDictionary
		if p.Action == "dictionary-create" {
			if p.ID != "" {
				return d, fmt.Errorf("new dictionaries allocate a fresh ID")
			}
			v.ID, err = NewGUIID()
			if err != nil {
				return d, err
			}
			v.KeyLength = p.KeyLength
			if v.KeyLength == 0 {
				v.KeyLength = 12
			}
		} else {
			v, err = out.Dictionary(p.ID)
			if err != nil {
				return d, err
			}
			if p.KeyLength != 0 && p.KeyLength != v.KeyLength {
				return d, fmt.Errorf("existing dictionary key length cannot change")
			}
		}
		v.Name = p.Name
		v, err = v.WithText(p.Text)
		if err != nil {
			return d, err
		}
		row, e := guiRecordMap(v)
		if e != nil {
			return d, e
		}
		putString(row, "folderId", p.FolderID)
		if p.Action == "dictionary-create" {
			records = append(records, row)
		} else {
			i := find(records, p.ID)
			if i < 0 {
				return d, fmt.Errorf("dictionary not found")
			}
			records[i] = row
		}
	case "dictionary-delete":
		if !p.Dictionaries {
			return d, fmt.Errorf("dictionary action requires dictionary collection")
		}
		i := find(records, p.ID)
		if i < 0 {
			return d, fmt.Errorf("dictionary not found")
		}
		records = append(records[:i], records[i+1:]...)
	default:
		return d, fmt.Errorf("unsupported library change")
	}
	out.setCollection(folderName, folders)
	out.setCollection(recordName, records)
	if err = out.Validate(); err != nil {
		return d, err
	}
	return out, nil
}
