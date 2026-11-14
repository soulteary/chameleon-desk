package chameleon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRecoveryBoundsInheritedOutputPipes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "exited", true: "cancelled"}[cancel], func(t *testing.T) {
			dir := t.TempDir()
			script := "#!/bin/sh\nsleep 6 &\necho started\necho ready > ready\n"
			if cancel {
				script += "wait\n"
			}
			if err := os.WriteFile(filepath.Join(dir, "staticnested"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			type result struct {
				out RecoveryResult
				err error
			}
			done := make(chan result, 1)
			go func() {
				out, err := runRecoveryIn(ctx, dir, dir, "staticnested", "1", "96", "1", "2")
				done <- result{out, err}
			}()
			readyDeadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
					break
				}
				select {
				case got := <-done:
					t.Fatalf("helper exited before ready: %+v", got)
				default:
				}
				if time.Now().After(readyDeadline) {
					t.Fatal("helper did not become ready")
				}
				time.Sleep(10 * time.Millisecond)
			}
			started := time.Now()
			if cancel {
				stop()
			}
			got := <-done
			out, err := got.out, got.err
			if cancel && !errors.Is(err, context.Canceled) || !cancel && !errors.Is(err, exec.ErrWaitDelay) {
				t.Fatalf("unexpected helper error: %v", err)
			}
			if elapsed := time.Since(started); elapsed > 3*time.Second || out.Stdout != "started\n" {
				t.Fatalf("helper output drain exceeded bound: %s %+v", elapsed, out)
			}
		})
	}
}
