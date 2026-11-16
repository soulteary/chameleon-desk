package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	chameleon "github.com/soulteary/chameleon-desk"
)

func webClassicPhysicalInput() chameleon.ClassicPhysicalWriteRequest {
	uid := chameleon.HexBytes{1, 2, 3, 4}
	sak := byte(9)
	ats := chameleon.HexBytes{}
	data := bytes.Repeat([]byte{0x22}, 320)
	for sector := 0; sector < 5; sector++ {
		copy(data[(sector*4+3)*16+6:], []byte{0xff, 7, 0x80})
	}
	hash := sha256.Sum256(data)
	dump := chameleon.ClassicCardDump{Schema: 2, UID: uid, TagType: 1000, Data: data, SHA256: hex.EncodeToString(hash[:])}
	backup := chameleon.ReadResult{Schema: 1, Kind: "classic", UID: uid, Requested: 20, Read: 20, Complete: true, Identity: &chameleon.ClassicIdentity{Schema: 1, UID: uid, ATQA: chameleon.HexBytes{0, 4}, SAK: &sak, ATS: &ats}, Dump: &dump}
	target := chameleon.ClassicRead{UID: uid}
	for sector := 0; sector < 5; sector++ {
		target.Sectors = append(target.Sectors, chameleon.SectorKeys{Sector: sector, A: bytes.Repeat([]byte{0x22}, 6)})
	}
	return chameleon.ClassicPhysicalWriteRequest{Source: dump, Target: target, Backup: backup}
}
func TestWebClassicWritePlanBindsFullRequestAndSessionBeforeIO(t *testing.T) {
	ws, h := configFixture(t, nil)
	input := webClassicPhysicalInput()
	body, _ := json.Marshal(input)
	w := configCall(ws, h, "/api/reader/classic-write-plan", string(body))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var receipt struct {
		Plan         chameleon.ClassicPhysicalWritePlan `json:"plan"`
		Confirmation string                             `json:"confirmation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || receipt.Confirmation == "" || len(receipt.Plan.Blocks) != 14 {
		t.Fatal(w.Body.String(), err)
	}
	if ws.sessionClient.Stats().Requests != 0 {
		t.Fatal("planning accessed hardware")
	}
	input.Confirm = true
	input.PlanDigest = receipt.Plan.Digest
	call := func(r chameleon.ClassicPhysicalWriteRequest, confirmation string) int {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"request": r, "confirmation": confirmation})
		return configCall(ws, h, "/api/reader/classic-write", string(body)).Code
	}
	if got := call(input, ""); got != 409 {
		t.Fatal("missing receipt", got)
	}
	input.Target.Sectors[0].A = bytes.Repeat([]byte{0x33}, 6)
	if got := call(input, receipt.Confirmation); got != 409 {
		t.Fatal("changed secret key accepted", got)
	}
	input.Target.Sectors[0].A = bytes.Repeat([]byte{0x22}, 6)
	ws.rotateSessionID()
	if got := call(input, receipt.Confirmation); got != 409 {
		t.Fatal("old session receipt accepted", got)
	}
	if ws.sessionClient.Stats().Requests != 0 {
		t.Fatal("invalid confirmation accessed hardware")
	}
	ws.allowWrites = false
	if got := call(input, receipt.Confirmation); got != 403 {
		t.Fatal("write policy", got)
	}
}
func TestWebClassicWorkflowValidationBeforeDeviceIO(t *testing.T) {
	ws, h := configFixture(t, nil)
	for _, tc := range []struct{ path, body string }{{"classic-repair", `{"read":{"uid":"01020304","sectors":[]}}`}, {"classic-verify", `{"identity":{},"keys":[]}`}, {"classic-dictionary", `{"identity":{},"sectors":5,"keys":[],"limit_seconds":1}`}, {"classic-recover", `{"strategy":"nested","limit_seconds":31}`}, {"classic-write-plan", `{}`}} {
		w := configCall(ws, h, "/api/reader/"+tc.path, tc.body)
		if w.Code != 400 && w.Code != 422 {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
	input := webClassicPhysicalInput()
	recovery := chameleon.ClassicRecoveryRequest{Identity: *input.Backup.Identity, KnownBlock: 0, KnownType: chameleon.KeyA, KnownKey: bytes.Repeat([]byte{0x22}, 6), TargetBlock: 4, TargetType: chameleon.KeyB, Strategy: "nested", LimitSeconds: 2, MaxRuns: 1, MaxAttempts: 1}
	body, _ := json.Marshal(recovery)
	w := configCall(ws, h, "/api/reader/classic-recover", string(body))
	if w.Code != 422 || !strings.Contains(w.Body.String(), "helper") {
		t.Fatal(w.Code, w.Body.String())
	}
	if ws.sessionClient.Stats().Requests != 0 {
		t.Fatal("invalid input or missing worker accessed device")
	}
}
func TestWebClassicVerificationCapabilityPreflight(t *testing.T) {
	ws, h := configFixture(t, readerPreflight(1001, 1002, 2000, 2008))
	input := webClassicPhysicalInput()
	request := chameleon.ClassicKeyVerificationRequest{Identity: *input.Backup.Identity, Keys: []chameleon.ClassicKeyCandidate{{Sector: 0, Type: chameleon.KeyA, Key: input.Target.Sectors[0].A}}}
	body, _ := json.Marshal(request)
	w := configCall(ws, h, "/api/reader/classic-verify", string(body))
	if w.Code != 502 || !strings.Contains(w.Body.String(), "missing_commands") || ws.sessionClient.Stats().Requests != 2 {
		t.Fatal(w.Code, w.Body.String(), ws.sessionClient.Stats())
	}
}
func TestWebClassicSelectedRepairTaskRetainsSparseObservation(t *testing.T) {
	input := webClassicPhysicalInput()
	source, err := chameleon.NewGUICardFromRead("target backup", input.Backup, "")
	if err != nil {
		t.Fatal(err)
	}
	request := chameleon.ClassicRepairRequest{Source: source, Read: input.Target, Blocks: []int{1}}
	scan := []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}
	steps := append(readerPreflight(1001, 1002, 2000, 2008), step{cmd: 1002, status: 0x68, data: []byte{1}}, step{cmd: 2000, data: scan}, step{cmd: 2008, payload: append([]byte{0x60, 1}, input.Target.Sectors[0].A...), data: input.Backup.Dump.Data[16:32]}, step{cmd: 2000, data: scan})
	ws, h := configFixture(t, steps)
	ws.allowWrites = false
	body, _ := json.Marshal(request)
	task := submitTask(t, ws, h, "/api/reader/classic-repair", string(body), "classic-selected-read-001")
	out := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
	var result chameleon.ClassicSelectedRepairResult
	if err = json.Unmarshal(out.Result, &result); err != nil || out.State != "succeeded" || out.Write || !result.Complete || len(result.Observations) != 1 || result.Observations[0].Block != 1 {
		t.Fatal(out.State, string(out.Result), err)
	}
	before := ws.sessionClient.Stats().Requests
	again := submitTask(t, ws, h, "/api/reader/classic-repair", string(body), "classic-selected-read-001")
	if again.ID != task.ID || ws.sessionClient.Stats().Requests != before {
		t.Fatal("receipt replayed sparse read")
	}
}
