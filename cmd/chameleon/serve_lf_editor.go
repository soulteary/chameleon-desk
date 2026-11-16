package main

import (
	"context"
	"encoding/json"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"net/http"
)

func (ws *webServer) handleLFIdentityEdit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Database json.RawMessage           `json:"database"`
		ID       string                    `json:"id"`
		Name     string                    `json:"name"`
		Identity chameleon.LFIdentityInput `json:"identity"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	db, err := chameleon.ParseGUIDatabase(body.Database)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	card, err := db.Card(body.ID)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	if body.Identity.Protocol != "ioprox" {
		writeErr(w, 400, fmt.Errorf("only the ioProx codec requires this device endpoint"))
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		if err := ws.featurePreflight(ctx, c, "library.edit-lf-device", &chameleon.FeatureRequest{LFIdentity: &body.Identity}); err != nil {
			return nil, err
		}
		updated, err := c.EditLFIdentity(ctx, card, body.Name, body.Identity)
		if err != nil {
			return nil, err
		}
		next, err := db.ReplaceCard(updated)
		if err != nil {
			return nil, err
		}
		return map[string]any{"database": next, "card": updated}, nil
	})
}
