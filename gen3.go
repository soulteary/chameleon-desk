package chameleon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
)

// Gen3 (APDU UID-change magic MIFARE Classic) support mirrors the upstream GUI
// gen3 writer. Block 0 and the UID are changed with special 0x90 0xFB APDUs,
// and the UID can be locked permanently with 0x90 0xFD. Data blocks use normal
// authenticated writes (see mfc), so only the block-0/UID/lock parts live here.

// gen3Send issues one Gen3 APDU with a fresh select. Gen3 cards often reboot
// and stop answering after a write, so a "no tag" status is treated as success
// for the write APDUs; the caller verifies by re-scanning.
func gen3Send(ctx context.Context, s *ReaderSession, frame []byte, tolerateNoTag bool) (HexBytes, error) {
	out, e := s.Raw14A(ctx, frame, RawOptions{ActivateField: true, WaitResponse: true, AppendCRC: true, AutoSelect: true, KeepField: true, TimeoutMS: 1000})
	if e != nil && tolerateNoTag {
		var status *StatusError
		if errors.As(e, &status) && (status.Status == 0x01 || status.Status == 0x41) {
			return nil, nil
		}
	}
	return out, e
}

// DetectGen3 reports whether a MIFARE Classic answering block-0 reads is present.
// Gen3 cannot be positively identified without writing, so this only confirms a
// readable Classic, as the GUI does.
func (c *Client) DetectGen3(ctx context.Context) (classic bool, err error) {
	err = c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		out, e := s.Raw14A(ctx, []byte{0x30, 0x00}, RawOptions{ActivateField: true, WaitResponse: true, AppendCRC: true, AutoSelect: true, KeepField: true, TimeoutMS: 1000})
		if e != nil {
			var status *StatusError
			if errors.As(e, &status) {
				return nil // not a readable Classic: clean false
			}
			return e
		}
		classic = len(out) == 16 || len(out) == 18
		return nil
	})
	return
}

// Gen3WriteBlock0 writes a full 16-byte block 0 (UID/BCC/SAK/ATQA/manufacturer)
// via the Gen3 APDU, without changing other blocks. This changes the card.
func (c *Client) Gen3WriteBlock0(ctx context.Context, data []byte) error {
	if len(data) != 16 {
		return fmt.Errorf("block 0 must be 16 bytes")
	}
	return c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		frame := append([]byte{0x90, 0xFB, 0xCC, 0xCC, 0x10}, data...)
		_, e = gen3Send(ctx, s, frame, true)
		return e
	})
}

// Gen3SetUID changes only the UID (4 or 7 bytes) via the Gen3 APDU; the card
// recomputes block 0. It re-scans after the card reboots and returns the UID
// read back so the caller can confirm the change.
func (c *Client) Gen3SetUID(ctx context.Context, uid []byte) (readBack HexBytes, err error) {
	if len(uid) != 4 && len(uid) != 7 {
		return nil, fmt.Errorf("Gen3 UID must be 4 or 7 bytes")
	}
	err = c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		frame := append([]byte{0x90, 0xFB, 0xCC, 0xCC, byte(len(uid))}, uid...)
		if _, e = gen3Send(ctx, s, frame, true); e != nil {
			return e
		}
		scanned, e := scanSession(ctx, s)
		if e != nil {
			if errors.Is(e, ErrNoCard) {
				return fmt.Errorf("card did not re-appear after the UID change; re-scan manually")
			}
			return e
		}
		readBack = append(HexBytes(nil), scanned...)
		if !bytes.Equal(readBack, uid) {
			return fmt.Errorf("UID read back as %s, not %s", readBack, HexBytes(uid))
		}
		return nil
	})
	return
}

// Gen3Lock permanently locks the UID (APDU 0x90 0xFD). This is irreversible:
// the card's UID can never be changed again.
func (c *Client) Gen3Lock(ctx context.Context) error {
	return c.Reader(ctx, func(s *ReaderSession) (e error) {
		defer s.fieldCleanup(&e)
		_, e = gen3Send(ctx, s, []byte{0x90, 0xFD, 0x11, 0x11, 0x00}, true)
		return e
	})
}
