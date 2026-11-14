package chameleon

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pbBytes(field int, data []byte) []byte {
	return append([]byte{byte(field<<3 | 2), byte(len(data))}, data...)
}

// TestFirmwareInitPacket builds a Nordic signed init packet for a binary.
func firmwareInitPacket(t testing.TB, firmware []byte, signed bool) []byte {
	t.Helper()
	sum := sha256.Sum256(firmware)
	reversed := make([]byte, len(sum))
	for i := range sum {
		reversed[len(sum)-1-i] = sum[i]
	}
	hash := append([]byte{1<<3 | 0, 3}, pbBytes(2, reversed)...)
	init := append([]byte{1<<3 | 0, 1, 7<<3 | 0, 0x90, 0x4e}, pbBytes(8, hash)...)
	command := append([]byte{1<<3 | 0, 1}, pbBytes(2, init)...)
	if !signed {
		return pbBytes(1, command)
	}
	return pbBytes(2, append(append(pbBytes(1, command), 2<<3|0, 0), pbBytes(3, bytes.Repeat([]byte{0xaa}, 64))...))
}

func firmwareTestZIP(t testing.TB, firmware []byte, signed bool) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, data := range map[string][]byte{"manifest.json": []byte(`{"manifest":{"application":{"bin_file":"application.bin","dat_file":"application.dat"}}}`), "application.bin": firmware, "application.dat": firmwareInitPacket(t, firmware, signed)} {
		w, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}

const (
	testMainCommit = "6009a5fb8adbb92d3efb548eaac9e501bd179801"
	testOldCommit  = "5c99d4a0000000000000000000000000000000aa"
)

type firmwareSite struct {
	artifacts, releases string
	files               map[string][]byte
	status              int
}

func (s *firmwareSite) updater(t *testing.T) *FirmwareUpdater {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || !strings.HasPrefix(r.Header.Get("User-Agent"), "chameleon-go/") {
			t.Errorf("unexpected request headers %v", r.Header)
		}
		if s.status != 0 {
			w.WriteHeader(s.status)
			fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)
			return
		}
		switch {
		case r.URL.Path == "/api/actions/artifacts":
			fmt.Fprint(w, s.artifacts)
		case r.URL.Path == "/api/releases":
			fmt.Fprint(w, strings.ReplaceAll(s.releases, "BASE", server.URL))
		case r.URL.Path == "/api/tags":
			fmt.Fprintf(w, `[{"name":"v2.3.0","commit":{"sha":"%s"}}]`, testMainCommit)
		case s.files[r.URL.Path] != nil:
			_, _ = w.Write(s.files[r.URL.Path])
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return &FirmwareUpdater{APIBase: server.URL + "/api", NightlyBase: server.URL + "/nightly", Client: server.Client()}
}

func artifactJSON(id, run int, branch, sha string, repo int, expired bool) string {
	return fmt.Sprintf(`{"id":%d,"name":"ultra-dfu-app","size_in_bytes":10,"expired":%v,"created_at":"2026-11-11T06:46:53Z","workflow_run":{"id":%d,"head_branch":"%s","head_sha":"%s","head_repository_id":%d}}`, id, expired, run, branch, sha, repo)
}

func releaseJSON(commit, digest string) string {
	return fmt.Sprintf(`[{"tag_name":"v2.3.0","prerelease":false,"target_commitish":"%s","author":{"login":"github-actions[bot]"},"assets":[{"name":"ultra-dfu-app.zip","size":5,"browser_download_url":"BASE/dl/stable.zip"}]},
{"tag_name":"dev","prerelease":true,"target_commitish":"%s","author":{"login":"github-actions[bot]"},"assets":[{"name":"lite-dfu-app.zip","size":5,"browser_download_url":"BASE/dl/lite.zip"},{"name":"ultra-dfu-app.zip","size":5,"digest":"%s","browser_download_url":"BASE/dl/dev.zip"}]}]`, testOldCommit, commit, digest)
}

func TestFirmwareLatestBuildSelection(t *testing.T) {
	ctx := context.Background()
	site := &firmwareSite{releases: releaseJSON(testMainCommit, "")}
	site.artifacts = `{"artifacts":[` + strings.Join([]string{
		artifactJSON(90, 900, "fix/branch", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1365161774, false),
		artifactJSON(80, 500, "main", testMainCommit, 581338100, false),
		artifactJSON(85, 400, "main", testOldCommit, 581338100, false),
		artifactJSON(70, 800, "main", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", 581338100, true),
		artifactJSON(60, 700, "main", "cccccccccccccccccccccccccccccccccccccccc", 42, false),
	}, ",") + `]}`
	u := site.updater(t)
	a, e := u.ActionsBuild(ctx, "ultra")
	if e != nil || a.Commit != testMainCommit || a.RunID != 500 || a.ArtifactID != 80 || !strings.HasSuffix(a.URL, "/nightly/suites/500/artifacts/80") {
		t.Fatalf("%+v %v", a, e)
	}
	if v, e := u.LatestBuild(ctx, "Chameleon Ultra"); e != nil || v.Source != "release" || v.Tag != "dev" {
		t.Fatalf("same-commit pre-release not preferred: %+v %v", v, e)
	}
	site.releases = releaseJSON(testOldCommit, "")
	if v, e := u.LatestBuild(ctx, "ultra"); e != nil || v.Source != "actions" || v.Commit != testMainCommit {
		t.Fatalf("%+v %v", v, e)
	}
	site.artifacts = `{"artifacts":[]}`
	if v, e := u.LatestBuild(ctx, "ultra"); e != nil || v.Source != "release" || v.Commit != testOldCommit {
		t.Fatalf("%+v %v", v, e)
	}
	if v, e := u.ReleaseBuild(ctx, "ultra", "v2.3.0"); e != nil || !strings.HasSuffix(v.URL, "/dl/stable.zip") {
		t.Fatalf("%+v %v", v, e)
	}
	if _, e := u.ReleaseBuild(ctx, "ultra", "v9"); e == nil {
		t.Fatal("unknown tag accepted")
	}
	if _, e := u.ActionsBuild(ctx, "mega"); e == nil {
		t.Fatal("unknown model accepted")
	}
	site.status = http.StatusForbidden
	if _, e := u.LatestBuild(ctx, "ultra"); e == nil || !strings.Contains(e.Error(), "rate limit") {
		t.Fatalf("API message lost: %v", e)
	}
}

func TestFirmwareCheckUpdate(t *testing.T) {
	ctx := context.Background()
	site := &firmwareSite{artifacts: `{"artifacts":[` + artifactJSON(80, 500, "main", testMainCommit, 581338100, false) + `]}`, releases: releaseJSON(testMainCommit, "")}
	u := site.updater(t)
	for version, want := range map[string][3]any{
		"v2.2.0-64-g5c99d4a":       {"5c99d4a", false, false},
		"v2.2.0-64-g6009a5f-dirty": {"6009a5f", true, true},
		"v2.3.0":                   {testMainCommit, false, true},
		testMainCommit:             {testMainCommit, false, true},
	} {
		v, e := u.CheckFirmwareUpdate(ctx, "ultra", version)
		if e != nil || v.DeviceCommit != want[0] || v.Dirty != want[1] || v.UpToDate != want[2] || v.Latest.Commit != testMainCommit {
			t.Fatalf("%s: %+v %v", version, v, e)
		}
	}
	if _, e := u.CheckFirmwareUpdate(ctx, "ultra", "v0.0.1"); e == nil {
		t.Fatal("unknown tag accepted")
	}
	if _, e := u.CheckFirmwareUpdate(ctx, "ultra", ""); e == nil {
		t.Fatal("empty version accepted")
	}
}

func TestFirmwareDownloadVerifiesAndNeverOverwrites(t *testing.T) {
	ctx := context.Background()
	good := firmwareTestZIP(t, []byte("firmware image"), true)
	sum := sha256.Sum256(good)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	site := &firmwareSite{releases: releaseJSON(testMainCommit, digest), artifacts: `{"artifacts":[` + artifactJSON(80, 500, "main", testMainCommit, 581338100, false) + `]}`,
		files: map[string][]byte{"/dl/dev.zip": good, "/nightly/suites/500/artifacts/80": good, "/dl/stable.zip": firmwareTestZIP(t, []byte("unsigned"), false)}}
	u := site.updater(t)
	dir := t.TempDir()
	clean := func() {
		t.Helper()
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".chameleon-fw-") {
				t.Fatalf("temporary file left: %s", entry.Name())
			}
		}
	}
	build, e := u.LatestBuild(ctx, "ultra")
	if e != nil {
		t.Fatal(e)
	}
	out := filepath.Join(dir, "fw.zip")
	v, e := u.DownloadFirmware(ctx, build, out)
	if e != nil || !v.DigestVerified || v.InitHash != "sha256" || len(v.Package.Parts) != 1 || v.Bytes != len(good) {
		t.Fatalf("%+v %v", v, e)
	}
	if b, _ := os.ReadFile(out); !bytes.Equal(b, good) {
		t.Fatal("saved file differs")
	}
	if _, e = ReadFirmwareApplication(out); e != nil {
		t.Fatal(e)
	}
	if _, e = u.DownloadFirmware(ctx, build, out); e == nil {
		t.Fatal("existing output replaced")
	}
	actions, _ := u.ActionsBuild(ctx, "ultra")
	if v, e = u.DownloadFirmware(ctx, actions, filepath.Join(dir, "actions.zip")); e != nil || v.DigestVerified || len(v.Notes) == 0 {
		t.Fatalf("%+v %v", v, e)
	}
	site.files["/dl/dev.zip"] = firmwareTestZIP(t, []byte("tampered image"), true)
	if _, e = u.DownloadFirmware(ctx, build, filepath.Join(dir, "bad-digest.zip")); e == nil || !strings.Contains(e.Error(), "digest") {
		t.Fatalf("digest mismatch accepted: %v", e)
	}
	stable, _ := u.ReleaseBuild(ctx, "ultra", "v2.3.0")
	if _, e = u.DownloadFirmware(ctx, stable, filepath.Join(dir, "unsigned.zip")); e == nil || !strings.Contains(e.Error(), "not signed") {
		t.Fatalf("unsigned package accepted: %v", e)
	}
	site.files["/dl/stable.zip"] = []byte("not a zip")
	if _, e = u.DownloadFirmware(ctx, stable, filepath.Join(dir, "junk.zip")); e == nil {
		t.Fatal("junk accepted")
	}
	site.files["/dl/stable.zip"] = bytes.Repeat([]byte{0}, firmwareMaxBytes+1)
	if _, e = u.DownloadFirmware(ctx, stable, filepath.Join(dir, "huge.zip")); e == nil {
		t.Fatal("oversized download accepted")
	}
	stable.URL = "http://example.invalid/fw.zip"
	if _, e = u.DownloadFirmware(ctx, stable, filepath.Join(dir, "plain.zip")); e == nil || !strings.Contains(e.Error(), "HTTPS") {
		t.Fatalf("plain HTTP accepted: %v", e)
	}
	for _, name := range []string{"bad-digest.zip", "unsigned.zip", "junk.zip", "huge.zip", "plain.zip"} {
		if _, e := os.Stat(filepath.Join(dir, name)); e == nil {
			t.Fatalf("%s written despite rejection", name)
		}
	}
	clean()
}

func TestVerifyFirmwareInit(t *testing.T) {
	fw := []byte("abc")
	init := firmwareInitPacket(t, fw, true)
	if kind, e := VerifyFirmwareInit(init, fw); e != nil || kind != "sha256" {
		t.Fatal(kind, e)
	}
	if _, e := VerifyFirmwareInit(init, []byte("abd")); e == nil {
		t.Fatal("hash mismatch accepted")
	}
	for i := range init {
		if _, e := VerifyFirmwareInit(init[:i], fw); e == nil {
			t.Fatalf("truncated init packet (%d bytes) accepted", i)
		}
	}
}
