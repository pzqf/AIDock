package projectsnapshot

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

func snapshotFixture(t *testing.T) (*Archive, Content, *workstore.Store, *workstore.Store) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("local Git unavailable")
	}
	root := t.TempDir()
	project := filepath.Join(root, "project")
	sourceRoot := filepath.Join(project, ".aidock")
	local := filepath.Join(root, "home", "local")
	for _, p := range []string{sourceRoot, local} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	binding := Binding{Project: workstore.ID(), Copy: workstore.ID(), Scope: "server", SourceRoot: sourceRoot, NativeRoot: project}
	a, err := New(local, binding)
	if err != nil {
		t.Fatal(err)
	}
	a.gitFailure = func(operation, detail string) { t.Log(operation, detail) }
	s := &workstore.Store{Root: sourceRoot, Local: local}
	n := &workstore.Store{Root: project, Local: filepath.Join(local, "native-mcp-private")}
	sourceTree := workstore.Tree{Entries: map[string]workstore.Entry{}}
	for p, body := range map[string][]byte{"profile.yaml": []byte("schema_version: 2\nrules: [fixture]\n"), "skills/guide/SKILL.md": []byte("---\nname: guide\ndescription: snapshot fixture\n---\n"), "skills/guide/assets/data.bin": {0, 255, 1, 128}} {
		if err = s.PutFile(&sourceTree, p, body, 0644); err != nil {
			t.Fatal(err)
		}
	}
	pkg := workstore.Tree{Exists: true, Entries: map[string]workstore.Entry{"SKILL.md": sourceTree.Entries["skills/guide/SKILL.md"], "assets": workstore.Directory(), "assets/data.bin": sourceTree.Entries["skills/guide/assets/data.bin"], "empty": workstore.Directory()}}
	cached := workstore.Tree{Entries: map[string]workstore.Entry{}}
	if err = s.PutFile(&cached, "inherited.txt", []byte("fixed global revision outside canonical source tree"), 0644); err != nil {
		t.Fatal(err)
	}
	pkg.Entries["inherited.txt"] = cached.Entries["inherited.txt"]
	exported := source.Export{Root: sourceRoot, Owner: binding.Project, Scope: binding.Scope, Copy: binding.Copy, Snapshot: workstore.Digest(sourceTree), Packages: []source.ExportPackage{{Tree: pkg}}}
	after := workstore.Tree{Entries: map[string]workstore.Entry{}}
	if err = n.PutFile(&after, workstore.FileContent, []byte("RAW_NATIVE_SECRET_SENTINEL"), 0600); err != nil {
		t.Fatal(err)
	}
	c := Content{Trigger: "aidock apply", Source: sourceTree, Effective: exported, Adapters: map[string]string{"codex-cli": "0.3.0"}, Transaction: workstore.BatchPlan{ID: workstore.ID(), Digest: workstore.Hash([]byte("fixed fixture plan")), Root: project, Identity: "profile/" + binding.Project + "/" + binding.Scope + "/" + binding.Copy, Action: "apply", Targets: []workstore.BatchTarget{{Path: ".codex/config.toml", File: true, Before: workstore.Tree{Entries: map[string]workstore.Entry{}}, After: after}}}}
	return a, c, s, n
}

func mustCapture(t *testing.T, a *Archive, c Content, s, n *workstore.Store) Record {
	t.Helper()
	r, err := a.Capture(context.Background(), c, s, n)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestArchiveCompleteSourceAndNativeBackupsSurviveOriginalBlobLoss(t *testing.T) {
	a, c, s, n := snapshotFixture(t)
	r := mustCapture(t, a, c, s, n)
	if !objectID.MatchString(r.Commit) || r.Summary.Added != 1 {
		t.Fatal(r.Commit, r.Summary)
	}
	manifest, err := a.git(context.Background(), []string{"cat-file", "blob", r.Commit + ":manifest.json"}, nil, workstore.MaxFile)
	if err != nil || bytes.Contains(manifest, []byte("RAW_NATIVE_SECRET_SENTINEL")) {
		t.Fatal("native bytes escaped into Git manifest", err)
	}
	for _, store := range []*workstore.Store{s, n} {
		path := filepath.Join(store.Local, "source-transactions", workstore.Hash([]byte(strings.ToLower(store.Root))), "blobs")
		if err = os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := a.Load(context.Background(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	entry := loaded.Effective.Packages[0].Tree.Entries["assets/data.bin"]
	data, err := a.SourceData(context.Background(), loaded, entry)
	if err != nil || !bytes.Equal(data, []byte{0, 255, 1, 128}) {
		t.Fatal(data, err)
	}
	if !loaded.Effective.Packages[0].Tree.Entries["empty"].Dir {
		t.Fatal("empty directory lost")
	}
	inherited, err := a.SourceData(context.Background(), loaded, loaded.Effective.Packages[0].Tree.Entries["inherited.txt"])
	if err != nil || string(inherited) != "fixed global revision outside canonical source tree" {
		t.Fatal("inherited body depended on current source cache", err)
	}
	if _, err = os.Stat(filepath.Join(a.Binding.NativeRoot, ".git")); !os.IsNotExist(err) {
		t.Fatal("project Git modified")
	}
	if _, err = os.Stat(filepath.Join(a.Binding.NativeRoot, ".codex")); !os.IsNotExist(err) {
		t.Fatal("archive modified native target")
	}
	if loaded.Digest != r.Digest || loaded.Commit != r.Commit {
		t.Fatal("immutable record changed")
	}
}

func TestArchiveMissingBackupAndAlteredRepositoryRefusePartialSuccess(t *testing.T) {
	t.Run("duplicate manifest identity", func(t *testing.T) {
		a, c, s, n := snapshotFixture(t)
		r := mustCapture(t, a, c, s, n)
		payload := bytes.Replace(encoded(r.Content), []byte(`"id":"`+r.ID+`"`), []byte(`"id":"ambiguous","id":"`+r.ID+`"`), 1)
		stream := fmt.Sprintf("blob\nmark :1\ndata %d\n%s\ncommit refs/aidock/snapshots/%s\ncommitter AIDock <snapshot@localhost> 1792160000 +0000\ndata 7\nfixture\nfrom %s\nM 100644 :1 manifest.json\n\ndone\n", len(payload), payload, r.ID, r.Commit)
		if _, err := a.git(context.Background(), []string{"fast-import", "--quiet", "--done"}, strings.NewReader(stream), 8192); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Metadata(context.Background(), r.ID); err == nil {
			t.Fatal("duplicate identity accepted")
		}
	})
	t.Run("unexpected Git hook", func(t *testing.T) {
		a, c, s, n := snapshotFixture(t)
		r := mustCapture(t, a, c, s, n)
		if err := os.WriteFile(filepath.Join(a.directory(), "git-runtime", "empty", "reference-transaction"), []byte("must-never-run"), 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Load(context.Background(), r.ID); err == nil {
			t.Fatal("unexpected Git hook accepted")
		}
	})
	t.Run("source body absent from new Git tree", func(t *testing.T) {
		a, c, s, n := snapshotFixture(t)
		r := mustCapture(t, a, c, s, n)
		payload := encoded(r.Content)
		stream := fmt.Sprintf("blob\nmark :1\ndata %d\n%s\ncommit refs/aidock/snapshots/%s\ncommitter AIDock <snapshot@localhost> 1792160000 +0000\ndata 7\nfixture\nfrom %s\ndeleteall\nM 100644 :1 manifest.json\n\ndone\n", len(payload), payload, r.ID, r.Commit)
		if _, err := a.git(context.Background(), []string{"fast-import", "--quiet", "--done"}, strings.NewReader(stream), 8192); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Metadata(context.Background(), r.ID); err != nil {
			t.Fatal("manifest should remain available without claiming body integrity", err)
		}
		if loaded, err := a.Load(context.Background(), r.ID); err == nil || loaded.ID != "" {
			t.Fatal("missing source bytes accepted", err)
		}
	})
	t.Run("native backup", func(t *testing.T) {
		a, c, s, n := snapshotFixture(t)
		r := mustCapture(t, a, c, s, n)
		e := c.Transaction.Targets[0].After.Entries[workstore.FileContent]
		path := filepath.Join(a.nativeBlobs().Local, "source-transactions", workstore.Hash([]byte(strings.ToLower(a.Binding.NativeRoot))), "blobs", e.Hash)
		if err := os.WriteFile(path, []byte("corrupt fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		loaded, err := a.Load(context.Background(), r.ID)
		if err == nil || loaded.ID != "" {
			t.Fatal("corrupt native bytes accepted", err)
		}
	})
	t.Run("external Git configuration", func(t *testing.T) {
		a, c, s, n := snapshotFixture(t)
		r := mustCapture(t, a, c, s, n)
		if err := os.WriteFile(filepath.Join(a.repository(), "config"), []byte(repositoryConfig+"[include]\npath=external-private-config\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Load(context.Background(), r.ID); err == nil {
			t.Fatal("repository configuration change accepted")
		}
		if _, err := a.Capture(context.Background(), c, s, n); err == nil {
			t.Fatal("altered repository accepted writes")
		}
	})
}

func TestArchiveIgnoresUserGitEnvironmentAndRejectsUnboundInput(t *testing.T) {
	a, c, s, n := snapshotFixture(t)
	trace := filepath.Join(t.TempDir(), "must-not-be-created")
	t.Setenv("GIT_TRACE", trace)
	t.Setenv("GIT_INDEX_FILE", trace+"-index")
	t.Setenv("GIT_OBJECT_DIRECTORY", trace+"-objects")
	t.Setenv("GIT_CONFIG_GLOBAL", trace+"-global")
	t.Setenv("GIT_CONFIG_SYSTEM", trace+"-system")
	r := mustCapture(t, a, c, s, n)
	if _, err := a.Load(context.Background(), r.ID); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{trace, trace + "-index", trace + "-objects"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatal("inherited Git environment used", p)
		}
	}
	other := *a
	other.Binding.Copy = workstore.ID()
	if _, err := other.Load(context.Background(), r.ID); err == nil {
		t.Fatal("cross-copy snapshot accepted")
	}
	bad := c
	bad.Effective.Copy = workstore.ID()
	if _, err := a.Capture(context.Background(), bad, s, n); err == nil {
		t.Fatal("foreign effective source accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Capture(ctx, c, s, n); err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := a.Load(context.Background(), "../other"); err == nil {
		t.Fatal("path input accepted")
	}
}

func TestArchivePreservesExistingProjectGitAndDirtyFiles(t *testing.T) {
	a, c, s, n := snapshotFixture(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()
	projectGit := filepath.Join(a.Binding.NativeRoot, ".git")
	cmd := exec.Command(git, "init", "--bare", "--template="+empty, projectGit)
	cmd.Env = []string{"PATH=" + filepath.Dir(git), "HOME=" + empty, "USERPROFILE=" + empty, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_SYSTEM=" + os.DevNull}
	for _, key := range []string{"SystemRoot", "WINDIR"} {
		if value := os.Getenv(key); value != "" {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	dirty := filepath.Join(a.Binding.NativeRoot, "unsubmitted-source.txt")
	if err = os.WriteFile(dirty, []byte("unrelated dirty work"), 0644); err != nil {
		t.Fatal(err)
	}
	observe := func() map[string]string {
		out := map[string]string{}
		err := filepath.WalkDir(projectGit, func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if !d.IsDir() {
				b, e := os.ReadFile(p)
				if e != nil {
					return e
				}
				rel, _ := filepath.Rel(projectGit, p)
				out[rel] = workstore.Hash(b)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := encoded(observe())
	mustCapture(t, a, c, s, n)
	if !bytes.Equal(encoded(observe()), before) {
		t.Fatal("project Git changed")
	}
	data, err := os.ReadFile(dirty)
	if err != nil || string(data) != "unrelated dirty work" {
		t.Fatal("unrelated dirty file changed", err)
	}
}
