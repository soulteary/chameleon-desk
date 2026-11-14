package chameleon

import (
	"context"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

func classicLogSlotSteps(tag uint16) []readerStep {
	types := make([]byte, 32)
	binary.BigEndian.PutUint16(types[4:], tag)
	steps := []readerStep{
		{command: 1002, status: 0x68, data: []byte{0}},
		{command: 1018, status: 0x68, data: []byte{0}},
		{command: 1003, request: []byte{1}, status: 0x68},
		{command: 1018, status: 0x68, data: []byte{1}},
		{command: 1019, status: 0x68, data: types},
	}
	if tag == 1103 {
		steps = append(steps, readerStep{command: 4030, status: 0x68, data: []byte{16}})
	}
	return steps
}

func TestClassicDetectionLogsSelectsSlotAndPagesFromStart(t *testing.T) {
	record := func(block, flags byte) []byte {
		b := make([]byte, 18)
		b[0], b[1] = block, flags
		copy(b[2:6], []byte{1, 2, 3, 4})
		return b
	}
	steps := classicLogSlotSteps(1001)
	steps = append(steps,
		readerStep{command: 4005, status: 0x68, data: []byte{0, 0, 0, 5}},
		readerStep{command: 4006, request: []byte{0, 0, 0, 2}, status: 0x68, data: record(2, 0)},
		readerStep{command: 4006, request: []byte{0, 0, 0, 3}, status: 0x68, data: append(record(3, 3), record(4, 0)...)},
		readerStep{command: 1003, request: []byte{0}, status: 0x68})
	c := readerSimulator(t, steps)
	logs, e := c.ClassicDetectionLogs(context.Background(), 2, 2, 2)
	if e != nil || len(logs) != 2 || logs[0].Block != 2 || logs[1].Block != 3 || logs[1].KeyType != "B" || !logs[1].Nested || logs[0].UID.String() != "01020304" {
		t.Fatalf("unexpected selected logs: %+v %v", logs, e)
	}
}

func TestClassicDetectionLogsRestoresSlotAfterRejection(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tag   uint16
		start uint32
		steps []readerStep
		want  string
	}{
		{"wrong type", 1103, 0, nil, "Classic slot"},
		{"invalid count", 1001, 0, []readerStep{{command: 4005, status: 0x68, data: []byte{1}}}, "invalid detection count"},
		{"start beyond snapshot", 1001, 2, []readerStep{{command: 4005, status: 0x68, data: []byte{0, 0, 0, 1}}}, "start exceeds"},
		{"no progress", 1001, 0, []readerStep{{command: 4005, status: 0x68, data: []byte{0, 0, 0, 1}}, {command: 4006, request: []byte{0, 0, 0, 0}, status: 0x68}}, "no forward progress"},
		{"short record", 1001, 0, []readerStep{{command: 4005, status: 0x68, data: []byte{0, 0, 0, 1}}, {command: 4006, request: []byte{0, 0, 0, 0}, status: 0x68, data: make([]byte, 17)}}, "truncated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := append(classicLogSlotSteps(tc.tag), tc.steps...)
			steps = append(steps, readerStep{command: 1003, request: []byte{0}, status: 0x68})
			c := readerSimulator(t, steps)
			if _, e := c.ClassicDetectionLogs(context.Background(), 2, tc.start, 2); e == nil || !strings.Contains(e.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, e)
			}
		})
	}
}

func TestClassicDetectionLogsRejectsInvalidBoundsWithoutIO(t *testing.T) {
	c := readerSimulator(t, nil)
	for _, v := range []struct{ slot, limit int }{{9, 1}, {-1, 1}, {1, 0}, {1, 100001}} {
		if _, e := c.ClassicDetectionLogs(context.Background(), v.slot, 0, v.limit); e == nil {
			t.Fatal("invalid bounds accepted", v)
		}
	}
}

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
