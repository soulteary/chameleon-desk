package main

import (
	"encoding/binary"
	chameleon "github.com/soulteary/chameleon-desk"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDarksideCollectionDoesNotRequireKnownKey(t *testing.T) {
	var mode, acquisitions atomic.Uint32
	port := workflowSimulator(t, func(cmd uint16, payload []byte) (uint16, []byte) {
		switch cmd {
		case 1002:
			return 0x68, []byte{byte(mode.Load())}
		case 1001:
			mode.Store(uint32(payload[0]))
			return 0x68, nil
		case 2000:
			return 0, []byte{4, 1, 2, 3, 4, 4, 0, 8, 0}
		case 2004:
			if len(payload) != 4 || payload[0] != byte(chameleon.KeyB) || payload[1] != 4 || payload[2] != 1 || payload[3] != 3 {
				t.Errorf("unexpected Darkside acquisition request %X", payload)
			}
			acquisitions.Add(1)
			sample := make([]byte, 33)
			binary.BigEndian.PutUint32(sample[1:5], 0x01020304)
			return 0, sample
		default:
			t.Errorf("unexpected simulated command %d", cmd)
			return 0x67, nil
		}
	})
	output := filepath.Join(t.TempDir(), "darkside.json")
	if err := runWorkflow([]string{"mfc", "recover-collect", "--kind", "darkside", "--dst-block", "4", "--dst-key-type", "B", "--max-runs", "1", "--port", port, "--output", output}); err != nil {
		t.Fatal(err)
	}
	bundle, err := chameleon.ReadRecoveryBundle(output)
	if err != nil || bundle.Kind != "darkside" || bundle.Tool != "darkside" || bundle.UID.String() != "01020304" || bundle.TargetBlock != 4 || bundle.TargetType != chameleon.KeyB || len(bundle.Samples) != 1 || len(bundle.Arguments) != 6 {
		t.Fatalf("invalid Darkside acquisition bundle: %+v %v", bundle, err)
	}
	if acquisitions.Load() != 1 || mode.Load() != 0 {
		t.Fatalf("acquisitions %d, final mode %d", acquisitions.Load(), mode.Load())
	}
}

func TestKeyedRecoveryCollectionRejectsInvalidKeysBeforeOpening(t *testing.T) {
	for _, kind := range []string{"nested", "hardnested"} {
		for _, key := range []string{"", "00", "00112233445566", "not-hex"} {
			t.Run(kind+"/"+key, func(t *testing.T) {
				args := []string{"mfc", "recover-collect", "--kind", kind, "--port", "tcp:127.0.0.1:0", "--output", filepath.Join(t.TempDir(), "capture.json")}
				if key != "" {
					args = append(args, "--key", key)
				}
				err := runWorkflow(args)
				want := "requires --key (six-byte known key)"
				if key == "not-hex" {
					want = "hex"
				}
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("invalid known key reached transport setup: %v", err)
				}
			})
		}
	}
}

func TestRecoveryCollectionRejectsInvalidTargetKeyTypeBeforeOpening(t *testing.T) {
	for _, kind := range []string{"nested", "hardnested", "darkside"} {
		err := runWorkflow([]string{"mfc", "recover-collect", "--kind", kind, "--key", "001122334455", "--dst-key-type", "C", "--port", "tcp:127.0.0.1:0", "--output", filepath.Join(t.TempDir(), "capture.json")})
		if err == nil || !strings.Contains(err.Error(), "dst-key-type must be A/B") {
			t.Fatalf("%s accepted invalid target key type: %v", kind, err)
		}
	}
}

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
