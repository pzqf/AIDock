package native

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func profileApplicationFixture(t *testing.T) (*Manager, Plan, []source.MCPSessionRequest, []source.MCPClientConfiguration) {
	t.Helper()
	m, _ := writableFixture(t)
	m.Source.Copy = "22222222-2222-4222-8222-222222222222"
	saveRules(t, m, []any{"joint profile rule"})
	fakeProbes(m, discoveredGuide)
	m.probe.rules = fakeRuleLoad
	m.probe.claudeRules = func(_ context.Context, _ toolBinary, cwd, _ string) ([]string, error) {
		return []string{filepath.Join(cwd, "CLAUDE.md")}, nil
	}
	for path, content := range map[string]string{
		".mcp.json":          "{\r\n \"privateSetting\":\"JSON_BACKUP_PRIVATE\",\r\n \"mcpServers\":{\"manual\":{\"command\":\"keep\"}}\r\n}\r\n",
		".codex/config.toml": "# handwritten\r\nmodel='TOML_BACKUP_PRIVATE'\r\n[mcp_servers.manual]\r\ncommand='keep'\r\n",
		"AGENTS.md":          "# handwritten rules\r\n",
	} {
		write(t, filepath.Join(m.Root, path), []byte(content))
	}
	r, err := m.Prepare(context.Background(), CheckRequest{Tools: []string{"claude-code", "codex-cli"}, ApplicationScope: SkillsRulesScope, Probe: true, RuleProbe: true})
	if err != nil || r.Status != "READY" {
		t.Fatal("skill/rule fixture unqualified", err)
	}
	requests := []source.MCPSessionRequest{}
	configs := []source.MCPClientConfiguration{}
	for _, name := range []string{"first-service", "second-service"} {
		requests = append(requests, source.MCPSessionRequest{Name: name, ExpectedSHA256: strings.Repeat("a", 64), ExpectedReviewSHA256: strings.Repeat("b", 64), ExpectedRuntimeAttempt: workstore.ID()})
		configs = append(configs, source.MCPClientConfiguration{Command: filepath.Join(t.TempDir(), "aidock.exe"), Args: []string{"mcp", "exec", "--plugin", name}})
	}
	return m, r.Plan, requests, configs
}

func compileProfileApplicationFixture(t *testing.T, m *Manager, p Plan, sessions []source.MCPSessionRequest, configs []source.MCPClientConfiguration) workstore.BatchPlan {
	t.Helper()
	requests, configurations := []MCPConnectionRequest{}, []*source.MCPClientConfiguration{}
	for i, s := range sessions {
		requests = append(requests, MCPConnectionRequest{Action: "install", Session: s})
		configurations = append(configurations, &configs[i])
	}
	connections, err := m.compileConnectionSet([]string{"claude-code", "codex-cli"}, requests, configurations, configs[0].Command, filepath.Dir(m.Source.Store.Local), true)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := m.profileApplicationAssets(p)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := m.profileApplicationBatch().Preview("apply", append(targets, connections...))
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestProfileApplicationJointBytesFailureRecoveryAndRollback(t *testing.T) {
	for _, fail := range []int{-1, 1, 5} {
		t.Run(strconv.Itoa(fail), func(t *testing.T) {
			m, native, sessions, configs := profileApplicationFixture(t)
			before := configurationTree(t, m.Root)
			plan := compileProfileApplicationFixture(t, m, native, sessions, configs)
			if len(plan.Targets) != 7 || !reflect.DeepEqual(before, configurationTree(t, m.Root)) {
				t.Fatal("joint preview changed targets or omitted a root")
			}
			if fail >= 0 {
				m.applicationHook = func(stage string, n int) error {
					if stage == "target" && n == fail {
						return errors.New("injected joint target failure")
					}
					return nil
				}
				if _, err := m.profileApplicationBatch().Apply(context.Background(), plan.ID, plan.Digest); err == nil {
					t.Fatal("failure not injected")
				}
				if !reflect.DeepEqual(before, configurationTree(t, m.Root)) {
					t.Fatal("joint failure left partial skill/rule/MCP files")
				}
				h, err := m.ProfileApplicationHistory()
				if err != nil || len(h) != 1 || h[0].State != "recovered" {
					t.Fatal(h, err)
				}
				return
			}
			j, err := m.profileApplicationBatch().Apply(context.Background(), plan.ID, plan.Digest)
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range Adapters() {
				data, err := os.ReadFile(filepath.Join(m.Root, a.MCPConfig.Path))
				if err != nil || !bytes.Contains(data, []byte("BACKUP_PRIVATE")) || !bytes.Contains(data, []byte("aidock-first-service")) || !bytes.Contains(data, []byte("aidock-second-service")) {
					t.Fatal("lost manual bytes or MCP set", err)
				}
				data, err = os.ReadFile(filepath.Join(m.Root, a.Directory, "guide/assets/data.bin"))
				if err != nil || !bytes.Equal(data, []byte{0, 1, 255}) {
					t.Fatal("joint install lost binary attachment", err)
				}
			}
			q, err := m.ReverseProfileApplication(context.Background(), j.Plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.ApplyProfileApplicationReverse(context.Background(), q.ID, q.Digest); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, configurationTree(t, m.Root)) {
				t.Fatal("joint rollback did not restore all original bytes")
			}
			if _, err = m.ReverseProfileApplication(context.Background(), q.ID); err == nil {
				t.Fatal("undo restored fake unapproved launchers")
			}
			// Raw native configuration never enters the ordinary source buckets.
			if err = filepath.WalkDir(m.Source.Store.Local, func(path string, d os.DirEntry, e error) error {
				if e != nil {
					return e
				}
				if d.IsDir() || workstore.Within(m.connectionArea(), path) {
					return nil
				}
				data, e := os.ReadFile(path)
				if e == nil && bytes.Contains(data, []byte("BACKUP_PRIVATE")) {
					t.Fatal("raw native bytes escaped private area", path)
				}
				return e
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProfileApplicationReconcilesManagedSetWithoutTouchingManualFields(t *testing.T) {
	m, p, sessions, configs := profileApplicationFixture(t)
	plan := compileProfileApplicationFixture(t, m, p, sessions, configs)
	if _, err := m.profileApplicationBatch().Apply(context.Background(), plan.ID, plan.Digest); err != nil {
		t.Fatal(err)
	}
	requests := []MCPConnectionRequest{{Action: "install", Session: sessions[0]}}
	targets, err := m.compileConnectionSet([]string{"claude-code", "codex-cli"}, requests, []*source.MCPClientConfiguration{&configs[0]}, configs[0].Command, filepath.Dir(m.Source.Store.Local), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		if target.Path == connectionLedgerPath {
			continue
		}
		data, err := m.connectionStore().Data(target.After.Entries[workstore.FileContent])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte("aidock-first-service")) || bytes.Contains(data, []byte("aidock-second-service")) || !bytes.Contains(data, []byte("BACKUP_PRIVATE")) {
			t.Fatal("disabled connection not removed or manual field changed")
		}
	}
}

func TestProfileApplicationRequestRejectsAmbiguityAndCannotBypassQualification(t *testing.T) {
	for _, raw := range []string{`null`, `{"native_plan":"x","native_digest":"y","sessions":[]}`, `{"native_plan":"x","native_plan":"x","native_digest":"y","sessions":[]}`, `{"native_plan":"x","native_digest":"y","sessions":[],"command":"PRIVATE_INPUT"}`} {
		_, err := ParseProfileApplicationRequest([]byte(raw))
		if err == nil || strings.Contains(err.Error(), "PRIVATE_INPUT") {
			t.Fatal("accepted ambiguity or leaked raw request", err)
		}
	}
	m, p, sessions, _ := profileApplicationFixture(t)
	req := ProfileApplicationRequest{NativePlan: p.ID, NativeDigest: p.Digest, Sessions: sessions}
	_, err := m.ProfileApplicationPlan(context.Background(), req, filepath.Join(t.TempDir(), "missing.exe"), filepath.Dir(m.Source.Store.Local))
	if err == nil {
		t.Fatal("missing program accepted")
	}
	_, err = m.ProfileApplicationPlan(context.Background(), ProfileApplicationRequest{}, "", "")
	if err == nil {
		t.Fatal("missing skill qualification accepted")
	}
}

func TestProfileApplicationWithoutMCPLeavesAbsentConfigurationAbsent(t *testing.T) {
	m, _ := writableFixture(t)
	m.Source.Copy = "22222222-2222-4222-8222-222222222222"
	fakeProbes(m, discoveredGuide)
	r, err := m.Prepare(context.Background(), CheckRequest{Tools: []string{"claude-code", "codex-cli"}, ApplicationScope: SkillsRulesScope, Probe: true})
	if err != nil || r.Status != "READY" {
		t.Fatal(r.Status, err)
	}
	executable := filepath.Join(t.TempDir(), "aidock.exe")
	write(t, executable, []byte("unexecuted fixed test host"))
	p, err := m.ProfileApplicationPlan(context.Background(), ProfileApplicationRequest{NativePlan: r.Plan.ID, NativeDigest: r.Plan.Digest, Sessions: []source.MCPSessionRequest{}}, executable, filepath.Dir(m.Source.Store.Local))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range p.Transaction.Targets {
		if target.Path == ".mcp.json" || target.Path == ".codex/config.toml" || target.Path == connectionLedgerPath {
			if target.Before.Exists || target.After.Exists {
				t.Fatal("empty effective MCP set created an empty configuration", target.Path)
			}
		}
	}
	if _, err := m.ApplyProfileApplication(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".mcp.json", ".codex/config.toml", connectionLedgerPath} {
		if _, err := os.Stat(filepath.Join(m.Root, path)); !os.IsNotExist(err) {
			t.Fatal("joint skill install created an absent MCP file", path, err)
		}
	}
}
