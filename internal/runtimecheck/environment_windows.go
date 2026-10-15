//go:build windows

package runtimecheck

import "golang.org/x/sys/windows"

// Resolve the OS directory through the Windows API, never inherited SystemRoot.
func baseEnvironment() ([]string, error) {
	root, err := windows.GetWindowsDirectory()
	if err != nil {
		return nil, err
	}
	return []string{"SystemRoot=" + root}, nil
}
