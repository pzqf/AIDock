// Package privatefs protects local backups which can contain original native
// configuration values. Such bytes must never enter source or export stores.
package privatefs

import (
	"os"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Ensure is only for an application's dedicated private directory. Call it
// before creating any children or writing backup bytes, and before reuse.
func Ensure(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return result.Fail("PRIVATE_BACKUP_UNAVAILABLE", "恢复资产需要明确的本机私有目录", 4)
	}
	if err := workstore.CheckLinks(path); err != nil {
		return result.Fail("PRIVATE_BACKUP_UNAVAILABLE", "恢复资产目录不能包含链接或重解析点", 4)
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return result.Fail("PRIVATE_BACKUP_UNAVAILABLE", "无法创建本机私有恢复目录", 4)
	}
	if err := protect(path); err != nil {
		return result.Fail("PRIVATE_BACKUP_UNAVAILABLE", "无法限制恢复目录访问；未读取或备份原生配置正文", 4)
	}
	return nil
}
