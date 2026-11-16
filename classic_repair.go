package chameleon

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// ClassicMergeResolution chooses one already-known byte. It never supplies a
// replacement value or turns an unknown byte into a guessed zero.
type ClassicMergeResolution struct {
	Block  int    `json:"block"`
	Byte   int    `json:"byte"`
	Source string `json:"source"` // left or right
}
type ClassicMergeOptions struct {
	ConfirmSameCard bool                     `json:"confirm_same_card"`
	Resolutions     []ClassicMergeResolution `json:"resolutions,omitempty"`
}
type ClassicMergeConflict struct {
	Block int    `json:"block"`
	Byte  int    `json:"byte"`
	Left  string `json:"left"`
	Right string `json:"right"`
}
type ClassicMergePreview struct {
	Schema          int                    `json:"schema"`
	Conflicts       []ClassicMergeConflict `json:"conflicts"`
	FilledBytes     int                    `json:"filled_bytes"`
	KnownBytes      int                    `json:"known_bytes"`
	TotalBytes      int                    `json:"total_bytes"`
	HasCompleteRead bool                   `json:"has_complete_read"`
}

// A bounded tree keeps input reads and their errors rather than rewriting the
// original observation into an alleged successful hardware operation.
type classicMergeNode struct {
	SourceID   string                       `json:"source_card_id,omitempty"`
	SourceName string                       `json:"source_name,omitempty"`
	Read       *ReadCardData                `json:"read,omitempty"`
	Selection  *ClassicSelectedRepairResult `json:"selection,omitempty"`
	Left       *classicMergeNode            `json:"left,omitempty"`
	Right      *classicMergeNode            `json:"right,omitempty"`
	Options    *ClassicMergeOptions         `json:"options,omitempty"`
	Edits      []string                     `json:"edited_rows,omitempty"`
}
type classicMergeEvidence struct {
	Schema int               `json:"schema"`
	Root   *classicMergeNode `json:"root"`
}
type classicMergeState struct {
	result    ReadResult
	rows      []string
	authority bool
	conflicts []ClassicMergeConflict
	filled    int
}

func sameClassicIdentity(a, b *ClassicIdentity) bool {
	return a != nil && b != nil && a.Validate() == nil && b.Validate() == nil && bytes.Equal(a.UID, b.UID) && bytes.Equal(a.ATQA, b.ATQA) && *a.SAK == *b.SAK && bytes.Equal(*a.ATS, *b.ATS)
}
func classicMergeLeaf(id, name string, d ReadCardData) (*classicMergeNode, error) {
	if d.Result.Kind != "classic" || d.Result.Identity == nil {
		return nil, fmt.Errorf("Classic merge requires captured identity in every source")
	}
	original, err := d.Result.draftRows()
	if err != nil {
		return nil, err
	}
	if _, err = cleanReadRows(d.Rows, original); err != nil {
		return nil, err
	}
	// Marshal/unmarshal detaches slices and pointers from callers.
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	var copied ReadCardData
	if err = json.Unmarshal(raw, &copied); err != nil {
		return nil, err
	}
	return &classicMergeNode{SourceID: id, SourceName: name, Read: &copied}, nil
}
func (c GUICard) classicMergeTree() (*classicMergeNode, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	d, err := c.ReadDraft()
	if err != nil {
		return nil, err
	}
	if d == nil || d.Result.Kind != "classic" {
		return nil, fmt.Errorf("Classic read draft required")
	}
	if raw, ok := c.fields["desk_classic_merge"]; ok {
		var e classicMergeEvidence
		if err := json.Unmarshal(raw, &e); err != nil || e.Schema != 1 || e.Root == nil {
			return nil, fmt.Errorf("invalid Classic merge evidence")
		}
		if _, err := evaluateClassicMerge(e.Root, 0, new(int), true); err != nil {
			return nil, err
		}
		e.Root.Edits = append([]string(nil), d.Rows...)
		return e.Root, nil
	}
	return classicMergeLeaf(c.ID, c.Name, *d)
}

func evaluateClassicMerge(n *classicMergeNode, depth int, leaves *int, requireResolved bool) (classicMergeState, error) {
	if n == nil || depth > 8 {
		return classicMergeState{}, fmt.Errorf("Classic merge evidence exceeds maximum depth")
	}
	if n.Selection != nil {
		*leaves = *leaves + 1
		if *leaves > 16 || n.Read != nil || n.Left != nil || n.Right != nil || n.Options != nil || len(n.Edits) != 0 || n.SourceID == "" {
			return classicMergeState{}, fmt.Errorf("invalid or oversized Classic selected source")
		}
		return n.Selection.mergeState()
	}
	if n.Read != nil {
		*leaves = *leaves + 1
		if *leaves > 16 || n.Left != nil || n.Right != nil || n.Options != nil || len(n.Edits) != 0 || n.SourceID == "" {
			return classicMergeState{}, fmt.Errorf("invalid or oversized Classic merge source")
		}
		d := n.Read
		if d.Schema != 1 || d.Result.Kind != "classic" || d.Result.Identity == nil {
			return classicMergeState{}, fmt.Errorf("Classic merge requires identity-bearing read sources")
		}
		original, err := d.Result.draftRows()
		if err != nil {
			return classicMergeState{}, err
		}
		rows, err := cleanReadRows(d.Rows, original)
		if err != nil {
			return classicMergeState{}, err
		}
		if err := classicMergeKeyEdits(original, rows); err != nil {
			return classicMergeState{}, err
		}
		return classicMergeState{result: d.Result, rows: rows, authority: d.Result.Complete && d.Result.Read == d.Result.Requested && d.Error == ""}, nil
	}
	if n.SourceID != "" || n.SourceName != "" || n.Left == nil || n.Right == nil || n.Options == nil || !n.Options.ConfirmSameCard {
		return classicMergeState{}, fmt.Errorf("Classic merge needs explicit same-card confirmation")
	}
	l, err := evaluateClassicMerge(n.Left, depth+1, leaves, true)
	if err != nil {
		return classicMergeState{}, err
	}
	r, err := evaluateClassicMerge(n.Right, depth+1, leaves, true)
	if err != nil {
		return classicMergeState{}, err
	}
	if l.result.Requested != r.result.Requested || l.result.Dump.TagType != r.result.Dump.TagType || !sameClassicIdentity(l.result.Identity, r.result.Identity) {
		return classicMergeState{}, fmt.Errorf("Classic merge identity or geometry differs; matching UID alone is insufficient")
	}
	state := classicMergeState{result: l.result, authority: l.authority || r.authority, rows: make([]string, len(l.rows)), conflicts: []ClassicMergeConflict{}}
	selected := map[int]string{}
	for _, v := range n.Options.Resolutions {
		key := v.Block*16 + v.Byte
		if v.Block < 0 || v.Block >= len(l.rows) || v.Byte < 0 || v.Byte >= 16 || (v.Source != "left" && v.Source != "right") || selected[key] != "" {
			return classicMergeState{}, fmt.Errorf("invalid or duplicate Classic merge resolution")
		}
		selected[key] = v.Source
	}
	used := map[int]bool{}
	for block := range l.rows {
		var row strings.Builder
		for at := 0; at < 16; at++ {
			a, b := l.rows[block][at*2:at*2+2], r.rows[block][at*2:at*2+2]
			value := a
			switch {
			case a == "??" && b != "??":
				value = b
				state.filled++
			case a != "??" && b != "??" && a != b:
				conflict := ClassicMergeConflict{block, at, a, b}
				state.conflicts = append(state.conflicts, conflict)
				key := block*16 + at
				if choice := selected[key]; choice != "" {
					used[key] = true
					if choice == "right" {
						value = b
					}
				} else if requireResolved {
					return classicMergeState{}, fmt.Errorf("Classic byte conflict at block %d byte %d requires an explicit source", block, at)
				}
			}
			row.WriteString(value)
		}
		state.rows[block] = row.String()
	}
	for key := range selected {
		if !used[key] {
			return classicMergeState{}, fmt.Errorf("Classic merge resolution does not refer to a conflict")
		}
	}
	if len(n.Edits) != 0 {
		edited, err := cleanReadRows(n.Edits, state.rows)
		if err != nil {
			return classicMergeState{}, err
		}
		if err := classicMergeKeyEdits(state.rows, edited); err != nil {
			return classicMergeState{}, err
		}
		state.rows = edited
	}
	// Original complete reads remain in the tree; this effective result is a
	// coverage description, not a new RF acquisition or snapshot guarantee.
	if r.result.Read > state.result.Read {
		state.result.Read = r.result.Read
	}
	data := make(HexBytes, state.result.Read*16)
	for block := 0; block < state.result.Read; block++ {
		for at := 0; at < 16; at++ {
			pair := state.rows[block][at*2 : at*2+2]
			if pair != "??" {
				b, _ := hex.DecodeString(pair)
				data[block*16+at] = b[0]
			}
		}
	}
	dump := *state.result.Dump
	dump.Data = data
	dump.SHA256 = imageDigest(data)
	dump.UnknownKeys = nil
	sectors := map[uint16]int{1000: 5, 1001: 16, 1002: 32, 1003: 40}[dump.TagType]
	for sector := 0; sector < sectors; sector++ {
		first, count, _ := ClassicSectorGeometry(sector)
		trailer := first + count - 1
		if trailer >= state.result.Read {
			continue
		}
		for side, offset := range []int{0, 20} {
			if strings.Contains(state.rows[trailer][offset:offset+12], "??") {
				kind := KeyA
				if side == 1 {
					kind = KeyB
				}
				dump.UnknownKeys = append(dump.UnknownKeys, ClassicUnknownKey{sector, kind})
			}
		}
	}
	state.result.Dump = &dump
	state.result.Complete = state.authority && state.result.Read == state.result.Requested
	return state, nil
}

// ClassicMergePreview reports byte conflicts without guessing how to resolve
// them. Actual merge additionally requires explicit same-card acknowledgement.
func (c GUICard) ClassicMergePreview(other GUICard) (ClassicMergePreview, error) {
	left, err := c.classicMergeTree()
	if err != nil {
		return ClassicMergePreview{}, err
	}
	right, err := other.classicMergeTree()
	if err != nil {
		return ClassicMergePreview{}, err
	}
	tree := &classicMergeNode{Left: left, Right: right, Options: &ClassicMergeOptions{ConfirmSameCard: true}}
	state, err := evaluateClassicMerge(tree, 0, new(int), false)
	if err != nil {
		return ClassicMergePreview{}, err
	}
	known := 0
	for _, row := range state.rows {
		for at := 0; at < len(row); at += 2 {
			if row[at:at+2] != "??" {
				known++
			}
		}
	}
	return ClassicMergePreview{Schema: 1, Conflicts: state.conflicts, FilledBytes: state.filled, KnownBytes: known, TotalBytes: len(state.rows) * 16, HasCompleteRead: state.authority}, nil
}

// ValidateClassicMerge checks identity, geometry and exact conflict choices without
// allocating a card ID or changing either source.
func (c GUICard) ValidateClassicMerge(other GUICard, o ClassicMergeOptions) error {
	if !o.ConfirmSameCard {
		return fmt.Errorf("explicit same-card confirmation is required; UID matching is insufficient")
	}
	left, err := c.classicMergeTree()
	if err != nil {
		return err
	}
	right, err := other.classicMergeTree()
	if err != nil {
		return err
	}
	_, err = evaluateClassicMerge(&classicMergeNode{Left: left, Right: right, Options: &o}, 0, new(int), true)
	return err
}
func (c GUICard) MergeClassicDraft(other GUICard, o ClassicMergeOptions) (GUICard, error) {
	if !o.ConfirmSameCard {
		return GUICard{}, fmt.Errorf("explicit same-card confirmation is required; UID matching is insufficient")
	}
	left, err := c.classicMergeTree()
	if err != nil {
		return GUICard{}, err
	}
	right, err := other.classicMergeTree()
	if err != nil {
		return GUICard{}, err
	}
	root := &classicMergeNode{Left: left, Right: right, Options: &o}
	state, err := evaluateClassicMerge(root, 0, new(int), true)
	if err != nil {
		return GUICard{}, err
	}
	return c.withClassicMergeRoot(root, state)
}
func (c GUICard) withClassicMergeRoot(root *classicMergeNode, state classicMergeState) (GUICard, error) {
	var err error
	out := c.withField("desk_classic_merge", classicMergeEvidence{Schema: 1, Root: root})
	var saved map[string]json.RawMessage
	if err = json.Unmarshal(c.fields["desk_read"], &saved); err != nil {
		return GUICard{}, err
	}
	saved["rows"], _ = json.Marshal(state.rows)
	out = out.withField("desk_read", saved)
	out.ID, err = NewGUIID()
	if err != nil {
		return GUICard{}, err
	}
	return out, out.Validate()
}

// ClassicRepairRows is the ReadDraft/EditData validation hook. It permits
// unknown-byte completion only when a bounded, validated evidence tree supplies
// those bytes. Ordinary drafts retain their existing mask.
func (c GUICard) ClassicRepairRows(d ReadCardData, original []string) ([]string, error) {
	raw, ok := c.fields["desk_classic_merge"]
	if !ok {
		return original, nil
	}
	if d.Result.Kind != "classic" {
		return nil, fmt.Errorf("Classic merge metadata on a non-Classic draft")
	}
	var e classicMergeEvidence
	if err := json.Unmarshal(raw, &e); err != nil || e.Schema != 1 {
		return nil, fmt.Errorf("invalid Classic merge evidence")
	}
	state, err := evaluateClassicMerge(e.Root, 0, new(int), true)
	if err != nil {
		return nil, err
	}
	if err := validateClassicMergeBase(e.Root, d); err != nil {
		return nil, err
	}
	if state.result.Requested != d.Result.Requested || state.result.Dump.TagType != d.Result.Dump.TagType || !sameClassicIdentity(state.result.Identity, d.Result.Identity) {
		return nil, fmt.Errorf("Classic merge evidence does not match the draft")
	}
	if err := classicMergeKeyEdits(state.rows, d.Rows); err != nil {
		return nil, err
	}
	return state.rows, nil
}

// ClassicRepairEffectiveRead is the promotion eligibility hook. Source errors
// are retained in evidence. Without a successful full-read source it reports an
// incomplete effective result even if partial observations fill every byte.
func (c GUICard) ClassicRepairEffectiveRead(d ReadCardData) (ReadResult, string, error) {
	raw, ok := c.fields["desk_classic_merge"]
	if !ok {
		return d.Result, d.Error, nil
	}
	var e classicMergeEvidence
	if err := json.Unmarshal(raw, &e); err != nil || e.Schema != 1 {
		return ReadResult{}, "", fmt.Errorf("invalid Classic merge evidence")
	}
	state, err := evaluateClassicMerge(e.Root, 0, new(int), true)
	if err != nil {
		return ReadResult{}, "", err
	}
	if err := validateClassicMergeBase(e.Root, d); err != nil {
		return ReadResult{}, "", err
	}
	message := ""
	if !state.authority {
		message = "merged observations have no complete successful full-read source; perform a new read before conversion"
	}
	return state.result, message, nil
}

func classicMergeKeyEdits(before, after []string) error {
	canonical, err := cleanReadRows(after, before)
	if err != nil {
		return err
	}
	after = canonical
	if len(before) != len(after) {
		return fmt.Errorf("Classic merged row count differs")
	}
	for sector := 0; sector < 40; sector++ {
		first, count, _ := ClassicSectorGeometry(sector)
		at := first + count - 1
		if at >= len(before) {
			break
		}
		if len(before[at]) != 32 || len(after[at]) != 32 {
			return fmt.Errorf("invalid Classic trailer row")
		}
		for _, offset := range []int{0, 20} {
			if before[at][offset:offset+12] != after[at][offset:offset+12] {
				return fmt.Errorf("Classic merge cannot use edited key bytes as verification evidence; reread or verify the keys")
			}
		}
	}
	return nil
}

func validateClassicMergeBase(n *classicMergeNode, d ReadCardData) error {
	for depth := 0; n != nil && depth <= 8; depth++ {
		if n.Read != nil {
			a, _ := json.Marshal(n.Read.Result)
			b, _ := json.Marshal(d.Result)
			if !bytes.Equal(a, b) || n.Read.Error != d.Error {
				return fmt.Errorf("Classic merge original observation was changed")
			}
			return nil
		}
		n = n.Left
	}
	return fmt.Errorf("Classic merge has no original observation")
}
