package chameleon

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestClassicAccessRedundancy(t *testing.T) {
	v := make([]byte, 16)
	copy(v[6:9], []byte{255, 7, 128})
	if e := ValidateClassicAccess(v); e != nil {
		t.Fatal(e)
	}
	for _, i := range []int{6, 7, 8} {
		b := append([]byte(nil), v...)
		b[i] ^= 1
		if e := ValidateClassicAccess(b); e == nil {
			t.Fatal("bad ACL accepted")
		}
	}
}
func TestClassicCloneDataOnlyAndReadback(t *testing.T) {
	data := bytes.Repeat([]byte{42}, 320)
	dump := ClassicCardDump{Schema: 1, UID: HexBytes{9, 8, 7, 6}, TagType: 1000, Data: data, SHA256: imageDigest(data)}
	km := ClassicKeyMap{Schema: 1, UID: HexBytes{1, 2, 3, 4}, Sectors: make([]SectorKeys, 5)}
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}}
	for sector := 0; sector < 5; sector++ {
		key := make([]byte, 6)
		km.Sectors[sector] = SectorKeys{Sector: sector, A: key}
		steps = append(steps, readerStep{command: 2000, data: []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}})
		for block := sector * 4; block < sector*4+3; block++ {
			if block == 0 {
				continue
			}
			p := append([]byte{0x60, byte(block)}, key...)
			steps = append(steps, readerStep{command: 2007, request: p}, readerStep{command: 2009, request: append(append([]byte(nil), p...), data[block*16:(block+1)*16]...)}, readerStep{command: 2008, request: p, data: data[block*16 : (block+1)*16]})
		}
	}
	c := readerSimulator(t, steps)
	v, e := c.CloneClassic(context.Background(), dump, km, ClassicCloneOptions{})
	if e != nil || v.WrittenBlocks != 14 || v.VerifiedBlocks != 14 || !v.ManufacturerSkipped || v.TrailersWritten {
		t.Fatal(v, e)
	}
}

func TestClassicCloneDefaultNeverRetriesWriteWithAnotherKey(t *testing.T) {
	for _, status := range []uint16{2, 6} {
		t.Run(string(rune('0'+status)), func(t *testing.T) {
			data := bytes.Repeat([]byte{42}, 320)
			dump := ClassicCardDump{Schema: 1, UID: HexBytes{9, 8, 7, 6}, TagType: 1000, Data: data, SHA256: imageDigest(data)}
			target := ClassicKeyMap{Schema: 1, Complete: true, UID: HexBytes{1, 2, 3, 4}}
			for i := 0; i < 5; i++ {
				target.Sectors = append(target.Sectors, SectorKeys{Sector: i, A: make([]byte, 6), B: bytes.Repeat([]byte{1}, 6)})
			}
			pa := append([]byte{0x60, 1}, make([]byte, 6)...)
			pb := append([]byte{0x61, 1}, bytes.Repeat([]byte{1}, 6)...)
			// A failed read-only authentication may select B. Once B's WRITE is sent,
			// even an authentication/status error must not select another candidate.
			c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}}, {command: 2007, request: pa, status: 6}, {command: 2007, request: pb}, {command: 2009, request: append(pb, data[16:32]...), status: status}})
			out, e := c.CloneClassic(context.Background(), dump, target, ClassicCloneOptions{})
			if !errors.Is(e, ErrWriteOutcomeUnknown) || !out.WriteAttempted || out.Outcome != "write_outcome_unknown" || out.WrittenBlocks != 0 {
				t.Fatalf("%+v %v", out, e)
			}
			if c.Stats().Requests != 5 {
				t.Fatal("additional command after uncertain write", c.Stats())
			}
		})
	}
}
