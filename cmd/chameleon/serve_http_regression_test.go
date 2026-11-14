package main

import (
	"net/http/httptest"
	"testing"
)

func TestServeDefaultPortNormalization(t *testing.T) {
	for _, tt := range []struct {
		host, origin, listen string
		want                 int
	}{
		{"127.0.0.1", "http://127.0.0.1", "80", 200},
		{"localhost", "http://localhost:80", "80", 200},
		{"localhost:80", "http://localhost", "80", 200},
		{"[::1]", "http://[::1]:80", "80", 200},
		{"[::1]:80", "http://[::1]", "80", 200},
		{"LOCALHOST", "http://localhost", "80", 200},
		{"127.0.0.1", "", "8088", 403},
		{"127.0.0.1", "http://127.0.0.1:8088", "80", 403},
		{"evil.example", "http://evil.example", "80", 403},
		{"localhost:", "", "80", 403},
		{"localhost:bad", "", "80", 403},
		{"localhost:65536", "", "80", 403},
		{"user@localhost", "", "80", 403},
		{"localhost/path", "", "80", 403},
	} {
		t.Run(tt.host+tt.origin+tt.listen, func(t *testing.T) {
			ws := &webServer{listenPort: tt.listen}
			h := offlineWeb(t, ws)
			r := httptest.NewRequest("GET", "http://localhost/api/capabilities", nil)
			r.Host = tt.host
			r.Header.Set("Origin", tt.origin)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tt.want {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
