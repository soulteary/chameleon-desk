package chameleon

import (
	"bytes"
	"context"
	"testing"
)

const gen3Flags = 0xF8 // ActivateField | WaitResponse | AppendCRC | AutoSelect | KeepField

func TestGen3WriteBlock0(t *testing.T) {
	data := bytes.Repeat([]byte{0x22}, 16)
	frame := append([]byte{0x90, 0xFB, 0xCC, 0xCC, 0x10}, data...)
	c := readerSimulator(t, []readerStep{
		{command: 1002, status: 0x68, data: []byte{1}},
		{command: 2010, request: rawReq(gen3Flags, len(frame)*8, frame), data: []byte{0x90, 0x00}},
		{command: 2101, status: 0x68},
	})
	if e := c.Gen3WriteBlock0(context.Background(), data); e != nil {
		t.Fatal(e)
	}
	if e := c.Gen3WriteBlock0(context.Background(), data[:15]); e == nil {
		t.Fatal("short block accepted")
	}
}

func TestGen3SetUID(t *testing.T) {
	uid := []byte{0x01, 0x02, 0x03, 0x04}
	frame := append([]byte{0x90, 0xFB, 0xCC, 0xCC, 0x04}, uid...)
	c := readerSimulator(t, []readerStep{
		{command: 1002, status: 0x68, data: []byte{1}},
		{command: 2010, request: rawReq(gen3Flags, len(frame)*8, frame), data: []byte{}},
		{command: 2000, data: []byte{4, 1, 2, 3, 4, 4, 0, 8, 0}}, // re-scan returns the new UID
		{command: 2101, status: 0x68},
	})
	back, e := c.Gen3SetUID(context.Background(), uid)
	if e != nil || back.String() != "01020304" {
		t.Fatalf("%v %v", back, e)
	}
	// A mismatching read-back is reported.
	c = readerSimulator(t, []readerStep{
		{command: 1002, status: 0x68, data: []byte{1}},
		{command: 2010, request: rawReq(gen3Flags, len(frame)*8, frame), data: []byte{}},
		{command: 2000, data: []byte{4, 9, 9, 9, 9, 4, 0, 8, 0}},
		{command: 2101, status: 0x68},
	})
	if _, e := c.Gen3SetUID(context.Background(), uid); e == nil {
		t.Fatal("mismatching UID read-back accepted")
	}
}

func TestGen3Detect(t *testing.T) {
	c := readerSimulator(t, []readerStep{
		{command: 1002, status: 0x68, data: []byte{1}},
		{command: 2010, request: rawReq(gen3Flags, 16, []byte{0x30, 0x00}), data: bytes.Repeat([]byte{0}, 18)},
		{command: 2101, status: 0x68},
	})
	classic, e := c.DetectGen3(context.Background())
	if e != nil || !classic {
		t.Fatalf("classic=%v err=%v", classic, e)
	}
}

func TestGen3Lock(t *testing.T) {
	c := readerSimulator(t, []readerStep{
		{command: 1002, status: 0x68, data: []byte{1}},
		{command: 2010, request: rawReq(gen3Flags, 40, []byte{0x90, 0xFD, 0x11, 0x11, 0x00}), data: []byte{0x90, 0x00}},
		{command: 2101, status: 0x68},
	})
	if e := c.Gen3Lock(context.Background()); e != nil {
		t.Fatal(e)
	}
}
