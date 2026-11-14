// Package dfu implements Nordic Secure Serial DFU framing and object transfer.
// It is independent of Chameleon's application protocol and never enters DFU.
package dfu

import (
	"bufio"
	"fmt"
	"io"
)

func EncodeSLIP(data []byte) []byte {
	out := make([]byte, 0, len(data)+1)
	for _, b := range data {
		switch b {
		case 0xc0:
			out = append(out, 0xdb, 0xdc)
		case 0xdb:
			out = append(out, 0xdb, 0xdd)
		default:
			out = append(out, b)
		}
	}
	return append(out, 0xc0)
}
func readSLIP(r *bufio.Reader) ([]byte, error) {
	out := []byte{}
	escape := false
	for {
		b, e := r.ReadByte()
		if e != nil {
			return nil, e
		}
		if escape {
			switch b {
			case 0xdc:
				b = 0xc0
			case 0xdd:
				b = 0xdb
			default:
				return nil, fmt.Errorf("invalid SLIP escape 0x%02x", b)
			}
			escape = false
		} else {
			if b == 0xc0 {
				if len(out) == 0 {
					continue
				}
				return out, nil
			}
			if b == 0xdb {
				escape = true
				continue
			}
		}
		out = append(out, b)
		if len(out) > 4096 {
			return nil, fmt.Errorf("SLIP response exceeds 4096 bytes")
		}
	}
}
func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, e := w.Write(b)
		if e != nil {
			return e
		}
		if n <= 0 || n > len(b) {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}
