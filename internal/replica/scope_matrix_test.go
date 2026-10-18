package replica

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

type matrixScope struct {
	name     string
	manager  *Manager
	bindings []Binding
	sources  []*source.Manager
}

// Deliberately reuse project, copy and skill identities across all scopes.
// Paths mirror a workspace container with independently owned child projects.
func threeScopeMatrix(t *testing.T) []matrixScope {
	t.Helper()
	parent, local := t.TempDir(), filepath.Join(t.TempDir(), "local")
	ids := []string{workstore.ID(), workstore.ID(), workstore.ID(), workstore.ID(), workstore.ID()}
	out := []matrixScope{}
	for _, scope := range []string{"workspace", "server", "client"} {
		pkg := filepath.Join(t.TempDir(), "package")
		generatedFixtureWrite(t, pkg, "SKILL.md", []byte("---\nname: guide\ndescription: scope matrix\n---\n"+scope+" original\n"))
		generatedFixtureWrite(t, pkg, "references/notes.txt", []byte(scope+" attachment"))
		group := matrixScope{name: scope}
		for i, id := range ids {
			root := filepath.Join(parent, fmt.Sprintf("copy-%d", i+1))
			if scope != "workspace" {
				root = filepath.Join(root, scope)
			}
			if err := os.MkdirAll(root, 0755); err != nil {
				t.Fatal(err)
			}
			s, err := source.New(filepath.Join(root, ".aidock"), local, owner, scope)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.BindBridge("", id); err != nil {
				t.Fatal(err)
			}
			applySource(t, s, source.Request{Action: "import", ID: skill, PackagePath: pkg, SourceRole: "local-original"})
			applySource(t, s, source.Request{Action: "enable", ID: skill})
			saveCopyProfile(t, s, map[string]any{"schema_version": 2, "name": "local", "rules": []any{fmt.Sprintf("%s local copy %d", scope, i+1)}})
			if i == 0 {
				saveCopyProfile(t, s, map[string]any{"schema_version": 2, "name": "local", "rules": []any{scope + " local enabled"}, "skills": map[string]any{"enable": []any{skill}}})
			}
			group.bindings = append(group.bindings, Binding{ID: id, Name: fmt.Sprintf("copy-%d", i+1), Root: s.Store.Root, Excludes: []string{}})
			group.sources = append(group.sources, s)
		}
		// Initiator and publisher are different copies; no permanent master.
		m, err := New(group.sources[2])
		if err != nil {
			t.Fatal(err)
		}
		group.manager = m
		for _, b := range group.bindings {
			p, err := m.Register(b)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.Apply(context.Background(), p.ID, p.Digest); err != nil {
				t.Fatal(err)
			}
		}
		order := append([]Binding{group.bindings[4]}, group.bindings[:4]...)
		baseline(t, m, order)
		out = append(out, group)
	}
	return out
}

func TestThreeScopeFiveCopyIdentityLocalGenerationAndRecoveryIsolation(t *testing.T) {
	groups, ctx := threeScopeMatrix(t), context.Background()
	all := []*source.Manager{}
	locals := map[string]string{}
	controls := map[string]string{}
	for _, group := range groups {
		all = append(all, group.sources...)
		for _, s := range group.sources {
			root := source.LocalProfileRoot(s.Store.Local, s.Owner, s.Scope, s.Copy)
			if _, exists := locals[root]; exists {
				t.Fatal("same copy identity collided across scopes", root)
			}
			locals[root] = workstore.Digest(generatedObservation(t, group.manager, root))
		}
		controls[group.name] = workstore.Digest(generatedObservation(t, group.manager, group.manager.Control.Root))
		applySource(t, group.sources[4], source.Request{Action: "edit", ID: skill, Files: []source.Patch{{Path: "references/notes.txt", Content: group.name + " publisher update"}}})
		applySource(t, group.sources[4], source.Request{Action: "disable", ID: skill})
	}
	originals := captureSources(t, all)
	for _, group := range groups {
		t.Run(group.name, func(t *testing.T) {
			m := group.manager
			order := append([]Binding{group.bindings[4]}, group.bindings[:4]...)
			choices := map[string]string{}
			for _, b := range order[1:] {
				choices[b.ID] = "source"
			}
			p, err := m.Resolve(ctx, resolveRequest(t, m, order, choices))
			if err != nil || len(p.Targets) != 4 || p.Source.ID == m.Source.Copy {
				t.Fatal("arbitrary publisher/initiator binding failed", p.Source.ID, err)
			}
			g, err := m.StageGeneration(ctx, GenerationRequest{Plan: p.ID, Digest: p.Digest, Tools: []string{"claude-code", "codex-cli"}, ApplicationScope: native.SkillsRulesScope})
			if err != nil || g.Blocked || len(g.Targets) != 4 {
				t.Fatal(g.Blocked, err)
			}
			for _, target := range g.Targets {
				for _, output := range target.Outputs {
					_, enabled := output.After.Entries["guide/SKILL.md"]
					if enabled != (target.Copy == group.bindings[0].ID) {
						t.Fatal("Local skill switch leaked or was ignored", target.Copy)
					}
					if enabled {
						body, err := m.Control.Data(output.After.Entries["guide/references/notes.txt"])
						if err != nil || string(body) != group.name+" publisher update" {
							t.Fatal("same UUID/name imported a foreign scope package", string(body), err)
						}
					}
				}
			}
			r, err := m.RehearseGenerated(ctx, g.ID, g.Digest)
			if err != nil || r.Status != "APPLIED" {
				t.Fatal(r.Status, err)
			}
			reverse, err := m.RehearsalReverse(r.ID, r.Transactions[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			r, err = m.RehearsalApply(ctx, r.ID, reverse.ID, reverse.Digest)
			if err != nil || r.Status != "ROLLED_BACK" {
				t.Fatal(r.Status, err)
			}
			for _, other := range groups {
				if other.name == group.name {
					continue
				}
				if _, err := other.manager.RehearsalShow(r.ID); err == nil {
					t.Fatal("foreign scope consumed another recovery identity")
				}
				if _, err := other.manager.loadResolution(p.ID, p.Digest); err == nil {
					t.Fatal("foreign scope consumed another source plan")
				}
				wrong, err := source.New(group.sources[0].Store.Root, m.Source.Store.Local, owner, other.name)
				if err != nil {
					t.Fatal(err)
				}
				_, err = wrong.State()
				code(t, err, "SOURCE_IDENTITY_MISMATCH")
			}
			unchanged(t, all, originals)
		})
	}
	for _, group := range groups {
		if controls[group.name] != workstore.Digest(generatedObservation(t, group.manager, group.manager.Control.Root)) {
			t.Fatal("isolated rehearsal changed a registered common baseline", group.name)
		}
		for _, s := range group.sources {
			root := source.LocalProfileRoot(s.Store.Local, s.Owner, s.Scope, s.Copy)
			if locals[root] != workstore.Digest(generatedObservation(t, group.manager, root)) {
				t.Fatal("scope matrix changed another Local profile", root)
			}
		}
		state, err := group.sources[0].Profile()
		if err != nil || !strings.Contains(string(data(state)), group.name+" local enabled") {
			t.Fatal("Local provenance changed", err)
		}
	}
}
