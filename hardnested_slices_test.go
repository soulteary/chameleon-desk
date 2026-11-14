package chameleon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHardnestedSliceBounds(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("hardnested regression requires a POSIX compiler")
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		if os.Getenv("CHAMELEON_TEST_C_REQUIRED") == "1" {
			t.Fatal("C compiler required for recovery regressions")
		}
		t.Skip("hardnested regression requires a C compiler")
	}
	binary := filepath.Join(t.TempDir(), "hardnested-slices")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, cc, "-O1", "-g", "-D_GNU_SOURCE", "-fsanitize=address", "-fno-omit-frame-pointer", "-pthread", "testdata/recovery/hardnested_slices.c", "-o", binary)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build hardnested slice regression: %v\n%s", err, output)
	}
	for failure := 0; failure <= 5; failure++ {
		t.Run(strconv.Itoa(failure), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, strconv.Itoa(failure))
			leaks := "1"
			if runtime.GOOS == "darwin" {
				leaks = "0"
			}
			command.Env = append(os.Environ(), "ASAN_OPTIONS=detect_leaks="+leaks+":halt_on_error=1:exitcode=97", "LSAN_OPTIONS=exitcode=98")
			output, err := command.CombinedOutput()
			actual := 0
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatalf("run: %v\n%s", err, output)
				}
				actual = exit.ExitCode()
			}
			expected := 0
			if failure != 0 {
				expected = 4
			}
			if ctx.Err() != nil || actual != expected || strings.Contains(string(output), "Sanitizer") {
				t.Fatalf("slice regression: exit %d, expected %d: %v\n%s", actual, expected, ctx.Err(), output)
			}
		})
	}
}
