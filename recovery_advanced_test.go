package chameleon

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHardnestedCollector(t *testing.T) {
	h := HardnestedCollector{}
	raw := make([]byte, 256*9)
	for i := 0; i < 256; i++ {
		binary.BigEndian.PutUint32(raw[i*9+4:], uint32(i)<<24)
	}
	if e := h.Add(raw); e != nil || !h.Ready() || h.Unique != 256 || h.Sum != 128 {
		t.Fatal(h.Unique, h.Sum, e)
	}
	if e := h.Add(raw); e != nil || h.Unique != 256 {
		t.Fatal(e)
	}
	file, e := h.File([]byte{1, 2, 3, 4, 5, 6, 7}, 4, KeyB)
	if e != nil || len(file) != 6+len(h.Data) || file[0] != 4 || file[5] != 1 {
		t.Fatal(e)
	}
	if e := h.Add([]byte{1}); e == nil {
		t.Fatal("truncated pairs accepted")
	}
}
func TestAdvancedRecoveryInvalidBeforeIO(t *testing.T) {
	c := readerSimulator(t, nil)
	_, e := c.RecoverClassic(context.Background(), 0, KeyA, nil, 4, KeyB, AdvancedRecoveryOptions{Limit: 5 * time.Second, MaxRuns: 0, MaxAttempts: 1})
	if e == nil {
		t.Fatal("unbounded config accepted")
	}
}
func TestAdvancedCandidateFormats(t *testing.T) {
	v := advancedCandidates("Key found: 010203040506\nKey1: FFFFFFFFFFFF\nKey 1... 112233445566\nnoise: aaaaaaaaaaaa")
	if len(v) != 3 {
		t.Fatal(v)
	}
}

// Successful external-process fixtures need startup headroom on hosts running
// concurrent builds/security scans. Deadline behavior is tested separately.
const recoveryFixtureLimit = time.Minute

func TestHardnestedAutomatedFlow(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "hardnested"), []byte("#!/bin/sh\nprintf 'Key found: 010203040506\\n'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	scan := []byte{4, 1, 2, 3, 4, 4, 0, 8, 0}
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: scan}, {command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: scan}}
	raw := make([]byte, 256*9)
	for i := 0; i < 256; i++ {
		binary.BigEndian.PutUint32(raw[i*9+4:], uint32(i)<<24)
	}
	steps = append(steps, readerStep{command: 2013, request: []byte{0, 0x60, 0, 0, 0, 0, 0, 0, 0, 0x61, 4}, data: raw}, readerStep{command: 1002, status: 0x68, data: []byte{1}}, readerStep{command: 2000, data: scan}, readerStep{command: 2007, request: []byte{0x61, 4, 1, 2, 3, 4, 5, 6}})
	c := readerSimulator(t, steps)
	v, e := c.RecoverClassic(context.Background(), 0, KeyA, make([]byte, 6), 4, KeyB, AdvancedRecoveryOptions{Strategy: "hardnested", ToolsDir: dir, Limit: recoveryFixtureLimit, MaxRuns: 1, MaxAttempts: 1})
	if e != nil || v.Key.String() != "010203040506" || v.NonceBytes != 2310 {
		t.Fatal(v, e)
	}
}
func TestStaticEncryptedValidationAndNonceShape(t *testing.T) {
	if _, _, e := parseStaticEncrypted(make([]byte, 17), 1); e == nil {
		t.Fatal("truncated sample accepted")
	}
	raw := make([]byte, 18)
	raw[4] = 0x12
	raw[5] = 0x34
	raw[6] = 0x8
	raw[7] = 1
	uid, sectors, e := parseStaticEncrypted(raw, 1)
	if e != nil || uid != 0 || len(sectors) != 1 || sectors[0].A.Parity != 8 || sectors[0].A.Encrypted != 0x01000000 {
		t.Fatal(uid, sectors, e)
	}
	c := readerSimulator(t, nil)
	if _, e := c.RecoverStaticEncrypted(context.Background(), nil, 16, "tools/bin", time.Second); e == nil {
		t.Fatal("invalid backdoor accepted")
	}
}
func TestDarksideAutomatedFlow(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "darkside"), []byte("#!/bin/sh\nprintf 'Key1: 010203040506\\n'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	scan := []byte{4, 1, 2, 3, 4, 4, 0, 8, 0}
	raw := make([]byte, 33)
	copy(raw[1:5], []byte{1, 2, 3, 4})
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: scan}, {command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: scan}, {command: 2004, request: []byte{0x61, 4, 1, 3}, data: raw}, {command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: scan}, {command: 2007, request: []byte{0x61, 4, 1, 2, 3, 4, 5, 6}}}
	c := readerSimulator(t, steps)
	v, e := c.RecoverClassic(context.Background(), 0, KeyA, nil, 4, KeyB, AdvancedRecoveryOptions{Strategy: "darkside", ToolsDir: dir, Limit: recoveryFixtureLimit, MaxRuns: 1, MaxAttempts: 1})
	if e != nil || v.Key.String() != "010203040506" {
		t.Fatal(v, e)
	}
}
func TestSenestedAutomatedWorkspaceAndVerification(t *testing.T) {
	dir := t.TempDir()
	tools := map[string]string{"staticnested_1nt": "name=$(printf 'keys_%s_%02d_%s.dic' \"$1\" \"$2\" \"$3\")\nprintf '010203040506\\n' > \"$name\"\n", "staticnested_2x1nt_rf08s": "cp \"$1\" \"${1%.dic}_filtered.dic\"\ncp \"$2\" \"${2%.dic}_filtered.dic\"\n", "staticnested_2x1nt_rf08s_1key": "printf '010203040506\\n'\n"}
	for name, body := range tools {
		if e := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nset -eu\n"+body), 0700); e != nil {
			t.Fatal(e)
		}
	}
	scan := []byte{4, 1, 2, 3, 4, 4, 0, 8, 0}
	raw := make([]byte, 18)
	copy(raw, []byte{1, 2, 3, 4})
	raw[4] = 0x12
	raw[5] = 0x34
	raw[11] = 0x56
	raw[12] = 0x78
	steps := []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 2000, data: scan}, {command: 2014, request: []byte{0, 0, 0, 0, 0, 0, 1, 0}, data: raw}}
	for _, kind := range []byte{0x61, 0x60} {
		steps = append(steps, readerStep{command: 1002, status: 0x68, data: []byte{1}}, readerStep{command: 2000, data: scan}, readerStep{command: 2015, request: []byte{3, kind, 1, 1, 2, 3, 4, 5, 6}, data: []byte{1, 1, 2, 3, 4, 5, 6}}, readerStep{command: 1002, status: 0x68, data: []byte{1}}, readerStep{command: 2000, data: scan}, readerStep{command: 2007, request: []byte{kind, 3, 1, 2, 3, 4, 5, 6}})
	}
	c := readerSimulator(t, steps)
	v, e := c.RecoverStaticEncrypted(context.Background(), make([]byte, 6), 1, dir, recoveryFixtureLimit)
	if e != nil || len(v.Sectors) != 1 || v.Sectors[0].A.String() != "010203040506" || v.Sectors[0].B.String() != "010203040506" {
		t.Fatal(v, e)
	}
}
