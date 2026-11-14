package chameleon

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

type ReaderConfig struct {
	BCC  int8 `json:"bcc"`
	CL2  int8 `json:"cl2"`
	CL3  int8 `json:"cl3"`
	RATS int8 `json:"rats"`
}

func (v ReaderConfig) payload() ([]byte, error) {
	p := []byte{byte(v.BCC), byte(v.CL2), byte(v.CL3), byte(v.RATS)}
	for _, b := range p {
		if int8(b) < -1 || int8(b) > 2 {
			return nil, fmt.Errorf("reader overrides must be -1 (leave unchanged) or 0..2")
		}
	}
	return p, nil
}
func (c *Client) ReaderConfig(ctx context.Context) (ReaderConfig, error) {
	b, e := c.request(ctx, CmdHf14aGetConfig, nil, 0x68)
	if e != nil {
		return ReaderConfig{}, e
	}
	if len(b) != 4 {
		return ReaderConfig{}, fmt.Errorf("invalid reader configuration")
	}
	v := ReaderConfig{int8(b[0]), int8(b[1]), int8(b[2]), int8(b[3])}
	_, e = v.payload()
	return v, e
}
func (c *Client) SetReaderConfig(ctx context.Context, v ReaderConfig) error {
	b, e := v.payload()
	if e != nil {
		return e
	}
	return c.set(ctx, CmdHf14aSetConfig, b...)
}
func (s *ReaderSession) fieldCleanup(err *error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.c.timeout)
	defer cancel()
	_, e := s.expect(ctx, CmdHf14aSetFieldOff, nil, 0x68)
	*err = errors.Join(*err, e)
}
func (c *Client) AuthenticateULC(ctx context.Context, key []byte) error {
	if len(key) != 16 {
		return fmt.Errorf("UL-C needs 16-byte 3DES key in cipher order")
	}
	return c.Reader(ctx, func(s *ReaderSession) error { _, e := s.expect(ctx, CmdMf0UlcAuth, key, 0); return e })
}
func (c *Client) ReadULC(ctx context.Context, key []byte, page, count int) (out HexBytes, err error) {
	if len(key) != 16 || page < 0 || page > 47 || count < 1 || count > 48 || count > 48-page {
		return nil, fmt.Errorf("UL-C key must be 16 bytes and pages fit 0..47")
	}
	err = c.Reader(ctx, func(s *ReaderSession) error {
		p := append(append([]byte(nil), key...), byte(page), byte(count))
		b, e := s.expect(ctx, CmdMf0UlcRead, p, 0)
		if e != nil {
			return e
		}
		if len(b) != count*4 {
			return fmt.Errorf("UL-C returned partial data: %d/%d bytes", len(b), count*4)
		}
		out = HexBytes(b)
		return nil
	})
	return
}
func (c *Client) WriteULCPage(ctx context.Context, key []byte, page int, data []byte) error {
	if len(key) != 16 || page < 0 || page > 47 || len(data) != 4 {
		return fmt.Errorf("UL-C key/page/data length invalid")
	}
	p := append(append([]byte(nil), key...), byte(page))
	p = append(p, data...)
	return c.Reader(ctx, func(s *ReaderSession) error { _, e := s.expect(ctx, CmdMf0UlcWrite, p, 0); return e })
}
func (c *Client) SetULCKey(ctx context.Context, oldKey, newKey []byte) error {
	if len(oldKey) != 16 || len(newKey) != 16 {
		return fmt.Errorf("old/new UL-C keys must have 16 bytes")
	}
	p := append(append([]byte(nil), oldKey...), newKey...)
	return c.Reader(ctx, func(s *ReaderSession) error { _, e := s.expect(ctx, CmdMf0UlcSetKey, p, 0); return e })
}
func type2Password(ctx context.Context, s *ReaderSession, password []byte) (HexBytes, error) {
	if len(password) != 4 {
		return nil, fmt.Errorf("Type2 password must be four bytes")
	}
	p := append([]byte{0x1b}, password...)
	b, e := s.Raw14A(ctx, p, RawOptions{ActivateField: true, WaitResponse: true, AppendCRC: true, AutoSelect: true, KeepField: true, CheckResponseCRC: true, TimeoutMS: 500})
	if e != nil {
		return nil, e
	}
	if len(b) != 2 {
		return nil, fmt.Errorf("PWD_AUTH did not return a two-byte PACK")
	}
	return b, nil
}
func (c *Client) ReadType2Authenticated(ctx context.Context, password []byte, page, count int) (out HexBytes, pack HexBytes, err error) {
	if len(password) != 4 || page < 0 || page > 255 || count < 1 || count > 256 || count > 256-page {
		return nil, nil, fmt.Errorf("password/page range invalid")
	}
	err = c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		pack, e = type2Password(ctx, s, password)
		if e != nil {
			return e
		}
		for p := page; p < page+count; {
			base := p / 4 * 4
			b, e := s.Raw14A(ctx, []byte{0x30, byte(base)}, RawOptions{WaitResponse: true, AppendCRC: true, KeepField: true, CheckResponseCRC: true, TimeoutMS: 500})
			if e != nil {
				return e
			}
			if len(b) != 16 {
				return fmt.Errorf("short authenticated page read")
			}
			n := min(4-p%4, page+count-p)
			out = append(out, b[(p%4)*4:(p%4+n)*4]...)
			p += n
		}
		return nil
	})
	if err != nil {
		out = nil
	}
	return
}
func (c *Client) WriteType2Authenticated(ctx context.Context, password []byte, page int, data []byte) (err error) {
	if len(password) != 4 || page < 0 || page > 255 || len(data) != 4 {
		return fmt.Errorf("password/page/data length invalid")
	}
	return c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		if _, e = type2Password(ctx, s, password); e != nil {
			return e
		}
		b, e := s.Raw14A(ctx, append([]byte{0xa2, byte(page)}, data...), RawOptions{WaitResponse: true, AppendCRC: true, KeepField: true, TimeoutMS: 500})
		if e != nil {
			return e
		}
		if len(b) != 1 || b[0]&15 != 10 {
			return fmt.Errorf("Type2 write was not acknowledged")
		}
		return nil
	})
}
func (c *Client) AuthTrace(ctx context.Context, block int, kind KeyType, key []byte, duration time.Duration) (out []TraceFrame, err error) {
	p, e := keyPayload(block, kind, key)
	if e != nil {
		return nil, e
	}
	ms := duration / time.Millisecond
	if ms < 1 || ms > 30000 || c.timeout < duration+2*time.Second {
		return nil, fmt.Errorf("duration must be 1ms..30s with timeout at least duration+2s")
	}
	p = append(p, byte(ms>>8), byte(ms))
	err = c.Reader(ctx, func(s *ReaderSession) error {
		b, e := s.expect(ctx, CmdHf14aAuthTrace, p, 0)
		if e != nil {
			return e
		}
		out, e = ParseTrace(b)
		return e
	})
	return
}

type LFSamples struct {
	RateHz      int      `json:"rate_hz"`
	Data        HexBytes `json:"data"`
	RequestedMS int      `json:"requested_ms"`
}

func (c *Client) CaptureLF(ctx context.Context, duration time.Duration) (out LFSamples, err error) {
	ms := duration / time.Millisecond
	if ms < 1 || ms > 10000 || c.timeout < duration+time.Second {
		return out, fmt.Errorf("LF capture requires 1ms..10s and timeout >= duration+1s")
	}
	err = c.Reader(ctx, func(s *ReaderSession) error {
		b, e := s.expect(ctx, CmdLfSniff, []byte{byte(ms >> 8), byte(ms)}, 0x40)
		if e != nil {
			var status *StatusError
			if errors.As(e, &status) && status.Status == 0x41 {
				return ErrNoCapture
			}
			return e
		}
		if len(b) == 0 {
			return ErrNoCapture
		}
		out = LFSamples{125000, HexBytes(b), int(ms)}
		return nil
	})
	return
}
func (c *Client) ReadLFADC(ctx context.Context) (out HexBytes, err error) {
	err = c.Reader(ctx, func(s *ReaderSession) error { b, e := s.expect(ctx, CmdAdcGenericRead, nil, 0x40); out = b; return e })
	return
}

type IOProxID struct {
	Version  byte     `json:"version"`
	Facility byte     `json:"facility"`
	Number   uint16   `json:"number"`
	Raw      HexBytes `json:"raw"`
	Encoded  HexBytes `json:"encoded"`
}

func parseIOProx(b []byte) (IOProxID, error) {
	if len(b) != 16 {
		return IOProxID{}, fmt.Errorf("ioProx data requires 16 bytes")
	}
	return IOProxID{b[0], b[1], binary.BigEndian.Uint16(b[2:]), append(HexBytes(nil), b[4:12]...), append(HexBytes(nil), b...)}, nil
}
func (c *Client) ComposeIOProx(ctx context.Context, version, facility byte, number uint16) (IOProxID, error) {
	b, e := c.request(ctx, CmdIoproxComposeId, []byte{version, facility, byte(number >> 8), byte(number)}, 0x68)
	if e != nil {
		return IOProxID{}, e
	}
	return parseIOProx(b)
}
func (c *Client) DecodeIOProx(ctx context.Context, raw []byte) (IOProxID, error) {
	if len(raw) != 8 {
		return IOProxID{}, fmt.Errorf("ioProx raw ID needs eight bytes")
	}
	b, e := c.request(ctx, CmdIoproxDecodeRaw, raw, 0x68)
	if e != nil {
		return IOProxID{}, e
	}
	return parseIOProx(b)
}

// Type2Info reads standard Type2 metadata without guessing passwords or keys.
func (c *Client) Type2Info(ctx context.Context, kind string, index byte) (out HexBytes, err error) {
	var command []byte
	length := 0
	switch kind {
	case "version":
		command = []byte{0x60}
		length = 8
	case "signature":
		command = []byte{0x3c, 0}
		length = 32
	case "counter":
		if index > 2 {
			return nil, fmt.Errorf("counter index must be 0..2")
		}
		command = []byte{0x39, index}
		length = 3
	case "authnonce":
		command = []byte{0x1a, 0}
		length = 9
	default:
		return nil, fmt.Errorf("Type2 info kind: version,signature,counter,authnonce")
	}
	err = c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		out, e = s.Raw14A(ctx, command, RawOptions{ActivateField: true, WaitResponse: true, AppendCRC: true, AutoSelect: true, KeepField: true, CheckResponseCRC: true, TimeoutMS: 500})
		if e != nil {
			return e
		}
		if len(out) != length {
			return fmt.Errorf("%s returned %d bytes; expected %d", kind, len(out), length)
		}
		return nil
	})
	return
}

type HFCardInspection struct {
	Cards   []Card `json:"cards"`
	Classic *bool  `json:"classic_supported,omitempty"`
	PRNG    *byte  `json:"prng_type,omitempty"`
}

func (c *Client) InspectHFCard(ctx context.Context) (out HFCardInspection, err error) {
	err = c.Reader(ctx, func(s *ReaderSession) error {
		b, e := s.expect(ctx, CmdHf14aScan, nil, 0)
		if e != nil {
			return e
		}
		out.Cards, e = parseCards(b)
		if e != nil {
			return e
		}
		if len(out.Cards) != 1 {
			return nil
		}
		r, e := s.Do(ctx, CmdMf1DetectSupport, nil)
		if e != nil {
			return e
		}
		if r.Status == 1 {
			return ErrNoCard
		}
		if r.Status != 0 && r.Status != 2 && r.Status != 6 {
			return r.Check(0)
		}
		supported := r.Status == 0
		out.Classic = &supported
		if supported {
			b, e = s.expect(ctx, CmdMf1DetectPrng, nil, 0)
			if e != nil {
				return e
			}
			if len(b) != 1 || b[0] > 2 {
				return fmt.Errorf("invalid Classic PRNG result")
			}
			v := b[0]
			out.PRNG = &v
		}
		return ensureUID(ctx, s, out.Cards[0].UID)
	})
	return
}
