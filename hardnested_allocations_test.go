package chameleon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestHardnestedAllocationFailures(t *testing.T) {
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
	src := filepath.Join("third_party", "chameleon", "src")
	hard := filepath.Join(src, "HardnestedRecovery")
	strip := "-Wl,--gc-sections"
	if runtime.GOOS == "darwin" {
		strip = "-Wl,-dead_strip"
	}
	binary := filepath.Join(t.TempDir(), "hardnested-allocations")
	args := []string{"-O1", "-g", "-D_GNU_SOURCE", "-fsanitize=address", "-fno-omit-frame-pointer", "-ffunction-sections", "-fdata-sections", strip, "-pthread", "-I" + src, "-I" + hard, "-I" + filepath.Join(hard, "pm3"), "-I" + filepath.Join(hard, "hardnested"), "testdata/recovery/hardnested_allocations.c", filepath.Join(src, "crypto1.c"), filepath.Join(src, "parity.c"), "-lm", "-o", binary}
	if _, err := os.Stat("/opt/homebrew/opt/xz/include/lzma.h"); err == nil {
		args = append(args, "-I/opt/homebrew/opt/xz/include")
	}
	probeArgs := []string{"-x", "c", "-", "-fsyntax-only"}
	if _, err := os.Stat("/opt/homebrew/opt/xz/include/lzma.h"); err == nil {
		probeArgs = append(probeArgs, "-I/opt/homebrew/opt/xz/include")
	}
	probe := exec.Command(cc, probeArgs...)
	probe.Stdin = strings.NewReader("#include <lzma.h>\n")
	if output, err := probe.CombinedOutput(); err != nil {
		if os.Getenv("CHAMELEON_TEST_C_REQUIRED") == "1" {
			t.Fatalf("liblzma headers required: %v\n%s", err, output)
		}
		t.Skip("hardnested allocation regression requires liblzma development files")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if output, err := exec.CommandContext(ctx, cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("build hardnested allocation regression: %v\n%s", err, output)
	}
	for _, mode := range []string{"success", "candidate-first", "candidate-append", "crypto-state"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, mode)
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
			expected := 4
			if mode == "success" {
				expected = 0
			}
			if ctx.Err() != nil || actual != expected || strings.Contains(string(output), "Sanitizer") {
				t.Fatalf("allocation regression: exit %d, expected %d: %v\n%s", actual, expected, ctx.Err(), output)
			}
		})
	}
}
