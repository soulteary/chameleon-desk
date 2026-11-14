package chameleon

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrNoCard = errors.New("no card detected")
var ErrReaderUnsupported = errors.New("device does not support ISO14443-A reader commands")

// HexBytes preserves raw bytes while encoding JSON as uppercase hexadecimal.
type HexBytes []byte

func (b HexBytes) String() string               { return strings.ToUpper(hex.EncodeToString(b)) }
func (b HexBytes) MarshalJSON() ([]byte, error) { return json.Marshal(b.String()) }

func (b *HexBytes) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	decoded, err := hex.DecodeString(text)
	if err != nil {
		return fmt.Errorf("invalid hexadecimal bytes: %w", err)
	}
	*b = decoded
	return nil
}

type Card struct {
	UID HexBytes `json:"uid"`
	// ATQA is the conventional numeric display; ATQAWire preserves wire order.
	ATQA      uint16   `json:"atqa"`
	ATQAWire  HexBytes `json:"atqa_wire"`
	SAK       byte     `json:"sak"`
	ATS       HexBytes `json:"ats,omitempty"`
	Type      string   `json:"type"`
	TypeBasis string   `json:"type_basis"`
}

// ModeRestoreError means scan cleanup failed. Inspect the device mode before
// further use; if the connection was lost, reopen it first.
type ModeRestoreError struct{ Err error }

func (e *ModeRestoreError) Error() string {
	return "could not restore original device mode; verify mode after reconnecting: " + e.Err.Error()
}
func (e *ModeRestoreError) Unwrap() error { return e.Err }

// ScanISO14443A discovers cards without reading or writing card memory. It
// temporarily enables reader mode when needed and restores the original mode.
// All calls on this Client wait until the whole sequence (including cleanup)
// completes. Card type is a SAK-based family hint, not chip authentication.
func (c *Client) ScanISO14443A(ctx context.Context) (cards []Card, err error) {
	queued, cancel := context.WithTimeout(ctx, c.timeout)
	err = c.acquire(queued)
	cancel()
	if err != nil {
		return nil, err
	}
	defer c.release()

	caps, err := c.exchange(ctx, 1035, nil, 0x68)
	if err != nil {
		return nil, err
	}
	if len(caps)%2 != 0 {
		return nil, fmt.Errorf("odd capabilities payload length")
	}
	supported := map[uint16]bool{}
	for i := 0; i < len(caps); i += 2 {
		supported[binary.BigEndian.Uint16(caps[i:])] = true
	}
	for _, cmd := range []uint16{1001, 1002, 2000} {
		if !supported[cmd] {
			return nil, ErrReaderUnsupported
		}
	}

	mode, err := c.exchange(ctx, 1002, nil, 0x68)
	if err != nil {
		return nil, err
	}
	if len(mode) != 1 || mode[0] > 1 {
		return nil, fmt.Errorf("invalid device mode payload: %x", mode)
	}
	if mode[0] == 0 {
		// Arm cleanup before sending: even a lost switch response may mean
		// the device changed mode. A fresh context allows cleanup after cancel.
		defer func() {
			restoreCtx, stop := context.WithTimeout(context.Background(), c.timeout)
			defer stop()
			if _, e := c.exchange(restoreCtx, 1001, []byte{0}, 0x68); e != nil {
				err = errors.Join(err, &ModeRestoreError{Err: e})
			}
		}()
		if _, err = c.exchange(ctx, 1001, []byte{1}, 0x68); err != nil {
			return nil, err
		}
	}
	payload, err := c.exchange(ctx, 2000, nil, 0x00)
	if err != nil {
		var status *StatusError
		if errors.As(err, &status) && status.Status == 0x01 {
			return nil, ErrNoCard
		}
		return nil, err
	}
	return parseCards(payload)
}

func parseCards(payload []byte) ([]Card, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("scan succeeded with an empty card payload")
	}
	cards := make([]Card, 0, 1)
	for offset := 0; offset < len(payload); {
		uidLen := int(payload[offset])
		offset++
		if uidLen != 4 && uidLen != 7 && uidLen != 10 {
			return nil, fmt.Errorf("invalid UID length %d", uidLen)
		}
		if len(payload)-offset < uidLen+4 {
			return nil, fmt.Errorf("truncated card record at offset %d", offset-1)
		}
		uid := append(HexBytes(nil), payload[offset:offset+uidLen]...)
		offset += uidLen
		atqa := append(HexBytes(nil), payload[offset:offset+2]...)
		offset += 2
		sak, atsLen := payload[offset], int(payload[offset+1])
		offset += 2
		if len(payload)-offset < atsLen {
			return nil, fmt.Errorf("truncated ATS at offset %d", offset)
		}
		ats := append(HexBytes(nil), payload[offset:offset+atsLen]...)
		offset += atsLen
		cards = append(cards, Card{UID: uid, ATQA: binary.LittleEndian.Uint16(atqa), ATQAWire: atqa, SAK: sak, ATS: ats, Type: cardType(sak), TypeBasis: "SAK-based inference; exact chip model not verified"})
	}
	return cards, nil
}

// NXP AN10833 recommends interpreting SAK as a bit map and explicitly warns
// against identifying a chip from ATQA. Multiple families can share a SAK.
func cardType(sak byte) string {
	if sak&0x04 != 0 {
		return "ISO14443-A (UID cascade incomplete)"
	}
	families := []string{}
	if sak&0x08 != 0 {
		switch {
		case sak&0x1f == 0x09:
			families = append(families, "MIFARE Mini-compatible")
		case sak&0x10 != 0:
			families = append(families, "MIFARE Classic 4K-compatible / Plus SL1")
		default:
			families = append(families, "MIFARE Classic 1K-compatible / Plus SL1")
		}
	}
	if sak&0x20 != 0 {
		families = append(families, "ISO14443-4 (DESFire / Plus / other)")
	}
	if sak&0x40 != 0 {
		families = append(families, "ISO18092 peer-to-peer")
	}
	if len(families) > 0 {
		return strings.Join(families, "; ")
	}
	switch sak & 0x7f {
	case 0:
		return "Ultralight / NTAG-compatible (exact model unknown)"
	case 0x10, 0x11:
		return "MIFARE Plus-compatible (exact model unknown)"
	default:
		return "ISO14443-A (unknown family)"
	}
}
