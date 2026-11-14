package chameleon

import (
	"bytes"
	"context"
	"testing"
)

// gen4Raw builds the HF14A_RAW request payload the SDK sends for one Gen4
// command frame: the flag/timeout/bit-length header plus the frame.
func gen4Raw(frame []byte, first bool) []byte {
	flags := byte(0x68) // WaitResponse | AppendCRC | KeepField
	if first {
		flags = 0xF8 // + ActivateField | AutoSelect
	}
	bits := len(frame) * 8
	return append([]byte{flags, 0x03, 0xE8, byte(bits >> 8), byte(bits)}, frame...)
}

func gen4Frame(password []byte, cmd byte, data ...byte) []byte {
	frame := append([]byte{0xCF}, password...)
	frame = append(frame, cmd)
	return append(frame, data...)
}

func sampleGen4Config() []byte {
	cfg := make([]byte, 30)
	cfg[0] = 0x00                      // protocol: Classic
	cfg[1] = 0x00                      // UID length selector: 4 bytes
	copy(cfg[2:6], []byte{1, 2, 3, 4}) // password
	cfg[6] = 0x00                      // shadow mode
	cfg[7] = 0x00                      // ATS length
	cfg[24], cfg[25] = 0x04, 0x00      // ATQA stored LSB first -> 0x0004
	cfg[26] = 0x08                     // SAK
	cfg[28] = 0x3F                     // max block
	return cfg
}

func TestParseGen4Config(t *testing.T) {
	cfg := sampleGen4Config()
	c, e := ParseGen4Config(cfg)
	if e != nil || c.Protocol != 0 || c.UIDLength != 4 || c.SAK != 0x08 {
		t.Fatalf("%+v %v", c, e)
	}
	if c.ATQA.String() != "0004" || c.Password.String() != "01020304" {
		t.Fatalf("atqa/password %+v", c)
	}
	// A 34-byte response (checksum + CRC) is accepted; the extra bytes drop.
	if _, e := ParseGen4Config(append(cfg, 1, 2, 3, 4)); e != nil {
		t.Fatal(e)
	}
	if _, e := ParseGen4Config(cfg[:29]); e == nil {
		t.Fatal("short config accepted")
	}
	bad := sampleGen4Config()
	bad[7] = 17
	if _, e := ParseGen4Config(bad); e == nil {
		t.Fatal("oversized ATS accepted")
	}
}

func TestGen4ReadAndWrite(t *testing.T) {
	pw := []byte{0, 0, 0, 0}
	cfg := sampleGen4Config()
	// Read configuration.
	c := readerSimulator(t, []readerStep{
		{command: 1002, status: 0x68, data: []byte{1}},
		{command: 2010, request: gen4Raw(gen4Frame(pw, 0xC6), true), data: cfg},
		{command: 2101, status: 0x68},
	})
	got, e := c.Gen4ReadConfig(context.Background(), pw)
	if e != nil || got.SAK != 0x08 {
		t.Fatalf("%+v %v", got, e)
	}
	// Write one block.
	block := bytes.Repeat([]byte{0xAB}, 16)
	c = readerSimulator(t, []readerStep{
		{command: 1002, status: 0x68, data: []byte{1}},
		{command: 2010, request: gen4Raw(gen4Frame(pw, 0xCD, append([]byte{5}, block...)...), true), data: []byte{0x90, 0x00}},
		{command: 2101, status: 0x68},
	})
	if e := c.Gen4WriteBlock(context.Background(), pw, 5, block); e != nil {
		t.Fatal(e)
	}
	// A rejected status is reported.
	c = readerSimulator(t, []readerStep{
		{command: 1002, status: 0x68, data: []byte{1}},
		{command: 2010, request: gen4Raw(gen4Frame(pw, 0xCD, append([]byte{5}, block...)...), true), data: []byte{0x67, 0x00}},
		{command: 2101, status: 0x68},
	})
	if e := c.Gen4WriteBlock(context.Background(), pw, 5, block); e == nil {
		t.Fatal("rejected write accepted")
	}
}

func TestGen4SetUID4Byte(t *testing.T) {
	pw := []byte{0, 0, 0, 0}
	cfg := sampleGen4Config() // protocol 0, 4-byte UID
	block0 := make([]byte, 16)
	copy(block0, []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x11})
	uid := []byte{0x01, 0x02, 0x03, 0x04}
	newBlock0 := append([]byte(nil), block0...)
	copy(newBlock0[0:4], uid)
	newBlock0[4] = uid[0] ^ uid[1] ^ uid[2] ^ uid[3]
	c := readerSimulator(t, []readerStep{
		{command: 1002, status: 0x68, data: []byte{1}},
		{command: 2010, request: gen4Raw(gen4Frame(pw, 0xC6), true), data: cfg},
		{command: 2010, request: gen4Raw(gen4Frame(pw, 0xCE, 0), false), data: block0},
		{command: 2010, request: gen4Raw(gen4Frame(pw, 0xCD, append([]byte{0}, newBlock0...)...), false), data: []byte{0x90, 0x00}},
		{command: 2101, status: 0x68},
	})
	if e := c.Gen4SetUID(context.Background(), pw, uid); e != nil {
		t.Fatal(e)
	}
	// A UID of the wrong length is rejected after reading the layout.
	c = readerSimulator(t, []readerStep{
		{command: 1002, status: 0x68, data: []byte{1}},
		{command: 2010, request: gen4Raw(gen4Frame(pw, 0xC6), true), data: cfg},
		{command: 2101, status: 0x68},
	})
	if e := c.Gen4SetUID(context.Background(), pw, []byte{1, 2, 3, 4, 5, 6, 7}); e == nil {
		t.Fatal("wrong UID length accepted")
	}
}

func TestGen4PasswordValidation(t *testing.T) {
	c := readerSimulator(t, nil)
	if _, e := c.Gen4ReadConfig(context.Background(), []byte{1, 2, 3}); e == nil {
		t.Fatal("short password accepted")
	}
}
