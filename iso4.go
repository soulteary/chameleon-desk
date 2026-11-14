package chameleon

import (
	"context"
	"fmt"
	"sync"
)

// ISO4Session owns a field-preserving T=CL conversation. It supports short
// command APDUs, card I-block chaining, bounded WTX, and sequence verification.
// Do not retain it or call Client methods inside its callback.
type ISO4Session struct {
	s          *ReaderSession
	Card       Card
	mu         sync.Mutex
	closed     bool
	tx, rx     byte
	maxPayload int
}

func (c *Client) ISO4(ctx context.Context, fn func(*ISO4Session) error) (err error) {
	return c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		raw, e := s.expect(ctx, CmdHf14aScanKeep, nil, 0)
		if e != nil {
			return e
		}
		cards, e := parseCards(raw)
		if e != nil {
			return e
		}
		if len(cards) != 1 || len(cards[0].ATS) == 0 {
			return fmt.Errorf("exactly one ISO14443-4 card with ATS required")
		}
		ats := cards[0].ATS
		if len(ats) < 2 || int(ats[0]) != len(ats) || int(ats[1]&15) > 8 {
			return fmt.Errorf("invalid ATS/FSCI")
		}
		fsc := []int{16, 24, 32, 40, 48, 64, 96, 128, 256}[ats[1]&15]
		session := &ISO4Session{s: s, Card: cards[0], maxPayload: min(60, fsc-3)}
		defer func() { session.mu.Lock(); session.closed = true; session.mu.Unlock() }()
		return fn(session)
	})
}
func (s *ISO4Session) Exchange(ctx context.Context, apdu []byte) (HexBytes, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if len(apdu) < 4 || len(apdu) > s.maxPayload {
		return nil, fmt.Errorf("ISO4 APDU exceeds short-frame/card FSC capacity (outbound chaining is not supported)")
	}
	frame := append([]byte{2 | s.tx}, apdu...)
	out := HexBytes{}
	for exchanges := 0; exchanges < 64; exchanges++ {
		b, e := s.s.Raw14A(ctx, frame, RawOptions{WaitResponse: true, AppendCRC: true, KeepField: true, CheckResponseCRC: true, TimeoutMS: 1000})
		if e != nil {
			return nil, e
		}
		if len(b) < 1 {
			return nil, fmt.Errorf("empty T=CL frame")
		}
		pcb := b[0]
		payload := b[1:]
		if pcb&8 != 0 {
			if len(payload) < 1 || payload[0] != 0 {
				return nil, fmt.Errorf("unsupported T=CL CID")
			}
			payload = payload[1:]
		}
		if pcb&0xc0 == 0xc0 {
			if pcb&0xf7 != 0xf2 || len(payload) != 1 || payload[0] < 1 || payload[0] > 59 {
				return nil, fmt.Errorf("invalid/unsupported T=CL S-block")
			}
			frame = append([]byte{pcb}, b[1:]...)
			continue
		}
		if pcb&0xe2 != 2 || pcb&4 != 0 || pcb&1 != s.rx {
			return nil, fmt.Errorf("unexpected T=CL PCB/sequence: %02x", pcb)
		}
		if len(out)+len(payload) > 4096 {
			return nil, fmt.Errorf("chained APDU exceeds 4096 bytes")
		}
		out = append(out, payload...)
		s.rx ^= 1
		if pcb&0x10 != 0 {
			frame = []byte{0xa2 | s.rx}
			continue
		}
		s.tx ^= 1
		if len(out) < 2 {
			return nil, fmt.Errorf("APDU response lacks status word")
		}
		return out, nil
	}
	return nil, fmt.Errorf("T=CL frame/WTX limit reached")
}
