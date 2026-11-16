package chameleon

import (
	"bytes"
	"fmt"
	"strings"
)

type CardByteDifference struct {
	Unit  int    `json:"unit"`
	Byte  int    `json:"byte"`
	Left  string `json:"left"`
	Right string `json:"right"`
	State string `json:"state"`
}
type CardComparison struct {
	Schema             int                  `json:"schema"`
	Compatible         bool                 `json:"geometry_compatible"`
	Reasons            []string             `json:"reasons"`
	Differences        []CardByteDifference `json:"differences"`
	UnknownComparisons int                  `json:"unknown_comparisons"`
}

func cardComparisonRows(c GUICard) ([]string, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	draft, err := c.ReadDraft()
	if err != nil {
		return nil, err
	}
	if draft != nil {
		return draft.Rows, nil
	}
	n := len(c.Data)
	width := 16
	if count, ok := guiClassicBlocks[c.Tag]; ok {
		n = max(n, count)
		if c.Tag == 1001 && len(c.Data) > 64 {
			n = max(n, 128)
		}
	}
	if count, ok := guiType2Pages[c.Tag]; ok {
		n = max(n, count)
		width = 4
	}
	rows := make([]string, n)
	for i := range rows {
		rows[i] = strings.Repeat("??", width)
		if i < len(c.Data) && len(c.Data[i]) > 0 {
			rows[i] = HexBytes(c.Data[i]).String()
		}
	}
	if _, ok := LFCardProtocol(c.Tag); ok {
		uid, err := guiUIDBytes(c.UID)
		if err != nil {
			return nil, err
		}
		return []string{HexBytes(uid).String()}, nil
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("card has no comparable memory")
	}
	return rows, nil
}

// CompareCards reports unknown/absent bytes as unknown, never as equal zeroes.
// It does not merge evidence, infer a shared physical source or modify either file.
func CompareCards(left, right GUICard) (CardComparison, error) {
	out := CardComparison{Schema: 1, Compatible: true, Reasons: []string{}, Differences: []CardByteDifference{}}
	a, err := cardComparisonRows(left)
	if err != nil {
		return out, err
	}
	b, err := cardComparisonRows(right)
	if err != nil {
		return out, err
	}
	if len(a) != len(b) {
		out.Compatible = false
		out.Reasons = append(out.Reasons, "Memory row counts differ")
	}
	if left.Tag != right.Tag {
		out.Reasons = append(out.Reasons, "Card types/source kinds differ; a byte comparison is not shared-source evidence")
	}
	leftID, _ := guiUIDBytes(left.UID)
	rightID, _ := guiUIDBytes(right.UID)
	if !bytes.Equal(leftID, rightID) {
		out.Reasons = append(out.Reasons, "Declared identities differ")
	}
	for i := 0; i < max(len(a), len(b)); i++ {
		var x, y string
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if len(x) != len(y) {
			out.Compatible = false
		}
		for j := 0; j < max(len(x), len(y)); j += 2 {
			p, q := "??", "??"
			if j+2 <= len(x) {
				p = x[j : j+2]
			}
			if j+2 <= len(y) {
				q = y[j : j+2]
			}
			state := "changed"
			if p == "??" || q == "??" {
				state = "unknown"
				out.UnknownComparisons++
			} else if p == q {
				continue
			}
			out.Differences = append(out.Differences, CardByteDifference{i, j / 2, p, q, state})
			if len(out.Differences) > 65536 {
				return out, fmt.Errorf("comparison exceeds byte limit")
			}
		}
	}
	return out, nil
}
