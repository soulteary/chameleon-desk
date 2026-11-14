package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

func TestFeatureEndpointsStayOfflineUntilExplicitDeviceProbe(t *testing.T) {
	ws := &webServer{port: "mock", openDevice: func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		t.Error("catalog/preview opened a device")
		return nil, io.EOF
	}}
	h := offlineWeb(t, ws)
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/api/capabilities", "", 200},
		{"POST", "/api/features/check", `{"id":"slot.load-card","slot":1,"card":{}}`, 200},
		{"POST", "/api/features/check", `{"id":"device.pairing"}`, 400},
		{"POST", "/api/features/check", `{"id":"slot.load-card","unexpected":true}`, 400},
		{"GET", "/api/features/check", "", 405},
		{"POST", "/api/features/device", "{}", 405},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, "http://127.0.0.1"+tc.path, strings.NewReader(tc.body)))
		if w.Code != tc.want {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		if w.Code == 200 && w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("capability response can be cached")
		}
	}
}
func TestExplicitFeatureProbeReadsOnlyModelAndCapabilities(t *testing.T) {
	ws := &webServer{port: "mock"}
	ws.openDevice = func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		return mockDevice(t, []step{{cmd: 1033, status: 0x68, data: []byte{1}}, {cmd: 1035, status: 0x68, data: []byte{0x03, 0xeb}}})("mock", time.Second)
	}
	h := offlineWeb(t, ws)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1/api/features/device", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		States []chameleon.FeatureAvailability `json:"states"`
		Device chameleon.FeatureDevice         `json:"device"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	if out.Device.ModelID == nil || *out.Device.ModelID != 1 {
		t.Fatal(out.Device)
	}
	for _, s := range out.States {
		if s.ID == "reader.hf-scan" && s.State != "unavailable" {
			t.Fatal(s)
		}
	}
}
func TestCatalogWebRoutesExist(t *testing.T) {
	ws := &webServer{}
	mux := http.NewServeMux()
	if e := ws.routes(mux); e != nil {
		t.Fatal(e)
	}
	for _, f := range chameleon.Features() {
		for _, entry := range f.Web.Entries {
			parts := strings.Fields(entry)
			if len(parts) != 2 || !strings.HasPrefix(parts[1], "/api/") {
				continue
			}
			_, pattern := mux.Handler(httptest.NewRequest(parts[0], "http://127.0.0.1"+parts[1], nil))
			if pattern != parts[1] {
				t.Errorf("%s: stale Web route %s", f.ID, entry)
			}
		}
	}
}
func TestFeaturePreviewCannotEnableReadOnlyWrites(t *testing.T) {
	ws := &webServer{}
	h := offlineWeb(t, ws)
	w := httptest.NewRecorder()
	body := `{"id":"slot.load-card","slot":1,"device":{"model_id":0,"capabilities":[],"transport":{"connection":"serial","medium":"serial"}},"card":{"id":"x","name":"LF","tag":100,"uid":"1122334455","sak":0,"atqa":[0,0],"ats":[],"data":[]}}`
	h.ServeHTTP(w, httptest.NewRequest("POST", "http://127.0.0.1/api/features/check", strings.NewReader(body)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "writes_disabled") {
		t.Fatal(w.Code, w.Body.String())
	}
}
