package chameleon

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// ClassicPromotionError evaluates a saved draft without I/O or generating IDs.
// The same checks are used by feature previews and the conversion itself.
func (c GUICard) ClassicPromotionError() error { _, err := c.classicPromotionCard(); return err }

func (c GUICard) classicPromotionCard() (GUICard, error) {
	if err := c.Validate(); err != nil {
		return GUICard{}, err
	}
	draft, err := c.ReadDraft()
	if err != nil {
		return GUICard{}, err
	}
	if draft == nil || draft.Result.Kind != "classic" {
		return GUICard{}, fmt.Errorf("only Classic read drafts can use Classic conversion")
	}
	if _, exists := c.fields["desk_read_origin"]; exists {
		return GUICard{}, fmt.Errorf("reserved desk_read_origin metadata already exists; conversion will not overwrite it")
	}
	r, effectiveError, err := c.ClassicRepairEffectiveRead(*draft)
	if err != nil {
		return GUICard{}, err
	}
	if !r.Complete || r.Read != r.Requested || effectiveError != "" {
		return GUICard{}, fmt.Errorf("Classic conversion requires a complete read without an operation or cleanup error")
	}
	if r.Identity == nil {
		return GUICard{}, fmt.Errorf("Classic identity is missing; read the card again with identity capture")
	}
	if err := r.Identity.Validate(); err != nil {
		return GUICard{}, err
	}
	id := r.Identity
	if len(id.UID) != 4 && len(id.UID) != 7 {
		return GUICard{}, fmt.Errorf("Classic conversion supports four- and seven-byte UID block-0 layouts only")
	}
	expected := map[uint16]byte{1000: 9, 1001: 8, 1002: 8, 1003: 0x18}[r.Dump.TagType]
	if *id.SAK&0x7f != expected {
		return GUICard{}, fmt.Errorf("observed SAK is inconsistent with the requested Classic geometry")
	}
	if len(r.Dump.UnknownKeys) != 0 {
		return GUICard{}, fmt.Errorf("Classic conversion requires all sector keys to be known")
	}
	if err := r.Dump.Validate(); err != nil {
		return GUICard{}, err
	}
	rows := make([][]byte, len(draft.Rows))
	for i, row := range draft.Rows {
		b, e := hex.DecodeString(row)
		if e != nil || len(b) != 16 {
			return GUICard{}, fmt.Errorf("Classic row %d must contain sixteen known bytes", i)
		}
		rows[i] = b
	}
	if len(rows) != guiClassicBlocks[r.Dump.TagType] {
		return GUICard{}, fmt.Errorf("Classic memory is incomplete")
	}
	for _, data := range [][][]byte{classicRows(r.Dump.Data), rows} {
		if err := validateClassicBlockIdentity(data[0], *id); err != nil {
			return GUICard{}, err
		}
		for sector := 0; sector < 40; sector++ {
			first, count, _ := ClassicSectorGeometry(sector)
			trailer := first + count - 1
			if trailer >= len(data) {
				break
			}
			if err := ValidateClassicAccess(data[trailer]); err != nil {
				return GUICard{}, fmt.Errorf("sector %d trailer: %w", sector, err)
			}
		}
	}
	out := c.withField("desk_read", nil)
	out = out.withField("desk_read_origin", map[string]any{"schema": 1, "source_card_id": c.ID, "draft": json.RawMessage(c.fields["desk_read"])})
	out.Tag = r.Dump.TagType
	out.UID = id.UID.String()
	out.SAK = *id.SAK
	out.ATQA = append(HexBytes(nil), id.ATQA...)
	out.ATS = append(HexBytes{}, (*id.ATS)...)
	out.Data = rows
	if err := out.Validate(); err != nil {
		return GUICard{}, err
	}
	if _, err := out.EmulatorImage(); err != nil {
		return GUICard{}, err
	}
	return out, nil
}
func classicRows(data []byte) [][]byte {
	rows := make([][]byte, len(data)/16)
	for i := range rows {
		rows[i] = data[i*16 : (i+1)*16]
	}
	return rows
}

func validateClassicBlockIdentity(block []byte, id ClassicIdentity) error {
	if !bytes.Equal(block[:len(id.UID)], id.UID) {
		return fmt.Errorf("block 0 UID differs from the observed identity")
	}
	// Manufacturer bytes are opaque. Do not infer RF SAK/ATQA or a universal
	// checksum layout from them; use the identity captured in the Reader operation.
	return nil
}

// PromoteClassicDraft creates a standard saved-card copy with a fresh ID. The
// caller must AddCard it, preserving the original draft. It performs no writes
// to a device or physical card and never fills unknown bytes or identity fields.
func (c GUICard) PromoteClassicDraft() (GUICard, error) {
	out, err := c.classicPromotionCard()
	if err != nil {
		return GUICard{}, err
	}
	out.ID, err = NewGUIID()
	return out, err
}
