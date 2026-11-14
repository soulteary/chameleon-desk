package main

import (
	"bytes"
	"encoding/json"
	"errors"
	chameleon "github.com/soulteary/chameleon-desk"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestForcedCatalogCallTransmitsAndPreservesStatus(t *testing.T) {
	for _, reader := range []bool{false, true} {
		for _, status := range []uint16{0x40, 0x67} {
			name := "direct"
			if reader {
				name = "reader"
			}
			t.Run(name+"/"+chameleon.StatusName(status), func(t *testing.T) {
				var mode, calls atomic.Uint32
				port := workflowSimulator(t, func(cmd uint16, payload []byte) (uint16, []byte) {
					switch cmd {
					case 1002:
						if !reader {
							t.Error("direct forced call unexpectedly read the device mode")
						}
						return 0x68, []byte{byte(mode.Load())}
					case 1001:
						mode.Store(uint32(payload[0]))
						return 0x68, nil
					case 3007:
						calls.Add(1)
						if !bytes.Equal(payload, []byte{0xaa, 0x55}) {
							t.Errorf("forced payload changed: %X", payload)
						}
						return status, []byte{0x10, 0x20}
					default:
						t.Errorf("unexpected forced-call command %d; capabilities must not be queried", cmd)
						return 0x67, nil
					}
				})
				output := filepath.Join(t.TempDir(), "response.json")
				args := []string{"call", "GENERIC_READ", "--force", "--yes", "--data", "AA55", "--port", port, "--output", output}
				if reader {
					args = append(args, "--reader")
				}
				err := runExtended(args)
				if status == 0x40 && err != nil {
					t.Fatal(err)
				}
				if status == 0x67 {
					var failure *chameleon.StatusError
					if !errors.As(err, &failure) || failure.Command != 3007 || failure.Status != status {
						t.Fatalf("firmware failure was not preserved: %v", err)
					}
				}
				raw, err := os.ReadFile(output)
				if err != nil {
					t.Fatal(err)
				}
				var response chameleon.Response
				if err := json.Unmarshal(raw, &response); err != nil || response.Command != chameleon.CmdGenericRead || response.Status != status || response.Data.String() != "1020" {
					t.Fatalf("forced-call response changed: %s %v", raw, err)
				}
				if calls.Load() != 1 || mode.Load() != 0 {
					t.Fatalf("raw calls %d, final mode %d", calls.Load(), mode.Load())
				}
			})
		}
	}
}

func TestForcedCallGuardsRunBeforeOpening(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		args       []string
	}{
		{"without-force", "no handler in the pinned firmware", []string{"call", "GENERIC_READ", "--yes"}},
		{"reader-without-force", "no handler in the pinned firmware", []string{"call", "GENERIC_READ", "--yes", "--reader"}},
		{"without-intent", "--yes", []string{"call", "GENERIC_READ", "--force"}},
		{"reader-without-intent", "--yes", []string{"call", "GENERIC_READ", "--force", "--reader"}},
		{"unknown-command", "catalog", []string{"call", "65535", "--force", "--yes"}},
		{"reader-bootloader", "bootloader cannot run inside reader session", []string{"call", "ENTER_BOOTLOADER", "--force", "--yes", "--reader"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runExtendedWith(append(tc.args, "--port", "mock"), noOpen(t))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("forced-call guard failed: %v", err)
			}
		})
	}
}
