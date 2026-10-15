//go:build !windows

package processgroup

import (
	"os/exec"
	"syscall"
)

// Start starts a process with descendant lifetime control. It is not a permissions sandbox.
func Start(c *exec.Cmd) (func() error, error) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	if err := c.Start(); err != nil {
		return nil, err
	}
	return func() error {
		err := syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return nil
		}
		return err
	}, nil
}
