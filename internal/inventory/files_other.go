//go:build !windows

package inventory

import (
	"fmt"
	"os"
	"runtime"
)

func isLink(info os.FileInfo) bool { return info.Mode()&os.ModeSymlink != 0 }

func checkHandle(root string, f *os.File) error {
	if runtime.GOOS == "linux" {
		path, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", f.Fd()))
		if err != nil {
			return err
		}
		if !inside(root, path) {
			return fmt.Errorf("打开的文件句柄越出声明根")
		}
	}
	return nil
}
