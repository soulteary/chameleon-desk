package main

import (
	"context"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"net/http"
	"time"
)

func (ws *webServer) handleRFCapture(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/rf/hf-sniff" {
		markConfigurationWrite(r.Context(), ws, false)
	}
	var input chameleon.RFCaptureRequest
	if r.URL.RawQuery != "" {
		writeErr(w, 400, fmt.Errorf("capture accepts no query parameters"))
		return
	}
	if err := decodeBody(r, &input); err != nil {
		writeErr(w, 400, err)
		return
	}
	if _, err := input.Requirements(); err != nil {
		writeErr(w, 422, err)
		return
	}
	feature := "rf.hf-trace"
	if r.URL.Path == "/api/rf/lf-capture" {
		feature = "rf.lf-samples"
	}
	if input.FeatureID() != feature || r.URL.Path == "/api/rf/hf-sniff" && input.Action != "hf-sniff" || r.URL.Path == "/api/rf/hf-auth" && input.Action != "hf-auth" {
		writeErr(w, 422, fmt.Errorf("capture action differs from registered feature"))
		return
	}
	if input.Action == "hf-sniff" && !ws.needWrites(w) {
		return
	}
	if err := input.ValidateTimeout(ws.timeout); err != nil {
		writeErr(w, 422, err)
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		if err := ws.featurePreflight(ctx, c, feature, &chameleon.FeatureRequest{RFCapture: &input}); err != nil {
			return nil, err
		}
		if input.Action == "hf-sniff" {
			markConfigurationWrite(ctx, ws, true)
		}
		out, err := c.CaptureRF(ctx, input)
		out.SessionID, _ = r.Context().Value(sessionContextKey{}).(string)
		return out, err
	}, max(30*time.Second, time.Duration(input.DurationMS)*time.Millisecond+10*time.Second))
}
