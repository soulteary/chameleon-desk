package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	chameleon "github.com/soulteary/chameleon-desk"
)

func (ws *webServer) handleLibraryManage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Database     json.RawMessage         `json:"database"`
		Op           string                  `json:"op"`
		Change       chameleon.LibraryChange `json:"change"`
		Dictionaries bool                    `json:"dictionaries"`
		ID           string                  `json:"id"`
		Query        string                  `json:"query"`
		Format       string                  `json:"format"`
		Name         string                  `json:"name"`
		Text         string                  `json:"text"`
		SHA256       string                  `json:"sha256"`
		Bundle       json.RawMessage         `json:"bundle"`
		Left         *chameleon.GUICard      `json:"left"`
		Right        *chameleon.GUICard      `json:"right"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	if body.Op == "diff" {
		if body.Left == nil || body.Right == nil {
			writeErr(w, 400, fmt.Errorf("two explicit card files are required"))
			return
		}
		out, err := chameleon.CompareCards(*body.Left, *body.Right)
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, out)
		return
	}
	if body.Op == "file-preview" || body.Op == "file-import" {
		file, err := chameleon.ParseCardFile(body.Format, body.Name, body.Text)
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		if body.Op == "file-preview" {
			writeJSON(w, file)
			return
		}
		if body.SHA256 != file.SHA256 {
			writeErr(w, 409, fmt.Errorf("preview the current file before importing"))
			return
		}
		result, err := file.ReadResult()
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		card, err := chameleon.NewGUICardFromRead(body.Name, result, "")
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		db, err := chameleon.ParseGUIDatabase(body.Database)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		db, err = db.AddCard(card)
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]any{"database": db, "card": card})
		return
	}
	db, err := chameleon.ParseGUIDatabase(body.Database)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	switch body.Op {
	case "view":
		folders, e := db.Folders(body.Dictionaries)
		if e != nil {
			writeErr(w, 422, e)
			return
		}
		records, e := db.Records(body.Dictionaries, body.Query, "all")
		if e != nil {
			writeErr(w, 422, e)
			return
		}
		writeJSON(w, map[string]any{"folders": folders, "records": records})
	case "change":
		next, e := db.ApplyLibraryChange(body.Change)
		if e != nil {
			writeErr(w, 422, e)
			return
		}
		writeJSON(w, map[string]any{"database": next})
	case "dictionary-show":
		v, e := db.Dictionary(body.ID)
		if e != nil {
			writeErr(w, 404, e)
			return
		}
		text, e := v.Text()
		if e != nil {
			writeErr(w, 422, e)
			return
		}
		writeJSON(w, map[string]any{"dictionary": v, "text": text})
	case "folder-export":
		bundle, e := db.ExportFolder(body.ID, body.Dictionaries)
		if e != nil {
			writeErr(w, 422, e)
			return
		}
		writeJSON(w, json.RawMessage(bundle))
	case "folder-import":
		next, e := db.ImportFolder(body.Bundle)
		if e != nil {
			writeErr(w, 422, e)
			return
		}
		writeJSON(w, map[string]any{"database": next})
	default:
		writeErr(w, 400, fmt.Errorf("unsupported library operation"))
	}
}
