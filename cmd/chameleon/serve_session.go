package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

type sessionContextKey struct{}

func hardwareRequest(path string) bool {
	switch path {
	case "/api/settings/read", "/api/settings/configure", "/api/slot/details", "/api/slot/edit", "/api/info", "/api/scan/hf", "/api/scan/lf", "/api/slot/activate", "/api/slot/load-card", "/api/features/device", "/api/firmware/check":
		return true
	}
	return false
}
func (ws *webServer) currentSessionID() string {
	ws.sessionMu.Lock()
	defer ws.sessionMu.Unlock()
	return ws.sessionID
}
func (ws *webServer) rotateSessionID() {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	ws.sessionMu.Lock()
	ws.sessionID = hex.EncodeToString(b)
	ws.sessionMu.Unlock()
}

// Called only while holding the common operation gate (or before serving).
func (ws *webServer) closeSession() {
	ws.sessionMu.Lock()
	c := ws.sessionClient
	ws.sessionClient = nil
	ws.sessionTarget = ""
	ws.sessionMu.Unlock()
	ws.rotateSessionID()
	if c != nil {
		_ = c.Close()
	}
}
func (ws *webServer) connectSession(ctx context.Context, target string) error {
	ws.closeSession()
	open := ws.openDevice
	if open == nil {
		open = chameleon.OpenContext
	}
	c, err := open(ctx, target, ws.timeout)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		_ = c.Close()
		return err
	}
	ws.sessionMu.Lock()
	ws.sessionClient = c
	ws.sessionTarget = target
	ws.sessionMu.Unlock()
	return nil
}
func validateSessionTarget(target string, ports []string) error {
	if strings.HasPrefix(target, "tcp:") {
		host, port, err := net.SplitHostPort(strings.TrimPrefix(target, "tcp:"))
		n, e := strconv.Atoi(port)
		if err != nil || e != nil || host == "" || n < 1 || n > 65535 {
			return fmt.Errorf("TCP target must be tcp:host:port")
		}
		return nil
	}
	for _, p := range ports {
		if target == p {
			return nil
		}
	}
	return fmt.Errorf("select a listed serial port or enter tcp:host:port")
}
func (ws *webServer) sessionStatus() any {
	ws.sessionMu.Lock()
	id, target, c := ws.sessionID, ws.sessionTarget, ws.sessionClient
	ws.sessionMu.Unlock()
	connected := c != nil && !c.IsClosed()
	kind := "direct"
	if ws.bridge != nil {
		kind = "ble-bridge"
		c = ws.getBridgeClient()
		connected = c != nil && !c.IsClosed()
	}
	out := map[string]any{"session_id": id, "connected": connected, "target": target, "kind": kind}
	if connected {
		out["transport"] = c.TransportInfo()
	}
	return out
}
func (ws *webServer) handleSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/api/session/ports" {
		ports, err := ws.serialPorts()
		if err != nil {
			writeErr(w, 502, err)
			return
		}
		if ports == nil {
			ports = []string{}
		}
		writeJSON(w, map[string]any{"ports": ports})
		return
	}
	// Status and transitions take the same gate as hardware work. A transition
	// waits for active work; queued work retains its original session identity.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	ws.gateOnce.Do(func() { ws.gate = make(chan struct{}, 1) })
	select {
	case ws.gate <- struct{}{}:
	case <-ctx.Done():
		writeErr(w, 408, ctx.Err())
		return
	}
	defer func() { <-ws.gate }()
	if ctx.Err() != nil {
		writeErr(w, 408, ctx.Err())
		return
	}
	if r.URL.Path == "/api/session/status" {
		writeJSON(w, ws.sessionStatus())
		return
	}
	if ws.bridge != nil {
		writeErr(w, 409, fmt.Errorf("use the configured BLE bridge controls"))
		return
	}
	var input struct {
		Target    string `json:"target,omitempty"`
		SessionID string `json:"session_id"`
	}
	if err := decodeBody(r, &input); err != nil {
		writeErr(w, 400, err)
		return
	}
	if input.SessionID != ws.currentSessionID() {
		writeErr(w, 409, fmt.Errorf("device session changed; refresh connection status"))
		return
	}
	switch r.URL.Path {
	case "/api/session/connect":
		var ports []string
		if !strings.HasPrefix(input.Target, "tcp:") {
			var err error
			ports, err = ws.serialPorts()
			if err != nil {
				writeErr(w, 502, err)
				return
			}
		}
		if err := validateSessionTarget(input.Target, ports); err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := ws.connectSession(ctx, input.Target); err != nil {
			writeErr(w, 502, err)
			return
		}
	case "/api/session/disconnect":
		ws.closeSession()
	default:
		writeErr(w, 404, fmt.Errorf("unknown session action"))
		return
	}
	writeJSON(w, ws.sessionStatus())
}

func (ws *webServer) serialPorts() ([]string, error) {
	if ws.listPorts != nil {
		return ws.listPorts()
	}
	return chameleon.Ports()
}
