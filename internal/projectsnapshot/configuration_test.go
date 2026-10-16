package projectsnapshot

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func layeredSnapshotFixture(t *testing.T) (*Archive, Content, *workstore.Store, *workstore.Store) {
	t.Helper()
	a, c, s, n := snapshotFixture(t)
	m, err := source.New(s.Root, s.Local, a.Binding.Project, a.Binding.Scope)
	if err != nil {
		t.Fatal(err)
	}
	m.Copy = a.Binding.Copy
	p, err := m.Plan(context.Background(), source.Request{Action: "create", Name: "guide", Description: "layered snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	state, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	local, err := m.ProfilePlan(context.Background(), source.ProfileEdit{Target: "local", ExpectedSHA256: state.Layers.LocalSHA256, ExpectedLayers: state.Layers.Digest, Profile: map[string]any{"schema_version": 2, "name": "local", "rules": []any{"LOCAL_CONFIGURATION_PRIVATE"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyProfile(context.Background(), "local", local.Plan.ID, local.Plan.Digest); err != nil {
		t.Fatal(err)
	}
	if err = m.WithSnapshotInputs(func(bundle source.Export, tree workstore.Tree, cfg source.SnapshotConfiguration) error {
		c.Source, c.Effective, c.Configuration = tree, bundle, &cfg
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return a, c, s, n
}

func TestArchiveV2RestoresLocalCacheWithoutWritingLiveRoots(t *testing.T) {
	a, c, s, n := layeredSnapshotFixture(t)
	before, err := s.Capture(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	r := mustCapture(t, a, c, s, n)
	if r.Version != 2 || r.Configuration == nil {
		t.Fatal("configuration snapshot omitted")
	}
	localRoot := r.Configuration.Layers.LocalRoot
	for _, path := range []string{localRoot, filepath.Join(s.Local, "source-transactions", workstore.Hash([]byte(strings.ToLower(s.Root))), "blobs")} {
		if !workstore.Within(a.Local, path) || path == a.Local {
			t.Fatal("fixture cleanup boundary")
		}
		if err = os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := a.Load(context.Background(), r.ID)
	if err != nil {
		t.Fatal("archive depended on current local input", err)
	}
	material, err := a.Materialize(context.Background(), r.ID, s)
	if err != nil {
		t.Fatal(err)
	}
	localBody, err := s.Data(material.Configuration.LocalTree.Entries["profile.yaml"])
	if err != nil || !bytes.Contains(localBody, []byte("LOCAL_CONFIGURATION_PRIVATE")) {
		t.Fatal("local bytes not returned to immutable cache", err)
	}
	if _, err = os.Stat(localRoot); !os.IsNotExist(err) {
		t.Fatal("cache materialization restored a live local root")
	}
	after, err := s.Capture(s.Root)
	if err != nil || workstore.Digest(before) != workstore.Digest(after) {
		t.Fatal("live project source changed", err)
	}
	if loaded.Commit != material.Commit || loaded.Configuration.Digest != material.Configuration.Digest {
		t.Fatal("immutable archive changed")
	}
}

func TestArchiveMaterializationCannotChooseAnotherCacheBinding(t *testing.T) {
	a, c, s, n := layeredSnapshotFixture(t)
	r := mustCapture(t, a, c, s, n)
	other := &workstore.Store{Root: s.Root, Local: filepath.Join(a.Local, "unbound-cache")}
	if _, err := a.Materialize(context.Background(), r.ID, other); err == nil {
		t.Fatal("caller selected another Local Root")
	}
	if _, err := os.Stat(other.Local); !os.IsNotExist(err) {
		t.Fatal("unbound cache created")
	}
}
