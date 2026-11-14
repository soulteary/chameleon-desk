package chameleon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMfulcThreadFailures(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("worker regression requires a POSIX compiler")
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		if os.Getenv("CHAMELEON_TEST_C_REQUIRED") == "1" {
			t.Fatal("C compiler required for recovery regressions")
		}
		t.Skip("worker regression requires a C compiler")
	}
	dir := t.TempDir()
	crypto := []string{"-lcrypto"}
	if _, err := os.Stat("/opt/homebrew/opt/openssl@3/lib/libcrypto.a"); err == nil {
		crypto = []string{"-I/opt/homebrew/opt/openssl@3/include", "/opt/homebrew/opt/openssl@3/lib/libcrypto.a"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	probe := exec.CommandContext(ctx, cc, append([]string{"-Wno-deprecated-declarations", "-x", "c", "-", "-x", "none", "-o", filepath.Join(dir, "openssl-probe")}, crypto...)...)
	probe.Stdin = strings.NewReader("#include <openssl/des.h>\nint main(void) { DES_cblock k = {0}; DES_key_schedule s; DES_set_key_unchecked(&k, &s); return 0; }\n")
	if output, err := probe.CombinedOutput(); err != nil {
		if os.Getenv("CHAMELEON_TEST_C_REQUIRED") == "1" {
			t.Fatalf("OpenSSL development files required: %v\n%s", err, output)
		}
		t.Skip("worker regression requires OpenSSL development files")
	}
	binary := filepath.Join(dir, "mfulc-threads")
	args := []string{"-O1", "-g", "-D_GNU_SOURCE", "-fsanitize=address", "-fno-omit-frame-pointer", "-Wno-deprecated-declarations", "-pthread", "testdata/recovery/mfulc_threads.c", "-o", binary}
	if output, err := exec.CommandContext(ctx, cc, append(args, crypto...)...).CombinedOutput(); err != nil {
		t.Fatalf("build worker regression: %v\n%s", err, output)
	}
	runCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	cmd := exec.CommandContext(runCtx, binary)
	leaks := "1"
	if runtime.GOOS == "darwin" {
		leaks = "0"
	}
	cmd.Env = append(os.Environ(), "ASAN_OPTIONS=detect_leaks="+leaks+":halt_on_error=1:exitcode=97", "LSAN_OPTIONS=exitcode=98")
	output, err := cmd.CombinedOutput()
	if err != nil || runCtx.Err() != nil || strings.Contains(string(output), "Sanitizer") || !strings.Contains(string(output), "Full key (hex): 0E000000080A0C0E10121416181A1C1E") {
		t.Fatalf("worker failure/recovery regression: %v\n%s", err, output)
	}
}
