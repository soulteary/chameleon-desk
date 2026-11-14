package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T, allowWrites bool) (*httptest.Server, *http.Client) {
	t.Helper()
	ws := &webServer{port: "mock", timeout: time.Second, allowWrites: allowWrites}
	mux := http.NewServeMux()
	if e := ws.routes(mux); e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewServer(ws.logging(mux))
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

func post(t *testing.T, client *http.Client, url, body string) (*http.Response, []byte) {
	t.Helper()
	resp, e := client.Post(url, "application/json", strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, b
}

func TestServeOfflineTools(t *testing.T) {
	srv, client := newTestServer(t, false)

	// NDEF read of a small Type 2 card with an empty message area.
	card := map[string]any{"id": "a", "name": "n", "uid": "04112233445566", "tag": 1100, "sak": 0, "atqa": []int{0, 0x44}, "ats": []int{}, "data": func() [][]int {
		p := make([][]int, 45)
		for i := range p {
			p[i] = []int{0, 0, 0, 0}
		}
		p[3] = []int{0xE1, 0x10, 0x12, 0x00}
		p[4] = []int{0x03, 0x03, 0xD1, 0x01} // TLV len 3, then a 1-byte text record start
		p[5] = []int{0x00, 0x54, 0x02, 0x65} // (won't fully decode; we only assert the endpoint runs)
		return p
	}()}
	cb, _ := json.Marshal(card)
	resp, _ := post(t, client, srv.URL+"/api/ndef/read", string(cb))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("ndef status %d", resp.StatusCode)
	}

	// ACL decode.
	resp, b := post(t, client, srv.URL+"/api/acl/decode", `{"access":"FF0780"}`)
	if resp.StatusCode != 200 || !bytes.Contains(b, []byte(`"key_a_write":"A"`)) {
		t.Fatalf("acl decode %d %s", resp.StatusCode, b)
	}
	// ACL encode.
	resp, b = post(t, client, srv.URL+"/api/acl/encode", `{"conditions":[0,0,0,4]}`)
	if resp.StatusCode != 200 || !bytes.Contains(b, []byte(`FF0780`)) {
		t.Fatalf("acl encode %d %s", resp.StatusCode, b)
	}
	// Vanity.
	resp, b = post(t, client, srv.URL+"/api/vanity", `{"uid":"04A1B2C3D4E5F6"}`)
	if resp.StatusCode != 200 || !bytes.Contains(b, []byte(`"vanity_sak":true`)) {
		t.Fatalf("vanity %d %s", resp.StatusCode, b)
	}
	// Dictionary sources.
	r, e := client.Get(srv.URL + "/api/dictionary/sources")
	if e != nil {
		t.Fatal(e)
	}
	b, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || !bytes.Contains(b, []byte("proxmark3-classic")) {
		t.Fatalf("dict %d %s", r.StatusCode, b)
	}
}

func TestServeWriteGate(t *testing.T) {
	srv, client := newTestServer(t, false)
	resp, _ := post(t, client, srv.URL+"/api/slot/activate", `{"slot":1}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without --allow-writes, got %d", resp.StatusCode)
	}
	// capabilities reflects the flag.
	r, _ := client.Get(srv.URL + "/api/capabilities")
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !bytes.Contains(b, []byte(`"writes_enabled":false`)) {
		t.Fatalf("caps %s", b)
	}
}

func TestServeCrossOriginBlocked(t *testing.T) {
	srv, client := newTestServer(t, true)
	req, _ := http.NewRequest("GET", srv.URL+"/api/dictionary/sources", nil)
	req.Header.Set("Origin", "http://evil.example")
	resp, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin request not blocked: %d", resp.StatusCode)
	}
}

func TestServeStaticIndex(t *testing.T) {
	srv, client := newTestServer(t, false)
	r, e := client.Get(srv.URL + "/")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || !bytes.Contains(b, []byte("<title>Chameleon Desk</title>")) {
		t.Fatalf("index %d", r.StatusCode)
	}
}

func TestServeNdefWriteAndDatabase(t *testing.T) {
	srv, client := newTestServer(t, false)
	// NDEF write into a Type 2 card returns an updated card with the message.
	pages := make([][]int, 45)
	for i := range pages {
		pages[i] = []int{0, 0, 0, 0}
	}
	pages[3] = []int{0xE1, 0x10, 0x12, 0x00}
	pages[4] = []int{0x03, 0x00, 0xFE, 0x00}
	card := map[string]any{"id": "a", "name": "n", "uid": "04112233445566", "tag": 1100, "sak": 0, "atqa": []int{0, 0x44}, "ats": []int{}, "data": pages}
	cb, _ := json.Marshal(card)
	resp, b := post(t, client, srv.URL+"/api/ndef/write", `{"card":`+string(cb)+`,"uri":"https://example.com"}`)
	if resp.StatusCode != 200 || !bytes.Contains(b, []byte(`"data"`)) {
		t.Fatalf("ndef write %d %s", resp.StatusCode, b)
	}
	// Re-read the written card to confirm the record round-trips.
	resp, b = post(t, client, srv.URL+"/api/ndef/read", string(b))
	if resp.StatusCode != 200 || !bytes.Contains(b, []byte("https://example.com")) {
		t.Fatalf("ndef read-back %d %s", resp.StatusCode, b)
	}
	// Database add then list.
	empty := `{"flutter.cards":[]}`
	resp, b = post(t, client, srv.URL+"/api/database", `{"database":`+empty+`,"op":"add","card":`+string(cb)+`}`)
	if resp.StatusCode != 200 || !bytes.Contains(b, []byte(`"flutter.cards"`)) {
		t.Fatalf("db add %d %s", resp.StatusCode, b)
	}
	db1 := b
	resp, b = post(t, client, srv.URL+"/api/database", `{"database":`+string(db1)+`,"op":"list"}`)
	if resp.StatusCode != 200 || !bytes.Contains(b, []byte(`"NTAG213"`)) {
		t.Fatalf("db list %d %s", resp.StatusCode, b)
	}
}

func TestServeLoadCardGate(t *testing.T) {
	srv, client := newTestServer(t, false)
	resp, _ := post(t, client, srv.URL+"/api/slot/load-card", `{"slot":8,"card":{}}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("load-card should be 403 without --allow-writes, got %d", resp.StatusCode)
	}
}
