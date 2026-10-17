package replica

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/pluginconfig"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/source"
)

func sharedPluginCopies(t *testing.T, count int) (*Manager, []Binding, []*source.Manager) {
	t.Helper()
	m, b, copies := scopedReplicaFixture(t, count)
	for i, s := range copies {
		state, err := s.MCP()
		if err != nil {
			t.Fatal(err)
		}
		server := state.Config["servers"].([]any)[0].(map[string]any)
		server["name"] = "project-memory"
		server["source"] = map[string]any{"type": "local", "path": "source-not-read"}
		server["enabled"] = false
		p, err := s.MCPPlan(context.Background(), source.MCPEdit{ExpectedSHA256: state.SHA256, Config: state.Config})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
			t.Fatal(err)
		}
		manifest := map[string]any{"schema_version": 1, "name": "shared-fixture", "version": "1.2.3", "type": "mcp-server", "source": server["source"], "permissions": server["permissions"], "dependencies": []any{}, "config_delivery": pluginconfig.Delivery, "config_schema": map[string]any{
			"languages": map[string]any{"type": "multi-select", "options": []any{"go", "typescript"}, "sync": "shared"},
			"optional":  map[string]any{"type": "string", "nullable": true, "sync": "shared"},
			"directory": map[string]any{"type": "directory", "sync": "shared"},
			"message":   map[string]any{"type": "string"},
			"port":      map[string]any{"type": "integer", "sync": "local"},
		}}
		root := filepath.Join(s.Store.Local, "mcp-artifacts", "shared-fixture")
		generatedFixtureWrite(t, root, "plugin.yaml", data(manifest))
		generatedFixtureWrite(t, root, "runtime/helper", []byte("never execute"))
		generatedFixtureWrite(t, root, "index.js", []byte("// never execute"))
		if err = os.Chmod(filepath.Join(root, "runtime/helper"), 0755); err != nil {
			t.Fatal(err)
		}
		request := pluginlock.Preparation{Name: "project-memory", ArtifactPath: "mcp-artifacts/shared-fixture", Version: "1.2.3", Runtime: pluginlock.Runtime{Kind: "native", Version: "1.2.3", Constraint: "=1.2.3", Executable: "runtime/helper"}}
		lock, err := s.InspectMCPLock(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := s.MCPLockPlan(context.Background(), source.MCPLockEdit{ExpectedSHA256: lock.ExpectedSHA256, Document: lock.Document})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.ApplyMCPLock(context.Background(), plan.Plan.ID, plan.Plan.Digest); err != nil {
			t.Fatal(err)
		}
		saveSharedConfiguration(t, s, func(p map[string]any) {
			p["config_overrides"] = map[string]any{"project-memory": map[string]any{"languages": []any{"go"}, "optional": "base", "directory": "${PROJECT}/index", "message": fmt.Sprintf("PRIVATE_COPY_%d", i), "port": 9000 + i}}
		}, nil)
	}
	local, err := m.copySource(b[1])
	if err != nil {
		t.Fatal(err)
	}
	saveCopyProfile(t, local, map[string]any{"schema_version": 2, "name": "target local", "config_overrides": map[string]any{"project-memory": map[string]any{"languages": []any{"typescript"}}}})
	return m, b, copies
}

func updateSharedPlugin(t *testing.T, s *source.Manager) {
	t.Helper()
	saveSharedConfiguration(t, s, func(p map[string]any) {
		v := p["config_overrides"].(map[string]any)["project-memory"].(map[string]any)
		v["languages"] = []any{}
		v["optional"] = nil
		v["directory"] = "${PROJECT}/new-index"
	}, nil)
}

func sharedPluginResolution(t *testing.T, m *Manager, b []Binding) Resolution {
	t.Helper()
	choices := map[string]string{}
	for _, copy := range b[1:] {
		choices[copy.ID+"/"+source.SharedPluginPrefix+"project-memory"] = "source"
	}
	p, err := m.Resolve(context.Background(), configurationRequest(t, m, b, choices))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSharedPluginFiveCopyCandidatesLocalPreservationThreeWayAndRehearsal(t *testing.T) {
	m, b, copies := sharedPluginCopies(t, 5)
	ctx := context.Background()
	baseline(t, m, b)
	updateSharedPlugin(t, copies[0])
	configurationStatusFor(t, m, b, source.SharedPluginPrefix+"project-memory", "SOURCE_CHANGED")
	comparison, err := m.Compare(ctx, Request{From: b[0].ID, Targets: []string{b[1].ID}})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range comparison.Targets[0].Configurations {
		if item.ID == source.SharedPluginPrefix+"project-memory" && (len(item.LocalOverrides) != 1 || item.LocalOverrides[0] != "languages") {
			t.Fatal("Local mask not explained", item.LocalOverrides)
		}
	}
	before := captureSources(t, copies)
	p := sharedPluginResolution(t, m, b)
	if len(p.Targets) != 4 {
		t.Fatal("missing target", len(p.Targets))
	}
	for _, target := range p.Targets {
		copyIndex := -1
		for i, binding := range b {
			if binding.ID == target.Copy.ID {
				copyIndex = i
			}
		}
		s, _ := m.copySource(target.Copy)
		if err := transfer(m.Control, s.Store, target.After); err != nil {
			t.Fatal(err)
		}
		doc, err := s.Store.Data(target.After.Entries["profile.yaml"])
		if err != nil {
			t.Fatal(err)
		}
		var profile map[string]any
		if err = json.Unmarshal(doc, &profile); err != nil {
			t.Fatal(err)
		}
		values := profile["config_overrides"].(map[string]any)["project-memory"].(map[string]any)
		if values["message"] != fmt.Sprintf("PRIVATE_COPY_%d", copyIndex) || values["port"] != float64(9000+copyIndex) || values["directory"] != "${PROJECT}/new-index" || values["optional"] != nil || len(values["languages"].([]any)) != 0 {
			t.Fatal(values)
		}
		if copyIndex == 1 {
			langs := target.Preflight.ProfileReport.Effective["config_overrides"].(map[string]any)["project-memory"].(map[string]any)["languages"].([]any)
			if len(langs) != 1 || langs[0] != "typescript" {
				t.Fatal("Local precedence lost", langs)
			}
		}
	}
	g, err := m.StageGeneration(ctx, GenerationRequest{Plan: p.ID, Digest: p.Digest, Tools: []string{"claude-code", "codex-cli"}, ApplicationScope: native.SkillsRulesScope})
	if err != nil || g.Blocked {
		t.Fatal(g.Blocked, err)
	}
	r, err := m.RehearseGenerated(ctx, g.ID, g.Digest)
	if err != nil {
		t.Fatal(err)
	}
	for _, after := range []bool{true, false, true} {
		for _, target := range p.Targets {
			want := target.Before
			if after {
				want = target.After
			}
			generatedEqual(t, m, generatedRoot(r, "copies/"+target.Copy.ID+"/.aidock"), want)
		}
		unchanged(t, copies, before)
		if after && len(r.Transactions) > 1 {
			break
		}
		reverse, err := m.RehearsalReverse(r.ID, r.Transactions[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		r, err = m.RehearsalApply(ctx, r.ID, reverse.ID, reverse.Digest)
		if err != nil {
			t.Fatal(err)
		}
	}
	// The same shared field changed at both ends is an explicit conflict.
	updateSharedPlugin(t, copies[1])
	saveSharedConfiguration(t, copies[1], func(p map[string]any) {
		p["config_overrides"].(map[string]any)["project-memory"].(map[string]any)["directory"] = "${PROJECT}/target-index"
	}, nil)
	configurationStatusFor(t, m, b, source.SharedPluginPrefix+"project-memory", "CONFLICT")
}

func TestSharedPluginComparisonBlocksMissingChangedContractAndUnsafeValue(t *testing.T) {
	value := func(name string) *source.SharedConfiguration {
		return &source.SharedConfiguration{ID: source.SharedPluginPrefix + name, Name: name, Digest: strings.Repeat("1", 64), Profile: map[string]any{}, Plugin: &source.SharedPluginConfiguration{Name: name, ManifestSHA256: strings.Repeat("2", 64), ArtifactSHA256: strings.Repeat("3", 64), Fields: []string{"language"}}}
	}
	a, b := value("service"), value("service")
	check := func(s, tv *source.SharedConfiguration, want string) {
		t.Helper()
		slist, tlist := []source.SharedConfiguration{}, []source.SharedConfiguration{}
		if s != nil {
			slist = append(slist, *s)
		}
		if tv != nil {
			tlist = append(tlist, *tv)
		}
		r := configurationDifferences(Binding{}, Binding{}, source.SharedCatalog{Configurations: slist}, source.SharedCatalog{Configurations: tlist}, Baseline{})
		if len(r) != 3 || r[2].Status != want {
			t.Fatal(r)
		}
	}
	check(a, b, "SAME")
	check(a, nil, "CONFIGURATION_CONTRACT_MISMATCH")
	b.Plugin.ManifestSHA256 = strings.Repeat("4", 64)
	check(a, b, "CONFIGURATION_CONTRACT_MISMATCH")
	b = value("service")
	b.Blocked = "PLUGIN_SHARED_VALUE_BLOCKED"
	check(a, b, "CONFIGURATION_VALUE_BLOCKED")
}

func TestLocalSharedPluginPublicationRollbackAndUndo(t *testing.T) {
	if os.Getenv("AIDOCK_NATIVE_PROBE_TEST") != "1" {
		t.Skip("opt-in actual two-tool candidate probes with temporary copies")
	}
	m, b, copies := sharedPluginCopies(t, 2)
	ctx := context.Background()
	baseline(t, m, b)
	updateSharedPlugin(t, copies[0])
	publisher := generatedObservation(t, m, b[0].Root)
	resolution := sharedPluginResolution(t, m, b)
	g, err := m.StageGeneration(ctx, GenerationRequest{Plan: resolution.ID, Digest: resolution.Digest, Tools: []string{"claude-code", "codex-cli"}, ApplicationScope: native.SkillsRulesScope})
	if err != nil {
		t.Fatal(err)
	}
	check, err := m.CheckGeneration(ctx, GenerationCheckRequest{Plan: g.ID, Digest: g.Digest, Probe: true, RuleProbe: true})
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.PlanPublication(ctx, PublicationRequest{g.ID, g.Digest, check.ID, check.Digest})
	if err != nil {
		t.Fatal(err)
	}
	current, err := m.ApplyPublication(ctx, p.ID, p.Digest)
	if err != nil {
		t.Fatal(err)
	}
	for _, after := range []bool{true, false, true} {
		coordinator := m.publicationCoordinator(p)
		for _, target := range p.Transaction.Targets {
			want := target.Before
			if after {
				want = target.After
			}
			root := ""
			for _, binding := range coordinator.Roots {
				if binding.Path == target.Path {
					root = binding.Root
				}
			}
			if target.File {
				replicaFileEqual(t, m, root, want)
			} else {
				generatedEqual(t, m, root, want)
			}
		}
		generatedEqual(t, m, b[0].Root, publisher)
		if after && len(current.Transactions) > 1 {
			break
		}
		reverse, err := m.ReversePublication(p.ID, current.Transactions[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		current, err = m.ApplyPublicationReverse(ctx, p.ID, reverse.ID, reverse.Digest)
		if err != nil {
			t.Fatal(err)
		}
	}
}
