package chameleon

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/bits"
	"os"
	"regexp"
	"strconv"
	"time"
)

type AdvancedRecoveryOptions struct {
	Strategy             string
	ToolsDir             string
	Limit                time.Duration
	MaxRuns, MaxAttempts int
	Slow                 bool
	SyncMax              byte
}
type AdvancedRecoveryResult struct {
	Strategy   string         `json:"strategy"`
	UID        HexBytes       `json:"uid"`
	Key        HexBytes       `json:"verified_key,omitempty"`
	Attempts   int            `json:"attempts"`
	NonceBytes int            `json:"nonce_bytes"`
	Tool       RecoveryResult `json:"tool_output"`
}
type HardnestedCollector struct {
	seen        [256]bool
	Unique, Sum int
	Data        HexBytes
}

func (h *HardnestedCollector) Add(raw []byte) error {
	if len(raw)%9 != 0 {
		return fmt.Errorf("hardnested response is not nonce pairs")
	}
	if len(h.Data)+len(raw) > 4<<20 {
		return fmt.Errorf("nonce capture exceeds 4MiB")
	}
	for p := 0; p < len(raw); p += 9 {
		nt := binary.BigEndian.Uint32(raw[p+4:])
		msb := byte(nt >> 24)
		if !h.seen[msb] {
			h.seen[msb] = true
			h.Unique++
			h.Sum += bits.OnesCount32((nt&0xff000000)|uint32(raw[p+8]&8)) % 2
		}
	}
	h.Data = append(h.Data, raw...)
	return nil
}
func (h *HardnestedCollector) Ready() bool {
	if h.Unique != 256 {
		return false
	}
	for _, n := range []int{0, 32, 56, 64, 80, 96, 104, 112, 120, 128, 136, 144, 152, 160, 176, 192, 200, 224, 256} {
		if h.Sum == n {
			return true
		}
	}
	return false
}
func (h *HardnestedCollector) File(uid []byte, block int, kind KeyType) ([]byte, error) {
	if !h.Ready() {
		return nil, fmt.Errorf("nonce capture incomplete or parity sum invalid")
	}
	if _, e := keyPayload(block, kind, make([]byte, 6)); e != nil {
		return nil, e
	}
	if len(uid) != 4 && len(uid) != 7 && len(uid) != 10 {
		return nil, fmt.Errorf("invalid UID")
	}
	out := append([]byte(nil), uid[len(uid)-4:]...)
	out = append(out, byte(block), byte(kind)&1)
	return append(out, h.Data...), nil
}
func scanSession(ctx context.Context, s *ReaderSession) (HexBytes, error) {
	b, e := s.expect(ctx, CmdHf14aScan, nil, 0)
	if e != nil {
		var status *StatusError
		if errors.As(e, &status) && status.Status == 1 {
			return nil, ErrNoCard
		}
		return nil, e
	}
	cards, e := parseCards(b)
	if e != nil {
		return nil, e
	}
	if len(cards) != 1 {
		return nil, fmt.Errorf("exactly one card required")
	}
	return append(HexBytes(nil), cards[0].UID...), nil
}
func (c *Client) verifyRecovery(ctx context.Context, uid []byte, block int, kind KeyType, candidates []HexBytes) (HexBytes, error) {
	var key HexBytes
	err := c.Reader(ctx, func(s *ReaderSession) error {
		for _, candidate := range candidates {
			current, e := scanSession(ctx, s)
			if e != nil {
				return e
			}
			if !bytes.Equal(current, uid) {
				return fmt.Errorf("card changed during recovery")
			}
			p, e := keyPayload(block, kind, candidate)
			if e != nil {
				return e
			}
			_, e = s.expect(ctx, CmdMf1AuthOneKeyBlock, p, 0)
			var status *StatusError
			if errors.As(e, &status) && status.Status == 6 {
				continue
			}
			if e != nil {
				return e
			}
			key = candidate
			return nil
		}
		return nil
	})
	return key, err
}

var advancedCandidatePattern = regexp.MustCompile(`(?im)^(?:Key found: |Key[0-9]+: )([a-f0-9]{12})\s*$`)

func advancedCandidates(out string) []HexBytes {
	v := recoveryCandidates(out)
	for _, m := range advancedCandidatePattern.FindAllStringSubmatch(out, -1) {
		b, _ := hex.DecodeString(m[1])
		v = append(v, b)
	}
	return v
}

// RecoverClassic automates weak/static Nested, Hardnested or Darkside. It never
// writes card memory. Every recovered key must pass same-full-UID authentication.
func (c *Client) RecoverClassic(ctx context.Context, knownBlock int, knownType KeyType, knownKey []byte, targetBlock int, targetType KeyType, o AdvancedRecoveryOptions) (out AdvancedRecoveryResult, err error) {
	if o.Limit <= 0 || o.MaxRuns < 1 || o.MaxRuns > 1000 || o.MaxAttempts < 1 || o.MaxAttempts > 10 {
		return out, fmt.Errorf("positive time limit, 1..1000 runs and 1..10 attempts required")
	}
	if _, e := keyPayload(targetBlock, targetType, make([]byte, 6)); e != nil {
		return out, e
	}
	if o.Strategy == "" {
		o.Strategy = "auto"
	}
	if o.Strategy != "auto" && o.Strategy != "nested" && o.Strategy != "hardnested" && o.Strategy != "darkside" {
		return out, fmt.Errorf("unknown recovery strategy")
	}
	if o.Strategy != "darkside" {
		if _, e := keyPayload(knownBlock, knownType, knownKey); e != nil {
			return out, e
		}
	}
	ctx, cancel := context.WithTimeout(ctx, o.Limit)
	defer cancel()
	err = c.Reader(ctx, func(s *ReaderSession) error { var e error; out.UID, e = scanSession(ctx, s); return e })
	if err != nil {
		return
	}
	if o.Strategy == "auto" || o.Strategy == "nested" {
		v, e := c.RecoverNested(ctx, o.ToolsDir, o.Limit, knownBlock, knownType, knownKey, targetBlock, targetType)
		if e == nil {
			out.Strategy = v.Sample.Tool
			out.Tool = v.Tool
			out.Key, err = c.verifyRecovery(ctx, out.UID, targetBlock, targetType, []HexBytes{v.Key})
			return
		}
		if o.Strategy == "nested" || !errors.Is(e, ErrHardnestedRequired) {
			return out, e
		}
		o.Strategy = "hardnested"
	}
	out.Strategy = o.Strategy
	if o.Strategy == "hardnested" {
		for attempt := 1; attempt <= o.MaxAttempts; attempt++ {
			out.Attempts = attempt
			h := HardnestedCollector{}
			err = c.Reader(ctx, func(s *ReaderSession) error {
				p, _ := keyPayload(knownBlock, knownType, knownKey)
				p = append([]byte{boolean(o.Slow)}, p...)
				p = append(p, byte(targetType), byte(targetBlock))
				for run := 0; run < o.MaxRuns; run++ {
					uid, e := scanSession(ctx, s)
					if e != nil {
						return e
					}
					if !bytes.Equal(uid, out.UID) {
						return fmt.Errorf("card changed during acquisition")
					}
					raw, e := s.expect(ctx, CmdMf1HardnestedAcquire, p, 0)
					if e != nil {
						return e
					}
					if e = h.Add(raw); e != nil {
						return e
					}
					if h.Unique == 256 {
						break
					}
				}
				return nil
			})
			if err != nil {
				return
			}
			if !h.Ready() {
				continue
			}
			data, e := h.File(out.UID, targetBlock, targetType)
			if e != nil {
				return out, e
			}
			out.NonceBytes = len(data)
			f, e := os.CreateTemp("", "chameleon-hardnested-*.bin")
			if e != nil {
				return out, e
			}
			path := f.Name()
			if _, e = f.Write(data); e != nil {
				_ = f.Close()
				_ = os.Remove(path)
				return out, e
			}
			if e = f.Close(); e != nil {
				_ = os.Remove(path)
				return out, e
			}
			out.Tool, err = RunRecovery(ctx, o.ToolsDir, "hardnested", path)
			_ = os.Remove(path)
			if err != nil {
				return
			}
			out.Key, err = c.verifyRecovery(ctx, out.UID, targetBlock, targetType, advancedCandidates(out.Tool.Stdout))
			if err != nil {
				return
			}
			if len(out.Key) > 0 {
				return out, nil
			}
		}
		return out, fmt.Errorf("hardnested acquisition/recovery exhausted bounded attempts")
	}
	args := []string{decimal(binary.BigEndian.Uint32(out.UID[len(out.UID)-4:]))}
	if o.SyncMax == 0 {
		o.SyncMax = 3
	}
	for attempt := 1; attempt <= o.MaxAttempts; attempt++ {
		out.Attempts = attempt
		var raw []byte
		err = c.Reader(ctx, func(s *ReaderSession) error {
			uid, e := scanSession(ctx, s)
			if e != nil {
				return e
			}
			if !bytes.Equal(uid, out.UID) {
				return fmt.Errorf("card changed during darkside")
			}
			raw, e = s.expect(ctx, CmdMf1DarksideAcquire, []byte{byte(targetType), byte(targetBlock), boolean(attempt == 1), o.SyncMax}, 0)
			return e
		})
		if err != nil {
			return
		}
		if len(raw) != 33 || raw[0] != 0 {
			return out, fmt.Errorf("darkside acquisition rejected or malformed response")
		}
		if binary.BigEndian.Uint32(raw[1:]) != binary.BigEndian.Uint32(out.UID[len(out.UID)-4:]) {
			return out, fmt.Errorf("darkside UID mismatch")
		}
		args = append(args, decimal(binary.BigEndian.Uint32(raw[5:])), strconv.FormatUint(binary.BigEndian.Uint64(raw[17:]), 10), strconv.FormatUint(binary.BigEndian.Uint64(raw[9:]), 10), decimal(binary.BigEndian.Uint32(raw[25:])), decimal(binary.BigEndian.Uint32(raw[29:])))
		out.Tool, err = RunRecovery(ctx, o.ToolsDir, "darkside", args...)
		if err != nil {
			return
		}
		out.Key, err = c.verifyRecovery(ctx, out.UID, targetBlock, targetType, advancedCandidates(out.Tool.Stdout))
		if err != nil {
			return
		}
		if len(out.Key) > 0 {
			return out, nil
		}
	}
	return out, fmt.Errorf("no darkside candidate authenticated within attempt limit")
}
