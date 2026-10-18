package projectsnapshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func publicationSnapshotFixture(t *testing.T) (*Archive, Content, *workstore.Store, *workstore.Store, workstore.Entry) {
	t.Helper()
	a, c, s, n := coordinatedSnapshotFixture(t)
	old := *c.Coordinated
	c.Coordinated = nil
	c.Trigger = "replica publication"
	publisher := a.Binding
	publisher.Copy, publisher.NativeRoot = workstore.ID(), t.TempDir()
	publisher.SourceRoot = filepath.Join(publisher.NativeRoot, ".aidock")
	other := publisher
	other.Copy, other.NativeRoot = workstore.ID(), t.TempDir()
	other.SourceRoot = filepath.Join(other.NativeRoot, ".aidock")
	id := workstore.ID()
	coordinator, err := PublicationCoordinator(a.Local, a.Binding.Project, a.Binding.Scope, id, []Binding{a.Binding, other})
	if err != nil {
		t.Fatal(err)
	}
	roots := append([]workstore.RootBinding{}, coordinator.Roots...)
	sort.Slice(roots, func(i, j int) bool { return roots[i].Path < roots[j].Path })
	p := workstore.BatchPlan{Version: 1, Kind: "coordinated-roots", ID: c.Transaction.ID, Root: coordinator.Local, Identity: coordinator.Identity, Action: "apply", Created: time.Now().UTC().Format(time.RFC3339Nano), Roots: roots, Parents: []string{}}
	foreignStore := &workstore.Store{Root: other.SourceRoot, Local: t.TempDir()}
	foreign := workstore.Tree{Entries: map[string]workstore.Entry{}}
	if err := foreignStore.PutFile(&foreign, "foreign.txt", []byte("FOREIGN_MEMBER_BODY_MUST_NOT_ENTER_OWN_ARCHIVE"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, root := range roots {
		target := workstore.BatchTarget{Path: root.Path, File: root.File, Before: workstore.Tree{Entries: map[string]workstore.Entry{}}, After: workstore.Tree{Entries: map[string]workstore.Entry{}}}
		prefix := PublicationMemberPrefix(a.Binding.Copy)
		if strings.HasPrefix(root.Path, prefix) {
			path := strings.TrimPrefix(root.Path, prefix)
			for _, t := range old.Targets {
				if t.Path == path {
					target.Before, target.After = t.Before, t.After
				}
			}
			// A live replica publication guards Local; it never rewinds it.
			if path == "local" {
				target.Before = workstore.Clone(target.After)
			}
		} else if root.Path == PublicationMemberPrefix(other.Copy)+"source" || root.Path == PublicationMemberPrefix(other.Copy)+"native/.codex" {
			target.Before, target.After = foreign, workstore.Clone(foreign)
		}
		p.Targets = append(p.Targets, target)
	}
	p.Digest = workstore.BatchPlanDigest(p)
	c.Publication = &PublicationCommit{Version: 1, Publication: id, Publisher: publisher, Members: []Binding{a.Binding, other}, Transaction: p, View: &PublicationView{Executable: filepath.Join(t.TempDir(), "aidock.exe"), ExecutableSHA: strings.Repeat("b", 64), Sessions: []source.MCPSessionRequest{}}}
	return a, c, s, n, foreign.Entries["foreign.txt"]
}

func TestArchiveV5RealPublicationProofArchivesOnlyOwnMember(t *testing.T) {
	a, c, s, n, foreign := publicationSnapshotFixture(t)
	r := mustCapture(t, a, c, s, n)
	if r.Version != 5 || r.Coordinated != nil || r.Publication.Transaction.Digest != c.Publication.Transaction.Digest {
		t.Fatal("shared proof omitted or disguised as replay")
	}
	legacy, err := a.git(context.Background(), []string{"for-each-ref", "--format=%(refname)", "refs/aidock/snapshots/", "refs/aidock/replays/"}, nil, 4096)
	if err != nil || strings.TrimSpace(string(legacy)) != "" {
		t.Fatal("publication entered legacy namespaces", err)
	}
	refs, err := a.References(context.Background())
	if err != nil || len(refs) != 1 || refs[0] != r.ID {
		t.Fatal(refs, err)
	}
	loaded, err := a.Load(context.Background(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state, err := loaded.Publication.State(a.Local); err != nil || state != "unknown" {
		t.Fatal("archive intent became a committed shared transaction", state, err)
	}
	if _, err := a.SourceData(context.Background(), loaded, foreign); err == nil {
		t.Fatal("foreign member source body entered own source Git")
	}
	if _, err := a.nativeBlobs().Data(foreign); err == nil {
		t.Fatal("foreign member native body entered own private backup")
	}
	var own workstore.Entry
	for _, target := range publicationMemberTargets(loaded.Content) {
		if target.Path == "native/.codex" {
			own = target.Before.Entries["private.json"]
		}
	}
	if b, err := a.nativeBlobs().Data(own); err != nil || string(b) != "PRIVATE_NATIVE_SIBLING_NOT_IN_GIT" {
		t.Fatal("own complete native container missing", err)
	}
	if _, err := a.SourceData(context.Background(), loaded, own); err == nil {
		t.Fatal("own raw native body entered source Git")
	}
	destination := &workstore.Store{Root: n.Root, Local: filepath.Join(a.Local, "publication-native-material")}
	if _, err := a.MaterializeNative(context.Background(), r.ID, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := destination.Data(own); err != nil {
		t.Fatal(err)
	}
	if _, err := destination.Data(foreign); err == nil {
		t.Fatal("foreign native materialized through own archive")
	}
}

func TestArchiveV5RejectsRehashedForeignBindingsAndCompilerSubsets(t *testing.T) {
	for _, mutation := range []string{"foreign-root", "omitted-member", "compiler", "publisher-target", "local-write", "v4-disguise", "reference"} {
		t.Run(mutation, func(t *testing.T) {
			a, c, s, n, _ := publicationSnapshotFixture(t)
			switch mutation {
			case "foreign-root":
				c.Publication.Transaction.Roots[0].Root = t.TempDir()
			case "omitted-member":
				c.Publication.Members = c.Publication.Members[:1]
			case "compiler":
				c.Transaction.Targets[0].Before = workstore.Clone(c.Transaction.Targets[0].After)
				c.Transaction.Digest = workstore.BatchPlanDigest(c.Transaction)
			case "publisher-target":
				c.Publication.Publisher = a.Binding
			case "local-write":
				for i := range c.Publication.Transaction.Targets {
					target := &c.Publication.Transaction.Targets[i]
					if target.Path == PublicationMemberPrefix(a.Binding.Copy)+"local" {
						target.Before = workstore.Tree{Entries: map[string]workstore.Entry{}}
					}
				}
			case "v4-disguise":
				c.Coordinated = &c.Publication.Transaction
			case "reference":
				c.Publication.Transaction.Reference = workstore.ID()
			}
			c.Publication.Transaction.Digest = workstore.BatchPlanDigest(c.Publication.Transaction)
			if _, err := a.Capture(context.Background(), c, s, n); err == nil {
				t.Fatal("forged shared snapshot accepted")
			}
		})
	}
}

func TestPublicationProofReadsActualGlobalCommitAndRecoveredFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "recover"}[fail], func(t *testing.T) {
			local, native := filepath.Join(t.TempDir(), "local"), t.TempDir()
			b := Binding{Project: workstore.ID(), Scope: "server", Copy: workstore.ID(), NativeRoot: native, SourceRoot: filepath.Join(native, ".aidock")}
			publisher := b
			publisher.Copy, publisher.NativeRoot = workstore.ID(), t.TempDir()
			publisher.SourceRoot = filepath.Join(publisher.NativeRoot, ".aidock")
			id := workstore.ID()
			coordinator, err := PublicationCoordinator(local, b.Project, b.Scope, id, []Binding{b})
			if err != nil {
				t.Fatal(err)
			}
			for _, root := range coordinator.Roots {
				if err := os.MkdirAll(filepath.Dir(root.Root), 0700); err != nil {
					t.Fatal(err)
				}
			}
			targets := []workstore.BatchTarget{}
			for _, root := range coordinator.Roots {
				empty := workstore.Tree{Entries: map[string]workstore.Entry{}}
				after := workstore.Clone(empty)
				if root.Path == PublicationMemberPrefix(b.Copy)+"source" {
					if err := coordinator.Blobs.PutFile(&after, "project.yaml", []byte("owned shared transaction fixture"), 0644); err != nil {
						t.Fatal(err)
					}
				}
				targets = append(targets, workstore.BatchTarget{Path: root.Path, File: root.File, Before: empty, After: after})
			}
			p, err := coordinator.PreviewObserved("apply", targets)
			if err != nil {
				t.Fatal(err)
			}
			proof := PublicationCommit{Version: 1, Publication: id, Publisher: publisher, Members: []Binding{b}, Transaction: p}
			if state, err := proof.State(local); err != nil || state != "unknown" {
				t.Fatal(state, err)
			}
			preparedObserved := false
			coordinator.Hook = func(stage string, n int) error {
				if stage == "target" && n == 0 {
					state, err := proof.State(local)
					if err != nil || state != "prepared" {
						t.Fatal("partial write became committed", state, err)
					}
					preparedObserved = true
					if fail {
						return errors.New("owned full commit fault")
					}
				}
				return nil
			}
			_, err = coordinator.Apply(context.Background(), p.ID, p.Digest)
			if (err != nil) != fail || !preparedObserved {
				t.Fatal(err)
			}
			state, err := proof.State(local)
			want := map[bool]string{false: "committed", true: "recovered"}[fail]
			if err != nil || state != want {
				t.Fatal(state, err)
			}
		})
	}
}

func TestPublicationCoordinatorBindsAll32CompleteMembersAndBoundsCount(t *testing.T) {
	local, parent, project := filepath.Join(t.TempDir(), "local"), t.TempDir(), workstore.ID()
	members := []Binding{}
	for i := 0; i < 33; i++ {
		native := filepath.Join(parent, workstore.ID())
		members = append(members, Binding{Project: project, Scope: "server", Copy: workstore.ID(), SourceRoot: filepath.Join(native, ".aidock"), NativeRoot: native})
	}
	coordinator, err := PublicationCoordinator(local, project, "server", workstore.ID(), members[:32])
	if err != nil || len(coordinator.Roots) != 289 {
		t.Fatal("32 complete members lost roots", err)
	}
	roots := append([]workstore.RootBinding{}, coordinator.Roots...)
	sort.Slice(roots, func(i, j int) bool { return roots[i].Path < roots[j].Path })
	p := workstore.BatchPlan{Version: 1, Kind: "coordinated-roots", ID: workstore.ID(), Root: coordinator.Local, Identity: coordinator.Identity, Action: "apply", Roots: roots}
	for _, root := range roots {
		empty := workstore.Tree{Entries: map[string]workstore.Entry{}}
		p.Targets = append(p.Targets, workstore.BatchTarget{Path: root.Path, File: root.File, Before: empty, After: workstore.Clone(empty)})
	}
	p.Digest = workstore.BatchPlanDigest(p)
	if err := coordinator.ValidateManifest(p); err != nil {
		t.Fatal("32 complete member manifest rejected", err)
	}
	_, err = PublicationCoordinator(local, project, "server", workstore.ID(), members)
	if err == nil {
		t.Fatal("unbounded complete members accepted")
	}
}
