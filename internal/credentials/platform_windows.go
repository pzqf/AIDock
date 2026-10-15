//go:build windows

package credentials

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var advapi = windows.NewLazySystemDLL("advapi32.dll")
var credRead = advapi.NewProc("CredReadW")
var credWrite = advapi.NewProc("CredWriteW")
var credDelete = advapi.NewProc("CredDeleteW")
var credEnumerate = advapi.NewProc("CredEnumerateW")
var credFree = advapi.NewProc("CredFree")

// Native CREDENTIALW layout; pointer alignment is supplied by Go on each arch.
type credential struct {
	Flags, Type             uint32
	TargetName, Comment     *uint16
	LastWritten             windows.Filetime
	CredentialBlobSize      uint32
	CredentialBlob          *byte
	Persist, AttributeCount uint32
	Attributes              unsafe.Pointer
	TargetAlias, UserName   *uint16
}
type windowsBackend struct{ prefix, mutex string }

func newBackend(ns string) backend {
	// The vault is per user and shared across login sessions. A Global mutex,
	// qualified by that user's SID, serializes the same vault in those sessions.
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return &windowsBackend{}
	}
	id := sha256.Sum256([]byte(user.User.Sid.String()))
	return &windowsBackend{prefix: "AIDock/v1/" + ns + "/", mutex: `Global\AIDockCredentialV1_` + hex.EncodeToString(id[:16]) + "_" + ns}
}
func (b *windowsBackend) available() bool { return b.prefix != "" }
func (b *windowsBackend) locked(ctx context.Context, fn func() error) error {
	if !b.available() {
		return fail("CREDENTIAL_VAULT_UNAVAILABLE", "无法确认当前 Windows 用户")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread() // Windows mutex ownership is thread-specific.
	name, _ := windows.UTF16PtrFromString(b.mutex)
	h, e := windows.CreateMutex(nil, false, name)
	if e != nil && !errors.Is(e, windows.ERROR_ALREADY_EXISTS) {
		return fail("CREDENTIAL_VAULT_UNAVAILABLE", "不能取得系统凭据库锁")
	}
	defer windows.CloseHandle(h)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		status, err := windows.WaitForSingleObject(h, 100)
		if err != nil {
			return fail("CREDENTIAL_VAULT_UNAVAILABLE", "系统凭据库锁不可用")
		}
		if status == windows.WAIT_OBJECT_0 || status == windows.WAIT_ABANDONED {
			defer windows.ReleaseMutex(h)
			return fn()
		}
		if time.Now().After(deadline) {
			return fail("CREDENTIAL_BUSY", "其它进程正在使用此凭据库；稍后重试")
		}
	}
}
func nativeError(err error) error {
	if errors.Is(err, syscall.Errno(1168)) {
		return errMissing
	}
	return fail("CREDENTIAL_VAULT_UNAVAILABLE", "Windows 凭据库访问失败；没有写入文件备份")
}
func (b *windowsBackend) entry(c *credential) (Entry, error) {
	name := windows.UTF16PtrToString(c.TargetName)
	revision := strings.TrimPrefix(windows.UTF16PtrToString(c.Comment), "aidock-v1:")
	if c.Type != 1 || !strings.HasPrefix(name, b.prefix) || !ValidName(strings.TrimPrefix(name, b.prefix)) || !revisionPattern.MatchString(revision) || windows.UTF16PtrToString(c.Comment) != "aidock-v1:"+revision {
		return Entry{}, fail("CREDENTIAL_OWNERSHIP_CONFLICT", "系统凭据项归属或修订无效；保留并阻断")
	}
	return Entry{Name: strings.TrimPrefix(name, b.prefix), Revision: revision, Updated: time.Unix(0, c.LastWritten.Nanoseconds()).UTC().Format(time.RFC3339Nano)}, nil
}
func freeCredential(c *credential) {
	if c != nil && c.CredentialBlob != nil && c.CredentialBlobSize <= 2560 {
		clear(unsafe.Slice(c.CredentialBlob, int(c.CredentialBlobSize)))
	}
	credFree.Call(uintptr(unsafe.Pointer(c)))
}
func (b *windowsBackend) read(name string) (Entry, []byte, error) {
	target, _ := windows.UTF16PtrFromString(b.prefix + name)
	var c *credential
	ok, _, err := credRead.Call(uintptr(unsafe.Pointer(target)), 1, 0, uintptr(unsafe.Pointer(&c)))
	if ok == 0 {
		return Entry{}, nil, nativeError(err)
	}
	defer freeCredential(c)
	e, err := b.entry(c)
	if err != nil {
		return Entry{}, nil, err
	}
	if c.CredentialBlobSize == 0 || c.CredentialBlobSize > MaxValue || c.CredentialBlob == nil {
		return Entry{}, nil, fail("CREDENTIAL_OWNERSHIP_CONFLICT", "系统凭据值格式无效")
	}
	return e, append([]byte(nil), unsafe.Slice(c.CredentialBlob, int(c.CredentialBlobSize))...), nil
}
func (b *windowsBackend) list() ([]Entry, error) {
	filter, _ := windows.UTF16PtrFromString(b.prefix + "*")
	var count uint32
	var values **credential
	ok, _, err := credEnumerate.Call(uintptr(unsafe.Pointer(filter)), 0, uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&values)))
	if ok == 0 {
		if errors.Is(err, syscall.Errno(1168)) {
			return []Entry{}, nil
		}
		return nil, nativeError(err)
	}
	defer func() {
		for _, c := range unsafe.Slice(values, int(count)) {
			if c != nil && c.CredentialBlob != nil && c.CredentialBlobSize <= 2560 {
				clear(unsafe.Slice(c.CredentialBlob, int(c.CredentialBlobSize)))
			}
		}
		credFree.Call(uintptr(unsafe.Pointer(values)))
	}()
	out := []Entry{}
	for _, c := range unsafe.Slice(values, int(count)) {
		e, err := b.entry(c)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (b *windowsBackend) write(e Entry, value []byte) error {
	target, _ := windows.UTF16PtrFromString(b.prefix + e.Name)
	comment, _ := windows.UTF16PtrFromString("aidock-v1:" + e.Revision)
	c := credential{Type: 1, TargetName: target, Comment: comment, CredentialBlobSize: uint32(len(value)), CredentialBlob: &value[0], Persist: 2}
	ok, _, err := credWrite.Call(uintptr(unsafe.Pointer(&c)), 0)
	runtime.KeepAlive(value)
	if ok == 0 {
		return nativeError(err)
	}
	return nil
}
func (b *windowsBackend) remove(name string) error {
	target, _ := windows.UTF16PtrFromString(b.prefix + name)
	ok, _, err := credDelete.Call(uintptr(unsafe.Pointer(target)), 1, 0)
	if ok == 0 {
		return nativeError(err)
	}
	return nil
}
