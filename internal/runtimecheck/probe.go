package runtimecheck

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pzqf/AIDock/internal/processgroup"
	"github.com/pzqf/AIDock/internal/workstore"
)

type limitedOutput struct {
	mu       sync.Mutex
	b        []byte
	overflow bool
}

func (w *limitedOutput) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(b)
	space := 128 - len(w.b)
	if len(b) > space {
		w.overflow = true
		b = b[:space]
	}
	w.b = append(w.b, b...)
	return n, nil
}

// NodeVersion executes only the already-authorized, rooted runtime with the
// fixed --version argument. It never executes the plugin entrypoint, inherits
// the user's environment, reads the credential vault, or returns raw output.
// Its caller verifies the complete artifact before and after this operation.
func NodeVersion(ctx context.Context, root string, input Input) (string, string) {
	if ctx.Err() != nil {
		return contextCode(ctx), ""
	}
	if !validInput(input) || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "RUNTIME_CHECK_START_FAILED", ""
	}
	executable := filepath.Join(root, filepath.FromSlash(input.Runtime.Executable))
	if err := workstore.CheckLinks(executable); err != nil {
		return "RUNTIME_ARTIFACT_CHANGED", ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "--version")
	cmd.Dir = root
	env, err := baseEnvironment()
	if err != nil {
		return "RUNTIME_CHECK_START_FAILED", ""
	}
	cmd.Env = env
	output := &limitedOutput{}
	cmd.Stdout = output
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	stop, err := processgroup.Start(cmd)
	if err != nil {
		if ctx.Err() != nil {
			return contextCode(ctx), ""
		}
		return "RUNTIME_CHECK_START_FAILED", ""
	}
	waitErr := cmd.Wait()
	if err = stop(); err != nil {
		return "RUNTIME_CHECK_CLEANUP_FAILED", ""
	}
	if ctx.Err() != nil {
		return contextCode(ctx), ""
	}
	if waitErr != nil {
		return "RUNTIME_CHECK_FAILED", ""
	}
	version := strings.TrimSuffix(strings.TrimSuffix(string(output.b), "\n"), "\r")
	if output.overflow || !strings.HasPrefix(version, "v") || !versionPattern.MatchString(version) {
		return "RUNTIME_CHECK_OUTPUT_INVALID", ""
	}
	if version != "v"+strings.TrimPrefix(input.Runtime.Version, "v") {
		return "RUNTIME_VERSION_MISMATCH", version
	}
	return "RUNTIME_VERSION_MATCH", version
}

func contextCode(ctx context.Context) string {
	if ctx.Err() == context.DeadlineExceeded {
		return "RUNTIME_CHECK_TIMEOUT"
	}
	return "RUNTIME_CHECK_CANCELED"
}
