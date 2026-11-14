package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	chameleon "github.com/soulteary/chameleon-desk"
)

func (ws *webServer) featureContext(device *chameleon.FeatureDevice) chameleon.FeatureContext {
	return chameleon.FeatureContext{Device: device, WritesAllowed: ws.allowWrites, BridgeConfigured: ws.bridge != nil}
}
func (ws *webServer) featureStates(device *chameleon.FeatureDevice) []chameleon.FeatureAvailability {
	states, _ := chameleon.EvaluateFeatures("web", ws.featureContext(device))
	return states
}
func (ws *webServer) handleFeatureDevice(w http.ResponseWriter, r *http.Request) {
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		d, e := c.FeatureDevice(ctx)
		if e != nil {
			return nil, e
		}
		return map[string]any{"schema_version": chameleon.FeatureSchemaVersion, "device": d, "states": ws.featureStates(&d)}, nil
	})
}

// Feature checks are offline previews based on submitted observations. They are
// not authorization and never replace the actual device-operation guards.
func (ws *webServer) handleFeatureCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var body struct {
		ID       string                   `json:"id"`
		Device   *chameleon.FeatureDevice `json:"device"`
		Card     json.RawMessage          `json:"card"`
		Slot     int                      `json:"slot"`
		Persist  bool                     `json:"persist"`
		Activate bool                     `json:"activate"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	if body.ID != "slot.load-card" {
		writeErr(w, 400, fmt.Errorf("only slot.load-card parameter previews are exposed"))
		return
	}
	request := chameleon.FeatureRequest{Slot: body.Slot, Persist: body.Persist, Activate: body.Activate}
	var result chameleon.FeatureAvailability
	card, err := chameleon.ParseGUICard(body.Card)
	if err == nil {
		request.Card = &card
		result, err = chameleon.EvaluateFeature(body.ID, "web", ws.featureContext(body.Device), &request)
	}
	if err != nil {
		result = chameleon.FeatureAvailability{ID: body.ID, State: "unavailable", Reasons: []chameleon.FeatureReason{{Code: "invalid_parameters", Message: err.Error()}}, RequiredCommands: []chameleon.Command{}}
	}
	writeJSON(w, map[string]any{"schema_version": chameleon.FeatureSchemaVersion, "state": result})
}
