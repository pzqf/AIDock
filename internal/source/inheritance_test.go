package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/workstore"
)

func library(t *testing.T) *Manager {
	t.Helper()
	d := t.TempDir()
	m, e := NewGlobal(filepath.Join(d, "config"), filepath.Join(d, "local"))
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func selectedSkill(t *testing.T, m *Manager, id string) Skill {
	t.Helper()
	for _, s := range current(t, m).Index.Skills {
		if s.ID == id {
			return s
		}
	}
	t.Fatal("owned skill missing", id)
	return Skill{}
}
func unchangedManager(t *testing.T, m *Manager, before workstore.Tree) {
	t.Helper()
	unchanged(t, before, capture(t, m))
}
func attach(t *testing.T, m, l *Manager) {
	t.Helper()
	if e := m.AttachLibrary(l.Store.Root); e != nil {
		t.Fatal(e)
	}
}
func effective(t *testing.T, m *Manager, id string) EffectiveSkill {
	t.Helper()
	s, e := m.State()
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range s.Effective {
		if v.Skill.ID == id {
			return v
		}
	}
	t.Fatal("missing effective", id)
	return EffectiveSkill{}
}
func TestGlobalPinnedInheritanceIsolationAndOffline(t *testing.T) {
	l := library(t)
	first := apply(t, l, Request{Action: "create", Name: "global-guide", Description: "example", Document: "first"})
	id := first.SkillID
	rev := selectedSkill(t, l, id).Revision
	a, b := manager(t), manager(t)
	attach(t, a, l)
	attach(t, b, l)
	apply(t, a, Request{Action: "inherit", ID: id, Revision: rev})
	apply(t, b, Request{Action: "inherit", ID: id, Revision: rev})
	bBefore := capture(t, b)
	globalBefore := capture(t, l)
	apply(t, a, Request{Action: "enable", ID: id})
	unchangedManager(t, b, bBefore)
	unchangedManager(t, l, globalBefore)
	apply(t, l, Request{Action: "edit", ID: id, Files: []Patch{{Path: "SKILL.md", Content: string(body("global-guide")) + "second"}}})
	newRev := selectedSkill(t, l, id).Revision
	if newRev == rev || effective(t, a, id).Skill.Revision != rev || effective(t, b, id).Skill.Revision != rev {
		t.Fatal("candidate update changed fixed project version")
	}
	cat, e := l.Catalog()
	if e != nil || len(cat) != 2 {
		t.Fatal(cat, e)
	}
	apply(t, a, Request{Action: "inherit", ID: id, Revision: newRev})
	if effective(t, b, id).Skill.Revision != rev {
		t.Fatal("cross-project update")
	}
	// Cached old revisions are independent of both library access and candidates.
	b.Library = nil
	f, e := b.File(id, "SKILL.md")
	if e != nil || !strings.Contains(f["content"].(string), "first") || f["read_only"] != true {
		t.Fatal(f, e)
	}
	_, e = b.Plan(context.Background(), Request{Action: "edit", ID: id, Files: []Patch{{Path: "SKILL.md", Content: "bad"}}})
	code(t, e, "INHERITED_READ_ONLY")
}
func TestOverrideForkRestoreAndUndo(t *testing.T) {
	l := library(t)
	p := apply(t, l, Request{Action: "create", Name: "base-guide", Description: "example"})
	id := p.SkillID
	rev := selectedSkill(t, l, id).Revision
	m := manager(t)
	attach(t, m, l)
	apply(t, m, Request{Action: "inherit", ID: id, Revision: rev})
	global := capture(t, l)
	apply(t, m, Request{Action: "override", ID: id, Revision: rev})
	s := effective(t, m, id)
	if s.Layer != "override" || s.Skill.BaseRevision != rev || s.Skill.BaseSource["owner"] != "global" {
		t.Fatal(s)
	}
	apply(t, m, Request{Action: "edit", ID: id, Files: []Patch{{Path: "SKILL.md", Content: string(body("base-guide")) + "custom"}}})
	custom := selectedSkill(t, m, id).Revision
	fork := apply(t, m, Request{Action: "fork", ID: id, Revision: custom, Name: "independent-guide"})
	f := selectedSkill(t, m, fork.SkillID)
	if f.ID == id || f.Upstream["owner"] != owner || f.Upstream["revision"] != custom || len(f.BaseSource) != 0 || f.BaseRevision != "" {
		t.Fatal(f)
	}
	restore := apply(t, m, Request{Action: "restore-inheritance", ID: id, Revision: rev})
	if effective(t, m, id).Layer != "inherited" {
		t.Fatal("restore failed")
	}
	if _, e := os.Stat(filepath.Join(m.Store.Root, filepath.FromSlash(".revisions/"+id+"/"+strings.TrimPrefix(custom, "sha256:")))); e != nil {
		t.Fatal("old custom revision lost", e)
	}
	rollback := apply(t, m, Request{Action: "rollback", Snapshot: restore.Plan.ID})
	if selectedSkill(t, m, id).Revision != custom {
		t.Fatal("restore rollback lost custom")
	}
	apply(t, m, Request{Action: "rollback", Snapshot: rollback.Plan.ID})
	if effective(t, m, id).Layer != "inherited" {
		t.Fatal("undo failed")
	}
	unchangedManager(t, l, global)
}
func TestGlobalPackageCacheIsComplete(t *testing.T) {
	l := library(t)
	pkg := filepath.Join(t.TempDir(), "package")
	doc := []byte("\xef\xbb\xbf---\r\nname: binary-guide\r\ndescription: example\r\nx-extra: keep # comment\r\n---\r\n\r\nbody\r\n")
	write(t, filepath.Join(pkg, "SKILL.md"), doc)
	write(t, filepath.Join(pkg, "assets", "data.bin"), []byte{0, 255, 2, 0})
	write(t, filepath.Join(pkg, "scripts", "run.sh"), []byte("#!/bin/sh\r\nexit 0\r\n"))
	if e := os.MkdirAll(filepath.Join(pkg, "empty"), 0755); e != nil {
		t.Fatal(e)
	}
	p := apply(t, l, Request{Action: "import", PackagePath: pkg, SourceRole: "local-original"})
	s := selectedSkill(t, l, p.SkillID)
	m := manager(t)
	attach(t, m, l)
	apply(t, m, Request{Action: "inherit", ID: s.ID, Revision: s.Revision})
	inherited := effective(t, m, s.ID)
	a, e := l.Store.Capture(filepath.Join(l.Store.Root, filepath.FromSlash(revisionPath(s))))
	if e != nil {
		t.Fatal(e)
	}
	b, e := m.cached(nil, inherited.Skill)
	if e != nil || workstore.Digest(a) != workstore.Digest(b) {
		t.Fatal("cache changed package", e)
	}
	apply(t, m, Request{Action: "override", ID: s.ID, Revision: s.Revision})
	local, e := m.Store.Capture(filepath.Join(m.Store.Root, "skills", s.ID))
	if e != nil || !reflect.DeepEqual(a, local) {
		t.Fatal("override not complete", e)
	}
	if _, ok := b.Entries["empty"]; !ok {
		t.Fatal("empty directory lost")
	}
	apply(t, m, Request{Action: "fork", ID: s.ID, Revision: s.Revision, Name: "fork-guide"})
	unchangedDoc, e := m.File(s.ID, "SKILL.md")
	if e != nil || unchangedDoc["content"] != string(doc) {
		t.Fatal("fork changed original", e)
	}
}
func TestAdoptionPlanBindsOldRevisionWithoutBindingCandidate(t *testing.T) {
	l := library(t)
	first := apply(t, l, Request{Action: "create", Name: "stable-guide", Description: "example"})
	id := first.SkillID
	rev := selectedSkill(t, l, id).Revision
	m := manager(t)
	attach(t, m, l)
	p, e := m.Plan(context.Background(), Request{Action: "inherit", ID: id, Revision: rev})
	if e != nil {
		t.Fatal(e)
	}
	apply(t, l, Request{Action: "edit", ID: id, Files: []Patch{{Path: "SKILL.md", Content: string(body("stable-guide")) + "new"}}})
	if _, e = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); e != nil {
		t.Fatal("new candidate invalidated approved old pin", e)
	}
	if effective(t, m, id).Skill.Revision != rev {
		t.Fatal("pin drift")
	}
}
func TestAdoptionRejectsRetirementAndProviderChanges(t *testing.T) {
	for _, change := range []string{"retirement", "root", "package", "record"} {
		t.Run(change, func(t *testing.T) {
			l := library(t)
			first := apply(t, l, Request{Action: "create", Name: "stale-guide", Description: "example"})
			id := first.SkillID
			s := selectedSkill(t, l, id)
			m := manager(t)
			attach(t, m, l)
			p, e := m.Plan(context.Background(), Request{Action: "inherit", ID: id, Revision: s.Revision})
			if e != nil {
				t.Fatal(e)
			}
			before := capture(t, m)
			switch change {
			case "retirement":
				apply(t, l, Request{Action: "retire", ID: id, Reason: "replaced"})
			case "root":
				attach(t, m, library(t))
			case "package":
				write(t, filepath.Join(l.Store.Root, filepath.FromSlash(revisionPath(s)), "SKILL.md"), body("stale-guide"))
			case "record":
				write(t, filepath.Join(l.Store.Root, filepath.FromSlash(recordPath(s))), []byte("{}"))
			}
			_, e = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest)
			code(t, e, "PLAN_STALE")
			unchangedManager(t, m, before)
		})
	}
}
func TestBaseStaleAndRestorationToSelectedPin(t *testing.T) {
	l := library(t)
	p := apply(t, l, Request{Action: "create", Name: "base-guide", Description: "example"})
	id := p.SkillID
	rev := selectedSkill(t, l, id).Revision
	m := manager(t)
	attach(t, m, l)
	apply(t, m, Request{Action: "inherit", ID: id, Revision: rev})
	apply(t, m, Request{Action: "override", ID: id, Revision: rev})
	apply(t, l, Request{Action: "edit", ID: id, Files: []Patch{{Path: "SKILL.md", Content: string(body("base-guide")) + "new"}}})
	next := selectedSkill(t, l, id).Revision
	if effective(t, m, id).Status == "BASE_STALE" {
		t.Fatal("candidate incorrectly invalidated base")
	}
	apply(t, m, Request{Action: "inherit", ID: id, Revision: next})
	if effective(t, m, id).Status != "BASE_STALE" {
		t.Fatal("selected pin mismatch not reported")
	}
	_, e := m.Plan(context.Background(), Request{Action: "restore-inheritance", ID: id, Revision: rev})
	code(t, e, "PLAN_STALE")
	apply(t, m, Request{Action: "restore-inheritance", ID: id, Revision: next})
	if effective(t, m, id).Skill.Revision != next {
		t.Fatal("wrong restore pin")
	}
}
func TestInheritanceRejectsCollisionsAndCorruptCache(t *testing.T) {
	l := library(t)
	p := apply(t, l, Request{Action: "create", Name: "common-guide", Description: "example"})
	id := p.SkillID
	s := selectedSkill(t, l, id)
	for _, kind := range []string{"id", "name", "duplicate", "cache", "identity", "path"} {
		t.Run(kind, func(t *testing.T) {
			m := manager(t)
			attach(t, m, l)
			if kind == "id" || kind == "name" {
				req := Request{Action: "create", Name: "local-guide", Description: "example"}
				if kind == "id" {
					req.ID = id
				} else {
					req.Name = s.Name
				}
				apply(t, m, req)
				before := capture(t, m)
				_, e := m.Plan(context.Background(), Request{Action: "inherit", ID: id, Revision: s.Revision})
				want := "SKILL_ID_COLLISION"
				if kind == "name" {
					want = "NATIVE_NAME_COLLISION"
				}
				code(t, e, want)
				unchangedManager(t, m, before)
				return
			}
			apply(t, m, Request{Action: "inherit", ID: id, Revision: s.Revision})
			if kind == "cache" {
				write(t, filepath.Join(m.Store.Root, filepath.FromSlash(revisionPath(s)), "assets", "extra.bin"), []byte{0})
				_, e := m.State()
				code(t, e, "REVISION_CORRUPT")
				return
			}
			b, e := os.ReadFile(filepath.Join(m.Store.Root, inheritanceFile))
			if e != nil {
				t.Fatal(e)
			}
			var meta Inheritance
			if e = json.Unmarshal(b, &meta); e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "duplicate":
				meta.Skills = append(meta.Skills, meta.Skills[0])
			case "identity":
				meta.Owner = workstore.ID()
			case "path":
				meta.Skills[0].Skill.Source.Path = "../escape"
			}
			write(t, filepath.Join(m.Store.Root, inheritanceFile), jsonBytes(meta))
			_, e = m.State()
			if e == nil {
				t.Fatal("unsafe inheritance accepted")
			}
		})
	}
}
func TestInheritanceFailureRestoresAllMetadata(t *testing.T) {
	l := library(t)
	p := apply(t, l, Request{Action: "create", Name: "failure-guide", Description: "example"})
	s := selectedSkill(t, l, p.SkillID)
	m := manager(t)
	attach(t, m, l)
	apply(t, m, Request{Action: "create", Name: "local-guide", Description: "example"})
	before := capture(t, m)
	plan, e := m.Plan(context.Background(), Request{Action: "inherit", ID: s.ID, Revision: s.Revision})
	if e != nil {
		t.Fatal(e)
	}
	m.Store.Hook = func(stage string, n int) error {
		if stage == "replaced" && n == 1 {
			return fmt.Errorf("injected failure")
		}
		return nil
	}
	_, e = m.Apply(context.Background(), plan.Plan.ID, plan.Plan.Digest)
	if e == nil {
		t.Fatal("injection did not fire")
	}
	m.Store.Hook = nil
	unchangedManager(t, m, before)
}
func TestSemanticRevisionCanonicalContract(t *testing.T) {
	m := manager(t)
	pkg := workstore.Tree{Entries: map[string]workstore.Entry{}}
	if e := m.Store.PutFile(&pkg, "SKILL.md", body("canonical-guide"), 0644); e != nil {
		t.Fatal(e)
	}
	s := Skill{Name: "canonical-guide", Files: packageFiles(pkg, ""), PackageFormat: map[string]string{"id": "native-skill", "version": "1"}, RevisionFormat: "semantic-v1"}
	base := packageRevision(s, pkg)
	s.DisplayName = "changed"
	s.Tags = []string{"tag"}
	s.Version = "human-label"
	s.Source.Path = "machine/elsewhere"
	if packageRevision(s, pkg) != base {
		t.Fatal("presentation affects revision")
	}
	s.Dependencies = map[string]any{}
	s.Requirements = map[string]any{}
	s.Compatibility = map[string]any{}
	if packageRevision(s, pkg) != base {
		t.Fatal("empty object normalization mismatch")
	}
	for _, kind := range []string{"dependencies", "compatibility", "requirements", "native_name", "format"} {
		t.Run(kind, func(t *testing.T) {
			copy := s
			switch kind {
			case "dependencies":
				copy.Dependencies = map[string]any{"project_files": []any{"src/main.go"}}
			case "compatibility":
				copy.Compatibility = map[string]any{"platforms": []any{"windows"}}
			case "requirements":
				copy.Requirements = map[string]any{"network": true}
			case "native_name":
				copy.Name = "renamed-guide"
			case "format":
				copy.PackageFormat = map[string]string{"id": "native-skill", "version": "2"}
			}
			if packageRevision(copy, pkg) == base {
				t.Fatal("semantic change reused revision")
			}
		})
	}
	// Golden locks the compact JSON encoding, empty values and tree digest.
	for p, e := range pkg.Entries {
		e.Mode = 0755
		pkg.Entries[p] = e
	}
	if packageRevision(s, pkg) != base {
		t.Fatal("platform mode representation changes semantic identity")
	}
	const golden = "sha256:d9d99287df909fd335c694f97ab54ebaa5b453114170b7fb30e922c2bd4578a6"
	if base != golden {
		t.Fatalf("semantic-v1 golden: %s", base)
	}
}
func TestGlobalAndInheritanceStrictSchemas(t *testing.T) {
	for _, b := range []string{`{"schema_version":1,"owner":"global","scope_id":"other"}`, `{"schema_version":1,"owner":"global","scope_id":"global","unknown":null}`} {
		if _, e := config.Validate("skill-library", []byte(b)); e == nil {
			t.Fatal("invalid library schema accepted")
		}
	}
}

func TestInheritedReferencesUseGlobalIdentity(t *testing.T) {
	l := library(t)
	p := apply(t, l, Request{Action: "create", Name: "reference-guide", Description: "example"})
	s := selectedSkill(t, l, p.SkillID)
	m := manager(t)
	attach(t, m, l)
	apply(t, m, Request{Action: "inherit", ID: s.ID, Revision: s.Revision})
	apply(t, m, Request{Action: "fork", ID: s.ID, Revision: s.Revision, Name: "fork-guide"})
	impact, e := m.References(s.ID)
	if e != nil || impact.Owner != "global" || impact.Scope != "global" {
		t.Fatal(impact, e)
	}
	count := 0
	for _, r := range impact.References {
		if r.Kind == "upstream" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("exact global upstream tuple was not included", impact)
	}
}
func TestInheritedProcessCrashHelper(t *testing.T) {
	if os.Getenv("AIDOCK_INHERITANCE_CRASH") != "1" {
		return
	}
	m, e := New(os.Getenv("AIDOCK_TEST_SOURCE"), os.Getenv("AIDOCK_TEST_LOCAL"), owner, "server")
	if e != nil {
		t.Fatal(e)
	}
	if e = m.AttachLibrary(os.Getenv("AIDOCK_TEST_LIBRARY")); e != nil {
		t.Fatal(e)
	}
	m.Store.Hook = func(phase string, n int) error {
		if phase == "replaced" && n == 2 {
			os.Exit(39)
		}
		return nil
	}
	_, e = m.Apply(context.Background(), os.Getenv("AIDOCK_TEST_PLAN"))
	t.Fatal("crash hook not reached", e)
}
func TestInheritedProcessCrashRecovery(t *testing.T) {
	l := library(t)
	p := apply(t, l, Request{Action: "create", Name: "crash-guide", Description: "example"})
	s := selectedSkill(t, l, p.SkillID)
	m := manager(t)
	attach(t, m, l)
	apply(t, m, Request{Action: "create", Name: "local-guide", Description: "example"})
	before := capture(t, m)
	provider := capture(t, l)
	p, e := m.Plan(context.Background(), Request{Action: "inherit", ID: s.ID, Revision: s.Revision})
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestInheritedProcessCrashHelper$")
	cmd.Env = append(os.Environ(), "AIDOCK_INHERITANCE_CRASH=1", "AIDOCK_TEST_SOURCE="+m.Store.Root, "AIDOCK_TEST_LOCAL="+m.Store.Local, "AIDOCK_TEST_LIBRARY="+l.Store.Root, "AIDOCK_TEST_PLAN="+p.Plan.ID)
	b, e := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(e, &exit) || exit.ExitCode() != 39 {
		t.Fatalf("expected process exit 39: %v %s", e, b)
	}
	fresh, e := New(m.Store.Root, m.Store.Local, owner, "server")
	if e != nil {
		t.Fatal(e)
	}
	state, e := fresh.State()
	if e != nil || !state.Pending {
		t.Fatal("missing recovery state", state, e)
	}
	n, e := fresh.Recover()
	if e != nil || n != 1 {
		t.Fatal(n, e)
	}
	unchangedManager(t, fresh, before)
	unchangedManager(t, l, provider)
	js, e := fresh.Store.Journals()
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, j := range js {
		if j.Plan.ID == p.Plan.ID && j.State == "recovered" && len(j.Plan.Inputs) == 4 {
			found = true
		}
	}
	if !found {
		t.Fatal("durable provenance was lost")
	}
}
