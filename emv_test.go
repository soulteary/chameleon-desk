package chameleon

import (
	"context"
	"testing"
	"time"
)

func TestEMVScanStrictParser(t *testing.T) {
	data := []byte{4, 1, 2, 3, 4, 4, 0, 0x20, 0, 1, 4, 0, 0xa4, 4, 0, 2, 0, 0x90, 0}
	v, e := ParseEMVScan(data)
	if e != nil || len(v.Pairs) != 1 || v.Pairs[0].StatusWord != 0x9000 {
		t.Fatal(v, e)
	}
	for i := 0; i < len(data); i++ {
		if _, e = ParseEMVScan(data[:i]); e == nil {
			t.Fatalf("truncated response length %d accepted", i)
		}
	}
	if _, e = ParseEMVScan(append(data, 0)); e == nil {
		t.Fatal("trailing bytes accepted")
	}
}
func TestBERTLVBoundsAndNesting(t *testing.T) {
	v, e := ParseBERTLV([]byte{0x6f, 5, 0x9f, 0x38, 2, 1, 2})
	if e != nil || len(v) != 1 || len(v[0].Children) != 1 {
		t.Fatal(v, e)
	}
	for _, b := range [][]byte{{0x6f, 0x80}, {0x9f}, {0x5a, 4, 1}, {0x6f, 2, 0x5a, 3}} {
		if _, e = ParseBERTLV(b); e == nil {
			t.Fatal(b)
		}
	}
}
func TestISO4InvalidProfileBeforeIO(t *testing.T) {
	c := readerSimulator(t, nil)
	if e := c.LoadISO4Profile(context.Background(), ISO4Profile{}); e == nil {
		t.Fatal("invalid identity accepted")
	}
}
func TestEMVScanSDK(t *testing.T) {
	raw := []byte{4, 1, 2, 3, 4, 4, 0, 0x20, 0, 1, 4, 0, 0xa4, 4, 0, 2, 0, 0x90, 0}
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: uint16(CmdHf14a4EmvScan), data: raw}})
	c.timeout = 10 * time.Second
	if _, e := c.ScanEMV(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func FuzzEMVResponse(f *testing.F) {
	f.Add([]byte{4, 1, 2, 3, 4, 4, 0, 0x20, 0, 0})
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = ParseEMVScan(b) })
}
func FuzzBERTLV(f *testing.F) {
	f.Add([]byte{0x5a, 2, 1, 2})
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = ParseBERTLV(b) })
}
