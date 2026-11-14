package chameleon

import (
	"bytes"
	"testing"
)

func TestNdefSuffixPreservesEmbeddedTerminator(t *testing.T) {
	for _, suffix := range [][]byte{
		{0xfd, 3, 0x11, 0xfe, 0x22, 0xfe},
		{0xfd, 0xff, 0, 3, 0x11, 0xfe, 0x22, 0, 0xfd, 1, 0xfe, 0xfe},
		{0xfd, 3, 0x11, 0xfe, 0x22},    // no terminator: preserve the entire region
		{0xfd, 0xff, 0xff, 0xff, 0xfe}, // malformed suffix must not be truncated
	} {
		t.Run(HexBytes(suffix).String(), func(t *testing.T) {
			region := make([]byte, 64)
			copy(region, []byte{3, 0})
			copy(region[2:], suffix)
			loc := make([]ndefLocation, len(region))
			for i := range loc {
				loc[i] = ndefLocation{0, i}
			}
			c, e := ndefFromRegion("test", loc, region)
			if e != nil {
				t.Fatal(e)
			}
			blocks := [][]byte{append([]byte(nil), region...)}
			original := append([]byte(nil), blocks[0]...)
			msg := []byte{0xd0, 0, 0}
			if !bytes.Contains(suffix, []byte{0xff, 0xff, 0xff}) && suffix[len(suffix)-1] == 0xfe {
				wantCapacity := 64 - 2 - len(suffix)
				if c.Capacity() != wantCapacity {
					t.Fatalf("capacity %d want %d", c.Capacity(), wantCapacity)
				}
				if e = c.WriteMessage(blocks, msg); e != nil {
					t.Fatal(e)
				}
				if !bytes.Equal(blocks[0][5:5+len(suffix)], suffix) {
					t.Fatalf("suffix lost: %X", blocks[0])
				}
				if e = c.WriteMessage(blocks, make([]byte, wantCapacity+1)); e == nil {
					t.Fatal("overflow accepted")
				}
			} else {
				if e = c.WriteMessage(blocks, msg); e == nil {
					t.Fatal("unterminated/malformed suffix allowed growth")
				}
				if !bytes.Equal(blocks[0], original) {
					t.Fatal("failed write changed dump")
				}
			}
		})
	}
}
