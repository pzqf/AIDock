package mcpsession

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/mcppolicy"
	"github.com/pzqf/AIDock/internal/mcprelay"
	"github.com/pzqf/AIDock/internal/pluginconfig"
	"github.com/pzqf/AIDock/internal/processgroup"
	"github.com/pzqf/AIDock/internal/runtimecheck"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Process owns only a previously authorized, byte-locked local Node entrypoint.
// Start must run inside the trusted source/permission/credential handoff.
type Process struct {
	command *exec.Cmd
	input   io.WriteCloser
	output  io.ReadCloser
	stop    func() error
}

func Start(ctx context.Context, root string, input Input, env []string) (*Process, error) {
	return StartConfigured(ctx, root, input, env, nil)
}

func StartConfigured(ctx context.Context, root string, input Input, env []string, configuration []byte) (*Process, error) {
	if !validInput(input) || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, invalid()
	}
	if err := pluginconfig.ValidateDelivery(configuration, input.ConfigurationSHA256); err != nil {
		return nil, err
	}
	for _, entry := range env {
		if name, _, ok := strings.Cut(entry, "="); ok && strings.EqualFold(name, pluginconfig.Variable) {
			return nil, invalid()
		}
	}
	executable := filepath.Join(root, filepath.FromSlash(input.RuntimeInput.Runtime.Executable))
	entry := filepath.Join(root, filepath.FromSlash(input.Entrypoint))
	for _, p := range []string{root, executable, entry} {
		if err := workstore.CheckLinks(p); err != nil {
			return nil, invalid()
		}
	}
	base, err := runtimecheck.MinimalEnvironment()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, executable, entry)
	cmd.Dir = root
	cmd.Env = append(base, env...)
	if len(configuration) > 0 {
		cmd.Env = append(cmd.Env, pluginconfig.Variable+"="+string(configuration))
	}
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return nil, err
	}
	stop, err := processgroup.Start(cmd)
	// os/exec already copied the environment at Start; do not retain secret values.
	clear(cmd.Env)
	cmd.Env = nil
	if err != nil {
		in.Close()
		out.Close()
		return nil, err
	}
	return &Process{cmd, in, out, stop}, nil
}
func (p *Process) Close() error {
	_ = p.input.Close()
	_ = p.output.Close()
	err := p.stop()
	_ = p.command.Wait()
	return err
}

// Handshake sends only fixed initialization and tools/list requests through the
// production relay. No business tool is invoked. All metadata is discarded
// after exact policy matching, and only count/digests enter the audit.
func (p *Process) Handshake(ctx context.Context, input Input, policy mcppolicy.Policy, binding mcprelay.Binding, recheck func(context.Context) error, audit func(mcprelay.Event) error) (code string, count int) {
	code = "MCP_SESSION_FAILED"
	auditFailed := false
	client, relaySide := net.Pipe()
	relayCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	defer func() {
		_ = client.Close()
		cancel()
		<-done
		if auditFailed {
			code = "MCP_SESSION_AUDIT_FAILED"
			count = 0
		} else if ctx.Err() == context.DeadlineExceeded {
			code = "MCP_SESSION_TIMEOUT"
			count = 0
		} else if ctx.Err() != nil {
			code = "MCP_SESSION_CANCELED"
			count = 0
		}
		if err := p.Close(); err != nil {
			code = "MCP_SESSION_CLEANUP_FAILED"
			count = 0
		}
	}()
	if recheck == nil || audit == nil || !validInput(input) || input.PolicySHA256 != policy.Digest() || binding.Plugin != input.RuntimeInput.Name {
		done <- invalid()
		return
	}
	approved, err := policy.Relay(binding, recheck, func(e mcprelay.Event) error {
		err := audit(e)
		if err != nil {
			auditFailed = true
		}
		return err
	})
	if err != nil {
		done <- err
		return
	}
	approved.Timeout = 3 * time.Second
	go func() {
		done <- mcprelay.Serve(relayCtx, mcprelay.Endpoint{Reader: relaySide, Writer: relaySide}, mcprelay.Endpoint{Reader: p.output, Writer: p.input}, approved)
	}()
	_ = client.SetDeadline(time.Now().Add(10 * time.Second))
	scanner := bufio.NewScanner(client)
	scanner.Buffer(make([]byte, 4096), mcprelay.MaxFrame+1)
	request := func(id int, method string, params any) (json.RawMessage, bool) {
		if json.NewEncoder(client).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}) != nil || !scanner.Scan() {
			return nil, false
		}
		var response struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &response) != nil || response.ID != id || len(response.Error) > 0 || len(response.Result) == 0 {
			return nil, false
		}
		return response.Result, true
	}
	if _, ok := request(1, "initialize", map[string]string{"protocolVersion": "2025-11-25"}); !ok {
		return
	}
	if json.NewEncoder(client).Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}) != nil {
		return
	}
	raw, ok := request(2, "tools/list", map[string]any{})
	if !ok {
		return
	}
	var list struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(raw, &list) != nil || list.Tools == nil {
		return
	}
	if len(list.Tools) != len(policy.Tools) {
		return "MCP_SESSION_TOOL_MISMATCH", 0
	}
	// The relay already verifies every approved descriptor and rejects duplicates.
	return "MCP_SESSION_MATCH", len(list.Tools)
}
