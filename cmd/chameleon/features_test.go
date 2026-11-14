package main

import (
	"errors"
	"flag"
	"os"
	"strings"
	"testing"

	chameleon "github.com/soulteary/chameleon-desk"
)

func TestFeatureCLIEntriesReachHelpWithoutDeviceAccess(t *testing.T) {
	oldArgs, oldOut, oldErr := os.Args, os.Stdout, os.Stderr
	sink, e := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { os.Args = oldArgs; os.Stdout = oldOut; os.Stderr = oldErr; sink.Close() }()
	os.Stdout = sink
	os.Stderr = sink
	seen := map[string]bool{}
	for _, f := range chameleon.Features() {
		for _, entry := range f.CLI.Entries {
			if seen[entry] {
				continue
			}
			seen[entry] = true
			os.Args = append([]string{"chameleon"}, strings.Fields(entry)...)
			os.Args = append(os.Args, "--help")
			if err := run(); err != nil && !errors.Is(err, flag.ErrHelp) {
				t.Errorf("%s: stale CLI entry %q: %v", f.ID, entry, err)
			}
		}
	}
}
func TestFeatureCLIListingValidatesInputsWithoutAutoSelectingHardware(t *testing.T) {
	if e := runFeatures([]string{"--surface", "unknown"}); e == nil {
		t.Fatal("unknown surface accepted")
	}
	if e := runFeatures([]string{"--id", "not-a-feature", "--port", "tcp:127.0.0.1:1"}); e == nil || !strings.Contains(e.Error(), "unknown feature") {
		t.Fatal("unknown feature accepted")
	}
	if e := runFeatures([]string{"--timeout", "0s"}); e == nil {
		t.Fatal("invalid timeout accepted")
	}
}
