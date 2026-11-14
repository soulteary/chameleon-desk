package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	chameleon "github.com/soulteary/chameleon-desk"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func slotInterfaceCapture(t *testing.T, phase string, persist, activate bool) (chameleon.GUICard, chameleon.GUICardLoadPlan, []step) {
	t.Helper()
	card, err := chameleon.ParseGUICard([]byte(`{"id":"simulated","name":"Offline sample","tag":100,"uid":"0102030405","sak":0,"atqa":[0,0],"ats":[],"data":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := chameleon.PlanGUICardLoad(card, 2)
	if err != nil {
		t.Fatal(err)
	}
	var caps []byte
	for _, cmd := range []chameleon.Command{chameleon.CmdGetActiveSlot, chameleon.CmdGetSlotTagNick, chameleon.CmdEm410xGetEmuId} {
		caps = binary.BigEndian.AppendUint16(caps, uint16(cmd))
	}
	if persist {
		caps = binary.BigEndian.AppendUint16(caps, uint16(chameleon.CmdSlotDataConfigSave))
	}
	for _, s := range plan.Steps {
		caps = binary.BigEndian.AppendUint16(caps, uint16(s.Command))
	}
	steps := []step{{cmd: 1002, status: 0x68, data: []byte{1}}, {cmd: 1001, status: 0x68, payload: []byte{0}}, {cmd: 1035, status: 0x68, data: caps}, {cmd: 1018, status: 0x68, data: []byte{0}}}
	for i, s := range plan.Steps {
		status := uint16(0x68)
		if phase == "write-failure" && i == 2 {
			status = 0x66
		}
		steps = append(steps, step{cmd: uint16(s.Command), payload: s.Payload, status: status})
		if status != 0x68 {
			break
		}
	}
	if phase != "write-failure" {
		id := []byte{0, 100, 1, 2, 3, 4, 5}
		if phase == "verify-failure" {
			id[6] = 9
		}
		steps = append(steps, step{cmd: uint16(chameleon.CmdEm410xGetEmuId), status: 0x68, data: id})
		if phase != "verify-failure" {
			steps = append(steps, step{cmd: 1008, payload: []byte{1, 1}, status: 0x68, data: []byte(plan.Nickname)})
		}
	}
	if !activate || phase == "write-failure" || phase == "verify-failure" {
		status := uint16(0x68)
		if phase == "slot-restore-failure" {
			status = 0x66
		}
		steps = append(steps, step{cmd: 1003, payload: []byte{0}, status: status})
	}
	if persist && phase != "write-failure" && phase != "verify-failure" && phase != "slot-restore-failure" {
		status := uint16(0x68)
		if phase == "save-failure" {
			status = 0x66
		}
		steps = append(steps, step{cmd: 1009, status: status})
	}
	status := uint16(0x68)
	if phase == "mode-restore-failure" {
		status = 0x66
	}
	steps = append(steps, step{cmd: 1001, payload: []byte{1}, status: status})
	return card, plan, steps
}

func TestSlotLoadPhasesAgreeAcrossSDKCLIAndWeb(t *testing.T) {
	for _, tc := range []struct {
		name, phase       string
		persist, activate bool
	}{
		{"RAM", "complete", false, false}, {"flash", "complete", true, false}, {"activate", "complete", false, true},
		{"write", "write-failure", true, true}, {"verify", "verify-failure", true, true},
		{"save", "save-failure", true, true}, {"restore-slot", "slot-restore-failure", true, false}, {"restore-mode", "mode-restore-failure", true, true},
	} {
		for _, entry := range []string{"SDK", "CLI", "Web"} {
			t.Run(tc.name+"/"+entry, func(t *testing.T) {
				card, plan, steps := slotInterfaceCapture(t, tc.phase, tc.persist, tc.activate)
				var out chameleon.GUICardLoadResult
				failed := false
				if entry == "CLI" {
					var consumed atomic.Int32
					port := workflowSimulator(t, func(cmd uint16, payload []byte) (uint16, []byte) {
						i := int(consumed.Add(1)) - 1
						if i >= len(steps) {
							t.Errorf("unexpected command %d", cmd)
							return 0x66, nil
						}
						s := steps[i]
						if cmd != s.cmd || !bytes.Equal(payload, s.payload) {
							t.Errorf("command %d/%X, want %d/%X", cmd, payload, s.cmd, s.payload)
						}
						return s.status, s.data
					})
					dir := t.TempDir()
					input, output := filepath.Join(dir, "card.json"), filepath.Join(dir, "result.json")
					writeJSONFile(t, input, card)
					boolFlag := func(value bool) string {
						if value {
							return "true"
						}
						return "false"
					}
					err := runLibrary([]string{"slot", "load-card", "--input", input, "--output", output, "--slot", "2", "--yes", "--port", port, "--save=" + boolFlag(tc.persist), "--activate=" + boolFlag(tc.activate)})
					failed = err != nil
					raw, err := os.ReadFile(output)
					if err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(raw, &out); err != nil {
						t.Fatal(err)
					}
					if int(consumed.Load()) != len(steps) {
						t.Fatal("CLI skipped cleanup steps")
					}
				} else {
					open := mockDevice(t, steps)
					if entry == "SDK" {
						client, _ := open("simulated", time.Second)
						var err error
						out, err = client.LoadGUICard(context.Background(), 2, card, chameleon.GUICardLoadOptions{Persist: tc.persist, Activate: tc.activate})
						failed = err != nil
					} else {
						ws := &webServer{port: "simulated", timeout: time.Second, allowWrites: true, openDevice: func(_ context.Context, p string, timeout time.Duration) (*chameleon.Client, error) {
							return open(p, timeout)
						}}
						w := interfaceRequest(t, ws, offlineWeb(t, ws), "/api/slot/load-card", map[string]any{"slot": 2, "card": card, "persist": tc.persist, "activate": tc.activate})
						failed = w.Code != 200
						if failed {
							var result struct {
								Error  string                      `json:"error"`
								Result chameleon.GUICardLoadResult `json:"result"`
							}
							if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 502 || result.Error == "" {
								t.Fatalf("missing error envelope: %s %v", w.Body.String(), err)
							}
							out = result.Result
						} else if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
							t.Fatal(err)
						}
					}
				}
				verified := tc.phase != "write-failure" && tc.phase != "verify-failure"
				persisted := verified && tc.persist && tc.phase != "save-failure" && tc.phase != "slot-restore-failure"
				completed := len(plan.Steps)
				if tc.phase == "write-failure" {
					completed = 2
				}
				if failed != (tc.phase != "complete") || out.Plan.Slot != 2 || out.Verified != verified || out.Persisted != persisted || out.Activated != (verified && tc.activate) || out.CompletedSteps != completed {
					t.Fatalf("phase result differs: %+v failed=%v", out, failed)
				}
			})
		}
	}
}
