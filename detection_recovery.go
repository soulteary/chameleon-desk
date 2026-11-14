package chameleon

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"
)

func (c *Client) DetectionLogs(ctx context.Context, limit int) (out []AuthLog, err error) {
	out = []AuthLog{}
	if limit < 1 || limit > 100000 {
		return nil, fmt.Errorf("log limit must be 1..100000")
	}
	err = c.Emulator(ctx, func(s *ReaderSession) error {
		b, e := s.expect(ctx, CmdMf1GetDetectionCount, nil, 0x68)
		if e != nil {
			return e
		}
		if len(b) != 4 {
			return fmt.Errorf("invalid detection count")
		}
		count := int(binary.BigEndian.Uint32(b))
		if count > limit {
			return fmt.Errorf("log count exceeds requested limit")
		}
		for pos := 0; pos < count; {
			payload := make([]byte, 4)
			binary.BigEndian.PutUint32(payload, uint32(pos))
			b, e = s.expect(ctx, CmdMf1GetDetectionLog, payload, 0x68)
			if e != nil {
				return e
			}
			if len(b) == 0 || len(b)%18 != 0 {
				return fmt.Errorf("truncated detection log or no forward progress")
			}
			n := min(len(b)/18, count-pos)
			for i := 0; i < n; i++ {
				p := b[i*18:]
				kind := "A"
				if p[1]&1 != 0 {
					kind = "B"
				}
				out = append(out, AuthLog{p[0], kind, p[1]&2 != 0, append(HexBytes(nil), p[2:6]...), append(HexBytes(nil), p[6:10]...), append(HexBytes(nil), p[10:14]...), append(HexBytes(nil), p[14:18]...)})
			}
			pos += n
		}
		return nil
	})
	return
}

type DetectionKeyCandidate struct {
	UID            HexBytes `json:"uid"`
	Block          byte     `json:"block"`
	KeyType        string   `json:"key_type"`
	Key            HexBytes `json:"candidate_key"`
	VerifiedOnCard bool     `json:"verified_on_card"`
}

var detectionKeyPattern = regexp.MustCompile(`(?im)^Found Key: \[([a-f0-9]{12})\]\s*$`)

// RecoverDetectionLogs reports offline candidates only, never on-card proof.
// Pair counts and wall time are bounded; duplicate/nested records are skipped.
func RecoverDetectionLogs(ctx context.Context, logs []AuthLog, tools string, limit time.Duration, maxPairs int) (out []DetectionKeyCandidate, err error) {
	if len(logs) > 10000 || limit <= 0 || maxPairs < 1 || maxPairs > 100000 {
		return nil, fmt.Errorf("invalid log count, time or pair bound")
	}
	for _, v := range logs {
		if len(v.UID) != 4 || len(v.NT) != 4 || len(v.NR) != 4 || len(v.AR) != 4 || (v.KeyType != "A" && v.KeyType != "B") {
			return nil, fmt.Errorf("malformed auth log")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	pairs := 0
	seen := map[string]bool{}
	for i, a := range logs {
		if a.Nested {
			continue
		}
		for _, b := range logs[i+1:] {
			if b.Nested || a.Block != b.Block || a.KeyType != b.KeyType || !bytes.Equal(a.UID, b.UID) || bytes.Equal(a.NT, b.NT) && bytes.Equal(a.NR, b.NR) && bytes.Equal(a.AR, b.AR) {
				continue
			}
			if pairs >= maxPairs {
				return out, fmt.Errorf("candidate pair limit reached")
			}
			pairs++
			args := []string{a.UID.String(), a.NT.String(), a.NR.String(), a.AR.String(), b.NT.String(), b.NR.String(), b.AR.String()}
			v, e := RunRecovery(ctx, tools, "mfkey32v2", args...)
			if e != nil {
				return out, e
			}
			for _, m := range detectionKeyPattern.FindAllStringSubmatch(v.Stdout, -1) {
				key, _ := hex.DecodeString(m[1])
				id := fmt.Sprintf("%x/%d/%s/%x", []byte(a.UID), a.Block, a.KeyType, key)
				if !seen[id] {
					seen[id] = true
					out = append(out, DetectionKeyCandidate{a.UID, a.Block, a.KeyType, key, false})
				}
			}
		}
	}
	return out, nil
}
