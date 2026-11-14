package chameleon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewGUIDictionaryFromText(t *testing.T) {
	text := strings.Join([]string{
		"# Comment line",
		"FFFFFFFFFFFF",
		"A0A1A2A3A4A5 well-known key",
		"ffffffffffff",                     // duplicate (case-insensitive), dropped
		"1122",                             // wrong length, skipped
		"00112233445566778899AABBCCDDEEFF", // 32-hex, wrong length once 12 is fixed
		"D3F7D3F7D3F7#inline",
	}, "\n")
	d, e := NewGUIDictionaryFromText("proxmark", text)
	if e != nil {
		t.Fatal(e)
	}
	if d.KeyLength != 12 || len(d.Keys) != 3 || d.Name != "proxmark" || d.ID == "" {
		t.Fatalf("%+v", d)
	}
	if !strings.EqualFold(d.Keys[1].String(), "a0a1a2a3a4a5") {
		t.Fatalf("label not stripped: %s", d.Keys[1])
	}
	// A T55xx-style list fixes the 8-hex length from the first key.
	d, e = NewGUIDictionaryFromText("pwds", "51243648\n000000AA\nFFFFFFFFFFFF")
	if e != nil || d.KeyLength != 8 || len(d.Keys) != 2 {
		t.Fatalf("%+v %v", d, e)
	}
	if _, e := NewGUIDictionaryFromText("x", "# only comments\nnothex"); e == nil {
		t.Fatal("empty dictionary accepted")
	}
}

func TestDictionaryCommentsDoNotSelectKeyLength(t *testing.T) {
	for _, comment := range []string{"# 11223344", "  # FFFFFFFFFFFF", "\t# 00112233445566778899AABBCCDDEEFF"} {
		t.Run(comment, func(t *testing.T) {
			d, e := NewGUIDictionaryFromText("keys", comment+"\nFFFFFFFFFFFF\tfirst key\nA0A1A2A3A4A5#11223344\n")
			if e != nil || d.KeyLength != 12 || len(d.Keys) != 2 || d.Keys[0].String() != "FFFFFFFFFFFF" || d.Keys[1].String() != "A0A1A2A3A4A5" {
				t.Fatalf("comment changed dictionary contents: %+v %v", d, e)
			}
		})
	}
	if _, e := NewGUIDictionaryFromText("keys", "# 11223344\n# FFFFFFFFFFFF\n# 00112233445566778899AABBCCDDEEFF"); e == nil {
		t.Fatal("commented keys accepted as a dictionary")
	}
}

func TestDownloadDictionary(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "chameleon-go/") {
			t.Errorf("missing User-Agent: %q", r.Header.Get("User-Agent"))
		}
		switch r.URL.Path {
		case "/keys.dic":
			fmt.Fprint(w, "FFFFFFFFFFFF\nA0A1A2A3A4A5\n")
		case "/empty.dic":
			fmt.Fprint(w, "# nothing usable\n")
		default:
			http.Error(w, "nope", http.StatusNotFound)
		}
	}))
	defer server.Close()
	ctx := context.Background()
	d, e := DownloadDictionary(ctx, server.Client(), DictionarySource{Name: "test", URL: server.URL + "/keys.dic"}, "")
	if e != nil || len(d.Keys) != 2 || d.Name != "test" {
		t.Fatalf("%+v %v", d, e)
	}
	if _, e := DownloadDictionary(ctx, server.Client(), DictionarySource{Name: "e", URL: server.URL + "/empty.dic"}, ""); e == nil {
		t.Fatal("empty dictionary accepted")
	}
	if _, e := DownloadDictionary(ctx, server.Client(), DictionarySource{Name: "m", URL: server.URL + "/missing.dic"}, ""); e == nil {
		t.Fatal("404 accepted")
	}
	if _, e := DownloadDictionary(ctx, server.Client(), DictionarySource{Name: "p", URL: "http://example.invalid/x.dic"}, ""); e == nil {
		t.Fatal("plain HTTP accepted")
	}
	if _, e := DictionarySourceByName("proxmark3-classic"); e != nil {
		t.Fatal(e)
	}
	if _, e := DictionarySourceByName("nope"); e == nil {
		t.Fatal("unknown source accepted")
	}
}
