//go:build !windows

package credentials

import "context"

type unavailable struct{}

func newBackend(string) backend     { return unavailable{} }
func (unavailable) available() bool { return false }
func (unavailable) locked(context.Context, func() error) error {
	return fail("CREDENTIAL_VAULT_UNAVAILABLE", "系统凭据库不可用；不会降级写文件")
}
func (unavailable) list() ([]Entry, error) {
	return nil, fail("CREDENTIAL_VAULT_UNAVAILABLE", "系统凭据库不可用")
}
func (unavailable) read(string) (Entry, []byte, error) {
	return Entry{}, nil, fail("CREDENTIAL_VAULT_UNAVAILABLE", "系统凭据库不可用")
}
func (unavailable) write(Entry, []byte) error {
	return fail("CREDENTIAL_VAULT_UNAVAILABLE", "系统凭据库不可用")
}
func (unavailable) remove(string) error {
	return fail("CREDENTIAL_VAULT_UNAVAILABLE", "系统凭据库不可用")
}
