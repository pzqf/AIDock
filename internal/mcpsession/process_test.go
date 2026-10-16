package mcpsession

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/mcppolicy"
	"github.com/pzqf/AIDock/internal/mcprelay"
)

func TestLockedProcessHandshakeFailureTimeoutAndCleanup(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	binary, err := os.ReadFile(node)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	executable := "node"
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	if err = os.WriteFile(filepath.Join(root, executable), binary, 0755); err != nil {
		t.Fatal(err)
	}
	policy := mcppolicy.Example()
	binding := mcprelay.Binding{Project: "test", Scope: "server", Copy: "copy", Plugin: "fixture", DataLevel: "Public", Destination: "unknown-external-client"}
	normal := `const readline=require('node:readline').createInterface({input:process.stdin});const descriptor=` + string(policy.Tools[0].Descriptor) + `;readline.on('line',l=>{let f=JSON.parse(l);if(!f.id)return;let result=f.method==='initialize'?{protocolVersion:'2025-11-25'}:{tools:[descriptor]};process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:f.id,result})+'\n');});`
	for _, tc := range []struct {
		name, body, want string
		timeout          time.Duration
		failAudit        bool
		deny             bool
	}{
		{name: "matching", body: normal, want: "MCP_SESSION_MATCH"},
		{name: "missing-tool", body: strings.Replace(normal, "tools:[descriptor]", "tools:[]", 1), want: "MCP_SESSION_TOOL_MISMATCH"},
		{name: "changed-descriptor", body: strings.Replace(normal, "tools:[descriptor]", "tools:[{...descriptor,description:'private-sentinel'}]", 1), want: "MCP_SESSION_FAILED"},
		{name: "malformed", body: `process.stdout.write('not-json\n');setInterval(()=>{},1000);`, want: "MCP_SESSION_FAILED"},
		{name: "oversized", body: `process.stdout.write('X'.repeat(1024*1024+10)+'\n');setInterval(()=>{},1000);`, want: "MCP_SESSION_FAILED"},
		{name: "timeout", body: `setInterval(()=>{},1000);`, want: "MCP_SESSION_TIMEOUT", timeout: 300 * time.Millisecond},
		{name: "audit-failure", body: normal, want: "MCP_SESSION_AUDIT_FAILED", failAudit: true},
		{name: "revoked-at-forward", body: normal, want: "MCP_SESSION_FAILED", deny: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, input := fixture(t)
			input.RuntimeInput.Runtime.Executable = executable
			input.Entrypoint = "index.js"
			input.PolicySHA256 = policy.Digest()
			if err := os.WriteFile(filepath.Join(root, input.Entrypoint), []byte(tc.body), 0644); err != nil {
				t.Fatal(err)
			}
			timeout := 5 * time.Second
			if tc.timeout > 0 {
				timeout = tc.timeout
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			process, err := Start(ctx, root, input, nil)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			events := []mcprelay.Event{}
			code, count := process.Handshake(ctx, input, policy, binding, func(context.Context) error {
				calls++
				if tc.deny {
					return invalid()
				}
				return nil
			}, func(e mcprelay.Event) error {
				if tc.failAudit {
					return invalid()
				}
				events = append(events, e)
				return nil
			})
			if code != tc.want {
				t.Fatal(code, count, events)
			}
			if code == "MCP_SESSION_MATCH" && (count != 1 || len(events) != 4 || calls != 4) {
				t.Fatal(count, len(events), calls)
			}
			if process.command.ProcessState == nil || !process.command.ProcessState.Exited() && runtime.GOOS == "windows" {
				t.Fatal("process was not waited")
			}
			raw, _ := json.Marshal(events)
			if strings.Contains(string(raw), "private-sentinel") {
				t.Fatal("raw plugin output entered audit")
			}
		})
	}
	// A child must die with the managed lifetime even if the root completes the handshake.
	marker := filepath.Join(t.TempDir(), "descendant-marker")
	markerJSON, _ := json.Marshal(marker)
	child := `setTimeout(()=>require('node:fs').writeFileSync(` + string(markerJSON) + `,'escaped'),1000);`
	childJSON, _ := json.Marshal(child)
	body := `require('node:child_process').spawn(process.execPath,['-e',` + string(childJSON) + `],{stdio:'ignore'});` + normal
	if err = os.WriteFile(filepath.Join(root, "index.js"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	_, input := fixture(t)
	input.RuntimeInput.Runtime.Executable = executable
	input.Entrypoint = "index.js"
	input.PolicySHA256 = policy.Digest()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	process, err := Start(ctx, root, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := process.Handshake(ctx, input, policy, binding, func(context.Context) error { return nil }, func(mcprelay.Event) error { return nil })
	if code != "MCP_SESSION_MATCH" {
		t.Fatal(code)
	}
	<-time.After(1300 * time.Millisecond)
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("descendant escaped cleanup", err)
	}
}
