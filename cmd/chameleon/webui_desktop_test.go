package main

import (
	"bytes"
	"context"
	"encoding/json"
	chameleon "github.com/soulteary/chameleon-desk"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Embedded asset contracts protect the application controls when the desktop
// shell is rearranged. The HTTP tests below remain fully device-independent.
func TestDesktopAssetsAndControlBindings(t *testing.T) {
	htmlBytes, e := webAssets.ReadFile("webui/index.html")
	if e != nil {
		t.Fatal(e)
	}
	html := string(htmlBytes)
	ids := map[string]bool{}
	for _, m := range regexp.MustCompile(`\bid="([^"]+)"`).FindAllStringSubmatch(html, -1) {
		if ids[m[1]] {
			t.Fatalf("duplicate id %s", m[1])
		}
		ids[m[1]] = true
	}
	for _, name := range []string{"app.js", "desktop.js", "window-geometry.js", "tool-panels.js"} {
		js, e := webAssets.ReadFile("webui/" + name)
		if e != nil {
			t.Fatal(e)
		}
		for _, m := range regexp.MustCompile(`(?:\$|document.querySelector)\("#([a-zA-Z0-9-]+)"\)`).FindAllStringSubmatch(string(js), -1) {
			if !ids[m[1]] {
				t.Fatalf("%s references missing control %s", name, m[1])
			}
		}
	}
	for _, id := range []string{"device", "slots", "read", "cards", "ndef", "acl", "vanity", "dictionary"} {
		if !ids[id] || !strings.Contains(html, `data-open="`+id+`"`) {
			t.Fatal("missing application", id)
		}
	}
	if strings.Contains(html, "https://") && strings.Contains(html, `src="https://`) {
		t.Fatal("unexpected external runtime dependency")
	}
	srv, client := newTestServer(t, false)
	for _, path := range []string{"/", "/desktop.js", "/window-geometry.js", "/tool-panels.js", "/app.js", "/style.css"} {
		r, e := client.Get(srv.URL + path)
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r.Body)
		r.Body.Close()
		if e != nil || r.StatusCode != http.StatusOK || len(b) == 0 {
			t.Fatal(path, r.StatusCode, e)
		}
	}
}

// These workflows use real HTTP handlers and synthetic card files. No device
// transport may be opened, including on validation failures.
func TestWebUIVanityRejectsInvalidUID(t *testing.T) {
	srv, client := newTestServer(t, false)
	for _, uid := range []string{"", "GGGGGGGG", "0411", "0411223344556600", "0411223Z"} {
		r, body := post(t, client, srv.URL+"/api/vanity", `{"uid":"`+uid+`"}`)
		if r.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "error") {
			t.Fatalf("invalid UID %q: %d %s", uid, r.StatusCode, body)
		}
	}
	for _, uid := range []string{"04112233", "04 11 22 33 44 55 66"} {
		r, body := post(t, client, srv.URL+"/api/vanity", `{"uid":"`+uid+`"}`)
		if r.StatusCode != http.StatusOK || !strings.Contains(string(body), "vanity_sak") {
			t.Fatalf("valid UID %q: %d %s", uid, r.StatusCode, body)
		}
	}
}

func TestWebUIOfflineToolRoundTrips(t *testing.T) {
	ws := &webServer{openDevice: func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		t.Fatal("offline tools attempted to open hardware")
		return nil, io.EOF
	}}
	h := offlineWeb(t, ws)
	request := func(path string, value any, status int) json.RawMessage {
		t.Helper()
		body, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "http://127.0.0.1:8088"+path, bytes.NewReader(body)))
		if w.Code != status {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return append(json.RawMessage(nil), w.Body.Bytes()...)
	}
	pages := make([][]int, 45)
	for i := range pages {
		pages[i] = []int{0, 0, 0, 0}
	}
	pages[3] = []int{0xE1, 0x10, 0x12, 0}
	pages[4] = []int{3, 0, 0xFE, 0}
	card := map[string]any{"id": "offline", "name": "Round trip", "uid": "04112233445566", "tag": 1100, "sak": 0, "atqa": []int{0, 68}, "ats": []int{}, "data": pages, "custom": "keep"}
	emptyRead := request("/api/ndef/read", card, 200)
	if !bytes.Contains(emptyRead, []byte(`"records":[]`)) {
		t.Fatalf("empty NDEF records must be an array: %s", emptyRead)
	}
	generated := request("/api/ndef/write", map[string]any{"card": card, "text": "回归测试"}, 200)
	read := request("/api/ndef/read", generated, 200)
	if !bytes.Contains(read, []byte("回归测试")) || !bytes.Contains(generated, []byte(`"custom":"keep"`)) {
		t.Fatalf("NDEF round trip %s", read)
	}
	request("/api/ndef/write", map[string]any{"card": card}, 400)
	request("/api/ndef/read", map[string]any{}, 400)

	db := request("/api/database", map[string]any{"database": map[string]any{"flutter.cards": []string{}, "custom": "keep"}, "op": "add", "card": generated}, 200)
	listed := request("/api/database", map[string]any{"database": db, "op": "list", "query": "Round trip"}, 200)
	var rows []struct {
		ID string `json:"id"`
	}
	if e := json.Unmarshal(listed, &rows); e != nil || len(rows) != 1 {
		t.Fatalf("database list %s: %v", listed, e)
	}
	shown := request("/api/database", map[string]any{"database": db, "op": "show", "id": rows[0].ID}, 200)
	if !bytes.Contains(shown, []byte(`"custom":"keep"`)) {
		t.Fatalf("card metadata lost: %s", shown)
	}
	deleted := request("/api/database", map[string]any{"database": db, "op": "delete", "id": rows[0].ID}, 200)
	var result struct {
		Database json.RawMessage `json:"database"`
	}
	if e := json.Unmarshal(deleted, &result); e != nil {
		t.Fatal(e)
	}
	empty := request("/api/database", map[string]any{"database": result.Database, "op": "list"}, 200)
	if string(bytes.TrimSpace(empty)) != "[]" || !bytes.Contains(result.Database, []byte(`"custom":"keep"`)) {
		t.Fatalf("delete round trip %s", deleted)
	}
	request("/api/database", map[string]any{"database": result.Database, "op": "show", "id": rows[0].ID}, 404)
}
