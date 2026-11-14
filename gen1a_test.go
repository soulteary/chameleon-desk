package chameleon

import (
	"bytes"
	"context"
	"testing"
)

// rawReq builds the HF14A_RAW request payload: flags, 1000ms timeout, bit
// length, then the frame.
func rawReq(flags byte, bits int, data []byte) []byte {
	return append([]byte{flags, 0x03, 0xE8, byte(bits >> 8), byte(bits)}, data...)
}

const (
	gen1aUnlock1Flags = 0xC8 // ActivateField | WaitResponse | KeepField
	gen1aStepFlags    = 0x48 // WaitResponse | KeepField
	gen1aRWFlags      = 0x68 // WaitResponse | AppendCRC | KeepField
)

func gen1aUnlockSteps() []readerStep {
	return []readerStep{
		{command: 2010, request: rawReq(gen1aUnlock1Flags, 7, []byte{0x40}), data: []byte{0x0a}},
		{command: 2010, request: rawReq(gen1aStepFlags, 8, []byte{0x43}), data: []byte{0x0a}},
	}
}

func TestGen1AWriteBlock(t *testing.T) {
	block := bytes.Repeat([]byte{0x11}, 16)
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}}
	steps = append(steps, gen1aUnlockSteps()...)
	steps = append(steps,
		readerStep{command: 2010, request: rawReq(gen1aRWFlags, 16, []byte{0xA0, 4}), data: []byte{0x0a}},
		readerStep{command: 2010, request: rawReq(gen1aRWFlags, 128, block), data: []byte{0x0a}},
		readerStep{command: 2101, status: 0x68},
	)
	c := readerSimulator(t, steps)
	if e := c.Gen1AWriteBlock(context.Background(), 4, block); e != nil {
		t.Fatal(e)
	}
}

func TestGen1ADetect(t *testing.T) {
	// Magic card: unlock acks.
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}}
	steps = append(steps, gen1aUnlockSteps()...)
	steps = append(steps, readerStep{command: 2101, status: 0x68})
	c := readerSimulator(t, steps)
	magic, e := c.DetectGen1A(context.Background())
	if e != nil || !magic {
		t.Fatalf("magic=%v err=%v", magic, e)
	}
	// Non-magic card: first unlock does not ack with 0x0a.
	c = readerSimulator(t, []readerStep{
		{command: 1002, status: 0x68, data: []byte{1}},
		{command: 2010, request: rawReq(gen1aUnlock1Flags, 7, []byte{0x40}), data: []byte{0x04}},
		{command: 2101, status: 0x68},
	})
	magic, e = c.DetectGen1A(context.Background())
	if e != nil || magic {
		t.Fatalf("non-magic should be false: magic=%v err=%v", magic, e)
	}
}

func TestGen1ASetUID(t *testing.T) {
	uid := []byte{0x01, 0x02, 0x03, 0x04}
	block0 := make([]byte, 16)
	copy(block0, []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x62, 0x08, 0x04, 0x00})
	newBlock0 := append([]byte(nil), block0...)
	copy(newBlock0[0:4], uid)
	newBlock0[4] = uid[0] ^ uid[1] ^ uid[2] ^ uid[3]
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}}
	steps = append(steps, gen1aUnlockSteps()...)
	steps = append(steps,
		readerStep{command: 2010, request: rawReq(gen1aRWFlags, 16, []byte{0x30, 0}), data: block0},
		readerStep{command: 2010, request: rawReq(gen1aRWFlags, 16, []byte{0xA0, 0}), data: []byte{0x0a}},
		readerStep{command: 2010, request: rawReq(gen1aRWFlags, 128, newBlock0), data: []byte{0x0a}},
		readerStep{command: 2101, status: 0x68},
	)
	c := readerSimulator(t, steps)
	if e := c.Gen1ASetUID(context.Background(), uid); e != nil {
		t.Fatal(e)
	}
	if e := c.Gen1ASetUID(context.Background(), []byte{1, 2, 3}); e == nil {
		t.Fatal("bad UID length accepted")
	}
}
