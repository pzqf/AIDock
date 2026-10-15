//go:build windows

package native

import (
	"github.com/pzqf/AIDock/internal/processgroup"
	"os/exec"
)

func startProbe(c *exec.Cmd) (func() error, error) { return processgroup.Start(c) }
