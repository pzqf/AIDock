//go:build !windows

package isolation

import (
	"context"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
	"os"
	"strconv"
)

func platformInfo() Platform {
	host, _ := os.Hostname()
	return Platform{Kernel: "unsupported", Host: workstore.Hash([]byte(host + "/" + strconv.Itoa(os.Getuid())))}
}
func probePlatform(context.Context, string, Platform) ([]Finding, bool) {
	return []Finding{{"OS_BOUNDARY", "UNAVAILABLE", "此平台未实现 OS 隔离原型"}}, true
}
func appContainerToken() bool                          { return false }
func denied(err error) bool                            { return false }
func childDenied(err error) bool                       { return false }
func checkProbePath(path string) error                 { return workstore.CheckLinks(path) }
func lockPlatform(local string, fn func() error) error { return fn() }
func recoverPlatform(p Pending) error {
	return result.Fail("ISOLATION_RECOVERY_UNAVAILABLE", "此平台不能恢复 Windows 隔离资源", 4)
}
