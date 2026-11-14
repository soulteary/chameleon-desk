package chameleon

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

func (c *Client) ReaderAPDU(ctx context.Context, apdu []byte) (out HexBytes, err error) {
	if len(apdu) == 0 || len(apdu) > 61 {
		return nil, fmt.Errorf("pinned firmware accepts APDU payloads of 1..61 bytes")
	}
	err = c.Reader(ctx, func(s *ReaderSession) error {
		b, e := s.expect(ctx, CmdHf14a4ReaderApdu, apdu, 0)
		out = HexBytes(b)
		return e
	})
	return
}
func (c *Client) SetISO144434Identity(ctx context.Context, card Card) error {
	p, e := antiCollisionPayload(card)
	if e != nil {
		return e
	}
	return c.set(ctx, CmdHf14a4SetAntiColl, p...)
}
func (c *Client) SendAPDUResponse(ctx context.Context, data []byte) error {
	if len(data) > 260 {
		return fmt.Errorf("APDU response too large")
	}
	p := []byte{byte(len(data) >> 8), byte(len(data))}
	return c.set(ctx, CmdHf14a4ApduSend, append(p, data...)...)
}
func (c *Client) AddStaticAPDU(ctx context.Context, command, response []byte) error {
	if len(command) == 0 || len(command) > 16 || len(response) > 253 {
		return fmt.Errorf("invalid static APDU lengths")
	}
	p := append([]byte{byte(len(command))}, command...)
	p = append(p, byte(len(response)>>8), byte(len(response)))
	return c.set(ctx, CmdHf14a4StaticResp, append(p, response...)...)
}
func (c *Client) ClearStaticAPDUs(ctx context.Context) error {
	return c.set(ctx, CmdHf14a4StaticResp, 0)
}
func (c *Client) SetType2Version(ctx context.Context, data []byte) error {
	if len(data) != 8 {
		return fmt.Errorf("version requires 8 bytes")
	}
	return c.set(ctx, CmdMf0NtagSetVersionData, data...)
}
func (c *Client) SetType2Signature(ctx context.Context, data []byte) error {
	if len(data) != 32 {
		return fmt.Errorf("signature requires 32 bytes")
	}
	return c.set(ctx, CmdMf0NtagSetSignatureData, data...)
}
func (c *Client) SetEmulatorPRNG(ctx context.Context, kind byte) error {
	if kind > 2 {
		return fmt.Errorf("PRNG must be static=0, weak=1 or hard=2")
	}
	return c.set(ctx, CmdMf1SetPrngType, kind)
}

type AuthLog struct {
	Block   byte     `json:"block"`
	KeyType string   `json:"key_type"`
	Nested  bool     `json:"nested"`
	UID     HexBytes `json:"uid"`
	NT      HexBytes `json:"nt"`
	NR      HexBytes `json:"nr"`
	AR      HexBytes `json:"ar"`
}

func (c *Client) ClassicDetectionLog(ctx context.Context, start uint32) ([]AuthLog, error) {
	p := make([]byte, 4)
	binary.BigEndian.PutUint32(p, start)
	b, e := c.request(ctx, CmdMf1GetDetectionLog, p, 0x68)
	if e != nil {
		return nil, e
	}
	if len(b)%18 != 0 {
		return nil, fmt.Errorf("truncated auth log")
	}
	out := make([]AuthLog, 0, len(b)/18)
	for len(b) > 0 {
		kind := "A"
		if b[1]&1 != 0 {
			kind = "B"
		}
		out = append(out, AuthLog{b[0], kind, b[1]&2 != 0, append(HexBytes(nil), b[2:6]...), append(HexBytes(nil), b[6:10]...), append(HexBytes(nil), b[10:14]...), append(HexBytes(nil), b[14:18]...)})
		b = b[18:]
	}
	return out, nil
}

var ErrNoCapture = errors.New("no frames captured during sniff interval")

type TraceFrame struct {
	FromCard bool     `json:"from_card"`
	Bits     uint16   `json:"bits"`
	Data     HexBytes `json:"data"`
}

func ParseTrace(data []byte) ([]TraceFrame, error) {
	out := []TraceFrame{}
	for len(data) > 0 {
		if len(data) < 2 {
			return nil, fmt.Errorf("truncated trace header")
		}
		header := binary.BigEndian.Uint16(data)
		bits := header & 0x7fff
		n := (int(bits) + 7) / 8
		data = data[2:]
		if len(data) < n {
			return nil, fmt.Errorf("truncated trace payload")
		}
		out = append(out, TraceFrame{header&0x8000 != 0, bits, append(HexBytes(nil), data[:n]...)})
		data = data[n:]
	}
	return out, nil
}

// SniffHF uses the emulator stack. The pinned firmware reloads active slot
// data from storage; save/export unsaved slot data before calling it.
func (c *Client) SniffHF(ctx context.Context, milliseconds uint16) (out []TraceFrame, err error) {
	if milliseconds < 1 || milliseconds > 30000 {
		return nil, fmt.Errorf("sniff duration must be 1..30000ms")
	}
	if c.timeout < time.Duration(milliseconds)*time.Millisecond+time.Second {
		return nil, fmt.Errorf("connection timeout must exceed sniff duration by at least 1s")
	}
	err = c.Emulator(ctx, func(s *ReaderSession) error {
		b, e := s.expect(ctx, CmdHf14aSniff, []byte{byte(milliseconds >> 8), byte(milliseconds)}, 0x68)
		if e != nil {
			var status *StatusError
			if errors.As(e, &status) && status.Status == 1 {
				return ErrNoCapture
			}
			return e
		}
		out, e = ParseTrace(b)
		return e
	})
	return
}
