package main

import (
	"context"
	"encoding/json"
	chameleon "github.com/soulteary/chameleon-desk"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const hfCaptureBody = `{"action":"hf-sniff","duration_ms":1,"acknowledge_slot_reload":true}`
const lfCaptureBody = `{"action":"lf-samples","duration_ms":1,"acknowledge_slot_reload":true}`

func TestWebRFCaptureTaskReadbackHandoffAndReceipt(t *testing.T) {
	for _, hf := range []bool{true, false} {
		t.Run(map[bool]string{true: "hf", false: "lf"}[hf], func(t *testing.T) {
			cmd, status, original, desired := uint16(3031), uint16(0x40), byte(0), byte(1)
			data := []byte{0, 200, 0, 200}
			path, body := "/api/rf/lf-capture", lfCaptureBody
			if hf {
				cmd, status, original, desired = 2020, 0x68, 1, 0
				data = []byte{0, 7, 0x26}
				path, body = "/api/rf/hf-sniff", hfCaptureBody
			}
			steps := append(readerPreflight(1001, 1002, cmd), step{cmd: 1002, status: 0x68, data: []byte{original}}, step{cmd: 1002, status: 0x68, data: []byte{original}}, step{cmd: 1001, status: 0x68, payload: []byte{desired}}, step{cmd: cmd, status: status, payload: []byte{0, 1}, data: data}, step{cmd: 1001, status: 0x68, payload: []byte{original}}, step{cmd: 1002, status: 0x68, data: []byte{original}})
			ws, h := configFixture(t, steps)
			ws.timeout = 3 * time.Second
			ws.allowWrites = hf
			task := submitTask(t, ws, h, path, body, "rf-hardware-capture-001")
			out := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
			if out.State != "succeeded" || out.Write != hf || out.Commands != uint64(len(steps)) {
				t.Fatal(out.State, out.Write, out.Commands, string(out.Result))
			}
			var result chameleon.RFCaptureResult
			if err := json.Unmarshal(out.Result, &result); err != nil || !result.ModeRestored || result.SessionID != task.SessionID || result.File == nil {
				t.Fatal(result, err)
			}
			count := ws.sessionClient.Stats().Requests
			again := submitTask(t, ws, h, path, body, "rf-hardware-capture-001")
			if again.ID != task.ID || ws.sessionClient.Stats().Requests != count {
				t.Fatal("capture replayed")
			}
			// Analysis is an explicit separate offline submission; it consumes no wire commands.
			file := result.File
			input := chameleon.RFFileRequest{Format: file.Format, Name: file.Name, Data: file.Data}
			if hf {
				input.Layout = "data"
			}
			encoded, _ := json.Marshal(input)
			submission, _ := json.Marshal(map[string]any{"operation_id": "files.rf-analyze", "request_id": "rf-file-handoff-0001", "body": json.RawMessage(encoded)})
			response := taskCall(h, ws.token, "POST", "/api/tasks", string(submission))
			var analysis webTask
			_ = json.Unmarshal(response.Body.Bytes(), &analysis)
			if response.Code != 202 {
				t.Fatal(response.Code, response.Body.String())
			}
			done := waitTask(t, ws, analysis.ID, func(v webTask) bool { return taskTerminal(&v) })
			if done.State != "succeeded" || done.SessionID != "offline" || ws.sessionClient.Stats().Requests != count {
				t.Fatal(done.State, string(done.Result))
			}
		})
	}
}
func TestWebRFCaptureValidationPermissionsAndCapabilityGuards(t *testing.T) {
	ws, h := configFixture(t, nil)
	ws.timeout = 3 * time.Second
	ws.allowWrites = false
	for _, tc := range []struct {
		path, body string
		code       int
	}{
		{"/api/rf/hf-sniff", hfCaptureBody, 403},
		{"/api/rf/hf-auth", hfCaptureBody, 422},
		{"/api/rf/lf-capture", hfCaptureBody, 422},
		{"/api/rf/lf-capture", `{"action":"lf-samples","duration_ms":1}`, 422},
		{"/api/rf/lf-capture", `{"action":"lf-samples","duration_ms":10001,"acknowledge_slot_reload":true}`, 422},
		{"/api/rf/lf-capture", `{"action":"lf-samples","duration_ms":3000,"acknowledge_slot_reload":true}`, 422},
		{"/api/rf/lf-capture?duration=1", lfCaptureBody, 400},
		{"/api/rf/lf-capture", `{"action":"lf-samples","duration_ms":1,"acknowledge_slot_reload":true,"persist":true}`, 400},
	} {
		w := configCall(ws, h, tc.path, tc.body)
		if w.Code != tc.code {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	if w := taskCall(h, "", "POST", "/api/rf/lf-capture", lfCaptureBody); w.Code != 403 {
		t.Fatal(w.Code)
	}
	ws.nativePort = "maintenance"
	if w := configCall(ws, h, "/api/rf/lf-capture", lfCaptureBody); w.Code != 502 || !strings.Contains(w.Body.String(), "bridge") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, model := range []byte{0, 1} {
		ws, h := configFixture(t, []step{{cmd: 1033, status: 0x68, data: []byte{model}}, {cmd: 1035, status: 0x68, data: configCaps(1001, 1002)}})
		ws.timeout = 3 * time.Second
		w := configCall(ws, h, "/api/rf/lf-capture", lfCaptureBody)
		if w.Code != 502 || !strings.Contains(w.Body.String(), "missing_commands") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
func TestWebRFCaptureQueuedCancelAndStaleSessionNeverExecute(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{true: "cancel", false: "session"}[cancel], func(t *testing.T) {
			ws, h := configFixture(t, nil)
			ws.timeout = 3 * time.Second
			ws.gateOnce.Do(func() { ws.gate = make(chan struct{}, 1) })
			ws.gate <- struct{}{}
			task := submitTask(t, ws, h, "/api/rf/lf-capture", lfCaptureBody, "rf-queued-attempt-001")
			if cancel {
				w := taskCall(h, ws.token, "POST", "/api/tasks/"+task.ID+"/cancel", "{}")
				if w.Code != 200 {
					t.Fatal(w.Code)
				}
			} else {
				ws.rotateSessionID()
			}
			<-ws.gate
			out := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
			if out.State == "succeeded" || out.Commands != 0 {
				t.Fatal(out)
			}
		})
	}
}
func TestWebRFCaptureModeFailureRetainsFileAndNoCaptureIsNotRetried(t *testing.T) {
	for _, failure := range []bool{false, true} {
		status := uint16(1)
		cleanup := uint16(0x68)
		var data []byte
		if failure {
			status, cleanup, data = 0x68, 0x67, []byte{0, 7, 0x26}
		}
		steps := append(readerPreflight(1001, 1002, 2020), step{cmd: 1002, status: 0x68, data: []byte{1}}, step{cmd: 1002, status: 0x68, data: []byte{1}}, step{cmd: 1001, status: 0x68, payload: []byte{0}}, step{cmd: 2020, status: status, payload: []byte{0, 1}, data: data}, step{cmd: 1001, status: cleanup, payload: []byte{1}})
		if !failure {
			steps = append(steps, step{cmd: 1002, status: 0x68, data: []byte{1}})
		}
		ws, h := configFixture(t, steps)
		ws.timeout = 3 * time.Second
		w := configCall(ws, h, "/api/rf/hf-sniff", hfCaptureBody)
		var result chameleon.RFCaptureResult
		if failure {
			var envelope struct{ Result chameleon.RFCaptureResult }
			_ = json.Unmarshal(w.Body.Bytes(), &envelope)
			result = envelope.Result
			if w.Code != 502 || result.File == nil || result.ModeRestored {
				t.Fatal(w.Code, w.Body.String())
			}
		} else {
			_ = json.Unmarshal(w.Body.Bytes(), &result)
			if w.Code != 200 || result.Status != "no_capture" || !result.ModeRestored || result.File != nil {
				t.Fatal(w.Code, w.Body.String())
			}
		}
	}
}
func TestWebRFCaptureCheckUsesSelectedCommandsWithoutHardware(t *testing.T) {
	ws := &webServer{timeout: 3 * time.Second}
	ws.rotateSessionID()
	h := offlineWeb(t, ws)
	ws.openDevice = func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		t.Error("preview opened device")
		return nil, nil
	}
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/features/check", strings.NewReader(`{"id":"rf.lf-samples","device":{"model_id":0,"capabilities":[1001,1002,3031],"transport":{"connection":"tcp","medium":"ble"}},"rf_capture":`+lfCaptureBody+`}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"available"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
