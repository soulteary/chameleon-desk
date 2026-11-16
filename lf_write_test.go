package chameleon

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func lfWriteRequest(t *testing.T) LFPhysicalWriteRequest {
	t.Helper()
	c, err := lfPromotionDraft(t, "pac", 0, HexBytes("12345678")).PromoteLFDraft()
	if err != nil {
		t.Fatal(err)
	}
	return LFPhysicalWriteRequest{Schema: 1, Source: c, Protocol: "pac", NewPassword: HexBytes{0xde, 0xad, 0xbe, 0xef}, OldPasswords: []HexBytes{{1, 2, 3, 4}}, TargetIsolated: true}
}
func TestLFPhysicalPlanBindsExplicitInputsWithoutPasswords(t *testing.T) {
	r := lfWriteRequest(t)
	p, err := r.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if p.TargetUIDBound || p.FirmwareEncodingValidation || p.Readback.Protocol != "pac" || p.WriteCommand != CmdPacWriteToT55xx || !bytes.Equal(p.Record.ID, []byte("12345678")) {
		t.Fatal(p)
	}
	commands, err := r.Requirements()
	if err != nil || len(commands) != 4 {
		t.Fatal(commands, err)
	}
	for _, want := range []Command{CmdChangeDeviceMode, CmdGetDeviceMode, CmdPacScan, CmdPacWriteToT55xx} {
		found := false
		for _, got := range commands {
			found = found || got == want
		}
		if !found {
			t.Fatal(want, commands)
		}
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "DEADBEEF") || strings.Contains(string(raw), "01020304") || strings.Contains(string(raw), "password\"") {
		t.Fatal(string(raw))
	}
	r.Source.Tag = 101
	r.Source.UID = "0102030405"
	r.Protocol = "em410x"
	p, err = r.Plan()
	if err != nil || p.SourceTag != 101 || p.Record.TagType != 100 || p.Readback.Protocol != "em410x" {
		t.Fatal(p, err)
	}
}
func TestLFPhysicalPlanRejectsUnqualifiedInputs(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*LFPhysicalWriteRequest)
	}{
		{"schema", func(r *LFPhysicalWriteRequest) { r.Schema = 0 }},
		{"protocol", func(r *LFPhysicalWriteRequest) { r.Protocol = "viking" }},
		{"target", func(r *LFPhysicalWriteRequest) { r.TargetIsolated = false }},
		{"new-password", func(r *LFPhysicalWriteRequest) { r.NewPassword = nil }},
		{"old-password", func(r *LFPhysicalWriteRequest) { r.OldPasswords = nil }},
		{"old-password-size", func(r *LFPhysicalWriteRequest) { r.OldPasswords = []HexBytes{{1}} }},
		{"memory", func(r *LFPhysicalWriteRequest) { r.Source.Data = [][]byte{{0}} }},
		{"draft", func(r *LFPhysicalWriteRequest) { r.Source = lfPromotionDraft(t, "pac", 0, HexBytes("12345678")) }},
		{"diagnostic", func(r *LFPhysicalWriteRequest) { r.Source.Tag = 203; r.Protocol = "em4x05" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := lfWriteRequest(t)
			tc.edit(&r)
			if _, err := r.Plan(); err == nil {
				t.Fatal("unsafe plan accepted")
			}
		})
	}
}
