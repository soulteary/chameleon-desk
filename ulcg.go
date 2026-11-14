package chameleon

import (
	"context"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type ULCGChallenges struct {
	Schema              int      `json:"schema"`
	UID                 HexBytes `json:"uid"`
	Challenge100        HexBytes `json:"challenge_100"`
	Challenge75         HexBytes `json:"challenge_75"`
	Challenge50         HexBytes `json:"challenge_50"`
	Challenge25         HexBytes `json:"challenge_25"`
	Challenge0          HexBytes `json:"challenge_0"`
	PossiblyErasedPages []int    `json:"possibly_erased_pages"`
}

func (v ULCGChallenges) Validate() error {
	if v.Schema != 1 || len(v.UID) != 4 && len(v.UID) != 7 && len(v.UID) != 10 {
		return fmt.Errorf("invalid ULCG metadata")
	}
	for _, b := range []HexBytes{v.Challenge100, v.Challenge75, v.Challenge50, v.Challenge25, v.Challenge0} {
		if len(b) != 8 {
			return fmt.Errorf("ULCG recovery needs all five eight-byte challenges")
		}
	}
	return nil
}

// CollectULCG is destructive on a vulnerable unlocked ULCG: pages 47..44 are
// overwritten with zeros. It never restores an inferred key automatically.
// progress is mandatory and runs BEFORE each potentially destructive write.
func (c *Client) CollectULCG(ctx context.Context, maxChallenges int, allowErase bool, progress func(ULCGChallenges) error) (out ULCGChallenges, err error) {
	if !allowErase || progress == nil || maxChallenges < 2 || maxChallenges > 10000 {
		return out, fmt.Errorf("ULCG requires explicit key-erasure authorization, durable progress callback and 2..10000 challenges")
	}
	out.Schema = 1
	err = c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		current, e := scanSession(ctx, s)
		if e != nil {
			return e
		}
		out.UID = append(HexBytes(nil), current...)
		raw := func(command []byte, checkCRC bool) (HexBytes, error) {
			return s.Raw14A(ctx, command, RawOptions{ActivateField: true, WaitResponse: true, AppendCRC: true, AutoSelect: true, CheckResponseCRC: checkCRC, TimeoutMS: 500})
		}
		config, e := raw([]byte{0x30, 40}, true)
		if e != nil {
			return e
		}
		if len(config) != 16 || config[8] < 48 || config[1]&0x80 != 0 {
			return fmt.Errorf("ULCG must be unlocked, AUTH0>=48, key lock unset")
		}
		nonce := func() (HexBytes, error) {
			v, e := raw([]byte{0x1a, 0}, true)
			if e != nil {
				return nil, e
			}
			if len(v) != 9 || v[0] != 0xaf {
				return nil, fmt.Errorf("ULCG challenge malformed")
			}
			return append(HexBytes(nil), v[1:]...), nil
		}
		seen := map[string]bool{}
		for i := 0; i < maxChallenges; i++ {
			b, e := nonce()
			if e != nil {
				return e
			}
			if seen[string(b)] {
				out.Challenge100 = b
				break
			}
			seen[string(b)] = true
		}
		if len(out.Challenge100) == 0 {
			return fmt.Errorf("no nonce collision within limit; no key pages written")
		}
		if e = progress(out); e != nil {
			return e
		}
		for i, page := range []int{47, 46, 45, 44} {
			if e = ensureUID(ctx, s, out.UID); e != nil {
				return e
			}
			out.PossiblyErasedPages = append(out.PossiblyErasedPages, page)
			if e = progress(out); e != nil {
				return e
			}
			ack, e := raw([]byte{0xa2, byte(page), 0, 0, 0, 0}, false)
			if e != nil {
				return e
			}
			if len(ack) != 1 || ack[0]&15 != 10 {
				return fmt.Errorf("ULCG page %d write not acknowledged", page)
			}
			b, e := nonce()
			if e != nil {
				return e
			}
			switch i {
			case 0:
				out.Challenge75 = b
			case 1:
				out.Challenge50 = b
			case 2:
				out.Challenge25 = b
			case 3:
				out.Challenge0 = b
			}
			if e = progress(out); e != nil {
				return e
			}
		}
		return out.Validate()
	})
	return
}

type ULCGRecovery struct {
	Candidate      HexBytes `json:"candidate_key"`
	VerifiedOnCard bool     `json:"verified_on_card"`
}

var ulcgKeyPattern = regexp.MustCompile(`(?im)^Full key \(hex\):\s*([a-f0-9]{32})\s*$`)

func RecoverULCG(ctx context.Context, v ULCGChallenges, tools string, threads int, limit time.Duration) (out ULCGRecovery, err error) {
	if e := v.Validate(); e != nil {
		return out, e
	}
	if threads < 1 || threads > 256 || limit <= 0 {
		return out, fmt.Errorf("1..256 threads and positive time limit required")
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	key := make([]byte, 16)
	nonces := map[int]HexBytes{1: v.Challenge25, 0: v.Challenge50, 3: v.Challenge75, 2: v.Challenge100}
	for _, segment := range []int{1, 0, 3, 2} {
		args := []string{"-c", v.Challenge0.String(), nonces[segment].String(), hex.EncodeToString(key), strconv.Itoa(segment + 1), strconv.Itoa(threads)}
		r, e := RunRecovery(ctx, tools, "mfulc_des_brute", args...)
		if e != nil {
			return out, e
		}
		m := ulcgKeyPattern.FindStringSubmatch(r.Stdout)
		if len(m) != 2 || strings.Contains(r.Stdout, "No matching key") {
			return out, fmt.Errorf("ULCG helper did not recover segment %d", segment+1)
		}
		full, e := hex.DecodeString(m[1])
		if e != nil {
			return out, e
		}
		copy(key[segment*4:segment*4+4], full[segment*4:segment*4+4])
	}
	out.Candidate = key
	return out, nil
}
