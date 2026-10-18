package native

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Only checked identities cross the replica/native boundary. Raw source,
// receiver content and common transaction roots are never caller inputs.
type PublicationRestorationCheck struct {
	Binding string `json:"applied_binding"`
	ID      string `json:"check_id"`
	Digest  string `json:"check_digest"`
}

type publicationRegeneratedView struct {
	candidate PublicationRestorationCandidate
	prepared  configurationPrepared
	frozen    source.FrozenMCP
	sessions  []source.MCPSessionRequest
	key       string
}

// Opaque compiler output; the owning replica coordinator must independently
// verify the original node/selection, preserve every other root and commit the
// REAL common plan. This value cannot apply or invent a member transaction.
type PublicationRegeneratedRestoration struct {
	review  PublicationRestorationMaterial
	views   []publicationRegeneratedView
	targets []workstore.BatchTarget
}

func (p PublicationRegeneratedRestoration) Targets() []workstore.BatchTarget {
	return clonePublicationTargets(p.targets)
}

func (m *Manager) CompilePublicationRestoration(ctx context.Context, proof projectsnapshot.PublicationCommit, id, digest string, checks []PublicationRestorationCheck, executable, home string) (PublicationRegeneratedRestoration, error) {
	var out PublicationRegeneratedRestoration
	review, err := m.InspectPublicationRestoration(ctx, proof, id, digest)
	if err != nil {
		return out, err
	}
	if len(checks) == 0 || len(checks) != len(review.views) {
		return out, result.Fail("PUBLICATION_RESTORE_CHECK_REQUIRED", "重新生成必须明确检查此成员要恢复的全部原应用", 4)
	}
	ordered := append([]PublicationRestorationCheck{}, checks...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Binding < ordered[j].Binding })
	out.review, out.targets = review, clonePublicationTargets(review.member.Targets)
	c, _ := proof.Coordinator(m.Source.Store.Local)
	// Native receiver bytes remain in native/private caches. Reading from the
	// original common namespace avoids the per-root Store key ambiguity.
	for _, t := range review.member.Targets {
		if !strings.HasPrefix(t.Path, "native/") {
			continue
		}
		for _, tree := range []workstore.Tree{t.Before, t.After} {
			if err := copyReplayTree(c.Blobs, m.connectionStore(), tree); err != nil {
				return PublicationRegeneratedRestoration{}, err
			}
		}
	}
	images := map[string]workstore.Tree{connectionLedgerPath: replayNativeImage(review.member, connectionLedgerPath, true, true)}
	generatedPaths := map[string]bool{}
	for i, check := range ordered {
		if i > 0 && check.Binding == ordered[i-1].Binding {
			return PublicationRegeneratedRestoration{}, result.Fail("INVALID_INPUT", "同一原应用不能重复提供检查", 2)
		}
		candidate, err := m.PublicationRestoreCandidate(ctx, proof, id, digest, check.Binding)
		if err != nil {
			return PublicationRegeneratedRestoration{}, err
		}
		checked, err := candidate.ReadPreparation(ctx, check.ID)
		if err != nil {
			return PublicationRegeneratedRestoration{}, err
		}
		if checked.Digest != check.Digest || checked.Status != "CHECKED" || !checked.Fresh || !checked.MCPReady || checked.CanApply {
			return PublicationRegeneratedRestoration{}, result.Fail("PUBLICATION_RESTORE_CHECK_REQUIRED", "原应用需要当前完整检查和完整摘要", 4)
		}
		prepared, err := m.loadConfigurationPreparationKind(check.ID, "publication-restore-candidate")
		if err != nil {
			return PublicationRegeneratedRestoration{}, err
		}
		if prepared.Native.Qualification == nil || !prepared.Request.Probe || prepared.Executable != executable || prepared.Home != home || prepared.Report.Candidate != candidate.digest {
			return PublicationRegeneratedRestoration{}, result.Fail("PUBLICATION_RESTORE_CHECK_REQUIRED", "恢复检查的原候选、工具资格、程序或数据根不符", 4)
		}
		view := publicationRegeneratedView{candidate: candidate, prepared: prepared}
		err = m.Source.WithReplaySnapshotInputs(ctx, candidate.draft, func(bundle source.Export, tree workstore.Tree, cfg source.SnapshotConfiguration) error {
			if string(jsonData(bundle)) != string(jsonData(prepared.Native.Source)) {
				return result.Fail("PLAN_STALE", "原冻结导出与恢复检查不一致", 3)
			}
			view.frozen = source.FrozenMCP{Tree: tree, Effective: bundle, Configuration: cfg}
			return nil
		})
		if err != nil {
			return PublicationRegeneratedRestoration{}, err
		}
		as, err := selectAdapters(candidate.tools)
		if err != nil {
			return PublicationRegeneratedRestoration{}, err
		}
		adapters := map[string]string{}
		for _, a := range as {
			if a.MCPConfig == nil {
				return PublicationRegeneratedRestoration{}, result.Fail("CONFIG_CONTRACT_UNSUPPORTED", "原工具没有受支持的 MCP 接收合同", 4)
			}
			if _, exists := images[a.MCPConfig.Path]; !exists {
				images[a.MCPConfig.Path] = replayNativeImage(review.member, a.MCPConfig.Path, true, true)
			}
			adapters[a.ID] = a.Version
		}
		err = m.Source.WithReplayMCPClientConfigs(ctx, candidate.draft, executable, home, func(configs []source.MCPClientConfiguration, sessions []source.MCPSessionRequest) error {
			if string(jsonData(sessions)) != string(jsonData(prepared.Sessions)) {
				return result.Fail("PLAN_STALE", "恢复检查后的当前授权或运行时变化", 3)
			}
			view.sessions = append([]source.MCPSessionRequest{}, sessions...)
			view.key = m.appliedKey(view.frozen.Effective, sessions, adapters, prepared.ExecutableSHA)
			requests, pointers := []MCPConnectionRequest{}, []*source.MCPClientConfiguration{}
			for i, session := range sessions {
				configs[i] = m.appliedConfiguration(configs[i], view.key)
				requests = append(requests, MCPConnectionRequest{Action: "install", Tools: candidate.tools, Session: session})
				pointers = append(pointers, &configs[i])
			}
			compiled, err := m.compileConnectionImages(images, candidate.tools, requests, pointers, executable, home, true)
			if err != nil {
				return err
			}
			for _, target := range compiled {
				if target.Path != connectionLedgerPath && target.Path != ".mcp.json" && target.Path != ".codex/config.toml" {
					return result.Fail("PUBLICATION_RESTORE_INVALID", "恢复编译器输出未适配路径", 5)
				}
				images[target.Path], generatedPaths[target.Path] = workstore.Clone(target.After), true
			}
			return nil
		})
		if err != nil {
			return PublicationRegeneratedRestoration{}, err
		}
		out.views = append(out.views, view)
	}
	for path := range generatedPaths {
		root, member, ok := replayNativeContainer(path)
		if !ok {
			return PublicationRegeneratedRestoration{}, result.Fail("PUBLICATION_RESTORE_INVALID", "恢复编译器输出未适配容器", 5)
		}
		for i := range out.targets {
			if out.targets[i].Path != "native/"+root {
				continue
			}
			if member == "" {
				out.targets[i].After = workstore.Clone(images[path])
			} else {
				replaceReplayMember(&out.targets[i].After, member, true, images[path])
			}
		}
		if err = copyReplayTree(m.connectionStore(), c.Blobs, images[path]); err != nil {
			return PublicationRegeneratedRestoration{}, err
		}
	}
	for _, view := range out.views {
		read, err := view.candidate.ReadPreparation(ctx, view.prepared.Report.ID)
		if err != nil {
			return PublicationRegeneratedRestoration{}, err
		}
		if !read.Fresh || read.Status != "CHECKED" || !read.MCPReady || read.Digest != view.prepared.Digest {
			return PublicationRegeneratedRestoration{}, result.Fail("PLAN_STALE", "重新生成期间完整恢复检查变化", 3)
		}
	}
	return out, nil
}

func (p PublicationRegeneratedRestoration) memberPlan(proof projectsnapshot.PublicationCommit) (workstore.BatchPlan, error) {
	if p.review.manager == nil || proof.Publication != p.review.proof.Publication || proof.Transaction.Action != p.review.plan.Action || proof.Transaction.Reference != p.review.plan.Reference {
		return workstore.BatchPlan{}, result.Fail("PUBLICATION_RESTORE_INVALID", "重新生成只能用于原共同恢复角色", 5)
	}
	m := p.review.manager
	if err := proof.Validate(m.Source.Store.Local); err != nil {
		return workstore.BatchPlan{}, err
	}
	if string(jsonData(proof.Members)) != string(jsonData(p.review.proof.Members)) || proof.Publisher != p.review.proof.Publisher {
		return workstore.BatchPlan{}, result.Fail("PUBLICATION_RESTORE_INVALID", "重新生成改变了原批次参与者", 5)
	}
	member := workstore.BatchPlan{ID: proof.Transaction.ID, Targets: []workstore.BatchTarget{}}
	prefix := projectsnapshot.PublicationMemberPrefix(m.Source.Copy)
	for _, target := range proof.Transaction.Targets {
		if strings.HasPrefix(target.Path, prefix) {
			target.Path = strings.TrimPrefix(target.Path, prefix)
			member.Targets = append(member.Targets, target)
		}
	}
	if string(jsonData(member.Targets)) != string(jsonData(p.targets)) {
		return workstore.BatchPlan{}, result.Fail("PUBLICATION_RESTORE_INVALID", "真实共同事务改变了恢复编译器的成员材料", 5)
	}
	return member, nil
}

func publicationRestorationNative(tx workstore.BatchPlan) []workstore.BatchTarget {
	out := []workstore.BatchTarget{}
	for _, path := range []string{".claude/skills", ".agents/skills", "CLAUDE.md", "AGENTS.md", ".codex/config.toml", ".mcp.json", connectionLedgerPath} {
		file := filepath.Base(path) != "skills"
		out = append(out, workstore.BatchTarget{Path: path, File: file, Before: replayNativeImage(tx, path, file, false), After: replayNativeImage(tx, path, file, true)})
	}
	return out
}

func (p PublicationRegeneratedRestoration) Capture(ctx context.Context, proof projectsnapshot.PublicationCommit) ([]projectsnapshot.Record, error) {
	member, err := p.memberPlan(proof)
	if err != nil {
		return nil, err
	}
	m := p.review.manager
	archive, err := m.projectSnapshotArchive()
	if err != nil {
		return nil, err
	}
	native := workstore.BatchPlan{Version: 1, Kind: "native-targets", ID: proof.Transaction.ID, Root: m.Root, Identity: "profile/" + m.Source.Owner + "/" + m.Source.Scope + "/" + m.Source.Copy, Action: proof.Transaction.Action, Reference: proof.Transaction.Reference, Targets: publicationRestorationNative(member)}
	native.Digest = workstore.BatchPlanDigest(native)
	out := []projectsnapshot.Record{}
	for _, view := range p.views {
		adapters := map[string]string{}
		for _, target := range view.prepared.Native.Targets {
			adapters[target.Adapter.ID] = target.Adapter.Version
		}
		own := proof
		own.View = &projectsnapshot.PublicationView{Executable: view.prepared.Executable, ExecutableSHA: view.prepared.ExecutableSHA, Sessions: view.sessions}
		record, err := archive.Capture(ctx, projectsnapshot.Content{Trigger: "replica publication", Source: view.frozen.Tree, Effective: view.frozen.Effective, Configuration: &view.frozen.Configuration, ActiveKey: view.key, Adapters: adapters, Transaction: native, Publication: &own, RestoredDraft: true, RestoreOf: proof.Transaction.Reference}, m.Source.Store, m.connectionStore())
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

// Called inside all source/Local/native and library locks on both sides of
// the owning COMMON transaction. No probes, source locks or runtime sessions.
func (p PublicationRegeneratedRestoration) Validate(ctx context.Context, proof projectsnapshot.PublicationCommit, after bool) error {
	member, err := p.memberPlan(proof)
	if err != nil {
		return err
	}
	m := p.review.manager
	if err := p.review.originalCommitted(); err != nil {
		return err
	}
	if err := m.Source.ValidateReplayIdentity(ctx, false); err != nil {
		return err
	}
	c, _ := p.review.proof.Coordinator(m.Source.Store.Local)
	seed, err := c.LoadPlan(p.review.plan.ID)
	if err != nil {
		return err
	}
	if string(jsonData(seed)) != string(jsonData(p.review.plan)) {
		return result.Fail("PLAN_STALE", "重新生成所依据的真实共同恢复提案变化", 3)
	}
	s, l := replayApplicationTrees(snapshotReplayApplication{Transaction: member}, after)
	for _, view := range p.views {
		if err := m.appliedCommitLocked(view.candidate.view); err != nil {
			return err
		}
		if err := m.appliedArchiveIdentityCurrent(ctx, view.candidate.view); err != nil {
			return err
		}
		sha, err := executableDigest(view.prepared.Executable)
		if err != nil || sha != view.prepared.ExecutableSHA {
			return result.Fail("PLAN_STALE", "恢复使用的当前 AIDock 程序变化", 3)
		}
		saved, err := m.loadConfigurationPreparationKind(view.prepared.Report.ID, "publication-restore-candidate")
		if err != nil {
			return err
		}
		if saved.Digest != view.prepared.Digest {
			return result.Fail("CHECK_CORRUPT", "完整恢复检查原件变化", 5)
		}
		compiler, err := m.replayCompiler(view.prepared.Native.Source)
		if err != nil {
			return err
		}
		q, err := compiler.QualifyDraft(view.prepared.Native.Targets, view.candidate.digest, SkillsRulesScope, view.prepared.Native.ExcludedChecks, view.prepared.Report.Check)
		if err != nil {
			return err
		}
		if string(jsonData(q)) != string(jsonData(view.prepared.Native.Qualification)) {
			return result.Fail("CHECK_CORRUPT", "完整恢复的工具发现资格不符", 5)
		}
		for _, target := range view.prepared.Native.Targets {
			if target.Rule == nil || target.Rule.Override == nil {
				continue
			}
			if target.Adapter.ID != "codex-cli" || target.Rule.Override.Path != "AGENTS.override.md" {
				return result.Fail("CHECK_CORRUPT", "完整恢复包含未适配的规则遮盖观察路径", 5)
			}
			observed, err := m.connectionStore().CaptureFile(filepath.Join(m.Root, target.Rule.Override.Path))
			if err != nil {
				return err
			}
			if workstore.Digest(observed) != workstore.Digest(target.Rule.Override.Before) {
				return result.Fail("PLAN_STALE", "完整恢复的规则遮盖条件变化", 3)
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
			if observed != view.frozen.Effective.BridgeDigest {
				return result.Fail("PLAN_STALE", "完整恢复的外部来源保护登记变化", 3)
			}
		}
		if err := m.Source.ValidateFrozenMCPTransaction(ctx, view.frozen, s, l, view.sessions); err != nil {
			return err
		}
	}
	return nil
}
