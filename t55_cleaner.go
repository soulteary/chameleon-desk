package chameleon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
)

// T55 password checking writes a probe identity and changes the password. One
// candidate is permitted per explicit invocation. An error or unconfirmed
// readback never starts another candidate write.

// T55CleanResult reports the outcome of the password search.
type T55CleanResult struct {
	WriteAttempted bool     `json:"write_attempted"`
	Outcome        string   `json:"outcome"`
	Found          bool     `json:"found"`
	Password       HexBytes `json:"password,omitempty"`
	Attempts       int      `json:"attempts"`
	OriginalID     HexBytes `json:"original_id,omitempty"`
	ProbeID        HexBytes `json:"probe_id"`
	NewPassword    HexBytes `json:"new_password"`
}

// CleanT55Password accepts exactly one candidate. progress runs BEFORE each
// write (which is destructive) and lets the caller stop. A correct password
// writes probeID and sets newPassword; probeID must differ from the current ID
// so a successful write is distinguishable. allowWrite must be true.
func (c *Client) CleanT55Password(ctx context.Context, candidates [][]byte, probeID, newPassword []byte, allowWrite bool, progress func(attempt int, password []byte) error) (out T55CleanResult, err error) {
	defer func() { out.Outcome = finishWriteOutcome(out.WriteAttempted, &err) }()
	out = T55CleanResult{ProbeID: append(HexBytes(nil), probeID...), NewPassword: append(HexBytes(nil), newPassword...)}
	if !allowWrite || progress == nil {
		return out, fmt.Errorf("T55xx password cleaning writes the card; it needs explicit authorization and a progress callback")
	}
	if len(probeID) != 5 || len(newPassword) != 4 {
		return out, fmt.Errorf("probe ID must be a 5-byte EM410x ID and the new password 4 bytes")
	}
	if len(candidates) != 1 {
		return out, fmt.Errorf("provide exactly one candidate password; inspect the target before a separate explicit attempt")
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
	candidate := candidates[0]
	out.Attempts = 1
	if e := progress(out.Attempts, candidate); e != nil {
		return out, e
	}
	out.WriteAttempted = true
	if e := c.WriteT55(ctx, "em410x", probeID, newPassword, [][]byte{candidate}); e != nil {
		// A failed WRITE is not evidence that another password is safe.
		return out, e
	}
	readBack, e := c.ScanLF(ctx, "em410x", 0)
	if e != nil {
		if errors.Is(e, ErrNoCard) {
			return out, fmt.Errorf("T55 write acknowledged but readback is unavailable; write outcome unknown: %w", e)
		}
		return out, e
	}
	if bytes.Equal(readBack.ID, probeID) {
		out.Found = true
		out.Password = append(HexBytes(nil), candidate...)
		return out, nil
	}
	return out, fmt.Errorf("T55 write acknowledged but probe identity was not confirmed; write outcome unknown")
}
