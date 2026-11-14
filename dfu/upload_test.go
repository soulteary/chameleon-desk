package dfu

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"net"
	"testing"
	"time"
)

func TestSLIP(t *testing.T) {
	for _, b := range [][]byte{{1}, {0xc0, 0xdb, 0, 1}, bytes.Repeat([]byte{0xdb}, 200)} {
		got, e := readSLIP(bufio.NewReader(bytes.NewReader(EncodeSLIP(b))))
		if e != nil || !bytes.Equal(got, b) {
			t.Fatalf("%x %v", got, e)
		}
	}
	for _, b := range [][]byte{{0xdb, 0x01}, bytes.Repeat([]byte{1}, 4097)} {
		if _, e := readSLIP(bufio.NewReader(bytes.NewReader(b))); e == nil {
			t.Fatal("accepted malformed packet")
		}
	}
}

type boot struct {
	prn                bool
	init, data         []byte
	kind               byte
	badCRC, rejectInit bool
	executed           int
}

func (b *boot) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	for {
		p, e := readSLIP(r)
		if e != nil {
			return
		}
		op := p[0]
		out := []byte{0x60, op, 1}
		switch op {
		case 9:
			out = append(out, p[1])
		case 2:
			b.prn = p[1] == 1
		case 7:
			out = append(out, 64, 0)
		case 6:
			b.kind = p[1]
			d := b.data
			if b.kind == 1 {
				d = b.init
			}
			out = append(out, le32(32)...)
			out = append(out, le32(len(d))...)
			crc := crc32.ChecksumIEEE(d)
			if b.badCRC && b.kind == 2 {
				crc++
			}
			buf := make([]byte, 4)
			binary.LittleEndian.PutUint32(buf, crc)
			out = append(out, buf...)
		case 1:
			b.kind = p[1]
			if b.kind == 1 {
				b.init = nil
			}
		case 8:
			if b.kind == 1 {
				b.init = append(b.init, p[1:]...)
			} else {
				b.data = append(b.data, p[1:]...)
			}
			if !b.prn {
				continue
			}
			out[1] = 3
			d := b.data
			if b.kind == 1 {
				d = b.init
			}
			out = append(out, le32(len(d))...)
			buf := make([]byte, 4)
			binary.LittleEndian.PutUint32(buf, crc32.ChecksumIEEE(d))
			out = append(out, buf...)
		case 3:
			d := b.data
			if b.kind == 1 {
				d = b.init
			}
			out = append(out, le32(len(d))...)
			buf := make([]byte, 4)
			binary.LittleEndian.PutUint32(buf, crc32.ChecksumIEEE(d))
			out = append(out, buf...)
		case 4:
			if b.kind == 1 && b.rejectInit {
				out[2] = 5
			} else {
				b.executed++
			}
		default:
			return
		}
		if e = writeAll(c, EncodeSLIP(out)); e != nil {
			return
		}
	}
}
func TestUpload(t *testing.T) {
	image := Image{Init: []byte{1, 2, 0xc0, 0xdb}, Firmware: bytes.Repeat([]byte{0xc0, 0xdb, 1, 2, 3}, 29)}
	for _, offset := range []int{0, 7, 32, 64, len(image.Firmware)} {
		t.Run(string(rune('A'+offset)), func(t *testing.T) {
			a, c := net.Pipe()
			b := &boot{data: append([]byte(nil), image.Firmware[:offset]...)}
			done := make(chan struct{})
			go func() { b.serve(c); close(done) }()
			last := offset
			result, e := Upload(context.Background(), a, image, Options{Timeout: time.Second, Progress: func(n, total int) {
				if n < last || n > total {
					t.Errorf("invalid progress")
				}
				last = n
			}})
			<-done
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(b.data, image.Firmware) || !bytes.Equal(b.init, image.Init) || result.ResumedOffset != offset || result.VerifiedOffset != len(image.Firmware) || !result.FinalExecuteAcknowledged {
				t.Fatalf("bad result %+v", result)
			}
		})
	}
}
func TestUploadFailsClosed(t *testing.T) {
	for _, reject := range []bool{false, true} {
		a, c := net.Pipe()
		b := &boot{badCRC: !reject, rejectInit: reject}
		done := make(chan struct{})
		go func() { b.serve(c); close(done) }()
		v, e := Upload(context.Background(), a, Image{Init: []byte{1}, Firmware: []byte{2, 3}}, Options{Timeout: time.Second})
		<-done
		if e == nil || v.FinalExecuteAcknowledged || len(b.data) != 0 {
			t.Fatalf("unsafe result %+v %v", v, e)
		}
	}
}
func TestUploadTimeoutClosesTransport(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	start := time.Now()
	_, e := Upload(context.Background(), a, Image{Init: []byte{1}, Firmware: []byte{2}}, Options{Timeout: 20 * time.Millisecond})
	if e == nil || time.Since(start) > time.Second {
		t.Fatal(e)
	}
	if _, e = b.Read(make([]byte, 1)); e != io.EOF {
		t.Fatalf("peer not closed: %v", e)
	}
}

type bleMock struct {
	conn   net.Conn
	reader *bufio.Reader
	t      *testing.T
}

func (b *bleMock) WriteControl(ctx context.Context, p []byte) error {
	if p[0] == 8 || p[0] == 7 || p[0] == 9 {
		b.t.Errorf("serial-only operation on BLE control: %x", p)
	}
	return writeAll(b.conn, EncodeSLIP(p))
}
func (b *bleMock) WritePacket(ctx context.Context, p []byte) error {
	if len(p) > 20 {
		b.t.Error("oversized packet")
	}
	return writeAll(b.conn, EncodeSLIP(append([]byte{8}, p...)))
}
func (b *bleMock) Notification(ctx context.Context) ([]byte, error) { return readSLIP(b.reader) }
func (b *bleMock) Close() error                                     { return b.conn.Close() }
func TestBLEUploadPRNAndResume(t *testing.T) {
	for _, offset := range []int{0, 7, 32} {
		a, c := net.Pipe()
		image := Image{Init: []byte{1, 2, 3}, Firmware: bytes.Repeat([]byte{1, 2, 3, 4, 0xc0}, 17)}
		boot := &boot{data: append([]byte(nil), image.Firmware[:offset]...)}
		done := make(chan struct{})
		go func() { boot.serve(c); close(done) }()
		v, e := UploadBLE(context.Background(), &bleMock{a, bufio.NewReader(a), t}, image, Options{Timeout: time.Second})
		<-done
		if e != nil || !v.FinalExecuteAcknowledged || !bytes.Equal(boot.data, image.Firmware) || !boot.prn {
			t.Fatal(v, e)
		}
	}
}

type corruptBLE struct{ *bleMock }

func (b *corruptBLE) Notification(ctx context.Context) ([]byte, error) {
	p, e := b.bleMock.Notification(ctx)
	if len(p) == 11 && p[1] == 3 {
		p[10] ^= 1
	}
	return p, e
}
func TestBLEReceiptCorruptionStopsBeforeFirmware(t *testing.T) {
	a, c := net.Pipe()
	boot := &boot{}
	done := make(chan struct{})
	go func() { boot.serve(c); close(done) }()
	transport := &corruptBLE{&bleMock{a, bufio.NewReader(a), t}}
	v, e := UploadBLE(context.Background(), transport, Image{Init: []byte{1}, Firmware: []byte{2, 3}}, Options{Timeout: time.Second})
	<-done
	if e == nil || v.FinalExecuteAcknowledged || len(boot.data) != 0 {
		t.Fatal(v, e)
	}
}
