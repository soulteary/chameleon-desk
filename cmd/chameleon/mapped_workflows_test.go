package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
