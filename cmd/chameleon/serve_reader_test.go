package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chameleon "github.com/soulteary/chameleon-desk"
)

func readerPreflight(commands ...uint16) []step {
	return []step{{cmd: 1033, status: 0x68, data: []byte{0}}, {cmd: 1035, status: 0x68, data: configCaps(commands...)}}
}
func webClassicInput() chameleon.ClassicRead {
	r := chameleon.ClassicRead{UID: chameleon.HexBytes{1, 2, 3, 4}}
	for i := 0; i < 5; i++ {
		r.Sectors = append(r.Sectors, chameleon.SectorKeys{Sector: i, A: chameleon.HexBytes{1, 2, 3, 4, 5, byte(6 + i)}})
	}
	return r
}

func TestWebClassicKeyedReadAndPartialTasks(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			input := webClassicInput()
			steps := append(readerPreflight(1001, 1002, 2000, 2008), step{cmd: 1002, status: 0x68, data: []byte{0}}, step{cmd: 1001, status: 0x68, payload: []byte{1}})
			for sector := 0; sector < 5; sector++ {
				steps = append(steps, step{cmd: 2000, data: []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}})
				for block := sector * 4; block < sector*4+4; block++ {
					payload := append([]byte{0x60, byte(block)}, input.Sectors[sector].A...)
					if partial && block == 1 {
						steps = append(steps, step{cmd: 2008, payload: payload, status: 2})
						break
					}
					data := bytes.Repeat([]byte{byte(block)}, 16)
					if block%4 == 3 {
						copy(data[6:9], []byte{0x7f, 0x07, 0x88})
					}
					steps = append(steps, step{cmd: 2008, payload: payload, data: data})
				}
				if partial {
					break
				}
			}
			steps = append(steps, step{cmd: 1001, status: 0x68, payload: []byte{0}})
			ws, h := configFixture(t, steps)
			ws.allowWrites = false
			body, _ := json.Marshal(input)
			task := submitTask(t, ws, h, "/api/reader/classic", string(body), "classic-keyed-001")
			out := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
			if out.Write {
				t.Fatal("read classified as write")
			}
			before := ws.sessionClient.Stats().Requests
			repeated := submitTask(t, ws, h, "/api/reader/classic", string(body), "classic-keyed-001")
			if repeated.ID != task.ID || ws.sessionClient.Stats().Requests != before {
				t.Fatal("query/repeated receipt executed the read again")
			}
			var result readerResult
			if partial {
				var envelope struct {
					Result readerResult `json:"result"`
				}
				if err := json.Unmarshal(out.Result, &envelope); err != nil {
					t.Fatal(err)
				}
				result = envelope.Result
				if out.State != "failed" || result.Complete || result.Read != 1 || len(result.Dump.Data) != 16 || result.Dump.SHA256 != "" {
					t.Fatal(out.State, string(out.Result))
				}
			} else {
				if err := json.Unmarshal(out.Result, &result); err != nil {
					t.Fatal(err)
				}
				if out.State != "succeeded" || !result.Complete || result.Read != 20 || len(result.Dump.UnknownKeys) != 5 || result.Dump.Validate() != nil {
					t.Fatal(out.State, string(out.Result))
				}
			}
		})
	}
}

func TestWebType2RangeTaskAndCleanupFailure(t *testing.T) {
	for _, cleanupFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(cleanupFailure), func(t *testing.T) {
			steps := append(readerPreflight(1001, 1002, 2000, 2010), step{cmd: 1002, status: 0x68, data: []byte{0}}, step{cmd: 1001, status: 0x68, payload: []byte{1}}, step{cmd: 2000, data: []byte{4, 1, 2, 3, 4, 4, 0, 0, 0}}, step{cmd: 2010, payload: []byte{0xf4, 1, 244, 0, 16, 0x30, 0}, data: bytes.Repeat([]byte{0x42}, 16)}, step{cmd: 2000, data: []byte{4, 1, 2, 3, 4, 4, 0, 0, 0}})
			status := uint16(0x68)
			if cleanupFailure {
				status = 0x67
			}
			steps = append(steps, step{cmd: 1001, status: status, payload: []byte{0}})
			ws, h := configFixture(t, steps)
			ws.allowWrites = false
			task := submitTask(t, ws, h, "/api/reader/type2", `{"uid":"01020304","start":1,"count":2}`, "type2-read-task01")
			out := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
			if cleanupFailure {
				if out.State != "failed" || !strings.Contains(string(out.Result), `"complete":false`) || !strings.Contains(string(out.Result), `"units_read":2`) {
					t.Fatal(out.State, string(out.Result))
				}
			} else if out.State != "succeeded" || !strings.Contains(string(out.Result), `"data":"4242424242424242"`) {
				t.Fatal(out.State, string(out.Result))
			}
			if out.Write {
				t.Fatal("page read classified as write")
			}
		})
	}
}

func TestWebLFSelectedProtocolAndNoCard(t *testing.T) {
	for _, absent := range []bool{false, true} {
		t.Run(fmt.Sprint(absent), func(t *testing.T) {
			status := uint16(0x40)
			data := make([]byte, 14)
			if absent {
				status = 0x41
				data = nil
			}
			steps := append(readerPreflight(1001, 1002, 3030), step{cmd: 1002, status: 0x68, data: []byte{1}}, step{cmd: 3030, payload: []byte{0x12, 0x34, 0x56, 0x78}, status: status, data: data})
			ws, h := configFixture(t, steps)
			ws.allowWrites = false
			w := configCall(ws, h, "/api/reader/lf", `{"protocol":"em4x05","password":305419896}`)
			if w.Code != 200 || !strings.Contains(w.Body.String(), fmt.Sprintf(`"present":%v`, !absent)) || strings.Contains(w.Body.String(), "305419896") {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestWebReaderValidationPolicyAndToken(t *testing.T) {
	ws, h := configFixture(t, nil)
	ws.allowWrites = false
	for _, tc := range []struct{ path, body string }{{"type2", `{"uid":"01020304","start":255,"count":2}`}, {"classic", `{"uid":"01020304","sectors":[]}`}, {"lf", `{"protocol":"idteck"}`}, {"lf", `{"protocol":"pac","password":0}`}, {"type2", `{"uid":"01020304","start":0,"count":1,"unknown":true}`}} {
		if w := configCall(ws, h, "/api/reader/"+tc.path, tc.body); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := taskCall(h, "wrong", "POST", "/api/reader/lf", `{"protocol":"em410x"}`)
	if w.Code != http.StatusForbidden {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1/api/reader/lf", strings.NewReader(`{"protocol":"em410x"}`))
	r.Header.Set("X-Chameleon-Token", ws.token)
	r.Header.Set("X-Chameleon-Session", "obsolete")
	stale := httptest.NewRecorder()
	h.ServeHTTP(stale, r)
	if stale.Code != 409 || ws.sessionClient.Stats().Requests != 0 {
		t.Fatal(stale.Code, stale.Body.String())
	}
	ws.nativePort = "native"
	w = configCall(ws, h, "/api/reader/lf", `{"protocol":"em410x"}`)
	if w.Code != 502 || ws.sessionClient.Stats().Requests != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestWebReaderLiteAndMissingCommandPreflight(t *testing.T) {
	for _, lite := range []bool{false, true} {
		t.Run(fmt.Sprint(lite), func(t *testing.T) {
			model := byte(0)
			caps := configCaps(1001, 1002)
			reason := "missing_commands"
			if lite {
				model = 1
				caps = configCaps(1001, 1002, 3000)
				reason = "unsupported_model"
			}
			ws, h := configFixture(t, []step{{cmd: 1033, status: 0x68, data: []byte{model}}, {cmd: 1035, status: 0x68, data: caps}})
			w := configCall(ws, h, "/api/reader/lf", `{"protocol":"em410x"}`)
			if w.Code != 502 || !strings.Contains(w.Body.String(), reason) || ws.sessionClient.Stats().Requests != 2 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestReaderQueuedCancelAndSessionReplacement(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprint(replace), func(t *testing.T) {
			ws, h := configFixture(t, nil)
			ws.allowWrites = false
			ws.gateOnce.Do(func() { ws.gate = make(chan struct{}, 1) })
			ws.gate <- struct{}{}
			defer func() {
				select {
				case <-ws.gate:
				default:
				}
			}()
			task := submitTask(t, ws, h, "/api/reader/type2", `{"uid":"01020304","start":0,"count":1}`, "queued-read-0012")
			if replace {
				ws.rotateSessionID()
			} else {
				if w := taskCall(h, ws.token, "POST", "/api/tasks/"+task.ID+"/cancel", "{}"); w.Code != 200 {
					t.Fatal(w.Code)
				}
			}
			<-ws.gate
			out := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
			if out.Write || out.Commands != 0 || out.Outcome != "not_started" {
				t.Fatal(out)
			}
		})
	}
}
