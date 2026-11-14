package chameleon

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRecoveryHelperFailureStatus(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("requires a POSIX sanitizer compiler")
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("requires a C compiler")
	}
	src, err := filepath.Abs("third_party/chameleon/src")
	if err != nil {
		t.Fatal(err)
	}
	buildDir := t.TempDir()
	build := func(name string, files ...string) string {
		t.Helper()
		binary := filepath.Join(buildDir, name)
		args := []string{"-O1", "-g", "-D_GNU_SOURCE", "-fsanitize=address", "-fno-omit-frame-pointer", "-I" + src, "-o", binary}
		args = append(args, files...)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if output, err := exec.CommandContext(ctx, cc, args...).CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", name, err, output)
		}
		return binary
	}
	run := func(binary, dir string, failure, want int, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, binary, args...)
		command.Dir = dir
		leaks := "1"
		if runtime.GOOS == "darwin" {
			leaks = "0"
		}
		command.Env = append(os.Environ(), fmt.Sprintf("RECOVERY_FAIL_AT=%d", failure), "ASAN_OPTIONS=detect_leaks="+leaks+":halt_on_error=1:exitcode=97", "LSAN_OPTIONS=exitcode=98")
		output, err := command.CombinedOutput()
		actual := 0
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("run: %v\n%s", err, output)
			}
			actual = exit.ExitCode()
		}
		if ctx.Err() != nil || actual != want || strings.Contains(string(output), "Sanitizer") {
			t.Fatalf("%s failure %d: exit %d, want %d: %v\n%s", filepath.Base(binary), failure, actual, want, err, output)
		}
		return string(output)
	}
	common := []string{filepath.Join(src, "crapto1.c"), filepath.Join(src, "crypto1.c"), filepath.Join(src, "bucketsort.c"), filepath.Join(src, "parity.c")}
	vectors := [][]string{
		{"11223344", "12345678", "01020304", "8177993d", "05060708", "02ac0c37"},
		{"11223344", "12345678", "01020304", "8177993d", "87654321", "05060708", "fa43d1e6"},
	}
	for i, name := range []string{"mfkey32", "mfkey32v2"} {
		binary := build(name, append([]string{filepath.Join(src, name+".c")}, common...)...)
		if output := run(binary, t.TempDir(), 0, 0, vectors[i]...); !strings.Contains(output, "Found Key: [a0a1a2a3a4a5]") {
			t.Fatalf("%s known vector changed: %s", name, output)
		}
		files := []string{"-DRECOVERY_ALLOC_COMMAND", filepath.Join(src, name+".c"), "testdata/recovery/recovery32_allocations.c", filepath.Join(src, "crypto1.c"), filepath.Join(src, "bucketsort.c"), filepath.Join(src, "parity.c")}
		failed := build(name+"-failed", files...)
		if output := run(failed, t.TempDir(), 0, 1, vectors[i]...); !strings.Contains(output, "Key recovery failed:") || strings.Contains(output, "Found Key:") {
			t.Fatalf("allocation failure reported as recovery: %s", output)
		}
	}
	write := func(dir, name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sentinel := "existing dictionary must survive\n"
	preserved := func(dir string, names ...string) {
		t.Helper()
		for _, name := range names {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil || string(data) != sentinel {
				t.Fatalf("changed %s after failure: %q %v", name, data, err)
			}
		}
	}
	input1, input2 := "keys_11223344_01_12345678.dic", "keys_11223344_01_12345679.dic"
	output1, output2 := "keys_11223344_01_12345678_filtered.dic", "keys_11223344_01_12345679_filtered.dic"
	for helper := 3; helper <= 5; helper++ {
		name := fmt.Sprintf("helper-%d", helper)
		files := []string{fmt.Sprintf("-DHELPER=%d", helper), "testdata/recovery/helper_failures.c"}
		if helper == 3 {
			files = append(files, common...)
		}
		binary := build(name, files...)
		args := []string{"11223344", "1", "12345678", "87654321", "1110"}
		outputs := []string{input1}
		maxFailure := 3
		if helper == 4 {
			args = []string{input1, input2}
			outputs = []string{output1, output2}
			maxFailure = 5
		}
		if helper == 5 {
			args = []string{"12345678", "a0a1a2a3a4a5", input2}
			outputs = nil
			maxFailure = 1
		}
		for failure := 1; failure <= maxFailure; failure++ {
			dir := t.TempDir()
			if helper >= 4 {
				write(dir, input1, "a0a1a2a3a4a5\n")
				write(dir, input2, "ffffffffffff\n")
			}
			for _, name := range outputs {
				write(dir, name, sentinel)
			}
			output := run(binary, dir, failure, 1, args...)
			preserved(dir, outputs...)
			if strings.Contains(output, "Finding phase complete") || strings.Contains(output, "keys saved") {
				t.Fatalf("helper %d announced success on allocation failure", helper)
			}
		}
		if helper == 3 || helper == 4 {
			for _, stage := range []string{"open", "write", "close"} {
				t.Run(fmt.Sprintf("helper-%d-output-%s", helper, stage), func(t *testing.T) {
					t.Setenv("RECOVERY_IO_FAILURE", stage)
					dir := t.TempDir()
					if helper == 4 {
						write(dir, input1, "a0a1a2a3a4a5\n")
						write(dir, input2, "a0a1a2a3a4a5\n")
					}
					run(binary, dir, 0, 1, args...)
				})
			}
		}
		if helper >= 4 {
			for _, input := range []string{"", "a0a1a2a3a4a5\n"} {
				dir := t.TempDir()
				write(dir, input1, input)
				write(dir, input2, input)
				run(binary, dir, 0, 0, args...)
				if helper == 4 {
					for _, name := range outputs {
						data, err := os.ReadFile(filepath.Join(dir, name))
						if err != nil || string(data) != input {
							t.Fatalf("filter changed valid dictionary %s: %q %v", name, data, err)
						}
					}
				}
			}
			for _, malformed := range []string{"not-a-key\n", "a0a1a2a3a4a5\n123\n", "0000000000000\n", "a0a1a2a3a4ag\n"} {
				dir := t.TempDir()
				write(dir, input1, "a0a1a2a3a4a5\n")
				write(dir, input2, malformed)
				for _, name := range outputs {
					write(dir, name, sentinel)
				}
				run(binary, dir, 0, 1, args...)
				preserved(dir, outputs...)
			}
			dir := t.TempDir()
			run(binary, dir, 0, 1, args...) // Missing input must not mean an empty dictionary.
		}
	}
	actual := build("staticnested-1nt", append([]string{filepath.Join(src, "staticnested_1nt.c")}, common...)...)
	actualDir := t.TempDir()
	output := run(actual, actualDir, 0, 0, "a13e4902", "15", "d14191b3", "2e9e49fc", "1111")
	data, err := os.ReadFile(filepath.Join(actualDir, "keys_a13e4902_15_d14191b3.dic"))
	if err != nil || !strings.Contains(output, "found 44075 keys") || fmt.Sprintf("%x", sha256.Sum256(data)) != "83df2ef799bb664b6b88e44f26c2e4e0bd141a9c3327ec0af451b2873c0a3ebc" {
		t.Fatalf("one-nonce known dictionary changed: %v\n%s", err, output)
	}
	// An actual recovery32 allocation failure must also preserve the one-nonce dictionary.
	files := []string{"-DRECOVERY_ALLOC_COMMAND", filepath.Join(src, "staticnested_1nt.c"), "testdata/recovery/recovery32_allocations.c", filepath.Join(src, "crypto1.c"), filepath.Join(src, "bucketsort.c"), filepath.Join(src, "parity.c")}
	binary := build("staticnested-1nt-recovery-failed", files...)
	dir := t.TempDir()
	write(dir, input1, sentinel)
	run(binary, dir, 0, 1, "11223344", "1", "12345678", "87654321", "1110")
	preserved(dir, input1)
}
