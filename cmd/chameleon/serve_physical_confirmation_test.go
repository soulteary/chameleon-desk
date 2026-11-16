package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPhysicalConfirmationBindsSessionServerKindAndInputWithoutExposingSecrets(t *testing.T) {
	ws := &webServer{}
	ws.rotateSessionID()
	input := webType2WriteInput()
	input.Password = []byte{0xde, 0xad, 0xbe, 0xef}
	token, err := ws.physicalConfirmation("type2", input)
	if err != nil {
		t.Fatal(err)
	}
	if err = ws.verifyPhysicalConfirmation("type2", input, token); err != nil {
		t.Fatal(err)
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToUpper(string(data)), "DEADBEEF") || strings.Contains(string(data), "password") || strings.Contains(string(data), "expected") {
		t.Fatal("input secrets exposed", string(data))
	}
	changed := input
	changed.Page++
	other := &webServer{}
	other.rotateSessionID()
	for _, test := range []struct {
		server *webServer
		kind   string
		input  any
		token  string
	}{
		{ws, "classic", input, token}, {ws, "type2", changed, token}, {other, "type2", input, token}, {ws, "type2", input, token + "x"}, {ws, "type2", input, "invalid"}, {ws, "type2", input, strings.Repeat("x", 2049)},
	} {
		if test.server.verifyPhysicalConfirmation(test.kind, test.input, test.token) == nil {
			t.Fatal("unbound confirmation accepted")
		}
	}
	ws.rotateSessionID()
	if ws.verifyPhysicalConfirmation("type2", input, token) == nil {
		t.Fatal("old session accepted")
	}
}
func TestPhysicalConfirmationRejectsExpiredAndTamperedReceipts(t *testing.T) {
	ws := &webServer{}
	ws.rotateSessionID()
	input := webType2WriteInput()
	token, err := ws.physicalConfirmation("type2", input)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[0])
	var receipt physicalPlanReceipt
	if err = json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Expires < time.Now().Unix()+299 || receipt.Expires > time.Now().Unix()+301 {
		t.Fatal("unexpected confirmation lifetime")
	}
	receipt.Expires = time.Now().Unix() - 1
	data, _ = json.Marshal(receipt)
	mac := hmac.New(sha256.New, ws.physicalKey[:])
	mac.Write([]byte("receipt\x00"))
	mac.Write(data)
	expired := base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if ws.verifyPhysicalConfirmation("type2", input, expired) == nil {
		t.Fatal("expired receipt accepted")
	}
	data[0] ^= 1
	tampered := base64.RawURLEncoding.EncodeToString(data) + "." + strings.Split(token, ".")[1]
	if ws.verifyPhysicalConfirmation("type2", input, tampered) == nil {
		t.Fatal("tampered receipt accepted")
	}
	if _, err = (&webServer{}).physicalConfirmation("type2", input); err == nil {
		t.Fatal("missing session accepted")
	}
}
func TestPhysicalWorkflowRoutesEnforceMethodTokenOriginAndSessionBeforeIO(t *testing.T) {
	ws, h := configFixture(t, nil)
	for _, path := range []string{"/api/reader/type2-workflow", "/api/reader/type2-write-plan", "/api/reader/type2-write", "/api/reader/classic-write-plan", "/api/reader/classic-write", "/api/physical/lf/plan", "/api/physical/lf/write"} {
		method, write := taskPath(path)
		if method != "POST" || !hardwareRequest(path) || write != (strings.HasSuffix(path, "-write") || strings.HasSuffix(path, "/write")) {
			t.Fatal(path, method, write)
		}
		for _, tc := range []struct {
			method, token, origin, session string
			status                         int
		}{
			{"GET", ws.token, "", "", 405}, {"POST", "", "", "", 403}, {"POST", ws.token, "http://evil.example", "", 403}, {"POST", ws.token, "", "old-session", 409},
		} {
			r := httptest.NewRequest(tc.method, "http://127.0.0.1"+path, strings.NewReader("{}"))
			r.Header.Set("X-Chameleon-Token", tc.token)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-Chameleon-Session", tc.session)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || ws.sessionClient.Stats().Requests != 0 {
				t.Fatal(path, w.Code, w.Body.String())
			}
		}
	}
	ws.allowWrites = false
	for _, path := range []string{"/api/reader/type2-write", "/api/reader/classic-write", "/api/physical/lf/write"} {
		w := taskCall(h, ws.token, http.MethodPost, path, "{}")
		if w.Code != 403 || ws.sessionClient.Stats().Requests != 0 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
}
