package main

import (
	"context"
	"net/http"

	chameleon "github.com/soulteary/chameleon-desk"
)

func (ws *webServer) handleSlotReadCurrent(w http.ResponseWriter, r *http.Request) {
	var input chameleon.SlotReadRequest
	if err := decodeBody(r, &input); err != nil {
		writeErr(w, 400, err)
		return
	}
	if _, err := input.Requirements(); err != nil {
		writeErr(w, 400, err)
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		if err := ws.featurePreflight(ctx, c, "slot.read-current", &chameleon.FeatureRequest{SlotRead: &input}); err != nil {
			return nil, err
		}
		return c.ReadCurrentSlot(ctx, input)
	})
}
