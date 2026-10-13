package source

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
)

func declaration() *Governance {
	return &Governance{Dependencies: map[string]any{}, Compatibility: map[string]any{}, Requirements: map[string]any{}}
}
func govern(t *testing.T, m *Manager, id string, g *Governance) Preview {
	t.Helper()
	return apply(t, m, Request{Action: "governance", ID: id, Revision: selectedSkill(t, m, id).Revision, Governance: g})
}
func hasCheck(r Preflight, id, code, status string) bool {
	for _, s := range r.Skills {
		if s.ID == id {
			for _, f := range s.Checks {
				if f.Code == code && f.Status == status {
					return true
				}
			}
		}
	}
	return false
}
func checked(t *testing.T, m *Manager) Preflight {
	t.Helper()
	r, e := m.Check()
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func skillDependency(m *Manager, s Skill, required bool) map[string]any {
	return map[string]any{"owner": m.Owner, "scope_id": m.Scope, "id": s.ID, "revision": s.Revision, "required": required}
}

func TestGovernanceRevisionRollbackUndoAndReadonly(t *testing.T) {
	m := manager(t)
	created := apply(t, m, Request{Action: "create", Name: "governed-guide", Description: "example"})
	id := created.SkillID
	apply(t, m, Request{Action: "edit", ID: id, Files: []Patch{{Path: "assets/data.bin", Content: "text attachment"}}})
	old := selectedSkill(t, m, id)
	oldTree := capture(t, m)
	g := declaration()
	g.Compatibility["platforms"] = []string{runtime.GOOS}
	plan, e := m.Plan(context.Background(), Request{Action: "governance", ID: id, Revision: old.Revision, Governance: g})
	if e != nil || plan.Preflight == nil || plan.Preflight.Status != "PASS" {
		t.Fatal(plan, e)
	}
	unchangedManager(t, m, oldTree)
	if _, e = m.Apply(context.Background(), plan.Plan.ID, plan.Plan.Digest); e != nil {
		t.Fatal(e)
	}
	s := selectedSkill(t, m, id)
	if s.Revision == old.Revision || s.RevisionFormat != "semantic-v1" || s.PackageDigest == "" {
		t.Fatal(s)
	}
	for _, f := range old.Files {
		b, _ := os.ReadFile(filepath.Join(m.Store.Root, filepath.FromSlash(s.Source.Path), filepath.FromSlash(f.Path)))
		if workstore.Hash(b) != f.SHA256 {
			t.Fatal("governance changed package bytes", f.Path)
		}
	}
	saved := capture(t, m)
	r := checked(t, m)
	again := checked(t, m)
	if !reflect.DeepEqual(r, again) || r.NativeReady {
		t.Fatal("nondeterministic or overstated report", r)
	}
	unchangedManager(t, m, saved)
	reverted := apply(t, m, Request{Action: "rollback", Snapshot: plan.Plan.ID})
	if selectedSkill(t, m, id).Revision != old.Revision {
		t.Fatal("legacy revision not restored")
	}
	if _, e = os.Stat(filepath.Join(m.Store.Root, ".governance-records", id, strings.TrimPrefix(s.Revision, "sha256:")+".json")); e != nil {
		t.Fatal("metadata archive lost", e)
	}
	apply(t, m, Request{Action: "rollback", Snapshot: reverted.Plan.ID})
	if selectedSkill(t, m, id).Revision != s.Revision {
		t.Fatal("undo failed")
	}
	unchangedManager(t, m, saved)
}

func TestGovernanceGlobalCandidateDoesNotChangeProjectPins(t *testing.T) {
	l := library(t)
	a, b := manager(t), manager(t)
	id := apply(t, l, Request{Action: "create", Name: "global-governed", Description: "example"}).SkillID
	old := selectedSkill(t, l, id).Revision
	attach(t, a, l)
	attach(t, b, l)
	apply(t, a, Request{Action: "inherit", ID: id, Revision: old})
	apply(t, b, Request{Action: "inherit", ID: id, Revision: old})
	aBefore, bBefore := capture(t, a), capture(t, b)
	g := declaration()
	g.Compatibility["tools"] = map[string]string{"codex-cli": ">=1.0.0"}
	govern(t, l, id, g)
	if selectedSkill(t, l, id).Revision == old {
		t.Fatal("global governance not versioned")
	}
	unchangedManager(t, a, aBefore)
	unchangedManager(t, b, bBefore)
	_, e := a.Plan(context.Background(), Request{Action: "governance", ID: id, Revision: old, Governance: g})
	code(t, e, "INHERITED_READ_ONLY")
	apply(t, a, Request{Action: "override", ID: id, Revision: old})
	govern(t, a, id, g)
	if effective(t, a, id).Layer != "override" || effective(t, b, id).Skill.Revision != old {
		t.Fatal("override leaked")
	}
}

func TestPreflightRequiredOptionalAndExactDependency(t *testing.T) {
	m := manager(t)
	a := apply(t, m, Request{Action: "create", Name: "consumer", Description: "example"}).SkillID
	b := apply(t, m, Request{Action: "create", Name: "dependency", Description: "example"}).SkillID
	target := selectedSkill(t, m, b)
	g := declaration()
	g.Dependencies["skills"] = []any{skillDependency(m, target, true)}
	govern(t, m, a, g)
	before := capture(t, m)
	_, e := m.Plan(context.Background(), Request{Action: "enable", ID: a})
	code(t, e, "SKILL_PREFLIGHT_FAILED")
	unchangedManager(t, m, before)
	apply(t, m, Request{Action: "enable", ID: b})
	apply(t, m, Request{Action: "enable", ID: a})
	if r := checked(t, m); r.Status != "PASS" || !hasCheck(r, a, "DEPENDENCY_PINNED", "PASS") {
		t.Fatal(r)
	}
	apply(t, m, Request{Action: "edit", ID: b, Files: []Patch{{Path: "SKILL.md", Content: string(body("dependency")) + "new revision"}}})
	if r := checked(t, m); !hasCheck(r, a, "DEPENDENCY_REVISION_MISMATCH", "FAIL") {
		t.Fatal(r)
	}
	// Save unresolved declarations without implicitly enabling or downloading.
	missing := target
	missing.ID = "99999999-9999-4999-8999-999999999999"
	g.Dependencies["skills"] = []any{skillDependency(m, missing, false)}
	govern(t, m, a, g)
	if r := checked(t, m); !hasCheck(r, a, "DEPENDENCY_MISSING", "WARN") {
		t.Fatal(r)
	}
	g.Dependencies["skills"] = []any{skillDependency(m, missing, true)}
	govern(t, m, a, g)
	if r := checked(t, m); !hasCheck(r, a, "DEPENDENCY_MISSING", "FAIL") {
		t.Fatal(r)
	}
}

func TestPreflightCyclesPlatformScriptsAndUnknowns(t *testing.T) {
	m := manager(t)
	a := apply(t, m, Request{Action: "create", Name: "cycle-a", Description: "example"}).SkillID
	b := apply(t, m, Request{Action: "create", Name: "cycle-b", Description: "example"}).SkillID
	ga, gb := declaration(), declaration()
	ga.Dependencies["skills"] = []any{skillDependency(m, selectedSkill(t, m, b), true)}
	govern(t, m, a, ga)
	gb.Dependencies["skills"] = []any{skillDependency(m, selectedSkill(t, m, a), true)}
	govern(t, m, b, gb)
	r := checked(t, m)
	if !hasCheck(r, a, "DEPENDENCY_CYCLE", "FAIL") || !hasCheck(r, b, "DEPENDENCY_CYCLE", "FAIL") {
		t.Fatal(r)
	}
	ga = declaration()
	ga.Compatibility["platforms"] = []string{"unavailable-platform"}
	ga.Requirements["scripts"] = []string{"scripts/missing.ps1"}
	govern(t, m, a, ga)
	r = checked(t, m)
	for _, c := range []string{"PLATFORM_INCOMPATIBLE", "SCRIPT_MISSING", "SCRIPT_EXEC_UNDECLARED"} {
		if !hasCheck(r, a, c, "FAIL") {
			t.Fatal(c, r)
		}
	}
	ga = declaration()
	ga.Dependencies["runtime"] = []any{map[string]any{"name": "python", "version": ">=3.12", "entrypoint": "python", "required": true}}
	ga.Dependencies["plugins"] = []any{map[string]any{"id": "example", "lock_digest": "sha256:" + strings.Repeat("a", 64), "required": true}}
	ga.Dependencies["project_files"] = []any{map[string]any{"path": "never-read.txt", "required": true}}
	ga.Compatibility["tools"] = map[string]string{"claude-code": ">=2.0.0"}
	ga.Requirements["exec"] = true
	govern(t, m, a, ga)
	r = checked(t, m)
	for _, c := range []string{"DEPENDENCY_RUNTIME_UNVERIFIED", "DEPENDENCY_PLUGINS_UNVERIFIED", "DEPENDENCY_PROJECT_FILES_UNVERIFIED", "TOOL_VERSION_UNVERIFIED", "EXECUTION_REQUIREMENT_UNVERIFIED"} {
		if !hasCheck(r, a, c, "UNKNOWN") {
			t.Fatal(c, r)
		}
	}
	if r.NativeReady {
		t.Fatal("unknown runtime claimed usable")
	}
}

func TestGovernanceRejectsBadStaleAndDriftedRequests(t *testing.T) {
	m := manager(t)
	id := apply(t, m, Request{Action: "create", Name: "safe-guide", Description: "example"}).SkillID
	rev := selectedSkill(t, m, id).Revision
	before := capture(t, m)
	for _, data := range []string{
		`{"action":"governance","id":"` + id + `","revision":"` + rev + `","governance":{"dependencies":{},"compatibility":{},"requirements":{},"root":"other"}}`,
		`{"action":"governance","id":"` + id + `","revision":"` + rev + `","governance":{"dependencies":null,"compatibility":{},"requirements":{}}}`,
	} {
		var req Request
		if e := json.Unmarshal([]byte(data), &req); e == nil {
			_, e = m.Plan(context.Background(), req)
			code(t, e, "INVALID_INPUT")
		}
		unchangedManager(t, m, before)
	}
	g := declaration()
	g.Requirements["undeclared-field"] = true
	_, e := m.Plan(context.Background(), Request{Action: "governance", ID: id, Revision: rev, Governance: g})
	code(t, e, "INVALID_INPUT")
	for _, p := range []string{"../outside.ps1", "C:/outside.ps1", "scripts/CON"} {
		g = declaration()
		g.Requirements["scripts"] = []string{p}
		g.Requirements["exec"] = true
		_, e = m.Plan(context.Background(), Request{Action: "governance", ID: id, Revision: rev, Governance: g})
		code(t, e, "INVALID_INPUT")
	}
	_, e = m.Plan(context.Background(), Request{Action: "governance", ID: id, Revision: "sha256:" + strings.Repeat("a", 64), Governance: declaration()})
	code(t, e, "PLAN_STALE")
	write(t, filepath.Join(m.Store.Root, "skills", id, "SKILL.md"), body("safe-guide"))
	drift := capture(t, m)
	_, e = m.Plan(context.Background(), Request{Action: "governance", ID: id, Revision: rev, Governance: declaration()})
	code(t, e, "SOURCE_PACKAGE_DRIFT")
	unchangedManager(t, m, drift)
	if r := checked(t, m); !hasCheck(r, id, "SOURCE_PACKAGE_DRIFT", "FAIL") {
		t.Fatal(r)
	}
}

func TestGovernancePlanStaleAndWriteFailureRestores(t *testing.T) {
	m := manager(t)
	id := apply(t, m, Request{Action: "create", Name: "recover-guide", Description: "example"}).SkillID
	g := declaration()
	g.Compatibility["platforms"] = []string{runtime.GOOS}
	req := Request{Action: "governance", ID: id, Revision: selectedSkill(t, m, id).Revision, Governance: g}
	p, e := m.Plan(context.Background(), req)
	if e != nil {
		t.Fatal(e)
	}
	apply(t, m, Request{Action: "metadata", ID: id, DisplayName: ptr("changed")})
	changed := capture(t, m)
	_, e = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest)
	code(t, e, "PLAN_STALE")
	unchangedManager(t, m, changed)
	p, e = m.Plan(context.Background(), req)
	if e != nil {
		t.Fatal(e)
	}
	m.Store.Hook = func(phase string, n int) error {
		if phase == "replaced" && n == 1 {
			return errors.New("injected governance write failure")
		}
		return nil
	}
	_, e = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest)
	if e == nil {
		t.Fatal("fault not injected")
	}
	m.Store.Hook = nil
	unchangedManager(t, m, changed)
}
