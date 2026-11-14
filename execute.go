package chameleon

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const SDKVersion = "0.8.0"

type CommandInfo struct {
	ID         Command `json:"id"`
	Name       string  `json:"name"`
	Group      string  `json:"group"`
	Effect     string  `json:"effect"`
	Registered bool    `json:"registered"`
}

func Commands() []CommandInfo { return append([]CommandInfo(nil), commandCatalog...) }
func LookupCommand(cmd Command) (CommandInfo, bool) {
	for _, s := range commandCatalog {
		if s.ID == cmd {
			return s, true
		}
	}
	return CommandInfo{}, false
}
func ParseCommand(name string) (Command, error) {
	n := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(name), "-", "_"))
	for _, s := range commandCatalog {
		if s.Name == n {
			return s.ID, nil
		}
	}
	base := 10
	if strings.HasPrefix(strings.ToLower(name), "0x") {
		base = 0
	}
	v, e := strconv.ParseUint(name, base, 16)
	if e != nil {
		return 0, fmt.Errorf("unknown command %q", name)
	}
	if _, ok := LookupCommand(Command(v)); !ok {
		return 0, fmt.Errorf("command %d is not in pinned catalog", v)
	}
	return Command(v), nil
}
func (c Command) String() string {
	if s, ok := LookupCommand(c); ok {
		return s.Name
	}
	return fmt.Sprintf("COMMAND_%d", c)
}
func StatusName(status uint16) string {
	if s, ok := statusNames[status]; ok {
		return s
	}
	return fmt.Sprintf("UNKNOWN_0x%04X", status)
}

type Response struct {
	Command    Command  `json:"command"`
	Status     uint16   `json:"status"`
	StatusName string   `json:"status_name"`
	Data       HexBytes `json:"data"`
	NoResponse bool     `json:"no_response,omitempty"`
}

func (r Response) Check(statuses ...uint16) error {
	for _, s := range statuses {
		if r.Status == s && !r.NoResponse {
			return nil
		}
	}
	return &StatusError{uint16(r.Command), r.Status}
}

// CommandOptions controls explicit compatibility with firmware variants.
type CommandOptions struct {
	// AllowUnregistered permits commands in the pinned catalog that are not
	// registered in its firmware. Unknown command IDs are always rejected.
	AllowUnregistered bool
}

// Execute sends a command registered in the pinned firmware. It preserves status in the
// Response; transport failures alone are Go errors. Check the response status.
// Wire methods intentionally require pre-encoded bytes; typed methods are in
// settings.go, emulator.go and cards.go. No mode switching is done here.
func (c *Client) Execute(ctx context.Context, cmd Command, payload []byte) (Response, error) {
	return c.ExecuteWithOptions(ctx, cmd, payload, CommandOptions{})
}

// ExecuteWithOptions sends a catalog command with explicit compatibility options.
// It keeps Execute's serialization, timeout and send-only bootloader behavior.
func (c *Client) ExecuteWithOptions(ctx context.Context, cmd Command, payload []byte, options CommandOptions) (Response, error) {
	meta, ok := LookupCommand(cmd)
	if !ok || (!meta.Registered && !options.AllowUnregistered) {
		return Response{}, fmt.Errorf("%s is not registered in pinned firmware", cmd)
	}
	if len(payload) > MaxPayload {
		return Response{}, fmt.Errorf("payload exceeds %d bytes", MaxPayload)
	}
	if cmd == CmdEnterBootloader {
		if len(payload) != 0 {
			return Response{}, fmt.Errorf("bootloader command requires an empty payload")
		}
		e := c.EnterBootloader(ctx)
		return Response{Command: cmd, NoResponse: true}, e
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if e := c.acquire(ctx); e != nil {
		return Response{}, e
	}
	defer c.release()
	f, e := c.exchangeFrame(ctx, uint16(cmd), payload)
	if e != nil {
		return Response{}, e
	}
	return Response{cmd, f.status, StatusName(f.status), HexBytes(f.data), false}, nil
}
func (c *Client) request(ctx context.Context, cmd Command, data []byte, status uint16) ([]byte, error) {
	r, e := c.Execute(ctx, cmd, data)
	if e != nil {
		return nil, e
	}
	if e = r.Check(status); e != nil {
		return nil, e
	}
	return r.Data, nil
}

// EnterBootloader is send-only: the firmware disconnects without replying.
func (c *Client) EnterBootloader(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if e := c.acquire(ctx); e != nil {
		return e
	}
	defer c.release()
	c.state.Lock()
	select {
	case <-c.done:
		c.state.Unlock()
		return ErrClosed
	default:
	}
	finished := make(chan struct{})
	c.active = finished
	c.state.Unlock()
	result := make(chan error, 1)
	go func() {
		defer close(finished)
		select {
		case <-c.done:
			result <- ErrClosed
			return
		default:
		}
		b := encode(1010)
		c.requests.Add(1)
		for len(b) > 0 {
			n, e := c.port.Write(b)
			if e != nil {
				result <- e
				return
			}
			if n == 0 {
				result <- io.ErrShortWrite
				return
			}
			b = b[n:]
		}
		result <- nil
	}()
	var e error
	select {
	case e = <-result:
	case <-ctx.Done():
		e = ctx.Err()
	case <-c.done:
		e = ErrClosed
	}
	closeErr := c.Close()
	if e != nil {
		return e
	}
	return closeErr
}
