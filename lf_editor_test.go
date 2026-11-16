package chameleon

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func lfEditorCard(t *testing.T, tag uint16, id HexBytes) GUICard {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"id": "saved", "name": "before", "tag": tag, "uid": id.String(), "data": [][]byte{}, "atqa": []int{}, "ats": []int{}, "sak": 0, "custom": map[string]any{"keep": true}})
	c, err := ParseGUICard(raw)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestLFStructuredEditorPreservesTypeAndMetadataForHostCodecs(t *testing.T) {
	hid, _ := (HIDProxID{Format: 1, Facility: 12, Number: 34}).Encode()
	number := uint32(1234)
	idteck, _ := ComposeIDTECK(number)
	for _, tc := range []struct {
		tag   uint16
		id    HexBytes
		input LFIdentityInput
	}{
		{100, HexBytes{1, 2, 3, 4, 5}, LFIdentityInput{Protocol: "em410x", TagType: 100, ID: HexBytes{5, 4, 3, 2, 1}}},
		{101, HexBytes{1, 2, 3, 4, 5}, LFIdentityInput{Protocol: "em410x", TagType: 101, ID: HexBytes{5, 4, 3, 2, 1}}},
		{102, HexBytes{1, 2, 3, 4, 5}, LFIdentityInput{Protocol: "em410x", TagType: 102, ID: HexBytes{5, 4, 3, 2, 1}}},
		{103, HexBytes{1, 2, 3, 4, 5}, LFIdentityInput{Protocol: "em410x", TagType: 103, ID: HexBytes{5, 4, 3, 2, 1}}},
		{104, bytes.Repeat([]byte{1}, 13), LFIdentityInput{Protocol: "em410x", TagType: 104, ID: bytes.Repeat([]byte{2}, 13)}},
		{150, HexBytes("12345678"), LFIdentityInput{Protocol: "pac", TagType: 150, PAC: "ABCDEFGH"}},
		{170, HexBytes{1, 2, 3, 4}, LFIdentityInput{Protocol: "viking", TagType: 170, ID: HexBytes{4, 3, 2, 1}}},
		{180, HexBytes{0x12, 0x34, 0x56, 0x78, 0x90}, LFIdentityInput{Protocol: "jablotron", TagType: 180, ID: HexBytes{0x98, 0x76, 0x54, 0x32, 0x10}}},
		{200, hid, LFIdentityInput{Protocol: "hidprox", TagType: 200, HID: &HIDProxID{Format: 1, Facility: 56, Number: 78}}},
		{310, idteck, LFIdentityInput{Protocol: "idteck", TagType: 310, IDTECKNumber: &number}},
	} {
		c := lfEditorCard(t, tc.tag, tc.id)
		before, _ := json.Marshal(c)
		out, err := c.EditLFIdentityOffline("edited", tc.input)
		if err != nil || out.Tag != tc.tag || out.ID != c.ID || out.Name != "edited" {
			t.Fatal(tc.tag, out, err)
		}
		if _, err = PlanGUICardLoad(out, 1); err != nil {
			t.Fatal(tc.tag, err)
		}
		raw, _ := json.Marshal(out)
		if !bytes.Contains(raw, []byte(`"keep":true`)) {
			t.Fatal(string(raw))
		}
		after, _ := json.Marshal(c)
		if !bytes.Equal(before, after) {
			t.Fatal("source mutated")
		}
	}
}
func TestLFStructuredEditorRequiresFirmwareForIOProxIncludingIDOnly(t *testing.T) {
	encoded := HexBytes{1, 83, 0x12, 0x34, 0, 0x78, 0x54, 0xe0, 0x3a, 0x5d, 0x65, 0xab, 0, 0, 0, 0}
	c := lfEditorCard(t, 201, encoded)
	if _, err := c.EditLFIdentityOffline("edited", LFIdentityInput{Protocol: "ioprox", TagType: 201, ID: encoded}); err == nil {
		t.Fatal("offline ioProx accepted")
	}
	for _, mismatch := range []bool{false, true} {
		response := append(HexBytes(nil), encoded...)
		if mismatch {
			response[1]++
		}
		client := readerSimulator(t, []readerStep{{command: 3012, request: encoded[4:12], status: 0x68, data: response}})
		out, err := client.EditLFIdentity(context.Background(), c, "edited", LFIdentityInput{Protocol: "ioprox", TagType: 201, ID: encoded})
		if mismatch {
			if err == nil {
				t.Fatal("inconsistent firmware/raw identity accepted")
			}
		} else if err != nil || out.UID != encoded.String() || out.Name != "edited" {
			t.Fatal(out, err)
		}
	}
	// Structured fields use one firmware composition, never a host fallback.
	client := readerSimulator(t, []readerStep{{command: 3013, request: []byte{1, 83, 0x12, 0x34}, status: 0x68, data: encoded}})
	if _, err := client.EditLFIdentity(context.Background(), c, "structured", LFIdentityInput{Protocol: "ioprox", TagType: 201, IOProx: &IOProxID{Version: 1, Facility: 83, Number: 0x1234}}); err != nil {
		t.Fatal(err)
	}
}
func TestLFStructuredEditorRejectsProtocolChangesAndDraftsBeforeIO(t *testing.T) {
	c := lfEditorCard(t, 150, HexBytes("12345678"))
	client := readerSimulator(t, nil)
	for _, input := range []LFIdentityInput{{Protocol: "viking", TagType: 170, ID: HexBytes{1, 2, 3, 4}}, {Protocol: "pac", TagType: 170, PAC: "ABCDEFGH"}, {Protocol: "pac", TagType: 150, PAC: "短"}} {
		if _, err := client.EditLFIdentity(context.Background(), c, "bad", input); err == nil {
			t.Fatal(input)
		}
	}
	draft := lfPromotionDraft(t, "pac", 0, HexBytes("12345678"))
	if _, err := draft.EditLFIdentityOffline("bad", LFIdentityInput{Protocol: "pac", TagType: 150, PAC: "ABCDEFGH"}); err == nil {
		t.Fatal("read draft edited as ordinary card")
	}
}

func TestLFStructuredEditorRequirementsSelectOnlyUsedCodec(t *testing.T) {
	encoded := HexBytes{1, 83, 0x12, 0x34, 0, 0x78, 0x54, 0xe0, 0x3a, 0x5d, 0x65, 0xab, 0, 0, 0, 0}
	for _, tc := range []struct {
		input   LFIdentityInput
		command Command
	}{
		{LFIdentityInput{Protocol: "pac", TagType: 150, PAC: "ABCDEFGH"}, 0},
		{LFIdentityInput{Protocol: "ioprox", TagType: 201, ID: encoded}, 3012},
		{LFIdentityInput{Protocol: "ioprox", TagType: 201, IOProx: &IOProxID{Raw: encoded[4:12]}}, 3012},
		{LFIdentityInput{Protocol: "ioprox", TagType: 201, IOProx: &IOProxID{Version: 1, Facility: 83, Number: 0x1234}}, 3013},
	} {
		commands, err := tc.input.EditRequirements()
		if err != nil {
			t.Fatal(err)
		}
		if tc.command == 0 {
			if len(commands) != 0 {
				t.Fatal(commands)
			}
		} else if len(commands) != 1 || commands[0] != tc.command {
			t.Fatal(commands)
		}
	}
	for _, input := range []LFIdentityInput{{Protocol: "pac", TagType: 170, PAC: "ABCDEFGH"}, {Protocol: "pac", TagType: 150, PAC: "短"}, {Protocol: "ioprox", TagType: 201}, {Protocol: "ioprox", TagType: 201, ID: HexBytes{1}}, {Protocol: "ioprox", TagType: 201, IOProx: &IOProxID{Raw: HexBytes{1}}}} {
		if _, err := input.EditRequirements(); err == nil {
			t.Fatal("malformed codec request accepted", input)
		}
	}
}
