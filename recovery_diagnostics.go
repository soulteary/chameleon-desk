package chameleon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

type RecoveryWorkerFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}
type RecoveryWorkerManifest struct {
	Schema int                  `json:"schema_version"`
	Build  BuildInfo            `json:"core_version"`
	OS     string               `json:"os"`
	Arch   string               `json:"arch"`
	Files  []RecoveryWorkerFile `json:"files"`
}
type RecoveryWorkerDiagnostic struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Code  string `json:"code"`
}
type RecoveryDiagnostic struct {
	Schema  int                        `json:"schema_version"`
	Build   *BuildInfo                 `json:"worker_version,omitempty"`
	Checks  []BridgeDiagnosticCheck    `json:"checks"`
	Workers []RecoveryWorkerDiagnostic `json:"workers"`
}

// DefaultRecoveryToolsDir prefers the companion beside the executable, independent
// of the working directory. A damaged companion is never hidden by a fallback.
func DefaultRecoveryToolsDir() string {
	executable, err := os.Executable()
	if err == nil {
		folder := filepath.Join(filepath.Dir(executable), "recovery", "bin")
		if _, err := os.Lstat(folder); err == nil {
			return folder
		}
	}
	return filepath.Join("tools", "bin")
}

// DiagnoseRecovery only inspects configured files. It never runs a helper, reads
// card/key inputs, searches PATH or opens a device. Hashes are integrity checks,
// not authentication of an untrusted distribution or runtime qualification.
func DiagnoseRecovery(directory string) RecoveryDiagnostic {
	return diagnoseRecovery(directory, CoreBuildInfo())
}
func diagnoseRecovery(directory string, core BuildInfo) RecoveryDiagnostic {
	out := RecoveryDiagnostic{Schema: 1, Checks: []BridgeDiagnosticCheck{}, Workers: []RecoveryWorkerDiagnostic{}}
	add := func(code, status, message string) {
		out.Checks = append(out.Checks, BridgeDiagnosticCheck{code, status, message})
	}
	names := map[string]string{}
	manifestPath := filepath.Join(directory, "workers.json")
	stat, err := os.Lstat(manifestPath)
	packaged := err == nil || !os.IsNotExist(err)
	valid := true
	if packaged {
		valid = false
		if err == nil && stat.Mode().IsRegular() && stat.Size() <= 64<<10 {
			raw, e := os.ReadFile(manifestPath)
			var m RecoveryWorkerManifest
			if e == nil {
				e = json.Unmarshal(raw, &m)
			}
			if e == nil && m.Schema == 1 && m.OS == runtime.GOOS && m.Arch == runtime.GOARCH && len(m.Files) == len(recoveryToolNames) && m.Build.Revision != "" && checkBridgeVersion(core, &m.Build) == nil {
				valid = true
				known := map[string]bool{}
				for _, name := range recoveryToolNames {
					known[name] = true
				}
				for _, f := range m.Files {
					hash, e := hex.DecodeString(f.SHA256)
					if !known[f.Name] || names[f.Name] != "" || e != nil || len(hash) != 32 {
						valid = false
						break
					}
					names[f.Name] = f.SHA256
				}
				if valid {
					build := m.Build
					out.Build = &build
				}
			}
		}
		if !valid {
			add("worker_manifest_invalid", "fail", "Worker manifest is invalid, incompatible or for another platform; use a matching native distribution")
		} else if core.Revision == "" {
			add("worker_revision_unverified", "unknown", "Worker release and compatibility match; the source core has no stamped revision")
		} else {
			add("worker_version_match", "pass", "Worker platform, release, compatibility and revision match")
		}
	} else {
		add("worker_manifest_missing", "unknown", "No companion manifest; local helper files are unversioned and have not been executed")
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	for _, name := range recoveryToolNames {
		item := RecoveryWorkerDiagnostic{Name: name, State: "available", Code: "file_present_unverified"}
		path := filepath.Join(directory, name+suffix)
		info, e := os.Lstat(path)
		switch {
		case e != nil:
			item.State, item.Code = "unavailable", "worker_missing"
		case !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 64<<20:
			item.State, item.Code = "unavailable", "worker_file_invalid"
		case runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0:
			item.State, item.Code = "unavailable", "worker_not_executable"
		case packaged && !valid:
			item.State, item.Code = "unavailable", "worker_manifest_invalid"
		case packaged:
			f, e := os.Open(path)
			if e != nil {
				item.State, item.Code = "unavailable", "worker_unreadable"
				break
			}
			h := sha256.New()
			_, e = io.Copy(h, io.LimitReader(f, (64<<20)+1))
			_ = f.Close()
			if e != nil || hex.EncodeToString(h.Sum(nil)) != names[name] {
				item.State, item.Code = "unavailable", "worker_checksum_mismatch"
			} else {
				item.Code = "worker_integrity_match"
			}
		}
		out.Workers = append(out.Workers, item)
	}
	add("worker_runtime_unverified", "unknown", "No helper was executed; loader dependencies, recovery results and hardware remain unverified")
	return out
}
func (r RecoveryDiagnostic) Require(names ...string) error {
	for _, name := range names {
		found := false
		for _, w := range r.Workers {
			if w.Name == name {
				found = true
				if w.State != "available" {
					return fmt.Errorf("trusted recovery helper is unavailable: %s (%s)", name, w.Code)
				}
			}
		}
		if !found {
			return fmt.Errorf("unknown recovery helper: %s", name)
		}
	}
	return nil
}
