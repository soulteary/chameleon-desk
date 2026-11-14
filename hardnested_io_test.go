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

func TestHardnestedNonceFiles(t *testing.T) {
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
	dir := t.TempDir()
	build := func(t *testing.T, name string, files ...string) string {
		t.Helper()
		out := filepath.Join(dir, name)
		args := []string{"-O1", "-g", "-D_GNU_SOURCE", "-fsanitize=address", "-fno-omit-frame-pointer", "-pthread", "-I" + src, "-I" + hard, "-I" + filepath.Join(hard, "pm3"), "-I" + filepath.Join(hard, "hardnested"), "-o", out}
		args = append(args, files...)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if output, err := exec.CommandContext(ctx, cc, args...).CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", name, err, output)
		}
		return out
	}
	run := func(t *testing.T, binary string, code int, file string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, file)
		cmd.Dir = t.TempDir()
		leaks := "1"
		if runtime.GOOS == "darwin" {
			leaks = "0"
		}
		cmd.Env = append(os.Environ(), "ASAN_OPTIONS=detect_leaks="+leaks+":halt_on_error=1:exitcode=97", "LSAN_OPTIONS=exitcode=98")
		output, err := cmd.CombinedOutput()
		actual := 0
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("run: %v\n%s", err, output)
			}
			actual = exit.ExitCode()
		}
		if ctx.Err() != nil || actual != code || strings.Contains(string(output), "Sanitizer") {
			t.Fatalf("run %s: exit %d, expected %d: %v\n%s", filepath.Base(binary), actual, code, ctx.Err(), output)
		}
		return string(output)
	}
	write := func(t *testing.T, name string, data []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	header := []byte{1, 2, 3, 4, 7, 1}
	valid := append([]byte(nil), header...)
	for i := 0; i < 256; i += 2 {
		valid = append(valid, byte(i), 0, 0, 0, byte(i+1), 0, 0, 0, 0xA5)
	}
	validPath := write(t, "complete.bin", valid)
	parser := build(t, "hardnested-parser", filepath.Join(hard, "hardnested_main.c"), "testdata/recovery/hardnested_capture.c")
	if output := run(t, parser, 0, validPath); !strings.Contains(output, "Key found: 974c262b9278") {
		t.Fatal("complete nonce capture layout changed")
	}
	invalid := [][]byte{nil, {1, 2, 3}, header, append(append([]byte(nil), header...), make([]byte, 9)...), append(append([]byte(nil), valid...), 0), append(append([]byte(nil), valid[:5]...), 2)}
	for n := 1; n < 9; n++ {
		invalid = append(invalid, append(append([]byte(nil), header...), make([]byte, n)...))
	}
	checkInvalid := func(t *testing.T, binary string) {
		t.Helper()
		for i, data := range invalid {
			path := write(t, "invalid-"+string(rune('a'+i))+".bin", data)
			if output := run(t, binary, 1, path); strings.Contains(output, "Key found:") {
				t.Fatal("incomplete nonce capture reported a key")
			}
		}
	}
	checkInvalid(t, parser)

	t.Run("actual worker", func(t *testing.T) {
		lzma := []string{"-llzma"}
		if _, err := os.Stat("/opt/homebrew/opt/xz/lib/liblzma.a"); err == nil {
			lzma = []string{"-I/opt/homebrew/opt/xz/include", "/opt/homebrew/opt/xz/lib/liblzma.a"}
		}
		probe := exec.Command(cc, append([]string{"-x", "c", "-", "-x", "none", "-o", filepath.Join(dir, "lzma-probe")}, lzma...)...)
		probe.Stdin = strings.NewReader("#include <lzma.h>\nint main(void) { return lzma_version_number() == 0; }\n")
		if output, err := probe.CombinedOutput(); err != nil {
			if os.Getenv("CHAMELEON_TEST_C_REQUIRED") == "1" {
				t.Fatalf("liblzma required: %v\n%s", err, output)
			}
			t.Skip("actual hardnested regression requires liblzma development files")
		}
		files := []string{filepath.Join(hard, "hardnested_main.c"), filepath.Join(src, "common.c"), filepath.Join(src, "crapto1.c"), filepath.Join(src, "crypto1.c"), filepath.Join(src, "bucketsort.c"), filepath.Join(src, "parity.c")}
		for _, rel := range []string{"pm3/ui.c", "pm3/util.c", "cmdhfmfhard.c", "pm3/commonutil.c", "pm3/util_posix.c", "hardnested/hardnested_bf_core.c", "hardnested/hardnested_bruteforce.c", "hardnested/hardnested_bitarray_core.c", "hardnested/tables.c"} {
			files = append(files, filepath.Join(hard, rel))
		}
		binary := build(t, "hardnested", append(files, append(lzma, "-lm")...)...)
		checkInvalid(t, binary)
		tiny := write(t, "tiny.bin", append(append([]byte(nil), header...), make([]byte, 9)...))
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		result, err := RunRecovery(ctx, dir, "hardnested", tiny)
		if err == nil || ctx.Err() != nil || !strings.Contains(result.Stderr, "Insufficient nonce data") {
			t.Fatalf("incomplete capture should fail before the deadline: %v %+v", err, result)
		}
		strip := "-Wl,--gc-sections"
		if runtime.GOOS == "darwin" {
			strip = "-Wl,-dead_strip"
		}
		reader := build(t, "hardnested-reader", append([]string{"-ffunction-sections", "-fdata-sections", strip, "testdata/recovery/hardnested_reader.c"}, append(lzma, "-lm")...)...)
		run(t, reader, 0, filepath.Join(dir, "write-only.txt"))
	})
}
