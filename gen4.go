package chameleon

import (
	"context"
	"fmt"
)

// Gen4 (Ultimate Magic Card / GTU) configuration and block access, mirroring
// the upstream GUI Gen4 tool and the proxmark3 "g4top" notes. GDM/USCUID cards
// use a different protocol and are out of scope. Every command is a reader
// exchange of 0xCF + 4-byte password + command byte; writes change the card.

// Gen4Config is the decoded 30-byte Gen4 configuration block.
type Gen4Config struct {
	Raw            HexBytes `json:"raw"`
	Protocol       byte     `json:"protocol"`
	UIDLength      int      `json:"uid_length"`
	Password       HexBytes `json:"password"`
	ShadowMode     byte     `json:"shadow_mode"`
	ATS            HexBytes `json:"ats"`
	ATQA           HexBytes `json:"atqa"`
	SAK            byte     `json:"sak"`
	UltralightMode byte     `json:"ultralight_mode"`
	MaxBlock       byte     `json:"max_block"`
	Block0Mode     byte     `json:"block0_mode"`
}

// ParseGen4Config decodes a configuration response (30, 32 or 34 bytes; the
// extra bytes are an internal checksum and/or RF CRC).
func ParseGen4Config(data []byte) (Gen4Config, error) {
	if len(data) != 30 && len(data) != 32 && len(data) != 34 {
		return Gen4Config{}, fmt.Errorf("expected a 30, 32 or 34 byte Gen4 configuration, got %d", len(data))
	}
	b := append([]byte(nil), data[:30]...)
	if b[7] > 16 {
		return Gen4Config{}, fmt.Errorf("invalid Gen4 configuration (ATS length %d)", b[7])
	}
	uidLen := map[byte]int{0: 4, 1: 7, 2: 10}[b[1]]
	return Gen4Config{
		Raw: b, Protocol: b[0], UIDLength: uidLen, Password: append(HexBytes(nil), b[2:6]...),
		ShadowMode: b[6], ATS: append(HexBytes(nil), b[8:8+b[7]]...), ATQA: HexBytes{b[25], b[24]},
		SAK: b[26], UltralightMode: b[27], MaxBlock: b[28], Block0Mode: b[29],
	}, nil
}

// gen4Session issues Gen4 commands over one reader session, activating and
// selecting on the first exchange and keeping the field for the rest.
type gen4Session struct {
	ctx      context.Context
	s        *ReaderSession
	password []byte
	first    bool
}

func newGen4Session(ctx context.Context, s *ReaderSession, password []byte) *gen4Session {
	return &gen4Session{ctx: ctx, s: s, password: append([]byte(nil), password...), first: true}
}

func (g *gen4Session) command(cmd byte, data []byte) (HexBytes, error) {
	frame := append([]byte{0xCF}, g.password...)
	frame = append(frame, cmd)
	frame = append(frame, data...)
	opts := RawOptions{WaitResponse: true, AppendCRC: true, KeepField: true, TimeoutMS: 1000}
	if g.first {
		opts.ActivateField, opts.AutoSelect, g.first = true, true, false
	}
	return g.s.Raw14A(g.ctx, frame, opts)
}

func (g *gen4Session) checkStatus(resp []byte) error {
	if (len(resp) != 2 && len(resp) != 4) || resp[0] != 0x90 || resp[1] != 0x00 {
		return fmt.Errorf("Gen4 command rejected (%s); wrong password?", HexBytes(resp))
	}
	return nil
}

func (g *gen4Session) readConfig() (Gen4Config, error) {
	data, e := g.command(0xC6, nil)
	if e != nil {
		return Gen4Config{}, e
	}
	return ParseGen4Config(data)
}

func (g *gen4Session) readBlock(block int) (HexBytes, error) {
	if block < 0 || block > 255 {
		return nil, fmt.Errorf("block must be 0..255")
	}
	data, e := g.command(0xCE, []byte{byte(block)})
	if e != nil {
		return nil, e
	}
	if len(data) != 16 && len(data) != 18 {
		return nil, fmt.Errorf("expected a 16-byte block, got %d", len(data))
	}
	return append(HexBytes(nil), data[:16]...), nil
}

func (g *gen4Session) writeBlock(block int, data []byte) error {
	if block < 0 || block > 255 {
		return fmt.Errorf("block must be 0..255")
	}
	if len(data) != 16 {
		return fmt.Errorf("block must be 16 bytes")
	}
	resp, e := g.command(0xCD, append([]byte{byte(block)}, data...))
	if e != nil {
		return e
	}
	return g.checkStatus(resp)
}

// Gen4ReadConfig reads and decodes the configuration block.
func (c *Client) Gen4ReadConfig(ctx context.Context, password []byte) (out Gen4Config, err error) {
	if len(password) != 4 {
		return out, fmt.Errorf("Gen4 password must be 4 bytes")
	}
	err = c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		out, e = newGen4Session(ctx, s, password).readConfig()
		return e
	})
	return
}

// Gen4ReadVersion reads the raw version bytes (command CC).
func (c *Client) Gen4ReadVersion(ctx context.Context, password []byte) (out HexBytes, err error) {
	if len(password) != 4 {
		return nil, fmt.Errorf("Gen4 password must be 4 bytes")
	}
	err = c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		out, e = newGen4Session(ctx, s, password).command(0xCC, nil)
		return e
	})
	return
}

// Gen4Dump reads count blocks starting at first.
func (c *Client) Gen4Dump(ctx context.Context, password []byte, first, count int) (out []HexBytes, err error) {
	if len(password) != 4 {
		return nil, fmt.Errorf("Gen4 password must be 4 bytes")
	}
	if first < 0 || count < 1 || first+count > 256 {
		return nil, fmt.Errorf("block range must fit 0..255")
	}
	err = c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		g := newGen4Session(ctx, s, password)
		for block := first; block < first+count; block++ {
			b, e := g.readBlock(block)
			if e != nil {
				return e
			}
			out = append(out, b)
		}
		return nil
	})
	return
}

// Gen4WriteBlock writes one 16-byte block. This changes the card.
func (c *Client) Gen4WriteBlock(ctx context.Context, password []byte, block int, data []byte) error {
	if len(password) != 4 {
		return fmt.Errorf("Gen4 password must be 4 bytes")
	}
	return c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		return newGen4Session(ctx, s, password).writeBlock(block, data)
	})
}

// Gen4WriteConfig writes a 30-byte configuration. permanent (command F1)
// makes it non-volatile; otherwise F0 is used. The password may change here.
func (c *Client) Gen4WriteConfig(ctx context.Context, password, config []byte, permanent bool) error {
	if len(password) != 4 {
		return fmt.Errorf("Gen4 password must be 4 bytes")
	}
	if _, e := ParseGen4Config(config); e != nil {
		return e
	}
	cmd := byte(0xF0)
	if permanent {
		cmd = 0xF1
	}
	return c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		g := newGen4Session(ctx, s, password)
		resp, e := g.command(cmd, config[:30])
		if e != nil {
			return e
		}
		return g.checkStatus(resp)
	})
}

// Gen4SetPassword reads the configuration, replaces the 4-byte password and
// writes it back (non-permanent). This changes the card's Gen4 password.
func (c *Client) Gen4SetPassword(ctx context.Context, oldPassword, newPassword []byte, permanent bool) error {
	if len(oldPassword) != 4 || len(newPassword) != 4 {
		return fmt.Errorf("Gen4 passwords must be 4 bytes")
	}
	cmd := byte(0xF0)
	if permanent {
		cmd = 0xF1
	}
	return c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		g := newGen4Session(ctx, s, oldPassword)
		config, e := g.readConfig()
		if e != nil {
			return e
		}
		raw := append([]byte(nil), config.Raw...)
		copy(raw[2:6], newPassword)
		resp, e := g.command(cmd, raw)
		if e != nil {
			return e
		}
		return g.checkStatus(resp)
	})
}

// Gen4SetUID changes the UID, preserving manufacturer, lock and OTP bytes the
// way the GUI does. The UID length must match the configured layout.
func (c *Client) Gen4SetUID(ctx context.Context, password, uid []byte) error {
	if len(password) != 4 {
		return fmt.Errorf("Gen4 password must be 4 bytes")
	}
	return c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		g := newGen4Session(ctx, s, password)
		config, e := g.readConfig()
		if e != nil {
			return e
		}
		if config.UIDLength == 0 || config.Protocol > 1 || len(uid) != config.UIDLength {
			return fmt.Errorf("UID must be %d bytes for this card's layout", config.UIDLength)
		}
		if config.Protocol == 1 && len(uid) == 7 {
			b0, e := g.readBlock(0)
			if e != nil {
				return e
			}
			b1, e := g.readBlock(1)
			if e != nil {
				return e
			}
			b2, e := g.readBlock(2)
			if e != nil {
				return e
			}
			copy(b0[0:3], uid[0:3])
			b0[3] = 0x88 ^ uid[0] ^ uid[1] ^ uid[2]
			copy(b1[0:4], uid[3:7])
			b2[0] = uid[3] ^ uid[4] ^ uid[5] ^ uid[6]
			for _, w := range []struct {
				block int
				data  []byte
			}{{0, b0}, {1, b1}, {2, b2}} {
				if e = g.writeBlock(w.block, w.data); e != nil {
					return e
				}
			}
			return nil
		}
		block, e := g.readBlock(0)
		if e != nil {
			return e
		}
		copy(block[0:len(uid)], uid)
		if config.Protocol == 0 && len(uid) == 4 {
			var bcc byte
			for _, x := range uid {
				bcc ^= x
			}
			block[len(uid)] = bcc
		}
		return g.writeBlock(0, block)
	})
}
