package main

import (
	"context"
	"encoding/json"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"net/http"
	"time"
)

func (ws *webServer) handleRFFile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" {
		writeErr(w, 400, fmt.Errorf("file analysis accepts no paths or query parameters"))
		return
	}
	var input chameleon.RFFileRequest
	if err := decodeBody(r, &input); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := input.Validate(); err != nil {
		writeErr(w, 422, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	ws.rfOnce.Do(func() { ws.rfGate = make(chan struct{}, 2) })
	select {
	case ws.rfGate <- struct{}{}:
	case <-ctx.Done():
		writeErr(w, 408, ctx.Err())
		return
	}
	defer func() { <-ws.rfGate }()
	if err := ws.hostTaskStart(ctx, "offline-analysis"); err != nil {
		writeErr(w, 408, err)
		return
	}
	out, err := chameleon.AnalyzeRFFile(ctx, input)
	if err != nil {
		writeErr(w, 422, err)
		return
	}
	data, err := json.Marshal(out)
	if err != nil || len(data) > 4<<20 {
		writeErr(w, 422, fmt.Errorf("analysis result exceeds 4 MiB; select a smaller range"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
