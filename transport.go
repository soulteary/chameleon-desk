package chameleon

import (
	"io"
	"net"
	"strings"
	"time"
)

// NewClient adapts an already configured duplex stream (for example a custom
// BLE bridge). Close must unblock Read and Write. The caller configures device
// framing transport properties such as DTR before construction. Client owns it.
// A TCP net.Conn has restrictive unknown-TCP properties. Other custom streams
// remain caller-managed; OpenContext handles built-in TCP bridge declarations.
func NewClient(stream io.ReadWriteCloser, timeout time.Duration) *Client {
	c := newClient(stream, timeout)
	if conn, ok := stream.(net.Conn); ok && conn.RemoteAddr() != nil && strings.HasPrefix(conn.RemoteAddr().Network(), "tcp") {
		c.transportInfo = TransportInfo{Connection: "tcp", Medium: "unknown"}
	}
	return c
}
