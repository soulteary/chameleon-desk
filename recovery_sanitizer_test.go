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

func TestRecoveryHelpersMemorySafety(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("sanitizer regression requires a POSIX compiler")
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("sanitizer regression requires a C compiler")
	}
	src := filepath.Join("third_party", "chameleon", "src")
	dir := t.TempDir()
	build := func(name string, files ...string) string {
		t.Helper()
		out := filepath.Join(dir, name)
		args := []string{"-O1", "-g", "-D_GNU_SOURCE", "-fsanitize=address", "-fno-omit-frame-pointer", "-pthread", "-I" + src, "-o", out}
		args = append(args, files...)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if output, err := exec.CommandContext(ctx, cc, args...).CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", name, err, output)
		}
		return out
	}
	run := func(binary string, exitCode int, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		leaks := "1"
		if runtime.GOOS == "darwin" {
			leaks = "0" // LeakSanitizer is unavailable on Darwin.
		}
		cmd.Env = append(os.Environ(), "ASAN_OPTIONS=detect_leaks="+leaks+":halt_on_error=1:exitcode=97", "LSAN_OPTIONS=exitcode=98")
		output, err := cmd.CombinedOutput()
		actual := 0
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("run %s: %v\n%s", binary, err, output)
			}
			actual = exit.ExitCode()
		}
		if ctx.Err() != nil || actual != exitCode || strings.Contains(string(output), "AddressSanitizer") || strings.Contains(string(output), "LeakSanitizer") {
			t.Fatalf("run %s: exit %d, expected %d: %v\n%s", filepath.Base(binary), actual, exitCode, err, output)
		}
		return string(output)
	}

	harness := build("nested-candidates", "testdata/recovery/nested_candidates.c", filepath.Join(src, "parity.c"))
	run(harness, 0)
	buckets := build("hardnested-buckets", "testdata/recovery/hardnested_buckets.c", filepath.Join(src, "parity.c"), "-lm")
	run(buckets, 0)
	allocationFiles := []string{"testdata/recovery/recovery32_allocations.c", filepath.Join(src, "crypto1.c"), filepath.Join(src, "bucketsort.c"), filepath.Join(src, "parity.c")}
	allocations := build("recovery32-allocations", allocationFiles...)
	run(allocations, 0)
	allocationCommand := append([]string{"-DRECOVERY_ALLOC_COMMAND", filepath.Join(src, "staticnested.c"), filepath.Join(src, "nested_util.c"), filepath.Join(src, "common.c")}, allocationFiles...)
	failedStatic := build("staticnested-allocation-failure", allocationCommand...)
	if output := run(failedStatic, 1, "1", "96", "18874693", "12345678"); !strings.Contains(output, "Nested recovery failed:") {
		t.Fatal("recovery allocation failure was reported as an empty successful result")
	}
	common := []string{filepath.Join(src, "crapto1.c"), filepath.Join(src, "crypto1.c"), filepath.Join(src, "bucketsort.c"), filepath.Join(src, "parity.c")}
	staticFiles := append([]string{filepath.Join(src, "staticnested.c"), filepath.Join(src, "nested_util.c"), filepath.Join(src, "common.c")}, common...)
	static := build("staticnested", staticFiles...)
	if output := run(static, 0, "1", "96", "18874693", "12345678"); strings.Contains(output, "Key ") {
		t.Fatal("single sample produced a repeated candidate")
	}
	run(static, 1, "1", "96", "1", "2")
	for _, tool := range []struct {
		name string
		args []string
	}{
		{"nested", []string{"1", "160", "1", "0", "0"}},
		{"staticnested", []string{"1", "96", "18874693", "12345678"}},
	} {
		files := []string{filepath.Join(src, tool.name+".c"), filepath.Join(src, "nested_util.c"), filepath.Join(src, "common.c"), filepath.Join(src, "parity.c"), "testdata/recovery/nested_failures.c"}
		for _, fault := range []string{"allocation", "thread"} {
			inputs := append([]string(nil), files...)
			if fault == "thread" {
				inputs = append(inputs, "-Dpthread_create=recovery_pthread_create")
			}
			command := build(tool.name+"-"+fault+"-failure", inputs...)
			if output := run(command, 1, tool.args...); !strings.Contains(output, "Nested recovery failed:") || strings.Contains(output, "Key ") {
				t.Fatalf("%s %s failure was reported as successful recovery", tool.name, fault)
			}
		}
	}

	mfkey := build("mfkey64", append([]string{filepath.Join(src, "mfkey64.c")}, common...)...)
	vector := []string{"65535D33", "BE2B7B5D", "B1E1B891", "2CF7A248", "36081500"}
	for _, extra := range [][]string{nil, {""}, {strings.Repeat("00", 2000)}, {"00", strings.Repeat("Ab", 2048)}} {
		args := append(append([]string(nil), vector...), extra...)
		if output := run(mfkey, 0, args...); !strings.Contains(strings.ToLower(output), "974c262b9278") {
			t.Fatal("known recovery key changed")
		}
	}
	for _, extra := range [][]string{{"0"}, {"0g"}, {"ff", "aaZz"}, {"00", "1"}} {
		run(mfkey, 1, append(append([]string(nil), vector...), extra...)...)
	}
	bad := append([]string(nil), vector...)
	bad[0] = "nothex"
	run(mfkey, 1, bad...)
	if output := run(mfkey, 1, "1", "2", "3", "4", "5"); !strings.Contains(output, "No recovery state found") || strings.Contains(output, "Found Key:") {
		t.Fatal("missing recovery state was reported as a key")
	}
	ownership := build("darkside-ownership", "testdata/recovery/darkside_ownership.c", filepath.Join(src, "common.c"))
	run(ownership, 0)
	darksideFiles := append([]string{filepath.Join(src, "darkside.c"), filepath.Join(src, "mfkey.c"), filepath.Join(src, "common.c")}, common...)
	darkside := build("darkside", darksideFiles...)
	if output := run(darkside, 0, "1", "2", "0", "0", "3", "4", "2", "0", "0", "3", "4"); !strings.Contains(output, "key not found") || strings.Contains(output, "Key1:") {
		t.Fatal("empty darkside captures were reported as recovered keys")
	}
}
