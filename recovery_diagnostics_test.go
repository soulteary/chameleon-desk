package chameleon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func recoveryManifestFixture(t *testing.T, dir string) RecoveryWorkerManifest {
	t.Helper()
	build := CoreBuildInfo()
	build.Revision = strings.Repeat("a", 40)
	m := RecoveryWorkerManifest{Schema: 1, Build: build, OS: runtime.GOOS, Arch: runtime.GOARCH}
	for _, name := range RecoveryTools() {
		data := []byte("not executed " + name)
		suffix := ""
		if runtime.GOOS == "windows" {
			suffix = ".exe"
		}
		if err := os.WriteFile(filepath.Join(dir, name+suffix), data, 0700); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(data)
		m.Files = append(m.Files, RecoveryWorkerFile{Name: name, SHA256: hex.EncodeToString(hash[:])})
	}
	writeRecoveryManifest(t, dir, m)
	return m
}
func writeRecoveryManifest(t *testing.T, dir string, m RecoveryWorkerManifest) {
	t.Helper()
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "workers.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestRecoveryDiagnosticPairsFilesAndRejectsTamperingWithoutExecution(t *testing.T) {
	dir := t.TempDir()
	m := recoveryManifestFixture(t, dir)
	good := diagnoseRecovery(dir, m.Build)
	if good.Build == nil || good.Require(RecoveryTools()...) != nil || good.Checks[0].Code != "worker_version_match" {
		t.Fatal(good)
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, "nested"+suffix), []byte("changed"), 0700); err != nil {
		t.Fatal(err)
	}
	bad := diagnoseRecovery(dir, m.Build)
	if bad.Require("nested") == nil || !strings.Contains(bad.Require("nested").Error(), "worker_checksum_mismatch") {
		t.Fatal(bad)
	}
	raw, _ := json.Marshal(bad)
	if strings.Contains(string(raw), dir) || strings.Contains(string(raw), "changed") {
		t.Fatal("file data/path exposed")
	}
	for _, change := range []func(*RecoveryWorkerManifest){func(v *RecoveryWorkerManifest) { v.OS = "other" }, func(v *RecoveryWorkerManifest) { v.Build.Release = "other" }, func(v *RecoveryWorkerManifest) { v.Build.Revision = strings.Repeat("b", 40) }, func(v *RecoveryWorkerManifest) { v.Files[0].Name = "../nested" }, func(v *RecoveryWorkerManifest) { v.Files = v.Files[:1] }} {
		data, _ := json.Marshal(m)
		var next RecoveryWorkerManifest
		_ = json.Unmarshal(data, &next)
		change(&next)
		writeRecoveryManifest(t, dir, next)
		if report := diagnoseRecovery(dir, m.Build); report.Require("staticnested") == nil {
			t.Fatal("incompatible manifest accepted", report)
		}
	}
}
func TestRecoveryDiagnosticLegacyMissingAndPermissionsAreExplicit(t *testing.T) {
	dir := t.TempDir()
	report := DiagnoseRecovery(dir)
	if report.Checks[0].Status != "unknown" || report.Require("nested") == nil {
		t.Fatal(report)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for _, name := range []string{"nested", "staticnested"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateClassicRecoveryHelpers(dir, "nested"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "nested"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateClassicRecoveryHelpers(dir, "nested"); err == nil {
		t.Fatal("non-executable accepted")
	}
	if err := os.Remove(filepath.Join(dir, "nested")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "staticnested"), filepath.Join(dir, "nested")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateClassicRecoveryHelpers(dir, "nested"); err == nil {
		t.Fatal("symlink accepted")
	}
}
