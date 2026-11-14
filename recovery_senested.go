package chameleon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math/bits"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type StaticEncryptedNonce struct {
	NT        uint32
	Encrypted uint32
	Parity    byte
}
type StaticEncryptedSector struct{ A, B StaticEncryptedNonce }

func staticNonce(p []byte) StaticEncryptedNonce {
	half := uint32(binary.BigEndian.Uint16(p))
	x := bits.ReverseBytes32(half)
	for i := 0; i < 16; i++ {
		x = (x >> 1) | (((x>>16 ^ x>>18 ^ x>>19 ^ x>>21) & 1) << 31)
	}
	return StaticEncryptedNonce{half<<16 | bits.ReverseBytes32(x), binary.BigEndian.Uint32(p[3:]), p[2] & 15}
}
func parseStaticEncrypted(raw []byte, sectors int) (uint32, []StaticEncryptedSector, error) {
	if len(raw) != 4+sectors*14 {
		return 0, nil, fmt.Errorf("incomplete static encrypted nonce response")
	}
	out := make([]StaticEncryptedSector, sectors)
	for i := range out {
		p := raw[4+i*14:]
		out[i] = StaticEncryptedSector{staticNonce(p[:7]), staticNonce(p[7:14])}
	}
	return binary.BigEndian.Uint32(raw), out, nil
}

type SectorKeys struct {
	BReadable bool     `json:"b_readable,omitempty"`
	Sector    int      `json:"sector"`
	A         HexBytes `json:"a,omitempty"`
	B         HexBytes `json:"b,omitempty"`
}
type StaticEncryptedRecovery struct {
	UID     HexBytes     `json:"uid"`
	Sectors []SectorKeys `json:"sectors"`
}

func candidateFile(path string) ([]HexBytes, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return candidateLines(io.LimitReader(f, 8<<20+1))
}
func candidateLines(r io.Reader) ([]HexBytes, error) {
	scanner := bufio.NewScanner(r)
	out := []HexBytes{}
	total := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		total += len(scanner.Bytes()) + 1
		if total > 8<<20 {
			return nil, fmt.Errorf("candidate dictionary exceeds 8MiB")
		}
		if line == "" {
			continue
		}
		if len(line) != 12 {
			return nil, fmt.Errorf("malformed candidate key")
		}
		key, e := hex.DecodeString(line)
		if e != nil {
			return nil, e
		}
		out = append(out, key)
	}
	return out, scanner.Err()
}
func (c *Client) checkCandidateBatches(ctx context.Context, uid []byte, block int, kind KeyType, keys []HexBytes) (HexBytes, error) {
	var found HexBytes
	err := c.Reader(ctx, func(s *ReaderSession) error {
		for pos := 0; pos < len(keys); pos += 83 {
			current, e := scanSession(ctx, s)
			if e != nil {
				return e
			}
			if !bytes.Equal(current, uid) {
				return fmt.Errorf("card changed during dictionary check")
			}
			n := min(83, len(keys)-pos)
			p := []byte{byte(block), byte(kind), byte(n)}
			for _, key := range keys[pos : pos+n] {
				if len(key) != 6 {
					return fmt.Errorf("invalid dictionary key length")
				}
				p = append(p, key...)
			}
			response, e := s.Do(ctx, CmdMf1CheckKeysOnBlock, p)
			if e != nil {
				return e
			}
			if response.Status == 1 {
				return ErrNoCard
			}
			if response.Status == 6 {
				continue
			}
			if e = response.Check(0); e != nil {
				return e
			}
			raw := response.Data
			if len(raw) != 7 || raw[0] > 1 {
				return fmt.Errorf("invalid dictionary-check response")
			}
			if raw[0] == 1 {
				candidate := append(HexBytes(nil), raw[1:]...)
				inBatch := false
				for _, key := range keys[pos : pos+n] {
					inBatch = inBatch || bytes.Equal(key, candidate)
				}
				if !inBatch {
					return fmt.Errorf("firmware returned key outside batch")
				}
				found = candidate
				return nil
			}
		}
		return nil
	})
	if err != nil || len(found) == 0 {
		return nil, err
	}
	return c.verifyRecovery(ctx, uid, block, kind, []HexBytes{found})
}

// RecoverStaticEncrypted automates RF08S/backdoor nonce acquisition, dictionary
// generation/filtering, and per-sector on-card key verification. Uses sector 0
// as acquisition start: the pinned firmware mishandles nonzero start offsets.
func (c *Client) RecoverStaticEncrypted(ctx context.Context, backdoor []byte, sectors int, tools string, limit time.Duration) (out StaticEncryptedRecovery, err error) {
	if len(backdoor) != 6 || sectors < 1 || sectors > 40 || limit <= 0 {
		return out, fmt.Errorf("six-byte backdoor, 1..40 sectors and positive limit required")
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	var raw []byte
	err = c.Reader(ctx, func(s *ReaderSession) error {
		var e error
		out.UID, e = scanSession(ctx, s)
		if e != nil {
			return e
		}
		p := append(append([]byte(nil), backdoor...), byte(sectors), 0)
		raw, e = s.expect(ctx, CmdMf1EncNestedAcquire, p, 0)
		return e
	})
	if err != nil {
		return
	}
	uid, samples, e := parseStaticEncrypted(raw, sectors)
	if e != nil {
		return out, e
	}
	if uid != binary.BigEndian.Uint32(out.UID[len(out.UID)-4:]) {
		return out, fmt.Errorf("static encrypted acquisition UID mismatch")
	}
	workspace, e := os.MkdirTemp("", "chameleon-senested-*")
	if e != nil {
		return out, e
	}
	defer os.RemoveAll(workspace)
	run := func(tool string, args ...string) (RecoveryResult, error) {
		return runRecoveryIn(ctx, tools, workspace, tool, args...)
	}
	for sector, pair := range samples {
		block := sector*4 + 3
		if sector >= 32 {
			block = 128 + (sector-32)*16 + 15
		}
		file := func(n StaticEncryptedNonce) string { return fmt.Sprintf("keys_%08x_%02d_%08x.dic", uid, sector, n.NT) }
		for _, nonce := range []StaticEncryptedNonce{pair.A, pair.B} {
			if _, e = run("staticnested_1nt", fmt.Sprintf("%08x", uid), strconv.Itoa(sector), fmt.Sprintf("%08x", nonce.NT), fmt.Sprintf("%08x", nonce.Encrypted), fmt.Sprintf("%04b", nonce.Parity)); e != nil {
				return out, e
			}
		}
		af, bf := file(pair.A), file(pair.B)
		if _, e = run("staticnested_2x1nt_rf08s", af, bf); e != nil {
			return out, e
		}
		filtered := func(f string) string { return strings.TrimSuffix(f, ".dic") + "_filtered.dic" }
		bkeys, e := candidateFile(filepath.Join(workspace, filtered(bf)))
		if e != nil {
			return out, e
		}
		keys := SectorKeys{Sector: sector}
		keys.B, e = c.checkCandidateBatches(ctx, out.UID, block, KeyB, bkeys)
		if e != nil {
			return out, e
		}
		if len(keys.B) > 0 {
			v, e := run("staticnested_2x1nt_rf08s_1key", fmt.Sprintf("%08x", pair.B.NT), strings.ToLower(keys.B.String()), af)
			if e == nil {
				candidates, e := candidateLines(strings.NewReader(v.Stdout))
				if e == nil {
					keys.A, e = c.checkCandidateBatches(ctx, out.UID, block, KeyA, candidates)
					if e != nil {
						return out, e
					}
				}
			}
		}
		if len(keys.A) == 0 {
			akeys, e := candidateFile(filepath.Join(workspace, filtered(af)))
			if e != nil {
				return out, e
			}
			keys.A, e = c.checkCandidateBatches(ctx, out.UID, block, KeyA, akeys)
			if e != nil {
				return out, e
			}
		}
		out.Sectors = append(out.Sectors, keys)
		if len(keys.A) == 0 || len(keys.B) == 0 {
			return out, fmt.Errorf("sector %d did not yield both verified keys", sector)
		}
	}
	return out, nil
}
