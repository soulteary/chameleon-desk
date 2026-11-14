package chameleon

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestClassicDumpKeyKnowledge(t *testing.T) {
	for _, name := range []string{"missing-A", "hidden-B", "readable-B", "complete"} {
		t.Run(name, func(t *testing.T) {
			km := ClassicKeyMap{Schema: 1, UID: HexBytes{1, 2, 3, 4}, Sectors: make([]SectorKeys, 5)}
			keyA := HexBytes{1, 2, 3, 4, 5, 6}
			keyB := HexBytes{6, 5, 4, 3, 2, 1}
			steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}}
			for sector := 0; sector < 5; sector++ {
				keys := SectorKeys{Sector: sector, A: keyA, B: keyB}
				if name == "missing-A" {
					keys.A = nil
				}
				if name == "hidden-B" || name == "readable-B" {
					keys.B = nil
				}
				km.Sectors[sector] = keys
				steps = append(steps, readerStep{command: 2000, data: []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}})
				for block := sector * 4; block < sector*4+4; block++ {
					data := make([]byte, 16)
					if block%4 == 3 {
						copy(data[6:9], []byte{0x7f, 0x07, 0x88})
						if name == "readable-B" {
							copy(data[6:9], []byte{0xff, 0x07, 0x80})
							copy(data[10:], keyB)
						}
					}
					kind, key := KeyA, keyA
					if name == "missing-A" {
						kind, key = KeyB, keyB
					}
					steps = append(steps, readerStep{command: 2008, request: append([]byte{byte(kind), byte(block)}, key...), data: data})
				}
				if name == "complete" {
					steps = append(steps, readerStep{command: 2007, request: append([]byte{0x61, byte(sector*4 + 3)}, keyB...)})
				}
			}
			km.updateComplete()
			c := readerSimulator(t, steps)
			d, e := c.DumpClassicWithKeys(context.Background(), km)
			if e != nil {
				t.Fatal(e)
			}
			raw, e := json.Marshal(d)
			if e != nil {
				t.Fatal(e)
			}
			var back ClassicCardDump
			if e = json.Unmarshal(raw, &back); e != nil {
				t.Fatal(e)
			}
			if e = back.Validate(); e != nil {
				t.Fatal(e)
			}
			if d.Schema != 2 {
				t.Fatal("no knowledge schema")
			}
			if name == "hidden-B" || name == "missing-A" {
				want := KeyB
				if name == "missing-A" {
					want = KeyA
				}
				if len(back.UnknownKeys) != 5 {
					t.Fatal(back.UnknownKeys)
				}
				for i, k := range back.UnknownKeys {
					if k.Sector != i || k.Type != want {
						t.Fatal(k)
					}
				}
			} else {
				if len(back.UnknownKeys) != 0 {
					t.Fatal(back.UnknownKeys)
				}
				if !bytes.Equal(back.Data[58:64], keyB) {
					t.Fatal("key B lost")
				}
			}
		})
	}
}
func TestCloneRejectsUnknownAndLegacyTrailersBeforeIO(t *testing.T) {
	for _, d := range []ClassicCardDump{
		{Schema: 1, UID: HexBytes{1, 2, 3, 4}, TagType: 1000},
		{Schema: 2, UID: HexBytes{1, 2, 3, 4}, TagType: 1000, UnknownKeys: []ClassicUnknownKey{{0, KeyB}}},
	} {
		d.Data = make([]byte, 320)
		d.SHA256 = imageDigest(d.Data)
		c := readerSimulator(t, nil)
		if _, e := c.CloneClassic(context.Background(), d, ClassicKeyMap{}, ClassicCloneOptions{WriteTrailers: true}); e == nil {
			t.Fatal("unsafe trailer accepted")
		}
	}
}
func TestClassicUnknownMetadataValidation(t *testing.T) {
	for _, keys := range [][]ClassicUnknownKey{{{5, KeyA}}, {{0, 0}}, {{0, KeyA}, {0, KeyA}}} {
		d := ClassicCardDump{Schema: 2, UID: HexBytes{1, 2, 3, 4}, TagType: 1000, Data: make([]byte, 320), UnknownKeys: keys}
		d.SHA256 = imageDigest(d.Data)
		if e := d.Validate(); e == nil {
			t.Fatal("invalid metadata accepted", keys)
		}
	}
}

func TestCloneKnownSchema2TrailersStillWorks(t *testing.T) {
	data := make([]byte, 320)
	newA := HexBytes{1, 2, 3, 4, 5, 6}
	newB := HexBytes{6, 5, 4, 3, 2, 1}
	oldA := HexBytes{9, 9, 9, 9, 9, 9}
	km := ClassicKeyMap{Schema: 1, UID: HexBytes{1, 2, 3, 4}, Sectors: make([]SectorKeys, 5)}
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}}
	for sector := 0; sector < 5; sector++ {
		trailer := data[(sector*4+3)*16 : (sector*4+4)*16]
		copy(trailer, newA)
		copy(trailer[6:10], []byte{0xff, 0x07, 0x80, 0x69})
		copy(trailer[10:], newB)
		km.Sectors[sector] = SectorKeys{Sector: sector, A: oldA}
		steps = append(steps, readerStep{command: 2000, data: []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}})
		for block := sector * 4; block < sector*4+4; block++ {
			if block == 0 {
				continue
			}
			p := append([]byte{0x60, byte(block)}, oldA...)
			value := data[block*16 : (block+1)*16]
			steps = append(steps, readerStep{command: 2009, request: append(append([]byte(nil), p...), value...)})
			read := append([]byte(nil), value...)
			if block%4 == 3 {
				p = append([]byte{0x60, byte(block)}, newA...)
				clear(read[:6])
			}
			steps = append(steps, readerStep{command: 2008, request: p, data: read})
			if block%4 == 3 {
				steps = append(steps, readerStep{command: 2007, request: append([]byte{0x61, byte(block)}, newB...), status: 6})
			}
		}
	}
	d := ClassicCardDump{Schema: 2, UID: HexBytes{9, 8, 7, 6}, TagType: 1000, Data: data, SHA256: imageDigest(data)}
	c := readerSimulator(t, steps)
	out, e := c.CloneClassic(context.Background(), d, km, ClassicCloneOptions{WriteTrailers: true, PreserveAccess: true})
	if e != nil || out.WrittenBlocks != 19 || out.VerifiedBlocks != 19 || !out.TrailersWritten {
		t.Fatal(out, e)
	}
}
