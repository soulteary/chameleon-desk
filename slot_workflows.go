package chameleon

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"
)

// slotSession serializes selection, work, restoration, and optional final save.
// Upstream slot switching may persist existing RAM, even for a read operation.
func (c *Client) slotSession(ctx context.Context, slot int, persist bool, fn func(*ReaderSession) error) error {
	if slot < 0 || slot > 8 {
		return fmt.Errorf("slot must be 0 (active) or 1..8")
	}
	return c.Emulator(ctx, func(s *ReaderSession) (err error) {
		a, e := s.expect(ctx, CmdGetActiveSlot, nil, 0x68)
		if e != nil {
			return e
		}
		if len(a) != 1 || a[0] > 7 {
			return fmt.Errorf("invalid active slot")
		}
		original := a[0]
		selected := original
		if slot > 0 {
			selected = byte(slot - 1)
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 3*c.timeout)
			defer cancel()
			if selected != original {
				_, e := s.expect(cleanup, CmdSetActiveSlot, []byte{original}, 0x68)
				err = errors.Join(err, e)
			}
			if err == nil && persist {
				_, e := s.expect(cleanup, CmdSlotDataConfigSave, nil, 0x68)
				err = errors.Join(err, e)
			}
		}()
		if selected != original {
			if _, e = s.expect(ctx, CmdSetActiveSlot, []byte{selected}, 0x68); e != nil {
				return e
			}
		}
		return fn(s)
	})
}

type SlotDetail struct {
	Slot
	HFNickname *string `json:"hf_nickname"`
	LFNickname *string `json:"lf_nickname"`
}

func slotNickname(ctx context.Context, s *ReaderSession, slot int, sense Sense) (*string, error) {
	r, e := s.Do(ctx, CmdGetSlotTagNick, []byte{byte(slot - 1), byte(sense)})
	if e != nil {
		return nil, e
	}
	if r.Status == 0x71 {
		return nil, nil
	}
	if e = r.Check(0x68); e != nil {
		return nil, e
	}
	if !utf8.Valid(r.Data) || len(r.Data) > 32 {
		return nil, fmt.Errorf("invalid nickname")
	}
	name := string(r.Data)
	return &name, nil
}
func (c *Client) SlotDetails(ctx context.Context) (out []SlotDetail, err error) {
	if e := c.acquire(ctx); e != nil {
		return nil, e
	}
	defer c.release()
	s := &ReaderSession{c: c}
	defer func() { s.closed = true }()
	types, e := s.expect(ctx, CmdGetSlotInfo, nil, 0x68)
	if e != nil {
		return nil, e
	}
	enabled, e := s.expect(ctx, CmdGetEnabledSlots, nil, 0x68)
	if e != nil {
		return nil, e
	}
	active, e := s.expect(ctx, CmdGetActiveSlot, nil, 0x68)
	if e != nil {
		return nil, e
	}
	if len(types) != 32 || len(enabled) != 16 || len(active) != 1 || active[0] > 7 {
		return nil, fmt.Errorf("invalid slot list")
	}
	for n := 0; n < 8; n++ {
		if enabled[n*2] > 1 || enabled[n*2+1] > 1 {
			return nil, fmt.Errorf("invalid slot flags")
		}
		hf, lf := binary.BigEndian.Uint16(types[n*4:]), binary.BigEndian.Uint16(types[n*4+2:])
		v := SlotDetail{Slot: Slot{Number: n + 1, Active: active[0] == byte(n), HFType: hf, LFType: lf, HFName: TagName(hf), LFName: TagName(lf), HFEnabled: enabled[n*2] != 0, LFEnabled: enabled[n*2+1] != 0}}
		v.HFNickname, e = slotNickname(ctx, s, n+1, SenseHF)
		if e != nil {
			return nil, e
		}
		v.LFNickname, e = slotNickname(ctx, s, n+1, SenseLF)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func (c *Client) ReadSlotNickname(ctx context.Context, slot int, sense Sense) (out *string, err error) {
	if _, e := slotByte(slot); e != nil {
		return nil, e
	}
	if _, e := senseByte(sense); e != nil {
		return nil, e
	}
	if e := c.acquire(ctx); e != nil {
		return nil, e
	}
	defer c.release()
	s := &ReaderSession{c: c}
	return slotNickname(ctx, s, slot, sense)
}
func (c *Client) UpdateSlotNickname(ctx context.Context, slot int, sense Sense, name *string) error {
	if _, e := slotByte(slot); e != nil {
		return e
	}
	if _, e := senseByte(sense); e != nil {
		return e
	}
	if name != nil && (!utf8.ValidString(*name) || len(*name) > 32) {
		return fmt.Errorf("nickname must be valid UTF-8 <=32 bytes")
	}
	if e := c.acquire(ctx); e != nil {
		return e
	}
	defer c.release()
	s := &ReaderSession{c: c}
	if name == nil {
		current, e := slotNickname(ctx, s, slot, sense)
		if e != nil {
			return e
		}
		if current == nil {
			return nil
		}
	}
	cmd := CmdDeleteSlotTagNick
	p := []byte{byte(slot - 1), byte(sense)}
	if name != nil {
		cmd = CmdSetSlotTagNick
		p = append(p, []byte(*name)...)
	}
	if _, e := s.expect(ctx, cmd, p, 0x68); e != nil {
		return e
	}
	actual, e := slotNickname(ctx, s, slot, sense)
	if e != nil {
		return e
	}
	if name == nil && actual != nil || name != nil && (actual == nil || *actual != *name) {
		return fmt.Errorf("nickname readback mismatch")
	}
	return nil
}

type SlotInitResult struct {
	CompletedSlots int  `json:"completed_slots"`
	Verified       bool `json:"verified"`
	Saved          bool `json:"saved"`
}

func (c *Client) InitializeAllSlots(ctx context.Context, hf, lf uint16) (out SlotInitResult, err error) {
	if _, e := PlanSlotInitialization(hf, lf); e != nil {
		return out, e
	}
	err = c.slotSession(ctx, 0, true, func(s *ReaderSession) error {
		for n := byte(0); n < 8; n++ {
			for _, typ := range []uint16{hf, lf} {
				p := []byte{n, byte(typ >> 8), byte(typ)}
				if _, e := s.expect(ctx, CmdSetSlotTagType, p, 0x68); e != nil {
					return e
				}
				if _, e := s.expect(ctx, CmdSetSlotDataDefault, p, 0x68); e != nil {
					return e
				}
			}
			for _, sense := range []Sense{SenseHF, SenseLF} {
				if _, e := s.expect(ctx, CmdSetSlotEnable, []byte{n, byte(sense), 1}, 0x68); e != nil {
					return e
				}
			}
			out.CompletedSlots++
		}
		types, e := s.expect(ctx, CmdGetSlotInfo, nil, 0x68)
		if e != nil {
			return e
		}
		enabled, e := s.expect(ctx, CmdGetEnabledSlots, nil, 0x68)
		if e != nil {
			return e
		}
		if len(types) != 32 || len(enabled) != 16 {
			return fmt.Errorf("invalid initialization readback")
		}
		for i := 0; i < 8; i++ {
			if binary.BigEndian.Uint16(types[i*4:]) != hf || binary.BigEndian.Uint16(types[i*4+2:]) != lf || !bytes.Equal(enabled[i*2:i*2+2], []byte{1, 1}) {
				return fmt.Errorf("slot %d initialization readback mismatch", i+1)
			}
		}
		out.Verified = true
		return nil
	})
	out.Saved = err == nil
	return
}
func (c *Client) SlotPRNG(ctx context.Context, slot int, value *byte, persist bool) (out byte, err error) {
	if value != nil && *value > 2 {
		return 0, fmt.Errorf("PRNG must be 0..2")
	}
	err = c.slotSession(ctx, slot, persist, func(s *ReaderSession) error {
		_, tag, _, _, e := c.activeGeometry(ctx)
		if e != nil {
			return e
		}
		if tag < 1000 || tag > 1003 {
			return fmt.Errorf("PRNG requires Classic slot")
		}
		if value != nil {
			if _, e = s.expect(ctx, CmdMf1SetPrngType, []byte{*value}, 0x68); e != nil {
				return e
			}
		}
		b, e := s.expect(ctx, CmdMf1GetPrngType, nil, 0x68)
		if e != nil {
			return e
		}
		if len(b) != 1 || b[0] > 2 {
			return fmt.Errorf("invalid PRNG response")
		}
		out = b[0]
		if value != nil && out != *value {
			return fmt.Errorf("PRNG readback mismatch")
		}
		return nil
	})
	return
}

type SlotInitializationPlan struct {
	Slots        int    `json:"slots"`
	HFType       uint16 `json:"hf_type"`
	LFType       uint16 `json:"lf_type"`
	SavesToFlash bool   `json:"saves_to_flash"`
}

func PlanSlotInitialization(hf, lf uint16) (SlotInitializationPlan, error) {
	if !((hf >= 1000 && hf <= 1003) || (hf >= 1100 && hf <= 1108)) {
		return SlotInitializationPlan{}, fmt.Errorf("HF type must support portable memory readback")
	}
	if _, _, e := lfBackupCommands(lf); e != nil {
		return SlotInitializationPlan{}, e
	}
	return SlotInitializationPlan{8, hf, lf, true}, nil
}
