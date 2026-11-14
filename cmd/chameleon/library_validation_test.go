package main

import (
	"context"
	chameleon "github.com/soulteary/chameleon-desk"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLibraryValidationIsOfflineAndRejectsMalformedDatabases(t *testing.T) {
	ws := &webServer{allowWrites: false, port: "mock", openDevice: func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		t.Error("library validation opened a device")
		return nil, io.EOF
	}}
	h := offlineWeb(t, ws)
	for _, tc := range []struct {
		database string
		status   int
	}{
		{`{"flutter.cards":[],"unknown":{"error":"preserved"}}`, 200},
		{`{"cards":[],"dictionaries":[]}`, 200},
		{`{"cards":"not a list"}`, 400},
		{`{"cards":["{\"id\":\"broken\"}"]}`, 400},
		{`{"cards":[],"flutter.cards":[]}`, 400},
		{`{"card_folders":["{\"id\":\"a\",\"name\":\"A\",\"parentId\":\"missing\"}"]}`, 400},
	} {
		r := httptest.NewRequest("POST", "http://127.0.0.1/api/database", strings.NewReader(`{"op":"validate","database":`+tc.database+`}`))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.database, w.Code, w.Body.String())
		}
		if w.Code == http.StatusOK && strings.TrimSpace(w.Body.String()) != `{"valid":true}` {
			t.Fatal(w.Body.String())
		}
	}
}
