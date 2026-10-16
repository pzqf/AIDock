// Package projectsnapshot keeps complete source observations in a private local
// Git object store. It never modifies a project's Git repository or restores a
// target. Native bytes stay in a separate private blob store, outside Git.
package projectsnapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/privatefs"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var bindingID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var objectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
var sha256ID = regexp.MustCompile(`^[0-9a-f]{64}$`)

const repositoryConfig = "[core]\n\trepositoryformatversion = 0\n\tfilemode = false\n\tbare = true\n\tlogallrefupdates = false\n\tlongpaths = true\n"

type Binding struct {
	Project    string `json:"project"`
	Scope      string `json:"scope_id"`
	Copy       string `json:"copy"`
	SourceRoot string `json:"source_root"`
	NativeRoot string `json:"native_root"`
}

type Summary struct {
	Added   int `json:"added"`
	Changed int `json:"changed"`
	Removed int `json:"removed"`
}

// Content contains immutable observations, not permission to restore them.
// Callers must recompile under current source/permission/target locks and use
// the owning transaction journal to establish whether an application committed.
type Content struct {
	Version         int                           `json:"schema_version"`
	ID              string                        `json:"id"`
	At              string                        `json:"at"`
	Trigger         string                        `json:"trigger"`
	Binding         Binding                       `json:"binding"`
	Source          workstore.Tree                `json:"saved_source"`
	Effective       source.Export                 `json:"effective"`
	Transaction     workstore.BatchPlan           `json:"transaction"`
	Adapters        map[string]string             `json:"adapter_versions"`
	PreviousApplied string                        `json:"previous_applied,omitempty"`
	Summary         Summary                       `json:"summary"`
	Configuration   *source.SnapshotConfiguration `json:"configuration,omitempty"`
	Digest          string                        `json:"digest"`
}

type Record struct {
	Content
	Commit string `json:"commit"`
}

type Archive struct {
	Local      string
	Binding    Binding
	gitPath    string
	gitFailure func(string, string)
}

func New(local string, binding Binding) (*Archive, error) {
	if !bindingID.MatchString(binding.Project) || !bindingID.MatchString(binding.Copy) || strings.TrimSpace(binding.Scope) == "" {
		return nil, result.Fail("INVALID_INPUT", "项目快照需要明确 project/scope/copy 绑定", 2)
	}
	for _, p := range []string{local, binding.SourceRoot, binding.NativeRoot} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return nil, result.Fail("INVALID_ROOTS", "项目快照需要规范绝对路径", 2)
		}
		if err := workstore.CheckLinks(p); err != nil {
			return nil, result.Fail("PATH_UNSAFE", "项目快照路径包含链接或不可读取项", 5)
		}
	}
	if workstore.Within(local, binding.SourceRoot) || workstore.Within(binding.SourceRoot, local) || workstore.Within(local, binding.NativeRoot) {
		return nil, result.Fail("INVALID_ROOTS", "快照 Local 与规范源不得包含或重叠，也不能包含工具项目", 2)
	}
	return &Archive{Local: local, Binding: binding}, nil
}

func encoded(v any) []byte    { data, _ := json.Marshal(v); return data }
func digest(c Content) string { c.Digest = ""; return workstore.Hash(encoded(c)) }
func (a *Archive) directory() string {
	return filepath.Join(a.Local, "project-snapshots", workstore.Hash(encoded(a.Binding)))
}
func (a *Archive) repository() string { return filepath.Join(a.directory(), "repository.git") }
func (a *Archive) lock() *workstore.Store {
	return &workstore.Store{Root: a.directory(), Local: filepath.Join(a.Local, "project-snapshot-control")}
}
func (a *Archive) nativeBlobs() *workstore.Store {
	return &workstore.Store{Root: a.Binding.NativeRoot, Local: filepath.Join(a.directory(), "native-private")}
}

func (a *Archive) validateRepository() error {
	if err := workstore.CheckLinks(a.repository()); err != nil {
		return result.Fail("PATH_UNSAFE", "快照 Git 路径不安全", 5)
	}
	if _, err := os.Lstat(a.repository()); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	err := filepath.WalkDir(a.repository(), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return workstore.CheckLinks(path)
	})
	if err != nil {
		return result.Fail("PATH_UNSAFE", "快照 Git 内存在链接或不可读取项", 5)
	}
	for _, p := range []string{"objects/info/alternates", "objects/info/http-alternates", "commondir", "worktrees"} {
		if _, err := os.Lstat(filepath.Join(a.repository(), filepath.FromSlash(p))); !os.IsNotExist(err) {
			return result.Fail("SNAPSHOT_REPOSITORY_INVALID", "快照 Git 不接受外部对象库或工作树关联", 5)
		}
	}
	data, err := workstore.ReadFile(filepath.Join(a.repository(), "config"))
	if err != nil || string(data) != repositoryConfig {
		return result.Fail("SNAPSHOT_REPOSITORY_INVALID", "快照 Git 配置已变化，保留原仓库供检查", 5)
	}
	return nil
}

type cappedOutput struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if n > b.limit-b.Len() {
		b.overflow = true
		p = p[:b.limit-b.Len()]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

// No user Git configuration, templates, hooks, credential helpers, traces,
// alternates or environment are inherited. Every revision argument is an ID
// produced or validated here; source names never become Git command arguments.
func (a *Archive) git(ctx context.Context, args []string, input io.Reader, limit int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, result.Fail("INVALID_INPUT", "缺少内部 Git 操作", 2)
	}
	if args[0] != "init" {
		if err := a.validateRepository(); err != nil {
			return nil, err
		}
	}
	executable := a.gitPath
	if executable == "" {
		var err error
		executable, err = exec.LookPath("git")
		if err != nil {
			return nil, result.Fail("SNAPSHOT_GIT_UNAVAILABLE", "本机 Git 不可用，未创建项目快照", 4)
		}
	}
	runtimeRoot := filepath.Join(a.directory(), "git-runtime")
	emptyHooks := filepath.Join(runtimeRoot, "empty")
	if err := workstore.CheckLinks(emptyHooks); err != nil {
		return nil, result.Fail("PATH_UNSAFE", "快照 Git 模板或钩子目录不安全", 5)
	}
	entries, err := os.ReadDir(emptyHooks)
	if err != nil || len(entries) != 0 {
		return nil, result.Fail("SNAPSHOT_REPOSITORY_INVALID", "快照 Git 模板与钩子目录必须保持为空", 5)
	}
	gitDirectory := a.repository()
	commandDirectory := a.directory()
	if args[0] != "init" {
		gitDirectory = "."
		commandDirectory = a.repository()
	}
	base := []string{"--no-pager", "--no-replace-objects", "-c", "core.hooksPath=" + filepath.Join(runtimeRoot, "empty"), "-c", "protocol.allow=never", "-c", "commit.gpgSign=false", "--git-dir=" + gitDirectory}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(bounded, executable, append(base, args...)...)
	cmd.Dir = commandDirectory
	cmd.Env = []string{"PATH=" + filepath.Dir(executable), "HOME=" + runtimeRoot, "USERPROFILE=" + runtimeRoot, "XDG_CONFIG_HOME=" + runtimeRoot, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_SYSTEM=" + os.DevNull, "GIT_ATTR_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}
	for _, key := range []string{"SystemRoot", "WINDIR"} {
		if v := os.Getenv(key); v != "" {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	cmd.Stdin = input
	stderr := &cappedOutput{limit: 32768}
	cmd.Stderr = stderr
	out := &cappedOutput{limit: limit}
	cmd.Stdout = out
	err = cmd.Run()
	if bounded.Err() != nil {
		return nil, bounded.Err()
	}
	if err != nil || out.overflow {
		if a.gitFailure != nil {
			a.gitFailure(args[0], stderr.String()+fmt.Sprint(err))
		}
		return nil, result.Fail("SNAPSHOT_GIT_FAILED", "本机 Git 快照操作失败；项目源和工具目标未由此操作修改", 5)
	}
	return out.Bytes(), nil
}

type blob struct {
	entry workstore.Entry
	data  []byte
}

func sourceTrees(c Content) []workstore.Tree {
	out := []workstore.Tree{c.Source}
	if c.Configuration != nil {
		out = append(out, c.Configuration.LocalTree)
		for _, pkg := range c.Configuration.ReferencePackages {
			out = append(out, pkg.Tree)
		}
	}
	for _, p := range c.Effective.Packages {
		out = append(out, p.Tree)
	}
	return out
}

func sourceBlobs(ctx context.Context, c Content, store *workstore.Store) ([]blob, error) {
	files := map[string]workstore.Entry{}
	for _, t := range sourceTrees(c) {
		for p, e := range t.Entries {
			if err := workstore.SafeRelative(p); err != nil {
				return nil, result.Fail("SNAPSHOT_INVALID", "快照文件清单无效", 5)
			}
			if e.Dir {
				continue
			}
			if old, ok := files[e.Hash]; ok && old.Size != e.Size {
				return nil, result.Fail("SNAPSHOT_INVALID", "快照正文清单存在歧义", 5)
			}
			files[e.Hash] = e
		}
	}
	keys := make([]string, 0, len(files))
	for h := range files {
		keys = append(keys, h)
	}
	sort.Strings(keys)
	out := []blob{}
	var total int64
	for _, h := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := store.Data(files[h])
		if err != nil {
			return nil, result.Fail("SNAPSHOT_CONTENT_MISSING", "快照源正文缺失或摘要不一致", 5)
		}
		total += int64(len(data))
		if total > workstore.MaxTree {
			return nil, result.Fail("SNAPSHOT_TOO_LARGE", "完整快照源正文超过受支持大小", 5)
		}
		out = append(out, blob{files[h], data})
	}
	return out, nil
}

func (a *Archive) validate(c Content) error {
	if c.Version != 1 && c.Version != 2 || c.Version == 1 && c.Configuration != nil || c.Version == 2 && c.Configuration == nil || !uuid.MatchString(c.ID) || c.Binding != a.Binding || !uuid.MatchString(c.Transaction.ID) || c.Transaction.Root != a.Binding.NativeRoot || c.Source.Entries == nil || c.Source.Exists == false || c.Effective.Root != a.Binding.SourceRoot || c.Effective.Owner != a.Binding.Project || c.Effective.Scope != a.Binding.Scope || c.Effective.Copy != a.Binding.Copy || c.Effective.Snapshot != workstore.Digest(c.Source) || c.Digest != digest(c) || c.Adapters == nil {
		return result.Fail("SNAPSHOT_INVALID", "项目快照身份、范围或正文清单不一致", 5)
	}
	if c.Configuration != nil {
		if err := source.ValidateSnapshotConfiguration(*c.Configuration, c.Effective, a.Local); err != nil {
			return err
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, c.At); err != nil {
		return result.Fail("SNAPSHOT_INVALID", "项目快照时间无效", 5)
	}
	if c.Trigger != "aidock apply" && c.Trigger != "aidock replay" {
		return result.Fail("SNAPSHOT_INVALID", "项目快照触发类型无效", 5)
	}
	if c.PreviousApplied != "" && !uuid.MatchString(c.PreviousApplied) {
		return result.Fail("SNAPSHOT_INVALID", "项目快照前序引用无效", 5)
	}
	want := a.Binding.Project + "/" + a.Binding.Scope + "/" + a.Binding.Copy
	if c.Transaction.Identity != want && c.Transaction.Identity != "profile/"+want {
		return result.Fail("SNAPSHOT_INVALID", "项目快照事务属于其它绑定或范围", 5)
	}
	if len(c.Transaction.Targets) == 0 || len(c.Transaction.Targets) > 7 {
		return result.Fail("SNAPSHOT_INVALID", "项目快照目标清单无效", 5)
	}
	if !sha256ID.MatchString(c.Transaction.Digest) {
		return result.Fail("SNAPSHOT_INVALID", "项目快照缺少完整事务摘要", 5)
	}
	seen := map[string]bool{}
	for _, target := range c.Transaction.Targets {
		if workstore.SafeRelative(target.Path) != nil || seen[target.Path] {
			return result.Fail("SNAPSHOT_INVALID", "项目快照目标路径无效或重复", 5)
		}
		seen[target.Path] = true
	}
	for _, tree := range sourceTrees(c) {
		if !tree.Exists && len(tree.Entries) > 0 {
			return result.Fail("SNAPSHOT_INVALID", "项目快照目录存在性不一致", 5)
		}
		for path, e := range tree.Entries {
			if workstore.SafeRelative(path) != nil || e.Dir && (e.Hash != "" || e.Size != 0) || !e.Dir && (!sha256ID.MatchString(e.Hash) || e.Size < 0 || e.Size > workstore.MaxFile) {
				return result.Fail("SNAPSHOT_INVALID", "项目快照源文件清单无效", 5)
			}
		}
	}
	return nil
}

// Capture consumes immutable manifests. Callers lock live source observation,
// then revalidate authority and targets before committing the application.
// Source bytes enter Git; native bytes enter only the private blob store.
func (a *Archive) Capture(ctx context.Context, c Content, sourceStore, nativeStore *workstore.Store) (Record, error) {
	var out Record
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if sourceStore == nil || nativeStore == nil {
		return out, result.Fail("INVALID_INPUT", "项目快照需要明确源与目标备份存储", 2)
	}
	c.Version, c.ID, c.At, c.Binding = 1, workstore.ID(), time.Now().UTC().Format(time.RFC3339Nano), a.Binding
	if c.Configuration != nil {
		c.Version = 2
	}
	c.Summary = Summary{}
	for _, t := range c.Transaction.Targets {
		for _, change := range workstore.Changes(t.Before, t.After) {
			if change.Before == nil {
				c.Summary.Added++
			} else if change.After == nil {
				c.Summary.Removed++
			} else {
				c.Summary.Changed++
			}
		}
	}
	c.Digest = digest(c)
	if err := a.validate(c); err != nil {
		return out, err
	}
	blobs, err := sourceBlobs(ctx, c, sourceStore)
	if err != nil {
		return out, err
	}
	if err = privatefs.Ensure(filepath.Dir(a.directory())); err != nil {
		return out, err
	}
	err = a.lock().Locked(func() error {
		for _, p := range []string{a.directory(), filepath.Join(a.directory(), "native-private"), filepath.Join(a.directory(), "git-runtime", "empty")} {
			if e := privatefs.Ensure(p); e != nil {
				return e
			}
		}
		if _, e := os.Lstat(a.repository()); os.IsNotExist(e) {
			if _, e = a.git(ctx, []string{"init", "--bare", "--template=" + filepath.Join(a.directory(), "git-runtime", "empty"), a.repository()}, nil, 8192); e != nil {
				return e
			}
			if e = os.WriteFile(filepath.Join(a.repository(), "config"), []byte(repositoryConfig), 0600); e != nil {
				return e
			}
		} else if e != nil {
			return e
		}
		if e := a.validateRepository(); e != nil {
			return e
		}
		for _, target := range c.Transaction.Targets {
			for _, tree := range []workstore.Tree{target.Before, target.After} {
				for _, entry := range tree.Entries {
					if err := ctx.Err(); err != nil {
						return err
					}
					if entry.Dir {
						continue
					}
					data, e := nativeStore.Data(entry)
					if e != nil {
						return result.Fail("SNAPSHOT_BACKUP_MISSING", "原生目标备份缺失或摘要不一致", 5)
					}
					temp := workstore.Tree{Entries: map[string]workstore.Entry{}}
					if e = a.nativeBlobs().PutFile(&temp, entry.Hash, data, entry.Mode); e != nil {
						return e
					}
				}
			}
		}
		payload := encoded(c)
		if len(payload) > workstore.MaxFile {
			return result.Fail("SNAPSHOT_TOO_LARGE", "项目快照元数据超过受支持大小", 5)
		}
		readers := []io.Reader{}
		for i, b := range blobs {
			readers = append(readers, strings.NewReader(fmt.Sprintf("blob\nmark :%d\ndata %d\n", i+1, len(b.data))), bytes.NewReader(b.data), strings.NewReader("\n"))
		}
		mark := len(blobs) + 1
		readers = append(readers, strings.NewReader(fmt.Sprintf("blob\nmark :%d\ndata %d\n", mark, len(payload))), bytes.NewReader(payload), strings.NewReader("\n"))
		var commit strings.Builder
		message := "AIDock project snapshot " + c.ID + "\n"
		fmt.Fprintf(&commit, "commit refs/aidock/snapshots/%s\ncommitter AIDock <snapshot@localhost> %d +0000\ndata %d\n%s\n", c.ID, time.Now().Unix(), len(message), message)
		for i, b := range blobs {
			fmt.Fprintf(&commit, "M 100644 :%d source-blobs/%s\n", i+1, b.entry.Hash)
		}
		fmt.Fprintf(&commit, "M 100644 :%d manifest.json\n\ndone\n", mark)
		readers = append(readers, strings.NewReader(commit.String()))
		if _, e := a.git(ctx, []string{"fast-import", "--quiet", "--done"}, io.MultiReader(readers...), 8192); e != nil {
			return e
		}
		commitID, e := a.reference(ctx, c.ID)
		if e != nil {
			return e
		}
		out = Record{Content: c, Commit: commitID}
		return nil
	})
	return out, err
}

func (a *Archive) reference(ctx context.Context, id string) (string, error) {
	if !uuid.MatchString(id) {
		return "", result.Fail("INVALID_INPUT", "需要当前绑定的项目快照 UUID", 2)
	}
	data, err := a.git(ctx, []string{"rev-parse", "--verify", "refs/aidock/snapshots/" + id + "^{commit}"}, nil, 1024)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if !objectID.MatchString(value) {
		return "", result.Fail("SNAPSHOT_INVALID", "快照 Git 对象身份无效", 5)
	}
	return value, nil
}

func (a *Archive) Load(ctx context.Context, id string) (Record, error) { return a.read(ctx, id, true) }

// Metadata checks the immutable manifest and binding. It does not claim that
// archived bodies, native backups, runtime dependencies or current policy pass.
func (a *Archive) Metadata(ctx context.Context, id string) (Record, error) {
	return a.read(ctx, id, false)
}

func (a *Archive) References(ctx context.Context) ([]string, error) {
	out := []string{}
	if _, err := os.Lstat(a.repository()); os.IsNotExist(err) {
		return out, nil
	} else if err != nil {
		return nil, err
	}
	err := a.lock().Locked(func() error {
		data, err := a.git(ctx, []string{"for-each-ref", "--format=%(refname:strip=3)", "refs/aidock/snapshots/"}, nil, 1<<20)
		if err != nil {
			return err
		}
		for _, id := range strings.Fields(string(data)) {
			if !uuid.MatchString(id) {
				return result.Fail("SNAPSHOT_INVALID", "项目快照引用清单损坏", 5)
			}
			out = append(out, id)
		}
		return nil
	})
	return out, err
}

func (a *Archive) read(ctx context.Context, id string, complete bool) (Record, error) {
	var out Record
	if !uuid.MatchString(id) {
		return out, result.Fail("INVALID_INPUT", "需要当前绑定的项目快照 UUID", 2)
	}
	if _, err := os.Lstat(a.repository()); os.IsNotExist(err) {
		return out, result.Fail("SNAPSHOT_NOT_FOUND", "当前绑定没有此项目快照", 2)
	} else if err != nil {
		return out, err
	}
	err := a.lock().Locked(func() error {
		commit, err := a.reference(ctx, id)
		if err != nil {
			return err
		}
		data, err := a.git(ctx, []string{"cat-file", "blob", commit + ":manifest.json"}, nil, workstore.MaxFile)
		if err != nil {
			return err
		}
		if _, err = config.Parse(data); err != nil {
			return result.Fail("SNAPSHOT_INVALID", "项目快照清单包含重复键或不支持的结构", 5)
		}
		var c Content
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&c) != nil || decoder.Decode(new(any)) != io.EOF || c.ID != id {
			return result.Fail("SNAPSHOT_INVALID", "项目快照清单不可读取", 5)
		}
		if err = a.validate(c); err != nil {
			return err
		}
		if !complete {
			out = Record{Content: c, Commit: commit}
			return nil
		}
		files := map[string]workstore.Entry{}
		for _, tree := range sourceTrees(c) {
			for _, e := range tree.Entries {
				if !e.Dir {
					files[e.Hash] = e
				}
			}
		}
		for hash, e := range files {
			if len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" || e.Size < 0 || e.Size > workstore.MaxFile {
				return result.Fail("SNAPSHOT_INVALID", "快照正文清单无效", 5)
			}
			body, err := a.git(ctx, []string{"cat-file", "blob", commit + ":source-blobs/" + hash}, nil, int(e.Size)+1)
			if err != nil || int64(len(body)) != e.Size || workstore.Hash(body) != hash {
				return result.Fail("SNAPSHOT_CONTENT_MISSING", "快照源正文缺失或摘要不一致", 5)
			}
		}
		if c.Configuration != nil {
			r := Record{Content: c, Commit: commit}
			if err = source.ValidateSnapshotConfigurationBytes(*c.Configuration, c.Effective, func(e workstore.Entry) ([]byte, error) { return a.SourceData(ctx, r, e) }); err != nil {
				return err
			}
		}
		for _, target := range c.Transaction.Targets {
			for _, tree := range []workstore.Tree{target.Before, target.After} {
				for _, e := range tree.Entries {
					if e.Dir {
						continue
					}
					if _, err = a.nativeBlobs().Data(e); err != nil {
						return result.Fail("SNAPSHOT_BACKUP_MISSING", "快照原生备份缺失或摘要不一致", 5)
					}
				}
			}
		}
		out = Record{Content: c, Commit: commit}
		return nil
	})
	return out, err
}

// SourceData returns archived bytes, independent of current global/source blobs.
// It is internal replay material; list/detail APIs must project safe metadata.
func (a *Archive) SourceData(ctx context.Context, r Record, e workstore.Entry) ([]byte, error) {
	if err := a.validate(r.Content); err != nil {
		return nil, err
	}
	if !objectID.MatchString(r.Commit) || len(e.Hash) != 64 || strings.Trim(e.Hash, "0123456789abcdef") != "" || e.Size < 0 || e.Size > workstore.MaxFile {
		return nil, result.Fail("SNAPSHOT_INVALID", "项目快照正文引用无效", 5)
	}
	data, err := a.git(ctx, []string{"cat-file", "blob", r.Commit + ":source-blobs/" + e.Hash}, nil, int(e.Size)+1)
	if err != nil || int64(len(data)) != e.Size || workstore.Hash(data) != e.Hash {
		return nil, result.Fail("SNAPSHOT_CONTENT_MISSING", "项目快照正文不可恢复", 5)
	}
	return data, nil
}
