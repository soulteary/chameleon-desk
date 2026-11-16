package chameleon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func slotPromotionResult(t *testing.T, tag uint16) ReadResult {
	t.Helper()
	r := slotSnapshotRequest(tag)
	n, width, err := r.geometry()
	if err != nil {
		t.Fatal(err)
	}
	s := &SlotSnapshot{Schema: 1, Source: "emulator_slot", MetadataRead: true, Slot: 3, Sense: r.Sense, TagType: tag,
		Enabled: true, Units: n, Read: n, UnitBytes: width, Complete: true, Unknown: slotUnknown(n, n), Uncaptured: slotUncaptured()}
	if r.Sense == SenseHF && width == 16 {
		c := classicPromotionResult(tag, HexBytes{1, 2, 3, 4})
		s.Identity, s.Data = c.Identity, c.Dump.Data
	} else if r.Sense == SenseHF {
		uid, sak, ats := HexBytes{4, 1, 2, 3, 4, 5, 6}, byte(0), HexBytes{}
		s.Identity = &ClassicIdentity{Schema: 1, UID: uid, ATQA: HexBytes{0, 0x44}, SAK: &sak, ATS: &ats}
		s.Data = bytes.Repeat([]byte{0x22}, n*width)
		copy(s.Data, uid[:3])
		s.Data[3] = 0x88 ^ uid[0] ^ uid[1] ^ uid[2]
		copy(s.Data[4:], uid[3:])
		s.Data[8] = uid[3] ^ uid[4] ^ uid[5] ^ uid[6]
	} else {
		id := bytes.Repeat([]byte{1}, width)
		switch tag {
		case 150:
			id = []byte("12345678")
		case 180:
			id = []byte{0x12, 0x34, 0x56, 0x78, 0x90}
		case 200:
			id, err = (HIDProxID{Format: 1, Facility: 12, Number: 345}).Encode()
		case 201:
			id = []byte{1, 83, 0x12, 0x34, 0, 0x78, 0x54, 0xe0, 0x3a, 0x5d, 0x65, 0xab, 0, 0, 0, 0}
		case 310:
			id, err = ComposeIDTECK(0x123456)
		}
		if err != nil {
			t.Fatal(err)
		}
		record, e := DecodeLFRecord(guiLFProtocols[tag], tag, id)
		if e != nil {
			t.Fatal(e)
		}
		s.Data, s.Record = id, &record
	}
	s.SHA256 = s.digest()
	out := ReadResult{Schema: 1, Kind: "slot", Complete: true, Requested: n, Read: n, Slot: s}
	if s.Identity != nil {
		out.UID = s.Identity.UID
	}
	return out
}

func TestSlotPromotionAllLayoutsRetainSourceAndPersistSeparately(t *testing.T) {
	for _, tag := range []uint16{1000, 1001, 1002, 1003, 1100, 1101, 1102, 1103, 1104, 1105, 1106, 1107, 1108, 100, 101, 102, 103, 104, 150, 170, 180, 200, 201, 310} {
		t.Run(fmt.Sprint(tag), func(t *testing.T) {
			r := slotPromotionResult(t, tag)
			c, err := NewGUICardFromRead("slot", r, "")
			if err != nil {
				t.Fatal(err)
			}
			c = c.withField("custom", map[string]any{"keep": true}).withField("extra", map[string]any{"vendor": "keep"}).withField("color", "#123456")
			if r.Slot.Sense == SenseHF {
				draft, _ := c.ReadDraft()
				draft.Rows[4] = "AA" + draft.Rows[4][2:]
				c, err = c.EditData("edited slot", draft.Rows)
				if err != nil {
					t.Fatal(err)
				}
			}
			before, _ := json.Marshal(c)
			state, err := EvaluateFeature("library.promote-slot", "web", FeatureContext{}, &FeatureRequest{Card: &c})
			if err != nil || state.State != "available" || len(state.RequiredCommands) != 0 {
				t.Fatal(state, err)
			}
			out, err := c.PromoteSlotDraft()
			if err != nil {
				t.Fatal(err)
			}
			if out.ID == c.ID || out.Tag != tag || out.Name != c.Name || out.Color() != "#123456" || out.fields["desk_read"] != nil {
				t.Fatal(out)
			}
			var origin struct {
				Source   string          `json:"source"`
				CardID   string          `json:"source_card_id"`
				Physical bool            `json:"physical_write_allowed"`
				Draft    json.RawMessage `json:"draft"`
			}
			if err := json.Unmarshal(out.fields["desk_read_origin"], &origin); err != nil || origin.Source != "emulator_slot" || origin.CardID != c.ID || origin.Physical || !bytes.Equal(origin.Draft, c.fields["desk_read"]) {
				t.Fatal(origin, err)
			}
			if r.Slot.Sense == SenseHF {
				image, e := out.EmulatorImage()
				if e != nil || len(image.Data) != len(r.Slot.Data) || out.Data[4][0] != 0xaa {
					t.Fatal(image, e)
				}
				if out.UID != r.UID.String() || out.SAK != *r.Slot.Identity.SAK || !bytes.Equal(out.ATQA, r.Slot.Identity.ATQA) || !bytes.Equal(out.ATS, *r.Slot.Identity.ATS) {
					t.Fatal(out)
				}
				if r.Slot.UnitBytes == 4 {
					if !bytes.Equal(out.Data[len(out.Data)-1], r.Slot.Data[len(r.Slot.Data)-4:]) {
						t.Fatal("emulator credentials were changed")
					}
					if bytes.Contains(out.fields["extra"], []byte("ultralight")) {
						t.Fatal("uncaptured Type2 extras invented")
					}
				}
			} else if out.UID != r.Slot.Data.String() || len(out.Data) != 0 {
				t.Fatal(out)
			}
			if _, err := PlanGUICardLoad(out, 1); err != nil {
				t.Fatal(err)
			}
			db, _ := ParseGUIDatabase([]byte(`{"flutter.cards":[],"custom":42}`))
			db, err = db.AddCard(c)
			if err != nil {
				t.Fatal(err)
			}
			db, err = db.AddCard(out)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(db)
			db, err = ParseGUIDatabase(raw)
			if err != nil {
				t.Fatal(err)
			}
			reloaded, err := db.Card(out.ID)
			if err != nil {
				t.Fatal(err)
			}
			if reloaded.requirePhysicalCardSource() == nil {
				t.Fatal("slot source became eligible for physical writing")
			}
			if err := reloaded.SlotPromotionError(); err == nil {
				t.Fatal("standard card promoted again")
			}
			original, _ := db.Card(c.ID)
			after, _ := json.Marshal(original)
			if !bytes.Equal(before, after) {
				t.Fatal("original draft changed")
			}
		})
	}
}

func TestSlotPromotionRejectsPartialInvalidAndConflictingSource(t *testing.T) {
	tests := []struct {
		name string
		tag  uint16
		edit func(*ReadResult)
	}{
		{"partial", 1001, func(r *ReadResult) {
			r.Read = 32
			r.Complete = false
			r.Slot.Read = 32
			r.Slot.Complete = false
			r.Slot.Data = r.Slot.Data[:512]
			r.Slot.Unknown = slotUnknown(32, 64)
		}},
		{"completion flag", 1001, func(r *ReadResult) { r.Complete = false; r.Slot.Complete = false }},
		{"missing HF identity", 1001, func(r *ReadResult) { r.Slot.Identity = nil; r.UID = nil }},
		{"missing ATS", 1001, func(r *ReadResult) { r.Slot.Identity.ATS = nil }},
		{"missing SAK", 1001, func(r *ReadResult) { r.Slot.Identity.SAK = nil }},
		{"unknown type", 1001, func(r *ReadResult) { r.Slot.TagType = 3000 }},
		{"Classic identity mismatch", 1001, func(r *ReadResult) { r.Slot.Data[0] ^= 1 }},
		{"Classic SAK", 1001, func(r *ReadResult) { *r.Slot.Identity.SAK = 0x18 }},
		{"Classic access", 1001, func(r *ReadResult) { r.Slot.Data[3*16+6] ^= 1 }},
		{"Type2 UID", 1100, func(r *ReadResult) { r.Slot.Data[0] ^= 1 }},
		{"Type2 BCC", 1100, func(r *ReadResult) { r.Slot.Data[3] ^= 1 }},
		{"Type2 identity", 1100, func(r *ReadResult) { *r.Slot.Identity.SAK = 8 }},
		{"Type2 geometry", 1100, func(r *ReadResult) { r.Slot.TagType = 1101 }},
		{"LF protocol", 150, func(r *ReadResult) { r.Slot.Record.Protocol = "viking" }},
		{"LF record", 150, func(r *ReadResult) { r.Slot.Record.PAC = "other" }},
		{"source changed", 1001, func(r *ReadResult) { r.Complete = false; r.Slot.Complete = false; r.Slot.SourceChanged = true }},
		{"unknown range", 1001, func(r *ReadResult) { r.Slot.Unknown = []SlotUnknownRange{{Start: 0, Count: 1, Reason: "unknown"}} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := slotPromotionResult(t, tc.tag)
			tc.edit(&r)
			r.Slot.SHA256 = r.Slot.digest()
			c, err := NewGUICardFromRead("invalid", r, "")
			if err == nil {
				if c.SlotPromotionError() == nil {
					t.Fatal("invalid source accepted")
				}
				if _, err = c.PromoteSlotDraft(); err == nil {
					t.Fatal("invalid source promoted")
				}
				state, e := EvaluateFeature("library.promote-slot", "web", FeatureContext{}, &FeatureRequest{Card: &c})
				if e != nil || state.State != "unavailable" || len(state.Reasons) == 0 {
					t.Fatal(state, e)
				}
			}
		})
	}
	r := slotPromotionResult(t, 1001)
	r.Slot.Data[0] ^= 1 // Do not recompute the checksum.
	if _, err := NewGUICardFromRead("corrupt", r, ""); err == nil {
		t.Fatal("corrupt checksum accepted")
	}
}

func TestSlotPromotionValidatesEditsAndPreservesUncapturedScope(t *testing.T) {
	for _, tag := range []uint16{1001, 1100, 150, 201} {
		c, err := NewGUICardFromRead("source", slotPromotionResult(t, tag), "")
		if err != nil {
			t.Fatal(err)
		}
		draft, _ := c.ReadDraft()
		if tag == 150 {
			draft.Rows[0] = "0000000000000000"
		} else {
			draft.Rows[0] = "FF" + draft.Rows[0][2:]
		}
		c, err = c.EditData("invalid edit", draft.Rows)
		if err != nil {
			t.Fatal(err)
		}
		if c.SlotPromotionError() == nil {
			t.Fatal("invalid edit promoted", tag)
		}
	}
	c, _ := NewGUICardFromRead("source", slotPromotionResult(t, 1001), "read failed")
	if c.SlotPromotionError() == nil {
		t.Fatal("read error ignored")
	}
	c, _ = NewGUICardFromRead("source", slotPromotionResult(t, 1001), "")
	c = c.withField("desk_read_origin", map[string]any{"keep": true})
	if c.SlotPromotionError() == nil {
		t.Fatal("reserved provenance overwritten")
	}
	for _, key := range []string{"ultralightVersion", "ultralightSignature", "ultralightCounters"} {
		c, _ = NewGUICardFromRead("source", slotPromotionResult(t, 1100), "")
		c = c.withField("extra", map[string]any{key: []int{0}})
		if err := c.SlotPromotionError(); err == nil || !strings.Contains(err.Error(), "not captured") {
			t.Fatal(err)
		}
	}
}

func TestSlotDerivedLFIsRejectedByPhysicalWritePlanAfterEditAndReload(t *testing.T) {
	c, _ := NewGUICardFromRead("source", slotPromotionResult(t, 150), "")
	c, err := c.PromoteSlotDraft()
	if err != nil {
		t.Fatal(err)
	}
	c, err = c.EditLFData("edited", []string{"4142434431323334"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(c)
	c, err = ParseGUICard(raw)
	if err != nil {
		t.Fatal(err)
	}
	r := LFPhysicalWriteRequest{Schema: 1, Source: c, Protocol: "pac", NewPassword: HexBytes{0, 0, 0, 0}, OldPasswords: []HexBytes{{0, 0, 0, 0}}, TargetIsolated: true}
	if _, err = r.Plan(); err == nil || !strings.Contains(err.Error(), "slot-derived") {
		t.Fatal(err)
	}
	state, err := EvaluateFeature("lf.clone", "web", FeatureContext{}, &FeatureRequest{LFWrite: &r})
	if err != nil || state.State != "unavailable" {
		t.Fatal(state, err)
	}
	// Omitting or changing a convenience flag must not hide the retained source.
	var origin map[string]json.RawMessage
	json.Unmarshal(c.fields["desk_read_origin"], &origin)
	delete(origin, "source")
	origin["physical_write_allowed"] = json.RawMessage(`true`)
	r.Source = c.withField("desk_read_origin", origin)
	if _, err = r.Plan(); err == nil {
		t.Fatal("origin draft ignored")
	}
}
