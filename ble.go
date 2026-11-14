package chameleon

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
)

const NUSServiceUUID = "6E400001-B5A3-F393-E0A9-E50E24DCCA9E"
const NUSRXUUID = "6E400002-B5A3-F393-E0A9-E50E24DCCA9E"
const NUSTXUUID = "6E400003-B5A3-F393-E0A9-E50E24DCCA9E"

var ErrBLEUnavailable = errors.New("native BLE requires macOS with cgo/CoreBluetooth; build with scripts/build.sh")

type BLEDevice struct {
	DFU  bool   `json:"dfu"`
	ID   string `json:"id"`
	Name string `json:"name"`
	RSSI int    `json:"rssi"`
}

// ScanBLE does not connect, pair, alter adapter power, or change device data.
func ScanBLE(ctx context.Context, duration time.Duration) ([]BLEDevice, error) {
	if duration <= 0 || duration > 2*time.Minute {
		return nil, errors.New("BLE scan duration must be positive and at most 2m")
	}
	setup, cancel := context.WithTimeout(ctx, duration+10*time.Second)
	defer cancel()
	return nativeBLEScan(setup, duration)
}
func OpenBLE(ctx context.Context, id string, timeout time.Duration) (*Client, error) {
	setup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stream, e := nativeBLEOpen(setup, id, timeout)
	if e != nil {
		return nil, e
	}
	c := NewClient(stream, timeout)
	c.transportInfo = bleTransport("ble", false)
	model, _, e := c.Model(setup)
	if e != nil || model > 1 {
		_ = c.Close()
		if e == nil {
			e = errors.New("BLE peer is not a supported Chameleon model")
		}
		return nil, e
	}
	return c, nil
}

// notificationStream turns lossless GATT notifications into a byte stream.
// Overflow fails the connection rather than silently dropping protocol bytes.
type notificationStream struct {
	mu         sync.Mutex
	cond       *sync.Cond
	buf        []byte
	err        error
	closed     bool
	once       sync.Once
	writeMu    sync.Mutex
	chunk      int
	write      func([]byte) (int, error)
	disconnect func() error
}

func newNotificationStream(chunk int, write func([]byte) (int, error), disconnect func() error) *notificationStream {
	if chunk < 1 {
		chunk = 20
	}
	s := &notificationStream{chunk: chunk, write: write, disconnect: disconnect}
	s.cond = sync.NewCond(&s.mu)
	return s
}
func (s *notificationStream) push(b []byte) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if len(s.buf)+len(b) > 65536 {
		s.mu.Unlock()
		s.fail(errors.New("BLE notification buffer overflow"))
		return
	}
	s.buf = append(s.buf, b...)
	s.cond.Signal()
	s.mu.Unlock()
}
func (s *notificationStream) fail(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
}
func (s *notificationStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.buf) == 0 && !s.closed {
		s.cond.Wait()
	}
	if len(s.buf) > 0 {
		n := copy(p, s.buf)
		s.buf = s.buf[n:]
		return n, nil
	}
	if s.err != nil {
		return 0, s.err
	}
	return 0, io.EOF
}
func (s *notificationStream) Write(p []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	written := 0
	for len(p) > 0 {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return written, ErrClosed
		}
		n := min(s.chunk, len(p))
		got, e := s.write(p[:n])
		if got < 0 || got > n {
			return written, io.ErrShortWrite
		}
		written += got
		if e != nil {
			return written, e
		}
		if got != n {
			return written, io.ErrShortWrite
		}
		p = p[n:]
	}
	return written, nil
}
func (s *notificationStream) Close() error {
	var e error
	s.once.Do(func() {
		s.fail(ErrClosed)
		if s.disconnect != nil {
			e = s.disconnect()
		}
	})
	return e
}

const DFUServiceUUID = "FE59"
const DFUControlUUID = "8EC90001-F315-4F60-9FB8-838830DAEA50"
const DFUPacketUUID = "8EC90002-F315-4F60-9FB8-838830DAEA50"

func bleUUIDEqual(a, b string) bool {
	normalize := func(s string) string {
		s = strings.ToUpper(s)
		if len(s) == 36 && strings.HasPrefix(s, "0000") && strings.HasSuffix(s, "-0000-1000-8000-00805F9B34FB") {
			return s[4:8]
		}
		return s
	}
	return normalize(a) == normalize(b)
}
