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

func TestHardnestedConcurrentCallbacks(t *testing.T) {
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
	probeArgs := []string{"-x", "c", "-", "-fsyntax-only"}
	include := ""
	if _, err := os.Stat("/opt/homebrew/opt/xz/include/lzma.h"); err == nil {
		include = "-I/opt/homebrew/opt/xz/include"
		probeArgs = append(probeArgs, include)
	}
	probe := exec.Command(cc, probeArgs...)
	probe.Stdin = strings.NewReader("#include <lzma.h>\n")
	if output, err := probe.CombinedOutput(); err != nil {
		if os.Getenv("CHAMELEON_TEST_C_REQUIRED") == "1" {
			t.Fatalf("liblzma headers required: %v\n%s", err, output)
		}
		t.Skip("hardnested progress regression requires liblzma development files")
	}
	strip := "-Wl,--gc-sections"
	if runtime.GOOS == "darwin" {
		strip = "-Wl,-dead_strip"
	}
	for _, fixture := range []string{"hardnested_progress", "hardnested_bitarray_dispatch"} {
		t.Run(fixture, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), fixture)
			args := []string{"-O1", "-g", "-D_GNU_SOURCE", "-fsanitize=address", "-fno-omit-frame-pointer", "-ffunction-sections", "-fdata-sections", strip, "-pthread", "testdata/recovery/" + fixture + ".c", "-lm", "-o", binary}
			if include != "" {
				args = append(args, include)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if output, err := exec.CommandContext(ctx, cc, args...).CombinedOutput(); err != nil {
				t.Fatalf("build hardnested progress regression: %v\n%s", err, output)
			}
			ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			command := exec.CommandContext(ctx, binary)
			leaks := "1"
			if runtime.GOOS == "darwin" {
				leaks = "0"
			}
			command.Env = append(os.Environ(), "ASAN_OPTIONS=detect_leaks="+leaks+":halt_on_error=1:exitcode=97", "LSAN_OPTIONS=exitcode=98")
			if output, err := command.CombinedOutput(); err != nil || ctx.Err() != nil || strings.Contains(string(output), "Sanitizer") {
				t.Fatalf("hardnested progress regression: %v (%v)\n%s", err, ctx.Err(), output)
			}
		})
	}

}
