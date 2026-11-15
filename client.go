// Package chameleon provides device workflows over TCP bridges and explicit USB maintenance.
package chameleon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var ErrClosed = errors.New("chameleon connection closed; open a new connection")

type transport interface{ io.ReadWriteCloser }

// Client serializes requests. A timeout after transmission invalidates the
// connection: this protocol has no request ID to disambiguate late responses.
type Client struct {
	transportInfo                             TransportInfo
	bridgeDeviceID                            string
	port                                      transport
	gate                                      chan struct{}
	done                                      chan struct{}
	once                                      sync.Once
	state                                     sync.Mutex
	active                                    chan struct{}
	closeErr                                  error
	dec                                       decoder
	timeout                                   time.Duration
	requests, responses, rejected, unexpected atomic.Uint64
}

type Stats struct {
	Requests            uint64 `json:"requests"`
	Responses           uint64 `json:"responses"`
	RejectedFrames      uint64 `json:"rejected_frames"`
	UnexpectedResponses uint64 `json:"unexpected_responses"`
}

// ErrUSBBridgeRequired identifies an application connection that must use a bridge.
var ErrUSBBridgeRequired = errors.New("USB application access requires the Python bridge and --port tcp:HOST:PORT; use OpenNativeUSBContext or --native-usb only for USB maintenance")

// Open opens a TCP application transport. USB and BLE use the Python bridge.
func Open(path string, timeout time.Duration) (*Client, error) {
	return OpenContext(context.Background(), path, timeout)
}

// OpenContext opens a TCP application transport without selecting a local port,
// launching Python or falling back to native USB after a bridge failure.
func OpenContext(ctx context.Context, path string, timeout time.Duration) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.HasPrefix(path, "ble:") {
		return nil, ErrBLEUnavailable
	}
	if !strings.HasPrefix(path, "tcp:") {
		return nil, ErrUSBBridgeRequired
	}
	conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", strings.TrimPrefix(path, "tcp:"))
	if err != nil {
		return nil, err
	}
	return tcpClient(ctx, conn, timeout)
}

func newClient(p transport, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	c := &Client{port: p, gate: make(chan struct{}, 1), done: make(chan struct{}), timeout: timeout}
	// Custom streams retain caller-managed policy for compatibility. Built-in
	// serial, TCP and BLE openers set explicit transport properties.
	c.transportInfo = TransportInfo{Connection: "custom", Medium: "caller-managed", AllowsPairingChanges: true, AllowsDeviceRestore: true}
	c.gate <- struct{}{}
	return c
}

func (c *Client) Close() error {
	c.once.Do(func() {
		c.state.Lock()
		close(c.done)
		active := c.active
		c.state.Unlock()
		c.closeErr = c.port.Close()
		// Do not let a caller reopen a port while an old transaction can
		// still access its transport (or an OS-reused file descriptor).
		if active != nil {
			<-active
		}
	})
	return c.closeErr
}
func (c *Client) Stats() Stats {
	return Stats{c.requests.Load(), c.responses.Load(), c.rejected.Load(), c.unexpected.Load()}
}

// acquire serializes individual queries and entire mode-switch/scan/restore
// sequences. Waiting callers can cancel without disturbing the active operation.
func (c *Client) acquire(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrClosed
	case <-c.gate:
	}
	if err := ctx.Err(); err != nil {
		c.release()
		return err
	}
	return nil
}
func (c *Client) release() { c.gate <- struct{}{} }

func (c *Client) query(ctx context.Context, command uint16) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := c.acquire(ctx); err != nil {
		return nil, err
	}
	defer c.release()
	return c.exchange(ctx, command, nil, 0x68)
}

// exchange requires the caller to hold the operation gate. A command-specific
// success status is required: HF discovery returns 0x00, not device status 0x68.
func (c *Client) exchange(ctx context.Context, command uint16, payload []byte, success uint16) ([]byte, error) {
	f, err := c.exchangeFrame(ctx, command, payload)
	if err != nil {
		return nil, err
	}
	if f.status != success {
		return nil, &StatusError{command, f.status}
	}
	return f.data, nil
}

func (c *Client) exchangeFrame(ctx context.Context, command uint16, payload []byte) (frame, error) {
	if err := c.checkTransportCommand(ctx, command); err != nil {
		return frame{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return frame{}, err
	}
	if len(payload) > MaxPayload {
		return frame{}, fmt.Errorf("payload exceeds %d bytes", MaxPayload)
	}
	c.state.Lock()
	select {
	case <-c.done:
		c.state.Unlock()
		return frame{}, ErrClosed
	default:
	}
	workerDone := make(chan struct{})
	c.active = workerDone
	c.state.Unlock()
	type result struct {
		value frame
		err   error
	}
	resultCh := make(chan result, 1)
	go func() {
		defer close(workerDone)
		select {
		case <-c.done:
			resultCh <- result{err: ErrClosed}
			return
		default:
		}
		b := encode(command, payload...)
		c.requests.Add(1)
		for len(b) > 0 {
			n, err := c.port.Write(b)
			if err != nil {
				resultCh <- result{err: err}
				return
			}
			if n == 0 {
				resultCh <- result{err: io.ErrShortWrite}
				return
			}
			b = b[n:]
		}
		buf := make([]byte, 512)
		var readErr error
		for {
			for {
				f, ok := c.dec.next()
				c.rejected.Store(c.dec.rejected)
				if !ok {
					break
				}
				if f.command != command {
					c.unexpected.Add(1)
					continue
				}
				c.responses.Add(1)
				resultCh <- result{value: f}
				return
			}
			if readErr != nil {
				resultCh <- result{err: readErr}
				return
			}
			n, err := c.port.Read(buf)
			readErr = err
			if n > 0 {
				c.dec.buf = append(c.dec.buf, buf[:n]...)
			}
			select {
			case <-c.done:
				resultCh <- result{err: ErrClosed}
				return
			default:
			}
		}
	}()
	select {
	case r := <-resultCh:
		if r.err != nil {
			var status *StatusError
			if !errors.As(r.err, &status) {
				_ = c.Close()
			}
		}
		return r.value, r.err
	case <-ctx.Done():
		_ = c.Close()
		return frame{}, fmt.Errorf("command %d: %w (connection invalidated)", command, ctx.Err())
	case <-c.done:
		return frame{}, ErrClosed
	}
}
