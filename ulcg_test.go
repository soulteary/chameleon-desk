package chameleon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestULCGRequiresErasureAndDurableJournal(t *testing.T) {
	c := readerSimulator(t, nil)
	if _, e := c.CollectULCG(context.Background(), 10, false, func(ULCGChallenges) error { return nil }); e == nil {
		t.Fatal("unapproved erasure accepted")
	}
	if _, e := c.CollectULCG(context.Background(), 10, true, nil); e == nil {
		t.Fatal("unjournaled erasure accepted")
	}
}
func ulcgRaw(command []byte, crc bool) []byte {
	flags := byte(0xf0)
	if crc {
		flags |= 4
	}
	bits := len(command) * 8
	return append([]byte{flags, 1, 244, byte(bits >> 8), byte(bits)}, command...)
}
func TestULCGJournalPrecedesEachKeyWrite(t *testing.T) {
	scan := []byte{4, 1, 2, 3, 4, 4, 0, 0, 0}
	config := make([]byte, 16)
	config[8] = 48
	challenge := append([]byte{0xaf}, bytes.Repeat([]byte{1}, 8)...)
	calls := 0
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: scan}, {command: 2010, request: ulcgRaw([]byte{0x30, 40}, true), data: config}, {command: 2010, request: ulcgRaw([]byte{0x1a, 0}, true), data: challenge}, {command: 2010, request: ulcgRaw([]byte{0x1a, 0}, true), data: challenge}}
	for i, page := range []byte{47, 46, 45, 44} {
		required := 2 + i*2
		steps = append(steps, readerStep{command: 2000, data: scan}, readerStep{command: 2010, request: ulcgRaw([]byte{0xa2, page, 0, 0, 0, 0}, false), data: []byte{10}, before: func() {
			if calls < required {
				t.Error("key write occurred before durable journal")
			}
		}}, readerStep{command: 2010, request: ulcgRaw([]byte{0x1a, 0}, true), data: challenge})
	}
	steps = append(steps, readerStep{command: 2101, status: 0x68})
	c := readerSimulator(t, steps)
	v, e := c.CollectULCG(context.Background(), 2, true, func(ULCGChallenges) error { calls++; return nil })
	if e != nil || calls != 9 || len(v.PossiblyErasedPages) != 4 {
		t.Fatal(v, calls, e)
	}
	if e = v.Validate(); e != nil {
		t.Fatal(e)
	}
}
func TestOfflineULCGRecoveryDoesNotClaimAuthentication(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "mfulc_des_brute"), []byte("#!/bin/sh\nprintf 'Full key (hex): 00112233445566778899aabbccddeeff\\n'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	nonce := make(HexBytes, 8)
	v := ULCGChallenges{Schema: 1, UID: HexBytes{1, 2, 3, 4}, Challenge100: nonce, Challenge75: nonce, Challenge50: nonce, Challenge25: nonce, Challenge0: nonce}
	r, e := RecoverULCG(context.Background(), v, dir, 1, 10*time.Second)
	if e != nil || r.Candidate.String() != "00112233445566778899AABBCCDDEEFF" || r.VerifiedOnCard {
		t.Fatal(r, e)
	}
}
