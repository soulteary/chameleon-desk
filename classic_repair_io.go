package chameleon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type ClassicRepairRequest struct {
	Source GUICard     `json:"source"`
	Read   ClassicRead `json:"read"`
	Blocks []int       `json:"blocks,omitempty"`
}

func (r ClassicRepairRequest) Requirements() ([]Command, error) {
	d, err := r.Source.ReadDraft()
	if err != nil {
		return nil, err
	}
	if d == nil || d.Result.Kind != "classic" || d.Result.Identity == nil {
		return nil, fmt.Errorf("repair requires a Classic draft with captured identity")
	}
	keys, err := r.Read.KeyMap()
	if err != nil {
		return nil, err
	}
	tag, _ := classicReadGeometry(keys)
	if tag != d.Result.Dump.TagType || !bytes.Equal(keys.UID, d.Result.UID) {
		return nil, fmt.Errorf("reread keys must select the draft UID and geometry")
	}
	if len(r.Blocks) != 0 {
		seen := map[int]bool{}
		first, count, _ := ClassicSectorGeometry(len(keys.Sectors) - 1)
		for _, block := range r.Blocks {
			if block < 0 || block >= first+count || seen[block] {
				return nil, fmt.Errorf("invalid or duplicate selected block")
			}
			seen[block] = true
		}
	}
	return r.Read.Requirements()
}

// ReadClassicRepair explicitly rereads the full requested geometry. It never
// silently replays a prior task; the returned observation can be compared and
// merged with the preserved source using explicit conflict choices.
func (c *Client) ReadClassicRepair(ctx context.Context, r ClassicRepairRequest) (ReadResult, error) {
	if _, err := r.Requirements(); err != nil {
		return ReadResult{}, err
	}
	if len(r.Blocks) != 0 {
		return ReadResult{}, fmt.Errorf("use ReadClassicSelectedRepair for selected blocks")
	}
	d, _ := r.Source.ReadDraft()
	keys, _ := r.Read.KeyMap()
	expected := *d.Result.Identity
	dump, err := c.dumpClassicWithKeys(ctx, keys, &expected)
	if dump.Schema == 2 {
		dump.SHA256 = imageDigest(dump.Data)
	}
	out := ReadResult{Schema: 1, Kind: "classic", Complete: err == nil, Requested: d.Result.Requested, Read: len(dump.Data) / 16, UID: dump.UID, Dump: &dump, Identity: &expected}
	return out, err
}

type ClassicKeyCandidate struct {
	Sector int      `json:"sector"`
	Type   KeyType  `json:"key_type"`
	Key    HexBytes `json:"key"`
}
type ClassicKeyVerificationRequest struct {
	Identity ClassicIdentity       `json:"identity"`
	Keys     []ClassicKeyCandidate `json:"keys"`
}
type ClassicKeyVerification struct {
	Sector   int     `json:"sector"`
	Type     KeyType `json:"key_type"`
	Verified bool    `json:"verified"`
	Readable bool    `json:"readable_key_b,omitempty"`
	Error    string  `json:"error,omitempty"`
}
type ClassicKeyVerificationResult struct {
	Schema      int                      `json:"schema"`
	Identity    ClassicIdentity          `json:"identity"`
	Complete    bool                     `json:"complete"`
	AllVerified bool                     `json:"all_verified"`
	Keys        []ClassicKeyVerification `json:"keys"`
}

func (r ClassicKeyVerificationRequest) Requirements() ([]Command, error) {
	if err := r.Identity.Validate(); err != nil {
		return nil, err
	}
	if len(r.Keys) < 1 || len(r.Keys) > 80 {
		return nil, fmt.Errorf("1..80 explicit key candidates required")
	}
	seen := map[[2]int]bool{}
	for _, key := range r.Keys {
		first, _, err := ClassicSectorGeometry(key.Sector)
		if err != nil {
			return nil, err
		}
		if _, err = keyPayload(first, key.Type, key.Key); err != nil {
			return nil, err
		}
		id := [2]int{key.Sector, int(key.Type)}
		if seen[id] {
			return nil, fmt.Errorf("duplicate sector/key type")
		}
		seen[id] = true
	}
	return []Command{CmdChangeDeviceMode, CmdGetDeviceMode, CmdHf14aScan, CmdMf1AuthOneKeyBlock, CmdMf1ReadOneBlock}, nil
}
func (c *Client) VerifyClassicKeys(ctx context.Context, r ClassicKeyVerificationRequest) (out ClassicKeyVerificationResult, err error) {
	if _, err = r.Requirements(); err != nil {
		return out, err
	}
	out = ClassicKeyVerificationResult{Schema: 1, Identity: r.Identity, Keys: []ClassicKeyVerification{}}
	candidates := append([]ClassicKeyCandidate(nil), r.Keys...)
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Sector != candidates[j].Sector {
			return candidates[i].Sector < candidates[j].Sector
		}
		return candidates[i].Type < candidates[j].Type
	})
	var expected = r.Identity
	verifiedA := map[int]HexBytes{}
	err = c.Reader(ctx, func(s *ReaderSession) error {
		for _, key := range candidates {
			if e := observeClassicIdentity(ctx, s, r.Identity.UID, &expected); e != nil {
				return e
			}
			first, count, _ := ClassicSectorGeometry(key.Sector)
			payload, _ := keyPayload(first, key.Type, key.Key)
			_, e := s.expect(ctx, CmdMf1AuthOneKeyBlock, payload, 0)
			v := ClassicKeyVerification{Sector: key.Sector, Type: key.Type, Verified: e == nil}
			if e != nil {
				var status *StatusError
				if !errors.As(e, &status) || (status.Status != 2 && status.Status != 6) {
					return e
				}
				if key.Type == KeyB && len(verifiedA[key.Sector]) == 6 {
					trailer, readErr := s.ReadClassicBlock(ctx, first+count-1, KeyA, verifiedA[key.Sector])
					if readErr != nil {
						return readErr
					}
					if ValidateClassicAccess(trailer) == nil && classicBReadable(trailer) && bytes.Equal(trailer[10:16], key.Key) {
						v.Verified = true
						v.Readable = true
					}
				}
				if !v.Verified {
					v.Error = "candidate did not pass authentication or readable-key evidence"
				}
			}
			if v.Verified && key.Type == KeyA {
				verifiedA[key.Sector] = append(HexBytes(nil), key.Key...)
			}
			out.Keys = append(out.Keys, v)
		}
		return observeClassicIdentity(ctx, s, r.Identity.UID, &expected)
	})
	out.Complete = err == nil
	out.AllVerified = out.Complete && len(out.Keys) == len(r.Keys)
	for _, key := range out.Keys {
		out.AllVerified = out.AllVerified && key.Verified
	}
	return out, err
}

type ClassicRecoveryRequest struct {
	Identity     ClassicIdentity `json:"identity"`
	KnownBlock   int             `json:"known_block"`
	KnownType    KeyType         `json:"known_type"`
	KnownKey     HexBytes        `json:"known_key,omitempty"`
	TargetBlock  int             `json:"target_block"`
	TargetType   KeyType         `json:"target_type"`
	Strategy     string          `json:"strategy"`
	LimitSeconds int             `json:"limit_seconds"`
	MaxRuns      int             `json:"max_runs"`
	MaxAttempts  int             `json:"max_attempts"`
	Slow         bool            `json:"slow,omitempty"`
	SyncMax      byte            `json:"sync_max,omitempty"`
}
type ClassicRecoveryResult struct {
	Schema       int                    `json:"schema"`
	Identity     ClassicIdentity        `json:"identity"`
	Complete     bool                   `json:"complete"`
	Verified     bool                   `json:"verified_on_card"`
	Recovery     AdvancedRecoveryResult `json:"recovery"`
	Verification *RecoveryVerification  `json:"verification,omitempty"`
}

func (r ClassicRecoveryRequest) Requirements() ([]Command, error) {
	if err := r.Identity.Validate(); err != nil {
		return nil, err
	}
	if r.LimitSeconds < 1 || r.LimitSeconds > 30 || r.MaxRuns < 1 || r.MaxRuns > 64 || r.MaxAttempts < 1 || r.MaxAttempts > 3 {
		return nil, fmt.Errorf("recovery requires a 1..30 second limit, 1..64 runs and 1..3 attempts")
	}
	if _, err := keyPayload(r.TargetBlock, r.TargetType, make([]byte, 6)); err != nil {
		return nil, err
	}
	if r.Strategy != "darkside" {
		if _, err := keyPayload(r.KnownBlock, r.KnownType, r.KnownKey); err != nil {
			return nil, err
		}
	}
	commands := []Command{CmdChangeDeviceMode, CmdGetDeviceMode, CmdHf14aScan, CmdMf1AuthOneKeyBlock}
	switch r.Strategy {
	case "nested":
		commands = append(commands, CmdMf1DetectPrng, CmdMf1StaticNestedAcquire, CmdMf1DetectNtDist, CmdMf1NestedAcquire)
	case "hardnested":
		commands = append(commands, CmdMf1HardnestedAcquire)
	case "darkside":
		commands = append(commands, CmdMf1DarksideAcquire)
	default:
		return nil, fmt.Errorf("select nested, hardnested or darkside explicitly")
	}
	return commands, nil
}

// ValidateClassicRecoveryHelpers performs no execution or device access. The
// directory is trusted server/CLI configuration, never a Web request field.
func ValidateClassicRecoveryHelpers(directory, strategy string) error {
	if strings.TrimSpace(directory) == "" {
		return fmt.Errorf("trusted recovery helper directory is not configured")
	}
	names := []string{}
	switch strategy {
	case "nested":
		names = []string{"nested", "staticnested"}
	case "hardnested":
		names = []string{"hardnested"}
	case "darkside":
		names = []string{"darkside"}
	default:
		return fmt.Errorf("unsupported recovery strategy")
	}
	return DiagnoseRecovery(directory).Require(names...)
}
func (c *Client) RecoverClassicBound(ctx context.Context, r ClassicRecoveryRequest, tools string) (out ClassicRecoveryResult, err error) {
	// A candidate from an interrupted inner helper is not a verified workflow result.
	defer func() {
		if err != nil || !out.Verified {
			out.Recovery.Key = nil
			if out.Verification != nil {
				out.Verification.Candidate = nil
				out.Verification.VerifiedOnCard = false
			}
		}
	}()
	if _, err = r.Requirements(); err != nil {
		return out, err
	}
	if err = ValidateClassicRecoveryHelpers(tools, r.Strategy); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.LimitSeconds)*time.Second)
	defer cancel()
	out = ClassicRecoveryResult{Schema: 1, Identity: r.Identity}
	expected := r.Identity
	if err = c.Reader(ctx, func(s *ReaderSession) error { return observeClassicIdentity(ctx, s, r.Identity.UID, &expected) }); err != nil {
		return out, err
	}
	out.Recovery, err = c.RecoverClassic(ctx, r.KnownBlock, r.KnownType, r.KnownKey, r.TargetBlock, r.TargetType, AdvancedRecoveryOptions{Strategy: r.Strategy, ToolsDir: tools, Limit: time.Duration(r.LimitSeconds) * time.Second, MaxRuns: r.MaxRuns, MaxAttempts: r.MaxAttempts, Slow: r.Slow, SyncMax: r.SyncMax})
	if err != nil {
		return out, err
	}
	if !bytes.Equal(out.Recovery.UID, r.Identity.UID) || len(out.Recovery.Key) != 6 {
		out.Recovery.Key = nil
		return out, fmt.Errorf("recovery did not produce a candidate for the expected full UID")
	}
	v, e := c.VerifyRecoveryCandidate(ctx, r.Identity.UID, r.TargetBlock, r.TargetType, out.Recovery.Key)
	out.Verification = &v
	if e != nil {
		out.Recovery.Key = nil
		return out, e
	}
	err = c.Reader(ctx, func(s *ReaderSession) error { return observeClassicIdentity(ctx, s, r.Identity.UID, &expected) })
	out.Complete = err == nil
	out.Verified = out.Complete && v.VerifiedOnCard
	if !out.Verified {
		out.Recovery.Key = nil
	}
	return out, err
}

type ClassicPhysicalWriteRequest struct {
	Source        ClassicCardDump `json:"source"`
	Target        ClassicRead     `json:"target"`
	Backup        ReadResult      `json:"backup"`
	WriteTrailers bool            `json:"write_trailers"`
	Confirm       bool            `json:"confirm"`
	PlanDigest    string          `json:"plan_digest,omitempty"`
}
type ClassicPhysicalWritePlan struct {
	Schema              int      `json:"schema"`
	TargetUID           HexBytes `json:"target_uid"`
	SourcePublicSHA256  string   `json:"source_public_sha256"`
	BackupPublicSHA256  string   `json:"backup_public_sha256"`
	TagType             uint16   `json:"tag_type"`
	Blocks              []int    `json:"blocks"`
	WriteTrailers       bool     `json:"write_trailers"`
	ManufacturerSkipped bool     `json:"manufacturer_skipped"`
	Digest              string   `json:"digest"`
}
type ClassicPhysicalWriteResult struct {
	Schema         int                      `json:"schema"`
	Plan           ClassicPhysicalWritePlan `json:"plan"`
	Backup         *ReadResult              `json:"backup,omitempty"`
	Clone          *ClassicCloneResult      `json:"clone,omitempty"`
	WriteAttempted bool                     `json:"write_attempted"`
	Complete       bool                     `json:"complete"`
	Outcome        string                   `json:"outcome"`
}

// classicPublicMemoryDigest excludes every trailer key, including readable Key B.
// Full dump checksums and credentials are bound only by private Web confirmation.
func classicPublicMemoryDigest(d ClassicCardDump) string {
	data := append([]byte(nil), d.Data...)
	for sector := 0; sector < 40; sector++ {
		first, count, _ := ClassicSectorGeometry(sector)
		offset := (first + count - 1) * 16
		if offset+16 > len(data) {
			break
		}
		clear(data[offset : offset+6])
		clear(data[offset+10 : offset+16])
	}
	return imageDigest(data)
}

func PlanClassicPhysicalWrite(r ClassicPhysicalWriteRequest) (ClassicPhysicalWritePlan, error) {
	if err := r.Source.Validate(); err != nil {
		return ClassicPhysicalWritePlan{}, err
	}
	if r.Source.Schema != 2 {
		return ClassicPhysicalWritePlan{}, fmt.Errorf("physical Classic writes require a schema-2 source")
	}
	target, err := r.Target.KeyMap()
	if err != nil {
		return ClassicPhysicalWritePlan{}, err
	}
	tag, _ := classicReadGeometry(target)
	if tag != r.Source.TagType {
		return ClassicPhysicalWritePlan{}, fmt.Errorf("source and target geometry differ")
	}
	if _, err = r.Backup.draftRows(); err != nil {
		return ClassicPhysicalWritePlan{}, err
	}
	if r.Backup.Kind != "classic" || !r.Backup.Complete || r.Backup.Identity == nil || r.Backup.Read != r.Backup.Requested || r.Backup.Dump.TagType != tag || !bytes.Equal(r.Backup.UID, target.UID) {
		return ClassicPhysicalWritePlan{}, fmt.Errorf("a complete identity-bearing backup of the explicit target is required")
	}
	if err = r.Backup.Dump.Validate(); err != nil {
		return ClassicPhysicalWritePlan{}, err
	}
	if r.WriteTrailers && (len(r.Source.UnknownKeys) != 0 || len(r.Backup.Dump.UnknownKeys) != 0) {
		return ClassicPhysicalWritePlan{}, fmt.Errorf("trailer writes require all source and backup keys to be known")
	}
	plan := ClassicPhysicalWritePlan{Schema: 1, TargetUID: append(HexBytes(nil), target.UID...), SourcePublicSHA256: classicPublicMemoryDigest(r.Source), BackupPublicSHA256: classicPublicMemoryDigest(*r.Backup.Dump), TagType: tag, Blocks: []int{}, WriteTrailers: r.WriteTrailers, ManufacturerSkipped: true}
	for sector := range target.Sectors {
		first, count, _ := ClassicSectorGeometry(sector)
		for block := first; block < first+count; block++ {
			if block == 0 || block == first+count-1 && !r.WriteTrailers {
				continue
			}
			if block == first+count-1 {
				if err = ValidateClassicAccess(r.Source.Data[block*16 : (block+1)*16]); err != nil {
					return ClassicPhysicalWritePlan{}, err
				}
			}
			plan.Blocks = append(plan.Blocks, block)
		}
	}
	// Public digest binds the visible plan, not secret target credentials. Web
	// confirmation additionally binds the full request with a server-private HMAC.
	raw, err := json.Marshal(plan)
	if err != nil {
		return ClassicPhysicalWritePlan{}, err
	}
	plan.Digest = imageDigest(raw)
	return plan, nil
}
func (r ClassicPhysicalWriteRequest) Requirements() ([]Command, error) {
	if _, err := PlanClassicPhysicalWrite(r); err != nil {
		return nil, err
	}
	commands, err := r.Target.Requirements()
	if err != nil {
		return nil, err
	}
	commands = append(commands, CmdMf1WriteOneBlock, CmdMf1AuthOneKeyBlock)
	if r.WriteTrailers {
		commands = append(commands, CmdMf1AuthOneKeyBlock)
	}
	return uniqueCommands(commands), nil
}

// WriteClassicPlanned revalidates the target backup before entering CloneClassic.
// beforeWrite lets a task layer mark the mutation boundary, not the preceding
// read-only preflight. There is no retry after a lost write response.
func (c *Client) WriteClassicPlanned(ctx context.Context, r ClassicPhysicalWriteRequest, beforeWrite func()) (out ClassicPhysicalWriteResult, err error) {
	plan, err := PlanClassicPhysicalWrite(r)
	if err != nil {
		return out, err
	}
	out = ClassicPhysicalWriteResult{Schema: 1, Plan: plan, Outcome: "not_started"}
	if !r.Confirm || r.PlanDigest != plan.Digest {
		return out, fmt.Errorf("explicit confirmation and the current plan digest are required")
	}
	backup, e := c.ReadClassicWithIdentity(ctx, r.Target)
	out.Backup = &backup
	if e != nil {
		return out, e
	}
	if !sameClassicIdentity(backup.Identity, r.Backup.Identity) || backup.Dump.SHA256 != r.Backup.Dump.SHA256 || !sameClassicUnknownKeys(backup.Dump.UnknownKeys, r.Backup.Dump.UnknownKeys) {
		return out, fmt.Errorf("target changed since backup; export a new backup and plan again")
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	keys, _ := r.Target.KeyMap()
	clone, e := c.CloneClassic(ctx, r.Source, keys, ClassicCloneOptions{WriteTrailers: r.WriteTrailers, PreserveAccess: true, ExpectedIdentity: r.Backup.Identity, NoWriteRetry: true, BeforeWrite: func() {
		out.WriteAttempted = true
		out.Outcome = "write_outcome_unknown"
		if beforeWrite != nil {
			beforeWrite()
		}
	}})
	out.Clone = &clone
	if e != nil {
		return out, e
	}
	out.Complete = true
	out.Outcome = "verified"
	return out, nil
}
func sameClassicUnknownKeys(a, b []ClassicUnknownKey) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[ClassicUnknownKey]bool{}
	for _, v := range a {
		seen[v] = true
	}
	for _, v := range b {
		if !seen[v] {
			return false
		}
	}
	return true
}
