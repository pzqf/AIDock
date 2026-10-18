package native

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Opaque compiler material for the common publication owner. HTTP/CLI cannot
// decode or manufacture it; JSON serialization exposes no raw configuration.
// It grants no write/commit permission and fabricates no member journal.
type ConfigurationPublicationMaterial struct {
	manager      *Manager
	prepared     configurationPrepared
	candidate    string
	publication  string
	activeKey    string
	sourceBefore workstore.Tree
	sourceAfter  workstore.Tree
	local        workstore.Tree
	frozen       source.SnapshotConfiguration
	native       []workstore.BatchTarget
	targets      []workstore.BatchTarget
}

func clonePublicationTargets(targets []workstore.BatchTarget) []workstore.BatchTarget {
	out := append([]workstore.BatchTarget{}, targets...)
	for i := range out {
		out[i].Before, out[i].After = workstore.Clone(out[i].Before), workstore.Clone(out[i].After)
	}
	return out
}

func (p ConfigurationPublicationMaterial) Targets() []workstore.BatchTarget {
	return clonePublicationTargets(p.targets)
}
func (p ConfigurationPublicationMaterial) ActiveKey() string { return p.activeKey }
func (p ConfigurationPublicationMaterial) Binding() projectsnapshot.Binding {
	if p.manager == nil {
		return projectsnapshot.Binding{}
	}
	m := p.manager
	return projectsnapshot.Binding{Project: m.Source.Owner, Scope: m.Source.Scope, Copy: m.Source.Copy, SourceRoot: m.Source.Store.Root, NativeRoot: m.Root}
}

func (m *Manager) CompileConfigurationPublication(ctx context.Context, c source.ConfigurationCandidate, id, digest, publication, executable, home string, blobs *workstore.Store) (ConfigurationPublicationMaterial, error) {
	var out ConfigurationPublicationMaterial
	if !validID.MatchString(publication) || blobs == nil || blobs.Local != projectsnapshot.PublicationArea(m.Source.Store.Local, m.Source.Owner, m.Source.Scope, publication) || blobs.Root != filepath.Join(m.Source.Store.Local, "replica-control", workstore.Hash([]byte(m.Source.Owner+"/"+m.Source.Scope))) {
		return out, result.Fail("INVALID_ROOTS", "完整副本原生材料只能进入明确批次的受限本机区", 2)
	}
	checked, err := m.ReadConfigurationPreparation(ctx, c, id)
	if err != nil {
		return out, err
	}
	if checked.Digest != digest || checked.Status != "CHECKED" || !checked.Fresh || !checked.MCPReady || checked.CanApply {
		return out, result.Fail("CONFIGURATION_CHECK_REQUIRED", "共同发布需要当前完整候选检查及完整摘要", 4)
	}
	p, err := m.loadConfigurationPreparation(id)
	if err != nil {
		return out, err
	}
	if p.Native.Qualification == nil || !p.Request.Probe || p.MCPCode != "" || p.Executable != executable || p.Home != home {
		return out, result.Fail("CONFIGURATION_CHECK_REQUIRED", "完整候选工具资格、MCP、程序或数据根不符", 4)
	}
	out = ConfigurationPublicationMaterial{manager: m, prepared: p, candidate: c.Digest(), publication: publication}
	err = m.Source.WithCandidateSnapshotInputs(ctx, c, func(bundle source.Export, after workstore.Tree, cfg source.SnapshotConfiguration) error {
		if string(jsonData(bundle)) != string(jsonData(p.Native.Source)) {
			return result.Fail("PLAN_STALE", "候选源导出与完整候选检查不同", 3)
		}
		before, err := m.Source.Store.Capture(m.Source.Store.Root)
		if err != nil {
			return err
		}
		out.sourceBefore, out.sourceAfter, out.local, out.frozen = before, workstore.Clone(after), workstore.Clone(cfg.LocalTree), cfg
		return nil
	})
	if err != nil {
		return ConfigurationPublicationMaterial{}, err
	}
	adapters := map[string]string{}
	for _, t := range p.Native.Targets {
		adapters[t.Adapter.ID] = t.Adapter.Version
	}
	err = m.Source.WithCandidateMCPClientConfigs(ctx, c, executable, home, func(configs []source.MCPClientConfiguration, sessions []source.MCPSessionRequest) error {
		if string(jsonData(sessions)) != string(jsonData(p.Sessions)) {
			return result.Fail("PLAN_STALE", "全部启用 MCP 的当前授权或运行时与检查不同", 3)
		}
		requests, pointers := []MCPConnectionRequest{}, []*source.MCPClientConfiguration{}
		for i, s := range sessions {
			requests = append(requests, MCPConnectionRequest{Action: "install", Tools: p.Report.Tools, Session: s})
			pointers = append(pointers, &configs[i])
		}
		observed, err := m.compileConnectionSet(p.Report.Tools, requests, pointers, executable, home, true)
		if err != nil {
			return err
		}
		if string(jsonData(observed)) != string(jsonData(p.Connections)) {
			return result.Fail("PLAN_STALE", "全部原生连接与完整检查不同", 3)
		}
		out.activeKey = m.appliedKey(p.Native.Source, sessions, adapters, p.ExecutableSHA)
		pointers = []*source.MCPClientConfiguration{}
		for i := range configs {
			configs[i] = m.appliedConfiguration(configs[i], out.activeKey)
			pointers = append(pointers, &configs[i])
		}
		fixed, err := m.compileConnectionSet(p.Report.Tools, requests, pointers, executable, home, true)
		if err == nil {
			out.native = append(batchTargets(p.Native), fixed...)
		}
		return err
	})
	if err != nil {
		return ConfigurationPublicationMaterial{}, err
	}
	out.targets, err = m.completeConfigurationTargets(out.sourceBefore, out.sourceAfter, out.local, out.local, out.native, blobs)
	if err != nil {
		return ConfigurationPublicationMaterial{}, err
	}
	fresh, err := m.ReadConfigurationPreparation(ctx, c, id)
	if err != nil {
		return ConfigurationPublicationMaterial{}, err
	}
	if fresh.Digest != digest || fresh.Status != "CHECKED" || !fresh.Fresh || !fresh.MCPReady {
		return ConfigurationPublicationMaterial{}, result.Fail("PLAN_STALE", "共同备份组装期间完整候选变化", 3)
	}
	return out, nil
}

func (p ConfigurationPublicationMaterial) Capture(ctx context.Context, proof projectsnapshot.PublicationCommit) (projectsnapshot.Record, error) {
	if p.manager == nil || proof.Publication != p.publication || proof.Transaction.Action != "apply" {
		return projectsnapshot.Record{}, result.Fail("INVALID_INPUT", "需要原完整候选的实际应用批次", 2)
	}
	m := p.manager
	if err := proof.Validate(m.Source.Store.Local); err != nil {
		return projectsnapshot.Record{}, err
	}
	byPath := map[string]workstore.BatchTarget{}
	prefix := projectsnapshot.PublicationMemberPrefix(m.Source.Copy)
	for _, target := range proof.Transaction.Targets {
		if strings.HasPrefix(target.Path, prefix) {
			target.Path = strings.TrimPrefix(target.Path, prefix)
			byPath[target.Path] = target
		}
	}
	if len(byPath) != len(p.targets) {
		return projectsnapshot.Record{}, result.Fail("PUBLICATION_COMMIT_INVALID", "全批事务缺少原完整目标集合", 5)
	}
	for _, target := range p.targets {
		if string(jsonData(byPath[target.Path])) != string(jsonData(target)) {
			return projectsnapshot.Record{}, result.Fail("PUBLICATION_COMMIT_INVALID", "全批事务改变了已检查目标的材料", 5)
		}
	}
	proof.View = &projectsnapshot.PublicationView{Executable: p.prepared.Executable, ExecutableSHA: p.prepared.ExecutableSHA, Sessions: append([]source.MCPSessionRequest{}, p.prepared.Sessions...)}
	tx := workstore.BatchPlan{Version: 1, Kind: "native-targets", ID: proof.Transaction.ID, Root: m.Root, Identity: "profile/" + m.Source.Owner + "/" + m.Source.Scope + "/" + m.Source.Copy, Action: "apply", Targets: p.native}
	tx.Digest = workstore.BatchPlanDigest(tx)
	adapters := map[string]string{}
	for _, t := range p.prepared.Native.Targets {
		adapters[t.Adapter.ID] = t.Adapter.Version
	}
	a, err := m.projectSnapshotArchive()
	if err != nil {
		return projectsnapshot.Record{}, err
	}
	return a.Capture(ctx, projectsnapshot.Content{Trigger: "replica publication", Source: p.sourceAfter, Effective: p.prepared.Native.Source, Configuration: &p.frozen, ActiveKey: p.activeKey, Adapters: adapters, Transaction: tx, Publication: &proof}, m.Source.Store, m.connectionStore())
}

// Validate is called ONLY under the owning coordinator's complete source,
// Local and native locks, and its caller's library locks. It does not reacquire
// those locks, execute probes or consume old permissions as present authority.
func (p ConfigurationPublicationMaterial) Validate(ctx context.Context, after bool) error {
	if p.manager == nil {
		return result.Fail("INVALID_INPUT", "缺少完整候选编译材料", 2)
	}
	m := p.manager
	if err := m.Source.ValidateReplayIdentity(ctx, false); err != nil {
		return err
	}
	current, err := m.loadConfigurationPreparation(p.prepared.Report.ID)
	if err != nil {
		return err
	}
	if current.Digest != p.prepared.Digest {
		return result.Fail("CHECK_CORRUPT", "原完整检查记录已变化", 5)
	}
	sha, err := executableDigest(p.prepared.Executable)
	if err != nil || sha != p.prepared.ExecutableSHA {
		return result.Fail("PLAN_STALE", "共同发布使用的 AIDock 程序变化", 3)
	}
	compiler, err := m.replayCompiler(p.prepared.Native.Source)
	if err != nil {
		return err
	}
	q, err := compiler.QualifyDraft(p.prepared.Native.Targets, p.candidate, SkillsRulesScope, p.prepared.Native.ExcludedChecks, p.prepared.Report.Check)
	if err != nil {
		return err
	}
	if string(jsonData(q)) != string(jsonData(p.prepared.Native.Qualification)) {
		return result.Fail("CHECK_CORRUPT", "共同发布的完整工具发现资格不符", 5)
	}
	for _, target := range p.prepared.Native.Targets {
		if target.Rule != nil && target.Rule.Override != nil {
			if target.Adapter.ID != "codex-cli" || target.Rule.Override.Path != "AGENTS.override.md" {
				return result.Fail("CHECK_CORRUPT", "完整候选包含未适配规则观察路径", 5)
			}
			observed, err := m.connectionStore().CaptureFile(filepath.Join(m.Root, target.Rule.Override.Path))
			if err != nil {
				return err
			}
			if workstore.Digest(observed) != workstore.Digest(target.Rule.Override.Before) {
				return result.Fail("PLAN_STALE", "规则遮盖条件变化", 3)
			}
		}
	}
	if m.Source.BridgeFile != "" {
		body, err := workstore.ReadFile(m.Source.BridgeFile)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		observed := ""
		if err == nil {
			observed = workstore.Hash(body)
		}
		if observed != p.prepared.Native.Source.BridgeDigest {
			return result.Fail("PLAN_STALE", "外部来源保护登记变化", 3)
		}
	}
	currentSource := p.sourceBefore
	if after {
		currentSource = p.sourceAfter
	}
	return m.Source.ValidateConfigurationCandidateTransaction(ctx, source.FrozenMCP{Tree: p.sourceAfter, Effective: p.prepared.Native.Source, Configuration: p.frozen}, currentSource, p.local, p.prepared.Sessions)
}
