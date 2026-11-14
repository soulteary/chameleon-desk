package chameleon

import (
	"bytes"
	"context"
	"testing"
)

func TestHIDFormatsRoundTripAndBounds(t *testing.T) {
	formats := HIDFormats()
	if len(formats) != 31 {
		t.Fatal(len(formats))
	}
	for _, f := range formats {
		v := HIDProxID{f.ID, f.MaxFacility, f.MaxNumber, f.MaxIssue, f.MaxOEM}
		data, e := v.Encode()
		if e != nil {
			t.Fatal(f.Name, e)
		}
		got, e := DecodeHIDProx(data)
		if e != nil || got != v {
			t.Fatal(f.Name, got, e)
		}
		v.Number++
		if _, e = v.Encode(); e == nil {
			t.Fatal("overflow accepted", f.Name)
		}
	}
	format, e := ParseHIDFormat("h10301")
	if e != nil || format != 1 {
		t.Fatal(format, e)
	}
	if _, e = (HIDProxID{Format: 1, Facility: 256}).Encode(); e == nil {
		t.Fatal("facility truncated")
	}
}
func TestIDTECKChecksumAndEndian(t *testing.T) {
	data, e := ComposeIDTECK(0x123456)
	if e != nil || !bytes.Equal(data, []byte{0x49, 0x44, 0x54, 0x4b, 0x9c, 0x56, 0x34, 0x12}) {
		t.Fatal(data, e)
	}
	info, e := DecodeIDTECK(data)
	if e != nil || info.Number != 0x123456 || !info.ChecksumValid || !info.PreambleValid {
		t.Fatal(info, e)
	}
	data[4] ^= 1
	c := readerSimulator(t, nil)
	if _, e = c.EncodeLFIdentity(context.Background(), LFIdentityInput{Protocol: "idteck", ID: data}); e == nil {
		t.Fatal("invalid IDTECK checksum allowed for writing")
	}
}
func TestElectraCaptureCanBeClonedWithoutPrefix(t *testing.T) {
	id := bytes.Repeat([]byte{0xa5}, 13)
	input := LFIdentityInput{Protocol: "em410x", TagType: 104, ID: id}
	payload := append(append(append([]byte(nil), id...), []byte{1, 2, 3, 4}...), []byte{0, 0, 0, 0}...)
	c := readerSimulator(t, []readerStep{{command: 1033, status: 0x68, data: []byte{0}}, {command: 1002, status: 0x68, data: []byte{0}}, {command: 1001, request: []byte{1}, status: 0x68}, {command: 3006, request: payload, status: 0x40}, {command: 1001, request: []byte{0}, status: 0x68}})
	v, e := c.CloneLF(context.Background(), input, []byte{1, 2, 3, 4}, [][]byte{{0, 0, 0, 0}})
	if e != nil || v.Protocol != "electra" || !bytes.Equal(v.ID, id) {
		t.Fatal(v, e)
	}
}
func TestLFCloneRejectsLiteBeforeRF(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1033, status: 0x68, data: []byte{1}}})
	if _, e := c.CloneLF(context.Background(), LFIdentityInput{Protocol: "em410x", ID: []byte{1, 2, 3, 4, 5}}, make([]byte, 4), [][]byte{make([]byte, 4)}); e == nil {
		t.Fatal("Lite accepted LF write")
	}
}
func TestLFSlotSetReadbackNormalizesTypePrefix(t *testing.T) {
	id := []byte{1, 2, 3, 4, 5}
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1018, status: 0x68, data: []byte{0}}, {command: 1018, status: 0x68, data: []byte{0}}, {command: 1019, status: 0x68, data: slotTypes(1001, 100, 1)}, {command: 5000, request: id, status: 0x68}, {command: 5001, status: 0x68, data: append([]byte{0, 100}, id...)}})
	v, e := c.SlotLFIdentity(context.Background(), 0, "em410x", &LFIdentityInput{Protocol: "em410x", ID: id}, false)
	if e != nil || !bytes.Equal(v.ID, id) {
		t.Fatal(v, e)
	}
}
func TestPACEncodingIsASCII(t *testing.T) {
	c := readerSimulator(t, nil)
	if _, e := c.EncodeLFIdentity(context.Background(), LFIdentityInput{Protocol: "pac", PAC: "12345678"}); e != nil {
		t.Fatal(e)
	}
	if _, e := c.EncodeLFIdentity(context.Background(), LFIdentityInput{Protocol: "pac", PAC: "测试"}); e == nil {
		t.Fatal("non-ASCII PAC accepted")
	}
}
func TestIOProxRawInputUsesFirmwareDecoder(t *testing.T) {
	raw := []byte{0, 0x78, 0x54, 0xe0, 0x3a, 0x5d, 0x65, 0xab}
	encoded := []byte{1, 83, 0x12, 0x34}
	encoded = append(encoded, raw...)
	encoded = append(encoded, 0, 0, 0, 0)
	c := readerSimulator(t, []readerStep{{command: 3012, request: raw, status: 0x68, data: encoded}})
	v, e := c.EncodeLFIdentity(context.Background(), LFIdentityInput{Protocol: "ioprox", IOProx: &IOProxID{Raw: raw}})
	if e != nil || v.IOProx.Number != 0x1234 || !bytes.Equal(v.ID, encoded) {
		t.Fatal(v, e)
	}
}
func TestPACUARTChecksumAndParity(t *testing.T) {
	id := []byte("12345678")
	raw, e := EncodePACRaw(id)
	if e != nil {
		t.Fatal(e)
	}
	got, e := DecodePACRaw(raw)
	if e != nil || !bytes.Equal(got, id) {
		t.Fatal(got, e)
	}
	for _, offset := range []int{0, 1, 2, 15} {
		bad := append([]byte(nil), raw...)
		bad[offset] ^= 1
		if _, e := DecodePACRaw(bad); e == nil {
			t.Fatal("PAC corruption accepted", offset)
		}
	}
}
func TestPyramid26HostDecode(t *testing.T) {
	data := make([]byte, 105)
	data[71] = 1
	put := func(start, n int, value uint32) {
		for i := 0; i < n; i++ {
			data[start+i] = byte(value>>uint(n-i-1)) & 1
		}
	}
	put(73, 8, 12)
	put(81, 16, 3456)
	raw := make([]byte, 16)
	for i := 0; i < 15; i++ {
		ones := 0
		for j := 0; j < 7; j++ {
			bit := data[i*7+j]
			raw[i+1] |= bit << uint(7-j)
			ones += int(bit)
		}
		if ones%2 == 0 {
			raw[i+1] |= 1
		}
	}
	v, e := DecodePyramid(raw)
	if e != nil || v.FormatLength != 26 || v.Facility == nil || *v.Facility != 12 || v.Number != 3456 || v.ParityErrors != 0 {
		t.Fatal(v, e)
	}
	if _, e := DecodePyramid(make([]byte, 16)); e == nil {
		t.Fatal("missing sentinel accepted")
	}
}
func TestJablotronBCDNumber(t *testing.T) {
	v, e := DecodeJablotronNumber([]byte{0x12, 0x34, 0x56, 0x78, 0x90})
	if e != nil || v != 1234567890 {
		t.Fatal(v, e)
	}
	if _, e := DecodeJablotronNumber([]byte{0xab, 0, 0, 0, 0}); e == nil {
		t.Fatal("invalid BCD accepted")
	}
}

func TestOfflineIDTECKShortPayload(t *testing.T) {
	full, e := ComposeIDTECK(12345)
	if e != nil {
		t.Fatal(e)
	}
	v, e := EncodeLFIdentityOffline(LFIdentityInput{Protocol: "idteck", ID: full[4:]})
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(v.ID, full) {
		t.Fatal(v.ID)
	}
	if _, e = EncodeLFIdentityOffline(LFIdentityInput{Protocol: "ioprox"}); e == nil {
		t.Fatal("requires firmware")
	}
}
