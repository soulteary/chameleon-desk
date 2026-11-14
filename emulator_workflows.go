package chameleon

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
)

type ClassicEmulatorSettings struct {
	EmulatorConfig
	FieldReset bool `json:"field_reset"`
	PRNG       byte `json:"prng"`
}
type Type2Counter struct {
	Value  uint32 `json:"value"`
	Status byte   `json:"status"`
}
type Type2EmulatorSettings struct {
	Detection bool                  `json:"detection"`
	UIDMagic  bool                  `json:"uid_magic"`
	WriteMode byte                  `json:"write_mode"`
	Version   HexBytes              `json:"version,omitempty"`
	Signature HexBytes              `json:"signature,omitempty"`
	Counters  map[byte]Type2Counter `json:"counters"`
}
type SlotEmulatorConfiguration struct {
	Slot     int                      `json:"slot"`
	TagType  uint16                   `json:"tag_type"`
	Identity Card                     `json:"identity"`
	Classic  *ClassicEmulatorSettings `json:"classic,omitempty"`
	Type2    *Type2EmulatorSettings   `json:"type2,omitempty"`
}

func (c *Client) emulatorConfiguration(ctx context.Context, s *ReaderSession) (out SlotEmulatorConfiguration, err error) {
	slot, tag, _, width, e := c.activeGeometry(ctx)
	if e != nil {
		return out, e
	}
	out.Slot, out.TagType = slot, tag
	identity, e := s.expect(ctx, CmdHf14aGetAntiCollData, nil, 0x68)
	if e != nil {
		return out, e
	}
	cards, e := parseCards(identity)
	if e != nil || len(cards) != 1 {
		return out, fmt.Errorf("invalid emulator identity")
	}
	out.Identity = cards[0]
	if width == 16 {
		b, e := s.expect(ctx, CmdMf1GetEmulatorConfig, nil, 0x68)
		if e != nil {
			return out, e
		}
		if len(b) != 5 || b[4] > 4 {
			return out, fmt.Errorf("invalid Classic configuration")
		}
		for _, v := range b[:4] {
			if v > 1 {
				return out, fmt.Errorf("invalid Classic flag")
			}
		}
		reset, e := s.expect(ctx, CmdMf1GetFieldOffDoReset, nil, 0x68)
		if e != nil {
			return out, e
		}
		prng, e := s.expect(ctx, CmdMf1GetPrngType, nil, 0x68)
		if e != nil {
			return out, e
		}
		if len(reset) != 1 || reset[0] > 1 || len(prng) != 1 || prng[0] > 2 {
			return out, fmt.Errorf("invalid Classic extra configuration")
		}
		out.Classic = &ClassicEmulatorSettings{EmulatorConfig: EmulatorConfig{b[0] != 0, b[1] != 0, b[2] != 0, b[3] != 0, b[4]}, FieldReset: reset[0] != 0, PRNG: prng[0]}
		return out, nil
	}
	b, e := s.expect(ctx, CmdMf0NtagGetEmulatorConfig, nil, 0x68)
	if e != nil {
		return out, e
	}
	if len(b) != 3 || b[0] > 1 || b[1] > 1 || b[2] > 4 {
		return out, fmt.Errorf("invalid Type2 configuration")
	}
	out.Type2 = &Type2EmulatorSettings{Detection: b[0] != 0, UIDMagic: b[1] != 0, WriteMode: b[2], Counters: map[byte]Type2Counter{}}
	if tag != 1103 && tag != 1104 {
		out.Type2.Version, e = s.expect(ctx, CmdMf0NtagGetVersionData, nil, 0x68)
		if e != nil {
			return out, e
		}
		out.Type2.Signature, e = s.expect(ctx, CmdMf0NtagGetSignatureData, nil, 0x68)
		if e != nil {
			return out, e
		}
		if len(out.Type2.Version) != 8 || len(out.Type2.Signature) != 32 {
			return out, fmt.Errorf("invalid Type2 version/signature")
		}
	}
	for index := byte(0); index < 3; index++ {
		b, e := s.expect(ctx, CmdMf0NtagGetCounterData, []byte{index}, 0x68)
		if isInvalidSlotType(e) {
			continue
		}
		if e != nil {
			return out, e
		}
		if len(b) != 4 {
			return out, fmt.Errorf("invalid Type2 counter")
		}
		out.Type2.Counters[index] = Type2Counter{uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16, b[3]}
	}
	return out, nil
}
func (c *Client) SlotEmulatorConfig(ctx context.Context, slot int) (out SlotEmulatorConfiguration, err error) {
	err = c.slotSession(ctx, slot, false, func(s *ReaderSession) error { var e error; out, e = c.emulatorConfiguration(ctx, s); return e })
	return
}

type EmulatorCounterPatch struct {
	Value        *uint32 `json:"value"`
	ResetTearing bool    `json:"reset_tearing,omitempty"`
}
type EmulatorPatch struct {
	ResetAuthAttempts bool                          `json:"reset_auth_attempts,omitempty"`
	Counters          map[byte]EmulatorCounterPatch `json:"counters,omitempty"`
	Family            string                        `json:"family,omitempty"`
	Identity          *Card                         `json:"identity,omitempty"`
	Detection         *bool                         `json:"detection,omitempty"`
	Gen1A             *bool                         `json:"gen1a,omitempty"`
	Gen2              *bool                         `json:"gen2,omitempty"`
	BlockZero         *bool                         `json:"block_zero,omitempty"`
	FieldReset        *bool                         `json:"field_reset,omitempty"`
	UIDMagic          *bool                         `json:"uid_magic,omitempty"`
	WriteMode         *byte                         `json:"write_mode,omitempty"`
	PRNG              *byte                         `json:"prng,omitempty"`
	Version           *HexBytes                     `json:"version,omitempty"`
	Signature         *HexBytes                     `json:"signature,omitempty"`
}

func (p EmulatorPatch) Validate() error {
	for index, counter := range p.Counters {
		if index > 2 || counter.Value == nil || *counter.Value > 0xffffff {
			return fmt.Errorf("counter index/value out of range")
		}
	}
	if p.Family != "" && p.Family != "classic" && p.Family != "type2" {
		return fmt.Errorf("family must be classic/type2")
	}
	if p.Identity != nil {
		if _, e := antiCollisionPayload(*p.Identity); e != nil {
			return e
		}
	}
	if p.WriteMode != nil && *p.WriteMode > 4 || p.PRNG != nil && *p.PRNG > 2 {
		return fmt.Errorf("invalid write mode/PRNG")
	}
	if p.Version != nil && len(*p.Version) != 8 || p.Signature != nil && len(*p.Signature) != 32 {
		return fmt.Errorf("version/signature must be 8/32 bytes")
	}
	return nil
}
func (c *Client) ConfigureSlotEmulator(ctx context.Context, slot int, p EmulatorPatch, persist bool) (out SlotEmulatorConfiguration, err error) {
	if e := p.Validate(); e != nil {
		return out, e
	}
	err = c.slotSession(ctx, slot, persist, func(s *ReaderSession) error {
		before, e := c.emulatorConfiguration(ctx, s)
		if e != nil {
			return e
		}
		if p.Family == "classic" && before.Classic == nil || p.Family == "type2" && before.Type2 == nil {
			return fmt.Errorf("emulator family mismatch; no configuration written")
		}
		if before.Classic != nil && (p.UIDMagic != nil || p.Version != nil || p.Signature != nil || p.ResetAuthAttempts || len(p.Counters) > 0) {
			return fmt.Errorf("Type2 properties on a Classic slot; no configuration written")
		}
		if before.Type2 != nil && (p.Gen1A != nil || p.Gen2 != nil || p.BlockZero != nil || p.FieldReset != nil || p.PRNG != nil) {
			return fmt.Errorf("Classic properties on a Type2 slot; no configuration written")
		}
		if before.Type2 != nil && (p.Version != nil && len(before.Type2.Version) == 0 || p.Signature != nil && len(before.Type2.Signature) == 0) {
			return fmt.Errorf("slot type has no version/signature property")
		}
		if before.Type2 != nil {
			for index := range p.Counters {
				if _, ok := before.Type2.Counters[index]; !ok {
					return fmt.Errorf("counter index %d unsupported; no configuration written", index)
				}
			}
			if p.ResetAuthAttempts {
				if _, ok := before.Type2.Counters[0]; !ok {
					return fmt.Errorf("auth counter reset unavailable on this tag type")
				}
			}
		}
		type operation struct {
			set, get Command
			data     []byte
		}
		ops := []operation{}
		flag := func(v *bool, set, get Command) {
			if v != nil {
				ops = append(ops, operation{set, get, []byte{boolean(*v)}})
			}
		}
		flag(p.Gen1A, 4011, 4010)
		flag(p.Gen2, 4013, 4012)
		flag(p.BlockZero, 4015, 4014)
		flag(p.FieldReset, 4038, 4039)
		flag(p.UIDMagic, 4020, 4019)
		if p.Detection != nil {
			if before.Classic != nil {
				flag(p.Detection, 4004, 4007)
			} else {
				flag(p.Detection, 4033, 4036)
			}
		}
		if p.WriteMode != nil {
			set, get := Command(4017), Command(4016)
			if before.Type2 != nil {
				set, get = 4032, 4031
			}
			ops = append(ops, operation{set, get, []byte{*p.WriteMode}})
		}
		if p.PRNG != nil {
			ops = append(ops, operation{4041, 4040, []byte{*p.PRNG}})
		}
		if p.Version != nil {
			ops = append(ops, operation{4024, 4023, append([]byte(nil), (*p.Version)...)})
		}
		if p.Signature != nil {
			ops = append(ops, operation{4026, 4025, append([]byte(nil), (*p.Signature)...)})
		}
		if p.Identity != nil {
			payload, _ := antiCollisionPayload(*p.Identity)
			ops = append(ops, operation{4001, 4018, payload})
		}
		for _, op := range ops {
			if _, e = s.expect(ctx, op.set, op.data, 0x68); e != nil {
				return fmt.Errorf("configuration may be partial at %s: %w", op.set, e)
			}
			b, e := s.expect(ctx, op.get, nil, 0x68)
			if e != nil {
				return e
			}
			if !bytes.Equal(b, op.data) {
				return fmt.Errorf("configuration readback mismatch for %s", op.set)
			}
		}
		for index := byte(0); index < 3; index++ {
			counter, ok := p.Counters[index]
			if !ok {
				continue
			}
			payload := []byte{index | boolean(counter.ResetTearing)<<7, byte(*counter.Value), byte(*counter.Value >> 8), byte(*counter.Value >> 16)}
			if _, e = s.expect(ctx, CmdMf0NtagSetCounterData, payload, 0x68); e != nil {
				return e
			}
			b, e := s.expect(ctx, CmdMf0NtagGetCounterData, []byte{index}, 0x68)
			if e != nil {
				return e
			}
			if len(b) != 4 || uint32(b[0])|uint32(b[1])<<8|uint32(b[2])<<16 != *counter.Value {
				return fmt.Errorf("counter patch readback mismatch")
			}
		}
		if p.ResetAuthAttempts {
			b, e := s.expect(ctx, CmdMf0NtagResetAuthCnt, nil, 0x68)
			if e != nil {
				return e
			}
			if len(b) != 1 {
				return fmt.Errorf("invalid auth counter reset result")
			}
		}
		out, e = c.emulatorConfiguration(ctx, s)
		return e
	})
	return
}
func (c *Client) SlotCounter(ctx context.Context, slot, index int, value *uint32, resetTearing bool, persist bool) (out Type2Counter, err error) {
	if index < 0 || index > 2 || value != nil && *value > 0xffffff {
		return out, fmt.Errorf("invalid Type2 counter index/value")
	}
	err = c.slotSession(ctx, slot, persist, func(s *ReaderSession) error {
		_, tag, _, _, e := c.activeGeometry(ctx)
		if e != nil {
			return e
		}
		if tag < 1100 || tag > 1108 {
			return fmt.Errorf("counter requires Type2 slot")
		}
		if value != nil {
			p := []byte{byte(index) | boolean(resetTearing)<<7, byte(*value), byte(*value >> 8), byte(*value >> 16)}
			if _, e = s.expect(ctx, CmdMf0NtagSetCounterData, p, 0x68); e != nil {
				return e
			}
		}
		b, e := s.expect(ctx, CmdMf0NtagGetCounterData, []byte{byte(index)}, 0x68)
		if e != nil {
			return e
		}
		if len(b) != 4 {
			return fmt.Errorf("invalid counter response")
		}
		out = Type2Counter{uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16, b[3]}
		if value != nil && out.Value != *value {
			return fmt.Errorf("counter readback mismatch")
		}
		return nil
	})
	return
}
func (c *Client) ResetSlotAuthAttempts(ctx context.Context, slot int, persist bool) (previous byte, err error) {
	err = c.slotSession(ctx, slot, persist, func(s *ReaderSession) error {
		_, tag, _, _, e := c.activeGeometry(ctx)
		if e != nil {
			return e
		}
		if tag < 1100 || tag > 1108 {
			return fmt.Errorf("authentication counter reset requires Type2")
		}
		b, e := s.expect(ctx, CmdMf0NtagResetAuthCnt, nil, 0x68)
		if e != nil {
			return e
		}
		if len(b) != 1 {
			return fmt.Errorf("invalid auth-counter reset response")
		}
		previous = b[0]
		return nil
	})
	return
}
func (c *Client) Type2DetectionLogs(ctx context.Context, slot int, start uint32, limit int) (out []HexBytes, err error) {
	if limit < 1 || limit > 100000 {
		return nil, fmt.Errorf("log limit must be 1..100000")
	}
	out = []HexBytes{}
	err = c.slotSession(ctx, slot, false, func(s *ReaderSession) error {
		_, tag, _, _, e := c.activeGeometry(ctx)
		if e != nil {
			return e
		}
		if tag < 1100 || tag > 1108 {
			return fmt.Errorf("password logs require Type2 slot")
		}
		b, e := s.expect(ctx, CmdMf0NtagGetDetectionCount, nil, 0x68)
		if e != nil {
			return e
		}
		if len(b) != 4 {
			return fmt.Errorf("invalid password log count")
		}
		count := binary.BigEndian.Uint32(b)
		if count == 0xffffffff {
			return fmt.Errorf("Type2 detection unavailable")
		}
		if start > count {
			return fmt.Errorf("start exceeds snapshot count")
		}
		for pos := start; pos < count && len(out) < limit; {
			p := make([]byte, 4)
			binary.BigEndian.PutUint32(p, pos)
			b, e = s.expect(ctx, CmdMf0NtagGetDetectionLog, p, 0x68)
			if e != nil {
				return e
			}
			if len(b) == 0 || len(b)%4 != 0 {
				return fmt.Errorf("password log truncated or no forward progress")
			}
			n := min(len(b)/4, limit-len(out))
			if uint32(n) > count-pos {
				n = int(count - pos)
			}
			for i := 0; i < n; i++ {
				out = append(out, append(HexBytes(nil), b[i*4:i*4+4]...))
			}
			pos += uint32(n)
		}
		return nil
	})
	return
}
