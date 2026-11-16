package chameleon

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// LFPromotionError checks whether a saved LF draft can be converted without
// device access or allocating a new record ID.
func (c GUICard) LFPromotionError() error { _, err := c.lfPromotionCard(); return err }

func (c GUICard) lfPromotionCard() (GUICard, error) {
	if err := c.Validate(); err != nil {
		return GUICard{}, err
	}
	draft, err := c.ReadDraft()
	if err != nil {
		return GUICard{}, err
	}
	if draft == nil || draft.Result.Kind != "lf" {
		return GUICard{}, fmt.Errorf("only LF read drafts can use LF conversion")
	}
	r := draft.Result
	if !r.Complete || r.Read != 1 || r.Requested != 1 || r.Present == nil || !*r.Present || r.Record == nil || draft.Error != "" {
		return GUICard{}, fmt.Errorf("LF conversion requires a completed present-card read without an operation or cleanup error")
	}
	if _, exists := c.fields["desk_read_origin"]; exists {
		return GUICard{}, fmt.Errorf("reserved desk_read_origin metadata already exists; conversion will not overwrite it")
	}
	record := r.Record
	var tag uint16
	switch record.Protocol {
	case "em410x":
		if record.TagType < 100 || record.TagType > 104 {
			return GUICard{}, fmt.Errorf("EM410x conversion requires a captured subtype in 100..104")
		}
		tag = record.TagType
	case "hidprox":
		tag = 200
	case "ioprox":
		tag = 201
	case "viking":
		tag = 170
	case "pac":
		tag = 150
	case "jablotron":
		tag = 180
	case "pyramid", "em4x05":
		return GUICard{}, fmt.Errorf("%s reader results have no supported standard-card conversion; retain the diagnostic draft", record.Protocol)
	default:
		return GUICard{}, fmt.Errorf("unsupported LF read protocol %q", record.Protocol)
	}
	if record.Protocol != "em410x" && record.TagType != 0 && record.TagType != tag {
		return GUICard{}, fmt.Errorf("LF protocol and captured tag type disagree")
	}
	if len(draft.Rows) != 1 {
		return GUICard{}, fmt.Errorf("LF draft must contain one complete identity row")
	}
	id, err := hex.DecodeString(draft.Rows[0])
	if err != nil {
		return GUICard{}, fmt.Errorf("LF identity must contain known hexadecimal bytes: %w", err)
	}
	// Source and edited values are independently checked. Decoded fields on the
	// observation remain the original observation, never an edited identity.
	if _, err = DecodeLFRecord(record.Protocol, tag, record.ID); err != nil {
		return GUICard{}, err
	}
	if _, err = DecodeLFRecord(record.Protocol, tag, id); err != nil {
		return GUICard{}, err
	}
	// ioProx's host codec preserves the firmware's structured/raw response but
	// does not verify their relationship. Do not treat raw editor changes as a
	// firmware-validated encoding. A future explicit validation can relax this.
	if record.Protocol == "ioprox" && !bytes.Equal(id, record.ID) {
		return GUICard{}, fmt.Errorf("edited ioProx identities require explicit firmware validation before conversion")
	}
	out := c.withField("desk_read", nil)
	out = out.withField("desk_read_origin", map[string]any{"schema": 1, "source_card_id": c.ID, "draft": json.RawMessage(c.fields["desk_read"])})
	out.Tag, out.UID = tag, HexBytes(id).String()
	out.SAK, out.ATQA, out.ATS, out.Data = 0, HexBytes{}, HexBytes{}, [][]byte{}
	if err = out.Validate(); err != nil {
		return GUICard{}, err
	}
	if _, err = PlanGUICardLoad(out, 1); err != nil {
		return GUICard{}, err
	}
	return out, nil
}

// PromoteLFDraft creates a standard saved-card copy with a fresh ID. The caller
// adds the copy to its database and retains the original read draft. It performs
// no device command and does not write a physical transponder.
func (c GUICard) PromoteLFDraft() (GUICard, error) {
	out, err := c.lfPromotionCard()
	if err != nil {
		return GUICard{}, err
	}
	out.ID, err = NewGUIID()
	return out, err
}

// LFCardProtocol returns the existing standard-card protocol mapping. Reader
// diagnostics such as Pyramid and EM4x05 deliberately have no mapping here.
func LFCardProtocol(tag uint16) (string, bool) {
	protocol, ok := guiLFProtocols[tag]
	return protocol, ok
}

// EditLFData updates one complete encoded LF identity and a name in an ordinary
// saved card, preserving identity type and unrelated metadata. It is offline.
func (c GUICard) EditLFData(name string, rows []string) (GUICard, error) {
	if err := c.Validate(); err != nil {
		return c, err
	}
	if err := c.RequireCardImage(); err != nil {
		return c, err
	}
	protocol, ok := LFCardProtocol(c.Tag)
	if !ok {
		return c, fmt.Errorf("saved-card LF type %d has no data editor", c.Tag)
	}
	if len(rows) != 1 {
		return c, fmt.Errorf("LF editing requires one complete identity row")
	}
	id, err := hex.DecodeString(strings.Join(strings.Fields(rows[0]), ""))
	if err != nil {
		return c, fmt.Errorf("LF identity must contain hexadecimal bytes: %w", err)
	}
	if protocol == "ioprox" {
		old, e := guiUIDBytes(c.UID)
		if e != nil {
			return c, e
		}
		if !bytes.Equal(id, old) {
			return c, fmt.Errorf("edited ioProx identities require explicit firmware validation before saving")
		}
		if _, e = DecodeLFRecord(protocol, c.Tag, id); e != nil {
			return c, e
		}
	} else if _, err = EncodeLFIdentityOffline(LFIdentityInput{Protocol: protocol, TagType: c.Tag, ID: id}); err != nil {
		return c, err
	}
	c.Name, c.UID = name, HexBytes(id).String()
	return c, c.Validate()
}
