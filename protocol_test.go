package chameleon

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func response(cmd, status uint16, payload []byte) []byte {
	b := make([]byte, len(payload)+10)
	b[0] = 0x11
	b[1] = 0xef
	binary.BigEndian.PutUint16(b[2:4], cmd)
	binary.BigEndian.PutUint16(b[4:6], status)
	binary.BigEndian.PutUint16(b[6:8], uint16(len(payload)))
	b[8] = lrc(b[:8])
	copy(b[9:], payload)
	b[len(b)-1] = lrc(payload)
	return b
}
func TestVersionRequestGolden(t *testing.T) {
	want := []byte{0x11, 0xef, 0x03, 0xe8, 0, 0, 0, 0, 0x15, 0}
	if !bytes.Equal(encode(1000), want) {
		t.Fatalf("got %x", encode(1000))
	}
}
func TestDecoderEverySplit(t *testing.T) {
	b := response(1000, 0x68, []byte{2, 1})
	for split := 0; split <= len(b); split++ {
		var d decoder
		d.buf = append(d.buf, b[:split]...)
		f, ok := d.next()
		if split < len(b) && ok {
			t.Fatal("premature frame")
		}
		if !ok {
			d.buf = append(d.buf, b[split:]...)
			f, ok = d.next()
		}
		if !ok || f.command != 1000 || !bytes.Equal(f.data, []byte{2, 1}) {
			t.Fatalf("split %d: %+v", split, f)
		}
	}
}
func TestDecoderCorruptionAndCoalescing(t *testing.T) {
	good := response(1000, 0x68, []byte{2, 1})
	bad := append([]byte(nil), good...)
	bad[len(bad)-1] ^= 1
	oversized := response(1000, 0x68, nil)
	binary.BigEndian.PutUint16(oversized[6:8], MaxPayload+1)
	oversized[8] = lrc(oversized[:8])
	var d decoder
	d.buf = append([]byte{0, 0x11, 1}, bad...)
	d.buf = append(d.buf, oversized...)
	d.buf = append(d.buf, good...)
	d.buf = append(d.buf, good...)
	for i := 0; i < 2; i++ {
		f, ok := d.next()
		if !ok || f.command != 1000 {
			t.Fatalf("frame %d missing", i)
		}
	}
	if _, ok := d.next(); ok || d.rejected != 2 {
		t.Fatalf("rejections %d", d.rejected)
	}
}
func TestDecoderPayloadContainingSOF(t *testing.T) {
	var d decoder
	payload := []byte{0x11, 0xef, 1, 2}
	d.buf = response(1017, 0x68, payload)
	f, ok := d.next()
	if !ok || !bytes.Equal(f.data, payload) {
		t.Fatal("payload damaged")
	}
}
func FuzzDecoder(f *testing.F) {
	f.Add(response(1000, 0x68, []byte{2, 1}))
	f.Add([]byte{0x11, 0xef})
	f.Fuzz(func(t *testing.T, b []byte) {
		var d decoder
		for len(b) > 0 {
			n := min(31, len(b))
			d.buf = append(d.buf, b[:n]...)
			b = b[n:]
			for {
				v, ok := d.next()
				if !ok {
					break
				}
				if len(v.data) > MaxPayload {
					t.Fatal("oversized payload")
				}
			}
			if len(d.buf) > MaxPayload+10 {
				t.Fatal("unbounded buffer")
			}
		}
	})
}
