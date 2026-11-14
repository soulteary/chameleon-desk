package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}

func ntagCard() map[string]any {
	pages := make([][]int, 45)
	for i := range pages {
		pages[i] = []int{0, 0, 0, 0}
	}
	pages[3] = []int{0xE1, 0x10, 0x12, 0x00}
	pages[4] = []int{0x03, 0x00, 0xFE, 0x00}
	return map[string]any{"id": "a", "name": "NT", "uid": "04112233445566", "tag": 1100, "sak": 0, "atqa": []int{0, 0x44}, "ats": []int{}, "data": pages}
}

func classicCard() map[string]any {
	blocks := make([][]int, 64)
	for i := range blocks {
		blocks[i] = make([]int, 16)
	}
	blocks[0][4] = 0x08 // SAK in block 0 (4-byte UID at byte 5 is index 5; byte 4 is BCC) - plain
	for s := 0; s < 16; s++ {
		blocks[s*4+3] = []int{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x07, 0x80, 0x69, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	}
	return map[string]any{"id": "b", "name": "MFC", "uid": "04112233", "tag": 1001, "sak": 8, "atqa": []int{0, 4}, "ats": []int{}, "data": blocks}
}

func TestCardToolNdefCLI(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "nt.json")
	out := filepath.Join(dir, "nt2.json")
	writeJSONFile(t, in, ntagCard())
	run := func(args ...string) error {
		if !isCardTool(args) {
			t.Fatalf("%v not routed", args)
		}
		return runCardTool(args)
	}
	if e := run("ndef", "write", "--input", in, "--uri", "https://example.com", "--text", "hi", "--output", out); e != nil {
		t.Fatal(e)
	}
	read := filepath.Join(dir, "read.json")
	if e := run("ndef", "read", "--input", out, "--output", read); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(read)
	if !bytes.Contains(b, []byte(`"https://example.com"`)) || !bytes.Contains(b, []byte(`"value": "hi"`)) {
		t.Fatal(string(b))
	}
	if e := run("ndef", "write", "--input", in, "--output", filepath.Join(dir, "x.json")); e == nil {
		t.Fatal("write with no record accepted")
	}
	if e := run("ndef", "write", "--input", in, "--uri", "https://x"); e == nil {
		t.Fatal("write without --output accepted")
	}
}

func TestCardToolACLAndVanityCLI(t *testing.T) {
	dir := t.TempDir()
	card := filepath.Join(dir, "mfc.json")
	writeJSONFile(t, card, classicCard())
	run := func(args ...string) error { return runCardTool(args) }
	out := filepath.Join(dir, "acl.json")
	if e := run("acl", "decode", "--input", card, "--sector", "1", "--output", out); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(out); !bytes.Contains(b, []byte(`"key_a_write": "A"`)) {
		t.Fatal(string(b))
	}
	enc := filepath.Join(dir, "enc.json")
	if e := run("acl", "encode", "--conditions", "0,0,0,4", "--output", enc); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(enc); !bytes.Contains(b, []byte(`"access_hex": "FF0780"`)) {
		t.Fatal(string(b))
	}
	for _, bad := range [][]string{
		{"acl", "decode", "--access", "FF0780", "--sector", "1"},
		{"acl", "encode", "--conditions", "0,0,0"},
		{"acl", "decode"},
	} {
		if e := run(bad...); e == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
	// Vanity: 4-byte UID at byte 5 holds plain SAK 0x08 → not vanity.
	vout := filepath.Join(dir, "v.json")
	writeJSONFile(t, card, func() map[string]any { c := classicCard(); c["data"].([][]int)[0][5] = 0x08; return c }())
	if e := run("vanity", "sak", "--input", card, "--output", vout); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(vout); !bytes.Contains(b, []byte(`"vanity_sak": false`)) {
		t.Fatal(string(b))
	}
	if e := run("vanity", "sak", "--uid", "04112233445566", "--output", filepath.Join(dir, "v2.json")); e != nil {
		t.Fatal(e)
	}
}

type redirectTransport struct{ base *url.URL }

func (r redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme, req.URL.Host = r.base.Scheme, r.base.Host
	return http.DefaultTransport.RoundTrip(req)
}

func TestCardToolDictionaryCLI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "FFFFFFFFFFFF\nA0A1A2A3A4A5\n")
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	old := newDictionaryHTTP
	newDictionaryHTTP = func() *http.Client { return &http.Client{Transport: redirectTransport{base}, Timeout: 5 * time.Second} }
	defer func() { newDictionaryHTTP = old }()
	dir := t.TempDir()
	out := filepath.Join(dir, "d.json")
	run := func(args ...string) error {
		if !isCardTool(args) {
			t.Fatalf("%v not routed", args)
		}
		return runCardTool(args)
	}
	if e := run("dictionary", "download", "--name", "proxmark3-classic", "--output", out); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(out); !bytes.Contains(b, []byte(`"keyLength": 12`)) {
		t.Fatal(string(b))
	}
	if e := run("dictionary", "download", "--list"); e != nil {
		t.Fatal(e)
	}
	for _, bad := range [][]string{
		{"dictionary", "download", "--name", "nope", "--output", filepath.Join(dir, "x.json")},
		{"dictionary", "download", "--name", "proxmark3-classic"},
	} {
		if e := run(bad...); e == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
}
