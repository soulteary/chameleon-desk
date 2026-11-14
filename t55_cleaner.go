package chameleon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
)

// T55xx password recovery mirrors the upstream GUI "T55xx password cleaner":
// it tries each candidate password by writing a probe EM410x ID and reading it
// back. A correct password lets the write land, so the probe ID reads back and
// the card is left with the chosen new password. This writes the card on every
// attempt and needs explicit authorization.

// T55CleanResult reports the outcome of the password search.
type T55CleanResult struct {
	Found       bool     `json:"found"`
	Password    HexBytes `json:"password,omitempty"`
	Attempts    int      `json:"attempts"`
	OriginalID  HexBytes `json:"original_id,omitempty"`
	ProbeID     HexBytes `json:"probe_id"`
	NewPassword HexBytes `json:"new_password"`
}

// CleanT55Password tries candidates one at a time. progress runs BEFORE each
// write (which is destructive) and lets the caller stop. A correct password
// writes probeID and sets newPassword; probeID must differ from the current ID
// so a successful write is distinguishable. allowWrite must be true.
func (c *Client) CleanT55Password(ctx context.Context, candidates [][]byte, probeID, newPassword []byte, allowWrite bool, progress func(attempt int, password []byte) error) (T55CleanResult, error) {
	out := T55CleanResult{ProbeID: append(HexBytes(nil), probeID...), NewPassword: append(HexBytes(nil), newPassword...)}
	if !allowWrite || progress == nil {
		return out, fmt.Errorf("T55xx password cleaning writes the card; it needs explicit authorization and a progress callback")
	}
	if len(probeID) != 5 || len(newPassword) != 4 {
		return out, fmt.Errorf("probe ID must be a 5-byte EM410x ID and the new password 4 bytes")
	}
	if len(candidates) < 1 || len(candidates) > 100000 {
		return out, fmt.Errorf("provide 1..100000 candidate passwords")
	}
	for _, k := range candidates {
		if len(k) != 4 {
			return out, fmt.Errorf("each candidate password must be 4 bytes")
		}
	}
	// A best-effort read of the current ID; a probe equal to it cannot be told
	// apart from a failed write, so that is rejected.
	if current, e := c.ScanLF(ctx, "em410x", 0); e == nil {
		out.OriginalID = append(HexBytes(nil), current.ID...)
		if bytes.Equal(current.ID, probeID) {
			return out, fmt.Errorf("probe ID equals the card's current ID; choose a different --probe-id so a successful write is detectable")
		}
	} else if !errors.Is(e, ErrNoCard) {
		return out, e
	}
	for i, candidate := range candidates {
		out.Attempts = i + 1
		if e := progress(out.Attempts, candidate); e != nil {
			return out, e
		}
		if e := c.WriteT55(ctx, "em410x", probeID, newPassword, [][]byte{candidate}); e != nil {
			// A write rejected by the firmware is not a fatal error; the
			// password was simply wrong. Only a transport error aborts.
			var status *StatusError
			if errors.As(e, &status) {
				continue
			}
			return out, e
		}
		readBack, e := c.ScanLF(ctx, "em410x", 0)
		if e != nil {
			if errors.Is(e, ErrNoCard) {
				continue
			}
			return out, e
		}
		if bytes.Equal(readBack.ID, probeID) {
			out.Found = true
			out.Password = append(HexBytes(nil), candidate...)
			return out, nil
		}
	}
	return out, nil
}
