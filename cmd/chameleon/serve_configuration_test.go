package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func configCaps(cmds ...uint16) []byte {
	var out []byte
	for _, c := range cmds {
		out = append(out, byte(c>>8), byte(c))
	}
	return out
}
func configFixture(t *testing.T, steps []step) (*webServer, http.Handler) {
	t.Helper()
	c, err := mockDevice(t, steps)("mock", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ws := &webServer{sessionEnabled: true, sessionClient: c, allowWrites: true, timeout: time.Second}
	ws.rotateSessionID()
	t.Cleanup(ws.closeSession)
	t.Cleanup(ws.tasks.stop)
	return ws, offlineWeb(t, ws)
}
func configCall(ws *webServer, h http.Handler, path, body string) *httptest.ResponseRecorder {
	method := "POST"
	if body == "" {
		method = "GET"
	}
	return taskCall(h, ws.token, method, path, body)
}
func TestWebSettingsConfigureAndReadback(t *testing.T) {
	settings := append([]byte{6, 2, 0, 0, 0, 0, 0}, []byte("654321")...)
	ws, h := configFixture(t, []step{{cmd: 1033, status: 0x68, data: []byte{0}}, {cmd: 1035, status: 0x68, data: configCaps(1013, 1015, 1016, 1034)}, {cmd: 1016, status: 0x68, data: []byte{0}}, {cmd: 1015, payload: []byte{2}, status: 0x68}, {cmd: 1016, status: 0x68, data: []byte{2}}, {cmd: 1013, status: 0x68}, {cmd: 1034, status: 0x68, data: settings}, {cmd: 1039, status: 0x67}, {cmd: 1041, status: 0x67}})
	task := submitTask(t, ws, h, "/api/settings/configure", `{"patch":{"animation":2},"persist":true}`, "settings-task-001")
	out := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
	if out.State != "succeeded" || strings.Contains(string(out.Result), "654321") || strings.Contains(string(out.Result), `"raw"`) {
		t.Fatal(out.State, string(out.Result))
	}
	if !strings.Contains(string(out.Result), `"save_confirmed":true`) {
		t.Fatal(string(out.Result))
	}
}
func TestWebConfigurationPreflightAndValidation(t *testing.T) {
	ws, h := configFixture(t, []step{{cmd: 1033, status: 0x68, data: []byte{1}}, {cmd: 1035, status: 0x68, data: configCaps(1034)}})
	if w := configCall(ws, h, "/api/settings/configure", `{"patch":{"animation":2}}`); w.Code != 502 || !strings.Contains(w.Body.String(), "missing_commands") {
		t.Fatal(w.Code, w.Body.String())
	}
	if ws.sessionClient.Stats().Requests != 2 {
		t.Fatal("write before capability preflight")
	}
	for _, tc := range []struct{ path, body string }{{"/api/settings/configure", `{"patch":{}}`}, {"/api/settings/configure", `{"patch":{"animation":300}}`}, {"/api/slot/edit", `{"slot":1,"sense":2,"action":"initialize","tag":1001}`}, {"/api/slot/edit", `{"slot":1,"sense":1,"action":"type","tag":1001}`}} {
		if w := configCall(ws, h, tc.path, tc.body); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	ws.allowWrites = false
	if w := configCall(ws, h, "/api/slot/edit", `{}`); w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestWebPairingRejectsOpaqueOrTCPBeforeIO(t *testing.T) {
	ws, _ := sessionFixture(t)
	ws.allowWrites = true
	if err := ws.connectSession(context.Background(), "tcp:127.0.0.1:1234"); err != nil {
		t.Fatal(err)
	}
	h := offlineWeb(t, ws)
	w := configCall(ws, h, "/api/settings/configure", `{"patch":{"ble_key":"123456"}}`)
	if w.Code != 502 || ws.sessionClient.Stats().Requests != 0 || strings.Contains(w.Body.String(), "123456") {
		t.Fatal(w.Code, w.Body.String())
	}
}
func configSlotFrames(tag uint16, enabled bool) []step {
	types := make([]byte, 32)
	types[0] = byte(tag >> 8)
	types[1] = byte(tag)
	flags := make([]byte, 16)
	if enabled {
		flags[0] = 1
	}
	return []step{{cmd: 1019, status: 0x68, data: types}, {cmd: 1023, status: 0x68, data: flags}, {cmd: 1018, status: 0x68, data: []byte{0}}}
}
func TestWebSlotEnableAndInitialization(t *testing.T) {
	for _, init := range []bool{false, true} {
		t.Run(map[bool]string{true: "initialize", false: "enable"}[init], func(t *testing.T) {
			steps := []step{{cmd: 1033, status: 0x68, data: []byte{1}}, {cmd: 1035, status: 0x68, data: configCaps(1018, 1019, 1023, 1006, 1005, 1009)}}
			steps = append(steps, configSlotFrames(1001, false)...)
			body := `{"slot":1,"sense":2,"action":"enable","enabled":true,"persist":true}`
			if init {
				steps = append(steps, step{cmd: 1005, payload: []byte{0, 3, 233}, status: 0x68})
				body = `{"slot":1,"sense":2,"action":"initialize","tag":1001,"confirm_initialize":true}`
			} else {
				steps = append(steps, step{cmd: 1006, payload: []byte{0, 2, 1}, status: 0x68})
			}
			steps = append(steps, configSlotFrames(1001, !init)...)
			if !init {
				steps = append(steps, step{cmd: 1009, status: 0x68})
			}
			ws, h := configFixture(t, steps)
			task := submitTask(t, ws, h, "/api/slot/edit", body, "slot-edit-task01")
			out := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
			if out.State != "succeeded" {
				t.Fatal(out.State, string(out.Result))
			}
			var result struct {
				ConfigurationVerified      bool `json:"configuration_verified"`
				InitializationAcknowledged bool `json:"initialization_acknowledged"`
				MemoryVerified             bool `json:"memory_verified"`
			}
			if err := json.Unmarshal(out.Result, &result); err != nil {
				t.Fatal(err)
			}
			if !result.ConfigurationVerified || result.MemoryVerified || result.InitializationAcknowledged != init {
				t.Fatal(result)
			}
		})
	}
}
func TestWebNicknameReadbackFailureIsNotSaved(t *testing.T) {
	ws, h := configFixture(t, []step{{cmd: 1033, status: 0x68, data: []byte{0}}, {cmd: 1035, status: 0x68, data: configCaps(1007, 1008, 1009)}, {cmd: 1007, payload: []byte{0, 2, 'A'}, status: 0x68}, {cmd: 1008, payload: []byte{0, 2}, status: 0x68, data: []byte("different")}})
	task := submitTask(t, ws, h, "/api/slot/edit", `{"slot":1,"sense":2,"action":"nickname","name":"A","persist":true}`, "nickname-task-001")
	out := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
	if out.Outcome != "write_outcome_unknown" || !strings.Contains(string(out.Result), "readback mismatch") || strings.Contains(string(out.Result), `"save_confirmed":true`) {
		t.Fatal(out.State, string(out.Result))
	}
}

func TestWebInitializationWrongCurrentTypeDoesNotWrite(t *testing.T) {
	steps := []step{{cmd: 1033, status: 0x68, data: []byte{0}}, {cmd: 1035, status: 0x68, data: configCaps(1005, 1018, 1019, 1023)}}
	steps = append(steps, configSlotFrames(1001, false)...)
	ws, h := configFixture(t, steps)
	task := submitTask(t, ws, h, "/api/slot/edit", `{"slot":1,"sense":2,"action":"initialize","tag":1002,"confirm_initialize":true}`, "init-preflight01")
	out := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
	if out.Outcome != "write_not_started" || out.WriteAttempted == nil || *out.WriteAttempted {
		t.Fatal(out.State, out.Outcome, string(out.Result))
	}
}
func TestWebSettingsReadOnlyAndTypeReadback(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		settings := append([]byte{6, 1, 0, 0, 0, 0, 0}, []byte("123456")...)
		ws, h := configFixture(t, []step{{cmd: 1033, status: 0x68, data: []byte{1}}, {cmd: 1035, status: 0x68, data: configCaps(1034)}, {cmd: 1034, status: 0x68, data: settings}, {cmd: 1039, status: 0x67}, {cmd: 1041, status: 0x67}})
		ws.allowWrites = false
		w := configCall(ws, h, "/api/settings/read", "")
		if w.Code != 200 || strings.Contains(w.Body.String(), "123456") || !strings.Contains(w.Body.String(), "unsupported") {
			t.Fatal(w.Code, w.Body.String())
		}
	})
	t.Run("type", func(t *testing.T) {
		steps := []step{{cmd: 1033, status: 0x68, data: []byte{1}}, {cmd: 1035, status: 0x68, data: configCaps(1004, 1018, 1019, 1023)}}
		steps = append(steps, configSlotFrames(1001, false)...)
		steps = append(steps, step{cmd: 1004, payload: []byte{0, 3, 234}, status: 0x68})
		steps = append(steps, configSlotFrames(1002, false)...)
		ws, h := configFixture(t, steps)
		w := configCall(ws, h, "/api/slot/edit", `{"slot":1,"sense":2,"action":"type","tag":1002}`)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"configuration_verified":true`) {
			t.Fatal(w.Code, w.Body.String())
		}
	})
}
