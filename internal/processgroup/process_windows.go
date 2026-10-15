//go:build windows

package processgroup

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A suspended start prevents descendants escaping before job assignment.
// This is process lifetime control, not a permissions/network sandbox.
// Start starts a process with descendant lifetime control. It is not a permissions sandbox.
func Start(c *exec.Cmd) (func() error, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	var lifecycle sync.Mutex
	active := true
	c.Cancel = func() error {
		lifecycle.Lock()
		defer lifecycle.Unlock()
		if active {
			_ = windows.TerminateJobObject(job, 1)
		}
		return c.Process.Kill()
	}
	if err = c.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	fail := func(err error) (func() error, error) {
		_ = c.Process.Kill()
		_ = c.Wait()
		windows.CloseHandle(job)
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(c.Process.Pid))
	if err != nil {
		return fail(err)
	}
	err = windows.AssignProcessToJobObject(job, process)
	windows.CloseHandle(process)
	if err != nil {
		return fail(err)
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fail(err)
	}
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	resumed := false
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != uint32(c.Process.Pid) {
			continue
		}
		thread, e := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if e != nil {
			err = e
			break
		}
		_, err = windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		resumed = err == nil
		break
	}
	windows.CloseHandle(snapshot)
	if !resumed {
		return fail(fmt.Errorf("managed primary thread resume failed: %v", err))
	}
	return func() error {
		lifecycle.Lock()
		defer lifecycle.Unlock()
		if !active {
			return nil
		}
		active = false
		defer windows.CloseHandle(job)
		if err := windows.TerminateJobObject(job, 1); err != nil {
			return err
		}
		type accounting struct {
			User, Kernel, PeriodUser, PeriodKernel int64
			Faults, Total, Active, Terminated      uint32
		}
		deadline := time.Now().Add(3 * time.Second)
		for {
			var info accounting
			if err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
				return err
			}
			if info.Active == 0 {
				return nil
			}
			if !time.Now().Before(deadline) {
				return fmt.Errorf("managed descendants did not exit")
			}
			time.Sleep(15 * time.Millisecond)
		}
	}, nil
}
