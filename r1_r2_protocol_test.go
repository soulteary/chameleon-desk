package chameleon

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestType2NewModelsAndSignatureRemainDistinctFromVerification(t *testing.T) {
	for _, version := range [][]byte{{0, 4, 4, 1, 1, 0, 0x0b, 3}, {0, 4, 4, 1, 1, 0, 0x0e, 3}} {
		layout, err := type2LayoutForVersion(version)
		if err != nil {
			t.Fatal(err)
		}
		input := Type2WorkflowRead{UID: type2TestUID, Full: true, CaptureSignature: true}
		memory := type2TestMemory(layout)
		steps, _ := type2ReadFixture(version, input, memory)
		signature := bytes.Repeat([]byte{0xa5}, 32)
		tail := append([]readerStep(nil), steps[len(steps)-3:]...)
		steps = append(steps[:len(steps)-3], readerStep{command: 2000, data: type2TestScan(input.UID)}, type2RawStep([]byte{0x3c, 0}, 0xfc, signature), readerStep{command: 2000, data: type2TestScan(input.UID)})
		steps = append(steps, tail...)
		out, err := readerSimulator(t, steps).ReadType2Workflow(context.Background(), input)
		if err != nil || !out.Complete || out.Type2.Signature.Status != "observed" || out.Type2.Signature.Verified || !bytes.Equal(out.Type2.Signature.Data, signature) || len(out.Type2.Counters) != 0 {
			t.Fatal(out, err)
		}
		card, err := NewGUICardFromRead("new model", out, "")
		if err != nil {
			t.Fatal(err)
		}
		standard, err := card.PromoteType2Draft(type2PromotionOptions())
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(standard.fields["extra"], []byte("ultralightCounters")) || standard.Tag != layout.tag || !bytes.Contains(standard.fields["extra"], []byte("ultralightSignature")) {
			t.Fatal(standard)
		}
		sig := out.Type2.Signature
		sig.Verified = true
		if ValidateType2ReadResult(out) == nil {
			t.Fatal("observation claimed cryptographic verification")
		}
		sig.Verified = false
		// A mirror in these profiles is enabled by MIRROR_PAGE, without the
		// NTAG213 MIRROR_CONF bits. Reject virtual RF bytes during conversion.
		memory[(layout.passwordPage-2)*4+2] = 4
		if !type2HasMirror(layout, memory) {
			t.Fatal("NTAG210/212 mirror not recognized")
		}
	}
}

func batchFixture(r Type2BatchWrite, execute bool, fail int) []readerStep {
	out := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}}
	for _, p := range r.Pages {
		steps := type2WriteFixture(Type2PageWrite{UID: r.UID, Page: p.Page, Expected: p.Expected, Data: p.Data}, true)
		out = append(out, steps[1:]...)
	}
	if execute {
		for i, p := range r.Pages {
			steps := type2WriteFixture(Type2PageWrite{UID: r.UID, Page: p.Page, Expected: p.Expected, Data: p.Data}, false)
			if i == fail {
				steps[12].data = []byte{0}
				steps = append(steps[:13], readerStep{command: 2101, status: 0x68})
				out = append(out, steps[1:]...)
				break
			}
			out = append(out, steps[1:]...)
		}
	}
	return out
}
func batchInput() Type2BatchWrite {
	return Type2BatchWrite{UID: type2TestUID, ExpectedVersion: HexBytes{0, 4, 4, 2, 1, 0, 0x0f, 3}, Pages: []Type2PageChange{{4, HexBytes{1, 2, 3, 4}, HexBytes{4, 3, 2, 1}}, {5, HexBytes{1, 2, 3, 4}, HexBytes{8, 7, 6, 5}}, {6, HexBytes{1, 2, 3, 4}, HexBytes{9, 8, 7, 6}}}}
}

func TestType2BatchPreflightPrefixAndNoReplay(t *testing.T) {
	r := batchInput()
	plan, err := readerSimulator(t, batchFixture(r, false, -1)).PlanType2UserPages(context.Background(), r)
	if err != nil || !bytes.Equal(plan.Version, r.ExpectedVersion) || len(plan.Pages) != 3 {
		t.Fatal(plan, err)
	}
	for _, fail := range []int{-1, 0, 1} {
		calls := 0
		out, err := readerSimulator(t, batchFixture(r, true, fail)).WriteType2UserPagesObserved(context.Background(), r, func() { calls++ })
		if fail < 0 {
			if err != nil || !out.Complete || calls != 3 || out.VerifiedPages != 3 {
				t.Fatal(out, err)
			}
		} else {
			if err == nil || out.Complete || calls != fail+1 || out.VerifiedPages != fail || out.Pages[fail].Outcome != "write_outcome_unknown" {
				t.Fatal(out, calls, err)
			}
			for _, page := range out.Pages[fail+1:] {
				if page.Outcome != "not_started" || page.Result != nil {
					t.Fatal("suffix attempted", out)
				}
			}
		}
	}
	wrong := batchInput()
	wrong.ExpectedVersion = HexBytes{0, 4, 4, 2, 1, 0, 0x11, 3}
	steps := batchFixture(wrong, false, -1)
	steps = append(steps[:3], readerStep{command: 2101, status: 0x68})
	called := false
	out, err := readerSimulator(t, steps).WriteType2UserPagesObserved(context.Background(), wrong, func() { called = true })
	if err == nil || called || out.Outcome != "write_not_started" {
		t.Fatal(out, err)
	}
	missing := batchInput()
	missing.ExpectedVersion = nil
	if _, err := readerSimulator(t, nil).WriteType2UserPagesObserved(context.Background(), missing, nil); err == nil {
		t.Fatal("execution without planned model")
	}
	duplicate := batchInput()
	duplicate.Pages[1].Page = 4
	if _, err := duplicate.Requirements(); err == nil {
		t.Fatal("duplicate page accepted")
	}
}

func valueFixture(r ClassicValueRequest, plan bool, status uint16) []readerStep {
	uid := r.Identity.UID
	scan := append(append([]byte{byte(len(uid))}, uid...), 4, 0, 8, 0)
	key, _ := keyPayload(r.Block, r.KeyType, r.Key)
	out := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: scan}, {command: 2008, request: key, data: r.Expected}, {command: 2000, data: scan}}
	if !plan {
		out = append(out, readerStep{command: 2009, request: append(append([]byte(nil), key...), EncodeClassicValue(r.Value, r.Address)...), status: status})
		if status == 0 {
			out = append(out, readerStep{command: 2008, request: key, data: EncodeClassicValue(r.Value, r.Address)}, readerStep{command: 2000, data: scan})
		}
	}
	return append(out, readerStep{command: 2101, status: 0x68})
}
func TestGuardedValuePlanWriteAndAmbiguousFailure(t *testing.T) {
	r := ClassicValueRequest{Identity: *classicPromotionResult(1001, HexBytes{1, 2, 3, 4}).Identity, Block: 1, KeyType: KeyA, Key: HexBytes{255, 255, 255, 255, 255, 255}, Expected: EncodeClassicValue(10, 1), Value: 15, Address: 1}
	plan, err := readerSimulator(t, valueFixture(r, true, 0)).PlanClassicValue(context.Background(), r)
	if err != nil || plan.WriteAttempted || plan.Outcome != "planned" {
		t.Fatal(plan, err)
	}
	for _, status := range []uint16{0, 7} {
		calls := 0
		out, err := readerSimulator(t, valueFixture(r, false, status)).WriteClassicValueObserved(context.Background(), r, func() { calls++ })
		if calls != 1 || !out.WriteAttempted {
			t.Fatal(out, err)
		}
		if status == 0 {
			if err != nil || !out.Verified {
				t.Fatal(out, err)
			}
		} else if err == nil || out.Verified || out.Outcome != "write_outcome_unknown" {
			t.Fatal(out, err)
		}
	}
	for _, block := range []int{0, 3, 7, 127, 143, 255} {
		r.Block = block
		if _, err := r.Requirements(); err == nil {
			t.Fatal("restricted value block", block)
		}
	}
}

func TestReadOnlyMagicInspectionDoesNotClaimGen3Authentication(t *testing.T) {
	id := *classicPromotionResult(1001, HexBytes{1, 2, 3, 4}).Identity
	r := MagicInspection{Family: "gen3", Identity: id}
	scan := []byte{4, 1, 2, 3, 4, 4, 0, 8, 0}
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: scan}, type2RawStep([]byte{0x30, 0}, 0xfc, make([]byte, 16)), {command: 2000, data: scan}, {command: 2101, status: 0x68}}
	out, err := readerSimulator(t, steps).InspectMagic(context.Background(), r)
	if err != nil || !out.Observed || out.ChipAuthenticated || len(out.Block0) != 16 {
		t.Fatal(out, err)
	}
	raw, _ := json.Marshal(out)
	if !bytes.Contains(raw, []byte("cannot positively identify Gen3")) {
		t.Fatal(string(raw))
	}
}

func TestRequestedSignatureFailureRetainsMemoryWithoutGrantingConversion(t *testing.T) {
	version := []byte{0, 4, 4, 1, 1, 0, 0x0b, 3}
	layout, _ := type2LayoutForVersion(version)
	input := Type2WorkflowRead{UID: type2TestUID, Full: true, CaptureSignature: true}
	steps, _ := type2ReadFixture(version, input, type2TestMemory(layout))
	steps = append(steps[:len(steps)-3], readerStep{command: 2000, data: type2TestScan(input.UID)}, type2RawStep([]byte{0x3c, 0}, 0xfc, []byte{0}), readerStep{command: 2101, status: 0x68})
	out, err := readerSimulator(t, steps).ReadType2Workflow(context.Background(), input)
	if err == nil || out.Complete || out.Read == 0 || out.Type2.Signature.Status != "failed" || out.Type2.Signature.Verified || len(out.Type2.Signature.Data) != 0 {
		t.Fatal(out, err)
	}
	card, e := NewGUICardFromRead("partial", out, err.Error())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = card.PromoteType2Draft(type2PromotionOptions()); e == nil {
		t.Fatal("failed signature read granted conversion")
	}
}
