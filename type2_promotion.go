package chameleon

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Type2PromotionOptions deliberately assigns emulator-only credentials. These
// values are not claimed to have been read from the source card. They appear in
// the resulting standard card memory and exports only after explicit consent.
type Type2PromotionOptions struct {
	EmulatorPassword           HexBytes `json:"emulator_password"`
	EmulatorPACK               HexBytes `json:"emulator_pack"`
	ConfirmEmulatorCredentials bool     `json:"confirm_emulator_credentials"`
}

func (o Type2PromotionOptions) Validate() error {
	if !o.ConfirmEmulatorCredentials || len(o.EmulatorPassword) != 4 || len(o.EmulatorPACK) != 2 {
		return fmt.Errorf("Type2 conversion requires explicit four-byte emulator password, two-byte emulator PACK and credential-assignment confirmation; source credentials are unknown")
	}
	return nil
}
func (c GUICard) Type2PromotionError(options Type2PromotionOptions) error {
	_, err := c.type2PromotionCard(options)
	return err
}
func (c GUICard) type2PromotionCard(options Type2PromotionOptions) (GUICard, error) {
	if err := c.Validate(); err != nil {
		return GUICard{}, err
	}
	draft, err := c.ReadDraft()
	if err != nil {
		return GUICard{}, err
	}
	if draft == nil || draft.Result.Kind != "type2" {
		return GUICard{}, fmt.Errorf("only modeled Type2 read drafts can use Type2 conversion")
	}
	r := draft.Result
	if err = ValidateType2ReadResult(r); err != nil {
		return GUICard{}, err
	}
	meta := r.Type2
	layout, _ := type2LayoutForVersion(meta.Version)
	if !r.Complete || draft.Error != "" || r.Start != 0 || r.Requested != layout.pages || r.Read != layout.pages {
		return GUICard{}, fmt.Errorf("Type2 conversion requires a complete identified-layout read without operation/cleanup errors")
	}
	if err = options.Validate(); err != nil {
		return GUICard{}, err
	}
	if _, exists := c.fields["desk_read_origin"]; exists {
		return GUICard{}, fmt.Errorf("reserved desk_read_origin metadata already exists")
	}
	if len(draft.Rows) != layout.pages {
		return GUICard{}, fmt.Errorf("Type2 page count is incomplete")
	}
	data := make([][]byte, layout.pages)
	for page, row := range draft.Rows {
		if page == layout.passwordPage || page == layout.packPage {
			if row != "????????" {
				return GUICard{}, fmt.Errorf("hidden Type2 credential pages must remain unknown in the source draft")
			}
			continue
		}
		b, e := hex.DecodeString(row)
		if e != nil || len(b) != 4 {
			return GUICard{}, fmt.Errorf("Type2 page %d must contain four known bytes", page)
		}
		data[page] = b
	}
	for _, flat := range []HexBytes{r.Data, flattenType2Rows(data)} {
		if err = validateType2UIDPages(flat, meta.Identity.UID); err != nil {
			return GUICard{}, err
		}
		if type2HasMirror(layout, flat) {
			return GUICard{}, fmt.Errorf("Type2 ASCII mirror is enabled; observed virtual bytes cannot be converted as physical memory")
		}
	}
	for _, flat := range []HexBytes{r.Data, flattenType2Rows(data)} {
		if err = validateType2Counters(layout, meta.Counters, flat); err != nil {
			return GUICard{}, err
		}
	}
	data[layout.passwordPage] = append([]byte(nil), options.EmulatorPassword...)
	// The two RFUI bytes belong to the chosen emulator profile, not a source read.
	data[layout.packPage] = append(append([]byte(nil), options.EmulatorPACK...), 0, 0)
	out := c.withField("desk_read", nil).withField("desk_read_origin", map[string]any{"schema": 1, "source_card_id": c.ID, "draft": json.RawMessage(c.fields["desk_read"]), "emulator_credentials_assigned": true, "emulator_pack_rfui_assigned_zero": true, "inactive_emulator_counters_assigned_zero": layout.tag <= 1102 && len(meta.Counters) == 0})
	out.Tag = layout.tag
	out.UID = meta.Identity.UID.String()
	out.SAK = *meta.Identity.SAK
	out.ATQA = append(HexBytes(nil), meta.Identity.ATQA...)
	out.ATS = append(HexBytes{}, (*meta.Identity.ATS)...)
	out.Data = data
	// Add version/counters without discarding unrelated GUI extras.
	extra := map[string]json.RawMessage{}
	if raw := out.fields["extra"]; raw != nil {
		if err = json.Unmarshal(raw, &extra); err != nil || extra == nil {
			return GUICard{}, fmt.Errorf("GUI extra metadata must be an object")
		}
	}
	extra["ultralightVersion"], _ = json.Marshal(ints(meta.Version))
	if sig := meta.Signature; sig != nil && sig.Status == "observed" {
		extra["ultralightSignature"], _ = json.Marshal(ints(sig.Data))
	}
	counters := []int{0, 0, 0}
	if layout.tag == 1105 || layout.tag == 1106 {
		copy(counters, meta.Counters)
	} else if len(meta.Counters) == 1 {
		counters[2] = meta.Counters[0]
	}
	if layout.tag == 1107 || layout.tag == 1108 {
		if _, exists := extra["ultralightCounters"]; exists {
			return GUICard{}, fmt.Errorf("NTAG210/212 have no captured RF counters; remove separately attached counter claims")
		}
	} else {
		extra["ultralightCounters"], _ = json.Marshal(counters)
	}
	out = out.withField("extra", extra)
	if err = out.Validate(); err != nil {
		return GUICard{}, err
	}
	if _, err = out.EmulatorImage(); err != nil {
		return GUICard{}, err
	}
	return out, nil
}
func (c GUICard) PromoteType2Draft(options Type2PromotionOptions) (GUICard, error) {
	out, err := c.type2PromotionCard(options)
	if err != nil {
		return GUICard{}, err
	}
	out.ID, err = NewGUIID()
	return out, err
}
func flattenType2Rows(rows [][]byte) HexBytes {
	out := make(HexBytes, len(rows)*4)
	for page, row := range rows {
		copy(out[page*4:], row)
	}
	return out
}
func validateType2UIDPages(data []byte, uid []byte) error {
	if len(data) < 12 || len(uid) != 7 || !bytes.Equal(data[:3], uid[:3]) || !bytes.Equal(data[4:8], uid[3:]) || data[3] != (0x88^uid[0]^uid[1]^uid[2]) || data[8] != (uid[3]^uid[4]^uid[5]^uid[6]) {
		return fmt.Errorf("Type2 UID/BCC memory differs from captured identity")
	}
	return nil
}
func type2HasMirror(layout type2Layout, data []byte) bool {
	if !((layout.tag >= 1100 && layout.tag <= 1102) || layout.tag == 1107 || layout.tag == 1108) || len(data) < layout.pages*4 {
		return false
	}
	cfg := (layout.passwordPage - 2) * 4
	return type2MirrorEnabled(layout, data[cfg:])
}
func type2MirrorEnabled(layout type2Layout, cfg []byte) bool {
	if len(cfg) < 3 {
		return false
	}
	if layout.tag == 1107 || layout.tag == 1108 {
		return cfg[2] > 3
	}
	return cfg[0]&0xc0 != 0 && cfg[2] > 3
}
func type2CounterIndices(layout type2Layout, data []byte) []byte {
	if layout.tag == 1107 || layout.tag == 1108 {
		return nil
	}
	if layout.tag == 1105 || layout.tag == 1106 {
		return []byte{0, 1, 2}
	}
	cfg := (layout.passwordPage - 1) * 4
	if len(data) > cfg && data[cfg]&0x10 != 0 {
		return []byte{2}
	}
	return nil
}
func validateType2Counters(layout type2Layout, counters []int, data []byte) error {
	want := type2CounterIndices(layout, data)
	if len(counters) != len(want) {
		return fmt.Errorf("Type2 counter observations are incomplete for this configuration")
	}
	for _, v := range counters {
		if v < 0 || v > 0xffffff {
			return fmt.Errorf("invalid Type2 counter")
		}
	}
	return nil
}
