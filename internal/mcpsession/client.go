package mcpsession

import (
	"context"
	"errors"
	"time"

	"github.com/pzqf/AIDock/internal/mcppolicy"
	"github.com/pzqf/AIDock/internal/mcprelay"
)

// Serve attaches a real client to this locked process. The authority gate must
// reacquire current source/approval locks for each bounded protocol transfer.
// It must release them while waiting for a response so revocation can proceed.
func (p *Process) Serve(ctx context.Context, input Input, policy mcppolicy.Policy, binding mcprelay.Binding, client mcprelay.Endpoint, guard func(context.Context, func() error) error, audit func(mcprelay.Event) error) (code string, count int) {
	code = "MCP_CLIENT_FAILED"
	defer func() {
		if err := p.Close(); err != nil {
			code = "MCP_SESSION_CLEANUP_FAILED"
		}
	}()
	if input.Mode != "stdio" || !validInput(input) || guard == nil || audit == nil || policy.Digest() != input.PolicySHA256 || binding.Plugin != input.RuntimeInput.Name {
		return
	}
	auditFailed := false
	approved, err := policy.Relay(binding, func(c context.Context) error {
		return guard(c, func() error { return nil })
	}, func(e mcprelay.Event) error {
		err := audit(e)
		if err != nil {
			auditFailed = true
		}
		return err
	})
	if err != nil {
		return
	}
	approved.Guard = guard
	approved.StopOnRevocation = true
	approved.Timeout = 10 * time.Second
	err = mcprelay.Serve(ctx, client, mcprelay.Endpoint{Reader: p.output, Writer: p.input}, approved)
	switch {
	case auditFailed:
		code = "MCP_SESSION_AUDIT_FAILED"
	case ctx.Err() != nil:
		code = "MCP_CLIENT_CANCELED"
	case err == nil:
		code = "MCP_CLIENT_CLOSED"
	default:
		var classified *mcprelay.Error
		if errors.As(err, &classified) && classified.Code == "AUTHORIZATION_STALE" {
			code = "MCP_CLIENT_REVOKED"
		}
	}
	return
}
