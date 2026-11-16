package chameleon

import (
	"context"
	"encoding/binary"
	"fmt"
	"reflect"
)

type CurrentEmulatorRequest struct {
	Slot     int                        `json:"slot"`
	TagType  uint16                     `json:"tag_type"`
	Action   string                     `json:"action"`
	Expected *SlotEmulatorConfiguration `json:"expected,omitempty"`
	Patch    EmulatorPatch              `json:"patch"`
	Persist  bool                       `json:"persist"`
	Start    uint32                     `json:"start"`
	Limit    int                        `json:"limit"`
}

func (r CurrentEmulatorRequest) Requirements() ([]Command, error) {
	if _, _, err := (SlotReadRequest{Slot: r.Slot, Sense: SenseHF, TagType: r.TagType}).geometry(); err != nil {
		return nil, err
	}
	commands := []Command{CmdGetDeviceCapabilities, CmdGetDeviceMode, CmdGetActiveSlot, CmdGetSlotInfo}
	classic := r.TagType <= 1003
	if r.Action != "configure" && (r.Expected != nil || r.Persist || !reflect.DeepEqual(r.Patch, EmulatorPatch{})) {
		return nil, fmt.Errorf("read operations do not accept configuration or save parameters")
	}
	if r.Action == "logs" {
		if r.Limit < 1 || r.Limit > 1024 {
			return nil, fmt.Errorf("select 1..1024 detection records")
		}
		if classic {
			return append(commands, CmdMf1GetDetectionCount, CmdMf1GetDetectionLog), nil
		}
		return append(commands, CmdMf0NtagGetDetectionCount, CmdMf0NtagGetDetectionLog), nil
	}
	commands = append(commands, CmdHf14aGetAntiCollData)
	if classic {
		commands = append(commands, CmdMf1GetEmulatorConfig, CmdMf1GetFieldOffDoReset, CmdMf1GetPrngType)
	} else {
		commands = append(commands, CmdMf0NtagGetPageCount, CmdMf0NtagGetEmulatorConfig, CmdMf0NtagGetCounterData)
		if r.TagType != 1103 && r.TagType != 1104 {
			commands = append(commands, CmdMf0NtagGetVersionData, CmdMf0NtagGetSignatureData)
		}
	}
	if r.Action == "configure" {
		if r.Expected == nil || r.Expected.Slot != r.Slot || r.Expected.TagType != r.TagType {
			return nil, fmt.Errorf("configure requires the observed current slot configuration")
		}
		if err := r.Patch.Validate(); err != nil {
			return nil, err
		}
		p := r.Patch
		if classic && r.Expected.Classic == nil || !classic && r.Expected.Type2 == nil {
			return nil, fmt.Errorf("expected configuration family differs")
		}
		if !classic {
			for index := range p.Counters {
				if _, ok := r.Expected.Type2.Counters[index]; !ok {
					return nil, fmt.Errorf("counter index %d was not observed as supported", index)
				}
			}
			if p.ResetAuthAttempts {
				if _, ok := r.Expected.Type2.Counters[0]; !ok {
					return nil, fmt.Errorf("authentication counter reset unavailable for this type")
				}
			}
			if p.Version != nil && len(r.Expected.Type2.Version) != 8 || p.Signature != nil && len(r.Expected.Type2.Signature) != 32 {
				return nil, fmt.Errorf("version/signature property was not captured for this type")
			}
		}

		if classic && (p.UIDMagic != nil || p.Version != nil || p.Signature != nil || p.ResetAuthAttempts || len(p.Counters) > 0) || !classic && (p.Gen1A != nil || p.Gen2 != nil || p.BlockZero != nil || p.FieldReset != nil || p.PRNG != nil) {
			return nil, fmt.Errorf("patch properties differ from selected slot family")
		}
		flag := func(on bool, set, get Command) {
			if on {
				commands = append(commands, set, get)
			}
		}
		flag(p.Gen1A != nil, 4011, 4010)
		flag(p.Gen2 != nil, 4013, 4012)
		flag(p.BlockZero != nil, 4015, 4014)
		flag(p.FieldReset != nil, 4038, 4039)
		flag(p.PRNG != nil, 4041, 4040)
		flag(p.UIDMagic != nil, 4020, 4019)
		if classic {
			flag(p.Detection != nil, 4004, 4007)
			flag(p.WriteMode != nil, 4017, 4016)
		} else {
			flag(p.Detection != nil, 4033, 4036)
			flag(p.WriteMode != nil, 4032, 4031)
		}
		flag(p.Identity != nil, 4001, 4018)
		flag(p.Version != nil, 4024, 4023)
		flag(p.Signature != nil, 4026, 4025)
		flag(len(p.Counters) > 0, 4028, 4027)
		if p.ResetAuthAttempts {
			commands = append(commands, CmdMf0NtagResetAuthCnt)
		}
		if r.Persist {
			commands = append(commands, CmdSlotDataConfigSave)
		}
	} else if r.Action != "configuration" {
		return nil, fmt.Errorf("select configuration, configure or logs")
	}
	return uniqueCommands(commands), nil
}

func (c *Client) currentEmulatorSource(ctx context.Context, s *ReaderSession, r CurrentEmulatorRequest) error {
	mode, err := s.expect(ctx, CmdGetDeviceMode, nil, 0x68)
	if err != nil {
		return err
	}
	if len(mode) != 1 || mode[0] != 0 {
		return fmt.Errorf("current emulator tools require emulator mode; mode was not changed")
	}
	active, err := s.expect(ctx, CmdGetActiveSlot, nil, 0x68)
	if err != nil {
		return err
	}
	if len(active) != 1 || int(active[0])+1 != r.Slot {
		return fmt.Errorf("active slot changed; refresh current slot")
	}
	types, err := s.expect(ctx, CmdGetSlotInfo, nil, 0x68)
	if err != nil {
		return err
	}
	if len(types) != 32 || binary.BigEndian.Uint16(types[(r.Slot-1)*4:]) != r.TagType {
		return fmt.Errorf("current slot type changed")
	}
	return nil
}

func (c *Client) currentEmulatorSession(ctx context.Context, r CurrentEmulatorRequest, fn func(*ReaderSession) error) error {
	commands, err := r.Requirements()
	if err != nil {
		return err
	}
	if c.transportInfo.NativeUSBMaintenance {
		return ErrUSBBridgeRequired
	}
	queued, cancel := context.WithTimeout(ctx, c.timeout)
	err = c.acquire(queued)
	cancel()
	if err != nil {
		return err
	}
	defer c.release()
	s := &ReaderSession{c: c}
	defer func() { s.closed = true }()
	raw, err := s.expect(ctx, CmdGetDeviceCapabilities, nil, 0x68)
	if err != nil {
		return err
	}
	if len(raw)%2 != 0 {
		return fmt.Errorf("invalid firmware capabilities")
	}
	supported := map[Command]bool{}
	for i := 0; i < len(raw); i += 2 {
		supported[Command(binary.BigEndian.Uint16(raw[i:]))] = true
	}
	for _, cmd := range commands {
		if !supported[cmd] {
			return fmt.Errorf("current emulator tool requires command %d", cmd)
		}
	}
	if err = c.currentEmulatorSource(ctx, s, r); err != nil {
		return err
	}
	if err = fn(s); err != nil {
		return err
	}
	return c.currentEmulatorSource(ctx, s, r)
}

func (c *Client) ReadCurrentEmulator(ctx context.Context, r CurrentEmulatorRequest) (out SlotEmulatorConfiguration, err error) {
	if r.Action != "configuration" {
		return out, fmt.Errorf("configuration read action required")
	}
	err = c.currentEmulatorSession(ctx, r, func(s *ReaderSession) error { var e error; out, e = c.emulatorConfiguration(ctx, s); return e })
	return
}

type CurrentEmulatorUpdateResult struct {
	Before         *SlotEmulatorConfiguration `json:"before"`
	After          *SlotEmulatorConfiguration `json:"after,omitempty"`
	WriteAttempted bool                       `json:"write_attempted"`
	Verified       bool                       `json:"verified"`
	Persisted      bool                       `json:"persisted"`
	Outcome        string                     `json:"outcome"`
}

func (c *Client) ConfigureCurrentEmulator(ctx context.Context, r CurrentEmulatorRequest, beforeWrite func()) (out CurrentEmulatorUpdateResult, err error) {
	out.Before = r.Expected
	out.Outcome = "write_not_started"
	if r.Action != "configure" {
		return out, fmt.Errorf("configure action required")
	}
	mark := func() {
		out.WriteAttempted = true
		out.Outcome = "configuration_may_be_partial"
		if beforeWrite != nil {
			beforeWrite()
		}
	}
	err = c.currentEmulatorSession(ctx, r, func(s *ReaderSession) error {
		cfg, e := c.configureEmulatorSession(ctx, s, r.Patch, r.Expected, mark)
		if e != nil {
			return e
		}
		out.After = &cfg
		if e = c.currentEmulatorSource(ctx, s, r); e != nil {
			return e
		}
		out.Verified = true
		if r.Persist {
			mark()
			if _, e = s.expect(ctx, CmdSlotDataConfigSave, nil, 0x68); e != nil {
				return e
			}
			out.Persisted = true
		}
		return nil
	})
	if err == nil {
		out.Outcome = "verified"
	}
	return
}

type CurrentDetectionLogs struct {
	Slot      int        `json:"slot"`
	TagType   uint16     `json:"tag_type"`
	Start     uint32     `json:"start"`
	Classic   []AuthLog  `json:"classic,omitempty"`
	Passwords []HexBytes `json:"passwords,omitempty"`
	Complete  bool       `json:"request_complete"`
}

func (c *Client) ReadCurrentDetectionLogs(ctx context.Context, r CurrentEmulatorRequest) (out CurrentDetectionLogs, err error) {
	out.Slot, out.TagType, out.Start = r.Slot, r.TagType, r.Start
	if r.Action != "logs" {
		return out, fmt.Errorf("logs action required")
	}
	err = c.currentEmulatorSession(ctx, r, func(s *ReaderSession) error {
		if r.TagType <= 1003 {
			var e error
			out.Classic, e = classicDetectionLogs(ctx, s, r.Start, r.Limit, false)
			return e
		}
		raw, e := s.expect(ctx, CmdMf0NtagGetDetectionCount, nil, 0x68)
		if e != nil {
			return e
		}
		if len(raw) != 4 {
			return fmt.Errorf("invalid detection count")
		}
		count := binary.BigEndian.Uint32(raw)
		if count == 0xffffffff || r.Start > count {
			return fmt.Errorf("detection unavailable or start beyond snapshot")
		}
		for pos := r.Start; pos < count && len(out.Passwords) < r.Limit; {
			p := make([]byte, 4)
			binary.BigEndian.PutUint32(p, pos)
			raw, e = s.expect(ctx, CmdMf0NtagGetDetectionLog, p, 0x68)
			if e != nil {
				return e
			}
			if len(raw) == 0 || len(raw)%4 != 0 {
				return fmt.Errorf("truncated detection response")
			}
			n := min(len(raw)/4, r.Limit-len(out.Passwords), int(count-pos))
			for i := 0; i < n; i++ {
				out.Passwords = append(out.Passwords, append(HexBytes(nil), raw[i*4:i*4+4]...))
			}
			pos += uint32(n)
		}
		return nil
	})
	out.Complete = err == nil
	return
}
