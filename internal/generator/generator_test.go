package generator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

const owner = "11111111-1111-4111-8111-111111111111"

var ctx = context.Background()

func write(t *testing.T, file string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, b, 0644); err != nil {
		t.Fatal(err)
	}
}
func apply(t *testing.T, s *source.Manager, r source.Request) source.Preview {
	t.Helper()
	p, err := s.Plan(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(ctx, p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	return p
}
func fixture(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := source.New(filepath.Join(dir, "project", ".aidock"), filepath.Join(dir, "local"), owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(dir, "project"), 0755); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(dir, "package")
	write(t, filepath.Join(pkg, "SKILL.md"), []byte("---\nname: fixture-guide\ndescription: generator fixture\n---\n[asset](assets/data.bin)\n"))
	write(t, filepath.Join(pkg, "assets", "data.bin"), []byte{0, 255, 13, 10, 128})
	write(t, filepath.Join(pkg, "scripts", "helper.py"), []byte("print('must not run')\n"))
	if err = os.MkdirAll(filepath.Join(pkg, "empty"), 0755); err != nil {
		t.Fatal(err)
	}
	p := apply(t, s, source.Request{Action: "import", PackagePath: pkg, SourceRole: "local-original"})
	apply(t, s, source.Request{Action: "enable", ID: p.SkillID})
	runtime := filepath.Join(dir, "runtime")
	write(t, filepath.Join(runtime, "generator.exe"), []byte("not an executable; must never be launched"))
	write(t, filepath.Join(runtime, "dependencies", "data.bin"), []byte{0, 1, 255})
	if err = os.MkdirAll(filepath.Join(runtime, "empty"), 0755); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "adapter.json")
	write(t, adapter, encode(Contract{1, "fixture-mirror", "1.0.0", Protocol, runtime, "generator.exe", []string{"claude-code", "codex-cli"}, "deny", "deny"}))
	return New(s), adapter
}
func stage(t *testing.T, m *Manager, adapter string) Observation {
	t.Helper()
	p, err := m.Inspect(ctx, adapter)
	if err != nil {
		t.Fatal(err)
	}
	o, err := m.Stage(ctx, adapter, p.Digest)
	if err != nil {
		t.Fatal(err)
	}
	return o
}
func returned(t *testing.T, m *Manager, s Stage) {
	t.Helper()
	if err := materialize(ctx, m.Source.Store, filepath.Join(s.Root, "output"), s.Preview.Expected); err != nil {
		t.Fatal(err)
	}
	if err := save(filepath.Join(s.Root, "output", "result.json"), receipt(s), false); err != nil {
		t.Fatal(err)
	}
}
func code(t *testing.T, err error, want string) {
	t.Helper()
	var classified *result.Error
	if !errors.As(err, &classified) || classified.Code != want {
		t.Fatalf("want %s got %v", want, err)
	}
}

func TestOfflineExchangeCompletePackageHistoryAndCleanup(t *testing.T) {
	m, adapter := fixture(t)
	before, err := m.Source.Store.Capture(m.Source.Store.Root)
	if err != nil {
		t.Fatal(err)
	}
	runtimeBefore, err := m.Source.Store.Capture(filepath.Join(filepath.Dir(adapter), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(filepath.Dir(m.Source.Store.Root), ".claude", "skills", "handwritten", "SKILL.md")
	write(t, native, []byte("handwritten original"))
	p, err := m.Inspect(ctx, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if p.ExecutionReady || p.PublicationReady || len(p.Source.Packages) != 1 {
		t.Fatal(p)
	}
	if _, err = os.Stat(m.area()); !os.IsNotExist(err) {
		t.Fatal("inspect materialized stage")
	}
	o := stage(t, m, adapter)
	s := o.Stage
	if o.State != "STAGED" || o.Stale || !o.CleanupSafe {
		t.Fatal(o)
	}
	for _, rel := range []string{"input/skills/fixture-guide/assets/data.bin", "runtime/dependencies/data.bin", "input/skills/fixture-guide/scripts/helper.py"} {
		b, err := os.ReadFile(filepath.Join(s.Root, filepath.FromSlash(rel)))
		if err != nil || len(b) == 0 {
			t.Fatal(rel, err)
		}
	}
	if i, err := os.Stat(filepath.Join(s.Root, "input", "skills", "fixture-guide", "empty")); err != nil || !i.IsDir() {
		t.Fatal(err)
	}
	request, err := os.ReadFile(filepath.Join(s.Root, "input", "request.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(request), m.Source.Store.Root) || strings.Contains(string(request), m.Source.Store.Local) {
		t.Fatal("host roots leaked into protocol")
	}
	returned(t, m, s)
	v, err := m.Validate(ctx, s.ID, s.Digest)
	if err != nil || v.Status != "PASS" || v.ExecutionReady || v.PublicationReady {
		t.Fatal(v, err)
	}
	loaded, err := New(m.Source).Show(ctx, s.ID)
	if err != nil || loaded.Validation == nil || loaded.Validation.Stale {
		t.Fatal(loaded, err)
	}
	after, err := m.Source.Store.Capture(m.Source.Store.Root)
	if err != nil || workstore.Digest(after) != workstore.Digest(before) {
		t.Fatal("source changed", err)
	}
	runtimeAfter, err := m.Source.Store.Capture(s.Preview.Contract.Runtime)
	if err != nil || workstore.Digest(runtimeAfter) != workstore.Digest(runtimeBefore) {
		t.Fatal("runtime changed", err)
	}
	if b, err := os.ReadFile(native); err != nil || string(b) != "handwritten original" {
		t.Fatal("native changed", err)
	}
	clean, err := m.Cleanup(ctx, s.ID, loaded.ObservationDigest)
	if err != nil || clean.State != "CLEANED" {
		t.Fatal(clean, err)
	}
	if _, err = os.Stat(s.Root); !os.IsNotExist(err) {
		t.Fatal("stage remains")
	}
	history, err := m.History(ctx)
	if err != nil || len(history) != 1 || history[0].State != "CLEANED" || history[0].Validation == nil || !history[0].Validation.Stale {
		t.Fatal(history, err)
	}
	if _, err = m.latest(s); err != nil {
		t.Fatal("validation lost", err)
	}
	if _, err = m.Source.Store.Data(v.Output.Entries[".agents/skills/fixture-guide/assets/data.bin"]); err != nil {
		t.Fatal("immutable returned bytes lost", err)
	}
	if _, err = m.Cleanup(ctx, s.ID, clean.ObservationDigest); err != nil {
		t.Fatal("cleanup not idempotent", err)
	}
}
func TestOutputValidationRejectsMissingExtraChangedAndReceipt(t *testing.T) {
	for _, kind := range []string{"missing", "extra", "changed", "empty-directory", "receipt", "input", "runtime"} {
		t.Run(kind, func(t *testing.T) {
			m, adapter := fixture(t)
			s := stage(t, m, adapter).Stage
			returned(t, m, s)
			asset := filepath.Join(s.Root, "output", ".agents", "skills", "fixture-guide", "assets", "data.bin")
			switch kind {
			case "missing":
				if err := os.Remove(asset); err != nil {
					t.Fatal(err)
				}
			case "extra":
				write(t, filepath.Join(s.Root, "output", ".claude", "settings.json"), []byte("{}"))
			case "changed":
				write(t, asset, []byte("changed binary"))
			case "empty-directory":
				if err := os.Remove(filepath.Join(s.Root, "output", ".claude", "skills", "fixture-guide", "empty")); err != nil {
					t.Fatal(err)
				}
			case "receipt":
				write(t, filepath.Join(s.Root, "output", "result.json"), []byte("{}"))
			case "input":
				write(t, filepath.Join(s.Root, "input", "skills", "fixture-guide", "SKILL.md"), []byte("changed"))
			case "runtime":
				write(t, filepath.Join(s.Root, "runtime", "dependencies", "data.bin"), []byte("changed"))
			}
			v, err := m.Validate(ctx, s.ID, s.Digest)
			if err != nil || v.Status != "FAIL" || len(v.Issues) == 0 || v.ExecutionReady || v.PublicationReady {
				t.Fatal(v, err)
			}
		})
	}
}
func TestPreviewAndRecordsBecomeStaleWhenDependencyOrSourceChanges(t *testing.T) {
	for _, kind := range []string{"dependency", "descriptor", "source"} {
		t.Run(kind, func(t *testing.T) {
			m, adapter := fixture(t)
			p, err := m.Inspect(ctx, adapter)
			if err != nil {
				t.Fatal(err)
			}
			s := stage(t, m, adapter).Stage
			returned(t, m, s)
			if _, err = m.Validate(ctx, s.ID, s.Digest); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "dependency":
				write(t, filepath.Join(p.Contract.Runtime, "dependencies", "data.bin"), []byte("changed"))
			case "descriptor":
				write(t, adapter, append(encode(p.Contract), ' '))
			case "source":
				apply(t, m.Source, source.Request{Action: "disable", ID: p.Source.Packages[0].Skill.ID})
			}
			_, err = m.Stage(ctx, adapter, p.Digest)
			if err == nil {
				t.Fatal("old preview accepted")
			}
			o, err := m.Show(ctx, s.ID)
			if err != nil || !o.Stale || o.Validation == nil || !o.Validation.Stale {
				t.Fatal(o, err)
			}
			_, err = m.Validate(ctx, s.ID, s.Digest)
			if err == nil {
				t.Fatal("old stage accepted")
			}
			if _, err = m.Cleanup(ctx, s.ID, o.ObservationDigest); err != nil {
				t.Fatal("stale input prevented owned cleanup", err)
			}
		})
	}
}
func TestCleanupPreservesChangesUnknownContentAndRecoveryMarkers(t *testing.T) {
	m, adapter := fixture(t)
	o := stage(t, m, adapter)
	write(t, filepath.Join(o.Stage.Root, "output", "new.bin"), []byte("new output"))
	_, err := m.Cleanup(ctx, o.Stage.ID, o.ObservationDigest)
	code(t, err, "GENERATOR_STAGE_STALE")
	unknown := filepath.Join(o.Stage.Root, "unowned.txt")
	write(t, unknown, []byte("preserve"))
	now, err := m.Show(ctx, o.Stage.ID)
	if err != nil || now.CleanupSafe {
		t.Fatal(now, err)
	}
	_, err = m.Cleanup(ctx, o.Stage.ID, now.ObservationDigest)
	code(t, err, "GENERATOR_RECOVERY_CONFLICT")
	if b, err := os.ReadFile(unknown); err != nil || string(b) != "preserve" {
		t.Fatal(err)
	}
	if err = os.Remove(unknown); err != nil {
		t.Fatal(err)
	}
	now, err = m.Show(ctx, o.Stage.ID)
	if err != nil {
		t.Fatal(err)
	}
	m.hook = func(point string) error {
		if point == "cleaning" {
			return fmt.Errorf("parent interrupted")
		}
		return nil
	}
	_, err = m.Cleanup(ctx, o.Stage.ID, now.ObservationDigest)
	if err == nil {
		t.Fatal("fault ignored")
	}
	fresh := New(m.Source)
	pending, err := fresh.Show(ctx, o.Stage.ID)
	if err != nil || pending.State != "CLEANING" {
		t.Fatal(pending, err)
	}
	_, err = fresh.Stage(ctx, adapter, o.Stage.Preview.Digest)
	code(t, err, "GENERATOR_CLEANUP_REQUIRED")
	clean, err := fresh.Cleanup(ctx, o.Stage.ID, pending.ObservationDigest)
	if err != nil || clean.State != "CLEANED" {
		t.Fatal(clean, err)
	}
	// Reappearing paths are never adopted through an old history record.
	write(t, filepath.Join(o.Stage.Root, "unowned.txt"), []byte("new occupant"))
	now, err = fresh.Show(ctx, o.Stage.ID)
	if err != nil || now.CleanupSafe {
		t.Fatal(now, err)
	}
	_, err = fresh.Cleanup(ctx, o.Stage.ID, now.ObservationDigest)
	code(t, err, "GENERATOR_RECOVERY_CONFLICT")
}
func TestCancellationAndConcurrentOutputMutationDoNotSaveAcceptance(t *testing.T) {
	for _, kind := range []string{"cancel-stage", "cancel-validate", "changed-output"} {
		t.Run(kind, func(t *testing.T) {
			m, adapter := fixture(t)
			c, cancel := context.WithCancel(ctx)
			defer cancel()
			if kind == "cancel-stage" {
				p, err := m.Inspect(ctx, adapter)
				if err != nil {
					t.Fatal(err)
				}
				m.hook = func(point string) error {
					if point == "marked" {
						cancel()
					}
					return nil
				}
				if _, err = m.Stage(c, adapter, p.Digest); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				history, err := New(m.Source).History(ctx)
				if err != nil || len(history) != 1 || history[0].State != "PREPARING" {
					t.Fatal(history, err)
				}
				if _, err = New(m.Source).Cleanup(ctx, history[0].Stage.ID, history[0].ObservationDigest); err != nil {
					t.Fatal(err)
				}
				return
			}
			s := stage(t, m, adapter).Stage
			returned(t, m, s)
			m.hook = func(point string) error {
				if point == "validated" {
					if kind == "cancel-validate" {
						cancel()
					} else {
						write(t, filepath.Join(s.Root, "output", "new.txt"), []byte("changed"))
					}
				}
				return nil
			}
			_, err := m.Validate(c, s.ID, s.Digest)
			if err == nil {
				t.Fatal("acceptance saved on mutation/cancel")
			}
			if v, err := m.latest(s); err != nil || v != nil {
				t.Fatal(v, err)
			}
		})
	}
}
func TestContractAndBindingBoundaries(t *testing.T) {
	m, adapter := fixture(t)
	p, err := m.Inspect(ctx, adapter)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"unknown-field", "escape", "overlap", "unknown-tool", "network", "missing"} {
		t.Run(kind, func(t *testing.T) {
			c := p.Contract
			switch kind {
			case "unknown-field":
				write(t, adapter, append(encode(c)[:len(encode(c))-1], []byte(",\"command\":\"bad\"}")...))
			case "escape":
				c.Entry = "../else.exe"
			case "overlap":
				c.Runtime = m.Source.Store.Root
			case "unknown-tool":
				c.Tools = []string{"unknown"}
			case "network":
				c.Network = "allow"
			case "missing":
				c.Entry = "missing.exe"
			}
			if kind != "unknown-field" {
				write(t, adapter, encode(c))
			}
			if _, err := m.Inspect(ctx, adapter); err == nil {
				t.Fatal("contract accepted", kind)
			}
		})
	}
	write(t, adapter, encode(p.Contract))
	s := stage(t, m, adapter).Stage
	other, err := source.New(filepath.Join(filepath.Dir(m.Source.Store.Root), "other", ".aidock"), m.Source.Store.Local, owner, "server")
	if err != nil {
		t.Fatal(err)
	}
	if records, err := New(other).History(ctx); err != nil || len(records) != 0 {
		t.Fatal("foreign stage leaked", err)
	}
	if _, err = New(other).Show(ctx, s.ID); err == nil {
		t.Fatal("foreign stage adopted")
	}
}

func TestMain(main *testing.M) {
	if len(os.Args) == 5 && os.Args[1] == "__generator_stage_crash" {
		s, err := source.New(os.Args[2], os.Args[3], owner, "server")
		if err != nil {
			os.Exit(94)
		}
		m := New(s)
		p, err := m.Inspect(ctx, os.Args[4])
		if err != nil {
			os.Exit(94)
		}
		m.hook = func(point string) error {
			if point == "marked" {
				os.Exit(92)
			}
			return nil
		}
		_, _ = m.Stage(ctx, os.Args[4], p.Digest)
		os.Exit(94)
	}
	os.Exit(main.Run())
}
func TestAbruptParentExitCanBeRecoveredByNewManager(t *testing.T) {
	m, adapter := fixture(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	output, err := exec.CommandContext(c, exe, "__generator_stage_crash", m.Source.Store.Root, m.Source.Store.Local, adapter).CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 92 {
		t.Fatal(err, string(output))
	}
	fresh := New(m.Source)
	history, err := fresh.History(ctx)
	if err != nil || len(history) != 1 || history[0].State != "PREPARING" {
		t.Fatal(history, err)
	}
	if _, err = fresh.Cleanup(ctx, history[0].Stage.ID, history[0].ObservationDigest); err != nil {
		t.Fatal(err)
	}
}

func TestRehashedFalsePassCannotBlessReturnedContent(t *testing.T) {
	m, adapter := fixture(t)
	s := stage(t, m, adapter).Stage
	v, err := m.Validate(ctx, s.ID, s.Digest)
	if err != nil || v.Status != "FAIL" {
		t.Fatal(v, err)
	}
	v.Status = "PASS"
	v.Issues = []string{}
	v.Digest = validationDigest(v)
	file := filepath.Join(m.base(), "validations", s.ID, v.ID+".json")
	if err = save(file, v, true); err != nil {
		t.Fatal(err)
	}
	o, err := m.Show(ctx, s.ID)
	if err != nil || !o.Stale || o.Validation != nil {
		t.Fatal("forged acceptance adopted", o, err)
	}
	if _, err = m.Cleanup(ctx, s.ID, o.ObservationDigest); err != nil {
		t.Fatal("corrupt validation prevented owned cleanup", err)
	}
}

func TestCleanupRechecksAfterStartingAndSupportsReadOnlyDependencies(t *testing.T) {
	m, adapter := fixture(t)
	p, err := m.Inspect(ctx, adapter)
	if err != nil {
		t.Fatal(err)
	}
	dependency := filepath.Join(p.Contract.Runtime, "dependencies", "data.bin")
	if err = os.Chmod(dependency, 0444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dependency, 0644)
	o := stage(t, m, adapter)
	s := o.Stage
	m.hook = func(point string) error {
		if point == "cleaning" {
			write(t, filepath.Join(s.Root, "output", "new.bin"), []byte("new"))
		}
		return nil
	}
	_, err = m.Cleanup(ctx, s.ID, o.ObservationDigest)
	code(t, err, "GENERATOR_STAGE_STALE")
	if b, err := os.ReadFile(filepath.Join(s.Root, "output", "new.bin")); err != nil || string(b) != "new" {
		t.Fatal("new file deleted", err)
	}
	fresh := New(m.Source)
	o, err = fresh.Show(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.Cleanup(ctx, s.ID, o.ObservationDigest); err != nil {
		t.Fatal("readonly cleanup failed", err)
	}
	info, err := os.Stat(dependency)
	if err != nil || info.Mode().Perm()&0200 != 0 {
		t.Fatal("original dependency permissions changed", err)
	}
}

func TestEmptyRootAfterMarkerRemovalRemainsRecoverable(t *testing.T) {
	m, adapter := fixture(t)
	s := stage(t, m, adapter).Stage
	if err := m.setState(s, "CLEANING"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err = os.RemoveAll(filepath.Join(s.Root, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	fresh := New(m.Source)
	o, err := fresh.Show(ctx, s.ID)
	if err != nil || !o.CleanupSafe || o.State != "CLEANING" {
		t.Fatal(o, err)
	}
	if _, err = fresh.Cleanup(ctx, s.ID, o.ObservationDigest); err != nil {
		t.Fatal("empty cleanup window unrecoverable", err)
	}
}
