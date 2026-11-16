package chameleon

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func classicPromotionResult(tag uint16, uid HexBytes) ReadResult {
	count := guiClassicBlocks[tag]
	data := bytes.Repeat([]byte{0x22}, count*16)
	sak := map[uint16]byte{1000: 9, 1001: 8, 1002: 8, 1003: 0x18}[tag]
	atqa := HexBytes{0, 4}
	if tag == 1003 {
		atqa[1] = 2
	}
	if len(uid) == 7 {
		atqa[1] |= 0x40
	}
	copy(data, uid)
	offset := 6
	if len(uid) == 4 {
		data[4] = uid[0] ^ uid[1] ^ uid[2] ^ uid[3]
		data[5] = sak
	} else {
		data[7] = sak
		offset = 8
	}
	data[offset], data[offset+1] = atqa[1], atqa[0]
	for sector := 0; sector < 40; sector++ {
		first, n, _ := ClassicSectorGeometry(sector)
		trailer := first + n - 1
		if trailer >= count {
			break
		}
		copy(data[trailer*16+6:], []byte{0xff, 7, 0x80})
	}
	ats := HexBytes{}
	return ReadResult{Schema: 1, Kind: "classic", Complete: true, Requested: count, Read: count, UID: uid, Identity: &ClassicIdentity{Schema: 1, UID: uid, ATQA: atqa, SAK: &sak, ATS: &ats}, Dump: &ClassicCardDump{Schema: 2, UID: uid, TagType: tag, Data: data, SHA256: imageDigest(data)}}
}
func promotionDraft(t *testing.T, r ReadResult, readError string) GUICard {
	t.Helper()
	c, e := NewGUICardFromRead("Classic", r, readError)
	if e != nil {
		t.Fatal(e)
	}
	return c
}

func TestClassicPromotionPreservesIdentityEditedBytesAndSource(t *testing.T) {
	for _, tag := range []uint16{1000, 1001, 1002, 1003} {
		for _, uid := range []HexBytes{{1, 2, 3, 4}, {4, 1, 2, 3, 4, 5, 6}} {
			r := classicPromotionResult(tag, uid)
			c := promotionDraft(t, r, "").withField("custom", map[string]any{"keep": true}).withField("color", "#112233")
			draft, _ := c.ReadDraft()
			draft.Rows[1] = "AA" + draft.Rows[1][2:]
			var err error
			c, err = c.EditData("edited", draft.Rows)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(c)
			if err = c.ClassicPromotionError(); err != nil {
				t.Fatal(err)
			}
			out, err := c.PromoteClassicDraft()
			if err != nil {
				t.Fatal(err)
			}
			if out.ID == c.ID || out.Name != "edited" || out.Tag != tag || out.UID != uid.String() || out.SAK != *r.Identity.SAK || !bytes.Equal(out.ATQA, r.Identity.ATQA) || out.Data[1][0] != 0xAA || out.Color() != "#112233" {
				t.Fatal(out)
			}
			raw, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(raw, []byte(`"keep":true`)) || !bytes.Contains(raw, []byte(`"source_card_id":"`+c.ID+`"`)) || out.fields["desk_read"] != nil {
				t.Fatal(string(raw))
			}
			after, _ := json.Marshal(c)
			if !bytes.Equal(before, after) {
				t.Fatal("source draft mutated")
			}
			if _, err = out.EmulatorImage(); err != nil {
				t.Fatal(err)
			}
			if _, err = out.Dump(); err != nil {
				t.Fatal(err)
			}
			plan, err := PlanGUICardLoad(out, 1)
			if err != nil || plan.MemoryUnits != len(out.Data) {
				t.Fatal(plan, err)
			}
			back, err := ParseGUICard(raw)
			if err != nil || back.ClassicPromotionError() == nil {
				t.Fatal("standard card can be promoted again", err)
			}
		}
	}
}

func TestClassicPromotionRejectsMissingUnknownInterruptedAndConflictingData(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mutate    func(*ReadResult)
		readError string
	}{
		{"missing identity", func(r *ReadResult) { r.Identity = nil }, ""},
		{"missing ATS", func(r *ReadResult) { r.Identity.ATS = nil }, ""},
		{"missing SAK", func(r *ReadResult) { r.Identity.SAK = nil }, ""},
		{"missing ATQA", func(r *ReadResult) { r.Identity.ATQA = nil }, ""},
		{"other identity UID", func(r *ReadResult) { r.Identity.UID = HexBytes{4, 3, 2, 1} }, ""},
		{"interrupted", func(r *ReadResult) { r.Complete = false }, "cancelled"},
		{"cleanup", func(r *ReadResult) { r.Complete = false }, "mode restore failed"},
		{"error with complete flag", func(r *ReadResult) {}, "error"},
		{"unknown B", func(r *ReadResult) { r.Dump.UnknownKeys = []ClassicUnknownKey{{0, KeyB}} }, ""},
		{"SAK mismatch", func(r *ReadResult) { *r.Identity.SAK = 0x18 }, ""},
		{"block UID", func(r *ReadResult) { r.Dump.Data[0] ^= 1 }, ""},
		{"ACL", func(r *ReadResult) { r.Dump.Data[3*16+6] = 0 }, ""},
		{"short prefix", func(r *ReadResult) { r.Complete = false; r.Read = 1; r.Dump.Data = r.Dump.Data[:16] }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := classicPromotionResult(1000, HexBytes{1, 2, 3, 4})
			tc.mutate(&r)
			r.Dump.SHA256 = imageDigest(r.Dump.Data)
			c, err := NewGUICardFromRead("bad", r, tc.readError)
			if err == nil {
				if c.ClassicPromotionError() == nil {
					t.Fatal("preview allowed")
				}
				if _, err = c.PromoteClassicDraft(); err == nil {
					t.Fatal("conversion allowed")
				}
			}
		})
	}
	c := promotionDraft(t, classicPromotionResult(1000, HexBytes{1, 2, 3, 4}), "")
	draft, _ := c.ReadDraft()
	draft.Rows[0] = "00" + draft.Rows[0][2:]
	c, _ = c.EditData(c.Name, draft.Rows)
	if c.ClassicPromotionError() == nil {
		t.Fatal("edited block 0 mismatch allowed")
	}
	for _, r := range []ReadResult{testReadResult(), {Schema: 1, Kind: "lf", Complete: true, Requested: 1, Read: 1, Present: boolPtr(true), Record: &LFRecord{Protocol: "em410x", TagType: 100, ID: HexBytes{1, 2, 3, 4, 5}}}} {
		c := promotionDraft(t, r, "")
		if _, err := c.PromoteClassicDraft(); err == nil {
			t.Fatal("non-Classic promoted")
		}
	}
}

func TestClassicPromotionCapabilityIsOfflineAndParameterSpecific(t *testing.T) {
	c := promotionDraft(t, classicPromotionResult(1000, HexBytes{1, 2, 3, 4}), "")
	for _, ctx := range []FeatureContext{{}, {Device: &FeatureDevice{ModelID: featureModelID(1), Capabilities: []uint16{}, Transport: TransportInfo{NativeUSBMaintenance: true}}}} {
		state, err := EvaluateFeature("library.promote-classic", "web", ctx, &FeatureRequest{Card: &c})
		if err != nil || state.State != "available" || len(state.RequiredCommands) != 0 {
			t.Fatal(state, err)
		}
	}
	state, _ := EvaluateFeature("library.promote-classic", "web", FeatureContext{}, nil)
	if state.State != "unknown" {
		t.Fatal(state)
	}
	c = promotionDraft(t, testReadResult(), "")
	state, _ = EvaluateFeature("library.promote-classic", "web", FeatureContext{}, &FeatureRequest{Card: &c})
	if state.State != "unavailable" {
		t.Fatal(state)
	}
}

func TestClassicIdentityCapturedAndComparedWithinRead(t *testing.T) {
	for _, kind := range []string{"success", "sector identity changed", "uid changed", "ATQA changed", "SAK changed", "ATS changed", "cleanup failure", "partial"} {
		t.Run(kind, func(t *testing.T) {
			r := classicReadInput()
			expected := classicPromotionResult(1000, r.UID)
			scan := []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}
			steps := []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1001, status: 0x68, request: []byte{1}}}
			for sector := 0; sector < 5; sector++ {
				if kind == "sector identity changed" && sector == 1 {
					changed := append([]byte(nil), scan...)
					changed[5] ^= 1
					steps = append(steps, readerStep{command: 2000, data: changed})
					break
				}
				steps = append(steps, readerStep{command: 2000, data: scan})
				for block := sector * 4; block < sector*4+4; block++ {
					request := append([]byte{0x60, byte(block)}, r.Sectors[sector].A...)
					status := uint16(0)
					data := expected.Dump.Data[block*16 : (block+1)*16]
					if kind == "partial" && block == 1 {
						status = 2
						data = nil
					}
					steps = append(steps, readerStep{command: 2008, request: request, status: status, data: data})
					if status != 0 {
						break
					}
				}
				if kind == "partial" {
					break
				}
			}
			if kind != "partial" && kind != "sector identity changed" {
				last := append([]byte(nil), scan...)
				switch kind {
				case "uid changed":
					last[1] ^= 1
				case "ATQA changed":
					last[5] ^= 1
				case "SAK changed":
					last[7] ^= 1
				case "ATS changed":
					last[8] = 1
					last = append(last, 0x42)
				}
				steps = append(steps, readerStep{command: 2000, data: last})
			}
			cleanup := uint16(0x68)
			if kind == "cleanup failure" {
				cleanup = 0x67
			}
			steps = append(steps, readerStep{command: 1001, status: cleanup, request: []byte{0}})
			c := readerSimulator(t, steps)
			out, err := c.ReadClassicWithIdentity(context.Background(), r)
			if out.Identity == nil || out.Identity.ATQA.String() != "0004" || out.Identity.ATS == nil {
				t.Fatal(out)
			}
			if kind == "success" {
				if err != nil || !out.Complete || out.Read != 20 || len(out.Dump.UnknownKeys) != 0 {
					t.Fatal(out, err)
				}
				draft := promotionDraft(t, out, "")
				if err = draft.ClassicPromotionError(); err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil || out.Complete {
					t.Fatal(out, err)
				}
				if kind == "sector identity changed" && out.Read != 4 {
					t.Fatal(out.Read)
				}
				if kind == "partial" && out.Read != 1 {
					t.Fatal(out.Read)
				}
				if out.Dump.SHA256 != imageDigest(out.Dump.Data) {
					t.Fatal("prefix checksum missing")
				}
				draft := promotionDraft(t, out, err.Error())
				if draft.ClassicPromotionError() == nil {
					t.Fatal("failed read converted")
				}
			}
		})
	}
}

func TestClassicPromotionEditedTrailerAndLegacyDraftRemainRestricted(t *testing.T) {
	c := promotionDraft(t, classicPromotionResult(1000, HexBytes{1, 2, 3, 4}), "")
	draft, _ := c.ReadDraft()
	draft.Rows[3] = draft.Rows[3][:12] + "000000" + draft.Rows[3][18:]
	c, _ = c.EditData(c.Name, draft.Rows)
	if err := c.ClassicPromotionError(); err == nil || !strings.Contains(err.Error(), "access") {
		t.Fatal(err)
	}
	reserved := promotionDraft(t, classicPromotionResult(1000, HexBytes{1, 2, 3, 4}), "").withField("desk_read_origin", map[string]any{"keep": true})
	if err := reserved.ClassicPromotionError(); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatal(err)
	}
	ten := promotionDraft(t, classicPromotionResult(1000, HexBytes{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}), "")
	if err := ten.ClassicPromotionError(); err == nil || !strings.Contains(err.Error(), "four- and seven-byte") {
		t.Fatal(err)
	}
	r := classicPromotionResult(1000, HexBytes{1, 2, 3, 4})
	r.Identity = nil
	c = promotionDraft(t, r, "")
	if c.Validate() != nil || c.ClassicPromotionError() == nil {
		t.Fatal("legacy identity guessed")
	}
}

func TestClassicPromotionPreservesOpaqueManufacturerBytes(t *testing.T) {
	for _, uid := range []HexBytes{{1, 2, 3, 4}, {4, 1, 2, 3, 4, 5, 6}} {
		r := classicPromotionResult(1000, uid)
		for i := len(uid); i < 16; i++ {
			r.Dump.Data[i] = byte(0xA0 + i)
		}
		r.Dump.SHA256 = imageDigest(r.Dump.Data)
		c := promotionDraft(t, r, "")
		out, err := c.PromoteClassicDraft()
		if err != nil {
			t.Fatal(err)
		}
		if out.SAK != 9 || !bytes.Equal(out.ATQA, r.Identity.ATQA) || !bytes.Equal(out.Data[0], r.Dump.Data[:16]) {
			t.Fatal("manufacturer data was interpreted or patched", out)
		}
	}
}
