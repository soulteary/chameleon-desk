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
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
	physicalOnce     sync.Once
	physicalKey      [32]byte
	physicalKeyError error
	recoveryTools    string
	tasks            webTasks
	sessionEnabled   bool
	sessionMu        sync.Mutex
	sessionID        string
	sessionClient    *chameleon.Client
	sessionTarget    string
	nativePort       string
	port             string
	timeout          time.Duration
	allowWrites      bool
	gateOnce         sync.Once
	gate             chan struct{}
	listenPort       string
	token            string
	bridge           managedBridge
	bridgeMu         sync.Mutex
	bridgeClient     *chameleon.Client
	// Optional transport factory for offline simulations.
	openDevice func(context.Context, string, time.Duration) (*chameleon.Client, error)
}

func runServe(args []string) error {
	f := flag.NewFlagSet("serve", flag.ContinueOnError)
	journalDefault, err := defaultTaskJournalPath()
	if err != nil {
		return err
	}
	journalPath := f.String("task-journal", journalDefault, "private durable task journal; one server per file, never replayed")
	taskTokenFile := f.String("task-token-file", "", "private task-client token file (default: JOURNAL.token)")
	addr := f.String("addr", "127.0.0.1:8088", "loopback listen address (localhost, 127.0.0.1 or ::1)")
	port := f.String("port", "", applicationPortHelp)
	timeout := f.Duration("timeout", 5*time.Second, "per-command device timeout")
	recoveryTools := f.String("recovery-tools-dir", chameleon.DefaultRecoveryToolsDir(), "trusted local recovery helper directory; browser cannot select executables")
	allowWrites := f.Bool("allow-writes", false, "enable endpoints that change the device or write cards")
	bridgeURL := f.String("ble-bridge", "", "HTTP loopback origin of a managed BLE bridge")
	usbBridgeURL := f.String("usb-bridge", "", "HTTP loopback origin of a managed USB serial bridge")
	nativePort := f.String("native-usb-maintenance", "", "explicit local application port for pairing/reset maintenance; routine operations remain on the Python bridge")
	bridgeToken := f.String("bridge-token-file", "", "private token file shared with the configured device bridge")
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
	ws := &webServer{recoveryTools: *recoveryTools, nativePort: *nativePort, sessionEnabled: true, port: *port, timeout: *timeout, allowWrites: *allowWrites}
	ws.rotateSessionID()
	if *nativePort != "" && (*bridgeURL != "" || *usbBridgeURL != "" || *port != "") {
		return fmt.Errorf("--native-usb-maintenance cannot be combined with --port or managed bridge options")
	}
	if *bridgeURL != "" && *usbBridgeURL != "" {
		return fmt.Errorf("--ble-bridge and --usb-bridge are mutually exclusive")
	}
	configuredURL := *bridgeURL
	if *usbBridgeURL != "" {
		configuredURL = *usbBridgeURL
	}
	if (configuredURL == "") != (*bridgeToken == "") || (configuredURL != "" && *port != "") {
		return fmt.Errorf("--ble-bridge or --usb-bridge requires --bridge-token-file and cannot be combined with --port")
	}
	if configuredURL != "" {
		token, err := readBridgeToken(*bridgeToken)
		if err != nil {
			return err
		}
		if *usbBridgeURL != "" {
			ws.bridge, err = chameleon.NewUSBBridge(configuredURL, token)
		} else {
			ws.bridge, err = chameleon.NewBLEBridge(configuredURL, token)
		}
		if err != nil {
			return err
		}
		defer ws.closeBridgeClient()
	}
	if *journalPath == "" {
		return fmt.Errorf("--task-journal must not be empty")
	}
	if err := ws.tasks.openJournal(*journalPath); err != nil {
		return fmt.Errorf("open task journal: %w", err)
	}
	defer func() { ws.tasks.stop(); ws.closeSession() }()
	startupTarget := ws.port
	if ws.nativePort != "" {
		startupTarget = ws.nativePort
	}
	if ws.bridge == nil && startupTarget != "" {
		if err := ws.connectSession(context.Background(), startupTarget); err != nil {
			return err
		}
	}
	mux := http.NewServeMux()
	if e = ws.routes(mux); e != nil {
		return e
	}
	tokenPath := *taskTokenFile
	if tokenPath == "" {
		tokenPath = *journalPath + ".token"
	}
	ln, e := net.Listen("tcp", *addr)
	if e != nil {
		return e
	}
	if err := writeTaskTokenFile(tokenPath, ws.token); err != nil {
		ln.Close()
		return fmt.Errorf("write task-client token: %w", err)
	}
	_, ws.listenPort, _ = net.SplitHostPort(ln.Addr().String())
	fmt.Printf("Chameleon Desk on http://%s/  (writes %s)\n", ln.Addr(), map[bool]string{true: "ENABLED", false: "disabled — pass --allow-writes to enable"}[ws.allowWrites])
	srv := &http.Server{Handler: ws.logging(mux), ReadHeaderTimeout: 10 * time.Second}
	fmt.Printf("Task client token: %s\n", tokenPath)
	shutdown, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-shutdown.Done():
			_ = srv.Close()
		case <-done:
		}
	}()
	e = srv.Serve(ln)
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}

func (ws *webServer) routes(mux *http.ServeMux) error {
	secret := make([]byte, 32)
	if _, e := rand.Read(secret); e != nil {
		return e
	}
	ws.token = hex.EncodeToString(secret)
	sub, _ := fs.Sub(webAssets, "webui")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	for _, action := range []string{"ports", "status", "connect", "disconnect"} {
		mux.HandleFunc("/api/session/"+action, ws.handleSession)
	}
	mux.HandleFunc("/api/tasks", ws.handleTasks)
	mux.HandleFunc("/api/tasks/", ws.handleTasks)
	for _, operation := range chameleon.Operations() {
		handler := ws.operationHandler(operation.Path)
		if handler == nil {
			return fmt.Errorf("operation %s has no handler", operation.ID)
		}
		mux.HandleFunc(operation.Path, handler)
	}
	mux.HandleFunc("/api/operations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, map[string]any{"schema_version": chameleon.OperationSchemaVersion, "operations": chameleon.Operations()})
	})
	mux.HandleFunc("/api/firmware/check", ws.handleFirmwareCheck)
	// Offline tools (no device).
	mux.HandleFunc("/api/ndef/read", ws.handleNdefRead)
	mux.HandleFunc("/api/ndef/write", ws.handleNdefWrite)
	mux.HandleFunc("/api/acl/decode", ws.handleACLDecode)
	mux.HandleFunc("/api/acl/encode", ws.handleACLEncode)
	mux.HandleFunc("/api/vanity", ws.handleVanity)
	mux.HandleFunc("/api/dictionary/sources", ws.handleDictSources)
	mux.HandleFunc("/api/dictionary/download", ws.handleDictDownload)
	mux.HandleFunc("/api/database", ws.handleDatabase)
	mux.HandleFunc("/api/library/manage", ws.handleLibraryManage)
	mux.HandleFunc("/api/capabilities", ws.handleCaps)
	mux.HandleFunc("/api/features/check", ws.handleFeatureCheck)
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
			operationNeedsToken := false
			switch r.URL.Path {
			case "/api/info", "/api/scan/hf", "/api/scan/lf", "/api/capabilities", "/api/dictionary/sources", "/api/bridge/status", "/api/features/device", "/api/operations", "/api/session/status", "/api/session/ports", "/api/settings/read", "/api/slot/details":
				method = http.MethodGet
			}
			if operation, ok := chameleon.OperationForPath(r.URL.Path); ok {
				method = operation.Method
				operationNeedsToken = operation.Method == http.MethodPost
			}
			if (r.URL.Path == "/api/tasks" || strings.HasPrefix(r.URL.Path, "/api/tasks/")) && r.Method == http.MethodGet {
				method = http.MethodGet
			}
			if r.Method != method {
				w.Header().Set("Allow", method)
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if operationNeedsToken || (strings.HasPrefix(r.URL.Path, "/api/reader/") || strings.HasPrefix(r.URL.Path, "/api/physical/")) || r.URL.Path == "/api/tasks" || strings.HasPrefix(r.URL.Path, "/api/tasks/") || r.URL.Path == "/api/settings/configure" || r.URL.Path == "/api/slot/edit" || r.URL.Path == "/api/slot/activate" || r.URL.Path == "/api/slot/load-card" || ((strings.HasPrefix(r.URL.Path, "/api/bridge/") || strings.HasPrefix(r.URL.Path, "/api/session/")) && method == http.MethodPost) {
				if ws.token == "" || r.Header.Get("X-Chameleon-Token") != ws.token {
					http.Error(w, "missing or invalid session token", http.StatusForbidden)
					return
				}
			}
		}
		if hardwareRequest(r.URL.Path) || (strings.HasPrefix(r.URL.Path, "/api/bridge/") && r.Method == http.MethodPost) {
			id := ws.currentSessionID()
			expected := r.Header.Get("X-Chameleon-Session")
			if expected != "" && expected != id {
				writeErr(w, http.StatusConflict, fmt.Errorf("device session changed; refresh connection status"))
				return
			}
			w.Header().Set("X-Chameleon-Session", id)
			r = r.WithContext(context.WithValue(r.Context(), sessionContextKey{}, id))
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
	if expected, ok := parent.Value(sessionContextKey{}).(string); ok && expected != ws.currentSessionID() {
		return fmt.Errorf("device session changed; request was not executed")
	}
	if ws.bridge != nil {
		c := ws.getBridgeClient()
		if c == nil || c.IsClosed() {
			return fmt.Errorf("device bridge session is disconnected; connect from device management")
		}
		return fn(ctx, c)
	}
	if ws.sessionEnabled {
		ws.sessionMu.Lock()
		c := ws.sessionClient
		ws.sessionMu.Unlock()
		if c == nil || c.IsClosed() {
			return fmt.Errorf("device session is disconnected; connect from device management")
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
	if ws.nativePort != "" && r.URL.Path != "/api/features/device" && r.URL.Path != "/api/settings/read" && r.URL.Path != "/api/settings/configure" {
		writeErr(w, http.StatusBadGateway, chameleon.ErrUSBBridgeRequired)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var out any
	e := ws.withDevice(r.Context(), func(ctx context.Context, c *chameleon.Client) error {
		stopProgress, err := ws.taskProgress(ctx, c)
		if err != nil {
			return err
		}
		defer stopProgress()
		v, e := fn(ctx, c)
		out = v
		return e
	})
	if e != nil {
		if ((strings.HasPrefix(r.URL.Path, "/api/reader/") || strings.HasPrefix(r.URL.Path, "/api/physical/")) || r.URL.Path == "/api/slot/load-card" || r.URL.Path == "/api/slot/edit" || r.URL.Path == "/api/slot/read-current" || strings.HasPrefix(r.URL.Path, "/api/slot/emulator/") || r.URL.Path == "/api/settings/configure") && out != nil {
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
	ws.device(w, r, func(ctx context.Context, c *chameleon.Client) (any, error) {
		info, err := c.Info(ctx)
		if err != nil {
			return nil, err
		}
		device := chameleon.FeatureDevice{ModelID: &info.ModelID, Capabilities: info.Capabilities, Transport: info.Transport}
		return struct {
			chameleon.Info
			FeatureSchemaVersion int                             `json:"feature_schema_version"`
			FeatureStates        []chameleon.FeatureAvailability `json:"feature_states"`
		}{info, chameleon.FeatureSchemaVersion, ws.featureStates(&device)}, nil
	})
}

func (ws *webServer) handleCaps(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"background_tasks_enabled": ws.sessionEnabled, "writes_enabled": ws.allowWrites, "csrf_token": ws.token, "ble_bridge_enabled": ws.bridgeMedium() == "ble", "usb_bridge_enabled": ws.bridgeMedium() == "usb-serial", "device_sessions_enabled": ws.sessionEnabled, "native_usb_maintenance_enabled": ws.nativePort != "", "operation_schema_version": chameleon.OperationSchemaVersion, "operations": chameleon.Operations(), "feature_schema_version": chameleon.FeatureSchemaVersion, "feature_catalog": chameleon.Features(), "feature_device": nil, "feature_states": ws.featureStates(nil)})
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
	if e := card.RequireCardImage(); e != nil {
		writeErr(w, http.StatusUnprocessableEntity, e)
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
	case "/api/slot/load-card", "/api/ndef/write", "/api/features/check":
		limit = webCardBodyLimit
	case "/api/tasks":
		limit = webCardBodyLimit + (64 << 10)
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
	if ws.nativePort != "" && (model == "" || version == "") {
		writeErr(w, http.StatusBadGateway, chameleon.ErrUSBBridgeRequired)
		return
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
	if e := card.RequireCardImage(); e != nil {
		writeErr(w, http.StatusUnprocessableEntity, e)
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
		Database       json.RawMessage                        `json:"database"`
		Op             string                                 `json:"op"`
		ReadResult     *chameleon.ReadResult                  `json:"read_result"`
		Type2Promotion *chameleon.Type2PromotionOptions       `json:"type2_promotion"`
		OtherID        string                                 `json:"other_id"`
		MergeOptions   chameleon.ClassicMergeOptions          `json:"merge_options"`
		SelectedRepair *chameleon.ClassicSelectedRepairResult `json:"selected_repair"`
		LFIdentity     *chameleon.LFIdentityInput             `json:"lf_identity"`
		ReadError      string                                 `json:"read_error"`
		Name           string                                 `json:"name"`
		Rows           []string                               `json:"rows"`
		Query          string                                 `json:"query"`
		Sense          string                                 `json:"sense"`
		ID             string                                 `json:"id"`
		Card           json.RawMessage                        `json:"card"`
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
	case "import-read":
		if body.ReadResult == nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("read_result required"))
			return
		}
		card, err := chameleon.NewGUICardFromRead(body.Name, *body.ReadResult, body.ReadError)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err)
			return
		}
		next, err := db.AddCard(card)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err)
			return
		}
		writeJSON(w, map[string]any{"database": next, "card": card})
	case "edit-lf":
		if body.LFIdentity == nil {
			writeErr(w, 400, fmt.Errorf("lf_identity required"))
			return
		}
		card, err := db.Card(body.ID)
		if err != nil {
			writeErr(w, 404, err)
			return
		}
		card, err = card.EditLFIdentityOffline(body.Name, *body.LFIdentity)
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		next, err := db.ReplaceCard(card)
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]any{"database": next, "card": card})
	case "repair-classic-preview", "repair-classic":
		if body.SelectedRepair == nil {
			writeErr(w, 400, fmt.Errorf("selected_repair required"))
			return
		}
		card, err := db.Card(body.ID)
		if err != nil {
			writeErr(w, 404, err)
			return
		}
		if body.Op == "repair-classic-preview" {
			preview, err := card.ClassicSelectedRepairPreview(*body.SelectedRepair)
			if err != nil {
				writeErr(w, 422, err)
				return
			}
			writeJSON(w, preview)
			return
		}
		card, err = card.ApplyClassicSelectedRepair(*body.SelectedRepair, body.MergeOptions)
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		next, err := db.AddCard(card)
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]any{"database": next, "card": card})
	case "edit-data":
		card, err := db.Card(body.ID)
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		card, err = card.EditData(body.Name, body.Rows)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err)
			return
		}
		next, err := db.ReplaceCard(card)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err)
			return
		}
		writeJSON(w, map[string]any{"database": next, "card": card})
	case "promote-classic":
		card, err := db.Card(body.ID)
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		card, err = card.PromoteClassicDraft()
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err)
			return
		}
		next, err := db.AddCard(card)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err)
			return
		}
		writeJSON(w, map[string]any{"database": next, "card": card})
	case "promote-slot", "promote-lf", "promote-type2":
		if body.Op == "promote-slot" && body.Type2Promotion != nil {
			writeErr(w, 400, fmt.Errorf("slot conversion preserves observed emulator bytes; Type2 credential-assignment options do not apply"))
			return
		}
		card, err := db.Card(body.ID)
		if err != nil {
			writeErr(w, 404, err)
			return
		}
		if body.Op == "promote-slot" {
			card, err = card.PromoteSlotDraft()
		} else if body.Op == "promote-lf" {
			card, err = card.PromoteLFDraft()
		} else {
			options := chameleon.Type2PromotionOptions{}
			if body.Type2Promotion != nil {
				options = *body.Type2Promotion
			}
			card, err = card.PromoteType2Draft(options)
		}
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		next, err := db.AddCard(card)
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]any{"database": next, "card": card})
	case "merge-classic-preview", "merge-classic":
		card, err := db.Card(body.ID)
		if err != nil {
			writeErr(w, 404, err)
			return
		}
		other, err := db.Card(body.OtherID)
		if err != nil {
			writeErr(w, 404, err)
			return
		}
		if body.Op == "merge-classic-preview" {
			preview, err := card.ClassicMergePreview(other)
			if err != nil {
				writeErr(w, 422, err)
				return
			}
			writeJSON(w, preview)
			return
		}
		card, err = card.MergeClassicDraft(other, body.MergeOptions)
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		next, err := db.AddCard(card)
		if err != nil {
			writeErr(w, 422, err)
			return
		}
		writeJSON(w, map[string]any{"database": next, "card": card})
	case "delete":
		out, removed, e := db.DeleteRecord(false, body.ID)
		if e != nil {
			writeErr(w, http.StatusNotFound, e)
			return
		}
		writeJSON(w, map[string]any{"removed": removed, "database": out})
	default:
		writeErr(w, http.StatusBadRequest, fmt.Errorf("unsupported database operation; use validate, list, show, add, delete, import-read, edit-data, edit-lf, promote-slot, promote-classic, promote-lf, promote-type2, merge-classic-preview, merge-classic, repair-classic-preview or repair-classic"))
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
