package chameleon

import (
	"bytes"
	"context"
	"math"
	"testing"
	"time"
)

func TestClassicValueRedundancy(t *testing.T) {
	for _, v := range []int32{0, 1, -1, math.MinInt32, math.MaxInt32} {
		b := EncodeClassicValue(v, 253)
		got, address, e := DecodeClassicValue(b)
		if e != nil || got != v || address != 253 {
			t.Fatal(got, address, e)
		}
		for i := range b {
			bad := append([]byte(nil), b...)
			bad[i] ^= 1
			if _, _, e := DecodeClassicValue(bad); e == nil {
				t.Fatalf("corruption at %d accepted", i)
			}
		}
	}
	if _, _, e := DecodeClassicValue(make([]byte, 15)); e == nil {
		t.Fatal("short block accepted")
	}
}
func TestClassicSectorGeometry4K(t *testing.T) {
	for _, v := range [][3]int{{0, 0, 4}, {31, 124, 4}, {32, 128, 16}, {39, 240, 16}} {
		first, count, e := ClassicSectorGeometry(v[0])
		if e != nil || first != v[1] || count != v[2] {
			t.Fatal(first, count, e)
		}
	}
	if _, _, e := ClassicSectorGeometry(40); e == nil {
		t.Fatal("sector 40 accepted")
	}
}
func TestClassicValueWire(t *testing.T) {
	p := []byte{0x60, 4, 1, 2, 3, 4, 5, 6, 0xc1, 0, 0, 1, 0, 0x61, 8, 6, 5, 4, 3, 2, 1}
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2011, request: p}})
	if e := c.ManipulateClassicValue(context.Background(), 4, KeyA, []byte{1, 2, 3, 4, 5, 6}, 0xc1, 256, 8, KeyB, []byte{6, 5, 4, 3, 2, 1}); e != nil {
		t.Fatal(e)
	}
}
func dictionarySteps() []readerStep {
	scan := []byte{4, 1, 2, 3, 4, 4, 0, 8, 0}
	reply := make([]byte, 490)
	reply[0] = 0xc0
	copy(reply[10:], bytes.Repeat([]byte{255}, 12))
	mask := bytes.Repeat([]byte{255}, 10)
	mask[0] = 0x3f
	request := append(mask, bytes.Repeat([]byte{255}, 6)...)
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: scan}, {command: 2000, data: scan}, {command: 2012, request: request, data: reply}}
	for _, kind := range []byte{0x60, 0x61} {
		steps = append(steps, readerStep{command: 2000, data: scan}, readerStep{command: 2007, request: append([]byte{kind, 0}, bytes.Repeat([]byte{255}, 6)...)})
	}
	return steps
}
func TestClassicDictionaryAndAutopwnComplete(t *testing.T) {
	c := readerSimulator(t, dictionarySteps())
	c.timeout = 5 * time.Second
	v, e := c.Autopwn(context.Background(), AutopwnOptions{Sectors: 1, Dictionary: []HexBytes{bytes.Repeat([]byte{255}, 6)}, Recovery: AdvancedRecoveryOptions{Limit: time.Minute, MaxRuns: 1, MaxAttempts: 1}})
	if e != nil || !v.Complete || v.Sectors[0].A.String() != "FFFFFFFFFFFF" {
		t.Fatal(v, e)
	}
	if e = v.Validate(); e != nil {
		t.Fatal(e)
	}
}
func TestDictionaryAuthMismatchIsExpected(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: []byte{4, 1, 2, 3, 4, 4, 0, 8, 0}}, {command: 2015, request: []byte{4, 0x60, 1, 0, 0, 0, 0, 0, 0}, status: 6}})
	key, e := c.checkCandidateBatches(context.Background(), []byte{1, 2, 3, 4}, 4, KeyA, []HexBytes{make([]byte, 6)})
	if e != nil || len(key) != 0 {
		t.Fatal(key, e)
	}
}
func TestDumpClassicWithPerSectorKeys(t *testing.T) {
	km := ClassicKeyMap{Schema: 1, UID: HexBytes{1, 2, 3, 4}, Sectors: make([]SectorKeys, 5)}
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}}
	scan := []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}
	for sector := 0; sector < 5; sector++ {
		key := bytes.Repeat([]byte{byte(sector)}, 6)
		km.Sectors[sector] = SectorKeys{Sector: sector, A: key}
		steps = append(steps, readerStep{command: 2000, data: scan})
		for block := sector * 4; block < sector*4+4; block++ {
			steps = append(steps, readerStep{command: 2008, request: append([]byte{0x60, byte(block)}, key...), data: func() []byte {
				b := bytes.Repeat([]byte{byte(block)}, 16)
				if block%4 == 3 {
					copy(b[6:9], []byte{0x7f, 7, 0x88})
				}
				return b
			}()})
		}
	}
	c := readerSimulator(t, steps)
	v, e := c.DumpClassicWithKeys(context.Background(), km)
	if e != nil || len(v.Data) != 320 || v.Data[64] != 4 || v.Data[7*16] != 1 {
		t.Fatal(v, e)
	}
	if e = v.Validate(); e != nil {
		t.Fatal(e)
	}
}
func TestReadableKeyBIsNotMisreportedAsFailedRecovery(t *testing.T) {
	scan := []byte{4, 1, 2, 3, 4, 4, 0, 8, 0}
	reply := make([]byte, 490)
	reply[0] = 0x80
	copy(reply[10:], bytes.Repeat([]byte{255}, 6))
	mask := bytes.Repeat([]byte{255}, 10)
	mask[0] = 0x3f
	trailer := make([]byte, 16)
	copy(trailer[6:9], []byte{255, 7, 128})
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: scan}, {command: 2000, data: scan}, {command: 2012, request: append(mask, bytes.Repeat([]byte{255}, 6)...), data: reply}, {command: 2000, data: scan}, {command: 2007, request: append([]byte{0x60, 0}, bytes.Repeat([]byte{255}, 6)...)}, {command: 2000, data: scan}, {command: 2008, request: append([]byte{0x60, 3}, bytes.Repeat([]byte{255}, 6)...), data: trailer}})
	c.timeout = 5 * time.Second
	v, e := c.CheckClassicDictionary(context.Background(), 1, []HexBytes{bytes.Repeat([]byte{255}, 6)})
	if e != nil || !v.Complete || !v.Sectors[0].BReadable || v.Sectors[0].B.String() != "000000000000" {
		t.Fatal(v, e)
	}
}

func TestAutopwnReadsKeyBAfterRecoveringKeyA(t *testing.T) {
	scan := []byte{4, 1, 2, 3, 4, 4, 0, 8, 0}
	key := bytes.Repeat([]byte{255}, 6)
	reply := make([]byte, 490)
	reply[0] = 0x30 // Only sector 1 keys are found in the initial dictionary.
	copy(reply[22:34], bytes.Repeat([]byte{255}, 12))
	mask := bytes.Repeat([]byte{255}, 10)
	mask[0] = 0x0f
	steps := []readerStep{
		{command: 1002, status: 0x68, data: []byte{1}},
		{command: 2000, data: scan},
		{command: 2000, data: scan},
		{command: 2012, request: append(mask, key...), data: reply},
	}
	for _, kind := range []byte{0x60, 0x61} {
		steps = append(steps,
			readerStep{command: 2000, data: scan},
			readerStep{command: 2007, request: append([]byte{kind, 4}, key...)})
	}
	// Reusing sector 1's A recovers sector 0's A. B is readable under A,
	// so its bytes must be read rather than passed through authentication.
	steps = append(steps,
		readerStep{command: 1002, status: 0x68, data: []byte{1}},
		readerStep{command: 2000, data: scan},
		readerStep{command: 2007, request: append([]byte{0x60, 0}, key...)},
		readerStep{command: 1002, status: 0x68, data: []byte{1}},
		readerStep{command: 2000, data: scan})
	trailer := make([]byte, 16)
	copy(trailer[6:9], []byte{255, 7, 128})
	copy(trailer[10:16], []byte{1, 2, 3, 4, 5, 6})
	steps = append(steps, readerStep{command: 2008, request: append([]byte{0x60, 3}, key...), data: trailer})
	c := readerSimulator(t, steps)
	c.timeout = 5 * time.Second
	v, e := c.Autopwn(context.Background(), AutopwnOptions{Sectors: 2, Dictionary: []HexBytes{key}, Recovery: AdvancedRecoveryOptions{Limit: time.Minute, MaxRuns: 1, MaxAttempts: 1}})
	if e != nil || !v.Complete || !v.Sectors[0].BReadable || v.Sectors[0].B.String() != "010203040506" {
		t.Fatalf("readable B was not recovered from its trailer: %+v %v", v, e)
	}
	if e := v.Validate(); e != nil {
		t.Fatal(e)
	}
}
