package native

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func claudeRuleResponse(id string, payload any) string {
	data, err := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id, "response": payload}})
	if err != nil {
		panic(err)
	}
	return string(data) + "\n"
}

func TestClaudeRuleProtocolOnlyInitializesAndRequestsLocalSummary(t *testing.T) {
	response := claudeRuleResponse("aidock-rule-init", map[string]any{}) + claudeRuleResponse("aidock-rule-context", map[string]any{"memoryFiles": []any{map[string]any{"path": "CLAUDE.md", "type": "Project", "tokens": 42}}})
	var requests bytes.Buffer
	sources, err := claudeRuleProtocol(&requests, strings.NewReader(response))
	if err != nil || len(sources) != 1 || sources[0] != "CLAUDE.md" {
		t.Fatal(sources, err)
	}
	frames := strings.Split(strings.TrimSpace(requests.String()), "\n")
	if len(frames) != 2 {
		t.Fatal("unexpected turn or protocol request", requests.String())
	}
	for i, frame := range frames {
		var req struct {
			Type    string            `json:"type"`
			Request map[string]string `json:"request"`
		}
		if err = json.Unmarshal([]byte(frame), &req); err != nil || req.Type != "control_request" {
			t.Fatal(req, err)
		}
		if i == 0 && req.Request["subtype"] != "initialize" || i == 1 && (req.Request["subtype"] != "get_context_usage" || req.Request["detail"] != "summary") {
			t.Fatal("must never request a full token count or model turn", req)
		}
	}
}

func TestClaudeRuleProtocolRejectsIncompleteOutOfOrderAndUnexpectedSources(t *testing.T) {
	init := claudeRuleResponse("aidock-rule-init", map[string]any{})
	for name, response := range map[string]string{
		"invalid-json":   "not JSON\n",
		"empty":          "",
		"denied":         `{"type":"control_response","response":{"subtype":"error","request_id":"aidock-rule-init","error":"PRIVATE"}}` + "\n",
		"out-of-order":   claudeRuleResponse("aidock-rule-context", map[string]any{"memoryFiles": []any{}}),
		"duplicate-init": init + init,
		"missing-list":   init + claudeRuleResponse("aidock-rule-context", map[string]any{}),
		"wrong-list":     init + claudeRuleResponse("aidock-rule-context", map[string]any{"memoryFiles": "PRIVATE"}),
		"other-source":   init + claudeRuleResponse("aidock-rule-context", map[string]any{"memoryFiles": []any{map[string]any{"path": "PRIVATE", "type": "Managed"}}}),
		"unknown-id":     init + claudeRuleResponse("other-context", map[string]any{"memoryFiles": []any{}}),
		"too-large":      strings.Repeat("x", 2<<20) + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := claudeRuleProtocol(&bytes.Buffer{}, strings.NewReader(response))
			if err == nil || strings.Contains(probeFailure(err), "PRIVATE") {
				t.Fatal("invalid protocol accepted or payload leaked", err)
			}
		})
	}
}

func TestClaudeRulesQualifiedJointInstallAndImportGuard(t *testing.T) {
	m, _ := writableFixture(t)
	fakeProbes(m, discoveredGuide)
	m.probe.claudeRules = func(_ context.Context, _ toolBinary, cwd, _ string) ([]string, error) {
		return []string{filepath.Join(cwd, "CLAUDE.md")}, nil
	}
	saveRules(t, m, []any{"Use project conventions."})
	sourceBefore := observe(t, m, m.Source.Store.Root)
	manual := []byte("\xef\xbb\xbf# Handwritten\r\n\r\n")
	write(t, filepath.Join(m.Root, "CLAUDE.md"), manual)
	ctx := context.Background()
	check, err := m.Check(ctx, CheckRequest{Tools: []string{"claude-code"}, Probe: true, RuleProbe: true})
	if err != nil || check.Tools[0].Rules.Status != "PASS" || check.Tools[0].L3 != "UNKNOWN" || check.Tools[0].L4 != "UNKNOWN" {
		t.Fatal(check, err)
	}
	p, err := m.Plan(ctx, Request{Tools: []string{"claude-code"}, CheckID: check.ID})
	if err != nil || !p.WriteSupported {
		t.Fatal(p, err)
	}
	if err = m.Apply(ctx, p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	after, err := m.Source.Store.CaptureFile(filepath.Join(m.Root, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	history, err := m.History()
	if err != nil || len(history) != 1 {
		t.Fatal(history, err)
	}
	for i := 0; i < 2; i++ {
		rp, err := m.Reverse(history[0].Plan.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = m.ApplyReverse(ctx, rp.ID, rp.Digest); err != nil {
			t.Fatal(err)
		}
		history, err = m.History()
		if err != nil {
			t.Fatal(err)
		}
		current, err := m.Source.Store.CaptureFile(filepath.Join(m.Root, "CLAUDE.md"))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			data, _ := m.Source.Store.Data(current.Entries[workstore.FileContent])
			if !bytes.Equal(data, manual) {
				t.Fatal("rollback changed handwritten bytes")
			}
		} else if workstore.Digest(current) != workstore.Digest(after) {
			t.Fatal("undo changed installed rule")
		}
	}
	unchanged(t, m, m.Source.Store.Root, sourceBefore)
	// Even an import in a preserved handwritten prefix must not reach the CLI.
	installed, err := m.Source.Store.Data(after.Entries[workstore.FileContent])
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(m.Root, "CLAUDE.md"), append([]byte("@/unapproved/private.md\n"), installed...))
	m.probe.claudeRules = func(context.Context, toolBinary, string, string) ([]string, error) {
		t.Fatal("unapproved import started a rule session")
		return nil, nil
	}
	check, err = m.Check(ctx, CheckRequest{Tools: []string{"claude-code"}, Probe: true, RuleProbe: true})
	if err != nil || check.Tools[0].Rules.Code != "RULES_IMPORTS_UNSUPPORTED" || check.Tools[0].Rules.Status != "UNKNOWN" {
		t.Fatal(check, err)
	}
	_, err = m.Plan(ctx, Request{Tools: []string{"claude-code"}, CheckID: check.ID})
	if err == nil {
		t.Fatal("import-bearing rule qualified for installation")
	}
}

func TestActualClaudeRuleSourcesWithoutModel(t *testing.T) {
	if os.Getenv("AIDOCK_CLAUDE_RULE_INTEGRATION") != "1" {
		t.Skip("explicit Claude rule protocol integration")
	}
	binary, err := resolveBinary("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cwd, home := filepath.Join(root, "project"), filepath.Join(root, "home")
	for _, dir := range []string{filepath.Join(cwd, ".git"), home} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	version, err := toolVersion(context.Background(), binary, "claude-code", cwd, home)
	if err != nil || !hasVersion(Adapters()[0].RuleVersions, version) {
		t.Fatal("unaccepted Claude rule protocol version", version, err)
	}
	for _, files := range []int{0, 1, 2} {
		if files == 1 {
			write(t, filepath.Join(cwd, "CLAUDE.md"), []byte("# Local test\nUse repository conventions.\n"))
		}
		if files == 2 {
			write(t, filepath.Join(cwd, ".claude", "rules", "additional.md"), []byte("# Additional\nKeep compatibility.\n"))
		}
		sources, err := probeClaudeRuleProcess(context.Background(), binary, cwd, home)
		if err != nil || len(sources) != files {
			t.Fatalf("candidate source count %d: %q (%v)", files, sources, err)
		}
		if files == 1 && !samePath(sources[0], filepath.Join(cwd, "CLAUDE.md")) {
			t.Fatalf("unexpected candidate sources: %q", sources)
		}
	}
	t.Log(fmt.Sprintf("Claude Code %s: candidate source, absent-file and additional-rule controls PASS; no model turn; L3/L4 UNKNOWN", version))

	m, _ := writableFixture(t)
	saveRules(t, m, []any{"Do not change business files."})
	r, err := m.Check(context.Background(), CheckRequest{Tools: []string{"claude-code"}, Probe: true, RuleProbe: true})
	if err != nil || r.Tools[0].Rules.Status != "PASS" {
		t.Fatal(r, err)
	}
	p, err := m.Plan(context.Background(), Request{Tools: []string{"claude-code"}, CheckID: r.ID})
	if err != nil || !p.WriteSupported {
		t.Fatal(p, err)
	}
}
