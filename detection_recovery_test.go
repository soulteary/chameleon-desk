package chameleon

import (
	"context"
	"testing"
	"time"
)

func TestDetectionLogNoProgressFails(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 4005, status: 0x68, data: []byte{0, 0, 0, 1}}, {command: 4006, request: []byte{0, 0, 0, 0}, status: 0x68}})
	if _, e := c.DetectionLogs(context.Background(), 10); e == nil {
		t.Fatal("zero progress accepted")
	}
}
func TestDetectionLogCountBound(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 4005, status: 0x68, data: []byte{0, 0, 1, 0}}})
	if _, e := c.DetectionLogs(context.Background(), 10); e == nil {
		t.Fatal("unbounded logs accepted")
	}
}
func TestDetectionRecoveryRejectsMalformed(t *testing.T) {
	if _, e := RecoverDetectionLogs(context.Background(), []AuthLog{{UID: []byte{1}}}, "tools/bin", time.Second, 10); e == nil {
		t.Fatal("malformed record accepted")
	}
}
