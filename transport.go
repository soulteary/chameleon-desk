package chameleon

import (
	"io"
	"time"
)

// NewClient adapts an already configured duplex stream (for example a custom
// BLE bridge). Close must unblock Read and Write. The caller configures device
// framing transport properties such as DTR before construction. Client owns it.
func NewClient(stream io.ReadWriteCloser, timeout time.Duration) *Client {
	return newClient(stream, timeout)
}
