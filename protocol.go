package chameleon

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// MaxPayload is the receive ceiling used by the upstream Python client.
const MaxPayload = 4096

type frame struct {
	command, status uint16
	data            []byte
}

func lrc(b []byte) byte {
	var sum byte
	for _, v := range b {
		sum += v
	}
	return -sum
}

func encode(command uint16, data ...byte) []byte {
	b := make([]byte, 10+len(data))
	b[0], b[1] = 0x11, 0xef
	binary.BigEndian.PutUint16(b[2:4], command)
	binary.BigEndian.PutUint16(b[6:8], uint16(len(data)))
	b[8] = lrc(b[:8])
	copy(b[9:], data)
	b[len(b)-1] = lrc(data)
	return b
}

// decoder accepts arbitrary serial chunks and resynchronizes after corruption.
type decoder struct {
	buf      []byte
	rejected uint64
}

func (d *decoder) next() (frame, bool) {
	for {
		i := bytes.Index(d.buf, []byte{0x11, 0xef})
		if i < 0 {
			if len(d.buf) > 0 && d.buf[len(d.buf)-1] == 0x11 {
				d.buf = []byte{0x11}
			} else {
				d.buf = nil
			}
			return frame{}, false
		}
		d.buf = d.buf[i:]
		if len(d.buf) < 9 {
			return frame{}, false
		}
		n := int(binary.BigEndian.Uint16(d.buf[6:8]))
		if lrc(d.buf[:9]) != 0 || n > MaxPayload {
			d.rejected++
			d.buf = d.buf[1:]
			continue
		}
		if len(d.buf) < n+10 {
			return frame{}, false
		}
		if lrc(d.buf[:n+10]) != 0 {
			d.rejected++
			d.buf = d.buf[1:]
			continue
		}
		f := frame{binary.BigEndian.Uint16(d.buf[2:4]), binary.BigEndian.Uint16(d.buf[4:6]), append([]byte(nil), d.buf[9:9+n]...)}
		d.buf = d.buf[n+10:]
		return f, true
	}
}

// StatusError is a valid firmware response reporting a non-success status.
type StatusError struct{ Command, Status uint16 }

func (e *StatusError) Error() string {
	return fmt.Sprintf("command %d (%s): firmware status 0x%04x (%s)", e.Command, Command(e.Command), e.Status, StatusName(e.Status))
}
