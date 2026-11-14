package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNewWorkflowWritesRequireConfirmation(t *testing.T) {
	for _, args := range [][]string{{"mfc", "clone"}, {"emv", "load"}, {"ulc", "ulcg-collect"}, {"ulc", "write"}, {"ulc", "set-key"}, {"ulc", "password-write"}, {"seos", "set"}, {"seos", "keys"}, {"rf", "config-set"}, {"mfc", "value-set"}, {"mfc", "value-inc"}} {
		args = append(args, "--port", "not-a-device")
		if e := runWorkflow(args); e == nil || !strings.Contains(e.Error(), "requires --yes") {
			t.Fatal(args, e)
		}
	}
}
func TestOfflineClassicValueDecodeCLI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "value.json")
	if e := runWorkflow([]string{"mfc", "value-decode", "--data", "01000000FEFFFFFF0100000004FB04FB", "--output", path}); e != nil {
		t.Fatal(e)
	}
}
func TestWorkflowHelpIsOffline(t *testing.T) {
	for _, group := range []string{"ulc", "rf", "seos", "emv", "des"} {
		if e := runWorkflow([]string{group, "--help"}); e != nil {
			t.Fatal(e)
		}
	}
	if e := runSamples([]string{"--help"}); e != nil {
		t.Fatal(e)
	}
}
