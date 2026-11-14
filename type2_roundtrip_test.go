package chameleon

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

// Replays the ordinary-page write/readback sequence exercised on an NTAG213,
// including NAK, short replies and failed mode restoration. No real I/O.
func TestType2WriteFailureDoesNotBlockFollowingRead(t *testing.T) {
	for _, tc := range []struct {
		name            string
		ack             []byte
		status, restore uint16
		ok              bool
	}{
		{"ack", []byte{0x0a}, 0, 0x68, true},
		{"nak", []byte{0}, 0, 0x68, false},
		{"empty", nil, 0, 0x68, false},
		{"oversized", []byte{0x0a, 0x0a}, 0, 0x68, false},
		{"transport-status", nil, 2, 0x68, false},
		{"restore-failure", []byte{0x0a}, 0, 0x66, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := []byte{0x55, 0xaa, 0, 0xff}
			read := append(make([]byte, 12), original...)
			c := readerSimulator(t, []readerStep{
				{command: 1002, status: 0x68, data: []byte{0}},
				{command: 1001, status: 0x68, request: []byte{1}},
				{command: 2010, status: tc.status, request: []byte{0xf0, 1, 0xf4, 0, 48, 0xa2, 39, 0x55, 0xaa, 0, 0xff}, data: tc.ack},
				{command: 1001, status: tc.restore, request: []byte{0}},
				{command: 1002, status: 0x68, data: []byte{0}},
				{command: 1001, status: 0x68, request: []byte{1}},
				{command: 2010, request: []byte{0xf4, 1, 0xf4, 0, 16, 0x30, 36}, data: read},
				{command: 1001, status: 0x68, request: []byte{0}},
			})
			err := c.WriteType2Page(context.Background(), 39, original)
			if (err == nil) != tc.ok {
				t.Fatalf("write error=%v, want success=%v", err, tc.ok)
			}
			if tc.restore != 0x68 {
				var restore *ModeRestoreError
				if !errors.As(err, &restore) {
					t.Fatalf("restore failure hidden: %v", err)
				}
			}
			got, err := c.ReadType2Pages(context.Background(), 39, 1)
			if err != nil || !bytes.Equal(got, original) {
				t.Fatalf("following read %x: %v", got, err)
			}
		})
	}
}
