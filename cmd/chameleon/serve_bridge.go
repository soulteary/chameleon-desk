package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

func readBridgeToken(path string) (string, error) {
	link, err := os.Lstat(path)
	if err != nil || !link.Mode().IsRegular() {
		return "", fmt.Errorf("bridge token must be a regular file, not a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("cannot open bridge token file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return "", fmt.Errorf("bridge token must be a private regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, 257))
	if err != nil || len(b) > 256 {
		return "", fmt.Errorf("invalid bridge token file")
	}
	return strings.TrimSpace(string(b)), nil
}

func (ws *webServer) getBridgeClient() *chameleon.Client {
	ws.bridgeMu.Lock()
	defer ws.bridgeMu.Unlock()
	return ws.bridgeClient
}

func (ws *webServer) closeBridgeClient() {
	ws.bridgeMu.Lock()
	c := ws.bridgeClient
	ws.bridgeClient = nil
	ws.bridgeMu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

func (ws *webServer) handleBridge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if ws.bridge == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("managed BLE bridge is not configured"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	action := strings.TrimPrefix(r.URL.Path, "/api/bridge/")
	if action == "status" {
		v, err := ws.bridge.Status(ctx)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, v)
		return
	}
	// Connection management shares the device operation gate. It does not
	// require card-write permission, but it does require the Web session token.
	ws.gateOnce.Do(func() { ws.gate = make(chan struct{}, 1) })
	select {
	case ws.gate <- struct{}{}:
	case <-ctx.Done():
		writeErr(w, http.StatusRequestTimeout, ctx.Err())
		return
	}
	defer func() { <-ws.gate }()
	var input struct {
		DeviceID string `json:"device_id"`
	}
	if action == "select" {
		if err := decodeBody(r, &input); err != nil || input.DeviceID == "" {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("device_id is required"))
			return
		}
	} else {
		var empty struct{}
		if err := decodeBody(r, &empty); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	var output any
	var err error
	switch action {
	case "scan":
		output, err = ws.bridge.Scan(ctx)
	case "select":
		output, err = ws.bridge.Select(ctx, input.DeviceID)
	case "connect":
		if c := ws.getBridgeClient(); c != nil && !c.IsClosed() {
			writeErr(w, http.StatusConflict, fmt.Errorf("BLE bridge is already connected"))
			return
		}
		ws.closeBridgeClient()
		var status chameleon.BridgeStatus
		status, err = ws.bridge.Connect(ctx)
		if err == nil {
			var c *chameleon.Client
			c, err = ws.bridge.OpenSession(ctx, status, ws.timeout)
			if err == nil {
				ws.bridgeMu.Lock()
				ws.bridgeClient = c
				ws.bridgeMu.Unlock()
				status.SessionActive = true
				output = status
			}
		}
	case "disconnect":
		// The control request ends the BLE session and therefore the data socket.
		// Keep the socket until the request so the bridge owns cleanup ordering.
		output, err = ws.bridge.Disconnect(ctx)
		ws.closeBridgeClient()
	default:
		writeErr(w, http.StatusNotFound, fmt.Errorf("unknown bridge action"))
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, output)
}
