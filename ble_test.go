package chameleon

import (
	"bytes"
	"io"
	"sync"
	"testing"
	"time"
)

func TestNotificationStream(t *testing.T) {
	var chunks [][]byte
	closed := false
	s := newNotificationStream(3, func(b []byte) (int, error) { chunks = append(chunks, append([]byte(nil), b...)); return len(b), nil }, func() error { closed = true; return nil })
	s.push([]byte{1, 2})
	s.push([]byte{3, 4})
	out := make([]byte, 4)
	if _, e := io.ReadFull(s, out); e != nil || !bytes.Equal(out, []byte{1, 2, 3, 4}) {
		t.Fatal(out, e)
	}
	if n, e := s.Write([]byte{1, 2, 3, 4, 5, 6, 7}); n != 7 || e != nil || len(chunks) != 3 {
		t.Fatal(chunks, n, e)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, e := s.Read(out); e == nil {
			t.Error("read succeeded after close")
		}
	}()
	_ = s.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("read blocked")
	}
	if !closed {
		t.Fatal("not disconnected")
	}
}
func TestNotificationOverflowAndConcurrentClose(t *testing.T) {
	s := newNotificationStream(20, nil, nil)
	s.push(make([]byte, 65537))
	if _, e := s.Read(make([]byte, 1)); e == nil {
		t.Fatal("overflow not reported")
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = s.Close(); s.push([]byte{1}) }()
	}
	wg.Wait()
}
func TestBLEUUIDComparison(t *testing.T) {
	if !bleUUIDEqual("FE59", "0000fe59-0000-1000-8000-00805f9b34fb") || !bleUUIDEqual(NUSServiceUUID, "6e400001-b5a3-f393-e0a9-e50e24dcca9e") || bleUUIDEqual(DFUControlUUID, DFUPacketUUID) {
		t.Fatal("UUID normalization")
	}
}
