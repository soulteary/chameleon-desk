package chameleon

import (
	"bytes"
	"context"
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
			steps = append(steps, readerStep{command: 2009, request: append(append([]byte(nil), p...), data[block*16:(block+1)*16]...)}, readerStep{command: 2008, request: p, data: data[block*16 : (block+1)*16]})
		}
	}
	c := readerSimulator(t, steps)
	v, e := c.CloneClassic(context.Background(), dump, km, ClassicCloneOptions{})
	if e != nil || v.WrittenBlocks != 14 || v.VerifiedBlocks != 14 || !v.ManufacturerSkipped || v.TrailersWritten {
		t.Fatal(v, e)
	}
}
