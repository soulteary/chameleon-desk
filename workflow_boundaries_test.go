package chameleon

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestExecuteBootloaderRejectsPayloadWithoutClosingConnection(t *testing.T) {
	for _, size := range []int{1, MaxPayload + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			c := readerSimulator(t, []readerStep{{command: 1000, status: 0x68, data: []byte{2, 1}}})
			if _, err := c.ExecuteWithOptions(context.Background(), CmdEnterBootloader, make([]byte, size), CommandOptions{AllowUnregistered: true}); err == nil || !strings.Contains(err.Error(), "payload") {
				t.Fatalf("invalid bootloader payload accepted: %v", err)
			}
			if c.Stats().Requests != 0 {
				t.Fatal("invalid request reached transport")
			}
			if _, err := c.Firmware(context.Background()); err != nil {
				t.Fatal("validation invalidated usable connection:", err)
			}
		})
	}
}

func TestRestoreRejectsMalformedLiveStateBeforeWrites(t *testing.T) {
	source := backupSimulator(t, newBackupDevice())
	snapshot, err := source.BackupDevice(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var caps []byte
	for _, command := range Commands() {
		if command.Registered {
			caps = append(caps, byte(command.ID>>8), byte(command.ID))
		}
	}
	cases := []struct {
		name         string
		mode, active []byte
	}{
		{"empty-mode", nil, nil}, {"invalid-mode", []byte{2}, nil}, {"long-mode", []byte{0, 1}, nil},
		{"empty-slot", []byte{1}, nil}, {"invalid-slot", []byte{1}, []byte{8}}, {"long-slot", []byte{1}, []byte{0, 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			steps := []readerStep{{command: 1033, status: 0x68, data: []byte{0}}, {command: 1035, status: 0x68, data: caps}, {command: 1034, status: 0x68, data: snapshot.Settings}, {command: 1002, status: 0x68, data: tc.mode}}
			if len(tc.mode) == 1 && tc.mode[0] <= 1 {
				steps = append(steps, readerStep{command: 1018, status: 0x68, data: tc.active})
			}
			reads := len(steps)
			steps = append(steps, readerStep{command: 1000, status: 0x68, data: []byte{2, 1}})
			c := readerSimulator(t, steps)
			out, err := c.RestoreDevice(context.Background(), snapshot, RestoreOptions{Persist: true})
			if err == nil || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("live state not rejected: %+v %v", out, err)
			}
			if c.Stats().Requests != uint64(reads) || out.CompletedSlots != 0 || out.Verified || out.Persisted {
				t.Fatalf("restore started before live state validation: %+v %+v", out, c.Stats())
			}
			if _, err := c.Firmware(context.Background()); err != nil {
				t.Fatal("connection did not recover:", err)
			}
		})
	}
}
