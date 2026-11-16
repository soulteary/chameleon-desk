package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chameleon "github.com/soulteary/chameleon-desk"
)

func webType2Raw(data []byte, flags byte, response []byte) step {
	return step{cmd: 2010, payload: append([]byte{flags, 1, 244, byte(len(data) * 8 >> 8), byte(len(data) * 8)}, data...), data: response}
}
func webType2Scan() []byte { return []byte{7, 4, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x44, 0, 0, 0} }
func webType2WriteInput() chameleon.Type2PageWrite {
	return chameleon.Type2PageWrite{UID: chameleon.HexBytes{4, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66}, Page: 4, Expected: chameleon.HexBytes{1, 2, 3, 4}, Data: chameleon.HexBytes{4, 3, 2, 1}}
}
func webType2PlanSteps() []step {
	scan := webType2Scan()
	version := []byte{0, 4, 4, 2, 1, 0, 0x0f, 3}
	steps := append(readerPreflight(1001, 1002, 2000, 2010, 2101), step{cmd: 1002, status: 0x68, data: []byte{1}}, step{cmd: 2000, data: scan}, webType2Raw([]byte{0x60}, 0xf4, version), step{cmd: 2000, data: scan})
	steps = append(steps, step{cmd: 2000, data: scan}, webType2Raw([]byte{0x3a, 41, 42}, 0xfc, make([]byte, 8)), step{cmd: 2000, data: scan}, step{cmd: 2000, data: scan}, webType2Raw([]byte{0x3a, 4, 4}, 0xfc, []byte{1, 2, 3, 4}), step{cmd: 2000, data: scan}, webType2Raw([]byte{0x60}, 0xf4, version), step{cmd: 2000, data: scan}, step{cmd: 2101, status: 0x68})
	return steps
}
func type2HandlerCall(handler http.HandlerFunc, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "http://127.0.0.1/api/reader/type2-write", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}
func TestWebType2PlanRequiresLiveProbeButNoWritePermission(t *testing.T) {
	ws, _ := configFixture(t, webType2PlanSteps())
	ws.allowWrites = false
	input := webType2WriteInput()
	w := type2HandlerCall(ws.handleType2WritePlan, type2PhysicalWriteBody{Request: input})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		Schema       int                          `json:"schema"`
		Plan         chameleon.Type2PageWritePlan `json:"plan"`
		Confirmation string                       `json:"confirmation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Schema != 1 || out.Plan.Model != "NTAG213" || out.Confirmation == "" || !bytes.Equal(out.Plan.Before, input.Expected) {
		t.Fatal(w.Body.String(), err)
	}
	if err := ws.verifyPhysicalConfirmation("type2", input, out.Confirmation); err != nil {
		t.Fatal(err)
	}
	input.Data[0] ^= 1
	if err := ws.verifyPhysicalConfirmation("type2", input, out.Confirmation); err == nil {
		t.Fatal("changed request accepted")
	}
}
func TestWebType2WriteRequiresReviewAndPermissionBeforeIO(t *testing.T) {
	ws, _ := configFixture(t, nil)
	input := webType2WriteInput()
	for _, body := range []any{input, type2PhysicalWriteBody{Request: input}, type2PhysicalWriteBody{Request: input, Confirm: true}, type2PhysicalWriteBody{Request: input, Confirmation: "invalid", Confirm: true}} {
		w := type2HandlerCall(ws.handleType2UserPageWrite, body)
		if w.Code < 400 || ws.sessionClient.Stats().Requests != 0 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	ws.allowWrites = false
	if w := type2HandlerCall(ws.handleType2UserPageWrite, type2PhysicalWriteBody{Request: input, Confirmation: "invalid", Confirm: true}); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestWebType2ReadValidationAndMissingCommands(t *testing.T) {
	ws, _ := configFixture(t, nil)
	if w := type2HandlerCall(ws.handleType2WorkflowRead, chameleon.Type2WorkflowRead{UID: chameleon.HexBytes{1, 2, 3, 4}, Full: true}); w.Code != 400 || ws.sessionClient.Stats().Requests != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	ws, _ = configFixture(t, readerPreflight(1001, 1002, 2000, 2010))
	input := chameleon.Type2WorkflowRead{UID: webType2WriteInput().UID, Full: true}
	w := type2HandlerCall(ws.handleType2WorkflowRead, input)
	if w.Code != 502 || !strings.Contains(w.Body.String(), "missing_commands") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestWebType2PhysicalWriteTaskTracksBoundaryAndDoesNotReplay(t *testing.T) {
	for _, cleanupFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified", true: "cleanup-failed"}[cleanupFailure], func(t *testing.T) {
			steps := webType2PlanSteps()
			steps = steps[:len(steps)-1]
			scan := webType2Scan()
			version := []byte{0, 4, 4, 2, 1, 0, 0x0f, 3}
			input := webType2WriteInput()
			status := uint16(0x68)
			if cleanupFailure {
				status = 0x67
			}
			steps = append(steps, webType2Raw(append([]byte{0xa2, 4}, input.Data...), 0xf8, []byte{10}), step{cmd: 2000, data: scan}, webType2Raw([]byte{0x3a, 4, 4}, 0xfc, input.Data), step{cmd: 2000, data: scan}, webType2Raw([]byte{0x60}, 0xf4, version), step{cmd: 2000, data: scan}, step{cmd: 2101, status: status})
			ws, h := configFixture(t, steps)
			token, err := ws.physicalConfirmation("type2", input)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(type2PhysicalWriteBody{Request: input, Confirmation: token, Confirm: true})
			task := submitTask(t, ws, h, "/api/reader/type2-write", string(body), "type2-physical-write-001")
			out := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
			var result chameleon.Type2PageWriteResult
			if cleanupFailure {
				var envelope struct {
					Result chameleon.Type2PageWriteResult `json:"result"`
				}
				json.Unmarshal(out.Result, &envelope)
				result = envelope.Result
				if out.State != "failed" || out.Outcome != "write_outcome_unknown" || result.Verified {
					t.Fatal(out.State, out.Outcome, string(out.Result))
				}
			} else {
				json.Unmarshal(out.Result, &result)
				if out.State != "succeeded" || !result.Verified {
					t.Fatal(out.State, string(out.Result))
				}
			}
			if !out.Write || out.WriteAttempted == nil || !*out.WriteAttempted || !result.WriteAttempted || !result.Acknowledged || !result.ReadbackMatches {
				t.Fatal(out, string(out.Result))
			}
			requests := ws.sessionClient.Stats().Requests
			repeated := submitTask(t, ws, h, "/api/reader/type2-write", string(body), "type2-physical-write-001")
			if repeated.ID != task.ID || ws.sessionClient.Stats().Requests != requests {
				t.Fatal("physical write was replayed")
			}
		})
	}
}
func TestWebType2ModeledReadTaskReturnsMaskedMetadata(t *testing.T) {
	scan := webType2Scan()
	version := []byte{0, 4, 4, 2, 1, 0, 0x0f, 3}
	steps := append(readerPreflight(1001, 1002, 2000, 2010, 2101), step{cmd: 1002, status: 0x68, data: []byte{1}}, step{cmd: 2000, data: scan}, webType2Raw([]byte{0x60}, 0xf4, version), step{cmd: 2000, data: scan}, step{cmd: 2000, data: scan}, webType2Raw([]byte{0x3a, 43, 44}, 0xfc, []byte{0xde, 0xad, 0xbe, 0xef, 0xbe, 0xef, 0, 0}), step{cmd: 2000, data: scan}, webType2Raw([]byte{0x60}, 0xf4, version), step{cmd: 2000, data: scan}, step{cmd: 2101, status: 0x68})
	ws, h := configFixture(t, steps)
	ws.allowWrites = false
	body, _ := json.Marshal(chameleon.Type2WorkflowRead{UID: webType2WriteInput().UID, Start: 43, Count: 2})
	task := submitTask(t, ws, h, "/api/reader/type2-workflow", string(body), "type2-modeled-read-001")
	out := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
	var result chameleon.ReadResult
	json.Unmarshal(out.Result, &result)
	if out.State != "succeeded" || out.Write || result.Type2 == nil || !result.Complete || !bytes.Equal(result.Data, make([]byte, 8)) || strings.Contains(string(out.Result), "DEADBEEF") {
		t.Fatal(out.State, string(out.Result))
	}
	if err := chameleon.ValidateType2ReadResult(result); err != nil {
		t.Fatal(err)
	}
}
