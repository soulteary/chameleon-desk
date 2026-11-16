package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	chameleon "github.com/soulteary/chameleon-desk"
)

func webSlotObservation(tag uint16) []step {
	types, flags := make([]byte, 32), make([]byte, 16)
	binary.BigEndian.PutUint16(types, tag)
	flags[0] = 1
	return []step{{cmd: 1002, status: 0x68, data: []byte{0}}, {cmd: 1018, status: 0x68, data: []byte{0}}, {cmd: 1019, status: 0x68, data: types}, {cmd: 1023, status: 0x68, data: flags}, {cmd: 1008, status: 0x68, payload: []byte{0, 2}, data: []byte("slot card")}, {cmd: 4018, status: 0x68, data: []byte{4, 1, 2, 3, 4, 4, 0, 8, 0}}}
}
func TestWebCurrentSlotTaskAndOfflineLibraryImport(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "partial"}[partial], func(t *testing.T) {
			tag := uint16(1001)
			input := chameleon.SlotReadRequest{Slot: 1, Sense: chameleon.SenseHF, TagType: tag}
			commands, _ := input.Requirements()
			var required []uint16
			for _, cmd := range commands {
				required = append(required, uint16(cmd))
			}
			caps := configCaps(required...)
			steps := append(readerPreflight(required...), step{cmd: 1035, status: 0x68, data: caps})
			steps = append(steps, webSlotObservation(tag)...)
			steps = append(steps, step{cmd: 4008, status: 0x68, payload: []byte{0, 32}, data: bytes.Repeat([]byte{0x42}, 512)})
			steps = append(steps, webSlotObservation(tag)...)
			if partial {
				steps = append(steps, step{cmd: 4008, status: 103, payload: []byte{32, 32}})
			} else {
				steps = append(steps, step{cmd: 4008, status: 0x68, payload: []byte{32, 32}, data: bytes.Repeat([]byte{0x43}, 512)})
				steps = append(steps, webSlotObservation(tag)...)
			}
			ws, h := configFixture(t, steps)
			ws.allowWrites = false
			body, _ := json.Marshal(input)
			task := submitTask(t, ws, h, "/api/slot/read-current", string(body), "slot-read-current-01")
			finished := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
			if finished.Write || finished.OperationID != "slot.read-current" {
				t.Fatal(finished)
			}
			var result chameleon.ReadResult
			if partial {
				var envelope struct {
					Result chameleon.ReadResult `json:"result"`
				}
				if e := json.Unmarshal(finished.Result, &envelope); e != nil {
					t.Fatal(e)
				}
				result = envelope.Result
				if finished.Outcome != "read_interrupted" || result.Read != 32 {
					t.Fatal(finished)
				}
			} else {
				if e := json.Unmarshal(finished.Result, &result); e != nil {
					t.Fatal(e)
				}
				if finished.State != "succeeded" || result.Read != 64 {
					t.Fatal(finished)
				}
			}
			if e := result.Slot.Validate(); e != nil {
				t.Fatal(e)
			}
			before := ws.sessionClient.Stats().Requests
			duplicate := submitTask(t, ws, h, "/api/slot/read-current", string(body), "slot-read-current-01")
			if duplicate.ID != task.ID || ws.sessionClient.Stats().Requests != before {
				t.Fatal("read replayed")
			}
			payload := map[string]any{"op": "import-read", "name": "snapshot", "read_result": result, "database": map[string]any{"flutter.cards": []string{}, "custom": "keep"}}
			raw, _ := json.Marshal(payload)
			request := httptest.NewRequest("POST", "http://127.0.0.1/api/database", bytes.NewReader(raw))
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != 200 || !strings.Contains(response.Body.String(), `"custom":"keep"`) || ws.sessionClient.Stats().Requests != before {
				t.Fatal(response.Code, response.Body.String())
			}
			var imported struct {
				Card chameleon.GUICard `json:"card"`
			}
			if e := json.Unmarshal(response.Body.Bytes(), &imported); e != nil {
				t.Fatal(e)
			}
			draft, e := imported.Card.ReadDraft()
			if e != nil {
				t.Fatal(e)
			}
			if draft.Result.Kind != "slot" || draft.Result.Slot.Identity.ATQA.String() != "0004" || imported.Card.RequireCardImage() == nil {
				t.Fatal(imported.Card)
			}
			if partial && draft.Rows[32] != strings.Repeat("??", 16) {
				t.Fatal("unknown suffix lost")
			}
		})
	}
}
func TestCurrentSlotRouteAuthorizationSessionAndValidationBeforeIO(t *testing.T) {
	ws, h := configFixture(t, nil)
	for _, test := range []struct {
		method, token, session, body string
		status                       int
	}{{"GET", ws.token, "", "", 405}, {"POST", "", "", `{}`, 403}, {"POST", ws.token, "old", `{}`, 409}, {"POST", ws.token, "", `{"slot":9,"sense":2,"tag_type":1001}`, 400}, {"POST", ws.token, "", `{"slot":1,"sense":2,"tag_type":1001,"persist":true}`, 400}} {
		r := httptest.NewRequest(test.method, "http://127.0.0.1/api/slot/read-current", strings.NewReader(test.body))
		r.Header.Set("X-Chameleon-Token", test.token)
		r.Header.Set("X-Chameleon-Session", test.session)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != test.status || ws.sessionClient.Stats().Requests != 0 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
