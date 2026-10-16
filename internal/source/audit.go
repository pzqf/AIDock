package source

import (
	"context"

	"github.com/pzqf/AIDock/internal/auditlog"
	"github.com/pzqf/AIDock/internal/mcpsession"
	"github.com/pzqf/AIDock/internal/pluginpermission"
	"github.com/pzqf/AIDock/internal/runtimecheck"
)

// AuditChains uses only the explicit local binding, so damaged portable source
// cannot hide the history needed for repair. It never probes or opens the vault.
func (m *Manager) AuditChains(ctx context.Context) []auditlog.Chain {
	out := []auditlog.Chain{}
	if n, err := m.originManager(); err != nil {
		out = append(out, auditlog.Unavailable("origin", "source", err))
	} else {
		out = append(out, n.Audit(ctx))
	}
	if n, err := m.installManager(); err != nil {
		out = append(out, auditlog.Unavailable("install", "source", err))
	} else {
		out = append(out, n.Audit(ctx))
	}
	if n, err := m.permissionManager(); err != nil {
		out = append(out, auditlog.Unavailable("permission", "copy", err))
	} else {
		out = append(out, n.Audit(ctx))
	}
	binding := pluginpermission.Binding{Version: 1, Project: m.Owner, Scope: m.Scope, SourceRoot: m.Store.Root, Copy: m.Copy}
	if n, err := runtimecheck.New(m.Store.Local, binding); err != nil {
		out = append(out, auditlog.Unavailable("runtime", "copy", err))
	} else {
		out = append(out, n.Audit(ctx))
	}
	if m.Copy == "" {
		out = append(out, auditlog.Chain{Category: "session", Scope: "copy", Status: "NOT_APPLICABLE", Code: "COPY_BINDING_REQUIRED", Anchor: "UNVERIFIED"})
	} else if n, err := mcpsession.New(m.Store.Local, binding); err != nil {
		out = append(out, auditlog.Unavailable("session", "copy", err))
	} else {
		out = append(out, n.Audit(ctx))
	}
	return out
}
