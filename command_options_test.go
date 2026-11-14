package chameleon

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestUnregisteredCommandOptionsPreserveWireStatusAndMode(t *testing.T) {
	for _, method := range []string{"client", "reader"} {
		for _, status := range []uint16{0, 103} {
			t.Run(method+"/"+StatusName(status), func(t *testing.T) {
				payload, data := []byte{1, 2, 3}, []byte{4, 5, 6}
				var steps []readerStep
				if method == "reader" {
					steps = append(steps,
						readerStep{command: 1002, status: 0x68, data: []byte{0}},
						readerStep{command: 1001, status: 0x68, request: []byte{1}})
				}
				steps = append(steps, readerStep{command: uint16(CmdGenericRead), status: status, request: payload, data: data})
				if method == "reader" {
					steps = append(steps, readerStep{command: 1001, status: 0x68, request: []byte{0}})
				}
				steps = append(steps, readerStep{command: 1000, status: 0x68, data: []byte{2, 1}})
				c := readerSimulator(t, steps)
				var r Response
				var e error
				options := CommandOptions{AllowUnregistered: true}
				if method == "client" {
					if _, err := c.Execute(context.Background(), CmdGenericRead, payload); err == nil {
						t.Fatal("default execution accepted an unregistered command")
					}
					r, e = c.ExecuteWithOptions(context.Background(), CmdGenericRead, payload, options)
				} else {
					e = c.Reader(context.Background(), func(s *ReaderSession) error {
						if _, err := s.Do(context.Background(), CmdGenericRead, payload); err == nil {
							t.Fatal("default reader execution accepted an unregistered command")
						}
						var err error
						r, err = s.DoWithOptions(context.Background(), CmdGenericRead, payload, options)
						return err
					})
				}
				if e != nil || r.Command != CmdGenericRead || r.Status != status || r.StatusName != StatusName(status) || r.NoResponse || !bytes.Equal(r.Data, data) {
					t.Fatalf("wire response changed: %+v %v", r, e)
				}
				if status == 0 {
					if err := r.Check(0); err != nil {
						t.Fatal(err)
					}
				} else {
					var rejected *StatusError
					if !errors.As(r.Check(0), &rejected) || rejected.Command != uint16(CmdGenericRead) || rejected.Status != status {
						t.Fatal("firmware rejection lost its command or status")
					}
				}
				if version, err := c.Firmware(context.Background()); err != nil || version != (Version{2, 1}) {
					t.Fatalf("response invalidated the connection: %+v %v", version, err)
				}
			})
		}
	}
}

func TestCommandOptionsKeepUnknownAndSessionGuards(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{1}}})
	for _, options := range []CommandOptions{{}, {AllowUnregistered: true}} {
		if _, e := c.ExecuteWithOptions(context.Background(), Command(65535), nil, options); e == nil {
			t.Fatal("unknown command sent")
		}
	}
	var retained *ReaderSession
	if e := c.Reader(context.Background(), func(s *ReaderSession) error {
		retained = s
		for _, options := range []CommandOptions{{}, {AllowUnregistered: true}} {
			for _, cmd := range []Command{Command(65535), CmdEnterBootloader} {
				if _, err := s.DoWithOptions(context.Background(), cmd, nil, options); err == nil {
					t.Fatal("unsafe reader command sent", cmd)
				}
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := retained.DoWithOptions(context.Background(), CmdGenericRead, nil, CommandOptions{AllowUnregistered: true}); !errors.Is(e, ErrClosed) {
		t.Fatalf("compatibility option revived an expired session: %v", e)
	}
	if c.Stats().Requests != 1 {
		t.Fatal("rejected requests reached the transport", c.Stats())
	}
}

func TestCommandOptionsKeepQueuedCancellationAndPayloadBounds(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: uint16(CmdGenericRead), data: []byte{7}}})
	options := CommandOptions{AllowUnregistered: true}
	<-c.gate
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := c.ExecuteWithOptions(ctx, CmdGenericRead, nil, options); !errors.Is(e, context.Canceled) {
		t.Fatalf("queued cancellation ignored: %v", e)
	}
	c.gate <- struct{}{}
	if _, e := c.ExecuteWithOptions(context.Background(), CmdGenericRead, make([]byte, MaxPayload+1), options); e == nil {
		t.Fatal("compatibility option bypassed payload bounds")
	}
	if r, e := c.ExecuteWithOptions(context.Background(), CmdGenericRead, nil, options); e != nil || !bytes.Equal(r.Data, []byte{7}) {
		t.Fatalf("pre-transmission rejection invalidated the connection: %+v %v", r, e)
	}
}

func TestCommandOptionsPreserveSendOnlyBootloader(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: uint16(CmdEnterBootloader)}})
	r, e := c.ExecuteWithOptions(context.Background(), CmdEnterBootloader, nil, CommandOptions{AllowUnregistered: true})
	if e != nil || r.Command != CmdEnterBootloader || !r.NoResponse || r.Check(0) == nil {
		t.Fatalf("bootloader response semantics changed: %+v %v", r, e)
	}
	if c.Stats().Requests != 1 || c.Stats().Responses != 0 {
		t.Fatal("bootloader waited for a reply", c.Stats())
	}
	if _, e := c.ExecuteWithOptions(context.Background(), CmdGenericRead, nil, CommandOptions{AllowUnregistered: true}); !errors.Is(e, ErrClosed) {
		t.Fatalf("bootloader did not invalidate the connection: %v", e)
	}
}
