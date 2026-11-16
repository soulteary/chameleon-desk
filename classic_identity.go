package chameleon

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
)

// ClassicIdentity is an observed anticollision response. ATQA uses numeric
// (GUI) byte order, not wire order. A non-nil empty ATS records observed absence.
// Pointer fields distinguish missing metadata from an observed zero/empty value.
type ClassicIdentity struct {
	Schema int       `json:"schema"`
	UID    HexBytes  `json:"uid"`
	ATQA   HexBytes  `json:"atqa"`
	SAK    *byte     `json:"sak"`
	ATS    *HexBytes `json:"ats"`
}

func (i ClassicIdentity) Validate() error {
	if i.Schema != 1 || (len(i.UID) != 4 && len(i.UID) != 7 && len(i.UID) != 10) || len(i.ATQA) != 2 || i.SAK == nil || i.ATS == nil || len(*i.ATS) > 254 {
		return fmt.Errorf("Classic identity requires schema 1, UID, two ATQA bytes, SAK and explicit ATS")
	}
	return nil
}

func observeClassicIdentity(ctx context.Context, s *ReaderSession, uid []byte, identity *ClassicIdentity) error {
	b, err := s.expect(ctx, CmdHf14aScan, nil, 0)
	if err != nil {
		var status *StatusError
		if errors.As(err, &status) && status.Status == 1 {
			return ErrNoCard
		}
		return err
	}
	cards, err := parseCards(b)
	if err != nil {
		return err
	}
	if len(cards) != 1 || !bytes.Equal(cards[0].UID, uid) {
		return fmt.Errorf("card changed during Classic read; exactly one matching UID required")
	}
	card := cards[0]
	atqa := make(HexBytes, 2)
	binary.BigEndian.PutUint16(atqa, card.ATQA)
	ats := append(HexBytes{}, card.ATS...)
	sak := card.SAK
	current := ClassicIdentity{Schema: 1, UID: append(HexBytes(nil), card.UID...), ATQA: atqa, SAK: &sak, ATS: &ats}
	if err := current.Validate(); err != nil {
		return err
	}
	if identity.Schema == 0 {
		*identity = current
		return nil
	}
	if !bytes.Equal(identity.UID, current.UID) || !bytes.Equal(identity.ATQA, current.ATQA) || *identity.SAK != sak || !bytes.Equal(*identity.ATS, ats) {
		return fmt.Errorf("Classic identity changed during read")
	}
	return nil
}

// ReadClassicWithIdentity reads under one Reader operation, observes identity
// before every sector and after the final block, and includes cleanup in Complete.
// Requested geometry and matching responses are not physical-card authentication.
// DumpClassicWithKeys retains its existing SDK/CLI command sequence and format.
func (c *Client) ReadClassicWithIdentity(ctx context.Context, r ClassicRead) (ReadResult, error) {
	keys, err := r.KeyMap()
	if err != nil {
		return ReadResult{}, err
	}
	first, count, _ := ClassicSectorGeometry(len(keys.Sectors) - 1)
	var identity ClassicIdentity
	dump, err := c.dumpClassicWithKeys(ctx, keys, &identity)
	// A prefix has its own checksum; it never becomes a complete Classic image.
	if dump.Schema == 2 {
		dump.SHA256 = imageDigest(dump.Data)
	}
	out := ReadResult{Schema: 1, Kind: "classic", Complete: err == nil, Requested: first + count, Read: len(dump.Data) / 16, UID: dump.UID, Dump: &dump}
	if identity.Schema == 1 {
		out.Identity = &identity
	}
	return out, err
}
