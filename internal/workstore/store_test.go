package workstore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/result"
)

func fixture(t *testing.T) (*Store, Tree, Plan) {
	t.Helper()
	d := t.TempDir()
	s := &Store{Root: filepath.Join(d, ".aidock"), Local: filepath.Join(d, "local")}
	if e := os.Mkdir(s.Root, 0755); e != nil {
		t.Fatal(e)
	}
	for p, b := range map[string][]byte{"a.txt": []byte("old A"), "b.bin": {0, 255, 1}, "draft.txt": []byte("unsaved source draft")} {
		if e := os.WriteFile(filepath.Join(s.Root, p), b, 0644); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.Mkdir(filepath.Join(s.Root, "empty"), 0755); e != nil {
		t.Fatal(e)
	}
	before, e := s.Capture(s.Root)
	if e != nil {
		t.Fatal(e)
	}
	after := Clone(before)
	for p, b := range map[string][]byte{"a.txt": []byte("new A"), "b.bin": {0, 254, 2}} {
		if e = s.PutFile(&after, p, b, 0644); e != nil {
			t.Fatal(e)
		}
	}
	p, e := s.Preview("edit", before, after)
	if e != nil {
		t.Fatal(e)
	}
	return s, before, p
}
func errorCode(t *testing.T, e error, want string) {
	t.Helper()
	var v *result.Error
	if !errors.As(e, &v) || v.Code != want {
		t.Fatalf("want %s, got %v", want, e)
	}
}

func TestLegacyPlanDigestAndApplyRemainCompatible(t *testing.T) {
	s, _, p := fixture(t)
	// Recreate the v0.2 serialized field order, before asset_effects existed.
	type legacyPlan struct {
		Version int      `json:"schema_version"`
		ID      string   `json:"id"`
		Kind    string   `json:"kind"`
		Root    string   `json:"root"`
		Action  string   `json:"action"`
		Created string   `json:"created"`
		Before  Tree     `json:"before"`
		After   Tree     `json:"after"`
		Changes []Change `json:"changes"`
		Digest  string   `json:"digest"`
	}
	old := legacyPlan{p.Version, p.ID, p.Kind, p.Root, p.Action, p.Created, p.Before, p.After, p.Changes, ""}
	b, _ := json.Marshal(old)
	old.Digest = Hash(b)
	if old.Digest != p.Digest {
		t.Fatal("legacy digest changed")
	}
	b, _ = json.Marshal(old)
	if e := os.WriteFile(filepath.Join(s.bucket(), "plans", p.ID+".json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	loaded, e := s.LoadPlan(p.ID)
	if e != nil || len(loaded.AssetEffects) != 0 {
		t.Fatal(loaded, e)
	}
	j, e := s.Apply(context.Background(), p.ID)
	if e != nil || j.State != "committed" {
		t.Fatal(j, e)
	}
}
func equalTree(t *testing.T, s *Store, want Tree) {
	t.Helper()
	got, e := s.Capture(s.Root)
	if e != nil || Digest(got) != Digest(want) {
		t.Fatalf("tree changed: %v\n%+v", e, Changes(want, got))
	}
}

func TestCommitConsumedAndTargetLockAcrossLocalRoots(t *testing.T) {
	s, _, p := fixture(t)
	if e := s.Locked(func() error {
		other := &Store{Root: s.Root, Local: filepath.Join(t.TempDir(), "other")}
		e := other.Locked(func() error { return nil })
		errorCode(t, e, "SOURCE_BUSY")
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	j, e := s.Apply(context.Background(), p.ID)
	if e != nil || j.State != "committed" {
		t.Fatal(j, e)
	}
	equalTree(t, s, p.After)
	_, e = s.Apply(context.Background(), p.ID)
	errorCode(t, e, "PLAN_CONSUMED")
}
func TestFailedSaveRestoresBinaryEmptyDirectoriesAndDraft(t *testing.T) {
	s, before, p := fixture(t)
	s.Hook = func(stage string, n int) error {
		if stage == "replaced" && n == 2 {
			return errors.New("injected write failure")
		}
		return nil
	}
	j, e := s.Apply(context.Background(), p.ID)
	errorCode(t, e, "SOURCE_APPLY_FAILED")
	if j.State != "recovered" {
		t.Fatal(j.State)
	}
	equalTree(t, s, before)
	if e = s.Pending(); e != nil {
		t.Fatal(e)
	}
}
func TestPlanStalePreservesExternalEdit(t *testing.T) {
	s, _, p := fixture(t)
	external := []byte("external editor")
	if e := os.WriteFile(filepath.Join(s.Root, "a.txt"), external, 0644); e != nil {
		t.Fatal(e)
	}
	_, e := s.Apply(context.Background(), p.ID)
	errorCode(t, e, "PLAN_STALE")
	b, _ := os.ReadFile(filepath.Join(s.Root, "a.txt"))
	if string(b) != string(external) {
		t.Fatal("external edit overwritten")
	}
	js, e := s.Journals()
	if e != nil || len(js) != 0 {
		t.Fatal(js, e)
	}
}
func TestAssetsAndPlanCorruptionRejectedBeforeWrite(t *testing.T) {
	for _, kind := range []string{"blob", "preview", "digest", "root", "kind", "journal"} {
		t.Run(kind, func(t *testing.T) {
			s, before, p := fixture(t)
			switch kind {
			case "blob", "preview":
				if e := os.Remove(filepath.Join(s.bucket(), "blobs", p.After.Entries["a.txt"].Hash)); e != nil {
					t.Fatal(e)
				}
				var e error
				if kind == "preview" {
					_, e = s.Preview("rollback", before, p.After)
				} else {
					_, e = s.Apply(context.Background(), p.ID)
				}
				errorCode(t, e, "BACKUP_INVALID")
			case "journal":
				j := Journal{Plan: p, State: "prepared"}
				j.Plan.Action = "tampered"
				if e := s.save(j); e != nil {
					t.Fatal(e)
				}
				_, e := s.Recover()
				errorCode(t, e, "RECOVERY_CONFLICT")
			default:
				switch kind {
				case "digest":
					p.Action = "tampered"
				case "root":
					p.Root = filepath.Join(t.TempDir(), "other")
					p.Digest = planDigest(p)
				case "kind":
					p.Kind = "replica"
					p.Digest = planDigest(p)
				}
				b, _ := json.Marshal(p)
				if e := os.WriteFile(filepath.Join(s.bucket(), "plans", p.ID+".json"), b, 0600); e != nil {
					t.Fatal(e)
				}
				_, e := s.Apply(context.Background(), p.ID)
				if e == nil {
					t.Fatal("corrupt plan accepted")
				}
			}
			equalTree(t, s, before)
		})
	}
	s, _, _ := fixture(t)
	if _, e := s.Data(Entry{Hash: strings.Repeat("../", 21) + "x"}); e == nil {
		t.Fatal("invalid blob path accepted")
	}
}

// The test process exits without defers after staging or replacing a file.
// Kernel locks must release, and a fresh process must recover from the journal.
func TestCrashHelper(t *testing.T) {
	if os.Getenv("AIDOCK_TEST_CRASH") != "1" {
		return
	}
	s := &Store{Root: os.Getenv("AIDOCK_TEST_ROOT"), Local: os.Getenv("AIDOCK_TEST_LOCAL")}
	s.Hook = func(stage string, n int) error {
		if stage == os.Getenv("AIDOCK_TEST_STAGE") && (stage == "staged" || n == 1) {
			os.Exit(37)
		}
		return nil
	}
	_, _ = s.Apply(context.Background(), os.Getenv("AIDOCK_TEST_PLAN"))
	os.Exit(38)
}
func crash(t *testing.T, s *Store, p Plan, stage string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
	cmd.Env = append(os.Environ(), "AIDOCK_TEST_CRASH=1", "AIDOCK_TEST_ROOT="+s.Root, "AIDOCK_TEST_LOCAL="+s.Local, "AIDOCK_TEST_PLAN="+p.ID, "AIDOCK_TEST_STAGE="+stage)
	b, e := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(e, &exit) || exit.ExitCode() != 37 {
		t.Fatalf("crash helper: %v %s", e, b)
	}
}
func TestProcessCrashRecoveryAndExternalConflict(t *testing.T) {
	for _, scenario := range []string{"staged", "replaced", "external-target", "external-unrelated"} {
		t.Run(scenario, func(t *testing.T) {
			s, before, p := fixture(t)
			stage := "replaced"
			if scenario == "staged" {
				stage = scenario
			}
			crash(t, s, p, stage)
			errorCode(t, s.Pending(), "RECOVERY_CONFLICT")
			_, e := s.Apply(context.Background(), p.ID)
			errorCode(t, e, "RECOVERY_CONFLICT")
			if scenario == "external-target" {
				if e = os.WriteFile(filepath.Join(s.Root, "a.txt"), []byte("external"), 0644); e != nil {
					t.Fatal(e)
				}
				_, e = s.Recover()
				errorCode(t, e, "RECOVERY_CONFLICT")
				b, _ := os.ReadFile(filepath.Join(s.Root, "a.txt"))
				if string(b) != "external" {
					t.Fatal("conflict overwritten")
				}
				return
			}
			if scenario == "external-unrelated" {
				if e = os.WriteFile(filepath.Join(s.Root, "extra.txt"), []byte("keep"), 0644); e != nil {
					t.Fatal(e)
				}
				if e = s.PutFile(&before, "extra.txt", []byte("keep"), 0644); e != nil {
					t.Fatal(e)
				}
			}
			n, e := s.Recover()
			if e != nil || n != 1 {
				t.Fatal(n, e)
			}
			equalTree(t, s, before)
			if e = s.Pending(); e != nil {
				t.Fatal(e)
			}
			n, e = s.Recover()
			if e != nil || n != 0 {
				t.Fatal(n, e)
			}
		})
	}
}
func TestCancellationRestoresSource(t *testing.T) {
	s, before, p := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	s.Hook = func(stage string, _ int) error {
		if stage == "replaced" {
			cancel()
		}
		return nil
	}
	_, e := s.Apply(ctx, p.ID)
	errorCode(t, e, "SOURCE_APPLY_FAILED")
	equalTree(t, s, before)
}
func TestUnsafeRootsAndCasePaths(t *testing.T) {
	s, _, _ := fixture(t)
	s.Local = filepath.Join(s.Root, "local")
	errorCode(t, s.Locked(func() error { return nil }), "INVALID_ROOTS")
	for _, p := range []string{"../x", "a/../x", "C:/x", "NUL.txt", "a\\b", "a. /x"} {
		if e := SafeRelative(p); e == nil {
			t.Fatalf("unsafe %s", p)
		}
	}
}
