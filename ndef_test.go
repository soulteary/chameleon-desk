package chameleon

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestNdefRecordRoundTrip(t *testing.T) {
	text, e := NewNdefText("héllo", "en")
	if e != nil || text.Kind != "text" || text.Value != "héllo" || text.Lang != "en" {
		t.Fatalf("%+v %v", text, e)
	}
	uri, e := NewNdefURI("https://www.example.com/a")
	if e != nil || uri.Payload[0] != 2 || uri.Value != "https://www.example.com/a" {
		t.Fatalf("%+v %v", uri, e) // prefix index 2 = https://www.
	}
	mime, _ := NewNdefMime("text/plain", "hi")
	ext, _ := NewNdefExternal("example.com:Type", "x")
	if ext.Type != "example.com:type" {
		t.Fatal("external type not lowercased")
	}
	records := []NdefRecord{text, uri, mime, ext}
	message, e := EncodeNdefMessage(records)
	if e != nil {
		t.Fatal(e)
	}
	back, e := DecodeNdefMessage(message)
	if e != nil || len(back) != 4 {
		t.Fatalf("%v %d", e, len(back))
	}
	for i := range records {
		if back[i].Value != records[i].Value || back[i].TNF != records[i].TNF || back[i].Type != records[i].Type {
			t.Fatalf("record %d round trip: %+v vs %+v", i, back[i], records[i])
		}
	}
	// A long payload forces the 4-byte length form.
	long, _ := NewNdefMime("application/octet-stream", strings.Repeat("x", 300))
	msg, _ := EncodeNdefMessage([]NdefRecord{long})
	if msg[0]&0x10 != 0 {
		t.Fatal("long record used the short-record flag")
	}
	if back, e := DecodeNdefMessage(msg); e != nil || len(back[0].Payload) != 300 {
		t.Fatalf("%v", e)
	}
}

func TestNdefDecodeRejections(t *testing.T) {
	good, _ := EncodeNdefMessage([]NdefRecord{mustText(t, "a")})
	cases := map[string][]byte{
		"empty":       nil,
		"no begin":    {0x40, 0x01, 0x01, 'T', 0x00},
		"chunked":     {0xA0, 0x01, 0x01, 'T', 0x00},
		"truncated":   good[:len(good)-1],
		"trailing":    append(append([]byte(nil), good...), 0x00),
		"no end flag": {0x81, 0x01, 0x01, 'T', 0x00},
	}
	for name, msg := range cases {
		if name == "empty" {
			if r, e := DecodeNdefMessage(msg); e != nil || r != nil {
				t.Fatalf("empty message should decode to no records: %v", e)
			}
			continue
		}
		if _, e := DecodeNdefMessage(msg); e == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func mustText(t *testing.T, s string) NdefRecord {
	t.Helper()
	r, e := NewNdefText(s, "en")
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestNdefType2Container(t *testing.T) {
	blocks := make([][]byte, 45)
	for i := range blocks {
		blocks[i] = make([]byte, 4)
	}
	blocks[3] = []byte{0xE1, 0x10, 0x12, 0x00} // CC: 0x12*8 = 144 bytes
	blocks[4] = []byte{0x03, 0x00, 0xFE, 0x00} // empty NDEF TLV then terminator
	container, e := DetectNdef(1100, blocks)
	if e != nil || container.Mapping != "NFC Forum Type 2" {
		t.Fatalf("%v", e)
	}
	message, _ := EncodeNdefMessage([]NdefRecord{mustText(t, "hello world")})
	if e = container.WriteMessage(blocks, message); e != nil {
		t.Fatal(e)
	}
	again, e := DetectNdef(1100, blocks)
	if e != nil {
		t.Fatal(e)
	}
	records, e := DecodeNdefMessage(again.Message())
	if e != nil || records[0].Value != "hello world" {
		t.Fatalf("%+v %v", records, e)
	}
	// Overflow is rejected and leaves the blocks usable.
	huge, _ := NewNdefMime("application/octet-stream", strings.Repeat("x", 400))
	hugeMsg, _ := EncodeNdefMessage([]NdefRecord{huge})
	if e = again.WriteMessage(blocks, hugeMsg); e == nil {
		t.Fatal("oversized NDEF message accepted")
	}
}

func TestNdefType2RejectsIncompletePhysicalPages(t *testing.T) {
	for _, size := range []int{0, 1, 3, 5} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			blocks := make([][]byte, 8)
			for i := range blocks {
				blocks[i] = make([]byte, 4)
			}
			blocks[3] = []byte{0xE1, 0x10, 2, 0}
			blocks[4] = make([]byte, size)
			// This TLV is physically on page 5. Missing bytes on page 4 must
			// never be compacted out to turn it into the start of user memory.
			blocks[5] = []byte{0x03, 0x00, 0xFE, 0}
			before, _ := json.Marshal(blocks)
			if _, e := DetectNdef(1100, blocks); e == nil {
				t.Fatal("incomplete page accepted as a writable NDEF container")
			}
			after, _ := json.Marshal(blocks)
			if !bytes.Equal(before, after) {
				t.Fatal("rejected dump was modified")
			}
		})
	}
	blocks := make([][]byte, 7)
	for i := range blocks {
		blocks[i] = make([]byte, 4)
	}
	blocks[3] = []byte{0xE1, 0x10, 2, 0}
	blocks[4] = []byte{0x03, 0, 0xFE, 0}
	if _, e := DetectNdef(1100, blocks); e == nil {
		t.Fatal("dump shorter than the capability-container region accepted")
	}
}

func TestNdefClassicContainer(t *testing.T) {
	blocks := make([][]byte, 64)
	for i := range blocks {
		blocks[i] = make([]byte, 16)
	}
	// MAD in sector 0 (blocks 1,2): mark sector 1 as NDEF (0x03,0xE1).
	blocks[1][2], blocks[1][3] = 0x03, 0xE1
	// Sector 1 data blocks 4,5,6 hold the NDEF TLV.
	blocks[4][0], blocks[4][1], blocks[4][2] = 0x03, 0x00, 0xFE
	container, e := DetectNdef(1001, blocks)
	if e != nil || container.Mapping != "MIFARE Classic" {
		t.Fatalf("%v", e)
	}
	message, _ := EncodeNdefMessage([]NdefRecord{mustText(t, "classic ndef")})
	if e = container.WriteMessage(blocks, message); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(blocks[4][:1], []byte{0x03}) {
		t.Fatal("TLV type overwritten")
	}
	again, _ := DetectNdef(1001, blocks)
	records, e := DecodeNdefMessage(again.Message())
	if e != nil || records[0].Value != "classic ndef" {
		t.Fatalf("%+v %v", records, e)
	}
	if _, e := DetectNdef(100, blocks); e == nil {
		t.Fatal("LF tag accepted for NDEF")
	}
}

func TestNdefText16Bit(t *testing.T) {
	// A UTF-16 BE text payload (status bit 0x80 set).
	payload := append([]byte{0x82, 'e', 'n'}, 0x00, 'A', 0x00, 'B')
	r, _ := ndefRecord(1, "T", nil, payload)
	if r.Value != "AB" {
		t.Fatalf("utf16 decode: %q", r.Value)
	}
	if hex.EncodeToString(r.Payload) == "" {
		t.Fatal("payload lost")
	}
}
