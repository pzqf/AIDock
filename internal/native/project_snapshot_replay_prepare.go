package native

import (
	"context"
	"errors"
	"path/filepath"
	"sort"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/privatefs"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

type SnapshotReplayPreparationRequest struct {
	SnapshotReplayCandidateRequest
	Preparation CheckRequest `json:"preparation"`
}

func ParseSnapshotReplayPreparationRequest(raw []byte) (SnapshotReplayPreparationRequest, error) {
	var out SnapshotReplayPreparationRequest
	bad := func() (SnapshotReplayPreparationRequest, error) {
		return SnapshotReplayPreparationRequest{}, result.Fail("INVALID_INPUT", "需要明确快照、候选摘要与 preparation；仅允许完整候选检查，拒绝重复键、别名、空值、未知字段或自定义命令", 2)
	}
	if len(raw) > 65536 {
		return bad()
	}
	v, err := config.Parse(raw)
	if err != nil {
		return bad()
	}
	fields, ok := v.(map[string]any)
	expected := 3
	if _, exists := fields["restore"]; exists {
		expected++
	}
	if !ok || len(fields) != expected {
		return bad()
	}
	for key := range fields {
		if key != "snapshot_id" && key != "candidate_digest" && key != "preparation" && key != "restore" {
			return bad()
		}
	}
	baseFields := map[string]any{"snapshot_id": fields["snapshot_id"], "candidate_digest": fields["candidate_digest"]}
	if v, ok := fields["restore"]; ok {
		baseFields["restore"] = v
	}
	base, err := ParseSnapshotReplayCandidateRequest(jsonData(baseFields))
	if err != nil {
		return bad()
	}
	if _, ok := fields["preparation"].(map[string]any); !ok {
		return bad()
	}
	p, err := ParseProfilePreparationRequest(jsonData(fields["preparation"]))
	if err != nil || p.RuleProbe && !p.Probe {
		return bad()
	}
	scope, err := NormalizeApplicationScope(p.ApplicationScope)
	if err != nil || scope != "" {
		return bad()
	}
	p.ApplicationScope = ""
	out.SnapshotReplayCandidateRequest, out.Preparation = base, p
	return out, nil
}

type ReplayNativeChange struct {
	Path    string             `json:"path"`
	Changes []workstore.Change `json:"changes"`
	Before  string             `json:"before_digest"`
	After   string             `json:"after_digest"`
}

// No source, handwritten rule, native JSON/TOML, tool arguments or secret body
// is returned. The complete compiler material stays in the native private area.
type SnapshotReplayPreparation struct {
	Restore       *SnapshotReplayRestoreTarget `json:"restore,omitempty"`
	ID            string                       `json:"id"`
	Snapshot      string                       `json:"snapshot_id"`
	Candidate     string                       `json:"candidate_digest"`
	Project       string                       `json:"project"`
	Scope         string                       `json:"scope"`
	Copy          string                       `json:"copy"`
	Root          string                       `json:"root"`
	Tools         []string                     `json:"tools"`
	Status        string                       `json:"status"`
	CanApply      bool                         `json:"can_apply"`
	Fresh         bool                         `json:"fresh"`
	Check         CheckReport                  `json:"check"`
	Changes       []ReplayNativeChange         `json:"native_changes"`
	SourceChanges []workstore.Change           `json:"source_changes"`
	LocalChanges  []workstore.Change           `json:"local_changes"`
	Gates         []inventory.Finding          `json:"gates"`
	Digest        string                       `json:"digest"`
	Boundary      string                       `json:"boundary"`
}

type snapshotReplayPrepared struct {
	Version       int                              `json:"schema_version"`
	Report        SnapshotReplayPreparation        `json:"report"`
	Request       SnapshotReplayPreparationRequest `json:"request"`
	Commit        string                           `json:"commit"`
	Native        Plan                             `json:"native"`
	Connections   []workstore.BatchTarget          `json:"connections"`
	Executable    string                           `json:"executable"`
	ExecutableSHA string                           `json:"executable_sha256"`
	Home          string                           `json:"home"`
	Digest        string                           `json:"digest"`
}

func replayPreparedDigest(p snapshotReplayPrepared) string {
	p.Digest = ""
	p.Report.Digest = ""
	return workstore.Hash(jsonData(p))
}
func (m *Manager) replayPreparedFile(id string) string {
	return filepath.Join(m.connectionArea(), "replay-prepared", id+".json")
}

// Use the existing compiler with a private asset store: even handwritten rule
// files can contain sensitive values. Never copy their bytes to source blobs.
func (m *Manager) replayCompiler(bundle source.Export) (*Manager, error) {
	if err := privatefs.Ensure(m.connectionArea()); err != nil {
		return nil, err
	}
	copyManager, copySource := *m, *m.Source
	copySource.Store = &workstore.Store{Root: m.Source.Store.Root, Local: m.connectionArea()}
	copyManager.Source, copyManager.probeLocal = &copySource, m.Source.Store.Local
	for _, pkg := range bundle.Packages {
		for _, e := range pkg.Tree.Entries {
			if e.Dir {
				continue
			}
			body, err := m.Source.Store.Data(e)
			if err != nil {
				return nil, err
			}
			scratch := workstore.Tree{Entries: map[string]workstore.Entry{}}
			if err = copySource.Store.PutFile(&scratch, "content", body, e.Mode); err != nil {
				return nil, err
			}
		}
	}
	return &copyManager, nil
}

func (m *Manager) compileSnapshotReplay(ctx context.Context, d source.ReplayDraft, req CheckRequest) (Plan, *Manager, error) {
	var p Plan
	var compiler *Manager
	as, err := selectAdapters(req.Tools)
	if err != nil {
		return p, nil, err
	}
	decisions, err := validateRuleResolutions(req.RuleResolutions, as)
	if err != nil {
		return p, nil, err
	}
	if err = m.profileApplicationBatch().Pending(); err != nil {
		return p, nil, err
	}
	err = m.Source.WithReplayExport(ctx, d, func(bundle source.Export) error {
		var e error
		compiler, e = m.replayCompiler(bundle)
		if e != nil {
			return e
		}
		selected, excluded := applicationBundle(bundle, SkillsRulesScope)
		p = Plan{Version: 1, Kind: "snapshot-replay-candidate", Root: m.Root, Source: bundle, Targets: []Target{}, ApplicationScope: SkillsRulesScope, ExcludedChecks: excluded, RuleResolutions: decisions}
		for _, a := range as {
			if err := ctx.Err(); err != nil {
				return err
			}
			var decision *RuleResolution
			for i := range decisions {
				if decisions[i].Tool == a.ID {
					decision = &decisions[i]
				}
			}
			t, err := compiler.target(a, selected, decision)
			if err != nil {
				return err
			}
			p.Targets = append(p.Targets, t)
			p.Blocked = p.Blocked || t.Status == "FAIL"
		}
		p.ContentDigest = contentDigest(p)
		return nil
	})
	return p, compiler, err
}

func (m *Manager) replayPreparationTools(ctx context.Context, id, commit string, requested []string) ([]string, error) {
	a, err := m.projectSnapshotArchive()
	if err != nil {
		return nil, err
	}
	r, err := a.Metadata(ctx, id)
	if err != nil {
		return nil, err
	}
	if r.Commit != commit {
		return nil, result.Fail("PLAN_STALE", "读取期间快照引用变化", 3)
	}
	tools := []string{}
	for id := range r.Adapters {
		tools = append(tools, id)
	}
	sort.Strings(tools)
	if _, err = selectAdapters(tools); err != nil {
		return nil, err
	}
	if len(requested) > 0 {
		selected, err := selectAdapters(requested)
		if err != nil {
			return nil, err
		}
		actual := []string{}
		for _, a := range selected {
			actual = append(actual, a.ID)
		}
		sort.Strings(actual)
		if string(jsonData(actual)) != string(jsonData(tools)) {
			return nil, result.Fail("SNAPSHOT_REPLAY_TOOL_SELECTION_MISMATCH", "回放检查必须包含原快照的全部工具；不能通过删减工具缩小范围", 4)
		}
	}
	return tools, nil
}

func (m *Manager) PrepareSnapshotReplay(ctx context.Context, req SnapshotReplayPreparationRequest, executable, home string) (SnapshotReplayPreparation, error) {
	out := SnapshotReplayPreparation{Restore: req.Restore, ID: workstore.ID(), Snapshot: req.Snapshot, Candidate: req.Candidate, Project: m.Source.Owner, Scope: m.Source.Scope, Copy: m.Source.Copy, Root: m.Root, Status: "NEEDS_CHECK", Fresh: true, Changes: []ReplayNativeChange{}, Gates: []inventory.Finding{}, Boundary: "重新编译冻结候选的完整技能包与规则，核对全部启用 MCP 的现有授权及连接。默认静态；仅明确 probe 才在空配置临时项目运行工具发现，规则来源另行确认。此检查不应用源、本机草稿或工具文件，不启动 MCP 或发送模型请求。CHECKED 只表示本次候选检查完成；须另行生成共同回放提案并确认，不能使用检查摘要调用普通应用。L3/L4 保持未验证。"}
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || !filepath.IsAbs(home) || filepath.Join(home, "local") != m.Source.Store.Local {
		return out, result.Fail("MCP_CLIENT_CONFIG_BINDING_INVALID", "完整候选检查需要当前显式 home 与稳定绝对程序路径；未启动进程", 2)
	}
	if req.Preparation.RuleProbe && !req.Preparation.Probe {
		return out, result.Fail("INVALID_INPUT", "规则来源核验需同时明确工具发现", 2)
	}
	scope, err := NormalizeApplicationScope(req.Preparation.ApplicationScope)
	if err != nil || scope != "" {
		return out, result.Fail("INVALID_INPUT", "回放准备检查完整候选，不能排除其它配置组件", 2)
	}
	d, commit, err := m.snapshotReplayCandidateFor(ctx, req.Snapshot, req.Restore)
	if err != nil {
		return out, err
	}
	if d.Digest != req.Candidate {
		return out, result.Fail("PLAN_STALE", "回放候选变化，请重新审查", 3)
	}
	out.Tools, err = m.replayPreparationTools(ctx, req.Snapshot, commit, req.Preparation.Tools)
	if err != nil {
		return out, err
	}
	req.Preparation.Tools = out.Tools
	p, compiler, err := m.compileSnapshotReplay(ctx, d, req.Preparation)
	if err != nil {
		return out, err
	}
	sha, err := executableDigest(executable)
	if err != nil {
		return out, err
	}
	out.SourceChanges, out.LocalChanges = workstore.Changes(d.SourceBefore, d.SourceAfter), workstore.Changes(d.LocalBefore, d.LocalAfter)
	out.Check, err = compiler.InspectDraftScoped(ctx, p.Targets, d.Digest, req.Preparation.Probe && !p.Blocked, req.Preparation.RuleProbe && !p.Blocked, SkillsRulesScope, p.ExcludedChecks)
	if err != nil {
		return out, err
	}
	if p.Blocked {
		out.Status = "BLOCKED"
		out.Gates = append(out.Gates, inventory.Finding{Status: "FAIL", Code: "REPLAY_NATIVE_PREFLIGHT_FAILED"})
	}
	if req.Preparation.Probe && !p.Blocked {
		q, e := compiler.QualifyDraft(p.Targets, d.Digest, SkillsRulesScope, p.ExcludedChecks, out.Check)
		if e == nil {
			p.Qualification = &q
			out.Status = "CHECKED"
		} else {
			var classified *result.Error
			if !errors.As(e, &classified) {
				return out, e
			}
			out.Status = "NEEDS_ATTENTION"
			out.Gates = append(out.Gates, inventory.Finding{Status: "UNKNOWN", Code: classified.Code})
		}
	}
	connections := []workstore.BatchTarget{}
	err = m.Source.WithReplayMCPClientConfigs(ctx, d, executable, home, func(configs []source.MCPClientConfiguration, sessions []source.MCPSessionRequest) error {
		requests, pointers := []MCPConnectionRequest{}, []*source.MCPClientConfiguration{}
		for i, s := range sessions {
			requests = append(requests, MCPConnectionRequest{Action: "install", Tools: out.Tools, Session: s})
			pointers = append(pointers, &configs[i])
		}
		var e error
		connections, e = m.compileConnectionSet(out.Tools, requests, pointers, executable, home, true)
		return e
	})
	if err != nil {
		var classified *result.Error
		if !errors.As(err, &classified) {
			return out, err
		}
		out.Gates = append(out.Gates, inventory.Finding{Status: "UNKNOWN", Code: classified.Code})
		if out.Status != "BLOCKED" {
			out.Status = "NEEDS_ATTENTION"
		}
	}
	// Recompile after explicit probes and current MCP gates. Every exact native
	// observation and rule-priority input must still be the one checked above.
	fresh, _, err := m.compileSnapshotReplay(ctx, d, req.Preparation)
	if err != nil {
		return out, err
	}
	if fresh.ContentDigest != p.ContentDigest {
		return out, result.Fail("PLAN_STALE", "检查期间完整候选产物或当前工具文件变化", 3)
	}
	currentSHA, err := executableDigest(executable)
	if err != nil || currentSHA != sha {
		return out, result.Fail("PLAN_STALE", "检查期间 AIDock 程序变化", 3)
	}
	for _, t := range append(batchTargets(p), connections...) {
		out.Changes = append(out.Changes, ReplayNativeChange{Path: t.Path, Changes: workstore.Changes(t.Before, t.After), Before: workstore.Digest(t.Before), After: workstore.Digest(t.After)})
	}
	// The source compiler and native-file transaction have distinct blob keys,
	// even within the same private area. Normalize native before/after assets
	// into the owning native transaction store for subsequent joint recovery.
	for _, t := range batchTargets(p) {
		for _, tree := range []workstore.Tree{t.Before, t.After} {
			for _, e := range tree.Entries {
				if e.Dir {
					continue
				}
				body, err := compiler.Source.Store.Data(e)
				if err != nil {
					return out, err
				}
				scratch := workstore.Tree{Entries: map[string]workstore.Entry{}}
				if err = m.connectionStore().PutFile(&scratch, "content", body, e.Mode); err != nil {
					return out, err
				}
			}
		}
	}
	stored := snapshotReplayPrepared{Version: 1, Report: out, Request: req, Commit: commit, Native: p, Connections: connections, Executable: executable, ExecutableSHA: sha, Home: home}
	out.Digest = replayPreparedDigest(stored)
	stored.Report = out
	stored.Digest = out.Digest
	if err = save(m.replayPreparedFile(out.ID), jsonData(stored)); err != nil {
		return out, err
	}
	return out, nil
}

// Read a saved result without rerunning discovery. Historical success never
// becomes current merely because it is the latest file on disk.
func (m *Manager) ReadSnapshotReplayPreparation(ctx context.Context, id string) (SnapshotReplayPreparation, error) {
	var stored snapshotReplayPrepared
	if !validID.MatchString(id) {
		return stored.Report, result.Fail("INVALID_INPUT", "需要当前绑定的候选检查 UUID", 2)
	}
	raw, err := workstore.ReadFile(m.replayPreparedFile(id))
	if err != nil {
		return stored.Report, err
	}
	if strict(raw, &stored) != nil || stored.Version != 1 || stored.Report.ID != id || stored.Report.Root != m.Root || stored.Report.Project != m.Source.Owner || stored.Report.Scope != m.Source.Scope || stored.Report.Copy != m.Source.Copy || stored.Digest != stored.Report.Digest || stored.Digest != replayPreparedDigest(stored) || stored.Report.CanApply || string(jsonData(stored.Report.Restore)) != string(jsonData(stored.Request.Restore)) {
		return SnapshotReplayPreparation{}, result.Fail("CHECK_CORRUPT", "回放候选检查绑定或完整摘要不符", 5)
	}
	out := stored.Report
	err = m.revalidateReplayPreparation(ctx, stored)
	if err != nil {
		out.Status, out.Fresh = "STALE", false
		out.Changes, out.SourceChanges, out.LocalChanges = []ReplayNativeChange{}, []workstore.Change{}, []workstore.Change{}
		code := "REPLAY_PREPARATION_STALE"
		var classified *result.Error
		if errors.As(err, &classified) {
			code = classified.Code
		}
		out.Gates = append(append([]inventory.Finding{}, out.Gates...), inventory.Finding{Status: "UNKNOWN", Code: code})
		out.Check.Stale, out.Check.StaleReason = true, code
	}
	return out, nil
}

func (m *Manager) revalidateReplayPreparation(ctx context.Context, p snapshotReplayPrepared) error {
	d, commit, err := m.snapshotReplayCandidateFor(ctx, p.Request.Snapshot, p.Request.Restore)
	if err != nil {
		return err
	}
	if d.Digest != p.Request.Candidate || commit != p.Commit {
		return result.Fail("PLAN_STALE", "冻结候选或快照绑定变化", 3)
	}
	tools, err := m.replayPreparationTools(ctx, p.Request.Snapshot, commit, p.Request.Preparation.Tools)
	if err != nil {
		return err
	}
	sha, err := executableDigest(p.Executable)
	if err != nil || sha != p.ExecutableSHA {
		return result.Fail("PLAN_STALE", "AIDock 程序变化", 3)
	}
	fresh, compiler, err := m.compileSnapshotReplay(ctx, d, p.Request.Preparation)
	if err != nil {
		return err
	}
	if fresh.ContentDigest != p.Native.ContentDigest {
		return result.Fail("PLAN_STALE", "完整候选、源依赖、规则优先级或目标观察变化", 3)
	}
	if err = compiler.InspectionFresh(p.Report.Check); err != nil {
		return err
	}
	if p.Native.Qualification != nil {
		q, err := compiler.QualifyDraft(fresh.Targets, d.Digest, SkillsRulesScope, fresh.ExcludedChecks, p.Report.Check)
		if err != nil {
			return err
		}
		if string(jsonData(q)) != string(jsonData(p.Native.Qualification)) {
			return result.Fail("CHECK_CORRUPT", "保存的候选检查资格不一致", 5)
		}
	}
	return m.Source.WithReplayMCPClientConfigs(ctx, d, p.Executable, p.Home, func(configs []source.MCPClientConfiguration, sessions []source.MCPSessionRequest) error {
		requests, pointers := []MCPConnectionRequest{}, []*source.MCPClientConfiguration{}
		for i, s := range sessions {
			requests = append(requests, MCPConnectionRequest{Action: "install", Tools: tools, Session: s})
			pointers = append(pointers, &configs[i])
		}
		connections, err := m.compileConnectionSet(tools, requests, pointers, p.Executable, p.Home, true)
		if err != nil {
			return err
		}
		if string(jsonData(connections)) != string(jsonData(p.Connections)) {
			return result.Fail("PLAN_STALE", "候选连接或原生字段归属变化", 3)
		}
		return nil
	})
}
