package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	chameleon "github.com/soulteary/chameleon-desk"
)

func signedFirmwareZIP(t *testing.T, firmware []byte) []byte {
	t.Helper()
	field := func(n int, data []byte) []byte { return append([]byte{byte(n<<3 | 2), byte(len(data))}, data...) }
	sum := sha256.Sum256(firmware)
	reversed := make([]byte, len(sum))
	for i := range sum {
		reversed[len(sum)-1-i] = sum[i]
	}
	command := append([]byte{8, 1}, field(2, field(8, append([]byte{8, 3}, field(2, reversed)...)))...)
	init := field(2, append(field(1, command), field(3, bytes.Repeat([]byte{1}, 64))...))
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, data := range map[string][]byte{"manifest.json": []byte(`{"manifest":{"application":{"bin_file":"application.bin","dat_file":"application.dat"}}}`), "application.bin": firmware, "application.dat": init} {
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

func TestFirmwareCheckAndDownloadCLI(t *testing.T) {
	const commit = "6009a5fb8adbb92d3efb548eaac9e501bd179801"
	pkg := signedFirmwareZIP(t, []byte("application"))
	sum := sha256.Sum256(pkg)
	var actionsDownloads atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/actions/artifacts":
			fmt.Fprintf(w, `{"artifacts":[{"id":8,"name":"lite-dfu-app","workflow_run":{"id":5,"head_branch":"main","head_sha":"%s","head_repository_id":581338100}}]}`, commit)
		case "/api/releases":
			fmt.Fprintf(w, `[{"tag_name":"dev","prerelease":true,"target_commitish":"%s","author":{"login":"github-actions[bot]"},"assets":[{"name":"lite-dfu-app.zip","digest":"sha256:%s","browser_download_url":"%s/dev.zip"}]}]`, commit, hex.EncodeToString(sum[:]), server.URL)
		case "/dev.zip":
			_, _ = w.Write(pkg)
		case "/nightly/actions/artifacts/8.zip":
			actionsDownloads.Add(1)
			_, _ = w.Write(pkg)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	old := newFirmwareUpdater
	newFirmwareUpdater = func() *chameleon.FirmwareUpdater {
		return &chameleon.FirmwareUpdater{APIBase: server.URL + "/api", NightlyBase: server.URL + "/nightly", Client: server.Client()}
	}
	defer func() { newFirmwareUpdater = old }()
	dir := t.TempDir()
	run := func(args ...string) error {
		if !isFirmwareUpdate(args) {
			t.Fatalf("%v not routed", args)
		}
		return runFirmwareUpdate(args)
	}
	status := filepath.Join(dir, "status.json")
	if e := run("firmware", "check", "--model", "lite", "--version", "v2.2.0-64-g5c99d4a", "--output", status); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(status); !bytes.Contains(b, []byte(`"up_to_date": false`)) || !bytes.Contains(b, []byte(commit)) {
		t.Fatal(string(b))
	}
	out := filepath.Join(dir, "fw.zip")
	if e := run("firmware", "download", "--model", "lite", "--output", out); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(out); !bytes.Equal(b, pkg) {
		t.Fatal("saved package differs")
	}
	if e := run("firmware", "download", "--model", "lite", "--output", out); e == nil {
		t.Fatal("existing firmware file replaced")
	}
	actionsBefore := actionsDownloads.Load()
	actionsOutput := filepath.Join(dir, "a.zip")
	if e := run("firmware", "download", "--model", "lite", "--source", "actions", "--output", actionsOutput); e != nil {
		t.Fatal(e)
	}
	if actionsDownloads.Load() != actionsBefore+1 {
		t.Fatal("actions download did not request the artifact ZIP path")
	}
	if b, _ := os.ReadFile(actionsOutput); !bytes.Equal(b, pkg) {
		t.Fatal("saved actions package differs")
	}
	for _, bad := range [][]string{
		{"firmware", "download", "--model", "lite"},
		{"firmware", "download", "--model", "lite", "--source", "mirror", "--output", filepath.Join(dir, "b.zip")},
		{"firmware", "download", "--model", "lite", "--source", "actions", "--tag", "dev", "--output", filepath.Join(dir, "b.zip")},
		{"firmware", "download", "--model", "mega", "--output", filepath.Join(dir, "b.zip")},
		{"firmware", "check", "--model", "lite", "--version", "v2.2.0-64-g5c99d4a", "--tag", "dev"},
	} {
		if e := run(bad...); e == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
	if isFirmwareUpdate([]string{"firmware", "flash"}) || isFirmwareUpdate([]string{"firmware"}) {
		t.Fatal("unrelated firmware action routed")
	}
}
