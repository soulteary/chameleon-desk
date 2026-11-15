package chameleon

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

type ReaderSession struct {
	c      *Client
	mu     sync.Mutex
	closed bool
}

// Do runs a wire command inside a Reader callback without reacquiring the client
// gate. Do not retain the session or call Client methods from that callback.
func (s *ReaderSession) Do(ctx context.Context, cmd Command, payload []byte) (Response, error) {
	return s.DoWithOptions(ctx, cmd, payload, CommandOptions{})
}

// DoWithOptions sends a catalog command with explicit compatibility options.
// It cannot enter the bootloader or extend the lifetime of a session.
func (s *ReaderSession) DoWithOptions(ctx context.Context, cmd Command, payload []byte, options CommandOptions) (Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Response{}, ErrClosed
	}
	if meta, ok := LookupCommand(cmd); !ok || (!meta.Registered && !options.AllowUnregistered) || cmd == CmdEnterBootloader {
		return Response{}, fmt.Errorf("command not allowed in reader session")
	}
	f, e := s.c.exchangeFrame(ctx, uint16(cmd), payload)
	return Response{cmd, f.status, StatusName(f.status), HexBytes(f.data), false}, e
}
func (s *ReaderSession) expect(ctx context.Context, cmd Command, payload []byte, status uint16) ([]byte, error) {
	r, e := s.Do(ctx, cmd, payload)
	if e != nil {
		return nil, e
	}
	if e = r.Check(status); e != nil {
		return nil, e
	}
	return r.Data, nil
}

// Reader keeps one reader-mode operation atomic and restores the initial mode.
// Call only session.Do (or session methods) in fn; calling c methods would wait
// on this same operation. fn must not retain or asynchronously use the session.
func (c *Client) Reader(ctx context.Context, fn func(*ReaderSession) error) error {
	if c.transportInfo.NativeUSBMaintenance {
		return ErrUSBBridgeRequired
	}
	return c.withMode(ctx, 1, fn)
}
func (c *Client) Emulator(ctx context.Context, fn func(*ReaderSession) error) error {
	return c.withMode(ctx, 0, fn)
}
func (c *Client) withMode(ctx context.Context, desired byte, fn func(*ReaderSession) error) (err error) {
	queued, cancel := context.WithTimeout(ctx, c.timeout)
	e := c.acquire(queued)
	cancel()
	if e != nil {
		return e
	}
	defer c.release()
	b, e := c.exchange(ctx, 1002, nil, 0x68)
	if e != nil {
		return e
	}
	if len(b) != 1 || b[0] > 1 {
		return fmt.Errorf("invalid device mode")
	}
	session := &ReaderSession{c: c}
	defer func() { session.mu.Lock(); session.closed = true; session.mu.Unlock() }()
	original := b[0]
	if original != desired {
		defer func() {
			cleanup, stop := context.WithTimeout(context.Background(), c.timeout)
			defer stop()
			if _, e := c.exchange(cleanup, 1001, []byte{original}, 0x68); e != nil {
				err = errors.Join(err, &ModeRestoreError{Err: e})
			}
		}()
		if _, e = c.exchange(ctx, 1001, []byte{desired}, 0x68); e != nil {
			return e
		}
	}
	return fn(session)
}

type KeyType byte

const (
	KeyA KeyType = 0x60
	KeyB KeyType = 0x61
)

func keyPayload(block int, kind KeyType, key []byte) ([]byte, error) {
	if block < 0 || block > 255 {
		return nil, fmt.Errorf("block must be 0..255")
	}
	if kind != KeyA && kind != KeyB {
		return nil, fmt.Errorf("key type must be 0x60 (A) or 0x61 (B)")
	}
	if len(key) != 6 {
		return nil, fmt.Errorf("MIFARE Classic key requires six bytes")
	}
	return append([]byte{byte(kind), byte(block)}, key...), nil
}
func (s *ReaderSession) ReadClassicBlock(ctx context.Context, block int, kind KeyType, key []byte) (HexBytes, error) {
	p, e := keyPayload(block, kind, key)
	if e != nil {
		return nil, e
	}
	b, e := s.expect(ctx, CmdMf1ReadOneBlock, p, 0)
	if e == nil && len(b) != 16 {
		e = fmt.Errorf("expected 16 block bytes, received %d", len(b))
	}
	return HexBytes(b), e
}
func (s *ReaderSession) WriteClassicBlock(ctx context.Context, block int, kind KeyType, key, data []byte) error {
	p, e := keyPayload(block, kind, key)
	if e != nil {
		return e
	}
	if len(data) != 16 {
		return fmt.Errorf("block data requires 16 bytes")
	}
	_, e = s.expect(ctx, CmdMf1WriteOneBlock, append(p, data...), 0)
	return e
}
func (c *Client) ReadClassicBlock(ctx context.Context, block int, kind KeyType, key []byte) (data HexBytes, err error) {
	_, err = keyPayload(block, kind, key)
	if err != nil {
		return nil, err
	}
	err = c.Reader(ctx, func(s *ReaderSession) error {
		var e error
		data, e = s.ReadClassicBlock(ctx, block, kind, key)
		return e
	})
	return
}
func (c *Client) WriteClassicBlock(ctx context.Context, block int, kind KeyType, key, data []byte) error {
	if _, e := keyPayload(block, kind, key); e != nil {
		return e
	}
	if len(data) != 16 {
		return fmt.Errorf("block data requires 16 bytes")
	}
	return c.Reader(ctx, func(s *ReaderSession) error { return s.WriteClassicBlock(ctx, block, kind, key, data) })
}
func (c *Client) AuthenticateClassic(ctx context.Context, block int, kind KeyType, key []byte) error {
	p, e := keyPayload(block, kind, key)
	if e != nil {
		return e
	}
	return c.Reader(ctx, func(s *ReaderSession) error { _, e := s.expect(ctx, CmdMf1AuthOneKeyBlock, p, 0); return e })
}

// ClassicSector maps 4K card blocks: sectors 0..31 have four blocks, 32..39 sixteen.
func ClassicSector(block int) (int, error) {
	if block < 0 || block > 255 {
		return 0, fmt.Errorf("block must be 0..255")
	}
	if block < 128 {
		return block / 4, nil
	}
	return 32 + (block-128)/16, nil
}
func ClassicTrailer(block int) (int, error) {
	sector, e := ClassicSector(block)
	if e != nil {
		return 0, e
	}
	if sector < 32 {
		return sector*4 + 3, nil
	}
	return 128 + (sector-32)*16 + 15, nil
}
func (c *Client) DumpClassic(ctx context.Context, blocks int, kind KeyType, key []byte) (dump HexBytes, err error) {
	if blocks != 20 && blocks != 64 && blocks != 128 && blocks != 256 {
		return nil, fmt.Errorf("blocks must be 20,64,128,256")
	}
	if _, e := keyPayload(0, kind, key); e != nil {
		return nil, e
	}
	err = c.Reader(ctx, func(s *ReaderSession) error {
		for block := 0; block < blocks; block++ {
			b, e := s.ReadClassicBlock(ctx, block, kind, key)
			if e != nil {
				return fmt.Errorf("block %d: %w", block, e)
			}
			dump = append(dump, b...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return dump, nil
}

type RawOptions struct {
	ActivateField    bool
	WaitResponse     bool
	AppendCRC        bool
	AutoSelect       bool
	KeepField        bool
	CheckResponseCRC bool
	TimeoutMS        uint16
	BitLength        uint16
}

func (o RawOptions) payload(data []byte) ([]byte, error) {
	bits := int(o.BitLength)
	if bits == 0 {
		bits = len(data) * 8
	}
	if bits > 65535 || len(data) > MaxPayload-5 {
		return nil, fmt.Errorf("raw frame too large")
	}
	if len(data) > 0 && (bits <= (len(data)-1)*8 || bits > len(data)*8) {
		return nil, fmt.Errorf("bit length incompatible with data")
	}
	if len(data) == 0 && bits != 0 {
		return nil, fmt.Errorf("bits without data")
	}
	flags := boolean(o.ActivateField)<<7 | boolean(o.WaitResponse)<<6 | boolean(o.AppendCRC)<<5 | boolean(o.AutoSelect)<<4 | boolean(o.KeepField)<<3 | boolean(o.CheckResponseCRC)<<2
	out := []byte{flags, byte(o.TimeoutMS >> 8), byte(o.TimeoutMS), byte(bits >> 8), byte(bits)}
	return append(out, data...), nil
}
func (s *ReaderSession) Raw14A(ctx context.Context, data []byte, o RawOptions) (HexBytes, error) {
	p, e := o.payload(data)
	if e != nil {
		return nil, e
	}
	b, e := s.expect(ctx, CmdHf14aRaw, p, 0)
	return HexBytes(b), e
}
func (c *Client) Raw14A(ctx context.Context, data []byte, o RawOptions) (out HexBytes, err error) {
	if _, e := o.payload(data); e != nil {
		return nil, e
	}
	err = c.Reader(ctx, func(s *ReaderSession) error { var e error; out, e = s.Raw14A(ctx, data, o); return e })
	return
}
func (c *Client) ReadType2Pages(ctx context.Context, start, count int) (out HexBytes, err error) {
	if start < 0 || start > 255 || count < 1 || count > 256 || count > 256-start {
		return nil, fmt.Errorf("page range must fit 0..255")
	}
	err = c.Reader(ctx, func(s *ReaderSession) error {
		for page := start; page < start+count; {
			base := page / 4 * 4
			b, e := s.Raw14A(ctx, []byte{0x30, byte(base)}, RawOptions{ActivateField: true, WaitResponse: true, AppendCRC: true, AutoSelect: true, CheckResponseCRC: true, TimeoutMS: 500})
			if e != nil {
				return e
			}
			if len(b) != 16 {
				return fmt.Errorf("READ returned %d bytes instead of 16", len(b))
			}
			n := min(4-page%4, start+count-page)
			out = append(out, b[(page%4)*4:(page%4+n)*4]...)
			page += n
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return
}
func (c *Client) WriteType2Page(ctx context.Context, page int, data []byte) error {
	if page < 0 || page > 255 || len(data) != 4 {
		return fmt.Errorf("page must be 0..255 and data exactly four bytes")
	}
	return c.Reader(ctx, func(s *ReaderSession) error {
		b, e := s.Raw14A(ctx, append([]byte{0xa2, byte(page)}, data...), RawOptions{ActivateField: true, WaitResponse: true, AppendCRC: true, AutoSelect: true, TimeoutMS: 500})
		if e != nil {
			return e
		}
		if len(b) != 1 || b[0]&0xf != 0xa {
			return fmt.Errorf("page write not acknowledged: %x", b)
		}
		return nil
	})
}

type LFCard struct {
	Protocol string   `json:"protocol"`
	TagType  uint16   `json:"tag_type,omitempty"`
	ID       HexBytes `json:"id"`
}

var lfScanCommands = map[string]Command{"em410x": CmdEm410xScan, "hidprox": CmdHidproxScan, "ioprox": CmdIoproxScan, "viking": CmdVikingScan, "pac": CmdPacScan, "jablotron": CmdJablotronScan, "pyramid": CmdPyramidScan, "em4x05": CmdEm4x05Scan}

func (c *Client) ScanLF(ctx context.Context, protocol string, format byte) (out LFCard, err error) {
	cmd, ok := lfScanCommands[protocol]
	if !ok {
		return out, fmt.Errorf("unknown LF protocol %q", protocol)
	}
	var p []byte
	if protocol == "hidprox" {
		p = []byte{format}
	}
	if protocol == "em4x05" {
		p = make([]byte, 4)
	}
	err = c.Reader(ctx, func(s *ReaderSession) error {
		b, e := s.expect(ctx, cmd, p, 0x40)
		if e != nil {
			var status *StatusError
			if errors.As(e, &status) && status.Status == 0x41 {
				return ErrNoCard
			}
			return e
		}
		out.Protocol = protocol
		if protocol == "em410x" {
			if len(b) < 2 {
				return fmt.Errorf("missing EM410x type")
			}
			out.TagType = binary.BigEndian.Uint16(b)
			n := 5
			if out.TagType == 104 {
				n = 13
			}
			if len(b) != n+2 {
				return fmt.Errorf("invalid EM410x length %d", len(b))
			}
			out.ID = append(HexBytes(nil), b[2:]...)
		} else {
			if len(b) == 0 {
				return fmt.Errorf("empty LF ID")
			}
			out.ID = append(HexBytes(nil), b...)
		}
		return nil
	})
	return
}
