package project

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
)

func setup(t *testing.T) (*Manager, string) {
	t.Helper()
	d := t.TempDir()
	m, e := New(filepath.Join(d, "local"))
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(d, "project")
	if e = os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	return m, root
}
func errorCode(t *testing.T, err error, want string) {
	t.Helper()
	var typed *result.Error
	if !errors.As(err, &typed) || typed.Code != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}
func TestCreateOpenCanonicalCatalogAndNoProjectWritesOnCancel(t *testing.T) {
	m, root := setup(t)
	ctx := context.Background()
	items, e := m.List()
	if e != nil || len(items) != 0 {
		t.Fatal(items, e)
	}
	if _, e = os.Stat(m.Local); !os.IsNotExist(e) {
		t.Fatal("empty list wrote local root")
	}
	p, e := m.Plan(ctx, Request{Root: root, Name: "项目 A", Create: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(root, ".aidock")); !os.IsNotExist(e) {
		t.Fatal("preview wrote project")
	}
	files, e := os.ReadDir(root)
	if e != nil || len(files) != 0 {
		t.Fatal("preview changed project directory, including lock sidecars", files, e)
	}
	h, e := m.Apply(ctx, p.ID, p.Digest)
	if e != nil || h.State != "committed" {
		t.Fatal(h, e)
	}
	b, e := m.Resolve(p.Binding.ID)
	if e != nil || b.Project != p.Request.Project || b.Copy != p.Request.Copy {
		t.Fatal(b, e)
	}
	s, e := source.New(b.Root, m.Local, b.Project, b.Scope)
	if e != nil {
		t.Fatal(e)
	}
	state, e := s.State()
	if e != nil || len(state.Index.Skills) != 0 {
		t.Fatal(state, e)
	}
	r, e := replica.New(s)
	if e != nil {
		t.Fatal(e)
	}
	registry, e := r.List()
	if e != nil || len(registry.Copies) != 1 || registry.Copies[0].ID != b.Copy {
		t.Fatal(registry, e)
	}
	_, e = m.Plan(ctx, Request{Root: root, Name: "duplicate", Create: false})
	errorCode(t, e, "PROJECT_ALREADY_REGISTERED")
	_, e = m.Apply(ctx, p.ID, p.Digest)
	if e != nil {
		t.Fatal("repeat confirmation", e)
	}
	other, e := New(filepath.Join(filepath.Dir(m.Local), "other-local"))
	if e != nil {
		t.Fatal(e)
	}
	open, e := other.Plan(ctx, Request{Root: root, Name: "已有项目", Create: false})
	if e != nil {
		t.Fatal(e)
	}
	_, e = other.Apply(ctx, open.ID, open.Digest)
	if e != nil {
		t.Fatal(e)
	}
	if open.Binding.Project != b.Project || open.Binding.Scope != b.Scope {
		t.Fatal("existing identity replaced")
	}
}
func TestStaleSourceAndRegistryDoNotInstall(t *testing.T) {
	for _, registry := range []bool{false, true} {
		t.Run(map[bool]string{false: "source", true: "registry"}[registry], func(t *testing.T) {
			m, root := setup(t)
			ctx := context.Background()
			p, e := m.Plan(ctx, Request{Root: root, Name: "A", Create: true})
			if e != nil {
				t.Fatal(e)
			}
			if !registry {
				if e = os.Mkdir(filepath.Join(root, ".aidock"), 0700); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(filepath.Join(root, ".aidock", "keep.txt"), []byte("external"), 0600); e != nil {
					t.Fatal(e)
				}
			} else {
				r2 := filepath.Join(filepath.Dir(root), "project-2")
				os.Mkdir(r2, 0700)
				p2, e := m.Plan(ctx, Request{Root: r2, Name: "B", Create: true, Project: p.Request.Project})
				if e != nil {
					t.Fatal(e)
				}
				if _, e = m.Apply(ctx, p2.ID, p2.Digest); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = m.Apply(ctx, p.ID, p.Digest); e == nil {
				t.Fatal("stale plan installed")
			}
			if _, e = os.Stat(filepath.Join(root, ".aidock", "project.yaml")); !os.IsNotExist(e) {
				t.Fatal("stale plan changed original project")
			}
		})
	}
}
func TestRegistrationFailureRecoversBothRoots(t *testing.T) {
	m, root := setup(t)
	ctx := context.Background()
	p, e := m.Plan(ctx, Request{Root: root, Name: "A", Create: true})
	if e != nil {
		t.Fatal(e)
	}
	m.Hook = func(stage string, i int) error {
		if stage == "target-complete" && i == 0 {
			return errors.New("injected")
		}
		return nil
	}
	if _, e = m.Apply(ctx, p.ID, p.Digest); e == nil {
		t.Fatal("fault missed")
	}
	h, e := m.History()
	if e != nil || len(h) != 1 || h[0].Pending || h[0].State != "recovered" {
		t.Fatal(h, e)
	}
	if _, e = os.Stat(filepath.Join(root, ".aidock")); !os.IsNotExist(e) {
		t.Fatal("source not restored")
	}
	items, e := m.List()
	if e != nil || len(items) != 0 {
		t.Fatal(items, e)
	}
}
func TestProjectCrashHelper(t *testing.T) {
	if os.Getenv("AIDOCK_PROJECT_CRASH") != "1" {
		return
	}
	m, e := New(os.Getenv("AIDOCK_PROJECT_LOCAL"))
	if e != nil {
		t.Fatal(e)
	}
	m.Hook = func(stage string, i int) error {
		if stage == "target-complete" && i == 0 {
			os.Exit(92)
		}
		return nil
	}
	_, e = m.Apply(context.Background(), os.Getenv("AIDOCK_PROJECT_PLAN"), os.Getenv("AIDOCK_PROJECT_DIGEST"))
	t.Fatal("crash not reached", e)
}
func TestActualProcessExitRecovery(t *testing.T) {
	m, root := setup(t)
	p, e := m.Plan(context.Background(), Request{Root: root, Name: "A", Create: true})
	if e != nil {
		t.Fatal(e)
	}
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(exe, "-test.run=^TestProjectCrashHelper$")
	cmd.Env = append(os.Environ(), "AIDOCK_PROJECT_CRASH=1", "AIDOCK_PROJECT_LOCAL="+m.Local, "AIDOCK_PROJECT_PLAN="+p.ID, "AIDOCK_PROJECT_DIGEST="+p.Digest)
	out, e := cmd.CombinedOutput()
	if x, ok := e.(*exec.ExitError); !ok || x.ExitCode() != 92 {
		t.Fatal(e, string(out))
	}
	fresh, e := New(m.Local)
	if e != nil {
		t.Fatal(e)
	}
	h, e := fresh.History()
	if e != nil || len(h) != 1 || !h[0].Pending {
		t.Fatal(h, e)
	}
	_, e = fresh.Recover(p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(root, ".aidock")); !os.IsNotExist(e) {
		t.Fatal("source survived recovery")
	}
	items, e := fresh.List()
	if e != nil || len(items) != 0 {
		t.Fatal(items, e)
	}
}

func TestConflictingEditPreservedAndRecoveryCanRetry(t *testing.T) {
	m, root := setup(t)
	p, e := m.Plan(context.Background(), Request{Root: root, Name: "A", Create: true})
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, ".aidock", "profile.yaml")
	var installed []byte
	m.Hook = func(stage string, i int) error {
		if stage == "target-complete" && i == 0 {
			installed, e = os.ReadFile(path)
			if e != nil {
				return e
			}
			if e = os.WriteFile(path, []byte("user edit"), 0600); e != nil {
				return e
			}
			return errors.New("interrupted with external edit")
		}
		return nil
	}
	_, e = m.Apply(context.Background(), p.ID, p.Digest)
	errorCode(t, e, "RECOVERY_CONFLICT")
	fresh, e := New(m.Local)
	if e != nil {
		t.Fatal(e)
	}
	h, e := fresh.History()
	if e != nil || len(h) != 1 || !h[0].Pending {
		t.Fatal(h, e)
	}
	_, e = fresh.Recover(p.ID)
	errorCode(t, e, "RECOVERY_CONFLICT")
	b, e := os.ReadFile(path)
	if e != nil || string(b) != "user edit" {
		t.Fatal("external edit lost", e)
	}
	// Simulate the user resolving the conflict to the saved installed bytes.
	if e = os.WriteFile(path, installed, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = fresh.Recover(p.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(root, ".aidock")); !os.IsNotExist(e) {
		t.Fatal("source was not restored")
	}
}

func TestPostCommitCleanupAndCanonicalCopyCatalog(t *testing.T) {
	m, root := setup(t)
	p, e := m.Plan(context.Background(), Request{Root: root, Name: "A", Create: true})
	if e != nil {
		t.Fatal(e)
	}
	m.Hook = func(stage string, _ int) error {
		if stage == "committed" {
			return errors.New("post-commit stop")
		}
		return nil
	}
	_, e = m.Apply(context.Background(), p.ID, p.Digest)
	errorCode(t, e, "COORDINATED_RECOVERY_REQUIRED")
	h, e := m.History()
	if e != nil || len(h) != 1 || h[0].State != "committed" || !h[0].Pending {
		t.Fatal(h, e)
	}
	m.Hook = nil
	if _, e = m.Recover(p.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(root, ".aidock", "project.yaml")); e != nil {
		t.Fatal("cleanup removed committed source", e)
	}
	h, e = m.History()
	if e != nil || h[0].Pending || h[0].State != "committed" {
		t.Fatal(h, e)
	}
	root2 := filepath.Join(filepath.Dir(root), "second-copy")
	if e = os.Mkdir(root2, 0700); e != nil {
		t.Fatal(e)
	}
	p2, e := m.Plan(context.Background(), Request{Root: root2, Name: "B", Create: true, Project: p.Binding.Project, Scope: p.Binding.Scope})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Apply(context.Background(), p2.ID, p2.Digest); e != nil {
		t.Fatal(e)
	}
	items, e := m.List()
	if e != nil || len(items) != 2 || items[0].Project != items[1].Project || items[0].Copy == items[1].Copy {
		t.Fatal(items, e)
	}
	s, _ := source.New(p.Binding.Root, m.Local, p.Binding.Project, p.Binding.Scope)
	r, _ := replica.New(s)
	registry, e := r.List()
	if e != nil || len(registry.Copies) != 2 {
		t.Fatal(registry, e)
	}
}

func TestInvalidLabelsAndPlanForeignLocalDoNotWrite(t *testing.T) {
	m, root := setup(t)
	for _, req := range []Request{{Root: root, Name: "bad\nname", Create: true}, {Root: root, Name: "A", Scope: "bad\tscope", Create: true}, {Root: filepath.Join(root, "missing"), Name: "A", Create: true}} {
		if _, e := m.Plan(context.Background(), req); e == nil {
			t.Fatal("invalid request accepted", req)
		}
	}
	p, e := m.Plan(context.Background(), Request{Root: root, Name: "A", Create: true})
	if e != nil {
		t.Fatal(e)
	}
	other, _ := New(filepath.Join(filepath.Dir(m.Local), "foreign"))
	if _, e = other.Apply(context.Background(), p.ID, p.Digest); e == nil {
		t.Fatal("foreign local applied plan")
	}
	if _, e = m.Apply(context.Background(), p.ID, "wrong"); e == nil {
		t.Fatal("bad digest accepted")
	}
	if _, e = os.Stat(filepath.Join(root, ".aidock")); !os.IsNotExist(e) {
		t.Fatal("negative request wrote source")
	}
}

func TestProjectPathNormalizesBeforeReview(t *testing.T) {
	m, root := setup(t)
	p, e := m.Plan(context.Background(), Request{Root: root + string(filepath.Separator), Name: "A", Create: true})
	if e != nil || p.Request.Root != root || p.Binding.Root != filepath.Join(root, ".aidock") {
		t.Fatal(p, e)
	}
	if _, e = m.Apply(context.Background(), p.ID, p.Digest); e != nil {
		t.Fatal(e)
	}
}
