package chameleon

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func boundaryHardnestedBundle(t *testing.T) RecoveryBundle {
	t.Helper()
	var h HardnestedCollector
	raw := make([]byte, 256*9)
	for i := 0; i < 256; i++ {
		binary.BigEndian.PutUint32(raw[i*9+4:], uint32(i)<<24)
	}
	if err := h.Add(raw); err != nil {
		t.Fatal(err)
	}
	file, err := h.File([]byte{1, 2, 3, 4}, 4, KeyB)
	if err != nil {
		t.Fatal(err)
	}
	return RecoveryBundle{Schema: 1, Kind: "hardnested", UID: HexBytes{1, 2, 3, 4}, TargetBlock: 4, TargetType: KeyB, Tool: "hardnested", HardnestedFile: file}
}
func boundaryDarksideBundle(count int) RecoveryBundle {
	b := RecoveryBundle{Schema: 1, Kind: "darkside", UID: HexBytes{1, 2, 3, 4}, TargetBlock: 4, TargetType: KeyB, Tool: "darkside", Arguments: []string{"16909060"}}
	for i := 0; i < count; i++ {
		raw := make([]byte, 33)
		copy(raw[1:], b.UID)
		binary.BigEndian.PutUint32(raw[5:], 0x10203040)
		binary.BigEndian.PutUint64(raw[9:], ^uint64(0))
		binary.BigEndian.PutUint64(raw[17:], 123)
		binary.BigEndian.PutUint32(raw[25:], ^uint32(0))
		binary.BigEndian.PutUint32(raw[29:], 85)
		b.Samples = append(b.Samples, raw)
		b.Arguments = append(b.Arguments, "270544960", "123", "18446744073709551615", "4294967295", "85")
	}
	return b
}

func TestRecoveryBundleValidatedRoundTrip(t *testing.T) {
	for _, b := range []RecoveryBundle{testNestedBundle(), boundaryHardnestedBundle(t), boundaryDarksideBundle(2)} {
		for _, prefix := range [][]byte{nil, {7, 8, 9}, {1, 2, 3, 4, 5, 6}} {
			b.UID = append(append(HexBytes(nil), prefix...), 1, 2, 3, 4)
			path := filepath.Join(t.TempDir(), "capture.json")
			if err := WriteRecoveryBundle(path, b); err != nil {
				t.Fatalf("valid %s capture with %d-byte UID: %v", b.Kind, len(b.UID), err)
			}
			if _, err := ReadRecoveryBundle(path); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func TestRecoveryBundlesRejectInconsistentCaptureBeforePublication(t *testing.T) {
	cases := []struct {
		name   string
		make   func() RecoveryBundle
		mutate func(*RecoveryBundle)
	}{
		{"nested-arguments", testNestedBundle, func(b *RecoveryBundle) { b.Arguments = []string{"16909060", "97", "1"} }},
		{"nested-uid", testNestedBundle, func(b *RecoveryBundle) { b.Arguments[0] = "1" }},
		{"nested-key-type", testNestedBundle, func(b *RecoveryBundle) { b.Arguments[1] = "96" }},
		{"nested-overflow", testNestedBundle, func(b *RecoveryBundle) { b.Arguments[2] = "4294967296" }},
		{"hardnested-uid", func() RecoveryBundle { return boundaryHardnestedBundle(t) }, func(b *RecoveryBundle) { b.HardnestedFile[0] ^= 1 }},
		{"hardnested-target", func() RecoveryBundle { return boundaryHardnestedBundle(t) }, func(b *RecoveryBundle) { b.HardnestedFile[4]++ }},
		{"hardnested-key-type", func() RecoveryBundle { return boundaryHardnestedBundle(t) }, func(b *RecoveryBundle) { b.HardnestedFile[5] = 0 }},
		{"hardnested-short-record", func() RecoveryBundle { return boundaryHardnestedBundle(t) }, func(b *RecoveryBundle) { b.HardnestedFile = append(b.HardnestedFile, 0) }},
		{"hardnested-missing-classes", func() RecoveryBundle { return boundaryHardnestedBundle(t) }, func(b *RecoveryBundle) { b.HardnestedFile[6+255*9+4] = 0 }},
		{"darkside-uid", func() RecoveryBundle { return boundaryDarksideBundle(1) }, func(b *RecoveryBundle) { b.Samples[0][1] ^= 1 }},
		{"darkside-status", func() RecoveryBundle { return boundaryDarksideBundle(1) }, func(b *RecoveryBundle) { b.Samples[0][0] = 1 }},
		{"darkside-arguments", func() RecoveryBundle { return boundaryDarksideBundle(1) }, func(b *RecoveryBundle) { b.Arguments[1] = "1" }},
		{"darkside-count", func() RecoveryBundle { return boundaryDarksideBundle(1) }, func(b *RecoveryBundle) { b.Samples = append(b.Samples, b.Samples[0]) }},
		{"darkside-runs", func() RecoveryBundle { return boundaryDarksideBundle(1) }, func(b *RecoveryBundle) { b.Runs = 2 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := tc.make()
			tc.mutate(&b)
			path := filepath.Join(t.TempDir(), "capture.json")
			if err := WriteRecoveryBundle(path, b); err == nil {
				t.Fatal("invalid bundle published")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("invalid bundle left output", err)
			}
		})
	}
	for _, i := range []int{2, 3} {
		args := append([]string(nil), boundaryDarksideBundle(1).Arguments...)
		args[i] = "18446744073709551616"
		if err := validateRecoveryArgs("darkside", args); err == nil {
			t.Fatalf("64-bit field %d overflow accepted", i)
		}
	}
}
func TestRecoveryDecimalArgumentsRespectWireWidths(t *testing.T) {
	for _, tc := range []struct {
		tool string
		args []string
		bad  []int
	}{
		{"nested", []string{"4294967295", "14", "4294967295", "4294967295", "255"}, []int{0, 2, 3, 4}},
		{"staticnested", []string{"4294967295", "97", "4294967295", "4294967295"}, []int{0, 2, 3}},
		{"darkside", []string{"4294967295", "4294967295", "18446744073709551615", "18446744073709551615", "4294967295", "4294967295"}, []int{0, 1, 4, 5}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			if err := validateRecoveryArgs(tc.tool, tc.args); err != nil {
				t.Fatal("maximum valid fields rejected", err)
			}
			for _, i := range tc.bad {
				args := append([]string(nil), tc.args...)
				args[i] = "4294967296"
				if tc.tool == "nested" && i == 4 {
					args[i] = "256"
				}
				if err := validateRecoveryArgs(tc.tool, args); err == nil {
					t.Fatalf("field %d overflow accepted", i)
				}
			}
		})
	}
}
func TestDarksideBundleSupportsDocumentedThousandRuns(t *testing.T) {
	b := boundaryDarksideBundle(1000)
	if err := b.Seal(); err != nil {
		t.Fatal(err)
	}
	if err := validateRecoveryArgs(b.Tool, b.Arguments); err != nil {
		t.Fatal("documented run count cannot be computed:", err)
	}
	b.Arguments = append(b.Arguments, "0", "0", "0", "0", "0")
	if err := validateRecoveryArgs(b.Tool, b.Arguments); err == nil {
		t.Fatal("unbounded acquisition count accepted")
	}
}
