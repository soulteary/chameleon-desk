package chameleon

import (
	"bytes"
	"context"
	"testing"
)

func TestULCReadAndPartialRejection(t *testing.T) {
	for _, n := range []int{4, 3} {
		c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2019, request: append(make([]byte, 16), 4, 1), data: make([]byte, n)}})
		v, e := c.ReadULC(context.Background(), make([]byte, 16), 4, 1)
		if n == 4 && (e != nil || len(v) != 4) {
			t.Fatal(v, e)
		}
		if n == 3 && e == nil {
			t.Fatal("short read accepted")
		}
	}
}
func TestType2PasswordFailureTurnsFieldOff(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2010, request: []byte{0xfc, 1, 244, 0, 40, 0x1b, 1, 2, 3, 4}, data: []byte{0}}, {command: 2101, status: 0x68}})
	if _, _, e := c.ReadType2Authenticated(context.Background(), []byte{1, 2, 3, 4}, 4, 1); e == nil {
		t.Fatal("invalid PACK accepted")
	}
}
func TestType2PasswordReadRetainsSelection(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2010, request: []byte{0xfc, 1, 244, 0, 40, 0x1b, 1, 2, 3, 4}, data: []byte{0xaa, 0xbb}}, {command: 2010, request: []byte{0x6c, 1, 244, 0, 16, 0x30, 4}, data: bytes.Repeat([]byte{42}, 16)}, {command: 2101, status: 0x68}})
	v, pack, e := c.ReadType2Authenticated(context.Background(), []byte{1, 2, 3, 4}, 5, 2)
	if e != nil || len(v) != 8 || pack.String() != "AABB" {
		t.Fatal(v, pack, e)
	}
}
func TestReaderConfigValidation(t *testing.T) {
	c := readerSimulator(t, nil)
	if e := c.SetReaderConfig(context.Background(), ReaderConfig{BCC: 3}); e == nil {
		t.Fatal("invalid config accepted")
	}
	if _, e := c.ReadULC(context.Background(), make([]byte, 16), int(^uint(0)>>1), 2); e == nil {
		t.Fatal("page overflow accepted")
	}
}
func TestSEOSStrictParsingAndBounds(t *testing.T) {
	v := SEOSData{Data: []byte{1, 2}, OID: []byte{3}, Tag: []byte{4}, Diversifier: []byte{5}, HashAlgorithm: 6, EncryptionAlgorithm: 9}
	p, e := v.payload()
	if e != nil {
		t.Fatal(e)
	}
	got, e := ParseSEOSData(p)
	if e != nil || !bytes.Equal(got.Data, v.Data) {
		t.Fatal(got, e)
	}
	for i := 0; i < len(p); i++ {
		if _, e = ParseSEOSData(p[:i]); e == nil {
			t.Fatal("truncated SEOS accepted")
		}
	}
	c := readerSimulator(t, nil)
	if e := c.SetSEOSKeys(context.Background(), make([]byte, 15), make([]byte, 16), make([]byte, 16)); e == nil {
		t.Fatal("short key accepted")
	}
}
