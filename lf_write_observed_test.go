package chameleon

import (
	"context"
	"testing"
)

func TestLFCloneObservedReportsOnlyTheWriteCommandBoundary(t *testing.T) {
	input := LFIdentityInput{Protocol: "em410x", ID: HexBytes{1, 2, 3, 4, 5}}
	payload := []byte{1, 2, 3, 4, 5, 1, 2, 3, 4, 0, 0, 0, 0}
	for _, tc := range []struct {
		name    string
		steps   []readerStep
		write   bool
		success bool
	}{
		{"lite", []readerStep{{command: 1033, status: 0x68, data: []byte{1}}}, false, false},
		{"model-failure", []readerStep{{command: 1033, status: 0x67}}, false, false},
		{"mode-failure", []readerStep{{command: 1033, status: 0x68, data: []byte{0}}, {command: 1002, status: 0x67}}, false, false},
		{"mode-change-failure", []readerStep{{command: 1033, status: 0x68, data: []byte{0}}, {command: 1002, status: 0x68, data: []byte{0}}, {command: 1001, request: []byte{1}, status: 0x67}, {command: 1001, request: []byte{0}, status: 0x68}}, false, false},
		{"write-failure", []readerStep{{command: 1033, status: 0x68, data: []byte{0}}, {command: 1002, status: 0x68, data: []byte{1}}, {command: 3001, request: payload, status: 0x41}}, true, false},
		{"write-success", []readerStep{{command: 1033, status: 0x68, data: []byte{0}}, {command: 1002, status: 0x68, data: []byte{1}}, {command: 3001, request: payload, status: 0x40}}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := readerSimulator(t, tc.steps)
			calls := 0
			_, err := c.CloneLFObserved(context.Background(), input, []byte{1, 2, 3, 4}, [][]byte{{0, 0, 0, 0}}, func() {
				calls++
				if c.Stats().Requests != 2 {
					t.Errorf("callback did not follow model/mode preflight: %+v", c.Stats())
				}
			})
			if (err == nil) != tc.success || (calls == 1) != tc.write || calls > 1 {
				t.Fatal(calls, err)
			}
		})
	}
}
func TestLFCloneObservedInvalidCodecAndPasswordNeverReportAWrite(t *testing.T) {
	c := readerSimulator(t, nil)
	calls := 0
	mark := func() { calls++ }
	if _, err := c.CloneLFObserved(context.Background(), LFIdentityInput{Protocol: "pac", PAC: "invalid"}, []byte{1, 2, 3, 4}, [][]byte{{0, 0, 0, 0}}, mark); err == nil {
		t.Fatal("invalid codec accepted")
	}
	if _, err := c.CloneLFObserved(context.Background(), LFIdentityInput{Protocol: "pac", PAC: "12345678"}, nil, [][]byte{{0, 0, 0, 0}}, mark); err == nil {
		t.Fatal("missing password accepted")
	}
	if calls != 0 || c.Stats().Requests != 0 {
		t.Fatal(calls, c.Stats())
	}
}
