package dfu

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"sync"
	"time"
)

type ProtocolError struct {
	Operation byte
	Code      byte
	Extended  byte
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("DFU operation 0x%02X rejected: result=0x%02X extended=0x%02X", e.Operation, e.Code, e.Extended)
}

type Image struct {
	Init     []byte
	Firmware []byte
}
type Options struct {
	Timeout  time.Duration
	Progress func(offset, total int)
}
type Result struct {
	Bytes                    int    `json:"image_bytes"`
	VerifiedOffset           int    `json:"verified_offset"`
	ResumedOffset            int    `json:"resumed_offset"`
	CRC32                    uint32 `json:"crc32"`
	ExecutedObjects          int    `json:"executed_objects"`
	FinalExecuteAcknowledged bool   `json:"final_execute_acknowledged"`
}

// BLETransport separates Secure DFU control, packet and notification characteristics.
// Writes and notifications must obey ctx; Close unblocks all pending operations.
type BLETransport interface {
	WriteControl(context.Context, []byte) error
	WritePacket(context.Context, []byte) error
	Notification(context.Context) ([]byte, error)
	Close() error
}

type session struct {
	ble     BLETransport
	stream  io.ReadWriteCloser
	reader  *bufio.Reader
	timeout time.Duration
	once    sync.Once
	chunk   int
	result  Result
}

func (s *session) close() {
	s.once.Do(func() {
		if s.ble != nil {
			_ = s.ble.Close()
		} else {
			_ = s.stream.Close()
		}
	})
}

// call invalidates the transport on timeout; there are no blind write retries.
func (s *session) call(ctx context.Context, op byte, payload []byte, writeOnly bool) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	type reply struct {
		data []byte
		err  error
	}
	ch := make(chan reply, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		var b []byte
		var e error
		expected := op
		if s.ble != nil {
			if writeOnly {
				e = s.ble.WritePacket(ctx, payload)
				expected = 3
			} else {
				e = s.ble.WriteControl(ctx, append([]byte{op}, payload...))
			}
			if e == nil {
				b, e = s.ble.Notification(ctx)
			}
		} else {
			e = writeAll(s.stream, EncodeSLIP(append([]byte{op}, payload...)))
			if e == nil && writeOnly {
				ch <- reply{}
				return
			}
			if e == nil {
				b, e = readSLIP(s.reader)
			}
		}
		if e != nil {
			ch <- reply{err: e}
			return
		}
		if len(b) < 3 || b[0] != 0x60 || b[1] != expected {
			ch <- reply{err: fmt.Errorf("unexpected DFU response %x for operation %02x", b, op)}
			return
		}
		if b[2] != 1 {
			extended := byte(0)
			if len(b) > 3 {
				extended = b[3]
			}
			ch <- reply{err: &ProtocolError{op, b[2], extended}}
			return
		}
		ch <- reply{data: b[3:]}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			s.close()
		}
		return r.data, r.err
	case <-ctx.Done():
		s.close()
		<-finished
		return nil, ctx.Err()
	}
}
func le32(n int) []byte { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, uint32(n)); return b }
func (s *session) selectObject(ctx context.Context, kind byte) (size, offset int, crc uint32, err error) {
	b, e := s.call(ctx, 6, []byte{kind}, false)
	if e != nil {
		return 0, 0, 0, e
	}
	if len(b) != 12 {
		return 0, 0, 0, fmt.Errorf("invalid DFU select response length")
	}
	size = int(binary.LittleEndian.Uint32(b))
	rawOffset := binary.LittleEndian.Uint32(b[4:])
	if rawOffset > 2<<20 {
		return 0, 0, 0, fmt.Errorf("DFU offset too large")
	}
	offset = int(rawOffset)
	crc = binary.LittleEndian.Uint32(b[8:])
	if size < 1 || size > 2<<20 {
		return 0, 0, 0, fmt.Errorf("invalid DFU object size %d", size)
	}
	return size, offset, crc, nil
}
func (s *session) execute(ctx context.Context) error {
	_, e := s.call(ctx, 4, nil, false)
	if e == nil {
		s.result.ExecutedObjects++
	}
	return e
}
func (s *session) create(ctx context.Context, kind byte, size int) error {
	_, e := s.call(ctx, 1, append([]byte{kind}, le32(size)...), false)
	return e
}
func (s *session) streamData(ctx context.Context, full []byte, from, to int) error {
	for pos := from; pos < to; {
		n := min(s.chunk, to-pos)
		if b, e := s.call(ctx, 8, full[pos:pos+n], true); e != nil {
			return e
		} else if s.ble != nil {
			if len(b) != 8 || binary.LittleEndian.Uint32(b) != uint32(pos+n) || binary.LittleEndian.Uint32(b[4:]) != crc32.ChecksumIEEE(full[:pos+n]) {
				return fmt.Errorf("BLE DFU packet receipt CRC/offset mismatch")
			}
		}
		pos += n
	}
	b, e := s.call(ctx, 3, nil, false)
	if e != nil {
		return e
	}
	if len(b) != 8 {
		return fmt.Errorf("invalid DFU checksum response length")
	}
	gotOffset := binary.LittleEndian.Uint32(b)
	gotCRC := binary.LittleEndian.Uint32(b[4:])
	expected := crc32.ChecksumIEEE(full[:to])
	if gotOffset != uint32(to) || gotCRC != expected {
		return fmt.Errorf("DFU CRC/offset mismatch: got %d/%08x expected %d/%08x; upload stopped", gotOffset, gotCRC, to, expected)
	}
	return nil
}

// Upload owns/closes stream. The caller must already be connected to the
// bootloader and must ensure Close unblocks Read/Write. Init is verified by the
// bootloader before firmware data is sent. Only matching prefixes are resumed;
// corrupted/different firmware prefixes stop instead of guessing a rewind.
func Upload(ctx context.Context, stream io.ReadWriteCloser, image Image, options Options) (Result, error) {
	return upload(ctx, stream, nil, image, options)
}
func UploadBLE(ctx context.Context, transport BLETransport, image Image, options Options) (Result, error) {
	return upload(ctx, nil, transport, image, options)
}
func upload(ctx context.Context, stream io.ReadWriteCloser, ble BLETransport, image Image, options Options) (result Result, err error) {
	if stream == nil && ble == nil {
		return result, fmt.Errorf("nil DFU transport")
	}
	s := &session{stream: stream, ble: ble, timeout: options.Timeout}
	if stream != nil {
		s.reader = bufio.NewReader(stream)
	}
	defer s.close()
	defer func() { result = s.result }()
	if s.timeout <= 0 {
		s.timeout = 5 * time.Second
	}
	if len(image.Init) < 1 || len(image.Init) > 65536 || len(image.Firmware) < 1 || len(image.Firmware) > 2<<20 {
		return result, fmt.Errorf("invalid init packet or firmware size")
	}
	// Snapshot input: the binary and CRC calculations refer to the same bytes.
	image.Init = append([]byte(nil), image.Init...)
	image.Firmware = append([]byte(nil), image.Firmware...)
	if s.ble != nil {
		s.chunk = 20
		if _, e := s.call(ctx, 2, []byte{1, 0}, false); e != nil {
			return result, e
		}
	} else {
		if b, e := s.call(ctx, 9, []byte{1}, false); e != nil {
			return result, e
		} else if len(b) != 1 || b[0] != 1 {
			return result, fmt.Errorf("invalid DFU ping echo")
		}

		mtu, e := s.call(ctx, 7, nil, false)
		if e != nil {
			return result, e
		}
		if len(mtu) != 2 {
			return result, fmt.Errorf("invalid DFU MTU response")
		}
		s.chunk = (int(binary.LittleEndian.Uint16(mtu))-1)/2 - 1
		if s.chunk < 1 {
			return result, fmt.Errorf("DFU MTU too small")
		}
		s.chunk = min(s.chunk, 4096)
		if _, e := s.call(ctx, 2, []byte{0, 0}, false); e != nil {
			return result, e
		}
	}
	maxSize, offset, crc, e := s.selectObject(ctx, 1)
	if e != nil {
		return result, e
	}
	if len(image.Init) > maxSize {
		return result, fmt.Errorf("init packet exceeds bootloader object size")
	}
	if offset < 1 || offset > len(image.Init) || crc32.ChecksumIEEE(image.Init[:offset]) != crc {
		if e = s.create(ctx, 1, len(image.Init)); e != nil {
			return result, e
		}
		offset = 0
	}
	if offset < len(image.Init) {
		if e = s.streamData(ctx, image.Init, offset, len(image.Init)); e != nil {
			return result, e
		}
	}
	if e = s.execute(ctx); e != nil {
		return result, fmt.Errorf("init packet not accepted: %w", e)
	}
	maxSize, offset, crc, e = s.selectObject(ctx, 2)
	if e != nil {
		return result, e
	}
	if offset > len(image.Firmware) {
		return result, fmt.Errorf("bootloader offset exceeds firmware length")
	}
	if crc32.ChecksumIEEE(image.Firmware[:offset]) != crc {
		return result, fmt.Errorf("DFU resume prefix CRC mismatch; no firmware data written")
	}
	s.result.ResumedOffset = offset
	s.result.VerifiedOffset = offset
	s.result.Bytes = len(image.Firmware)
	s.result.CRC32 = crc32.ChecksumIEEE(image.Firmware)
	if offset > 0 {
		end := min(len(image.Firmware), offset+(maxSize-offset%maxSize))
		if offset%maxSize != 0 && offset < len(image.Firmware) {
			if e = s.streamData(ctx, image.Firmware, offset, end); e != nil {
				return result, e
			}
			offset = end
			s.result.VerifiedOffset = offset
		}
		if e = s.execute(ctx); e != nil {
			return result, e
		}
		if options.Progress != nil {
			options.Progress(offset, len(image.Firmware))
		}
	}
	for offset < len(image.Firmware) {
		end := min(len(image.Firmware), offset+maxSize)
		if e = s.create(ctx, 2, end-offset); e != nil {
			return result, e
		}
		if e = s.streamData(ctx, image.Firmware, offset, end); e != nil {
			return result, e
		}
		if e = s.execute(ctx); e != nil {
			return result, e
		}
		offset = end
		s.result.VerifiedOffset = offset
		if options.Progress != nil {
			options.Progress(offset, len(image.Firmware))
		}
	}
	s.result.FinalExecuteAcknowledged = true
	return result, nil
}
