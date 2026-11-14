package chameleon

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

var ErrHardnestedRequired = errors.New("card requires hardnested; export encrypted nonces with the wire API and use the hardnested tool")

type NestedSample struct {
	Tool        string   `json:"tool"`
	UID         uint32   `json:"uid"`
	TargetBlock int      `json:"target_block"`
	TargetType  KeyType  `json:"target_type"`
	Arguments   []string `json:"arguments"`
}
type VerifiedRecovery struct {
	Sample     NestedSample   `json:"sample"`
	Tool       RecoveryResult `json:"tool_output"`
	Candidates int            `json:"candidates"`
	Key        HexBytes       `json:"verified_key,omitempty"`
}

func decimal(n uint32) string { return strconv.FormatUint(uint64(n), 10) }
func nestedSample(kind byte, dist, nonces []byte, target int, kt KeyType) (NestedSample, error) {
	v := NestedSample{TargetBlock: target, TargetType: kt}
	switch kind {
	case 0:
		if len(nonces) < 12 || (len(nonces)-4)%8 != 0 {
			return v, fmt.Errorf("invalid staticnested nonce response")
		}
		v.Tool = "staticnested"
		v.UID = binary.BigEndian.Uint32(nonces)
		v.Arguments = []string{decimal(v.UID), decimal(uint32(kt))}
		for p := 4; p < len(nonces); p += 4 {
			v.Arguments = append(v.Arguments, decimal(binary.BigEndian.Uint32(nonces[p:])))
		}
	case 1:
		if len(dist) != 8 || len(nonces) < 9 || len(nonces)%9 != 0 {
			return v, fmt.Errorf("invalid nested nonce/distance response")
		}
		v.Tool = "nested"
		v.UID = binary.BigEndian.Uint32(dist)
		v.Arguments = []string{decimal(v.UID), decimal(binary.BigEndian.Uint32(dist[4:]))}
		for p := 0; p < len(nonces); p += 9 {
			v.Arguments = append(v.Arguments, decimal(binary.BigEndian.Uint32(nonces[p:])), decimal(binary.BigEndian.Uint32(nonces[p+4:])), decimal(uint32(nonces[p+8])))
		}
	default:
		return v, ErrHardnestedRequired
	}
	return v, validateRecoveryArgs(v.Tool, v.Arguments)
}

// CollectNested performs bounded RF acquisition and restores the original mode.
// It writes no card blocks. Only weak/static PRNG paths are automated here.
func (c *Client) CollectNested(ctx context.Context, knownBlock int, knownType KeyType, key []byte, targetBlock int, targetType KeyType) (out NestedSample, err error) {
	p, e := keyPayload(knownBlock, knownType, key)
	if e != nil {
		return out, e
	}
	if _, e = keyPayload(targetBlock, targetType, key); e != nil {
		return out, e
	}
	payload := append(p, byte(targetType), byte(targetBlock))
	err = c.Reader(ctx, func(s *ReaderSession) error {
		level, e := s.expect(ctx, CmdMf1DetectPrng, nil, 0)
		if e != nil {
			return e
		}
		if len(level) != 1 {
			return fmt.Errorf("invalid PRNG response")
		}
		var dist, nonces []byte
		switch level[0] {
		case 0:
			nonces, e = s.expect(ctx, CmdMf1StaticNestedAcquire, payload, 0)
		case 1:
			dist, e = s.expect(ctx, CmdMf1DetectNtDist, p, 0)
			if e == nil {
				nonces, e = s.expect(ctx, CmdMf1NestedAcquire, payload, 0)
			}
		default:
			return ErrHardnestedRequired
		}
		if e != nil {
			return e
		}
		out, e = nestedSample(level[0], dist, nonces, targetBlock, targetType)
		return e
	})
	return
}

var candidatePattern = regexp.MustCompile(`(?im)^Key [0-9]+\.\.\. ([a-f0-9]{12})\s*$`)

func recoveryCandidates(stdout string) []HexBytes {
	seen := map[string]bool{}
	out := []HexBytes{}
	for _, m := range candidatePattern.FindAllStringSubmatch(stdout, -1) {
		b, _ := hex.DecodeString(m[1])
		id := hex.EncodeToString(b)
		if !seen[id] {
			seen[id] = true
			out = append(out, b)
		}
	}
	return out
}

// RecoverNested releases the device during CPU recovery, then authenticates every
// candidate against the same UID. A tool-reported candidate alone is not success.
func (c *Client) RecoverNested(ctx context.Context, tools string, limit time.Duration, knownBlock int, knownType KeyType, key []byte, targetBlock int, targetType KeyType) (out VerifiedRecovery, err error) {
	if limit <= 0 {
		return out, fmt.Errorf("recovery limit must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	out.Sample, err = c.CollectNested(ctx, knownBlock, knownType, key, targetBlock, targetType)
	if err != nil {
		return
	}
	out.Tool, err = RunRecovery(ctx, tools, out.Sample.Tool, out.Sample.Arguments...)
	if err != nil {
		return
	}
	candidates := recoveryCandidates(out.Tool.Stdout)
	out.Candidates = len(candidates)
	for _, candidate := range candidates {
		verified := false
		err = c.Reader(ctx, func(s *ReaderSession) error {
			raw, e := s.expect(ctx, CmdHf14aScan, nil, 0)
			if e != nil {
				return e
			}
			cards, e := parseCards(raw)
			if e != nil {
				return e
			}
			if len(cards) != 1 || len(cards[0].UID) < 4 || binary.BigEndian.Uint32(cards[0].UID[len(cards[0].UID)-4:]) != out.Sample.UID {
				return fmt.Errorf("card changed during recovery")
			}
			p, _ := keyPayload(targetBlock, targetType, candidate)
			_, e = s.expect(ctx, CmdMf1AuthOneKeyBlock, p, 0)
			var status *StatusError
			if errors.As(e, &status) && status.Status == 6 {
				return nil
			}
			if e == nil {
				verified = true
			}
			return e
		})
		if err != nil {
			return
		}
		if verified {
			out.Key = candidate
			return
		}
	}
	return out, fmt.Errorf("no candidate passed on-card authentication")
}
