//go:build !windows

package runtimecheck

func baseEnvironment() ([]string, error) { return []string{}, nil }
