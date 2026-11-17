package chameleon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

var recoveryToolNames = []string{"nested", "staticnested", "darkside", "mfkey32", "mfkey32v2", "mfkey64", "staticnested_1nt", "staticnested_2x1nt_rf08s", "staticnested_2x1nt_rf08s_1key", "mfulc_des_brute", "hardnested"}

func RecoveryTools() []string { return append([]string(nil), recoveryToolNames...) }

type RecoveryResult struct {
	Tool      string `json:"tool"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated,omitempty"`
}

type boundedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > b.limit-b.Len() {
		b.truncated = true
	}
	if b.Len() < b.limit {
		_, _ = b.Buffer.Write(p[:min(len(p), b.limit-b.Len())])
	}
	return n, nil
}

// RunRecovery invokes a pinned upstream C helper, never a shell. The directory
// must be trusted. It is an optional offline dependency, independent of USB.
func RunRecovery(ctx context.Context, directory, tool string, args ...string) (RecoveryResult, error) {
	return runRecoveryIn(ctx, directory, "", tool, args...)
}
func runRecoveryIn(ctx context.Context, directory, workspace, tool string, args ...string) (RecoveryResult, error) {
	ok := false
	for _, n := range recoveryToolNames {
		if n == tool {
			ok = true
		}
	}
	if !ok {
		return RecoveryResult{}, fmt.Errorf("unknown recovery tool %q", tool)
	}
	if e := validateRecoveryArgs(tool, args); e != nil {
		return RecoveryResult{Tool: tool}, e
	}
	if e := ctx.Err(); e != nil {
		return RecoveryResult{Tool: tool}, e
	}
	if e := DiagnoseRecovery(directory).Require(tool); e != nil {
		return RecoveryResult{Tool: tool}, e
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	path, e := filepath.Abs(filepath.Join(directory, tool+suffix))
	if e != nil {
		return RecoveryResult{}, e
	}
	work := workspace
	if tool == "hardnested" && workspace == "" {
		input, e := filepath.Abs(args[0])
		if e != nil {
			return RecoveryResult{}, e
		}
		args = []string{input}
		work, e = os.MkdirTemp("", "chameleon-recovery-*")
		if e != nil {
			return RecoveryResult{}, e
		}
		defer os.RemoveAll(work)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	// Bound pipe draining if a helper exits but a descendant retains its output.
	cmd.WaitDelay = time.Second
	if work != "" {
		cmd.Dir = work
	}
	out, errout := &boundedBuffer{limit: 2 << 20}, &boundedBuffer{limit: 2 << 20}
	cmd.Stdout = out
	cmd.Stderr = errout
	e = cmd.Run()
	if ctx.Err() != nil {
		e = ctx.Err()
	}
	truncated := out.truncated || errout.truncated
	if truncated {
		e = errors.Join(e, fmt.Errorf("recovery output exceeded capture limit"))
	}
	return RecoveryResult{tool, out.String(), errout.String(), truncated}, e
}
