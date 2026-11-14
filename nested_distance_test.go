package chameleon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestNestedDistanceBounds(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("requires a POSIX sanitizer compiler")
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		if os.Getenv("CHAMELEON_TEST_C_REQUIRED") == "1" {
			t.Fatal("C compiler required for recovery regressions")
		}
		t.Skip("requires a C compiler")
	}
	src := filepath.Join("third_party", "chameleon", "src")
	binary := filepath.Join(t.TempDir(), "nested-distance")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	args := []string{"-O1", "-g", "-fsanitize=address", "-fno-omit-frame-pointer", "-I" + src, "testdata/recovery/nested_distance.c", filepath.Join(src, "common.c"), "-o", binary}
	if output, err := exec.CommandContext(ctx, cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("build distance regression: %v\n%s", err, output)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary)
	leaks := "1"
	if runtime.GOOS == "darwin" {
		leaks = "0"
	}
	command.Env = append(os.Environ(), "ASAN_OPTIONS=detect_leaks="+leaks+":halt_on_error=1:exitcode=97", "LSAN_OPTIONS=exitcode=98")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("distance regression: %v (%v)\n%s", err, ctx.Err(), output)
	}
}
