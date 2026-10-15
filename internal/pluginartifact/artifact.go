// Package pluginartifact imports a complete local ZIP into the exact directory
// and byte manifest already selected by plugins.lock. It never executes code.
package pluginartifact

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

type Edit struct {
	Action             string `json:"action"`
	Name               string `json:"name"`
	ExpectedSHA256     string `json:"expected_sha256"`
	ExpectedLockSHA256 string `json:"expected_lock_sha256"`
	ArchivePath        string `json:"archive_path,omitempty"`
	Snapshot           string `json:"snapshot,omitempty"`
}
type State struct {
	Name           string              `json:"name"`
	ArtifactPath   string              `json:"artifact_path"`
	Root           string              `json:"root"`
	SHA256         string              `json:"sha256"`
	Status         string              `json:"status"`
	History        []workstore.Journal `json:"history"`
	Recovery       bool                `json:"recovery_required"`
	ExecutionReady bool                `json:"execution_ready"`
	Boundary       string              `json:"boundary"`
}
type Preview struct {
	Plan          workstore.Plan `json:"plan"`
	Before        State          `json:"before"`
	Candidate     State          `json:"candidate"`
	ArchiveSHA256 string         `json:"archive_sha256,omitempty"`
}
type Manager struct {
	Store       *workstore.Store
	Plugin      pluginlock.Plugin
	Binding     string
	BeforeApply func() error
}

func jsonBytes(v any) []byte { b, _ := json.Marshal(v); return b }
func New(local, binding string, plugin pluginlock.Plugin) (*Manager, error) {
	if !filepath.IsAbs(local) || filepath.Clean(local) != local || binding == "" {
		return nil, result.Fail("INVALID_INPUT", "安装包需要规范本机根和项目绑定", 2)
	}
	if _, err := pluginlock.Parse(jsonBytes(pluginlock.Lock{Version: 1, Plugins: []pluginlock.Plugin{plugin}})); err != nil {
		return nil, err
	}
	if plugin.Platform != runtime.GOOS+"-"+runtime.GOARCH {
		return nil, result.Fail("ARTIFACT_PLATFORM_MISMATCH", "完整包平台与当前主机不符", 5)
	}
	target := filepath.Join(local, filepath.FromSlash(plugin.Artifact.Path))
	for _, p := range []string{local, target} {
		if err := workstore.CheckLinks(p); err != nil {
			return nil, err
		}
	}
	return &Manager{Store: &workstore.Store{Root: target, Local: filepath.Join(local, "plugin-artifact-transactions"), PlanKind: "plugin-artifact"}, Plugin: plugin, Binding: binding}, nil
}
func (m *Manager) matches(t workstore.Tree) bool {
	if !t.Exists || len(t.Entries) != len(m.Plugin.Artifact.Entries) {
		return false
	}
	for _, e := range m.Plugin.Artifact.Entries {
		got, ok := t.Entries[e.Path]
		if !ok || got.Dir != e.Directory || got.Mode != e.Mode || got.Hash != e.SHA256 || got.Size != e.Bytes {
			return false
		}
	}
	return true
}
func (m *Manager) state(t workstore.Tree, history []workstore.Journal) State {
	status := "MISSING"
	if t.Exists {
		status = "OCCUPIED"
		if m.matches(t) {
			status = "MATCHED"
		} else if len(t.Entries) == 0 {
			status = "EMPTY"
		}
	}
	out := State{Name: m.Plugin.Name, ArtifactPath: m.Plugin.Artifact.Path, Root: m.Store.Root, SHA256: workstore.Digest(t), Status: status, History: history, Boundary: "只按已保存安装锁导入完整 ZIP，不覆盖已有文件，不提升来源信任、不登记安装或启动程序。回退与撤销仅处理此产物目录。"}
	for _, j := range history {
		if j.State == "prepared" {
			out.Recovery = true
		}
	}
	return out
}
func (m *Manager) State() (State, error) {
	t, err := m.Store.Capture(m.Store.Root)
	if err != nil {
		return State{}, err
	}
	js, err := m.Store.Journals()
	if err != nil {
		return State{}, err
	}
	return m.state(t, js), nil
}
func (m *Manager) locked(fn func() error) error {
	// Explicit planning may establish missing Local Root parent directories. It
	// never creates the final artifact root before the prepared transaction.
	parent := filepath.Dir(m.Store.Root)
	if err := workstore.CheckLinks(parent); err != nil {
		return err
	}
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	return m.Store.Locked(fn)
}
func openArchive(path string) (*os.File, os.FileInfo, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, nil, result.Fail("INVALID_INPUT", "完整包路径须为规范绝对路径", 2)
	}
	if err := workstore.CheckLinks(path); err != nil {
		return nil, nil, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > pluginlock.MaxArtifactBytes+(16<<20) {
		return nil, nil, result.Fail("ARTIFACT_ARCHIVE_INVALID", "ZIP 不是普通文件或超过 528 MB", 5)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		f.Close()
		return nil, nil, result.Fail("PLAN_STALE", "ZIP 在打开时变化", 3)
	}
	return f, after, nil
}
func archiveHash(ctx context.Context, f *os.File) (string, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	h := sha256.New()
	var total int64
	reader := io.LimitReader(f, pluginlock.MaxArtifactBytes+(16<<20)+1)
	buf := make([]byte, 128<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := reader.Read(buf)
		total += int64(n)
		if total > pluginlock.MaxArtifactBytes+(16<<20) {
			return "", result.Fail("ARTIFACT_ARCHIVE_INVALID", "ZIP 超过大小限制", 5)
		}
		if n > 0 {
			h.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func archiveUnchanged(f *os.File, before os.FileInfo, path string) error {
	if err := workstore.CheckLinks(path); err != nil {
		return err
	}
	after, err := f.Stat()
	if err != nil {
		return err
	}
	now, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, now) || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return result.Fail("PLAN_STALE", "完整安装包在读取期间变化", 3)
	}
	return nil
}
func (m *Manager) importTree(ctx context.Context, path string) (workstore.Tree, string, error) {
	t := workstore.Tree{Exists: true, Entries: map[string]workstore.Entry{}}
	f, before, err := openArchive(path)
	if err != nil {
		return t, "", err
	}
	defer f.Close()
	hash, err := archiveHash(ctx, f)
	if err != nil {
		return t, "", err
	}
	z, err := zip.NewReader(f, before.Size())
	if err != nil {
		return t, "", err
	}
	if len(z.File) != len(m.Plugin.Artifact.Entries) {
		return t, "", result.Fail("ARTIFACT_ARCHIVE_INVALID", "ZIP 条目数量与完整安装锁不同，必须包含所有目录、隐藏文件和运行时", 5)
	}
	want := map[string]pluginlock.Entry{}
	for _, e := range m.Plugin.Artifact.Entries {
		want[e.Path] = e
	}
	seen := map[string]bool{}
	for _, file := range z.File {
		if err := ctx.Err(); err != nil {
			return t, "", err
		}
		p := file.Name
		if file.FileInfo().IsDir() {
			p = strings.TrimSuffix(p, "/")
		}
		if err := workstore.SafeRelative(p); err != nil {
			return t, "", err
		}
		e, ok := want[p]
		if !ok || seen[strings.ToLower(p)] || file.Mode()&os.ModeSymlink != 0 || file.Mode()&os.ModeType != 0 && !file.Mode().IsDir() || e.Directory != file.FileInfo().IsDir() || uint64(e.Bytes) != file.UncompressedSize64 {
			return t, "", result.Fail("ARTIFACT_ARCHIVE_INVALID", "ZIP 路径、类型、数量或大小与安装锁不符: "+p, 5)
		}
		seen[strings.ToLower(p)] = true
		if e.Directory {
			t.Entries[p] = workstore.Entry{Dir: true, Mode: e.Mode}
			continue
		}
		r, err := file.Open()
		if err != nil {
			return t, "", err
		}
		data, readErr := io.ReadAll(io.LimitReader(r, e.Bytes+1))
		closeErr := r.Close()
		if readErr != nil {
			return t, "", readErr
		}
		if closeErr != nil {
			return t, "", closeErr
		}
		if int64(len(data)) != e.Bytes || workstore.Hash(data) != e.SHA256 {
			return t, "", result.Fail("ARTIFACT_ARCHIVE_INVALID", "ZIP 文件字节或摘要不符: "+p, 5)
		}
		if err = m.Store.PutFile(&t, p, data, e.Mode); err != nil {
			return t, "", err
		}
	}
	// PutFile may create parent directories; the explicit lock controls their
	// final modes and the exact final set, independent of ZIP entry ordering.
	for _, e := range m.Plugin.Artifact.Entries {
		if e.Directory {
			t.Entries[e.Path] = workstore.Entry{Dir: true, Mode: e.Mode}
		}
	}
	if !m.matches(t) {
		return t, "", result.Fail("ARTIFACT_ARCHIVE_INVALID", "当前平台无法保持安装锁声明的文件模式或完整内容", 5)
	}
	if err = archiveUnchanged(f, before, path); err != nil {
		return t, "", err
	}
	endHash, err := archiveHash(ctx, f)
	if err != nil {
		return t, "", err
	}
	if endHash != hash {
		return t, "", result.Fail("PLAN_STALE", "ZIP 在核验时变化", 3)
	}
	return t, hash, nil
}
func (m *Manager) candidate(before workstore.Tree, req Edit, history []workstore.Journal, ctx context.Context) (workstore.Tree, string, error) {
	if req.Action == "import" {
		if req.Snapshot != "" || req.ArchivePath == "" {
			return workstore.Tree{}, "", result.Fail("INVALID_INPUT", "导入需要完整 ZIP 路径", 2)
		}
		if len(before.Entries) != 0 {
			return workstore.Tree{}, "", result.Fail("ARTIFACT_TARGET_OCCUPIED", "目标已有内容；不会覆盖或接管，先核对已有产物", 4)
		}
		return m.importTree(ctx, req.ArchivePath)
	}
	if req.Action != "rollback" || req.Snapshot == "" || req.ArchivePath != "" {
		return workstore.Tree{}, "", result.Fail("INVALID_INPUT", "需要明确导入或历史回退动作", 2)
	}
	for _, j := range history {
		if j.State == "committed" {
			if j.Plan.ID != req.Snapshot {
				return workstore.Tree{}, "", result.Fail("ROLLBACK_NOT_LATEST", "只允许回退最近成功的完整包动作，避免越过后续修改", 3)
			}
			if workstore.Digest(before) != workstore.Digest(j.Plan.After) {
				return workstore.Tree{}, "", result.Fail("PLAN_STALE", "产物在上次成功动作后已变化；保留外部编辑", 3)
			}
			if len(j.Plan.Before.Entries) > 0 && !m.matches(j.Plan.Before) {
				return workstore.Tree{}, "", result.Fail("SNAPSHOT_DEPENDENCY_MISSING", "历史产物与当前锁不匹配，先恢复对应锁", 5)
			}
			return j.Plan.Before, "", nil
		}
	}
	return workstore.Tree{}, "", result.Fail("SNAPSHOT_NOT_FOUND", "没有可回退的完整包记录", 4)
}
func (m *Manager) Plan(ctx context.Context, req Edit, source string) (Preview, error) {
	var out Preview
	if req.Name != m.Plugin.Name || req.ExpectedSHA256 == "" || source == "" {
		return out, result.Fail("INVALID_INPUT", "需要当前 MCP、产物观察摘要与源绑定", 2)
	}
	err := m.locked(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.Store.Pending(); err != nil {
			return err
		}
		before, err := m.Store.Capture(m.Store.Root)
		if err != nil {
			return err
		}
		if workstore.Digest(before) != req.ExpectedSHA256 {
			return result.Fail("PLAN_STALE", "产物目录在读取后变化", 3)
		}
		js, err := m.Store.Journals()
		if err != nil {
			return err
		}
		after, hash, err := m.candidate(before, req, js, ctx)
		if err != nil {
			return err
		}
		out.Before = m.state(before, js)
		out.Candidate = m.state(after, js)
		out.ArchiveSHA256 = hash
		out.Plan, err = m.Store.PreviewBound("artifact-"+req.Action, before, after, map[string]string{"request": string(jsonBytes(req)), "source_sha256": source, "binding": m.Binding, "plugin_sha256": workstore.Hash(jsonBytes(m.Plugin)), "archive_sha256": hash})
		return err
	})
	return out, err
}
func (m *Manager) Apply(ctx context.Context, id, digest, source string) (workstore.Journal, error) {
	m.Store.Validate = func(p workstore.Plan) error {
		if m.BeforeApply != nil {
			if err := m.BeforeApply(); err != nil {
				return err
			}
		}
		if digest == "" || p.Digest != digest || len(p.Inputs) != 5 || p.Inputs["source_sha256"] != source || p.Inputs["binding"] != m.Binding || p.Inputs["plugin_sha256"] != workstore.Hash(jsonBytes(m.Plugin)) {
			return result.Fail("PLAN_STALE", "确认摘要、项目、安装锁或源已变化", 3)
		}
		var req Edit
		if err := json.Unmarshal([]byte(p.Inputs["request"]), &req); err != nil {
			return err
		}
		if req.Name != m.Plugin.Name || req.ExpectedSHA256 != workstore.Digest(p.Before) || p.Action != "artifact-"+req.Action {
			return result.Fail("ARTIFACT_PLAN_INVALID", "完整包动作与候选不匹配", 5)
		}
		js, err := m.Store.Journals()
		if err != nil {
			return err
		}
		after, hash, err := m.candidate(p.Before, req, js, ctx)
		if err != nil {
			return err
		}
		if workstore.Digest(after) != workstore.Digest(p.After) || hash != p.Inputs["archive_sha256"] {
			return result.Fail("PLAN_STALE", "ZIP、历史或候选内容在预览后变化", 3)
		}
		return nil
	}
	return m.Store.Apply(ctx, id)
}
func (m *Manager) Recover() (int, error) { return m.Store.Recover() }

// RecoveryStore needs only the explicit local target, so source corruption or a
// changed lock cannot strand a prepared transaction. It never accepts a plan.
func RecoveryStore(local, artifactPath string) (*workstore.Store, error) {
	if workstore.SafeRelative(artifactPath) != nil || !strings.HasPrefix(artifactPath, "mcp-artifacts/") || !filepath.IsAbs(local) {
		return nil, fmt.Errorf("恢复目标须位于 Local Root 的 mcp-artifacts 子目录")
	}
	return &workstore.Store{Root: filepath.Join(local, filepath.FromSlash(artifactPath)), Local: filepath.Join(local, "plugin-artifact-transactions"), PlanKind: "plugin-artifact"}, nil
}
