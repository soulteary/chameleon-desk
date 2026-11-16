package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	chameleon "github.com/soulteary/chameleon-desk"
)

func webLFWriteRequest(t *testing.T) chameleon.LFPhysicalWriteRequest {
	t.Helper()
	present := true
	record, err := chameleon.DecodeLFRecord("pac", 0, []byte("12345678"))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := chameleon.NewGUICardFromRead("PAC", chameleon.ReadResult{Schema: 1, Kind: "lf", Read: 1, Requested: 1, Complete: true, Present: &present, Record: &record}, "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := draft.PromoteLFDraft()
	if err != nil {
		t.Fatal(err)
	}
	return chameleon.LFPhysicalWriteRequest{Schema: 1, Source: card, Protocol: "pac", NewPassword: chameleon.HexBytes{0xde, 0xad, 0xbe, 0xef}, OldPasswords: []chameleon.HexBytes{{1, 2, 3, 4}}, TargetIsolated: true}
}
func webLFWriteBody(t *testing.T, request chameleon.LFPhysicalWriteRequest, token string) string {
	t.Helper()
	raw, err := json.Marshal(lfPhysicalWriteBody{Request: request, Confirmation: token})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func webLFPlanToken(t *testing.T, ws *webServer, request chameleon.LFPhysicalWriteRequest) string {
	t.Helper()
	w := configCall(ws, http.HandlerFunc(ws.handleLFWritePlan), "/api/physical/lf/plan", webLFWriteBody(t, request, ""))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var body struct {
		Confirmation string `json:"confirmation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Confirmation == "" {
		t.Fatal(body, err)
	}
	return body.Confirmation
}
func TestWebLFPhysicalPlanDoesNotContactDevice(t *testing.T) {
	ws, _ := configFixture(t, nil)
	ws.allowWrites = false
	r := webLFWriteRequest(t)
	token := webLFPlanToken(t, ws, r)
	if ws.sessionClient.Stats().Requests != 0 {
		t.Fatal("planning contacted the device")
	}
	if err := ws.verifyPhysicalConfirmation("lf", r, token); err != nil {
		t.Fatal(err)
	}
	r.NewPassword[0] ^= 1
	if err := ws.verifyPhysicalConfirmation("lf", r, token); err == nil {
		t.Fatal("changed password accepted")
	}
	w := configCall(ws, http.HandlerFunc(ws.handleLFWrite), "/api/physical/lf/write", webLFWriteBody(t, r, token))
	if w.Code != 403 || ws.sessionClient.Stats().Requests != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestWebLFPhysicalConfirmationPrecedesDeviceAndWrite(t *testing.T) {
	ws, _ := configFixture(t, nil)
	r := webLFWriteRequest(t)
	token := webLFPlanToken(t, ws, r)
	r.Source.UID = "4142434431323334"
	w := configCall(ws, http.HandlerFunc(ws.handleLFWrite), "/api/physical/lf/write", webLFWriteBody(t, r, token))
	if w.Code != 502 || !strings.Contains(w.Body.String(), "session/input changed") || ws.sessionClient.Stats().Requests != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestWebLFPhysicalWriteAcknowledgementAndReadback(t *testing.T) {
	for _, tc := range []struct {
		name        string
		writeStatus uint16
		scanStatus  uint16
		observed    string
		success     bool
	}{
		{"verified", 0x40, 0x40, "12345678", true},
		{"write-error", 0x41, 0, "", false},
		{"no-readback", 0x40, 0x41, "", false},
		{"different-readback", 0x40, 0x40, "ABCDEFGH", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := webLFWriteRequest(t)
			steps := []step{{cmd: 1033, status: 0x68, data: []byte{0}}, {cmd: 1035, status: 0x68, data: configCaps(1001, 1002, 3014, 3015)}, {cmd: 1033, status: 0x68, data: []byte{0}}, {cmd: 1002, status: 0x68, data: []byte{1}}, {cmd: 3015, payload: append(append([]byte("12345678"), r.NewPassword...), r.OldPasswords[0]...), status: tc.writeStatus}}
			if tc.writeStatus == 0x40 {
				steps = append(steps, step{cmd: 1002, status: 0x68, data: []byte{1}}, step{cmd: 3014, status: tc.scanStatus, data: []byte(tc.observed)})
			}
			ws, _ := configFixture(t, steps)
			token := webLFPlanToken(t, ws, r)
			w := configCall(ws, http.HandlerFunc(ws.handleLFWrite), "/api/physical/lf/write", webLFWriteBody(t, r, token))
			if tc.success {
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				var out lfPhysicalWriteResult
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.Verified || !out.Acknowledged || out.TargetUIDBound || out.PasswordVerified || out.VerificationScope != "selected_protocol_identity_only" {
					t.Fatal(out, err)
				}
			} else {
				if w.Code != 502 || !strings.Contains(w.Body.String(), "write_outcome_unknown") {
					t.Fatal(w.Code, w.Body.String())
				}
			}
		})
	}
}
func TestWebLFPhysicalTaskRetainsUnknownResultAndDoesNotReplay(t *testing.T) {
	r := webLFWriteRequest(t)
	steps := []step{{cmd: 1033, status: 0x68, data: []byte{0}}, {cmd: 1035, status: 0x68, data: configCaps(1001, 1002, 3014, 3015)}, {cmd: 1033, status: 0x68, data: []byte{0}}, {cmd: 1002, status: 0x68, data: []byte{1}}, {cmd: 3015, payload: append(append([]byte("12345678"), r.NewPassword...), r.OldPasswords[0]...), status: 0x40}, {cmd: 1002, status: 0x68, data: []byte{1}}, {cmd: 3014, status: 0x41}}
	ws, h := configFixture(t, steps)
	token := webLFPlanToken(t, ws, r)
	body := webLFWriteBody(t, r, token)
	if w := taskCall(h, "", "POST", "/api/physical/lf/write", body); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	task := submitTask(t, ws, h, "/api/physical/lf/write", body, "lf-unknown-result-0001")
	out := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
	if out.State != "failed" || out.Outcome != "write_outcome_unknown" || out.WriteAttempted == nil || !*out.WriteAttempted || !strings.Contains(string(out.Result), `"acknowledged":true`) || !strings.Contains(string(out.Result), `"verified":false`) {
		t.Fatal(out.State, out.Outcome, string(out.Result))
	}
	again := submitTask(t, ws, h, "/api/physical/lf/write", body, "lf-unknown-result-0001")
	if again.ID != task.ID || ws.sessionClient.Stats().Requests != 7 {
		t.Fatal("write replayed", again.ID, task.ID, ws.sessionClient.Stats())
	}
}
func TestWebLFPhysicalIOProxFailureDoesNotWrite(t *testing.T) {
	r := webLFWriteRequest(t)
	id := chameleon.HexBytes{1, 83, 0x12, 0x34, 0, 0x78, 0x54, 0xe0, 0x3a, 0x5d, 0x65, 0xab, 0, 0, 0, 0}
	r.Source.Tag = 201
	r.Source.UID = id.String()
	r.Protocol = "ioprox"
	ws, h := configFixture(t, []step{{cmd: 1033, status: 0x68, data: []byte{0}}, {cmd: 1035, status: 0x68, data: configCaps(1001, 1002, 3010, 3011, 3012)}, {cmd: 3012, payload: id[4:12], status: 0x67}})
	token := webLFPlanToken(t, ws, r)
	task := submitTask(t, ws, h, "/api/physical/lf/write", webLFWriteBody(t, r, token), "lf-ioprox-check-0001")
	out := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
	if out.Outcome != "write_not_started" || out.WriteAttempted == nil || *out.WriteAttempted || ws.sessionClient.Stats().Requests != 3 {
		t.Fatal(out.Outcome, string(out.Result))
	}
}

func TestEM410xPhysicalReadbackAcceptsClockSubtypesAndKeepsElectraDistinct(t *testing.T) {
	for expected := uint16(100); expected <= 103; expected++ {
		for observed := uint16(100); observed <= 103; observed++ {
			if !sameEM410xIdentityType(expected, observed) {
				t.Fatal(expected, observed)
			}
		}
	}
	if sameEM410xIdentityType(100, 104) || sameEM410xIdentityType(104, 103) || sameEM410xIdentityType(100, 0) || !sameEM410xIdentityType(104, 104) {
		t.Fatal("EM410x and Electra identity families conflated")
	}
}
func TestWebEM410xPhysicalWriteVerifiesSameIdentityWithDifferentClockSubtype(t *testing.T) {
	request := webLFWriteRequest(t)
	request.Source.Tag = 100
	request.Source.UID = "1122334455"
	request.Protocol = "em410x"
	id := []byte{0x11, 0x22, 0x33, 0x44, 0x55}
	steps := []step{{cmd: 1033, status: 0x68, data: []byte{0}}, {cmd: 1035, status: 0x68, data: configCaps(1001, 1002, 3000, 3001)}, {cmd: 1033, status: 0x68, data: []byte{0}}, {cmd: 1002, status: 0x68, data: []byte{1}}, {cmd: 3001, status: 0x40, payload: append(append(append([]byte{}, id...), request.NewPassword...), request.OldPasswords[0]...)}, {cmd: 1002, status: 0x68, data: []byte{1}}, {cmd: 3000, status: 0x40, data: append([]byte{0, 103}, id...)}}
	ws, _ := configFixture(t, steps)
	token := webLFPlanToken(t, ws, request)
	w := configCall(ws, http.HandlerFunc(ws.handleLFWrite), "/api/physical/lf/write", webLFWriteBody(t, request, token))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out lfPhysicalWriteResult
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.Verified || out.Observed.TagType != 103 || out.Record.TagType != 100 || out.VerificationScope != "selected_protocol_identity_only" {
		t.Fatal(out, err)
	}
}

func TestWebLFPhysicalReaderSetupFailureRemainsWriteNotStarted(t *testing.T) {
	request := webLFWriteRequest(t)
	steps := []step{{cmd: 1033, status: 0x68, data: []byte{0}}, {cmd: 1035, status: 0x68, data: configCaps(1001, 1002, 3014, 3015)}, {cmd: 1033, status: 0x68, data: []byte{0}}, {cmd: 1002, status: 0x68, data: []byte{0}}, {cmd: 1001, status: 0x67, payload: []byte{1}}, {cmd: 1001, status: 0x68, payload: []byte{0}}}
	ws, h := configFixture(t, steps)
	token := webLFPlanToken(t, ws, request)
	task := submitTask(t, ws, h, "/api/physical/lf/write", webLFWriteBody(t, request, token), "lf-mode-failed-before-write-001")
	out := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
	if out.State != "failed" || out.Outcome != "write_not_started" || out.WriteAttempted == nil || *out.WriteAttempted || !strings.Contains(string(out.Result), `"outcome":"write_not_started"`) {
		t.Fatal(out.State, out.Outcome, string(out.Result))
	}
}
