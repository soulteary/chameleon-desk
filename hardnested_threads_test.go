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

func TestHardnestedWorkerPartitions(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("hardnested regression requires a POSIX compiler")
	}
	cc, e := exec.LookPath("cc")
	if e != nil {
		if os.Getenv("CHAMELEON_TEST_C_REQUIRED") == "1" {
			t.Fatal("C compiler required for recovery regressions")
		}
		t.Skip("hardnested regression requires a C compiler")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "hardnested-threads")
	command := exec.CommandContext(ctx, cc, "-O1", "-g", "-fsanitize=address", "-fno-omit-frame-pointer", "-pthread", "testdata/recovery/hardnested_threads.c", "-o", binary)
	if output, e := command.CombinedOutput(); e != nil {
		t.Fatalf("build hardnested thread regression: %v\n%s", e, output)
	}
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	command = exec.CommandContext(ctx, binary)
	leaks := "1"
	if runtime.GOOS == "darwin" {
		leaks = "0"
	}
	command.Env = append(os.Environ(), "ASAN_OPTIONS=detect_leaks="+leaks+":halt_on_error=1:exitcode=97", "LSAN_OPTIONS=exitcode=98")
	if output, e := command.CombinedOutput(); e != nil || ctx.Err() != nil || strings.Contains(string(output), "Sanitizer") {
		t.Fatalf("hardnested thread regression: %v (%v)\n%s", e, ctx.Err(), output)
	}
}
