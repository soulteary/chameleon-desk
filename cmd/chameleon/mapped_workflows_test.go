package main

import (
	"encoding/binary"
	"encoding/json"
	chameleon "github.com/soulteary/chameleon-desk"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestDetectionWorkflowUsesRequestedFamilyAndRestoresSlot(t *testing.T) {
	for _, family := range []string{"mfc", "type2", "emulator"} {
		t.Run(family, func(t *testing.T) {
			var mu sync.Mutex
			active := byte(0)
			var commands []uint16
			port := workflowSimulator(t, func(cmd uint16, payload []byte) (uint16, []byte) {
				mu.Lock()
				defer mu.Unlock()
				commands = append(commands, cmd)
				switch cmd {
				case 1002:
					return 0x68, []byte{0}
				case 1018:
					return 0x68, []byte{active}
				case 1003:
					active = payload[0]
					return 0x68, nil
				case 1019:
					types := make([]byte, 32)
					tag := uint16(1100)
					if family == "mfc" {
						tag = 1001
					}
					binary.BigEndian.PutUint16(types[int(active)*4:], tag)
					return 0x68, types
				case 4005, 4034:
					return 0x68, []byte{0, 0, 0, 3}
				case 4030:
					return 0x68, []byte{45}
				case 4006, 4035:
					if len(payload) != 4 || binary.BigEndian.Uint32(payload) != 1 {
						t.Errorf("requested log offset %X", payload)
					}
					if cmd == 4006 {
						return 0x68, []byte{4, 1, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
					}
					return 0x68, []byte{1, 2, 3, 4}
				default:
					t.Errorf("unexpected simulated command %d", cmd)
					return 0x67, nil
				}
			})
			action := "edetect"
			if family == "emulator" {
				action = "password-log"
			}
			output := filepath.Join(t.TempDir(), "logs.json")
			if err := runMappedWorkflow([]string{family, action, "--port", port, "--slot", "2", "--yes", "--start", "1", "--limit", "1", "--output", output}); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if active != 0 {
				t.Fatal("original slot not restored", active)
			}
			for _, cmd := range commands {
				if family == "mfc" && (cmd == 4034 || cmd == 4035) || family != "mfc" && (cmd == 4005 || cmd == 4006) {
					t.Fatalf("wrong detection command family: %d", cmd)
				}
			}
			raw, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if family == "mfc" {
				var logs []chameleon.AuthLog
				if err := json.Unmarshal(raw, &logs); err != nil || len(logs) != 1 || logs[0].KeyType != "B" || logs[0].Block != 4 {
					t.Fatalf("invalid Classic detection output %s: %v", raw, err)
				}
			} else {
				var logs []chameleon.HexBytes
				if err := json.Unmarshal(raw, &logs); err != nil || len(logs) != 1 || logs[0].String() != "01020304" {
					t.Fatalf("invalid Type2 detection output %s: %v", raw, err)
				}
			}
		})
	}
}

func TestMappedMutationsRequireYesBeforeDeviceOpen(t *testing.T) {
	for _, args := range [][]string{{"mfc", "eload"}, {"type2", "eload"}, {"emulator", "configure"}, {"slot", "openall"}, {"slot", "nickname-delete"}, {"slot", "nickname-set"}, {"settings", "configure"}, {"lf", "clone"}} {
		args = append(args, "--port", "not-a-device")
		if e := runMappedWorkflow(args); e == nil || !strings.Contains(e.Error(), "requires --yes") {
			t.Fatal(args, e)
		}
	}
}
func TestMappedOfflinePlanAndFormats(t *testing.T) {
	dir := t.TempDir()
	if e := runMappedWorkflow([]string{"slot", "openall", "--plan", "--output", filepath.Join(dir, "plan.json")}); e != nil {
		t.Fatal(e)
	}
	if e := runMappedWorkflow([]string{"lf", "formats", "--output", filepath.Join(dir, "formats.json")}); e != nil {
		t.Fatal(e)
	}
}
func TestMappedPatchUnknownFieldRejectedBeforeDeviceOpen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "patch.json")
	if e := os.WriteFile(p, []byte(`{"gen22":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	e := runMappedWorkflow([]string{"emulator", "configure", "--yes", "--input", p, "--port", "not-a-device"})
	if e == nil || !strings.Contains(e.Error(), "unknown field") {
		t.Fatal(e)
	}
}
func TestMappedNicknameReadDoesNotRequireMutationConfirmation(t *testing.T) {
	e := runMappedWorkflow([]string{"slot", "nickname-get", "--slot", "1", "--port", "not-a-device"})
	if e == nil || strings.Contains(e.Error(), "requires --yes") {
		t.Fatal(e)
	}
}

func TestWorkflowCLIRoutes(t *testing.T) {
	workflows := map[string][]string{
		"mfc":      {"eload", "esave", "eview", "econfig", "edetect"},
		"type2":    {"eload", "esave", "eview", "econfig", "edetect"},
		"slot":     {"list", "prng", "openall", "nickname-get", "nickname-set", "nickname-delete"},
		"settings": {"view", "configure"},
		"emulator": {"config", "configure", "counter", "reset-auth", "password-log"},
		"lf":       {"read", "clone", "econfig", "encode", "decode", "formats"},
	}
	for group, actions := range workflows {
		for _, action := range actions {
			t.Run(group+"/"+action, func(t *testing.T) {
				if !isMappedWorkflow([]string{group, action}) {
					t.Fatalf("unreachable workflow %s %s", group, action)
				}
			})
		}
	}
	for _, route := range []string{
		"slot select", "slot type", "slot delete", "slot init", "slot enable", "slot save",
		"settings set", "settings clear-bonds", "settings save", "settings reset", "settings factory-reset",
	} {
		t.Run(route, func(t *testing.T) {
			args := strings.Fields(route)
			if isMappedWorkflow(args) || !extendedCommand(args[0]) {
				t.Fatalf("native command routed incorrectly: %s", route)
			}
		})
	}
	for _, args := range [][]string{nil, {"slot"}, {"slot", "unknown"}, {"settings", "unknown"}, {"unknown", "read"}} {
		if isMappedWorkflow(args) {
			t.Fatalf("unexpected workflow %v", args)
		}
	}
}

func TestLFEncodeOfflineWithoutPort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.json")
	if e := os.WriteFile(path, []byte(`{"protocol":"em410x","id":"0102030405"}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := runMappedWorkflow([]string{"lf", "encode", "--input", path, "--port", "not-a-device", "--output", filepath.Join(dir, "encoded.json")}); e != nil {
		t.Fatal(e)
	}
}
