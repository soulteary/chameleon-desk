package chameleon

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func type2PromotionFixture(t *testing.T, version []byte) GUICard {
	t.Helper()
	layout, _ := type2LayoutForVersion(version)
	input := Type2WorkflowRead{UID: type2TestUID, Full: true}
	steps, _ := type2ReadFixture(version, input, type2TestMemory(layout))
	r, err := readerSimulator(t, steps).ReadType2Workflow(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewGUICardFromRead("Type2 read", r, "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func type2PromotionOptions() Type2PromotionOptions {
	return Type2PromotionOptions{EmulatorPassword: HexBytes{1, 2, 3, 4}, EmulatorPACK: HexBytes{5, 6}, ConfirmEmulatorCredentials: true}
}
func TestType2PromotionRecognizedLayoutsPreserveSourceMasksAndAssignOnlyChosenCredentials(t *testing.T) {
	for _, version := range [][]byte{{0, 4, 4, 2, 1, 0, 0x0f, 3}, {0, 4, 4, 2, 1, 0, 0x11, 3}, {0, 4, 4, 2, 1, 0, 0x13, 3}, {0, 4, 3, 1, 1, 0, 0x0b, 3}, {0, 4, 3, 2, 1, 0, 0x0e, 3}} {
		layout, _ := type2LayoutForVersion(version)
		t.Run(layout.name, func(t *testing.T) {
			source := type2PromotionFixture(t, version).withField("custom", map[string]any{"keep": true}).withField("extra", map[string]any{"vendor": "keep"})
			before, _ := json.Marshal(source)
			draft, _ := source.ReadDraft()
			rows := append([]string(nil), draft.Rows...)
			rows[4] = "AABBCCDD"
			edited, err := source.EditData("edited", rows)
			if err != nil {
				t.Fatal(err)
			}
			out, err := edited.PromoteType2Draft(type2PromotionOptions())
			if err != nil {
				t.Fatal(err)
			}
			if out.ID == source.ID || out.Tag != layout.tag || out.UID != type2TestUID.String() || !bytes.Equal(out.Data[4], []byte{0xaa, 0xbb, 0xcc, 0xdd}) || !bytes.Equal(out.Data[layout.passwordPage], []byte{1, 2, 3, 4}) || !bytes.Equal(out.Data[layout.packPage], []byte{5, 6, 0, 0}) {
				t.Fatal(out)
			}
			if d, e := out.ReadDraft(); e != nil || d != nil {
				t.Fatal(d, e)
			}
			raw, _ := json.Marshal(out)
			if !bytes.Contains(raw, []byte(`"desk_read_origin"`)) || !bytes.Contains(raw, []byte(`"hidden_credentials"`)) || !bytes.Contains(raw, []byte(`"emulator_credentials_assigned":true`)) || !bytes.Contains(raw, []byte(`"vendor":"keep"`)) {
				t.Fatal(string(raw))
			}
			roundtrip, e := ParseGUICard(raw)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = roundtrip.EmulatorImage(); e != nil {
				t.Fatal(e)
			}
			if _, e = PlanGUICardLoad(roundtrip, 1); e != nil {
				t.Fatal(e)
			}
			after, _ := json.Marshal(source)
			if !bytes.Equal(before, after) {
				t.Fatal("source mutated")
			}
		})
	}
}
func TestType2PromotionRefusesImplicitSecretsPartialReadsAndInventedPages(t *testing.T) {
	version := []byte{0, 4, 4, 2, 1, 0, 0x0f, 3}
	c := type2PromotionFixture(t, version)
	for _, o := range []Type2PromotionOptions{{}, {EmulatorPassword: HexBytes{1, 2, 3, 4}, EmulatorPACK: HexBytes{5, 6}}, {EmulatorPassword: HexBytes{1}, EmulatorPACK: HexBytes{5, 6}, ConfirmEmulatorCredentials: true}} {
		if c.Type2PromotionError(o) == nil {
			t.Fatal("implicit source secrets accepted")
		}
	}
	draft, _ := c.ReadDraft()
	for _, mutate := range []func(*ReadCardData){
		func(d *ReadCardData) { d.Result.Complete = false },
		func(d *ReadCardData) { d.Error = "cleanup failed" },
		func(d *ReadCardData) { d.Rows[43] = "00000000" },
		func(d *ReadCardData) { d.Rows[4] = "????????" },
		func(d *ReadCardData) { d.Rows[0] = "001122BB" },
		func(d *ReadCardData) { d.Rows[41] = "40000400" },
	} {
		raw, _ := json.Marshal(draft)
		var d ReadCardData
		json.Unmarshal(raw, &d)
		mutate(&d)
		changed := c.withField("desk_read", d)
		if changed.Type2PromotionError(type2PromotionOptions()) == nil {
			t.Fatal("invalid/incomplete source accepted")
		}
	}
	// Legacy range drafts retain their offset data but have no verified layout.
	legacy, e := NewGUICardFromRead("legacy", ReadResult{Schema: 1, Kind: "type2", Start: 4, Requested: 1, Read: 1, Complete: true, UID: type2TestUID, Data: HexBytes{1, 2, 3, 4}}, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = legacy.Type2PromotionError(type2PromotionOptions()); e == nil || !strings.Contains(e.Error(), "metadata") {
		t.Fatal(e)
	}
}
