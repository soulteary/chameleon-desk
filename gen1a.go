package chameleon

import (
	"context"
	"fmt"
)

// Gen1A ("Chinese magic" / UID-changeable MIFARE Classic) support mirrors the
// upstream GUI gen1 writer. The card answers a backdoor unlock (0x40 at 7 bits,
// then 0x43) and afterwards accepts unauthenticated reads (0x30) and writes
// (0xA0) to any block, block 0 included. Writes change the card.

// gen1aSession runs backdoor commands over one reader session. It unlocks once
// on the first use and keeps the field for the rest.
type gen1aSession struct {
	ctx      context.Context
	s        *ReaderSession
	unlocked bool
}

func (g *gen1aSession) unlock() error {
	// 0x40 at 7 bits, no CRC/select, field on; the card acks with 0x0a.
	ack, e := g.s.Raw14A(g.ctx, []byte{0x40}, RawOptions{ActivateField: true, WaitResponse: true, KeepField: true, TimeoutMS: 1000, BitLength: 7})
	if e != nil {
		return e
	}
	if len(ack) == 0 || ack[0] != 0x0a {
		return fmt.Errorf("card did not answer the Gen1A unlock; not a Gen1A magic card")
	}
	ack, e = g.s.Raw14A(g.ctx, []byte{0x43}, RawOptions{WaitResponse: true, KeepField: true, TimeoutMS: 1000})
	if e != nil {
		return e
	}
	if len(ack) == 0 || ack[0] != 0x0a {
		return fmt.Errorf("card rejected the Gen1A second unlock step")
	}
	g.unlocked = true
	return nil
}

func (g *gen1aSession) ensure() error {
	if g.unlocked {
		return nil
	}
	return g.unlock()
}

func (g *gen1aSession) readBlock(block int) (HexBytes, error) {
	if block < 0 || block > 255 {
		return nil, fmt.Errorf("block must be 0..255")
	}
	if e := g.ensure(); e != nil {
		return nil, e
	}
	data, e := g.s.Raw14A(g.ctx, []byte{0x30, byte(block)}, RawOptions{WaitResponse: true, AppendCRC: true, KeepField: true, TimeoutMS: 1000})
	if e != nil {
		return nil, e
	}
	if len(data) != 16 && len(data) != 18 {
		return nil, fmt.Errorf("Gen1A read returned %d bytes; expected 16", len(data))
	}
	return append(HexBytes(nil), data[:16]...), nil
}

func (g *gen1aSession) writeBlock(block int, data []byte) error {
	if block < 0 || block > 255 {
		return fmt.Errorf("block must be 0..255")
	}
	if len(data) != 16 {
		return fmt.Errorf("block must be 16 bytes")
	}
	if e := g.ensure(); e != nil {
		return e
	}
	ack, e := g.s.Raw14A(g.ctx, []byte{0xA0, byte(block)}, RawOptions{WaitResponse: true, AppendCRC: true, KeepField: true, TimeoutMS: 1000})
	if e != nil {
		return e
	}
	if len(ack) == 0 || ack[0] != 0x0a {
		return fmt.Errorf("Gen1A write command not acknowledged at block %d", block)
	}
	ack, e = g.s.Raw14A(g.ctx, data, RawOptions{WaitResponse: true, AppendCRC: true, KeepField: true, TimeoutMS: 1000})
	if e != nil {
		return e
	}
	if len(ack) == 0 || ack[0] != 0x0a {
		return fmt.Errorf("Gen1A write data not acknowledged at block %d", block)
	}
	return nil
}

// DetectGen1A reports whether a Gen1A magic card answers the backdoor unlock.
func (c *Client) DetectGen1A(ctx context.Context) (magic bool, err error) {
	err = c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		g := &gen1aSession{ctx: ctx, s: s}
		if e := g.unlock(); e != nil {
			magic = false
			return nil // a non-magic card is a clean "false", not an error
		}
		magic = true
		return nil
	})
	return
}

// Gen1AReadBlock reads one block through the backdoor (no authentication).
func (c *Client) Gen1AReadBlock(ctx context.Context, block int) (out HexBytes, err error) {
	err = c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		out, e = (&gen1aSession{ctx: ctx, s: s}).readBlock(block)
		return e
	})
	return
}

// Gen1AWriteBlock writes one 16-byte block through the backdoor. This changes
// the card; block 0 (UID/BCC/SAK/ATQA) is writable too.
func (c *Client) Gen1AWriteBlock(ctx context.Context, block int, data []byte) error {
	return c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		return (&gen1aSession{ctx: ctx, s: s}).writeBlock(block, data)
	})
}

// Gen1ASetUID reads block 0, replaces the UID (and its BCC for a 4-byte UID),
// keeps the SAK/ATQA/manufacturer bytes, and writes block 0 back.
func (c *Client) Gen1ASetUID(ctx context.Context, uid []byte) error {
	if len(uid) != 4 && len(uid) != 7 {
		return fmt.Errorf("Gen1A UID must be 4 or 7 bytes")
	}
	return c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		g := &gen1aSession{ctx: ctx, s: s}
		block0, e := g.readBlock(0)
		if e != nil {
			return e
		}
		out := append(HexBytes(nil), block0...)
		if len(uid) == 4 {
			copy(out[0:4], uid)
			out[4] = uid[0] ^ uid[1] ^ uid[2] ^ uid[3]
		} else {
			copy(out[0:7], uid) // 7-byte UID occupies bytes 0..6; byte 7 stays SAK
		}
		return g.writeBlock(0, out)
	})
}
