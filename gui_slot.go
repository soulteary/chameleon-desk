package chameleon

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// GUICardStep is one protocol write of the ChameleonUltraGUI "load saved card
// into slot" sequence. Payloads are already encoded (slot numbers are 0..7).
type GUICardStep struct {
	Command Command  `json:"command"`
	Name    string   `json:"name"`
	Payload HexBytes `json:"payload"`
}

// GUICardLoadPlan is computed offline. It mirrors the upstream GUI slot manager
// order: enable, activate, type, defaults, identity, memory, extras, nickname.
type GUICardLoadPlan struct {
	Slot        int           `json:"slot"`
	Sense       string        `json:"sense"`
	CardTagType uint16        `json:"card_tag_type"`
	SlotTagType uint16        `json:"slot_tag_type"`
	SlotTagName string        `json:"slot_tag_name"`
	ClassicEV1  bool          `json:"classic_ev1,omitempty"`
	Nickname    string        `json:"nickname"`
	MemoryUnits int           `json:"memory_units"`
	SkippedRows int           `json:"skipped_rows"`
	Steps       []GUICardStep `json:"steps"`
	Notes       []string      `json:"notes,omitempty"`

	sense              Sense
	identity           []byte   // expected anti-collision readback (HF)
	lfGet              Command  // LF ID readback command
	lfID               []byte   // expected LF ID
	runs               []guiRun // memory runs for readback
	width              int      // memory unit width
	version, signature []byte
	counters           [][4]byte // expected counter bytes (index, 24-bit LE)
}

type guiRun struct {
	start int
	data  []byte
}

type GUICardLoadOptions struct {
	// Persist sends SLOT_DATA_CONFIG_SAVE after a verified load, as the GUI does.
	Persist bool
	// Activate leaves the loaded slot active, as the GUI does. Otherwise the
	// previously active slot is selected again.
	Activate bool
}

type GUICardLoadResult struct {
	Plan GUICardLoadPlan `json:"plan"`
	// CompletedSteps counts acknowledged plan commands. A command that fails
	// without a response may still have changed device state.
	CompletedSteps int  `json:"completed_steps"`
	Verified       bool `json:"verified"`
	Persisted      bool `json:"persisted"`
	Activated      bool `json:"activated"`
}

var guiClassicBlocks = map[uint16]int{1000: 20, 1001: 64, 1002: 128, 1003: 256}
var guiType2Pages = map[uint16]int{1100: 45, 1101: 135, 1102: 231, 1103: 16, 1104: 48, 1105: 20, 1106: 41, 1107: 20, 1108: 41}
var guiLFProtocols = map[uint16]string{100: "em410x", 101: "em410x", 102: "em410x", 103: "em410x", 104: "em410x", 150: "pac", 170: "viking", 180: "jablotron", 200: "hidprox", 201: "ioprox", 310: "idteck"}
var guiLFLengths = map[string]int{"em410x": 5, "hidprox": 13, "ioprox": 16, "viking": 4, "pac": 8, "jablotron": 5, "idteck": 8}

// guiChunkBytes matches the upstream GUI Classic upload chunk (eight blocks).
const guiChunkBytes = 128

func guiUIDBytes(uid string) ([]byte, error) {
	return hex.DecodeString(strings.NewReplacer(" ", "", ":", "").Replace(uid))
}

// guiNickname truncates on a rune boundary; firmware accepts at most 32 bytes.
func guiNickname(name string) string {
	if strings.TrimSpace(name) == "" {
		return "No name"
	}
	for len(name) > 32 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name
}

type guiCardExtra struct {
	Signature []int `json:"ultralightSignature"`
	Version   []int `json:"ultralightVersion"`
	Counters  []int `json:"ultralightCounters"`
}

func guiExtraBytes(v []int, want int, what string) ([]byte, error) {
	if len(v) == 0 {
		return nil, nil
	}
	if len(v) != want {
		return nil, fmt.Errorf("%s requires %d bytes", what, want)
	}
	out := make([]byte, len(v))
	for i, n := range v {
		if n < 0 || n > 255 {
			return nil, fmt.Errorf("%s byte out of range", what)
		}
		out[i] = byte(n)
	}
	return out, nil
}

// PlanGUICardLoad validates a saved GUI card and produces the exact write
// sequence without a device. Unsupported tag types are rejected, as in the GUI.
func PlanGUICardLoad(card GUICard, slot int) (GUICardLoadPlan, error) {
	p := GUICardLoadPlan{Slot: slot, CardTagType: card.Tag, SlotTagType: card.Tag, Nickname: guiNickname(card.Name)}
	if slot < 1 || slot > 8 {
		return p, fmt.Errorf("slot must be 1..8")
	}
	if e := card.Validate(); e != nil {
		return p, e
	}
	uid, e := guiUIDBytes(card.UID)
	if e != nil {
		return p, fmt.Errorf("invalid GUI UID: %w", e)
	}
	n := byte(slot - 1)
	classicBlocks, isClassic := guiClassicBlocks[card.Tag]
	type2Pages, isType2 := guiType2Pages[card.Tag]
	protocol, isLF := guiLFProtocols[card.Tag]
	switch {
	case isClassic:
		// Upstream stores Classic EV1 as 1K with signature sector rows 64..71.
		if card.Tag == 1001 && len(card.Data) > 64 {
			for b := 64; b < 72 && b < len(card.Data); b++ {
				if len(card.Data[b]) != 0 {
					p.ClassicEV1, p.SlotTagType, classicBlocks = true, 1002, 128
					break
				}
			}
		}
		p.sense, p.width = SenseHF, 16
	case isType2:
		p.sense, p.width = SenseHF, 4
	case card.Tag == 3000:
		p.sense = SenseHF
	case isLF:
		p.sense = SenseLF
		if card.Tag != 104 {
			if protocol == "em410x" {
				p.SlotTagType = 100
			}
		}
	default:
		return p, fmt.Errorf("tag type %d (%s) cannot be loaded into a slot from a GUI card", card.Tag, TagName(card.Tag))
	}
	p.Sense = map[Sense]string{SenseHF: "hf", SenseLF: "lf"}[p.sense]
	p.SlotTagName = TagName(p.SlotTagType)
	add := func(cmd Command, payload ...byte) {
		meta, _ := LookupCommand(cmd)
		p.Steps = append(p.Steps, GUICardStep{cmd, meta.Name, HexBytes(append([]byte{}, payload...))})
	}
	add(CmdSetSlotEnable, n, byte(p.sense), 1)
	add(CmdSetActiveSlot, n)
	add(CmdSetSlotTagType, n, byte(p.SlotTagType>>8), byte(p.SlotTagType))
	add(CmdSetSlotDataDefault, n, byte(p.SlotTagType>>8), byte(p.SlotTagType))
	if p.sense == SenseHF {
		if len(uid) != 4 && len(uid) != 7 && len(uid) != 10 {
			return p, fmt.Errorf("HF UID must be 4, 7 or 10 bytes")
		}
		if len(card.ATQA) != 2 || len(card.ATS) > 255 {
			return p, fmt.Errorf("invalid HF identity")
		}
		// GUI cards keep ATQA in numeric order; the wire order is reversed.
		id := append([]byte{byte(len(uid))}, uid...)
		id = append(id, card.ATQA[1], card.ATQA[0], card.SAK, byte(len(card.ATS)))
		id = append(id, card.ATS...)
		p.identity = id
		add(CmdHf14aSetAntiCollData, id...)
	}
	if isClassic || isType2 {
		units, cmd := classicBlocks, CmdMf1WriteEmuBlockData
		if isType2 {
			units, cmd = type2Pages, CmdMf0NtagWriteEmuPageData
		}
		var run guiRun
		flush := func() {
			if len(run.data) == 0 {
				return
			}
			payload := []byte{byte(run.start)}
			if isType2 {
				payload = append(payload, byte(len(run.data)/4))
			}
			add(cmd, append(payload, run.data...)...)
			p.runs = append(p.runs, run)
			p.MemoryUnits += len(run.data) / p.width
			run = guiRun{}
		}
		for i := 0; i < units && i < len(card.Data); i++ {
			row := card.Data[i]
			if len(row) == 0 {
				flush()
				p.SkippedRows++
				continue
			}
			if len(row) != p.width {
				return p, fmt.Errorf("memory row %d has %d bytes; want %d or empty", i, len(row), p.width)
			}
			if len(run.data) == 0 {
				run.start = i
			}
			run.data = append(run.data, row...)
			if len(run.data) >= guiChunkBytes {
				flush()
			}
		}
		flush()
		if len(card.Data) > units {
			p.Notes = append(p.Notes, fmt.Sprintf("%d rows beyond the %d-unit slot type are not written", len(card.Data)-units, units))
		}
		if len(card.Data) < units {
			p.Notes = append(p.Notes, fmt.Sprintf("card has %d of %d units; the rest keep firmware defaults", len(card.Data), units))
		}
	}
	if isType2 {
		var extra guiCardExtra
		if raw := card.fields["extra"]; len(raw) != 0 {
			if e := json.Unmarshal(raw, &extra); e != nil {
				return p, fmt.Errorf("invalid GUI extra data: %w", e)
			}
		}
		version, e := guiExtraBytes(extra.Version, 8, "ultralightVersion")
		if e != nil {
			return p, e
		}
		signature, e := guiExtraBytes(extra.Signature, 32, "ultralightSignature")
		if e != nil {
			return p, e
		}
		p.version, p.signature = version, signature
		if version != nil {
			add(CmdMf0NtagSetVersionData, version...)
		}
		if signature != nil {
			add(CmdMf0NtagSetSignatureData, signature...)
		}
		if len(extra.Counters) > 3 {
			return p, fmt.Errorf("at most three Ultralight counters are supported")
		}
		for i, v := range extra.Counters {
			if v < 0 || v > 0xffffff {
				return p, fmt.Errorf("counter %d is not a 24-bit value", i)
			}
			add(CmdMf0NtagSetCounterData, byte(i)|0x80, byte(v), byte(v>>8), byte(v>>16))
			p.counters = append(p.counters, [4]byte{byte(i), byte(v), byte(v >> 8), byte(v >> 16)})
		}
		// Counter-capable types also get the auth-attempt counter reset upstream.
		if card.Tag != 1103 && card.Tag != 1104 {
			add(CmdMf0NtagResetAuthCnt)
		}
	}
	if isLF {
		want := guiLFLengths[protocol]
		if card.Tag == 104 {
			want = 13
		}
		if len(uid) != want {
			return p, fmt.Errorf("%s ID requires %d bytes; card has %d", TagName(card.Tag), want, len(uid))
		}
		get, set, e := lfIDCommands(protocol)
		if e != nil {
			return p, e
		}
		p.lfGet, p.lfID = get, uid
		add(set, uid...)
		if card.Tag >= 101 && card.Tag <= 103 {
			p.Notes = append(p.Notes, "EM410x clock variants are loaded as plain EM410X, as the upstream GUI does")
		}
	}
	if p.Nickname != card.Name {
		p.Notes = append(p.Notes, "nickname differs from the card name (empty or longer than 32 bytes)")
	}
	add(CmdSetSlotTagNick, append([]byte{n, byte(p.sense)}, []byte(p.Nickname)...)...)
	return p, nil
}

// LoadGUICard runs the planned sequence and reads identity, memory, LF ID,
// counters, version, signature and nickname back before the optional save. Verification compares
// device RAM with the card file; it does not prove RF behaviour. A failure can
// leave the slot partially written: its previous content is already replaced
// once SET_SLOT_DATA_DEFAULT has run, so keep a backup of slots you care about.
func (c *Client) LoadGUICard(ctx context.Context, slot int, card GUICard, o GUICardLoadOptions) (out GUICardLoadResult, err error) {
	plan, e := PlanGUICardLoad(card, slot)
	out.Plan = plan
	if e != nil {
		return out, e
	}
	n := byte(slot - 1)
	err = c.Emulator(ctx, func(s *ReaderSession) (err error) {
		caps, e := s.expect(ctx, CmdGetDeviceCapabilities, nil, 0x68)
		if e != nil {
			return e
		}
		if len(caps)%2 != 0 {
			return fmt.Errorf("invalid capabilities")
		}
		available := map[Command]bool{}
		for i := 0; i < len(caps); i += 2 {
			available[Command(binary.BigEndian.Uint16(caps[i:]))] = true
		}
		required := []Command{CmdGetActiveSlot, CmdGetSlotTagNick}
		if o.Persist {
			required = append(required, CmdSlotDataConfigSave)
		}
		for _, step := range plan.Steps {
			required = append(required, step.Command)
		}
		if plan.identity != nil {
			required = append(required, CmdHf14aGetAntiCollData)
		}
		if plan.width == 16 {
			required = append(required, CmdMf1ReadEmuBlockData)
		}
		if plan.width == 4 {
			required = append(required, CmdMf0NtagReadEmuPageData)
		}
		if plan.lfGet != 0 {
			required = append(required, plan.lfGet)
		}
		if len(plan.counters) != 0 {
			required = append(required, CmdMf0NtagGetCounterData)
		}
		if plan.version != nil {
			required = append(required, CmdMf0NtagGetVersionData)
		}
		if plan.signature != nil {
			required = append(required, CmdMf0NtagGetSignatureData)
		}
		for _, cmd := range required {
			if !available[cmd] {
				return fmt.Errorf("device does not support %s; nothing written", cmd)
			}
		}
		a, e := s.expect(ctx, CmdGetActiveSlot, nil, 0x68)
		if e != nil {
			return e
		}
		if len(a) != 1 || a[0] > 7 {
			return fmt.Errorf("invalid active slot")
		}
		original := a[0]
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 3*c.timeout)
			defer cancel()
			if original != n && (err != nil || !o.Activate) {
				_, e := s.expect(cleanup, CmdSetActiveSlot, []byte{original}, 0x68)
				err = errors.Join(err, e)
			} else {
				out.Activated = err == nil
			}
			if err == nil && o.Persist {
				_, e := s.expect(cleanup, CmdSlotDataConfigSave, nil, 0x68)
				err = errors.Join(err, e)
				out.Persisted = e == nil
			}
		}()
		for i, step := range plan.Steps {
			if _, e = s.expect(ctx, step.Command, step.Payload, 0x68); e != nil {
				return fmt.Errorf("step %d %s (slot may be partially written): %w", i+1, step.Name, e)
			}
			out.CompletedSteps++
		}
		if plan.identity != nil {
			got, e := s.expect(ctx, CmdHf14aGetAntiCollData, nil, 0x68)
			if e != nil {
				return e
			}
			if !bytes.Equal(got, plan.identity) {
				return fmt.Errorf("identity readback mismatch")
			}
		}
		for _, run := range plan.runs {
			count := len(run.data) / plan.width
			cmd := CmdMf1ReadEmuBlockData
			if plan.width == 4 {
				cmd = CmdMf0NtagReadEmuPageData
			}
			got, e := s.expect(ctx, cmd, []byte{byte(run.start), byte(count)}, 0x68)
			if e != nil {
				return e
			}
			if !bytes.Equal(got, run.data) {
				return fmt.Errorf("memory readback mismatch at unit %d", run.start)
			}
		}
		for _, extra := range []struct {
			get  Command
			want []byte
			name string
		}{
			{CmdMf0NtagGetVersionData, plan.version, "version"},
			{CmdMf0NtagGetSignatureData, plan.signature, "signature"},
		} {
			if extra.want == nil {
				continue
			}
			got, e := s.expect(ctx, extra.get, nil, 0x68)
			if e != nil {
				return e
			}
			if !bytes.Equal(got, extra.want) {
				return fmt.Errorf("%s readback mismatch", extra.name)
			}
		}
		for _, want := range plan.counters {
			got, e := s.expect(ctx, CmdMf0NtagGetCounterData, want[:1], 0x68)
			if e != nil {
				return e
			}
			if len(got) != 4 || !bytes.Equal(got[:3], want[1:]) {
				return fmt.Errorf("counter %d readback mismatch", want[0])
			}
		}
		if plan.lfGet != 0 {
			got, e := s.expect(ctx, plan.lfGet, nil, 0x68)
			if e != nil {
				return e
			}
			if plan.lfGet == CmdEm410xGetEmuId {
				if got, e = normalizeEM410xID(plan.SlotTagType, got); e != nil {
					return e
				}
			}
			if !bytes.Equal(got, plan.lfID) {
				return fmt.Errorf("LF ID readback mismatch")
			}
		}
		nick, e := s.expect(ctx, CmdGetSlotTagNick, []byte{n, byte(plan.sense)}, 0x68)
		if e != nil {
			return e
		}
		if string(nick) != plan.Nickname {
			return fmt.Errorf("nickname readback mismatch")
		}
		out.Verified = true
		return nil
	})
	return out, err
}
