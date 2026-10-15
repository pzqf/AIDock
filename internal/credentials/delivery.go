package credentials

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/pzqf/AIDock/internal/secref"
)

// Delivery is supplied only by the internal launch authorization gate. It does
// not grant execution or isolation. Digests identify metadata, never secret values.
type Delivery struct {
	BindingSHA256       string `json:"binding_sha256"`
	AuthorizationSHA256 string `json:"authorization_sha256"`
	InputSHA256         string `json:"input_sha256"`
}

func (d Delivery) valid() bool {
	for _, s := range []string{d.BindingSHA256, d.AuthorizationSHA256, d.InputSHA256} {
		if len(s) != 64 || strings.Trim(s, "0123456789abcdef") != "" {
			return false
		}
	}
	return true
}

// WithEnvironment reads only requested names while the vault mutex is held.
// The source caller must hold its current permission lock through this callback.
// AUTHORIZED is written before disclosure; it does not assert process start or
// successful consumption. A partial audit append never invokes the callback.
// No CLI/HTTP endpoint exposes this method. The callback is a short launch step,
// must not retain values or re-enter the vault, and its diagnostics are redacted.
func (m *Manager) WithEnvironment(ctx context.Context, d Delivery, refs []secref.Reference, consume func([]string) error) error {
	if !d.valid() || consume == nil || len(refs) > 64 {
		return fail("CREDENTIAL_DELIVERY_INVALID", "凭据交付需要完整授权绑定与有效回调")
	}
	seen := map[string]bool{}
	for _, r := range refs {
		if !secref.ValidName(r.Name) || !secref.ValidVariable(r.Variable) || seen[r.Variable] {
			return fail("CREDENTIAL_DELIVERY_INVALID", "凭据交付映射无效")
		}
		seen[r.Variable] = true
	}
	if len(refs) == 0 {
		return consumeSafe(ctx, consume, []string{})
	}
	if !m.store.available() {
		return fail("CREDENTIAL_VAULT_UNAVAILABLE", "系统凭据库不可用；未交付")
	}
	return m.store.locked(ctx, func() error {
		events, err := m.audit()
		if err != nil {
			return err
		}
		if len(pending(events)) > 0 {
			return fail("CREDENTIAL_RECOVERY_REQUIRED", "先核对未完成的密钥操作；未交付")
		}
		names := secref.Names(refs)
		if len(events)+len(names) > 10000 {
			return fail("CREDENTIAL_AUDIT_UNAVAILABLE", "审计容量不足；未交付")
		}
		values := map[string][]byte{}
		entries := map[string]Entry{}
		defer func() {
			for _, b := range values {
				wipe(b)
			}
		}()
		for _, name := range names {
			entry, value, e := m.store.read(name)
			if e != nil {
				wipe(value)
				if errors.Is(e, errMissing) {
					return fail("CREDENTIAL_MISSING", "请求的密钥不存在；未交付")
				}
				return fail("CREDENTIAL_READ_FAILED", "系统凭据读取失败；未交付")
			}
			values[name] = value
			entries[name] = entry
			if entry.Name != name || !revisionPattern.MatchString(entry.Revision) || len(value) == 0 || len(value) > MaxValue || !utf8.Valid(value) || strings.ContainsRune(string(value), 0) {
				return fail("CREDENTIAL_INVALID", "系统凭据格式无效；未交付")
			}
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		for _, name := range names {
			a := Event{ID: randomID(), Action: "inject", Name: name, After: entries[name].Revision, Outcome: "AUTHORIZED", Delivery: &d}
			if err = m.append(events, a); err != nil {
				return err
			}
			events = append(events, a)
		}
		env := make([]string, 0, len(refs))
		for _, r := range refs {
			env = append(env, r.Variable+"="+string(values[r.Name]))
		}
		defer clear(env)
		return consumeSafe(ctx, consume, env)
	})
}
func consumeSafe(ctx context.Context, consume func([]string) error, env []string) (err error) {
	defer func() {
		if recover() != nil {
			err = fail("CREDENTIAL_CONSUMER_FAILED", "凭据交付后的内部启动步骤失败；诊断已遮蔽")
		}
	}()
	if err = ctx.Err(); err != nil {
		return err
	}
	if consume(env) != nil {
		return fail("CREDENTIAL_CONSUMER_FAILED", "凭据交付后的内部启动步骤失败；诊断已遮蔽")
	}
	return nil
}
