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

// DetectionLogs reads the active slot's complete Classic log snapshot.
// It rejects snapshots larger than limit rather than returning a partial log.
func (c *Client) DetectionLogs(ctx context.Context, limit int) (out []AuthLog, err error) {
	if limit < 1 || limit > 100000 {
		return nil, fmt.Errorf("log limit must be 1..100000")
	}
	out = []AuthLog{}
	err = c.Emulator(ctx, func(s *ReaderSession) error {
		var e error
		out, e = classicDetectionLogs(ctx, s, 0, limit, true)
		return e
	})
	return
}

// ClassicDetectionLogs reads at most limit records from start in one slot's
// snapshot. Slot 0 selects the active slot; other selections are restored.
func (c *Client) ClassicDetectionLogs(ctx context.Context, slot int, start uint32, limit int) (out []AuthLog, err error) {
	if limit < 1 || limit > 100000 {
		return nil, fmt.Errorf("log limit must be 1..100000")
	}
	out = []AuthLog{}
	err = c.slotSession(ctx, slot, false, func(s *ReaderSession) error {
		_, _, _, width, e := c.activeGeometry(ctx)
		if e != nil {
			return e
		}
		if width != 16 {
			return fmt.Errorf("authentication logs require a Classic slot")
		}
		out, e = classicDetectionLogs(ctx, s, start, limit, false)
		return e
	})
	return
}

func classicDetectionLogs(ctx context.Context, s *ReaderSession, start uint32, limit int, requireComplete bool) ([]AuthLog, error) {
	out := []AuthLog{}
	b, e := s.expect(ctx, CmdMf1GetDetectionCount, nil, 0x68)
	if e != nil {
		return out, e
	}
	if len(b) != 4 {
		return out, fmt.Errorf("invalid detection count")
	}
	count := binary.BigEndian.Uint32(b)
	if requireComplete && count > uint32(limit) {
		return out, fmt.Errorf("log count exceeds requested limit")
	}
	if start > count {
		return out, fmt.Errorf("start exceeds snapshot count")
	}
	for pos := start; pos < count && len(out) < limit; {
		payload := make([]byte, 4)
		binary.BigEndian.PutUint32(payload, pos)
		b, e = s.expect(ctx, CmdMf1GetDetectionLog, payload, 0x68)
		if e != nil {
			return out, e
		}
		if len(b) == 0 || len(b)%18 != 0 {
			return out, fmt.Errorf("truncated detection log or no forward progress")
		}
		n := min(len(b)/18, limit-len(out))
		if uint32(n) > count-pos {
			n = int(count - pos)
		}
		for i := 0; i < n; i++ {
			p := b[i*18:]
			kind := "A"
			if p[1]&1 != 0 {
				kind = "B"
			}
			out = append(out, AuthLog{p[0], kind, p[1]&2 != 0, append(HexBytes(nil), p[2:6]...), append(HexBytes(nil), p[6:10]...), append(HexBytes(nil), p[10:14]...), append(HexBytes(nil), p[14:18]...)})
		}
		pos += uint32(n)
	}
	return out, nil
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
