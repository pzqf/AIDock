//go:build !windows

package privatefs

import "os"

func protect(path string) error { return os.Chmod(path, 0700) }
