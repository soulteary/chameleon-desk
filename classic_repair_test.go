package chameleon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func repairPair(t *testing.T) (GUICard, GUICard) {
	t.Helper()
	full := classicPromotionResult(1000, HexBytes{1, 2, 3, 4})
	for at := 3*16 + 10; at < 4*16; at++ {
		full.Dump.Data[at] = 0xBB
	}
	missing := full
	copyDump := *full.Dump
	copyDump.Data = append(HexBytes(nil), full.Dump.Data...)
	for at := 3*16 + 10; at < 4*16; at++ {
		copyDump.Data[at] = 0
	}
	copyDump.UnknownKeys = []ClassicUnknownKey{{0, KeyB}}
	copyDump.SHA256 = imageDigest(copyDump.Data)
	missing.Dump = &copyDump
	prefix := full
	prefix.Complete = false
	prefix.Read = 4
	prefixDump := *full.Dump
	prefixDump.Data = append(HexBytes(nil), full.Dump.Data[:64]...)
	prefixDump.SHA256 = imageDigest(prefixDump.Data)
	prefix.Dump = &prefixDump
	return promotionDraft(t, missing, ""), promotionDraft(t, prefix, "interrupted after sector 0")
}
func TestClassicMergeFillsVerifiedKeyBytesAndPreservesSources(t *testing.T) {
	left, right := repairPair(t)
	leftRaw, _ := json.Marshal(left)
	rightRaw, _ := json.Marshal(right)
	preview, err := left.ClassicMergePreview(right)
	if err != nil || preview.FilledBytes != 6 || len(preview.Conflicts) != 0 || !preview.HasCompleteRead {
		t.Fatal(preview, err)
	}
	if _, err = left.MergeClassicDraft(right, ClassicMergeOptions{}); err == nil {
		t.Fatal("implicit same-card claim accepted")
	}
	merged, err := left.MergeClassicDraft(right, ClassicMergeOptions{ConfirmSameCard: true})
	if err != nil {
		t.Fatal(err)
	}
	if merged.ID == left.ID || merged.ID == right.ID {
		t.Fatal("source overwritten")
	}
	draft, err := merged.ReadDraft()
	if err != nil {
		t.Fatal(err)
	}
	if draft.Rows[3][20:] != "BBBBBBBBBBBB" || len(draft.Result.Dump.UnknownKeys) != 1 {
		t.Fatal(draft)
	}
	effective, message, err := merged.ClassicRepairEffectiveRead(*draft)
	if err != nil || message != "" || !effective.Complete || len(effective.Dump.UnknownKeys) != 0 {
		t.Fatal(effective, message, err)
	}
	if err = merged.ClassicPromotionError(); err != nil {
		t.Fatal(err)
	}
	standard, err := merged.PromoteClassicDraft()
	if err != nil || standard.Data[3][10] != 0xBB {
		t.Fatal(standard, err)
	}
	a, _ := json.Marshal(left)
	b, _ := json.Marshal(right)
	if !bytes.Equal(a, leftRaw) || !bytes.Equal(b, rightRaw) {
		t.Fatal("source mutation")
	}
	raw, _ := json.Marshal(merged)
	if !bytes.Contains(raw, []byte("interrupted after sector 0")) {
		t.Fatal("error history lost")
	}
	if _, err = ParseGUICard(raw); err != nil {
		t.Fatal(err)
	}
	rows := append([]string(nil), draft.Rows...)
	rows[3] = rows[3][:20] + "AA" + rows[3][22:]
	if _, err = merged.EditData("invalid key edit", rows); err == nil {
		t.Fatal("merge key evidence overwritten")
	}
}
func TestClassicMergeConflictNeedsExactSourceChoices(t *testing.T) {
	left, right := repairPair(t)
	d, _ := right.ReadDraft()
	d.Rows[1] = "AA" + d.Rows[1][2:]
	right, err := right.EditData("edited source", d.Rows)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := left.ClassicMergePreview(right)
	if err != nil || len(preview.Conflicts) != 1 || preview.Conflicts[0].Block != 1 || preview.Conflicts[0].Byte != 0 {
		t.Fatal(preview, err)
	}
	for _, choices := range [][]ClassicMergeResolution{nil, {{Block: 1, Byte: 0, Source: "value"}}, {{Block: 2, Byte: 0, Source: "right"}}, {{Block: 1, Byte: 0, Source: "right"}, {Block: 1, Byte: 0, Source: "left"}}} {
		if _, err = left.MergeClassicDraft(right, ClassicMergeOptions{ConfirmSameCard: true, Resolutions: choices}); err == nil {
			t.Fatal("bad choices accepted", choices)
		}
	}
	merged, err := left.MergeClassicDraft(right, ClassicMergeOptions{ConfirmSameCard: true, Resolutions: []ClassicMergeResolution{{Block: 1, Byte: 0, Source: "right"}}})
	if err != nil {
		t.Fatal(err)
	}
	draft, _ := merged.ReadDraft()
	if !strings.HasPrefix(draft.Rows[1], "AA") {
		t.Fatal(draft.Rows[1])
	}
	// Further known-data edits remain explicit inputs to a later merge.
	rows := append([]string(nil), draft.Rows...)
	rows[2] = "CC" + rows[2][2:]
	merged, err = merged.EditData("new edits", rows)
	if err != nil {
		t.Fatal(err)
	}
	next, err := merged.ClassicMergePreview(left)
	if err != nil || len(next.Conflicts) != 2 {
		t.Fatal(next, err)
	}
}
func TestClassicMergeRejectsIdentityChangesAndHasNoInventedAuthority(t *testing.T) {
	left, right := repairPair(t)
	d, _ := right.ReadDraft()
	r := d.Result
	r.Identity = &ClassicIdentity{Schema: 1, UID: r.UID, ATQA: HexBytes{0, 0x44}, SAK: r.Identity.SAK, ATS: r.Identity.ATS}
	changed := promotionDraft(t, r, d.Error)
	if _, err := left.ClassicMergePreview(changed); err == nil {
		t.Fatal("UID-only merge accepted")
	}
	r = classicPromotionResult(1000, HexBytes{1, 2, 3, 4})
	r.Complete = false
	a := promotionDraft(t, r, "cleanup failed")
	b := promotionDraft(t, r, "interrupted")
	merged, err := a.MergeClassicDraft(b, ClassicMergeOptions{ConfirmSameCard: true})
	if err != nil {
		t.Fatal(err)
	}
	effective, reason, err := merged.ClassicRepairEffectiveRead(*mustDraft(t, merged))
	if err != nil || effective.Complete || reason == "" || merged.ClassicPromotionError() == nil {
		t.Fatal(effective, reason, err)
	}
	raw, _ := json.Marshal(merged)
	if !bytes.Contains(raw, []byte("cleanup failed")) || !bytes.Contains(raw, []byte("interrupted")) {
		t.Fatal("errors not preserved")
	}
	// Malformed editable rows fail validation rather than indexing out of range.
	var fields map[string]json.RawMessage
	json.Unmarshal(merged.fields["desk_read"], &fields)
	fields["rows"] = json.RawMessage(`["A"]`)
	bad := merged.withField("desk_read", fields)
	if bad.Validate() == nil {
		t.Fatal("malformed rows accepted")
	}
	for i := 0; i < 12; i++ {
		next, err := left.MergeClassicDraft(right, ClassicMergeOptions{ConfirmSameCard: true})
		if err != nil {
			if i < 7 {
				t.Fatal(err)
			}
			return
		}
		left = next
	}
	t.Fatal("merge evidence was not bounded")
}
func mustDraft(t *testing.T, c GUICard) *ReadCardData {
	t.Helper()
	d, e := c.ReadDraft()
	if e != nil || d == nil {
		t.Fatal(e)
	}
	return d
}

func TestClassicRepairAndVerificationRejectBeforeIO(t *testing.T) {
	c := readerSimulator(t, nil)
	left, _ := repairPair(t)
	r := ClassicRepairRequest{Source: left, Read: classicReadInput()}
	r.Read.UID = HexBytes{4, 3, 2, 1}
	if _, err := c.ReadClassicRepair(context.Background(), r); err == nil {
		t.Fatal("wrong UID reread")
	}
	id := classicPromotionResult(1000, HexBytes{1, 2, 3, 4}).Identity
	for _, keys := range [][]ClassicKeyCandidate{nil, {{Sector: 0, Type: KeyA, Key: HexBytes{1}}}, {{Sector: 0, Type: KeyA, Key: make(HexBytes, 6)}, {Sector: 0, Type: KeyA, Key: make(HexBytes, 6)}}} {
		if _, err := c.VerifyClassicKeys(context.Background(), ClassicKeyVerificationRequest{Identity: *id, Keys: keys}); err == nil {
			t.Fatal(keys)
		}
	}
	if err := ValidateClassicRecoveryHelpers(t.TempDir(), "nested"); err == nil {
		t.Fatal("missing tools accepted")
	}
}
func TestClassicKeyVerificationUsesReadableKeyBWithoutGuessing(t *testing.T) {
	r := classicPromotionResult(1000, HexBytes{1, 2, 3, 4})
	identity := *r.Identity
	scan := []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}
	a, b := bytes.Repeat([]byte{0x11}, 6), bytes.Repeat([]byte{0x22}, 6)
	trailer := append([]byte(nil), r.Dump.Data[48:64]...)
	copy(trailer[10:16], b)
	authA := append([]byte{0x60, 0}, a...)
	authB := append([]byte{0x61, 0}, b...)
	read := append([]byte{0x60, 3}, a...)
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: scan}, {command: 2007, request: authA}, {command: 2000, data: scan}, {command: 2007, request: authB, status: 6}, {command: 2008, request: read, data: trailer}, {command: 2000, data: scan}})
	out, err := c.VerifyClassicKeys(context.Background(), ClassicKeyVerificationRequest{Identity: identity, Keys: []ClassicKeyCandidate{{Sector: 0, Type: KeyB, Key: b}, {Sector: 0, Type: KeyA, Key: a}}})
	if err != nil || !out.Complete || !out.AllVerified || len(out.Keys) != 2 || !out.Keys[1].Readable {
		t.Fatal(out, err)
	}
	raw, _ := json.Marshal(out)
	if bytes.Contains(raw, []byte("111111111111")) || bytes.Contains(raw, []byte("222222222222")) {
		t.Fatal("candidate keys copied into result")
	}
}
func TestClassicRecoveryBoundsAndMissingDependencies(t *testing.T) {
	r := ClassicRecoveryRequest{Identity: *classicPromotionResult(1000, HexBytes{1, 2, 3, 4}).Identity, KnownBlock: 0, KnownType: KeyA, KnownKey: make(HexBytes, 6), TargetBlock: 4, TargetType: KeyB, Strategy: "nested", LimitSeconds: 3, MaxRuns: 1, MaxAttempts: 1}
	c := readerSimulator(t, nil)
	if _, err := c.RecoverClassicBound(context.Background(), r, t.TempDir()); err == nil {
		t.Fatal("missing dependency executed RF")
	}
	r.LimitSeconds = 31
	if _, err := r.Requirements(); err == nil {
		t.Fatal("unbounded recovery accepted")
	}
	if runtimeSuffix() == "" {
		dir := t.TempDir()
		for _, name := range []string{"nested", "staticnested"} {
			os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0600)
		}
		if ValidateClassicRecoveryHelpers(dir, "nested") == nil {
			t.Fatal("nonexecutable helper accepted")
		}
	}
}
func runtimeSuffix() string {
	if os.PathSeparator == '\\' {
		return ".exe"
	}
	return ""
}

func classicPhysicalRequest() ClassicPhysicalWriteRequest {
	backup := classicPromotionResult(1000, HexBytes{1, 2, 3, 4})
	source := *backup.Dump
	source.Data = append(HexBytes(nil), source.Data...)
	source.Data[16] = 0xA5
	source.SHA256 = imageDigest(source.Data)
	target := ClassicRead{UID: append(HexBytes(nil), backup.UID...)}
	for sector := 0; sector < 5; sector++ {
		target.Sectors = append(target.Sectors, SectorKeys{Sector: sector, A: bytes.Repeat([]byte{0x22}, 6)})
	}
	return ClassicPhysicalWriteRequest{Source: source, Target: target, Backup: backup}
}
func classicPhysicalBackupSteps(r ClassicPhysicalWriteRequest, changed bool) []readerStep {
	scan := []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}}
	for sector := 0; sector < 5; sector++ {
		steps = append(steps, readerStep{command: 2000, data: scan})
		for block := sector * 4; block < sector*4+4; block++ {
			data := append([]byte(nil), r.Backup.Dump.Data[block*16:(block+1)*16]...)
			if changed && block == 1 {
				data[0] ^= 1
			}
			request := append([]byte{0x60, byte(block)}, r.Target.Sectors[sector].A...)
			steps = append(steps, readerStep{command: 2008, request: request, data: data})
		}
	}
	return append(steps, readerStep{command: 2000, data: scan})
}
func TestClassicPhysicalWritePlanProtectsTargetAndSkipsManufacturer(t *testing.T) {
	r := classicPhysicalRequest()
	plan, err := PlanClassicPhysicalWrite(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blocks) != 14 || !plan.ManufacturerSkipped || plan.Blocks[0] != 1 {
		t.Fatal(plan)
	}
	for _, block := range plan.Blocks {
		if block == 0 || block%4 == 3 {
			t.Fatal("protected block in data-only plan", block)
		}
	}
	alternate := r
	alternate.Target.Sectors = append([]SectorKeys(nil), r.Target.Sectors...)
	alternate.Target.Sectors[0].A = bytes.Repeat([]byte{0x33}, 6)
	other, err := PlanClassicPhysicalWrite(alternate)
	if err != nil || other.Digest != plan.Digest {
		t.Fatal("public plan digest exposes candidate-key testing", other, err)
	}
	// Neither known source nor backup trailer keys may influence public hashes.
	keyChanged := r
	keyChanged.Source.Data = append(HexBytes(nil), r.Source.Data...)
	keyChanged.Source.Data[3*16] ^= 0x55
	keyChanged.Source.Data[3*16+10] ^= 0x55
	keyChanged.Source.SHA256 = imageDigest(keyChanged.Source.Data)
	copyBackup := *r.Backup.Dump
	copyBackup.Data = append(HexBytes(nil), r.Backup.Dump.Data...)
	copyBackup.Data[3*16] ^= 0x33
	copyBackup.Data[3*16+10] ^= 0x33
	copyBackup.SHA256 = imageDigest(copyBackup.Data)
	keyChanged.Backup.Dump = &copyBackup
	changedPlan, e := PlanClassicPhysicalWrite(keyChanged)
	if e != nil || changedPlan.Digest != plan.Digest || changedPlan.SourcePublicSHA256 != plan.SourcePublicSHA256 || changedPlan.BackupPublicSHA256 != plan.BackupPublicSHA256 {
		t.Fatal("public plan hashes secret trailer credentials", changedPlan, e)
	}
	raw, _ := json.Marshal(plan)
	if bytes.Contains(raw, []byte(r.Source.SHA256)) || bytes.Contains(raw, []byte(r.Backup.Dump.SHA256)) {
		t.Fatal("private full-dump checksums leaked into public plan")
	}
	c := readerSimulator(t, nil)
	if _, err = c.WriteClassicPlanned(context.Background(), r, nil); err == nil {
		t.Fatal("missing confirmation accepted")
	}
	r.Confirm = true
	r.PlanDigest = "wrong"
	if _, err = c.WriteClassicPlanned(context.Background(), r, nil); err == nil {
		t.Fatal("stale plan accepted")
	}
	r.Backup.Complete = false
	if _, err = PlanClassicPhysicalWrite(r); err == nil {
		t.Fatal("incomplete target backup accepted")
	}
}
func TestClassicPhysicalWriteRechecksBackupBeforeMutation(t *testing.T) {
	r := classicPhysicalRequest()
	plan, _ := PlanClassicPhysicalWrite(r)
	r.Confirm = true
	r.PlanDigest = plan.Digest
	c := readerSimulator(t, classicPhysicalBackupSteps(r, true))
	attempted := false
	out, err := c.WriteClassicPlanned(context.Background(), r, func() { attempted = true })
	if err == nil || attempted || out.WriteAttempted || out.Outcome != "not_started" || out.Backup == nil {
		t.Fatal(out, err)
	}
}
func TestClassicPhysicalWriteDataOnlyReadbackAndLostResponse(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified", true: "lost"}[lost], func(t *testing.T) {
			r := classicPhysicalRequest()
			plan, _ := PlanClassicPhysicalWrite(r)
			r.Confirm = true
			r.PlanDigest = plan.Digest
			steps := classicPhysicalBackupSteps(r, false)
			steps = append(steps, readerStep{command: 1002, status: 0x68, data: []byte{1}})
			scan := []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}
			var client *Client
			for sector := 0; sector < 5; sector++ {
				for block := sector * 4; block < sector*4+3; block++ {
					if block == 0 {
						continue
					}
					steps = append(steps, readerStep{command: 2000, data: scan})
					key := r.Target.Sectors[sector].A
					prefix := append([]byte{0x60, byte(block)}, key...)
					data := r.Source.Data[block*16 : (block+1)*16]
					steps = append(steps, readerStep{command: 2007, request: prefix})
					step := readerStep{command: 2009, request: append(append([]byte(nil), prefix...), data...)}
					if lost {
						step.before = func() { client.Close() }
						steps = append(steps, step)
						break
					}
					steps = append(steps, step, readerStep{command: 2008, request: prefix, data: data})
				}
				if lost {
					break
				}
			}
			client = readerSimulator(t, steps)
			calls := 0
			out, err := client.WriteClassicPlanned(context.Background(), r, func() { calls++ })
			if calls != 1 || !out.WriteAttempted || out.Backup == nil {
				t.Fatal(out, err)
			}
			if lost {
				if err == nil || out.Complete || out.Outcome != "write_outcome_unknown" {
					t.Fatal(out, err)
				}
			} else if err != nil || !out.Complete || out.Outcome != "verified" || out.Clone == nil || out.Clone.VerifiedBlocks != 14 || out.Clone.TrailersWritten {
				t.Fatal(out, err)
			}
		})
	}
}

func TestClassicSelectedRepairEvidenceAndConflicts(t *testing.T) {
	left, _ := repairPair(t)
	input := ClassicRepairRequest{Source: left, Read: classicPhysicalRequest().Target, Blocks: []int{3, 1}}
	input.Read.Sectors[0].B = bytes.Repeat([]byte{0xBB}, 6)
	d, _ := left.ReadDraft()
	trailer := append([]byte(nil), d.Result.Dump.Data[48:64]...)
	copy(trailer[10:], input.Read.Sectors[0].B)
	scan := []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}
	data := append([]byte(nil), d.Result.Dump.Data[16:32]...)
	data[0] = 0xAB
	read := func(block int) []byte { return append([]byte{0x60, byte(block)}, input.Read.Sectors[0].A...) }
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: scan}, {command: 2008, request: read(1), data: data}, {command: 2000, data: scan}, {command: 2008, request: read(3), data: trailer}, {command: 2000, data: scan}})
	out, err := c.ReadClassicSelectedRepair(context.Background(), input)
	if err != nil || !out.Complete || len(out.Observations) != 2 || out.Observations[0].Block != 1 {
		t.Fatal(out, err)
	}
	preview, err := left.ClassicSelectedRepairPreview(out)
	if err != nil || preview.FilledBytes != 6 || len(preview.Conflicts) != 1 {
		t.Fatal(preview, err)
	}
	if _, err = left.ApplyClassicSelectedRepair(out, ClassicMergeOptions{ConfirmSameCard: true}); err == nil {
		t.Fatal("conflict was silently overwritten")
	}
	merged, err := left.ApplyClassicSelectedRepair(out, ClassicMergeOptions{ConfirmSameCard: true, Resolutions: []ClassicMergeResolution{{Block: 1, Byte: 0, Source: "right"}}})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := merged.ReadDraft()
	if err != nil || draft.Rows[1][:2] != "AB" || draft.Rows[3][20:] != "BBBBBBBBBBBB" {
		t.Fatal(draft, err)
	}
	if err = merged.ClassicPromotionError(); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(merged)
	if _, err = ParseGUICard(raw); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"requested_blocks":[1,3]`)) {
		t.Fatal("selected observation lost")
	}
	rows := append([]string(nil), draft.Rows...)
	rows[3] = "AA" + rows[3][2:]
	if _, err = merged.EditData("forged key", rows); err == nil {
		t.Fatal("edited selected key accepted")
	}
}
func TestClassicSelectedRepairPartialIsSparseArchivalEvidence(t *testing.T) {
	_, source := repairPair(t)
	input := ClassicRepairRequest{Source: source, Read: classicPhysicalRequest().Target, Blocks: []int{7, 1}}
	scan := []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}
	data := bytes.Repeat([]byte{0x22}, 16)
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: scan}, {command: 2008, request: append([]byte{0x60, 1}, input.Read.Sectors[0].A...), data: data}, {command: 2000, data: scan}, {command: 2008, request: append([]byte{0x60, 7}, input.Read.Sectors[1].A...), status: 2}})
	out, err := c.ReadClassicSelectedRepair(context.Background(), input)
	if err == nil || out.Complete || out.Error == "" || len(out.Observations) != 1 {
		t.Fatal(out, err)
	}
	merged, err := source.ApplyClassicSelectedRepair(out, ClassicMergeOptions{ConfirmSameCard: true})
	if err != nil {
		t.Fatal(err)
	}
	d, err := merged.ReadDraft()
	if err != nil {
		t.Fatal(err)
	}
	if d.Rows[7] != strings.Repeat("??", 16) || d.Error != "interrupted after sector 0" || merged.ClassicPromotionError() == nil {
		t.Fatal("partial evidence became a complete read", d)
	}
	raw, _ := json.Marshal(merged)
	if !bytes.Contains(raw, []byte(out.Error)) {
		t.Fatal("selected error discarded")
	}
	out.Identity.ATQA = HexBytes{0, 2}
	if _, err = source.ApplyClassicSelectedRepair(out, ClassicMergeOptions{ConfirmSameCard: true}); err == nil {
		t.Fatal("mismatched identity merged")
	}
}
func TestClassicSelectedRepairRejectsInvalidBlocksBeforeIO(t *testing.T) {
	source, _ := repairPair(t)
	input := ClassicRepairRequest{Source: source, Read: classicPhysicalRequest().Target}
	c := readerSimulator(t, nil)
	for _, blocks := range [][]int{nil, {-1}, {20}, {1, 1}} {
		input.Blocks = blocks
		if _, err := c.ReadClassicSelectedRepair(context.Background(), input); err == nil {
			t.Fatal("invalid selection accepted", blocks)
		}
	}
}

func TestClassicPhysicalWriteChecksFullIdentityAtMutationBoundary(t *testing.T) {
	r := classicPhysicalRequest()
	plan, _ := PlanClassicPhysicalWrite(r)
	r.Confirm = true
	r.PlanDigest = plan.Digest
	steps := classicPhysicalBackupSteps(r, false)
	// The card changes after the backup reread, retaining its UID but changing SAK.
	steps = append(steps, readerStep{command: 1002, status: 0x68, data: []byte{1}}, readerStep{command: 2000, data: []byte{4, 1, 2, 3, 4, 4, 0, 8, 0}})
	c := readerSimulator(t, steps)
	calls := 0
	out, err := c.WriteClassicPlanned(context.Background(), r, func() { calls++ })
	if err == nil || calls != 0 || out.WriteAttempted || out.Outcome != "not_started" {
		t.Fatal(out, err, calls)
	}
}
func TestClassicDictionaryBoundValidatesAndVerifiesExistingWorkflow(t *testing.T) {
	r := ClassicDictionaryRequest{Identity: *classicPromotionResult(1000, HexBytes{1, 2, 3, 4}).Identity, Sectors: 5, Keys: []HexBytes{bytes.Repeat([]byte{0xff}, 6)}, LimitSeconds: 5}
	c := readerSimulator(t, nil)
	if _, err := c.CheckClassicDictionaryBound(context.Background(), r); err == nil {
		t.Fatal("insufficient command timeout entered RF")
	}
	r.LimitSeconds = 31
	if _, err := r.Requirements(); err == nil {
		t.Fatal("unbounded dictionary")
	}
	r.LimitSeconds = 5
	r.Keys = append(r.Keys, r.Keys[0])
	if _, err := r.Requirements(); err == nil {
		t.Fatal("duplicate dictionary")
	}
	r.Keys = r.Keys[:1]
	scan := []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}
	reply := make([]byte, 490)
	reply[0] = 0xff
	reply[1] = 0xc0
	for at := 0; at < 10; at++ {
		copy(reply[10+6*at:], r.Keys[0])
	}
	mask := bytes.Repeat([]byte{0xff}, 10)
	mask[0] = 0
	mask[1] = 0x3f
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: scan}, {command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: scan}, {command: 2000, data: scan}, {command: 2012, request: append(mask, r.Keys[0]...), data: reply}}
	for sector := 0; sector < 5; sector++ {
		for _, kind := range []byte{0x60, 0x61} {
			steps = append(steps, readerStep{command: 2000, data: scan}, readerStep{command: 2007, request: append([]byte{kind, byte(sector * 4)}, r.Keys[0]...)})
		}
	}
	steps = append(steps, readerStep{command: 1002, status: 0x68, data: []byte{1}}, readerStep{command: 2000, data: scan})
	c = readerSimulator(t, steps)
	c.timeout = 5 * time.Second
	out, err := c.CheckClassicDictionaryBound(context.Background(), r)
	if err != nil || !out.Complete || !out.Verified || !out.KeyMap.Complete || out.KeyMap.Validate() != nil {
		t.Fatal(out, err)
	}
}

func TestClassicPhysicalWriteNeverReplaysStatusFailures(t *testing.T) {
	for _, status := range []uint16{2, 6} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			r := classicPhysicalRequest()
			for sector := range r.Target.Sectors {
				r.Target.Sectors[sector].B = bytes.Repeat([]byte{0x22}, 6)
			}
			plan, err := PlanClassicPhysicalWrite(r)
			if err != nil {
				t.Fatal(err)
			}
			r.Confirm = true
			r.PlanDigest = plan.Digest
			steps := classicPhysicalBackupSteps(r, false)
			prefix := append([]byte{0x60, 1}, r.Target.Sectors[0].A...)
			steps = append(steps, readerStep{command: 1002, status: 0x68, data: []byte{1}}, readerStep{command: 2000, data: []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}}, readerStep{command: 2007, request: prefix}, readerStep{command: 2009, request: append(append([]byte(nil), prefix...), r.Source.Data[16:32]...), status: status})
			c := readerSimulator(t, steps)
			calls := 0
			out, err := c.WriteClassicPlanned(context.Background(), r, func() { calls++ })
			if err == nil || calls != 1 || !out.WriteAttempted || out.Outcome != "write_outcome_unknown" || out.Clone.WrittenBlocks != 0 {
				t.Fatal(out, err, calls)
			}
		})
	}
}

func TestClassicRecoveryBoundDropsCandidateWhenFinalIdentityChanges(t *testing.T) {
	if runtimeSuffix() != "" {
		t.Skip("fixture uses POSIX executable helpers")
	}
	tools := t.TempDir()
	for _, name := range []string{"nested", "staticnested"} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte("#!/bin/sh\nprintf 'Key 1... FFFFFFFFFFFF\\n'\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	r := ClassicRecoveryRequest{Identity: *classicPromotionResult(1000, HexBytes{1, 2, 3, 4}).Identity, KnownBlock: 0, KnownType: KeyA, KnownKey: make(HexBytes, 6), TargetBlock: 4, TargetType: KeyB, Strategy: "nested", LimitSeconds: 3, MaxRuns: 1, MaxAttempts: 1}
	scan := []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}
	mode := readerStep{command: 1002, status: 0x68, data: []byte{1}}
	steps := []readerStep{mode, {command: 2000, data: scan}, mode, {command: 2000, data: scan}, mode, {command: 2002, data: []byte{0}}, {command: 2003, request: []byte{0x60, 0, 0, 0, 0, 0, 0, 0, 0x61, 4}, data: []byte{1, 2, 3, 4, 0, 0, 0, 1, 0, 0, 0, 2}}}
	candidate := bytes.Repeat([]byte{0xff}, 6)
	for i := 0; i < 3; i++ {
		steps = append(steps, mode, readerStep{command: 2000, data: scan}, readerStep{command: 2007, request: append([]byte{0x61, 4}, candidate...)})
	}
	steps = append(steps, mode, readerStep{command: 2000, data: []byte{4, 1, 2, 3, 4, 4, 0, 8, 0}})
	out, err := readerSimulator(t, steps).RecoverClassicBound(context.Background(), r, tools)
	if err == nil || out.Complete || out.Verified || len(out.Recovery.Key) != 0 || out.Verification == nil || out.Verification.VerifiedOnCard || len(out.Verification.Candidate) != 0 {
		t.Fatal(out, err)
	}
}
