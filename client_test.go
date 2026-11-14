package chameleon

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func simulator(t *testing.T, handler func(uint16) []byte) *Client {
	t.Helper()
	client, server := net.Pipe()
	c := newClient(client, time.Second)
	t.Cleanup(func() { _ = c.Close(); _ = server.Close() })
	go func() {
		defer server.Close()
		request := make([]byte, 10)
		for {
			if _, e := io.ReadFull(server, request); e != nil {
				return
			}
			cmd := binary.BigEndian.Uint16(request[2:4])
			b := handler(cmd)
			if b != nil {
				if _, e := server.Write(b); e != nil {
					return
				}
			}
		}
	}()
	return c
}
func TestConcurrentQueriesSerialized(t *testing.T) {
	c := simulator(t, func(cmd uint16) []byte { return response(cmd, 0x68, []byte{2, 1}) })
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := c.Firmware(context.Background())
			if e != nil || v != (Version{2, 1}) {
				t.Errorf("%+v %v", v, e)
			}
		}()
	}
	wg.Wait()
	if s := c.Stats(); s.Requests != 32 || s.Responses != 32 {
		t.Fatalf("%+v", s)
	}
}
func TestTimeoutInvalidatesConnection(t *testing.T) {
	c := simulator(t, func(uint16) []byte { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, e := c.Firmware(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("%v", e)
	}
	if _, e := c.Firmware(context.Background()); !errors.Is(e, ErrClosed) {
		t.Fatalf("%v", e)
	}
}
func TestCanceledQueuedRequestDoesNotCloseConnection(t *testing.T) {
	c := simulator(t, func(cmd uint16) []byte { return response(cmd, 0x68, []byte{2, 1}) })
	<-c.gate
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := c.Firmware(ctx); !errors.Is(e, context.Canceled) {
		t.Fatalf("%v", e)
	}
	c.gate <- struct{}{}
	if _, e := c.Firmware(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestFirmwareErrorKeepsConnection(t *testing.T) {
	calls := 0
	c := simulator(t, func(cmd uint16) []byte {
		calls++
		if calls == 1 {
			return response(cmd, 0x67, nil)
		}
		return response(cmd, 0x68, []byte{2, 1})
	})
	var status *StatusError
	if _, e := c.Firmware(context.Background()); !errors.As(e, &status) || status.Status != 0x67 {
		t.Fatalf("%v", e)
	}
	if _, e := c.Firmware(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestUnexpectedAndCorruptResponses(t *testing.T) {
	c := simulator(t, func(cmd uint16) []byte {
		bad := response(cmd, 0x68, []byte{2, 1})
		bad[8] ^= 1
		return append(append(bad, response(999, 0x68, nil)...), response(cmd, 0x68, []byte{2, 1})...)
	})
	if _, e := c.Firmware(context.Background()); e != nil {
		t.Fatal(e)
	}
	s := c.Stats()
	if s.RejectedFrames != 1 || s.UnexpectedResponses != 1 {
		t.Fatalf("%+v", s)
	}
}
func TestCloseUnblocksQuery(t *testing.T) {
	received := make(chan struct{})
	c := simulator(t, func(uint16) []byte { close(received); return nil })
	done := make(chan error, 1)
	go func() { _, e := c.Firmware(context.Background()); done <- e }()
	<-received
	_ = c.Close()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("expected error")
		}
	case <-time.After(time.Second):
		t.Fatal("close did not unblock query")
	}
}
func TestShortPayloadRejected(t *testing.T) {
	c := simulator(t, func(cmd uint16) []byte { return response(cmd, 0x68, []byte{2}) })
	if _, e := c.Firmware(context.Background()); e == nil {
		t.Fatal("accepted malformed version")
	}
}

func TestDisconnectInvalidatesConnection(t *testing.T) {
	local, peer := net.Pipe()
	c := newClient(local, time.Second)
	defer c.Close()
	go func() {
		request := make([]byte, 10)
		_, _ = io.ReadFull(peer, request)
		_ = peer.Close()
	}()
	if _, err := c.Firmware(context.Background()); err == nil {
		t.Fatal("expected disconnect error")
	}
	if _, err := c.Firmware(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected closed client, got %v", err)
	}
}

func TestBlockedWriteTimeoutStopsWorker(t *testing.T) {
	local, peer := net.Pipe()
	defer peer.Close() // Deliberately never read the request.
	c := newClient(local, time.Second)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Firmware(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout, got %v", err)
	}
	select {
	case <-c.active:
	default:
		t.Fatal("Close returned before transaction exited")
	}
}
func TestSlotParsing(t *testing.T) {
	c := simulator(t, func(cmd uint16) []byte {
		switch cmd {
		case 1019:
			b := make([]byte, 32)
			binary.BigEndian.PutUint16(b, 1001)
			binary.BigEndian.PutUint16(b[2:], 100)
			return response(cmd, 0x68, b)
		case 1023:
			b := make([]byte, 16)
			b[0] = 1
			return response(cmd, 0x68, b)
		case 1018:
			return response(cmd, 0x68, []byte{0})
		}
		return response(cmd, 0x67, nil)
	})
	slots, e := c.Slots(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if len(slots) != 8 || slots[0].HFType != 1001 || slots[0].LFType != 100 || !slots[0].Active || !slots[0].HFEnabled || slots[0].LFEnabled || slots[7].Number != 8 {
		t.Fatalf("%+v", slots)
	}
}
