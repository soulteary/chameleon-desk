package chameleon

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ClassicSelectedBlock is a successful block observation. Trailer keys which
// were neither authenticated nor readable remain explicitly unknown.
type ClassicSelectedBlock struct {
	Block       int                 `json:"block"`
	Data        HexBytes            `json:"data"`
	UnknownKeys []ClassicUnknownKey `json:"unknown_keys,omitempty"`
}
type ClassicSelectedRepairResult struct {
	Schema       int                    `json:"schema"`
	Identity     ClassicIdentity        `json:"identity"`
	TagType      uint16                 `json:"tag_type"`
	Requested    int                    `json:"geometry_blocks"`
	Blocks       []int                  `json:"requested_blocks"`
	Observations []ClassicSelectedBlock `json:"observations"`
	Complete     bool                   `json:"complete"`
	Error        string                 `json:"error,omitempty"`
}

func classicBlockSector(block int) (int, int, int) {
	for sector := 0; sector < 40; sector++ {
		first, count, _ := ClassicSectorGeometry(sector)
		if block >= first && block < first+count {
			return sector, first, count
		}
	}
	return -1, 0, 0
}

// ReadClassicSelectedRepair reads only explicitly selected blocks. Its result
// is sparse evidence, never a new complete-card read or promotion authority.
func (c *Client) ReadClassicSelectedRepair(ctx context.Context, r ClassicRepairRequest) (out ClassicSelectedRepairResult, err error) {
	if _, err = r.Requirements(); err != nil {
		return
	}
	if len(r.Blocks) == 0 {
		return out, fmt.Errorf("at least one selected block required")
	}
	d, _ := r.Source.ReadDraft()
	keys, _ := r.Read.KeyMap()
	out = ClassicSelectedRepairResult{Schema: 1, Identity: *d.Result.Identity, TagType: d.Result.Dump.TagType, Requested: d.Result.Requested, Blocks: append([]int(nil), r.Blocks...), Observations: []ClassicSelectedBlock{}}
	sort.Ints(out.Blocks)
	err = c.Reader(ctx, func(s *ReaderSession) error {
		for _, block := range out.Blocks {
			if e := observeClassicIdentity(ctx, s, keys.UID, &out.Identity); e != nil {
				return e
			}
			sector, first, count := classicBlockSector(block)
			k := keys.Sectors[sector]
			verified := [2]bool{}
			var data []byte
			var last error
			for side, key := range []HexBytes{k.A, k.B} {
				if len(key) == 0 {
					continue
				}
				kind := KeyA
				if side == 1 {
					kind = KeyB
				}
				data, last = s.ReadClassicBlock(ctx, block, kind, key)
				if last == nil {
					verified[side] = true
					break
				}
				var status *StatusError
				if !errors.As(last, &status) || (status.Status != 2 && status.Status != 6) {
					return last
				}
			}
			if last != nil {
				return last
			}
			observation := ClassicSelectedBlock{Block: block, Data: append(HexBytes(nil), data...)}
			if block == first+count-1 {
				if e := ValidateClassicAccess(data); e != nil {
					return fmt.Errorf("sector %d trailer: %w", sector, e)
				}
				readable := classicBReadable(data)
				for side, key := range []HexBytes{k.A, k.B} {
					kind := KeyA
					offset := 0
					if side == 1 {
						kind = KeyB
						offset = 10
					}
					if side == 1 && readable {
						if len(key) == 6 && !bytes.Equal(key, data[10:16]) {
							return fmt.Errorf("sector %d supplied Key B differs from readable trailer", sector)
						}
						continue
					}
					if len(key) == 0 {
						observation.UnknownKeys = append(observation.UnknownKeys, ClassicUnknownKey{sector, kind})
						clear(observation.Data[offset : offset+6])
						continue
					}
					if !verified[side] {
						p, _ := keyPayload(block, kind, key)
						if _, e := s.expect(ctx, CmdMf1AuthOneKeyBlock, p, 0); e != nil {
							return fmt.Errorf("sector %d supplied key %02x not verified: %w", sector, byte(kind), e)
						}
					}
					copy(observation.Data[offset:offset+6], key)
				}
			}
			out.Observations = append(out.Observations, observation)
		}
		return observeClassicIdentity(ctx, s, keys.UID, &out.Identity)
	})
	out.Complete = err == nil
	if err != nil {
		out.Error = err.Error()
	}
	return
}

func (s ClassicSelectedRepairResult) mergeState() (classicMergeState, error) {
	if s.Schema != 1 || s.Identity.Validate() != nil {
		return classicMergeState{}, fmt.Errorf("invalid selected Classic identity")
	}
	blocks := map[uint16]int{1000: 20, 1001: 64, 1002: 128, 1003: 256}[s.TagType]
	if blocks == 0 || s.Requested != blocks || len(s.Blocks) == 0 || len(s.Blocks) > blocks || len(s.Observations) > len(s.Blocks) || s.Complete && (s.Error != "" || len(s.Observations) != len(s.Blocks)) {
		return classicMergeState{}, fmt.Errorf("invalid selected Classic coverage")
	}
	requested := map[int]bool{}
	for _, block := range s.Blocks {
		if block < 0 || block >= blocks || requested[block] {
			return classicMergeState{}, fmt.Errorf("invalid selected block")
		}
		requested[block] = true
	}
	rows := make([]string, blocks)
	for at := range rows {
		rows[at] = strings.Repeat("??", 16)
	}
	seen := map[int]bool{}
	read := 0
	for _, o := range s.Observations {
		if !requested[o.Block] || seen[o.Block] || len(o.Data) != 16 {
			return classicMergeState{}, fmt.Errorf("invalid selected Classic observation")
		}
		seen[o.Block] = true
		row := strings.ToUpper(hex.EncodeToString(o.Data))
		sector, first, count := classicBlockSector(o.Block)
		unknown := map[KeyType]bool{}
		for _, key := range o.UnknownKeys {
			if o.Block != first+count-1 || key.Sector != sector || (key.Type != KeyA && key.Type != KeyB) || unknown[key.Type] {
				return classicMergeState{}, fmt.Errorf("invalid selected trailer key mask")
			}
			unknown[key.Type] = true
			offset := 0
			if key.Type == KeyB {
				offset = 20
			}
			row = row[:offset] + strings.Repeat("??", 6) + row[offset+12:]
		}
		if o.Block == first+count-1 && ValidateClassicAccess(o.Data) != nil {
			return classicMergeState{}, fmt.Errorf("invalid selected trailer access bits")
		}
		rows[o.Block] = row
		if o.Block+1 > read {
			read = o.Block + 1
		}
	}
	dump := &ClassicCardDump{Schema: 2, UID: append(HexBytes(nil), s.Identity.UID...), TagType: s.TagType, Data: make(HexBytes, read*16)}
	// Sparse rows are evaluated only inside the evidence overlay. Their zero-filled
	// backing bytes are not exported as a successful physical dump.
	for block := 0; block < read; block++ {
		for at := 0; at < 16; at++ {
			pair := rows[block][at*2 : at*2+2]
			if pair != "??" {
				b, _ := hex.DecodeString(pair)
				dump.Data[block*16+at] = b[0]
			}
		}
	}
	dump.SHA256 = imageDigest(dump.Data)
	result := ReadResult{Schema: 1, Kind: "classic", UID: dump.UID, Identity: &s.Identity, Requested: blocks, Read: read, Dump: dump, Complete: false}
	return classicMergeState{result: result, rows: rows, authority: false}, nil
}

func (c GUICard) classicSelectedMergeRoot(s ClassicSelectedRepairResult, o ClassicMergeOptions) (*classicMergeNode, error) {
	if !o.ConfirmSameCard {
		return nil, fmt.Errorf("explicit same-card confirmation required")
	}
	left, err := c.classicMergeTree()
	if err != nil {
		return nil, err
	}
	// Detach caller-owned evidence before attaching to a new card.
	s = copyClassicSelected(s)
	return &classicMergeNode{Left: left, Right: &classicMergeNode{SourceID: c.ID, SourceName: "selected block reread", Selection: &s}, Options: &o}, nil
}
func copyClassicSelected(s ClassicSelectedRepairResult) ClassicSelectedRepairResult {
	// All slices are copied, including nested data/masks, without exposing source keys.
	s.Identity.UID = append(HexBytes(nil), s.Identity.UID...)
	s.Identity.ATQA = append(HexBytes(nil), s.Identity.ATQA...)
	if s.Identity.SAK != nil {
		v := *s.Identity.SAK
		s.Identity.SAK = &v
	}
	if s.Identity.ATS != nil {
		v := append(HexBytes{}, (*s.Identity.ATS)...)
		s.Identity.ATS = &v
	}
	s.Blocks = append([]int(nil), s.Blocks...)
	s.Observations = append([]ClassicSelectedBlock(nil), s.Observations...)
	for at := range s.Observations {
		s.Observations[at].Data = append(HexBytes(nil), s.Observations[at].Data...)
		s.Observations[at].UnknownKeys = append([]ClassicUnknownKey(nil), s.Observations[at].UnknownKeys...)
	}
	return s
}
func (c GUICard) ClassicSelectedRepairPreview(s ClassicSelectedRepairResult) (ClassicMergePreview, error) {
	root, err := c.classicSelectedMergeRoot(s, ClassicMergeOptions{ConfirmSameCard: true})
	if err != nil {
		return ClassicMergePreview{}, err
	}
	state, err := evaluateClassicMerge(root, 0, new(int), false)
	if err != nil {
		return ClassicMergePreview{}, err
	}
	known := 0
	for _, row := range state.rows {
		known += 16 - strings.Count(row, "??")
	}
	return ClassicMergePreview{Schema: 1, Conflicts: state.conflicts, FilledBytes: state.filled, KnownBytes: known, TotalBytes: len(state.rows) * 16, HasCompleteRead: state.authority}, nil
}
func (c GUICard) ApplyClassicSelectedRepair(s ClassicSelectedRepairResult, o ClassicMergeOptions) (GUICard, error) {
	root, err := c.classicSelectedMergeRoot(s, o)
	if err != nil {
		return GUICard{}, err
	}
	state, err := evaluateClassicMerge(root, 0, new(int), true)
	if err != nil {
		return GUICard{}, err
	}
	return c.withClassicMergeRoot(root, state)
}
