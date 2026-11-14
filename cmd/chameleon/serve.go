package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed webui/*
var webAssets embed.FS

// isServe routes the local web GUI.
func isServe(args []string) bool { return len(args) >= 1 && args[0] == "serve" }

// webServer exposes a subset of the SDK over a loopback HTTP API plus a static
// single-page UI. Reads are always allowed; card/device writes need the
// explicit --allow-writes flag and a per-server token. It binds only to loopback.
type webServer struct {
	port         string
	timeout      time.Duration
	allowWrites  bool
	gateOnce     sync.Once
	gate         chan struct{}
	listenPort   string
	token        string
	bridge       *chameleon.BLEBridge
	bridgeMu     sync.Mutex
	bridgeClient *chameleon.Client
	// Optional transport factory for offline simulations.
	openDevice func(context.Context, string, time.Duration) (*chameleon.Client, error)
}

func runServe(args []string) error {
	f := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := f.String("addr", "127.0.0.1:8088", "loopback listen address (localhost, 127.0.0.1 or ::1)")
	port := f.String("port", "", "USB serial or TCP port; otherwise one USB device is auto-selected per request")
	timeout := f.Duration("timeout", 5*time.Second, "per-command device timeout")
	allowWrites := f.Bool("allow-writes", false, "enable endpoints that change the device or write cards")
	bridgeURL := f.String("ble-bridge", "", "HTTP loopback origin of a managed BLE bridge")
	bridgeToken := f.String("bridge-token-file", "", "private token file shared with the managed BLE bridge")
	if e := f.Parse(args[1:]); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	if *timeout <= 0 {
		return fmt.Errorf("invalid timeout")
	}
	host, _, e := net.SplitHostPort(*addr)
	if e != nil {
		return fmt.Errorf("--addr must be host:port")
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("--addr must use a loopback host")
	}
	ws := &webServer{port: *port, timeout: *timeout, allowWrites: *allowWrites}
	if (*bridgeURL == "") != (*bridgeToken == "") || (*bridgeURL != "" && *port != "") {
		return fmt.Errorf("--ble-bridge requires --bridge-token-file and cannot be combined with --port")
	}
	if *bridgeURL != "" {
		token, err := readBridgeToken(*bridgeToken)
		if err != nil {
			return err
		}
		ws.bridge, err = chameleon.NewBLEBridge(*bridgeURL, token)
		if err != nil {
			return err
		}
		defer ws.closeBridgeClient()
	}
	mux := http.NewServeMux()
	if e = ws.routes(mux); e != nil {
		return e
	}
	ln, e := net.Listen("tcp", *addr)
	if e != nil {
		return e
	}
	_, ws.listenPort, _ = net.SplitHostPort(ln.Addr().String())
	fmt.Printf("Chameleon Desk on http://%s/  (writes %s)\n", ln.Addr(), map[bool]string{true: "ENABLED", false: "disabled — pass --allow-writes to enable"}[ws.allowWrites])
	srv := &http.Server{Handler: ws.logging(mux), ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(ln)
}

func (ws *webServer) routes(mux *http.ServeMux) error {
	secret := make([]byte, 32)
	if _, e := rand.Read(secret); e != nil {
		return e
	}
	ws.token = hex.EncodeToString(secret)
	sub, _ := fs.Sub(webAssets, "webui")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	// Device reads.
	mux.HandleFunc("/api/info", ws.handleInfo)
	mux.HandleFunc("/api/scan/hf", ws.handleScanHF)
	mux.HandleFunc("/api/scan/lf", ws.handleScanLF)
	mux.HandleFunc("/api/firmware/check", ws.handleFirmwareCheck)
	// Device writes (gated).
	mux.HandleFunc("/api/slot/activate", ws.handleSlotActivate)
	mux.HandleFunc("/api/slot/load-card", ws.handleSlotLoadCard)
	// Offline tools (no device).
	mux.HandleFunc("/api/ndef/read", ws.handleNdefRead)
	mux.HandleFunc("/api/ndef/write", ws.handleNdefWrite)
	mux.HandleFunc("/api/acl/decode", ws.handleACLDecode)
	mux.HandleFunc("/api/acl/encode", ws.handleACLEncode)
	mux.HandleFunc("/api/vanity", ws.handleVanity)
	mux.HandleFunc("/api/dictionary/sources", ws.handleDictSources)
	mux.HandleFunc("/api/dictionary/download", ws.handleDictDownload)
	mux.HandleFunc("/api/database", ws.handleDatabase)
	mux.HandleFunc("/api/capabilities", ws.handleCaps)
	for _, path := range []string{"status", "scan", "select", "connect", "disconnect"} {
		mux.HandleFunc("/api/bridge/"+path, ws.handleBridge)
	}
	return nil
}

func (ws *webServer) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, port, e := httpAuthority(r.Host)
		ip := net.ParseIP(host)
		if e != nil || (host != "localhost" && (ip == nil || !ip.IsLoopback())) || (ws.listenPort != "" && port != ws.listenPort) {
			http.Error(w, "untrusted Host", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, e := url.Parse(origin)
			if e != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || u.Opaque != "" {
				http.Error(w, "cross-origin requests are not allowed", http.StatusForbidden)
				return
			}
			oh, op, e := httpAuthority(u.Host)
			if e != nil || oh != host || op != port {
				http.Error(w, "cross-origin requests are not allowed", http.StatusForbidden)
				return
			}
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "cross-site request", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			method := http.MethodPost
			switch r.URL.Path {
			case "/api/info", "/api/scan/hf", "/api/scan/lf", "/api/capabilities", "/api/dictionary/sources", "/api/bridge/status":
				method = http.MethodGet
			}
			if r.Method != method {
				w.Header().Set("Allow", method)
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if r.URL.Path == "/api/slot/activate" || r.URL.Path == "/api/slot/load-card" || (strings.HasPrefix(r.URL.Path, "/api/bridge/") && method == http.MethodPost) {
				if ws.token == "" || r.Header.Get("X-Chameleon-Token") != ws.token {
					http.Error(w, "missing or invalid session token", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, e error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": e.Error()})
}

func (ws *webServer) needWrites(w http.ResponseWriter) bool {
	if !ws.allowWrites {
		writeErr(w, http.StatusForbidden, fmt.Errorf("writes are disabled; restart the server with --allow-writes"))
		return false
	}
	return true
}

func (ws *webServer) withDevice(parent context.Context, fn func(context.Context, *chameleon.Client) error) error {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	ws.gateOnce.Do(func() { ws.gate = make(chan struct{}, 1) })
	select {
	case ws.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-ws.gate }()
	if e := ctx.Err(); e != nil {
		return e
	}
	if ws.bridge != nil {
		c := ws.getBridgeClient()
		if c == nil || c.IsClosed() {
			return fmt.Errorf("BLE bridge session is disconnected; connect from device management")
		}
		return fn(ctx, c)
	}
	port := ws.port
	if port == "" {
		p, e := autoPort()
		if e != nil {
			return e
		}
		port = p
	}
	open := ws.openDevice
	if open == nil {
		open = chameleon.OpenContext
	}
	c, e := open(ctx, port, ws.timeout)
	if e != nil {
		return e
	}
	defer c.Close()
	if e := ctx.Err(); e != nil {
		return e
	}
	return fn(ctx, c)
}

func (ws *webServer) device(w http.ResponseWriter, r *http.Request, fn func(context.Context, *chameleon.Client) (any, error)) {
	var out any
	e := ws.withDevice(r.Context(), func(ctx context.Context, c *chameleon.Client) error {
		v, e := fn(ctx, c)
		out = v
		return e
	})
	if e != nil {
		if r.URL.Path == "/api/slot/load-card" && out != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			writeJSON(w, map[string]any{"error": e.Error(), "result": out})
			return
		}
		writeErr(w, http.StatusBadGateway, e)
		return
	}
	writeJSON(w, out)
}

func (ws *webServer) handleInfo(w http.ResponseWriter, r *http.Request) {
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) { return c.Info(ctx) })
}

func (ws *webServer) handleCaps(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"writes_enabled": ws.allowWrites, "csrf_token": ws.token, "ble_bridge_enabled": ws.bridge != nil})
}

func (ws *webServer) handleScanHF(w http.ResponseWriter, r *http.Request) {
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		cards, e := c.ScanISO14443A(ctx)
		var restore *chameleon.ModeRestoreError
		if errors.Is(e, chameleon.ErrNoCard) && !errors.As(e, &restore) {
			return map[string]any{"cards": []any{}, "present": false}, nil
		}
		if e != nil {
			return nil, e
		}
		return map[string]any{"cards": cards, "present": len(cards) > 0}, nil
	})
}

func (ws *webServer) handleScanLF(w http.ResponseWriter, r *http.Request) {
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		card, e := c.ScanLF(ctx, "em410x", 0)
		var restore *chameleon.ModeRestoreError
		if errors.Is(e, chameleon.ErrNoCard) && !errors.As(e, &restore) {
			return map[string]any{"present": false}, nil
		}
		if e != nil {
			return nil, e
		}
		return map[string]any{"present": true, "card": card}, nil
	})
}

func (ws *webServer) handleSlotActivate(w http.ResponseWriter, r *http.Request) {
	if !ws.needWrites(w) {
		return
	}
	var body struct {
		Slot int `json:"slot"`
	}
	if e := decodeBody(r, &body); e != nil {
		writeErr(w, http.StatusBadRequest, e)
		return
	}
	if body.Slot < 1 || body.Slot > 8 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("slot must be 1..8"))
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		if e := c.SetActiveSlot(ctx, body.Slot); e != nil {
			return nil, e
		}
		return map[string]any{"ok": true, "active_slot": body.Slot}, nil
	})
}

func (ws *webServer) handleNdefRead(w http.ResponseWriter, r *http.Request) {
	b, e := readBody(r, 4<<20)
	if e != nil {
		writeErr(w, http.StatusBadRequest, e)
		return
	}
	card, e := chameleon.ParseGUICard(b)
	if e != nil {
		writeErr(w, http.StatusBadRequest, e)
		return
	}
	container, e := chameleon.DetectNdef(card.Tag, card.Data)
	if e != nil {
		writeErr(w, http.StatusUnprocessableEntity, e)
		return
	}
	records, e := chameleon.DecodeNdefMessage(container.Message())
	if e != nil {
		writeErr(w, http.StatusUnprocessableEntity, e)
		return
	}
	if records == nil {
		records = []chameleon.NdefRecord{}
	}
	writeJSON(w, map[string]any{"mapping": container.Mapping, "capacity": container.Capacity(), "message_hex": strings.ToUpper(hex.EncodeToString(container.Message())), "records": records})
}

func (ws *webServer) handleACLDecode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Access string `json:"access"`
	}
	if e := decodeBody(r, &body); e != nil {
		writeErr(w, http.StatusBadRequest, e)
		return
	}
	v, e := chameleon.DecodeClassicAccessHex(body.Access)
	if e != nil {
		writeErr(w, http.StatusUnprocessableEntity, e)
		return
	}
	writeJSON(w, v)
}

func (ws *webServer) handleACLEncode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Conditions []int `json:"conditions"`
	}
	if e := decodeBody(r, &body); e != nil || len(body.Conditions) != 4 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("conditions must be four integers 0..7"))
		return
	}
	var c [4]int
	copy(c[:], body.Conditions)
	bytes, e := chameleon.EncodeClassicAccess(c)
	if e != nil {
		writeErr(w, http.StatusUnprocessableEntity, e)
		return
	}
	writeJSON(w, map[string]any{"access_hex": strings.ToUpper(hex.EncodeToString(bytes[:]))})
}

func (ws *webServer) handleVanity(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UID string `json:"uid"`
	}
	if e := decodeBody(r, &body); e != nil {
		writeErr(w, http.StatusBadRequest, e)
		return
	}
	uid := strings.ToUpper(strings.Join(strings.Fields(body.UID), ""))
	vanity := chameleon.VanitySAKFromUIDHex(uid)
	if vanity == nil {
		writeErr(w, http.StatusUnprocessableEntity, fmt.Errorf("UID must be 4 or 7 hexadecimal bytes"))
		return
	}
	writeJSON(w, map[string]any{"uid": uid, "vanity_sak": vanity})
}

func (ws *webServer) handleDictSources(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, chameleon.DictionarySources)
}

const webCardBodyLimit = 4<<20 + 64<<10
const webDatabaseBodyLimit = 40 << 20 // 32 MiB database + 4 MiB card + envelope

func readBody(r *http.Request, limit int64) ([]byte, error) {
	defer r.Body.Close()
	b, e := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("request body exceeds %d bytes", limit)
	}
	return b, nil
}
func decodeBody(r *http.Request, v any) error {
	limit := int64(1 << 20)
	switch r.URL.Path {
	case "/api/slot/load-card", "/api/ndef/write":
		limit = webCardBodyLimit
	case "/api/database":
		limit = webDatabaseBodyLimit
	}
	b, e := readBody(r, limit)
	if e != nil {
		return e
	}
	if trimmed := bytes.TrimSpace(b); len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("request must contain a JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return fmt.Errorf("request must contain exactly one JSON object")
	}
	return nil
}

// --- GUI-aligned endpoints added to match the CLI feature set ---

func (ws *webServer) handleFirmwareCheck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model   string `json:"model"`
		Version string `json:"version"`
	}
	if e := decodeBody(r, &body); e != nil {
		writeErr(w, http.StatusBadRequest, e)
		return
	}
	model, version := body.Model, body.Version
	if model != "" {
		var e error
		model, e = chameleon.FirmwareModel(model)
		if e != nil {
			writeErr(w, http.StatusBadRequest, e)
			return
		}
	}
	if model == "" || version == "" {
		e := ws.withDevice(r.Context(), func(ctx context.Context, c *chameleon.Client) error {
			var e error
			if model == "" {
				if _, model, e = c.Model(ctx); e != nil {
					return e
				}
			}
			if version == "" {
				version, e = c.GitVersion(ctx)
			}
			return e
		})
		if e != nil {
			writeErr(w, http.StatusBadGateway, e)
			return
		}
	}
	name, e := chameleon.FirmwareModel(model)
	if e != nil {
		writeErr(w, http.StatusBadRequest, e)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	v, e := newFirmwareUpdater().CheckFirmwareUpdate(ctx, name, version)
	if e != nil {
		writeErr(w, http.StatusBadGateway, e)
		return
	}
	writeJSON(w, v)
}

func (ws *webServer) handleSlotLoadCard(w http.ResponseWriter, r *http.Request) {
	if !ws.needWrites(w) {
		return
	}
	var body struct {
		Slot     int             `json:"slot"`
		Card     json.RawMessage `json:"card"`
		Persist  bool            `json:"persist"`
		Activate bool            `json:"activate"`
	}
	if e := decodeBody(r, &body); e != nil {
		writeErr(w, http.StatusBadRequest, e)
		return
	}
	card, e := chameleon.ParseGUICard(body.Card)
	if e != nil {
		writeErr(w, http.StatusBadRequest, e)
		return
	}
	if _, e := chameleon.PlanGUICardLoad(card, body.Slot); e != nil {
		writeErr(w, http.StatusBadRequest, e)
		return
	}
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		return c.LoadGUICard(ctx, body.Slot, card, chameleon.GUICardLoadOptions{Persist: body.Persist, Activate: body.Activate})
	})
}

func (ws *webServer) handleNdefWrite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Card       json.RawMessage `json:"card"`
		Text       string          `json:"text"`
		Language   string          `json:"language"`
		URI        string          `json:"uri"`
		Mime       string          `json:"mime"`
		External   string          `json:"external"`
		Value      string          `json:"value"`
		MessageHex string          `json:"message_hex"`
		Records    ndefRecords     `json:"records"`
	}
	if e := decodeBody(r, &body); e != nil {
		writeErr(w, http.StatusBadRequest, e)
		return
	}
	card, e := chameleon.ParseGUICard(body.Card)
	if e != nil {
		writeErr(w, http.StatusBadRequest, e)
		return
	}
	message, e := encodeNdefInputs(body.Records, body.MessageHex, body.Text, body.Language, body.URI, body.Mime, body.External, body.Value)
	if e != nil {
		writeErr(w, http.StatusBadRequest, e)
		return
	}
	container, e := chameleon.DetectNdef(card.Tag, card.Data)
	if e != nil {
		writeErr(w, http.StatusUnprocessableEntity, e)
		return
	}
	if e := container.WriteMessage(card.Data, message); e != nil {
		writeErr(w, http.StatusUnprocessableEntity, e)
		return
	}
	writeJSON(w, card)
}

func (ws *webServer) handleDictDownload(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if e := decodeBody(r, &body); e != nil {
		writeErr(w, http.StatusBadRequest, e)
		return
	}
	src, e := chameleon.DictionarySourceByName(body.Name)
	if e != nil {
		writeErr(w, http.StatusBadRequest, e)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	d, e := chameleon.DownloadDictionary(ctx, nil, src, body.Name)
	if e != nil {
		writeErr(w, http.StatusBadGateway, e)
		return
	}
	writeJSON(w, d)
}

func (ws *webServer) handleDatabase(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Database json.RawMessage `json:"database"`
		Op       string          `json:"op"`
		Query    string          `json:"query"`
		Sense    string          `json:"sense"`
		ID       string          `json:"id"`
		Card     json.RawMessage `json:"card"`
	}
	if e := decodeBody(r, &body); e != nil {
		writeErr(w, http.StatusBadRequest, e)
		return
	}
	db, e := chameleon.ParseGUIDatabase(body.Database)
	if e != nil {
		writeErr(w, http.StatusBadRequest, e)
		return
	}
	if body.Sense == "" {
		body.Sense = "all"
	}
	switch body.Op {
	case "validate":
		writeJSON(w, map[string]bool{"valid": true})
	case "list":
		v, e := db.Records(false, body.Query, body.Sense)
		if e != nil {
			writeErr(w, http.StatusUnprocessableEntity, e)
			return
		}
		writeJSON(w, v)
	case "show":
		v, e := db.Card(body.ID)
		if e != nil {
			writeErr(w, http.StatusNotFound, e)
			return
		}
		writeJSON(w, v)
	case "add":
		card, e := chameleon.ParseGUICard(body.Card)
		if e != nil {
			writeErr(w, http.StatusBadRequest, e)
			return
		}
		out, e := db.AddCard(card)
		if e != nil {
			writeErr(w, http.StatusUnprocessableEntity, e)
			return
		}
		writeJSON(w, out)
	case "delete":
		out, removed, e := db.DeleteRecord(false, body.ID)
		if e != nil {
			writeErr(w, http.StatusNotFound, e)
			return
		}
		writeJSON(w, map[string]any{"removed": removed, "database": out})
	default:
		writeErr(w, http.StatusBadRequest, fmt.Errorf("op must be validate, list, show, add or delete"))
	}
}

// Browser authorities omit :80 for HTTP's default port. Normalize both Host
// and Origin without accepting userinfo, paths or malformed ports.
func httpAuthority(authority string) (host, port string, err error) {
	u, e := url.Parse("http://" + authority)
	if e != nil {
		return "", "", e
	}
	if u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || strings.HasSuffix(authority, ":") {
		return "", "", fmt.Errorf("invalid HTTP authority")
	}
	host = strings.ToLower(u.Hostname())
	port = u.Port()
	if port == "" {
		port = "80"
	}
	n, e := strconv.Atoi(port)
	if e != nil || n < 1 || n > 65535 {
		return "", "", fmt.Errorf("invalid HTTP port")
	}
	return host, strconv.Itoa(n), nil
}
