package workstore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func coordinatedFixture(t *testing.T) (*Coordinator, BatchPlan) {
	t.Helper()
	c := &Coordinator{Local: t.TempDir(), Identity: "fixture/project/server"}
	c.Blobs = &Store{Root: filepath.Join(t.TempDir(), "assets"), Local: c.Local}
	targets := []BatchTarget{}
	for _, key := range []string{"control", "copy-one", "copy-two"} {
		root := filepath.Join(t.TempDir(), ".aidock")
		if err := os.MkdirAll(filepath.Join(root, "empty"), 0755); err != nil {
			t.Fatal(err)
		}
		for name, bytes := range map[string][]byte{"body": []byte("before-" + key), "unrelated": {0, 255, 11}} {
			if err := os.WriteFile(filepath.Join(root, name), bytes, 0644); err != nil {
				t.Fatal(err)
			}
		}
		before, err := c.Blobs.Capture(root)
		if err != nil {
			t.Fatal(err)
		}
		after := Clone(before)
		if err = c.Blobs.PutFile(&after, "body", []byte("after-"+key), 0644); err != nil {
			t.Fatal(err)
		}
		if err = c.Blobs.PutFile(&after, "added.bin", []byte{255, 0, 29}, 0644); err != nil {
			t.Fatal(err)
		}
		c.Roots = append(c.Roots, RootBinding{Path: key, Root: root})
		targets = append(targets, BatchTarget{Path: key, Before: before, After: after})
	}
	p, err := c.Preview("apply", targets)
	if err != nil {
		t.Fatal(err)
	}
	return c, p
}
func coordinatedTrees(t *testing.T, c *Coordinator) []string {
	t.Helper()
	out := []string{}
	for _, root := range c.Roots {
		s := c.store(root.Path)
		tree, err := s.Capture(s.Root)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, Digest(tree))
	}
	return out
}
func coordinatedEqual(t *testing.T, c *Coordinator, targets []BatchTarget, after bool) {
	t.Helper()
	for _, target := range targets {
		s := c.store(target.Path)
		tree, err := s.Capture(s.Root)
		want := target.Before
		if after {
			want = target.After
		}
		if err != nil || Digest(tree) != Digest(want) {
			t.Fatal("incomplete root", target.Path, err)
		}
	}
}
func TestCoordinatorApplyReverseAndUndoRestoreBaselineAndPackagesTogether(t *testing.T) {
	c, p := coordinatedFixture(t)
	ctx := context.Background()
	j, err := c.Apply(ctx, p.ID, p.Digest)
	if err != nil || j.State != "committed" {
		t.Fatal(j, err)
	}
	coordinatedEqual(t, c, p.Targets, true)
	if err := os.WriteFile(filepath.Join(c.Roots[1].Root, "handwritten"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	r, err := c.Reverse(j.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	rolled, err := c.Apply(ctx, r.ID, r.Digest)
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range c.Roots {
		body, err := os.ReadFile(filepath.Join(root.Root, "body"))
		if err != nil || string(body) != "before-"+root.Path {
			t.Fatal("baseline/package rollback", err)
		}
	}
	if bytes, err := os.ReadFile(filepath.Join(c.Roots[1].Root, "handwritten")); err != nil || string(bytes) != "keep" {
		t.Fatal("unrelated lost", err)
	}
	u, err := c.Reverse(rolled.Plan.ID)
	if err != nil || u.Action != "undo-rollback" {
		t.Fatal(u, err)
	}
	if _, err = c.Apply(ctx, u.ID, u.Digest); err != nil {
		t.Fatal(err)
	}
	for _, root := range c.Roots {
		bytes, err := os.ReadFile(filepath.Join(root.Root, "added.bin"))
		if err != nil || string(bytes) != string([]byte{255, 0, 29}) {
			t.Fatal("binary lost", err)
		}
		if _, err := os.Stat(filepath.Join(root.Root, "empty")); err != nil {
			t.Fatal("empty dir lost", err)
		}
	}
	_, err = c.Reverse(p.ID)
	batchCode(t, err, "TRANSACTION_NOT_FOUND")
	_, err = c.Apply(ctx, p.ID, p.Digest)
	batchCode(t, err, "PLAN_CONSUMED")
}
func TestCoordinatorValidatesAllInputsBeforeFirstReplacement(t *testing.T) {
	for _, scenario := range []string{"stale", "backup", "confirmation", "cancelled", "lock"} {
		t.Run(scenario, func(t *testing.T) {
			c, p := coordinatedFixture(t)
			ctx := context.Background()
			digest := p.Digest
			want := ""
			switch scenario {
			case "stale":
				if err := os.WriteFile(filepath.Join(c.Roots[2].Root, "body"), []byte("external"), 0644); err != nil {
					t.Fatal(err)
				}
				want = "PLAN_STALE"
			case "backup":
				e := p.Targets[2].After.Entries["added.bin"]
				if err := os.Remove(filepath.Join(c.store(p.Targets[2].Path).bucket(), "blobs", e.Hash)); err != nil {
					t.Fatal(err)
				}
				want = "BACKUP_INVALID"
			case "confirmation":
				digest = "wrong"
				want = "PLAN_CONFIRMATION_REQUIRED"
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "lock":
				r := c.Roots[2].Root
				release, err := lock(filepath.Join(filepath.Dir(r), "."+filepath.Base(r)+".aidock-lock"))
				if err != nil {
					t.Fatal(err)
				}
				defer release()
				want = "SOURCE_BUSY"
			}
			before := coordinatedTrees(t, c)
			_, err := c.Apply(ctx, p.ID, digest)
			if scenario == "cancelled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else {
				batchCode(t, err, want)
			}
			after := coordinatedTrees(t, c)
			for i := range before {
				if before[i] != after[i] {
					t.Fatal("root touched before validation", i)
				}
			}
			for _, root := range c.Roots {
				if _, err := os.Stat(CoordinatedMarker(root.Root)); !os.IsNotExist(err) {
					t.Fatal("barrier left before prepare", err)
				}
			}
		})
	}
}
func TestCoordinatorSecondRootFailureRestoresEveryRoot(t *testing.T) {
	c, p := coordinatedFixture(t)
	c.Hook = func(stage string, i int) error {
		if stage == "target" && i == 1 {
			return errors.New("injected second-root failure")
		}
		return nil
	}
	j, err := c.Apply(context.Background(), p.ID, p.Digest)
	batchCode(t, err, "COORDINATED_APPLY_FAILED")
	if j.State != "recovered" {
		t.Fatal(j.State)
	}
	coordinatedEqual(t, c, p.Targets, false)
	if err = c.Pending(); err != nil {
		t.Fatal(err)
	}
}

type coordinatedChild struct {
	Local, Identity, ID, Digest string
	Roots                       []RootBinding
}

func TestCoordinatorProcessHelper(t *testing.T) {
	path := os.Getenv("AIDOCK_COORDINATED_CONFIG")
	if path == "" {
		return
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v coordinatedChild
	if err = json.Unmarshal(bytes, &v); err != nil {
		t.Fatal(err)
	}
	c := &Coordinator{Local: v.Local, Identity: v.Identity, Roots: v.Roots}
	phase := os.Getenv("AIDOCK_COORDINATED_PHASE")
	c.Hook = func(stage string, i int) error {
		if stage == phase && i == 0 {
			os.Exit(27)
		}
		return nil
	}
	if phase == "recovered-target" {
		_, err = c.Recover()
	} else {
		_, err = c.Apply(context.Background(), v.ID, v.Digest)
	}
	t.Fatalf("child did not interrupt: %v", err)
}
func coordinatedCrash(t *testing.T, c *Coordinator, p BatchPlan, phase string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "config.json")
	bytes, _ := json.Marshal(coordinatedChild{Local: c.Local, Identity: c.Identity, Roots: c.Roots, ID: p.ID, Digest: p.Digest})
	if err := os.WriteFile(file, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestCoordinatorProcessHelper$")
	cmd.Env = append(os.Environ(), "AIDOCK_COORDINATED_CONFIG="+file, "AIDOCK_COORDINATED_PHASE="+phase)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 27 {
		t.Fatal(string(output), err)
	}
}
func TestCoordinatorProcessExitAtEveryDurableBoundary(t *testing.T) {
	for _, phase := range []string{"prepared", "barrier", "target-complete", "committed", "cleared-barrier"} {
		t.Run(phase, func(t *testing.T) {
			c, p := coordinatedFixture(t)
			coordinatedCrash(t, c, p, phase)
			fresh := &Coordinator{Local: c.Local, Identity: c.Identity, Roots: c.Roots}
			if phase == "target-complete" {
				for _, root := range c.Roots {
					s := &Store{Root: root.Root, Local: t.TempDir()}
					batchCode(t, s.Pending(), "COORDINATED_RECOVERY_REQUIRED")
					_, err := s.Recover()
					batchCode(t, err, "COORDINATED_RECOVERY_REQUIRED")
				}
				foreign := &Coordinator{Local: t.TempDir(), Identity: "another-binding", Roots: c.Roots}
				_, err := foreign.Recover()
				batchCode(t, err, "RECOVERY_CONFLICT")
			}
			_, err := fresh.Recover()
			if err != nil {
				t.Fatal(err)
			}
			committed := phase == "committed" || phase == "cleared-barrier"
			coordinatedEqual(t, fresh, p.Targets, committed)
			if err = fresh.Pending(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestCoordinatorRecoveryConflictAndRepeatedInterruption(t *testing.T) {
	c, p := coordinatedFixture(t)
	coordinatedCrash(t, c, p, "target-complete")
	conflictPath := filepath.Join(c.Roots[1].Root, "body")
	if err := os.WriteFile(conflictPath, []byte("external edit"), 0644); err != nil {
		t.Fatal(err)
	}
	before := coordinatedTrees(t, c)
	_, err := c.Recover()
	batchCode(t, err, "RECOVERY_CONFLICT")
	after := coordinatedTrees(t, c)
	for i := range before {
		if before[i] != after[i] {
			t.Fatal("partially recovered before checking all roots")
		}
	}
	if err = os.WriteFile(conflictPath, []byte("before-copy-one"), 0644); err != nil {
		t.Fatal(err)
	}
	coordinatedCrash(t, c, p, "recovered-target")
	for _, root := range c.Roots {
		batchCode(t, c.store(root.Path).Pending(), "COORDINATED_RECOVERY_REQUIRED")
	}
	if _, err = c.Recover(); err != nil {
		t.Fatal(err)
	}
	coordinatedEqual(t, c, p.Targets, false)
}
func TestCoordinatorUnavailableRootBlocksRecoveryWithoutReplacingOthers(t *testing.T) {
	c, p := coordinatedFixture(t)
	coordinatedCrash(t, c, p, "target-complete")
	r := c.Roots[1].Root
	if err := os.Rename(r, r+"-offline"); err != nil {
		t.Fatal(err)
	}
	first := c.store(c.Roots[0].Path)
	before, _ := first.Capture(first.Root)
	_, err := c.Recover()
	batchCode(t, err, "RECOVERY_CONFLICT")
	after, _ := first.Capture(first.Root)
	if Digest(before) != Digest(after) {
		t.Fatal("other root replaced while one unavailable")
	}
	if err = os.Rename(r+"-offline", r); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Recover(); err != nil {
		t.Fatal(err)
	}
	coordinatedEqual(t, c, p.Targets, false)
}
func TestCoordinatorUnavailableParentKeepsLocalHistoryAndPlanVisible(t *testing.T) {
	c, p := coordinatedFixture(t)
	coordinatedCrash(t, c, p, "target-complete")
	parent := filepath.Dir(c.Roots[1].Root)
	if err := os.Rename(parent, parent+"-offline"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(parent+"-offline", parent)
	js, err := c.History()
	if err != nil || len(js) != 1 || js[0].State != "prepared" {
		t.Fatal("history concealed by unavailable target", js, err)
	}
	got, err := c.LoadPlan(p.ID)
	if err != nil || got.Digest != p.Digest {
		t.Fatal("recovery plan concealed", err)
	}
	_, err = c.Recover()
	batchCode(t, err, "INVALID_ROOTS")
	if err = os.Rename(parent+"-offline", parent); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Recover(); err != nil {
		t.Fatal(err)
	}
	coordinatedEqual(t, c, p.Targets, false)
}
func TestCoordinatorRejectsOverlapUnboundAndCorruptBarrier(t *testing.T) {
	c, p := coordinatedFixture(t)
	bad := *c
	bad.Roots = append(append([]RootBinding{}, c.Roots...), RootBinding{Path: "nested", Root: filepath.Join(c.Roots[0].Root, "nested")})
	batchCode(t, bad.Pending(), "INVALID_ROOTS")
	bad = *c
	bad.Roots = append([]RootBinding{}, c.Roots...)
	bad.Roots[0].Root = c.Local
	batchCode(t, bad.Pending(), "INVALID_ROOTS")
	copy := p
	copy.Targets = append([]BatchTarget{}, p.Targets...)
	copy.Targets[0].Path = "unbound"
	_, err := c.Preview("apply", copy.Targets)
	batchCode(t, err, "BACKUP_INVALID")
	if err = os.WriteFile(CoordinatedMarker(c.Roots[0].Root), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = c.Apply(context.Background(), p.ID, p.Digest)
	batchCode(t, err, "COORDINATED_RECOVERY_REQUIRED")
	_, err = c.Recover()
	batchCode(t, err, "RECOVERY_CONFLICT")
	coordinatedEqual(t, c, p.Targets, false)
}
