package chameleon

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

type APDUPair struct {
	Command    HexBytes `json:"command"`
	Response   HexBytes `json:"response"`
	StatusWord uint16   `json:"status_word"`
}
type EMVScan struct {
	Card  Card       `json:"card"`
	Pairs []APDUPair `json:"pairs"`
}

func ParseEMVScan(b []byte) (EMVScan, error) {
	var out EMVScan
	if len(b) < 1 {
		return out, fmt.Errorf("empty EMV response")
	}
	n := int(b[0])
	if n != 4 && n != 7 && n != 10 {
		return out, fmt.Errorf("invalid EMV UID length")
	}
	if len(b) < n+5 {
		return out, fmt.Errorf("truncated EMV identity")
	}
	identity := 1 + n + 4 + int(b[n+4])
	if len(b) < identity+1 {
		return out, fmt.Errorf("truncated EMV ATS/count")
	}
	cards, e := parseCards(b[:identity])
	if e != nil || len(cards) != 1 {
		return out, fmt.Errorf("invalid EMV identity: %v", e)
	}
	out.Card = cards[0]
	count := int(b[identity])
	b = b[identity+1:]
	for i := 0; i < count; i++ {
		if len(b) < 1 {
			return out, fmt.Errorf("truncated APDU command length")
		}
		n := int(b[0])
		b = b[1:]
		if n < 4 || len(b) < n+2 {
			return out, fmt.Errorf("truncated APDU command")
		}
		command := append(HexBytes(nil), b[:n]...)
		size := int(binary.LittleEndian.Uint16(b[n:]))
		b = b[n+2:]
		if size < 2 || len(b) < size {
			return out, fmt.Errorf("truncated APDU response/status")
		}
		response := append(HexBytes(nil), b[:size]...)
		out.Pairs = append(out.Pairs, APDUPair{command, response, binary.BigEndian.Uint16(response[len(response)-2:])})
		b = b[size:]
	}
	if len(b) != 0 || count == 0 {
		return out, fmt.Errorf("unexpected EMV trailing bytes or no APDU pairs")
	}
	return out, nil
}
func (c *Client) ScanEMV(ctx context.Context) (out EMVScan, err error) {
	if c.timeout < 10*time.Second {
		return out, fmt.Errorf("EMV scan requires command timeout >=10s")
	}
	err = c.Reader(ctx, func(s *ReaderSession) error {
		raw, e := s.expect(ctx, CmdHf14a4EmvScan, nil, 0)
		if e != nil {
			var status *StatusError
			if errors.As(e, &status) && status.Status == 1 {
				return ErrNoCard
			}
			return e
		}
		out, e = ParseEMVScan(raw)
		return e
	})
	return
}

type ISO4Profile struct {
	Card      Card       `json:"card"`
	Responses []APDUPair `json:"responses"`
}

func (p ISO4Profile) Validate() error {
	if len(p.Card.UID) != 4 && len(p.Card.UID) != 7 {
		return fmt.Errorf("ISO4 profile UID must be 4 or 7 bytes")
	}
	if _, e := antiCollisionPayload(p.Card); e != nil {
		return e
	}
	if len(p.Responses) > 12 {
		return fmt.Errorf("static table has at most 12 entries")
	}
	seen := map[string]bool{}
	for _, v := range p.Responses {
		if len(v.Command) < 1 || len(v.Command) > 16 || len(v.Response) > 253 {
			return fmt.Errorf("static APDU command/response exceeds firmware limits")
		}
		key := string(v.Command)
		if seen[key] {
			return fmt.Errorf("duplicate static APDU command")
		}
		seen[key] = true
	}
	return nil
}

// LoadISO4Profile overwrites the active ISO14443-4 static response table. The
// firmware provides no getter for the old table; callers must retain a profile.
func (c *Client) LoadISO4Profile(ctx context.Context, p ISO4Profile) error {
	if e := p.Validate(); e != nil {
		return e
	}
	return c.Emulator(ctx, func(s *ReaderSession) error {
		active, e := s.expect(ctx, CmdGetActiveSlot, nil, 0x68)
		if e != nil {
			return e
		}
		types, e := s.expect(ctx, CmdGetSlotInfo, nil, 0x68)
		if e != nil {
			return e
		}
		if len(active) != 1 || active[0] > 7 || len(types) != 32 || binary.BigEndian.Uint16(types[int(active[0])*4:]) != 3000 {
			return fmt.Errorf("active slot must already be type 3000")
		}
		identity, _ := antiCollisionPayload(p.Card)
		if _, e = s.expect(ctx, CmdHf14a4SetAntiColl, identity, 0x68); e != nil {
			return e
		}
		if _, e = s.expect(ctx, CmdHf14a4StaticResp, []byte{0}, 0x68); e != nil {
			return e
		}
		for _, v := range p.Responses {
			payload := append([]byte{byte(len(v.Command))}, v.Command...)
			payload = append(payload, byte(len(v.Response)>>8), byte(len(v.Response)))
			payload = append(payload, v.Response...)
			if _, e = s.expect(ctx, CmdHf14a4StaticResp, payload, 0x68); e != nil {
				return e
			}
		}
		return nil
	})
}

type TLV struct {
	Tag      HexBytes `json:"tag"`
	Value    HexBytes `json:"value"`
	Children []TLV    `json:"children,omitempty"`
}

func ParseBERTLV(data []byte) ([]TLV, error) {
	if len(data) > 65536 {
		return nil, fmt.Errorf("TLV input exceeds 64KiB")
	}
	nodes := 0
	var parse func([]byte, int) ([]TLV, error)
	parse = func(b []byte, depth int) ([]TLV, error) {
		if depth > 16 {
			return nil, fmt.Errorf("TLV nesting exceeds 16")
		}
		out := []TLV{}
		for len(b) > 0 {
			if b[0] == 0 || b[0] == 255 {
				b = b[1:]
				continue
			}
			nodes++
			if nodes > 4096 {
				return nil, fmt.Errorf("too many TLV nodes")
			}
			tagLen := 1
			if b[0]&31 == 31 {
				for {
					if tagLen >= len(b) || tagLen >= 4 {
						return nil, fmt.Errorf("invalid TLV tag")
					}
					v := b[tagLen]
					tagLen++
					if v&128 == 0 {
						break
					}
				}
			}
			if len(b) <= tagLen {
				return nil, fmt.Errorf("missing TLV length")
			}
			tag := append(HexBytes(nil), b[:tagLen]...)
			constructed := b[0]&32 != 0
			b = b[tagLen:]
			size := int(b[0])
			b = b[1:]
			if size&128 != 0 {
				n := size & 127
				if n == 0 || n > 3 || len(b) < n {
					return nil, fmt.Errorf("indefinite/oversized TLV length")
				}
				size = 0
				for _, v := range b[:n] {
					size = size<<8 | int(v)
				}
				b = b[n:]
			}
			if size > len(b) {
				return nil, fmt.Errorf("truncated TLV value")
			}
			node := TLV{Tag: tag, Value: append(HexBytes(nil), b[:size]...)}
			if constructed {
				var e error
				node.Children, e = parse(b[:size], depth+1)
				if e != nil {
					return nil, e
				}
			}
			out = append(out, node)
			b = b[size:]
		}
		return out, nil
	}
	return parse(data, 0)
}

type ISO4Debug struct {
	Received  byte     `json:"received"`
	Sent      byte     `json:"sent"`
	LastPCB   byte     `json:"last_pcb"`
	LastMatch bool     `json:"last_static_match"`
	Raw       HexBytes `json:"raw"`
}

func (c *Client) ISO4Debug(ctx context.Context) (ISO4Debug, error) {
	b, e := c.request(ctx, CmdHf14a4DebugCounters, nil, 0x68)
	if e != nil {
		return ISO4Debug{}, e
	}
	if len(b) < 4 || b[3] > 1 {
		return ISO4Debug{}, fmt.Errorf("invalid ISO4 debug counters")
	}
	return ISO4Debug{b[0], b[1], b[2], b[3] != 0, b}, nil
}
