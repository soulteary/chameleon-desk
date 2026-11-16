package chameleon

import (
	"bytes"
	"context"
	"fmt"
	"time"
)

// ClassicDictionaryRequest is an explicit bounded key search. It does not run
// nested recovery, reuse saved credentials or update a card-library entry.
type ClassicDictionaryRequest struct {
	Identity     ClassicIdentity `json:"identity"`
	Sectors      int             `json:"sectors"`
	Keys         []HexBytes      `json:"keys"`
	LimitSeconds int             `json:"limit_seconds"`
}
type ClassicDictionaryResult struct {
	Schema   int             `json:"schema"`
	Identity ClassicIdentity `json:"identity"`
	Complete bool            `json:"complete"`
	Verified bool            `json:"verified_on_card"`
	KeyMap   ClassicKeyMap   `json:"key_map"`
}

func (r ClassicDictionaryRequest) Requirements() ([]Command, error) {
	if err := r.Identity.Validate(); err != nil {
		return nil, err
	}
	if r.Sectors != 5 && r.Sectors != 16 && r.Sectors != 32 && r.Sectors != 40 {
		return nil, fmt.Errorf("Classic dictionary geometry requires 5, 16, 32 or 40 sectors")
	}
	if len(r.Keys) < 1 || len(r.Keys) > 4096 || r.LimitSeconds < 1 || r.LimitSeconds > 30 {
		return nil, fmt.Errorf("dictionary requires 1..4096 explicit keys and a 1..30 second limit")
	}
	seen := map[string]bool{}
	for _, key := range r.Keys {
		if len(key) != 6 || seen[string(key)] {
			return nil, fmt.Errorf("dictionary requires distinct six-byte keys")
		}
		seen[string(key)] = true
	}
	return []Command{CmdChangeDeviceMode, CmdGetDeviceMode, CmdHf14aScan, CmdMf1AuthOneKeyBlock, CmdMf1ReadOneBlock, CmdMf1CheckKeysOfSectors}, nil
}
func (c *Client) CheckClassicDictionaryBound(ctx context.Context, r ClassicDictionaryRequest) (out ClassicDictionaryResult, err error) {
	if _, err = r.Requirements(); err != nil {
		return
	}
	// Validate the existing firmware helper's command budget before touching RF.
	minimum := time.Second + time.Duration(2*r.Sectors+1)*100*time.Millisecond
	if c.timeout < minimum {
		return out, fmt.Errorf("sector dictionary check needs per-command timeout at least %s", minimum)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.LimitSeconds)*time.Second)
	defer cancel()
	out = ClassicDictionaryResult{Schema: 1, Identity: r.Identity}
	expected := r.Identity
	if err = c.Reader(ctx, func(s *ReaderSession) error { return observeClassicIdentity(ctx, s, r.Identity.UID, &expected) }); err != nil {
		return
	}
	out.KeyMap, err = c.CheckClassicDictionary(ctx, r.Sectors, r.Keys)
	if err != nil {
		return
	}
	if !bytes.Equal(out.KeyMap.UID, r.Identity.UID) {
		return out, fmt.Errorf("dictionary result UID differs from expected full UID")
	}
	err = c.Reader(ctx, func(s *ReaderSession) error { return observeClassicIdentity(ctx, s, r.Identity.UID, &expected) })
	out.Complete = err == nil
	out.Verified = out.Complete
	return
}
