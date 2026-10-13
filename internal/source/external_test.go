package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/bridge"
)

func externalFixture(t *testing.T, m *Manager) (bridge.Registration, string) {
	t.Helper()
	d := t.TempDir()
	b := bridge.Registration{ID: "33333333-3333-4333-8333-333333333333", Project: owner, Scope: "server", Copy: "test", Root: filepath.Join(d, "external"), SourcePath: "skills", SourceRole: "external-source", Format: "native-skill", Packages: []string{"guide"}}
	write(t, filepath.Join(b.Root, "skills/guide/SKILL.md"), append(body("external-guide"), []byte("\n[reference](references/guide.md)\n")...))
	write(t, filepath.Join(b.Root, "skills/guide/references/guide.md"), []byte("first"))
	write(t, filepath.Join(b.Root, "skills/guide/assets/data.bin"), []byte{0, 1, 255})
	if err := os.MkdirAll(filepath.Join(b.Root, "skills/guide/empty"), 0755); err != nil {
		t.Fatal(err)
	}
	m.BridgeFile, m.Copy = filepath.Join(d, "bridge.json"), "test"
	write(t, m.BridgeFile, jsonBytes(bridge.Index{Version: 1, Bridges: []bridge.Registration{b}}))
	return b, filepath.Join(b.Root, "skills/guide/assets/data.bin")
}
func adoption(t *testing.T, m *Manager) Request {
	t.Helper()
	idx, err := bridge.Load(m.BridgeFile)
	if err != nil {
		t.Fatal(err)
	}
	report, err := bridge.Check(context.Background(), idx)
	if err != nil || report.Bridges[0].CurrentRevision == "" {
		t.Fatal(report, err)
	}
	b, c := report.Bridges[0], report.Bridges[0].Candidates[0]
	return Request{Action: "external-adopt", BridgeID: b.ID, PackagePath: c.Skill.Path, Revision: c.Revision, BridgeRevision: b.CurrentRevision}
}
func TestExternalApprovalOfflineHistoryRollbackUndo(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "local-guide", Description: "fixture"})
	b, binary := externalFixture(t, m)
	sourceBefore, err := m.Store.Capture(b.Root)
	if err != nil {
		t.Fatal(err)
	}
	first := apply(t, m, adoption(t, m))
	id := first.SkillID
	a := current(t, m).External[0]
	if effective(t, m, id).Layer != "external" || !effective(t, m, id).ReadOnly || len(current(t, m).Index.Skills) != 1 {
		t.Fatal("external copied into owned index")
	}
	if _, err := os.Stat(filepath.Join(m.Store.Root, "skills", id)); !os.IsNotExist(err) {
		t.Fatal("editable duplicate created")
	}
	if switches(current(t, m).Profile)["disable"].([]any)[0] != id {
		t.Fatal("new approval not disabled")
	}
	apply(t, m, Request{Action: "enable", ID: id})
	write(t, binary, []byte{0, 2, 254})
	if effective(t, m, id).Skill.Revision != a.Skill.Revision {
		t.Fatal("external change updated pin automatically")
	}
	second := apply(t, m, adoption(t, m))
	if second.SkillID != id || current(t, m).External[0].CandidateRevision == a.CandidateRevision {
		t.Fatal("update lost identity or ignored binary change")
	}
	latest := capture(t, m)
	if len(current(t, m).ExternalHistory) != 2 {
		t.Fatal("missing approvals")
	}
	reverted := apply(t, m, Request{Action: "rollback", Snapshot: second.Plan.ID})
	if effective(t, m, id).Skill.Revision != a.Skill.Revision || len(current(t, m).ExternalHistory) != 2 {
		t.Fatal("rollback removed history")
	}
	apply(t, m, Request{Action: "rollback", Snapshot: reverted.Plan.ID})
	unchanged(t, latest, capture(t, m))
	// Offline selection restores the old package, including binaries/empty dirs,
	// while leaving the external authority exactly as the user changed it.
	if err := os.Rename(b.Root, b.Root+"-offline"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(m.BridgeFile); err != nil {
		t.Fatal(err)
	}
	selected := apply(t, m, Request{Action: "external-select", ID: id, Approval: a.ApprovalID})
	f, err := m.File(id, "references/guide.md")
	if err != nil || f["content"] != "first" || f["read_only"] != true {
		t.Fatal(f, err)
	}
	pkg, err := m.cached(nil, effective(t, m, id).Skill)
	if err != nil || !pkg.Entries["empty"].Dir {
		t.Fatal("empty dir lost", err)
	}
	data, err := m.Store.Data(pkg.Entries["assets/data.bin"])
	if err != nil || string(data) != string([]byte{0, 1, 255}) {
		t.Fatal(data, err)
	}
	apply(t, m, Request{Action: "rollback", Snapshot: selected.Plan.ID})
	if effective(t, m, id).Skill.Revision == a.Skill.Revision {
		t.Fatal("undo selection did not restore newer pin")
	}
	write(t, filepath.Join(b.Root+"-offline", "skills/guide/assets/data.bin"), []byte{0, 1, 255})
	externalAfter, err := m.Store.Capture(b.Root + "-offline")
	if err != nil {
		t.Fatal(err)
	}
	unchanged(t, sourceBefore, externalAfter)
}
func TestExternalReadOnlyAndCopyBoundaries(t *testing.T) {
	m := manager(t)
	b, _ := externalFixture(t, m)
	r := adoption(t, m)
	for _, change := range []func(){func() { m.Copy = "wrong" }, func() { m.Scope = "client" }, func() { m.Owner = "22222222-2222-4222-8222-222222222222" }} {
		change()
		_, err := m.Plan(context.Background(), r)
		code(t, err, "SOURCE_IDENTITY_MISMATCH")
		m.Copy, m.Scope, m.Owner = "test", "server", owner
	}
	p := apply(t, m, r)
	before := capture(t, m)
	for _, action := range []string{"edit", "rename", "governance", "metadata", "retire", "fork", "override"} {
		_, err := m.Plan(context.Background(), Request{Action: action, ID: p.SkillID})
		code(t, err, "EXTERNAL_READ_ONLY")
		unchanged(t, before, capture(t, m))
	}
	// Scan metadata and cache metadata are observations, never upstream writes.
	got, err := os.ReadFile(filepath.Join(b.Root, "skills/guide/assets/data.bin"))
	if err != nil || string(got) != string([]byte{0, 1, 255}) {
		t.Fatal(got, err)
	}
}
func TestExternalPlanStaleForSourceRegistrationAndCopy(t *testing.T) {
	for _, kind := range []string{"binary", "registration", "copy", "missing", "other-package"} {
		t.Run(kind, func(t *testing.T) {
			m := manager(t)
			b, binary := externalFixture(t, m)
			r := adoption(t, m)
			p, err := m.Plan(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			before := capture(t, m)
			switch kind {
			case "binary":
				write(t, binary, []byte{0, 2, 255})
			case "registration":
				write(t, m.BridgeFile, append(jsonBytes(bridge.Index{Version: 1, Bridges: []bridge.Registration{b}}), '\n'))
			case "copy":
				m.Copy = "other"
			case "missing":
				if err := os.Rename(b.Root, b.Root+"-missing"); err != nil {
					t.Fatal(err)
				}
			case "other-package":
				b.Packages = nil
				write(t, m.BridgeFile, jsonBytes(bridge.Index{Version: 1, Bridges: []bridge.Registration{b}}))
				write(t, filepath.Join(b.Root, "skills/other/SKILL.md"), body("other"))
			}
			_, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest)
			code(t, err, "PLAN_STALE")
			unchanged(t, before, capture(t, m))
		})
	}
}
func TestExternalWriteFailureRecoversWholeSelection(t *testing.T) {
	for _, crash := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "interruption"}[crash], func(t *testing.T) {
			m := manager(t)
			_, binary := externalFixture(t, m)
			apply(t, m, adoption(t, m))
			before := capture(t, m)
			write(t, binary, []byte{0, 3, 254})
			p, err := m.Plan(context.Background(), adoption(t, m))
			if err != nil {
				t.Fatal(err)
			}
			m.Store.Hook = func(stage string, n int) error {
				if stage == "replaced" && n == 2 {
					if crash {
						panic("simulated crash")
					}
					return errors.New("fixture failure")
				}
				return nil
			}
			if crash {
				func() {
					defer func() {
						if recover() == nil {
							t.Fatal("no interruption")
						}
					}()
					m.Apply(context.Background(), p.Plan.ID)
				}()
			} else {
				if _, err = m.Apply(context.Background(), p.Plan.ID); err == nil {
					t.Fatal("no failure")
				}
			}
			m.Store.Hook = nil
			if crash {
				fresh, err := New(m.Store.Root, m.Store.Local, m.Owner, m.Scope)
				if err != nil {
					t.Fatal(err)
				}
				fresh.Copy, fresh.BridgeFile = m.Copy, m.BridgeFile
				m = fresh
				if _, err = m.Plan(context.Background(), Request{Action: "disable", ID: p.SkillID}); err == nil {
					t.Fatal("pending transaction accepted new write")
				}
				if _, err = m.Recover(); err != nil {
					t.Fatal(err)
				}
			}
			unchanged(t, before, capture(t, m))
			if len(current(t, m).ExternalHistory) != 1 {
				t.Fatal("uncommitted approval remained")
			}
		})
	}
}
func TestExternalCorruptCacheAndCollisionBlock(t *testing.T) {
	m := manager(t)
	_, _ = externalFixture(t, m)
	apply(t, m, Request{Action: "create", Name: "external-guide", Description: "collision"})
	before := capture(t, m)
	_, err := m.Plan(context.Background(), adoption(t, m))
	code(t, err, "NATIVE_NAME_COLLISION")
	unchanged(t, before, capture(t, m))
	m = manager(t)
	_, _ = externalFixture(t, m)
	p := apply(t, m, adoption(t, m))
	s := effective(t, m, p.SkillID).Skill
	write(t, filepath.Join(m.Store.Root, filepath.FromSlash(revisionPath(s)), "assets/data.bin"), []byte("tampered"))
	_, err = m.File(s.ID, "assets/data.bin")
	code(t, err, "REVISION_CORRUPT")
}
func TestExternalRootsRejectBeforeLocalWrites(t *testing.T) {
	m := manager(t)
	b, _ := externalFixture(t, m)
	m.Store.Local = filepath.Join(b.Root, "local")
	_, err := m.Plan(context.Background(), Request{Action: "external-adopt"})
	code(t, err, "INVALID_ROOTS")
	if _, err := os.Stat(m.Store.Local); !os.IsNotExist(err) {
		t.Fatal("local root created in authority")
	}
	// Strict request decoding never accepts a caller-selected authority or copy.
	for _, extra := range []string{`"copy":"foreign"`, `"root":"foreign"`, `"files":[]`} {
		var r Request
		err := r.UnmarshalJSON([]byte(`{"action":"external-adopt",` + extra + `}`))
		if err == nil {
			err = validateRequest(r)
		}
		if err == nil || !strings.Contains(err.Error(), "field") && !strings.Contains(err.Error(), "字段") {
			t.Fatal("unrelated field accepted", extra, err)
		}
	}
}

func TestExternalReadoptionAfterRollbackPreservesIdentity(t *testing.T) {
	m := manager(t)
	apply(t, m, Request{Action: "create", Name: "owned", Description: "fixture"})
	_, _ = externalFixture(t, m)
	p := apply(t, m, adoption(t, m))
	apply(t, m, Request{Action: "rollback", Snapshot: p.Plan.ID})
	if len(current(t, m).External) != 0 || len(current(t, m).ExternalHistory) != 1 {
		t.Fatal("rollback did not preserve approved history")
	}
	again := apply(t, m, adoption(t, m))
	if again.SkillID != p.SkillID {
		t.Fatal("same external package got a new stable ID")
	}
}

func TestExternalAlternateHomeCannotWriteInsideAuthority(t *testing.T) {
	m := manager(t)
	b, _ := externalFixture(t, m)
	apply(t, m, adoption(t, m))
	local := filepath.Join(b.Root, "redirected-home")
	_, err := New(m.Store.Root, local, owner, "server")
	code(t, err, "INVALID_ROOTS")
	if _, err := os.Stat(local); !os.IsNotExist(err) {
		t.Fatal("alternate home changed authority")
	}
}

func TestExternalRepeatedApprovalNeverRewritesCacheModes(t *testing.T) {
	m := manager(t)
	_, binary := externalFixture(t, m)
	first := apply(t, m, adoption(t, m))
	s := effective(t, m, first.SkillID).Skill
	before, err := m.cached(nil, s)
	if err != nil {
		t.Fatal(err)
	}
	// Non-executable permissions are deliberately not portable identity. A
	// repeated approval still must not rewrite the first complete cache.
	if err := os.Chmod(binary, 0444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(binary, 0644) })
	second := apply(t, m, adoption(t, m))
	after, err := m.cached(nil, effective(t, m, second.SkillID).Skill)
	if err != nil {
		t.Fatal(err)
	}
	unchanged(t, before, after)
}
