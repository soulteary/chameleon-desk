package chameleon

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func lfPromotionDraft(t *testing.T, protocol string, tag uint16, id HexBytes) GUICard {
	t.Helper()
	record, err := DecodeLFRecord(protocol, tag, id)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewGUICardFromRead("LF source", ReadResult{Schema: 1, Kind: "lf", Complete: true, Requested: 1, Read: 1, Present: boolPtr(true), Record: &record}, "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestLFPromotionSupportedMappingsAndMetadata(t *testing.T) {
	hid, _ := (HIDProxID{Format: 1, Facility: 12, Number: 345}).Encode()
	io := HexBytes{1, 83, 0x12, 0x34, 0, 0x78, 0x54, 0xe0, 0x3a, 0x5d, 0x65, 0xab, 0, 0, 0, 0}
	for _, tc := range []struct {
		protocol            string
		inputTag, outputTag uint16
		id                  HexBytes
	}{
		{"em410x", 100, 100, HexBytes{1, 2, 3, 4, 5}}, {"em410x", 101, 101, HexBytes{1, 2, 3, 4, 5}}, {"em410x", 102, 102, HexBytes{1, 2, 3, 4, 5}}, {"em410x", 103, 103, HexBytes{1, 2, 3, 4, 5}}, {"em410x", 104, 104, bytes.Repeat([]byte{0xA5}, 13)},
		{"hidprox", 0, 200, hid}, {"ioprox", 0, 201, io}, {"viking", 0, 170, HexBytes{1, 2, 3, 4}}, {"pac", 0, 150, HexBytes("12345678")}, {"jablotron", 0, 180, HexBytes{0x12, 0x34, 0x56, 0x78, 0x90}},
	} {
		t.Run(tc.protocol+TagName(tc.outputTag), func(t *testing.T) {
			c := lfPromotionDraft(t, tc.protocol, tc.inputTag, tc.id).withField("custom", map[string]any{"keep": true}).withField("color", "#123456")
			source, _ := json.Marshal(c)
			if err := c.LFPromotionError(); err != nil {
				t.Fatal(err)
			}
			out, err := c.PromoteLFDraft()
			if err != nil {
				t.Fatal(err)
			}
			if out.ID == c.ID || out.Name != c.Name || out.Tag != tc.outputTag || out.UID != tc.id.String() || out.Color() != "#123456" || len(out.Data) != 0 {
				t.Fatal(out)
			}
			if _, err = PlanGUICardLoad(out, 1); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(out)
			if out.fields["desk_read"] != nil || !bytes.Contains(raw, []byte(`"keep":true`)) || !bytes.Contains(raw, []byte(`"source_card_id":"`+c.ID+`"`)) {
				t.Fatal(string(raw))
			}
			back, err := ParseGUICard(raw)
			if err != nil || back.LFPromotionError() == nil {
				t.Fatal("standard record can be promoted again", err)
			}
			after, _ := json.Marshal(c)
			if !bytes.Equal(source, after) {
				t.Fatal("source changed")
			}
		})
	}
}
func TestLFPromotionUsesValidatedEditedIdentity(t *testing.T) {
	c := lfPromotionDraft(t, "pac", 0, HexBytes("12345678"))
	c, err := c.EditData("edited", []string{"4142434431323334"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.PromoteLFDraft()
	if err != nil || out.UID != "4142434431323334" {
		t.Fatal(out, err)
	}
	draft, _ := c.ReadDraft()
	if string(draft.Result.Record.ID) != "12345678" {
		t.Fatal("original identity replaced")
	}
}
func TestLFPromotionRejectsDiagnosticAmbiguousAndUnvalidatedInputs(t *testing.T) {
	diagnostic := lfPromotionDraft(t, "em4x05", 0, make(HexBytes, 14))
	if err := diagnostic.LFPromotionError(); err == nil || !strings.Contains(err.Error(), "diagnostic") {
		t.Fatal(err)
	}
	ambiguous := lfPromotionDraft(t, "em410x", 0, HexBytes{1, 2, 3, 4, 5})
	if _, err := ambiguous.PromoteLFDraft(); err == nil {
		t.Fatal("missing subtype inferred")
	}
	wrongType := lfPromotionDraft(t, "pac", 170, HexBytes("12345678"))
	if _, err := wrongType.PromoteLFDraft(); err == nil {
		t.Fatal("tag mismatch accepted")
	}
	c := lfPromotionDraft(t, "pac", 0, HexBytes("12345678"))
	draft, _ := c.ReadDraft()
	draft.Error = "mode restore failed"
	c = c.withField("desk_read", draft)
	if c.LFPromotionError() == nil {
		t.Fatal("cleanup error accepted")
	}
	c = lfPromotionDraft(t, "pac", 0, HexBytes("12345678")).withField("desk_read_origin", map[string]any{"keep": true})
	if c.LFPromotionError() == nil {
		t.Fatal("provenance overwritten")
	}
	c = lfPromotionDraft(t, "ioprox", 0, HexBytes{1, 83, 0x12, 0x34, 0, 0x78, 0x54, 0xe0, 0x3a, 0x5d, 0x65, 0xab, 0, 0, 0, 0})
	draft, _ = c.ReadDraft()
	draft.Rows[0] = "02" + draft.Rows[0][2:]
	c, err := c.EditData(c.Name, draft.Rows)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.LFPromotionError(); err == nil || !strings.Contains(err.Error(), "firmware validation") {
		t.Fatal(err)
	}
	classic := promotionDraft(t, classicPromotionResult(1000, HexBytes{1, 2, 3, 4}), "")
	if classic.LFPromotionError() == nil {
		t.Fatal("Classic accepted by LF converter")
	}
}

func TestStandardLFEditingPreservesTypeAndMetadata(t *testing.T) {
	source := lfPromotionDraftForEdit(t)
	out, err := source.EditLFData("new", []string{"41 42 43 44 31 32 33 34"})
	if err != nil || out.UID != "4142434431323334" || out.Name != "new" || out.Tag != 150 || out.Color() != "#123456" || out.fields["desk_read_origin"] == nil {
		t.Fatal(out, err)
	}
	if source.UID != "3132333435363738" {
		t.Fatal("source mutated")
	}
	if _, err = source.EditLFData("bad", []string{"0000000000000000"}); err == nil {
		t.Fatal("non-printable PAC accepted")
	}
	if _, err = source.EditLFData("bad", []string{}); err == nil {
		t.Fatal("missing row accepted")
	}
	draft := lfPromotionDraftForEdit(t)
	draft.Tag = 0
	if _, err = draft.EditLFData("bad", []string{"11"}); err == nil {
		t.Fatal("unknown tag accepted")
	}
}
func lfPromotionDraftForEdit(t *testing.T) GUICard {
	t.Helper()
	source := lfPromotionDraft(t, "pac", 0, HexBytes("12345678")).withField("color", "#123456")
	out, err := source.PromoteLFDraft()
	if err != nil {
		t.Fatal(err)
	}
	return out
}
