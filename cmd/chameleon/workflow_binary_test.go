package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	chameleon "github.com/soulteary/chameleon-desk"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func binaryCloneInputs(t *testing.T) (string, string, chameleon.ClassicCardDump) {
	t.Helper()
	dir := t.TempDir()
	data := make(chameleon.HexBytes, 320)
	keys := chameleon.ClassicKeyMap{Schema: 1, UID: chameleon.HexBytes{1, 2, 3, 4}, Complete: true}
	for sector := 0; sector < 5; sector++ {
		key := chameleon.HexBytes{255, 255, 255, 255, 255, 255}
		keys.Sectors = append(keys.Sectors, chameleon.SectorKeys{Sector: sector, A: key, B: key})
		trailer := data[(sector*4+3)*16 : (sector*4+4)*16]
		copy(trailer[:6], key)
		copy(trailer[6:9], []byte{255, 7, 128})
		copy(trailer[10:], key)
	}
	digest := sha256.Sum256(data)
	dump := chameleon.ClassicCardDump{Schema: 2, UID: keys.UID, TagType: 1000, Data: data, SHA256: hex.EncodeToString(digest[:])}
	if err := dump.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := keys.Validate(); err != nil {
		t.Fatal(err)
	}
	input, keyPath := filepath.Join(dir, "dump.json"), filepath.Join(dir, "keys.json")
	writeJSONFile(t, input, dump)
	writeJSONFile(t, keyPath, keys)
	return input, keyPath, dump
}

func TestWorkflowBinaryRejectsStructuredAndMutatingActionsBeforeOpening(t *testing.T) {
	input, keys, _ := binaryCloneInputs(t)
	for group, actions := range map[string][]string{
		"mfc":   {"clone", "auth-log", "recover-log", "dictionary", "autopwn", "value-get", "value-set", "value-inc", "value-dec", "value-copy", "value-decode", "recover-collect", "recover-compute", "recover-verify"},
		"ulc":   {"auth", "write", "set-key", "password-read", "password-write", "ulcg-collect", "ulcg-recover"},
		"type2": {"password-read", "password-write"},
		"rf":    {"info", "config", "config-set", "auth-trace"},
		"seos":  {"get", "set", "keys"},
		"emv":   {"debug", "scan", "load", "tlv"},
		"des":   {"info", "auth", "check"},
	} {
		for _, action := range actions {
			t.Run(group+"/"+action, func(t *testing.T) {
				output := filepath.Join(t.TempDir(), "output")
				err := runWorkflow([]string{group, action, "--binary", "--yes", "--input", input, "--keys-file", keys, "--key", "FFFFFFFFFFFF", "--port", "tcp:127.0.0.1:0", "--output", output})
				if err == nil || !strings.Contains(err.Error(), "--binary requires a byte-returning") {
					t.Fatalf("unsupported binary output reached operation setup: %v", err)
				}
				if _, err := os.Stat(output); !os.IsNotExist(err) {
					t.Fatalf("invalid binary request created an output: %v", err)
				}
			})
		}
	}
}

func TestWorkflowBinaryPreservesByteReturningActions(t *testing.T) {
	_, keys, dump := binaryCloneInputs(t)
	cases := []struct {
		group, action string
		length        int
	}{
		{"mfc", "dump-keys", 320},
		{"ulc", "read", 4},
		{"ulc", "version", 8}, {"ulc", "signature", 32}, {"ulc", "counter", 3}, {"ulc", "authnonce", 9},
		{"type2", "version", 8}, {"type2", "signature", 32}, {"type2", "counter", 3}, {"type2", "authnonce", 9},
		{"rf", "lf-samples", 4}, {"rf", "adc", 4},
	}
	for _, tc := range cases {
		t.Run(tc.group+"/"+tc.action, func(t *testing.T) {
			want := bytes.Repeat([]byte{0x5a}, tc.length)
			if tc.group == "mfc" {
				want = dump.Data
			}
			port := workflowSimulator(t, func(cmd uint16, payload []byte) (uint16, []byte) {
				switch cmd {
				case 1002:
					return 0x68, []byte{1}
				case 2000:
					return 0, []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}
				case 2007:
					return 0, nil
				case 2008:
					block := int(payload[1])
					return 0, dump.Data[block*16 : (block+1)*16]
				case 2019, 2010:
					return 0, want
				case 2101:
					return 0x68, nil
				case 3009, 3031:
					return 0x40, want
				default:
					t.Errorf("unexpected simulated command %d", cmd)
					return 0x67, nil
				}
			})
			output := filepath.Join(t.TempDir(), "data.bin")
			if err := runWorkflow([]string{tc.group, tc.action, "--binary", "--key", "00112233445566778899AABBCCDDEEFF", "--keys-file", keys, "--duration", "1ms", "--port", port, "--output", output}); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(output)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("binary output differs: %X %v", got, err)
			}
		})
	}
}
