package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

func TestAdvancedReaderRejectsInvalidRequestsBeforeTransport(t *testing.T) {
	original := openReaderWorkflowClient
	defer func() { openReaderWorkflowClient = original }()
	opens := 0
	openReaderWorkflowClient = func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		opens++
		return nil, errors.New("unexpected transport")
	}
	dir := t.TempDir()
	identity := *webPromotionResult().Identity
	for i, tc := range []struct {
		action string
		input  any
		flags  []string
	}{
		{"classic-repair", map[string]any{}, nil},
		{"classic-verify", chameleon.ClassicKeyVerificationRequest{Identity: identity, Keys: []chameleon.ClassicKeyCandidate{{Sector: 0, Type: chameleon.KeyA, Key: chameleon.HexBytes{1}}}}, nil},
		{"classic-dictionary", chameleon.ClassicDictionaryRequest{Identity: identity, Sectors: 5, Keys: []chameleon.HexBytes{{1, 2, 3, 4, 5, 6}}, LimitSeconds: 31}, nil},
		{"classic-dictionary", chameleon.ClassicDictionaryRequest{Identity: identity, Sectors: 40, Keys: []chameleon.HexBytes{{1, 2, 3, 4, 5, 6}}, LimitSeconds: 5}, nil},
		{"classic-recover", chameleon.ClassicRecoveryRequest{Identity: identity, Strategy: "darkside", TargetBlock: 4, TargetType: chameleon.KeyA, LimitSeconds: 5, MaxRuns: 1, MaxAttempts: 1}, []string{"--recovery-tools-dir", dir}},
		{"classic-verify", map[string]any{"identity": identity, "keys": []any{}, "recovery_tools_dir": "untrusted"}, nil},
	} {
		input := filepath.Join(dir, tc.action+string(rune('a'+i))+".json")
		output := input + ".out"
		writeReaderFixture(t, input, tc.input)
		args := append([]string{"reader", tc.action, "--port", "tcp:127.0.0.1:1", "--input", input, "--output", output}, tc.flags...)
		if err := runReaderWorkflowCLI(args); err == nil {
			t.Fatal(tc.action, "invalid request accepted")
		}
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Fatal("invalid operation published result", err)
		}
	}
	if opens != 0 {
		t.Fatal("invalid input contacted transport", opens)
	}
}
func TestAdvancedReaderRepairSelectsWholeReadOrSparseEvidence(t *testing.T) {
	source, err := chameleon.NewGUICardFromRead("source", webPromotionResult(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, blocks := range [][]int{nil, {1}} {
		raw, _ := json.Marshal(chameleon.ClassicRepairRequest{Source: source, Read: webClassicInput(), Blocks: blocks})
		op, err := prepareAdvancedReader("classic-repair", raw, time.Second, "")
		if err != nil || op.readResult != (len(blocks) == 0) || op.request.ClassicRepair == nil || op.feature != "reader.classic-repair" {
			t.Fatal(op, err)
		}
	}
}
func TestAdvancedReaderVerificationSavesExactOnceAndPartialResults(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "partial"}[partial], func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "request.json")
			output := filepath.Join(dir, "result.json")
			identity := *webPromotionResult().Identity
			request := chameleon.ClassicKeyVerificationRequest{Identity: identity, Keys: []chameleon.ClassicKeyCandidate{{Sector: 0, Type: chameleon.KeyA, Key: chameleon.HexBytes{1, 2, 3, 4, 5, 6}}}}
			if partial {
				request.Keys = append(request.Keys, chameleon.ClassicKeyCandidate{Sector: 1, Type: chameleon.KeyA, Key: chameleon.HexBytes{6, 5, 4, 3, 2, 1}})
			}
			writeReaderFixture(t, input, request)
			var mu sync.Mutex
			auths := 0
			opens := 0
			port := workflowSimulator(t, func(cmd uint16, payload []byte) (uint16, []byte) {
				switch cmd {
				case 1033:
					return 0x68, []byte{0}
				case 1035:
					return 0x68, configCaps(1001, 1002, 2000, 2007, 2008)
				case 1002:
					return 0x68, []byte{1}
				case 2000:
					return 0, []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}
				case 2007:
					mu.Lock()
					defer mu.Unlock()
					auths++
					if partial && auths == 2 {
						return 0x67, nil
					}
					return 0, nil
				default:
					t.Error("unexpected command", cmd, payload)
					return 0x67, nil
				}
			})
			original := openReaderWorkflowClient
			openReaderWorkflowClient = func(ctx context.Context, p string, timeout time.Duration) (*chameleon.Client, error) {
				opens++
				return original(ctx, p, timeout)
			}
			defer func() { openReaderWorkflowClient = original }()
			err := runReaderWorkflowCLI([]string{"reader", "classic-verify", "--port", port, "--input", input, "--output", output})
			if (err != nil) != partial {
				t.Fatal(err)
			}
			if partial && !strings.Contains(err.Error(), "result saved") {
				t.Fatal(err)
			}
			var envelope struct {
				Schema int                                    `json:"schema"`
				Result chameleon.ClassicKeyVerificationResult `json:"result"`
				Error  *string                                `json:"error"`
			}
			readReaderFixture(t, output, &envelope)
			if envelope.Schema != 1 || envelope.Result.Complete == partial || len(envelope.Result.Keys) != 1 || (envelope.Error != nil) != partial {
				t.Fatal(envelope)
			}
			mu.Lock()
			count := auths
			mu.Unlock()
			want := 1
			if partial {
				want = 2
			}
			if count != want || opens != 1 {
				t.Fatal("verification replay", count, opens)
			}
			before := opens
			if err := runReaderWorkflowCLI([]string{"reader", "classic-verify", "--port", port, "--input", input, "--output", output}); err == nil || opens != before {
				t.Fatal("existing output entered transport", err, opens)
			}
		})
	}
}
