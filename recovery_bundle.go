package chameleon

// Durable Classic recovery bundles separate RF acquisition, CPU recovery and
// on-card proof. A bundle is untrusted input and is checksum/shape validated.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
)

const RecoveryBundleSchema = 1
const maxRecoveryBundle = 8 << 20

type RecoveryBundle struct {
	Schema         int      `json:"schema"`
	Kind           string   `json:"kind"` // nested, darkside, hardnested
	UID            HexBytes `json:"uid"`
	TargetBlock    int      `json:"target_block"`
	TargetType     KeyType  `json:"target_type"`
	Tool           string   `json:"tool"`
	Arguments      []string `json:"arguments,omitempty"`
	HardnestedFile HexBytes `json:"hardnested_file,omitempty"`
	Samples        [][]byte `json:"samples,omitempty"`
	Runs           int      `json:"runs,omitempty"`
	Checksum       string   `json:"sha256"`
}

func (b RecoveryBundle) checksum() (string, error) {
	b.Checksum = ""
	raw, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func (b *RecoveryBundle) Seal() error {
	if err := b.Validate(false); err != nil {
		return err
	}
	sum, err := b.checksum()
	if err != nil {
		return err
	}
	b.Checksum = sum
	return nil
}
func (b RecoveryBundle) Validate(checksum bool) error {
	if b.Schema != RecoveryBundleSchema || (len(b.UID) != 4 && len(b.UID) != 7 && len(b.UID) != 10) || b.TargetBlock < 0 || b.TargetBlock > 255 || (b.TargetType != KeyA && b.TargetType != KeyB) {
		return fmt.Errorf("invalid recovery bundle identity or schema")
	}
	if _, err := keyPayload(b.TargetBlock, b.TargetType, make([]byte, 6)); err != nil {
		return err
	}
	switch b.Kind {
	case "nested":
		if b.Tool != "nested" && b.Tool != "staticnested" {
			return fmt.Errorf("invalid nested tool")
		}
		if len(b.Arguments) == 0 {
			return fmt.Errorf("nested arguments missing")
		}
	case "darkside":
		if b.Tool != "darkside" || len(b.Arguments) < 6 || len(b.Samples) == 0 {
			return fmt.Errorf("invalid darkside capture")
		}
	case "hardnested":
		if b.Tool != "hardnested" || len(b.HardnestedFile) < 6+9*256 || len(b.HardnestedFile) > 4<<20+6 {
			return fmt.Errorf("invalid hardnested capture")
		}
	default:
		return fmt.Errorf("unsupported recovery bundle kind")
	}
	if checksum {
		sum, err := b.checksum()
		if err != nil {
			return err
		}
		if !bytes.Equal([]byte(sum), []byte(b.Checksum)) {
			return fmt.Errorf("recovery bundle SHA-256 mismatch")
		}
	}
	return nil
}
func ReadRecoveryBundle(path string) (RecoveryBundle, error) {
	var b RecoveryBundle
	f, err := os.Open(path)
	if err != nil {
		return b, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxRecoveryBundle+1))
	if err != nil {
		return b, err
	}
	if len(data) > maxRecoveryBundle {
		return b, fmt.Errorf("recovery bundle exceeds 8MiB")
	}
	if err = json.Unmarshal(data, &b); err != nil {
		return b, err
	}
	return b, b.Validate(true)
}
func WriteRecoveryBundle(path string, b RecoveryBundle) error {
	if err := b.Seal(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxRecoveryBundle {
		return fmt.Errorf("recovery bundle exceeds 8MiB")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

type OfflineRecovery struct {
	Kind           string         `json:"kind"`
	CaptureSHA256  string         `json:"capture_sha256"`
	UID            HexBytes       `json:"uid"`
	Block          int            `json:"target_block"`
	KeyType        KeyType        `json:"target_type"`
	Candidates     []HexBytes     `json:"candidate_keys"`
	VerifiedOnCard bool           `json:"verified_on_card"`
	Tool           RecoveryResult `json:"tool_output"`
}

func ComputeRecovery(ctx context.Context, tools string, b RecoveryBundle) (OfflineRecovery, error) {
	var out OfflineRecovery
	if err := b.Validate(true); err != nil {
		return out, err
	}
	out = OfflineRecovery{Kind: b.Kind, CaptureSHA256: b.Checksum, UID: append(HexBytes(nil), b.UID...), Block: b.TargetBlock, KeyType: b.TargetType, VerifiedOnCard: false}
	args := append([]string(nil), b.Arguments...)
	if b.Kind == "hardnested" {
		f, err := os.CreateTemp("", "chameleon-hardnested-offline-*.bin")
		if err != nil {
			return out, err
		}
		name := f.Name()
		defer os.Remove(name)
		if _, err = f.Write(b.HardnestedFile); err != nil {
			f.Close()
			return out, err
		}
		if err = f.Close(); err != nil {
			return out, err
		}
		args = []string{name}
	}
	tool, err := RunRecovery(ctx, tools, b.Tool, args...)
	out.Tool = tool
	if err != nil {
		return out, err
	}
	if b.Kind == "darkside" || b.Kind == "hardnested" {
		out.Candidates = advancedCandidates(tool.Stdout)
	} else {
		out.Candidates = recoveryCandidates(tool.Stdout)
	}
	return out, nil
}

type RecoveryVerification struct {
	UID            HexBytes `json:"uid"`
	Block          int      `json:"block"`
	KeyType        KeyType  `json:"key_type"`
	Candidate      HexBytes `json:"candidate"`
	VerifiedOnCard bool     `json:"verified_on_card"`
}

func (c *Client) VerifyRecoveryCandidate(ctx context.Context, uid []byte, block int, kind KeyType, candidate []byte) (RecoveryVerification, error) {
	out := RecoveryVerification{UID: append(HexBytes(nil), uid...), Block: block, KeyType: kind, Candidate: append(HexBytes(nil), candidate...)}
	if len(uid) != 4 && len(uid) != 7 && len(uid) != 10 {
		return out, fmt.Errorf("invalid full UID")
	}
	if _, err := keyPayload(block, kind, candidate); err != nil {
		return out, err
	}
	err := c.Reader(ctx, func(s *ReaderSession) error {
		current, e := scanSession(ctx, s)
		if e != nil {
			return e
		}
		if !bytes.Equal(current, uid) {
			return fmt.Errorf("card UID does not match capture")
		}
		p, _ := keyPayload(block, kind, candidate)
		_, e = s.expect(ctx, CmdMf1AuthOneKeyBlock, p, 0)
		if e != nil {
			return e
		}
		out.VerifiedOnCard = true
		return nil
	})
	return out, err
}

func (c *Client) CollectRecoveryBundle(ctx context.Context, knownBlock int, knownType KeyType, knownKey []byte, targetBlock int, targetType KeyType, kind string, maxRuns int, slow bool, syncMax byte) (out RecoveryBundle, err error) {
	if maxRuns < 1 || maxRuns > 1000 {
		return out, fmt.Errorf("max runs must be 1..1000")
	}
	if kind == "nested" {
		s, e := c.CollectNested(ctx, knownBlock, knownType, knownKey, targetBlock, targetType)
		if e != nil {
			return out, e
		}
		var uid HexBytes
		e = c.Reader(ctx, func(rs *ReaderSession) error { var x error; uid, x = scanSession(ctx, rs); return x })
		if e != nil {
			return out, e
		}
		if binary.BigEndian.Uint32(uid[len(uid)-4:]) != s.UID {
			return out, fmt.Errorf("card changed after nonce acquisition")
		}
		return RecoveryBundle{Schema: RecoveryBundleSchema, Kind: "nested", UID: uid, TargetBlock: targetBlock, TargetType: targetType, Tool: s.Tool, Arguments: s.Arguments}, nil
	}
	if _, e := keyPayload(targetBlock, targetType, make([]byte, 6)); e != nil {
		return out, e
	}
	err = c.Reader(ctx, func(s *ReaderSession) error {
		out.Schema = RecoveryBundleSchema
		out.Kind = kind
		out.TargetBlock = targetBlock
		out.TargetType = targetType
		out.UID, err = scanSession(ctx, s)
		if err != nil {
			return err
		}
		switch kind {
		case "hardnested":
			if _, err = keyPayload(knownBlock, knownType, knownKey); err != nil {
				return err
			}
			h := HardnestedCollector{}
			p, _ := keyPayload(knownBlock, knownType, knownKey)
			p = append([]byte{boolean(slow)}, p...)
			p = append(p, byte(targetType), byte(targetBlock))
			for i := 0; i < maxRuns && !h.Ready(); i++ {
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
				out.Runs++
			}
			if !h.Ready() {
				return fmt.Errorf("hardnested capture incomplete: %d/256 nonce classes", h.Unique)
			}
			out.HardnestedFile, err = h.File(out.UID, targetBlock, targetType)
			out.Tool = "hardnested"
			return err
		case "darkside":
			if syncMax == 0 {
				syncMax = 3
			}
			out.Tool = "darkside"
			args := []string{decimal(uint32(binary.BigEndian.Uint32(out.UID[len(out.UID)-4:])))}
			for i := 0; i < maxRuns; i++ {
				uid, e := scanSession(ctx, s)
				if e != nil {
					return e
				}
				if !bytes.Equal(uid, out.UID) {
					return fmt.Errorf("card changed during acquisition")
				}
				raw, e := s.expect(ctx, CmdMf1DarksideAcquire, []byte{byte(targetType), byte(targetBlock), boolean(i == 0), syncMax}, 0)
				if e != nil {
					return e
				}
				if len(raw) != 33 || raw[0] != 0 || binary.BigEndian.Uint32(raw[1:]) != binary.BigEndian.Uint32(out.UID[len(out.UID)-4:]) {
					return fmt.Errorf("invalid darkside response")
				}
				out.Samples = append(out.Samples, append([]byte(nil), raw...))
				args = append(args, decimal(binary.BigEndian.Uint32(raw[5:])), strconv.FormatUint(binary.BigEndian.Uint64(raw[17:]), 10), strconv.FormatUint(binary.BigEndian.Uint64(raw[9:]), 10), decimal(binary.BigEndian.Uint32(raw[25:])), decimal(binary.BigEndian.Uint32(raw[29:])))
			}
			out.Arguments = args
			return nil
		default:
			return fmt.Errorf("kind must be nested, darkside, or hardnested")
		}
	})
	return
}
