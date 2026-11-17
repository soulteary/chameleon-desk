package main

import (
	"context"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"net/http"
	"time"
)

// hostTaskStart records bounded host/management work without manufacturing
// device-protocol progress or claiming the bridge data socket.
func (ws *webServer) hostTaskStart(ctx context.Context, phase string) error {
	t, _ := ctx.Value(taskKey{}).(*webTask)
	if t == nil {
		return nil
	}
	ts := &ws.tasks
	ts.mu.Lock()
	defer ts.mu.Unlock()
	t.started = true
	t.State = "running"
	t.Phase = phase
	t.Outcome = "in_progress"
	t.Updated = time.Now().UTC()
	if err := ts.persistLocked(t); err != nil {
		t.started = false
		return fmt.Errorf("could not record task start; no host action was started")
	}
	return nil
}
func (ws *webServer) handleEnvironment(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" {
		writeErr(w, 400, fmt.Errorf("environment diagnostics accept no paths or query parameters"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	ws.gateOnce.Do(func() { ws.gate = make(chan struct{}, 1) })
	select {
	case ws.gate <- struct{}{}:
	case <-ctx.Done():
		writeErr(w, 408, ctx.Err())
		return
	}
	defer func() { <-ws.gate }()
	if expected, ok := ctx.Value(sessionContextKey{}).(string); ok && expected != ws.currentSessionID() {
		writeErr(w, 409, fmt.Errorf("device session changed; diagnostic was not started"))
		return
	}
	if err := ws.hostTaskStart(ctx, "environment"); err != nil {
		writeErr(w, 503, err)
		return
	}
	report := chameleon.DiagnoseEnvironment(ws.recoveryTools)
	if bridge, ok := ws.bridge.(interface {
		Diagnose(context.Context) chameleon.BridgeDiagnostic
	}); ok {
		result := bridge.Diagnose(ctx)
		report.Bridge = &result
	} else {
		report.Checks = append(report.Checks, chameleon.BridgeDiagnosticCheck{Code: "managed_bridge_not_configured", Status: "unknown", Message: "No diagnosable managed bridge is configured; existing TCP/native maintenance policy is unchanged"})
	}
	writeJSON(w, report)
}
