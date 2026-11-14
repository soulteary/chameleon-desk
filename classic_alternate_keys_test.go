package chameleon

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func alternateClassicKeys() (ClassicKeyMap, HexBytes, HexBytes) {
	a, b := HexBytes{1, 2, 3, 4, 5, 6}, HexBytes{6, 5, 4, 3, 2, 1}
	k := ClassicKeyMap{Schema: 1, UID: HexBytes{1, 2, 3, 4}, Complete: true, Sectors: make([]SectorKeys, 5)}
	for i := range k.Sectors {
		k.Sectors[i] = SectorKeys{Sector: i, A: a, B: b}
	}
	return k, a, b
}

func classicAlternatePrefix() []readerStep {
	return []readerStep{
		{command: 1002, status: 0x68, data: []byte{1}},
		{command: 2000, data: []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}},
	}
}

func TestClassicCloneUsesKeyBAfterAccessDenied(t *testing.T) {
	k, a, b := alternateClassicKeys()
	data := bytes.Repeat([]byte{42}, 320)
	dump := ClassicCardDump{Schema: 1, UID: HexBytes{9, 8, 7, 6}, TagType: 1000, Data: data, SHA256: imageDigest(data)}
	steps := classicAlternatePrefix()[:1]
	for sector := 0; sector < 5; sector++ {
		steps = append(steps, classicAlternatePrefix()[1])
		for block := sector * 4; block < sector*4+3; block++ {
			if block == 0 {
				continue
			}
			value := data[block*16 : (block+1)*16]
			pa, pb := append([]byte{byte(KeyA), byte(block)}, a...), append([]byte{byte(KeyB), byte(block)}, b...)
			steps = append(steps,
				readerStep{command: 2009, request: append(pa, value...), status: 2},
				readerStep{command: 2009, request: append(append([]byte(nil), pb...), value...)},
				readerStep{command: 2008, request: pb, data: value})
		}
	}
	c := readerSimulator(t, steps)
	out, e := c.CloneClassic(context.Background(), dump, k, ClassicCloneOptions{})
	if e != nil || out.WrittenBlocks != 14 || out.VerifiedBlocks != 14 || !out.ManufacturerSkipped || out.TrailersWritten {
		t.Fatalf("alternate-key clone failed: %+v %v", out, e)
	}
}

func TestClassicDumpUsesKeyBAfterAccessDenied(t *testing.T) {
	k, a, b := alternateClassicKeys()
	steps := classicAlternatePrefix()[:1]
	want := make([]byte, 0, 320)
	for sector := 0; sector < 5; sector++ {
		steps = append(steps, classicAlternatePrefix()[1])
		for block := sector * 4; block < sector*4+4; block++ {
			value := bytes.Repeat([]byte{byte(block)}, 16)
			if block%4 == 3 {
				clear(value[:6])
				copy(value[6:9], []byte{0x7f, 0x07, 0x88})
				clear(value[10:16])
			}
			pa, pb := append([]byte{byte(KeyA), byte(block)}, a...), append([]byte{byte(KeyB), byte(block)}, b...)
			steps = append(steps,
				readerStep{command: 2008, request: pa, status: 2},
				readerStep{command: 2008, request: pb, data: value})
			if block%4 == 3 {
				steps = append(steps, readerStep{command: 2007, request: pa})
				value = append([]byte(nil), value...)
				copy(value[:6], a)
				copy(value[10:16], b)
			}
			want = append(want, value...)
		}
	}
	c := readerSimulator(t, steps)
	out, e := c.DumpClassicWithKeys(context.Background(), k)
	if e != nil || out.Validate() != nil || len(out.UnknownKeys) != 0 || !bytes.Equal(out.Data, want) {
		t.Fatalf("alternate-key dump failed: %+v %v", out, e)
	}
}

func TestClassicAlternateKeysStopOnDenialAndUnexpectedErrors(t *testing.T) {
	for _, operation := range []string{"clone", "dump"} {
		for _, tc := range []struct {
			name      string
			first     uint16
			second    int
			transport bool
		}{
			{"both denied", 2, 2, false},
			{"authentication then denial", 6, 2, false},
			{"denial then authentication", 2, 6, false},
			{"no card", 1, -1, false},
			{"CRC error", 4, -1, false},
			{"unsupported", 103, -1, false},
			{"transport closed", 0, -1, true},
		} {
			t.Run(operation+"/"+tc.name, func(t *testing.T) {
				k, a, b := alternateClassicKeys()
				data := make([]byte, 320)
				dump := ClassicCardDump{Schema: 1, UID: HexBytes{9, 8, 7, 6}, TagType: 1000, Data: data, SHA256: imageDigest(data)}
				cmd, block := uint16(2008), byte(0)
				if operation == "clone" {
					cmd, block = 2009, 1
				}
				payload := func(kind KeyType, key HexBytes) []byte {
					p := append([]byte{byte(kind), block}, key...)
					if operation == "clone" {
						p = append(p, data[int(block)*16:(int(block)+1)*16]...)
					}
					return p
				}
				steps := append(classicAlternatePrefix(), readerStep{command: cmd, request: payload(KeyA, a), status: tc.first})
				if tc.second >= 0 {
					steps = append(steps, readerStep{command: cmd, request: payload(KeyB, b), status: uint16(tc.second)})
				}
				var c *Client
				if tc.transport {
					steps[len(steps)-1].before = func() { _ = c.port.Close() }
				}
				c = readerSimulator(t, steps)
				var e error
				if operation == "clone" {
					out, err := c.CloneClassic(context.Background(), dump, k, ClassicCloneOptions{})
					e = err
					if out.WrittenBlocks != 0 || out.VerifiedBlocks != 0 {
						t.Fatal("failed clone counted an unverified write", out)
					}
				} else {
					out, err := c.DumpClassicWithKeys(context.Background(), k)
					e = err
					if len(out.Data) != 0 {
						t.Fatal("failed read contributed dump bytes")
					}
				}
				if e == nil {
					t.Fatal("failed operation reported success")
				}
				var status *StatusError
				if tc.transport {
					if errors.As(e, &status) {
						t.Fatal("transport error was treated as a key rejection", e)
					}
				} else {
					want := tc.first
					if tc.second >= 0 {
						want = uint16(tc.second)
					}
					if !errors.As(e, &status) || status.Status != want {
						t.Fatalf("unexpected failure: %v", e)
					}
				}
			})
		}
	}
}
