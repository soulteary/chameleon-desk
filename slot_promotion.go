package chameleon

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// SlotPromotionError checks a saved slot draft without device I/O or a new ID.
// A successful conversion describes emulator data, not a physical source card.
func (c GUICard) SlotPromotionError() error { _, err := c.slotPromotionCard(); return err }

func (c GUICard) slotPromotionCard() (GUICard, error) {
	if err := c.Validate(); err != nil {
		return GUICard{}, err
	}
	draft, err := c.ReadDraft()
	if err != nil {
		return GUICard{}, err
	}
	if draft == nil || draft.Result.Kind != "slot" || draft.Result.Slot == nil {
		return GUICard{}, fmt.Errorf("only slot snapshot drafts can use slot conversion")
	}
	s := draft.Result.Slot
	if !s.Complete || s.Read != s.Units || s.SourceChanged || !s.MetadataRead || draft.Error != "" {
		return GUICard{}, fmt.Errorf("slot conversion requires a complete snapshot with confirmed metadata and no read error or source change")
	}
	if _, exists := c.fields["desk_read_origin"]; exists {
		return GUICard{}, fmt.Errorf("reserved desk_read_origin metadata already exists; conversion will not overwrite it")
	}
	rows := make([][]byte, len(draft.Rows))
	for i, row := range draft.Rows {
		b, e := hex.DecodeString(row)
		if e != nil || len(b) != s.UnitBytes {
			return GUICard{}, fmt.Errorf("slot row %d must contain %d known bytes", i, s.UnitBytes)
		}
		rows[i] = b
	}
	out := c.withField("desk_read", nil)
	out.Tag = s.TagType
	if s.Sense == SenseHF {
		if s.Identity == nil {
			return GUICard{}, fmt.Errorf("slot conversion requires captured HF identity")
		}
		id := s.Identity
		if s.UnitBytes == 16 {
			for _, data := range [][][]byte{classicRows(s.Data), rows} {
				if err := validateClassicConversionRows(s.TagType, *id, data); err != nil {
					return GUICard{}, err
				}
			}
		} else {
			if err := validateType2Identity(*id); err != nil {
				return GUICard{}, err
			}
			for _, data := range [][]byte{s.Data, flattenType2Rows(rows)} {
				if err := validateType2UIDPages(data, id.UID); err != nil {
					return GUICard{}, err
				}
			}
			// These values were not captured by schema-1 slot snapshots. Do not
			// promote separately attached GUI extras into observed device facts.
			if raw := c.fields["extra"]; raw != nil {
				var extra map[string]json.RawMessage
				if err := json.Unmarshal(raw, &extra); err != nil || extra == nil {
					return GUICard{}, fmt.Errorf("GUI extra metadata must be an object")
				}
				for _, key := range []string{"ultralightVersion", "ultralightSignature", "ultralightCounters"} {
					if _, exists := extra[key]; exists {
						return GUICard{}, fmt.Errorf("%s was not captured by this slot snapshot; conversion will not claim it", key)
					}
				}
			}
		}
		out.UID, out.SAK = id.UID.String(), *id.SAK
		out.ATQA, out.ATS = append(HexBytes(nil), id.ATQA...), append(HexBytes{}, (*id.ATS)...)
		out.Data = rows
	} else {
		if s.Record == nil || len(rows) != 1 {
			return GUICard{}, fmt.Errorf("slot conversion requires a complete LF identity")
		}
		if _, err := DecodeLFRecord(s.Record.Protocol, s.TagType, rows[0]); err != nil {
			return GUICard{}, err
		}
		if s.Record.Protocol == "ioprox" && !bytes.Equal(rows[0], s.Data) {
			return GUICard{}, fmt.Errorf("edited ioProx identities require explicit firmware validation before conversion")
		}
		out.UID = HexBytes(rows[0]).String()
		out.SAK, out.ATQA, out.ATS, out.Data = 0, HexBytes{}, HexBytes{}, [][]byte{}
	}
	out = out.withField("desk_read_origin", map[string]any{
		"schema": 1, "source": "emulator_slot", "source_card_id": c.ID,
		"physical_write_allowed": false, "draft": json.RawMessage(c.fields["desk_read"]),
	})
	if err := out.Validate(); err != nil {
		return GUICard{}, err
	}
	if s.Sense == SenseHF {
		if _, err := out.EmulatorImage(); err != nil {
			return GUICard{}, err
		}
	}
	if _, err := PlanGUICardLoad(out, 1); err != nil {
		return GUICard{}, err
	}
	return out, nil
}

// PromoteSlotDraft creates a standard saved-card copy and retains the complete
// source draft in desk_read_origin. The caller adds it alongside the draft.
// No device command is sent and physical-card writing is not enabled.
func (c GUICard) PromoteSlotDraft() (GUICard, error) {
	out, err := c.slotPromotionCard()
	if err != nil {
		return GUICard{}, err
	}
	out.ID, err = NewGUIID()
	return out, err
}

// Physical card workflows accepting a saved card must retain the distinction
// between physical reads and emulator snapshots, including after JSON reload.
func (c GUICard) requirePhysicalCardSource() error {
	if err := c.RequireCardImage(); err != nil {
		return err
	}
	if raw, exists := c.fields["desk_read_origin"]; exists {
		var origin struct {
			Source string `json:"source"`
			Draft  struct {
				Result struct {
					Kind string          `json:"kind"`
					Slot json.RawMessage `json:"slot_snapshot"`
				} `json:"read_result"`
			} `json:"draft"`
		}
		if err := json.Unmarshal(raw, &origin); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("invalid read-source metadata for physical-card writing")
		}
		if origin.Source == "emulator_slot" || origin.Draft.Result.Kind == "slot" || origin.Draft.Result.Slot != nil {
			return fmt.Errorf("physical-card writing from slot-derived saved cards is not enabled")
		}
	}
	return nil
}
