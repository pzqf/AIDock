package replica

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/generator"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

const mirrorGenerator = "aidock-owned-mirror-v1"
const externalMirrorGenerator = "aidock-external-mirror-v1"

type GenerationRequest struct {
	ApplicationScope string   `json:"application_scope,omitempty"`
	Plan             string   `json:"plan_id"`
	Digest           string   `json:"digest"`
	Tools            []string `json:"tools"`
}

// ExternalGenerationRequest consumes a current PASS output from the offline
// generator exchange. It still creates only an independent rehearsal
// candidate; it never starts the external program or publishes a copy.
type ExternalGenerationRequest struct {
	Plan        string `json:"plan_id"`
	Digest      string `json:"digest"`
	Stage       string `json:"stage_id"`
	StageDigest string `json:"stage_digest"`
}

type GenerationRepairRequest struct {
	Request
	ApplicationScope string   `json:"application_scope,omitempty"`
	Comparison       string   `json:"comparison_digest"`
	Tools            []string `json:"tools"`
}

// Generation binds complete source inputs, output observations and generated
// trees in Local. A descriptor never grants permission to launch a command.
type Generation struct {
	ApplicationScope     string            `json:"application_scope,omitempty"`
	Version              int               `json:"schema_version"`
	Kind                 string            `json:"kind"`
	ID                   string            `json:"id"`
	Plan                 string            `json:"resolution_plan"`
	PlanDigest           string            `json:"resolution_digest"`
	Created              string            `json:"created"`
	Generator            string            `json:"generator"`
	Tools                []string          `json:"tools"`
	Request              ResolveRequest    `json:"request"`
	Targets              []GeneratedTarget `json:"targets"`
	Blocked              bool              `json:"blocked"`
	Digest               string            `json:"digest"`
	Boundary             string            `json:"boundary"`
	Repair               bool              `json:"repair_only,omitempty"`
	NoChanges            bool              `json:"no_changes,omitempty"`
	ExternalStage        string            `json:"external_stage_id,omitempty"`
	ExternalStageDigest  string            `json:"external_stage_digest,omitempty"`
	ExternalValidation   string            `json:"external_validation_id,omitempty"`
	ExternalOutputDigest string            `json:"external_output_digest,omitempty"`
}

type GeneratedTarget struct {
	ExcludedChecks      []inventory.Finding  `json:"excluded_checks,omitempty"`
	Copy                string               `json:"copy_id"`
	SourceBefore        string               `json:"source_before"`
	SourceAfter         string               `json:"source_after"`
	Changes             []workstore.Change   `json:"source_changes"`
	Outputs             []native.Target      `json:"outputs"`
	Preserved           *native.Preservation `json:"preserved,omitempty"`
	ConfigurationDigest string               `json:"configuration_digest,omitempty"`
}

func preservation(t ProposedTarget) *native.Preservation {
	ids, names := map[string]bool{}, map[string]bool{}
	keep := &native.Preservation{}
	for _, path := range t.Copy.Excludes {
		parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
		if parts[0] != "skills" {
			continue
		}
		if len(parts) == 1 {
			keep.All = true
			continue
		}
		if uuid.MatchString(parts[1]) {
			ids[parts[1]] = true
		} else {
			names[parts[1]] = true
		}
	}
	for _, skill := range t.Preflight.Skills {
		if excluded(t.Copy, &source.SharedEntry{ID: skill.ID, Name: skill.Name, Path: "skills/" + skill.ID}) {
			ids[skill.ID], names[skill.Name] = true, true
		}
	}
	for id := range ids {
		keep.IDs = append(keep.IDs, id)
	}
	for name := range names {
		keep.Names = append(keep.Names, name)
	}
	sort.Strings(keep.IDs)
	sort.Strings(keep.Names)
	if !keep.All && len(ids) == 0 && len(names) == 0 {
		return nil
	}
	return keep
}

func generationNoChanges(g Generation, p Resolution) bool {
	if workstore.Digest(p.ControlBefore) != workstore.Digest(p.ControlAfter) {
		return false
	}
	for _, t := range g.Targets {
		if len(t.Changes) != 0 {
			return false
		}
		for _, output := range t.Outputs {
			if len(output.Changes) != 0 || output.Rule != nil && len(output.Rule.Changes) != 0 {
				return false
			}
		}
	}
	return true
}

func generationDigest(g Generation) string { g.Digest = ""; return workstore.Hash(data(g)) }
func (m *Manager) generationFile(id string) string {
	return filepath.Join(m.resolutionDir(), "generation", id+".json")
}
func mirrorDirectory(tool string) string {
	switch tool {
	case "claude-code":
		return ".claude/skills"
	case "codex-cli":
		return ".agents/skills"
	}
	return ""
}

func validateGeneration(g Generation, p Resolution) error {
	bad := func() error {
		return result.Fail("GENERATOR_MANIFEST_INVALID", "生成清单身份、输入、输出绑定或摘要不一致", 5)
	}
	if (g.Version < 1 || g.Version > 5) || g.Kind != "replica-generation" || !uuid.MatchString(g.ID) || g.Plan != p.ID || g.PlanDigest != p.Digest || g.Generator != mirrorGenerator && g.Generator != externalMirrorGenerator || g.Digest != generationDigest(g) || len(g.Targets) != len(p.Targets) || len(g.Tools) < 1 || len(g.Tools) > 2 {
		return bad()
	}
	if g.Version == 5 && (g.ApplicationScope != native.SkillsRulesScope || g.Generator != mirrorGenerator) || g.Version < 5 && g.ApplicationScope != "" {
		return bad()
	}
	if g.Generator == externalMirrorGenerator {
		if g.Version != 3 || !uuid.MatchString(g.ExternalStage) || g.ExternalStageDigest == "" || !uuid.MatchString(g.ExternalValidation) || g.ExternalOutputDigest == "" {
			return bad()
		}
	} else if g.ExternalStage != "" || g.ExternalStageDigest != "" || g.ExternalValidation != "" || g.ExternalOutputDigest != "" || g.Version == 3 {
		return bad()
	}
	if g.Version >= 2 && (g.Repair != p.Repair || g.NoChanges != generationNoChanges(g, p)) || g.Version == 1 && (g.Repair || g.NoChanges || p.Repair) {
		return bad()
	}
	tools := map[string]bool{}
	if string(data(g.Request)) != string(data(p.Request)) {
		return bad()
	}
	for _, tool := range g.Tools {
		if mirrorDirectory(tool) == "" || tools[tool] {
			return bad()
		}
		tools[tool] = true
	}
	blocked := false
	for i, target := range g.Targets {
		candidate := p.Targets[i]
		if g.ApplicationScope == "" && len(target.ExcludedChecks) != 0 {
			return bad()
		}
		for _, finding := range target.ExcludedChecks {
			if finding.Component != "mcp" {
				return bad()
			}
		}
		if g.Version >= 2 && string(data(target.Preserved)) != string(data(preservation(candidate))) || g.Version == 1 && target.Preserved != nil {
			return bad()
		}
		if target.Copy != candidate.Copy.ID || target.SourceBefore != workstore.Digest(candidate.Before) || target.SourceAfter != workstore.Digest(candidate.After) || len(target.Outputs) != len(tools) || string(data(target.Changes)) != string(data(candidate.Changes)) {
			return bad()
		}
		seen := map[string]bool{}
		for _, output := range target.Outputs {
			a := output.Adapter
			if output.Rule != nil {
				if g.Version < 4 || native.ValidateDraftRule(output) != nil {
					return bad()
				}
			}
			if !tools[a.ID] || seen[a.ID] || a.Directory != mirrorDirectory(a.ID) || output.Root != filepath.Join(filepath.Dir(candidate.Copy.Root), filepath.FromSlash(a.Directory)) || string(data(output.Changes)) != string(data(workstore.Changes(output.Before, output.After))) {
				return bad()
			}
			seen[a.ID] = true
			if output.Status == "FAIL" {
				blocked = true
			}
		}
	}
	if blocked != g.Blocked {
		return bad()
	}
	return nil
}

// StageGeneration reuses the native compiler on immutable candidates. Only
// Local blobs/manifests are saved; source and native roots are observed twice.
func (m *Manager) StageGeneration(ctx context.Context, req GenerationRequest) (Generation, error) {
	scope, err := native.NormalizeApplicationScope(req.ApplicationScope)
	if err != nil {
		return Generation{}, err
	}
	p, err := m.loadResolution(req.Plan, req.Digest)
	if err != nil {
		return Generation{}, err
	}
	if err = m.revalidate(ctx, p); err != nil {
		return Generation{}, err
	}
	g := Generation{Version: 4, Kind: "replica-generation", ID: workstore.ID(), Plan: p.ID, PlanDigest: p.Digest, Created: time.Now().UTC().Format(time.RFC3339Nano), Generator: mirrorGenerator, Tools: append([]string{}, req.Tools...), Request: p.Request, Targets: []GeneratedTarget{}, Repair: p.Repair,
		Boundary: "仅暂存内置 owned 完整包与规则托管区块的生成候选和恢复清单。每个目标从自己的候选及有效配置重新生成，保留手写项；源、工具目录、规则文件与真实基线不写。不运行第三方生成器、工具或技能脚本，不证明工具会话生效。仅技能与规则的候选通过当前检查后可另行预览正式同步；完整 Profile 正式发布尚未开放。"}
	if p.Repair {
		g.Boundary = "纯生成物修复：按每个目标自己已保存的技能源编译；不采用发布者版本、不处理源冲突、不改变源或共同基线。本机排除项与手写项保留。确认只在独立项目预演，可回退与撤销；不运行第三方脚本，不证明会话生效；仅技能与规则的候选通过当前检查后可另行预览正式同步，完整 Profile 正式发布尚未开放。"
	}
	if scope == native.SkillsRulesScope {
		g.Version, g.ApplicationScope = 5, scope
		g.Boundary += " 工具生成范围：仅技能与规则；MCP 原生连接文件不写，范围外诊断逐副本保留，不代表已修复或生效。共享源仍按逐项决策处理，本机 MCP 授权不传播。"
	}
	if resolutionChangesMCP(p) {
		g.Boundary += " 本候选修改 MCP 定义或精确锁，目前只能独立预演；正式发布需先完成完整 MCP 联合应用和全批恢复，不能按仅技能与规则范围发布。"
	}
	// Validate the allowlist before observing any tool output directory.
	seen := map[string]bool{}
	if len(req.Tools) < 1 || len(req.Tools) > 2 {
		return g, result.Fail("TOOL_REQUIRED", "明确选择一到两个目标工具", 2)
	}
	for _, tool := range req.Tools {
		if mirrorDirectory(tool) == "" || seen[tool] {
			return g, result.Fail("TOOL_UNSUPPORTED", "生成工具未知或重复", 2)
		}
		seen[tool] = true
	}
	for _, target := range p.Targets {
		if err = ctx.Err(); err != nil {
			return g, err
		}
		s, err := m.copySource(target.Copy)
		if err != nil {
			return g, err
		}
		bundle, err := s.ExportCandidate(target.After)
		if err != nil {
			return g, err
		}
		n, err := native.New(s, filepath.Dir(target.Copy.Root))
		if err != nil {
			return g, err
		}
		observations := map[string]workstore.Tree{}
		for _, tool := range req.Tools {
			root := filepath.Join(n.Root, filepath.FromSlash(mirrorDirectory(tool)))
			if err = workstore.CoordinatedPending(root); err != nil {
				return g, err
			}
			before, err := s.Store.Capture(root)
			if err != nil {
				return g, err
			}
			observations[tool] = before
		}
		keep := preservation(target)
		outputs, excluded, err := n.DraftConfigurationTargetsScoped(ctx, req.Tools, bundle, observations, keep, scope)
		if err != nil {
			return g, err
		}
		for _, output := range outputs {
			if output.Status == "FAIL" {
				g.Blocked = true
			}
			if err = transferOutput(s.Store, m.Control, output); err != nil {
				return g, err
			}
		}
		g.Targets = append(g.Targets, GeneratedTarget{Copy: target.Copy.ID, SourceBefore: workstore.Digest(target.Before), SourceAfter: workstore.Digest(target.After), Changes: target.Changes, Outputs: outputs, Preserved: keep, ConfigurationDigest: bundle.ConfigurationDigest, ExcludedChecks: excluded})
	}
	g.NoChanges = generationNoChanges(g, p)
	g.Digest = generationDigest(g)
	if err = validateGeneration(g, p); err != nil {
		return g, err
	}
	if err = m.revalidateGeneration(ctx, g, p); err != nil {
		return g, err
	}
	return g, saveLocal(m.generationFile(g.ID), g)
}

func externalPackageKey(p source.ExportPackage) string {
	return p.Skill.ID + "\x00" + p.Skill.Name + "\x00" + p.Skill.Revision
}

func sameExternalPackages(a, b []source.ExportPackage) bool {
	if len(a) != len(b) {
		return false
	}
	left, right := make([]string, 0, len(a)), make([]string, 0, len(b))
	for _, p := range a {
		left = append(left, externalPackageKey(p))
	}
	for _, p := range b {
		right = append(right, externalPackageKey(p))
	}
	sort.Strings(left)
	sort.Strings(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func externalSubtree(t workstore.Tree, prefix string) (workstore.Tree, bool) {
	out := workstore.Tree{Entries: map[string]workstore.Entry{}}
	root, ok := t.Entries[prefix]
	if !ok || !root.Dir {
		return out, false
	}
	out.Exists = true
	for path, entry := range t.Entries {
		if strings.HasPrefix(path, prefix+"/") {
			out.Entries[strings.TrimPrefix(path, prefix+"/")] = entry
		}
	}
	return out, true
}

// externalBundle replaces each candidate package tree with the exact tree
// returned by the validated exchange. The candidate's identities and
// preflight remain authoritative, so a return package cannot add or remove a
// skill, change its revision, or bypass local exclusion handling.
func externalBundle(candidate source.Export, returned source.Export, output workstore.Tree, tool string) (source.Export, error) {
	if !sameExternalPackages(candidate.Packages, returned.Packages) {
		return source.Export{}, result.Fail("GENERATOR_PACKAGE_MISMATCH", "外部返回包与目标候选的技能身份或修订不一致；需重新生成", 4)
	}
	byKey := map[string]source.ExportPackage{}
	for _, p := range returned.Packages {
		byKey[externalPackageKey(p)] = p
	}
	bundle := candidate
	bundle.Packages = make([]source.ExportPackage, len(candidate.Packages))
	for i, p := range candidate.Packages {
		returnedPackage, ok := byKey[externalPackageKey(p)]
		if !ok {
			return source.Export{}, result.Fail("GENERATOR_PACKAGE_MISMATCH", "外部返回包缺少目标候选技能", 4)
		}
		tree, ok := externalSubtree(output, mirrorDirectory(tool)+"/"+returnedPackage.Skill.Name)
		if !ok {
			return source.Export{}, result.Fail("GENERATOR_OUTPUT_INVALID", "外部返回包缺少完整工具技能目录："+returnedPackage.Skill.Name, 5)
		}
		if workstore.Digest(tree) != workstore.Digest(p.Tree) {
			return source.Export{}, result.Fail("GENERATOR_PACKAGE_MISMATCH", "外部完整包字节或权限与目标候选不一致："+p.Skill.Name, 4)
		}
		p.Tree = tree
		bundle.Packages[i] = p
	}
	return bundle, nil
}

func (m *Manager) draftExternalOutputs(ctx context.Context, n *native.Manager, bundle source.Export, returned generator.ValidatedOutput, tools []string, observations map[string]workstore.Tree, keep *native.Preservation) ([]native.Target, error) {
	outputs := []native.Target{}
	for _, tool := range tools {
		external, err := externalBundle(bundle, returned.Stage.Preview.Source, returned.Output, tool)
		if err != nil {
			return nil, err
		}
		for _, pkg := range external.Packages {
			if err = transfer(m.Source.Store, n.Source.Store, pkg.Tree); err != nil {
				return nil, err
			}
		}
		one, err := n.DraftTargetsPreserving(ctx, []string{tool}, external, map[string]workstore.Tree{tool: observations[tool]}, keep)
		if err != nil {
			return nil, err
		}
		if len(one) != 1 {
			return nil, result.Fail("GENERATOR_MANIFEST_INVALID", "外部返回包未生成唯一工具目标", 5)
		}
		outputs = append(outputs, one[0])
	}
	return outputs, nil
}

func (m *Manager) checkExternalCompiler(ctx context.Context, g Generation, p Resolution, returned generator.ValidatedOutput) error {
	if string(data(g.Tools)) != string(data(returned.Stage.Preview.Contract.Tools)) {
		return result.Fail("GENERATOR_MANIFEST_INVALID", "生成工具集合与外部暂存合同不一致", 5)
	}
	for i, target := range g.Targets {
		n, err := m.candidateNative(p.Targets[i].Copy)
		if err != nil {
			return err
		}
		bundle, err := n.Source.ExportCandidate(p.Targets[i].After)
		if err != nil {
			return err
		}
		if target.ConfigurationDigest != bundle.ConfigurationDigest {
			return result.Fail("PLAN_STALE", "目标副本的本机覆盖或全局配置已变化", 3)
		}
		observations := map[string]workstore.Tree{}
		for _, output := range target.Outputs {
			observations[output.Adapter.ID] = output.Before
		}
		outputs, err := m.draftExternalOutputs(ctx, n, bundle, returned, g.Tools, observations, preservation(p.Targets[i]))
		if err != nil {
			return err
		}
		if string(data(outputs)) != string(data(target.Outputs)) {
			return result.Fail("GENERATOR_MANIFEST_INVALID", "外部返回包的完整目标与归属清单不一致；重新计算摘要不能采纳被改写候选", 5)
		}
	}
	return nil
}

// StageExternalGeneration maps one validated offline generator output to all
// selected replica candidates. It deliberately requires every target to have
// the same complete owned package set as the generator input; target-only or
// excluded packages remain a diagnostic instead of being silently deleted.
func (m *Manager) StageExternalGeneration(ctx context.Context, req ExternalGenerationRequest) (Generation, error) {
	p, err := m.loadResolution(req.Plan, req.Digest)
	if err != nil {
		return Generation{}, err
	}
	if err = m.revalidate(ctx, p); err != nil {
		return Generation{}, err
	}
	gm := generator.New(m.Source)
	returned, err := gm.LoadValidated(ctx, req.Stage, req.StageDigest)
	if err != nil {
		return Generation{}, err
	}
	sourceBundle := returned.Stage.Preview.Source
	if sourceBundle.Root != p.Source.Root || sourceBundle.Owner != m.Source.Owner || sourceBundle.Scope != m.Source.Scope {
		return Generation{}, result.Fail("GENERATOR_SOURCE_MISMATCH", "外部暂存必须来自本次副本预演选择的源绑定", 4)
	}
	g := Generation{Version: 3, Kind: "replica-generation", ID: workstore.ID(), Plan: p.ID, PlanDigest: p.Digest, Created: time.Now().UTC().Format(time.RFC3339Nano), Generator: externalMirrorGenerator, Tools: append([]string{}, returned.Stage.Preview.Contract.Tools...), Request: p.Request, Targets: []GeneratedTarget{}, Repair: p.Repair,
		ExternalStage: req.Stage, ExternalStageDigest: returned.Stage.Digest, ExternalValidation: returned.Validation.ID, ExternalOutputDigest: workstore.Digest(returned.Output),
		Boundary: "仅把当前 owned 源绑定上已通过离线完整性校验的外部返回包，逐目标建立独立副本预演候选。要求目标完整技能集合与返回输入一致；保留本机排除与手写项。未启动外部程序，不修改登记项目或真实工具目录；外部返回包的正式发布尚未开放。"}
	seen := map[string]bool{}
	for _, tool := range g.Tools {
		if mirrorDirectory(tool) == "" || seen[tool] {
			return g, result.Fail("TOOL_UNSUPPORTED", "外部暂存包含未知或重复工具", 2)
		}
		seen[tool] = true
	}
	for _, target := range p.Targets {
		if err = ctx.Err(); err != nil {
			return g, err
		}
		s, err := m.copySource(target.Copy)
		if err != nil {
			return g, err
		}
		bundle, err := s.ExportCandidate(target.After)
		if err != nil {
			return g, err
		}
		if !sameExternalPackages(bundle.Packages, sourceBundle.Packages) {
			return g, result.Fail("GENERATOR_PACKAGE_MISMATCH", target.Copy.Name+" 的候选技能集合与外部暂存输入不一致；保留诊断，不生成候选", 4)
		}
		n, err := native.New(s, filepath.Dir(target.Copy.Root))
		if err != nil {
			return g, err
		}
		observations := map[string]workstore.Tree{}
		for _, tool := range g.Tools {
			root := filepath.Join(n.Root, filepath.FromSlash(mirrorDirectory(tool)))
			if err = workstore.CoordinatedPending(root); err != nil {
				return g, err
			}
			before, captureErr := s.Store.Capture(root)
			if captureErr != nil {
				return g, captureErr
			}
			observations[tool] = before
		}
		outputs, err := m.draftExternalOutputs(ctx, n, bundle, returned, g.Tools, observations, preservation(target))
		if err != nil {
			return g, err
		}
		for _, output := range outputs {
			if output.Status == "FAIL" {
				g.Blocked = true
			}
			if err = transfer(s.Store, m.Control, output.Before, output.After); err != nil {
				return g, err
			}
		}
		g.Targets = append(g.Targets, GeneratedTarget{Copy: target.Copy.ID, SourceBefore: workstore.Digest(target.Before), SourceAfter: workstore.Digest(target.After), Changes: target.Changes, Outputs: outputs, Preserved: preservation(target), ConfigurationDigest: bundle.ConfigurationDigest})
	}
	g.NoChanges = generationNoChanges(g, p)
	g.Digest = generationDigest(g)
	if err = validateGeneration(g, p); err != nil {
		return g, err
	}
	if err = m.revalidateGeneration(ctx, g, p); err != nil {
		return g, err
	}
	return g, saveLocal(m.generationFile(g.ID), g)
}

func (m *Manager) loadGeneration(id, digest string) (Generation, Resolution, error) {
	var g Generation
	if !uuid.MatchString(id) {
		return g, Resolution{}, result.Fail("INVALID_INPUT", "需要生成清单 UUID", 2)
	}
	b, err := workstore.ReadFile(m.generationFile(id))
	if err != nil {
		return g, Resolution{}, err
	}
	if err = decode(b, &g); err != nil {
		return g, Resolution{}, err
	}
	p, err := m.loadResolution(g.Plan, g.PlanDigest)
	if err != nil {
		return g, p, err
	}
	if g.ID != id {
		return g, p, result.Fail("GENERATOR_MANIFEST_INVALID", "清单 UUID 不一致", 5)
	}
	if err = validateGeneration(g, p); err != nil {
		return g, p, err
	}
	if digest == "" || digest != g.Digest {
		return g, p, result.Fail("PLAN_CONFIRMATION_REQUIRED", "需要生成清单的完整摘要", 4)
	}
	return g, p, nil
}

func (m *Manager) GenerationShow(id, digest string) (Generation, error) {
	g, _, err := m.loadGeneration(id, digest)
	return g, err
}

func (m *Manager) revalidateGeneration(ctx context.Context, g Generation, p Resolution) error {
	if err := m.revalidate(ctx, p); err != nil {
		return err
	}
	if g.Generator == externalMirrorGenerator {
		returned, err := generator.New(m.Source).LoadValidated(ctx, g.ExternalStage, g.ExternalStageDigest)
		if err != nil {
			return err
		}
		if returned.Validation.ID != g.ExternalValidation || workstore.Digest(returned.Output) != g.ExternalOutputDigest || returned.Stage.Preview.Source.Root != p.Source.Root || returned.Stage.Preview.Source.Owner != m.Source.Owner || returned.Stage.Preview.Source.Scope != m.Source.Scope {
			return result.Fail("PLAN_STALE", "外部返回包、校验记录或源绑定已变化，请重新暂存并生成", 3)
		}
		if err = m.checkExternalCompiler(ctx, g, p, returned); err != nil {
			return err
		}
	}
	if g.Version == 1 {
		for _, target := range p.Targets {
			if preservation(target) != nil {
				return result.Fail("PLAN_STALE", "旧生成清单未包含本机排除保护，请重新暂存", 3)
			}
		}
	}
	for i, target := range g.Targets {
		s, err := m.copySource(p.Targets[i].Copy)
		if err != nil {
			return err
		}
		configuration, err := s.ConfigurationDigest()
		if err != nil {
			return err
		}
		if target.ConfigurationDigest != configuration {
			return result.Fail("PLAN_STALE", "目标副本的本机覆盖或全局配置已变化，请重新生成清单", 3)
		}
		for _, output := range target.Outputs {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := workstore.CoordinatedPending(output.Root); err != nil {
				return err
			}
			current, err := m.Control.Capture(output.Root)
			if err != nil {
				return err
			}
			if workstore.Digest(current) != workstore.Digest(output.Before) {
				return result.Fail("PLAN_STALE", "生成目录在暂存或确认后变化，请重新生成清单: "+output.Root, 3)
			}
			if err = m.revalidateRule(p.Targets[i].Copy, output); err != nil {
				return err
			}
			matched := false
			for _, a := range native.Adapters() {
				if a.ID == output.Adapter.ID && string(data(a)) == string(data(output.Adapter)) {
					matched = true
				}
			}
			if !matched {
				return result.Fail("PLAN_STALE", "生成适配器契约已变化，请重新暂存", 3)
			}
		}
	}
	if g.Version >= 4 {
		return m.checkGenerationCompiler(ctx, g, p)
	}
	return nil
}

func (m *Manager) RehearseGenerated(ctx context.Context, id, digest string) (Rehearsal, error) {
	g, p, err := m.loadGeneration(id, digest)
	if err != nil {
		return Rehearsal{}, err
	}
	if g.Blocked {
		return Rehearsal{}, result.Fail("GENERATOR_PREFLIGHT_FAILED", "生成目录有占用、人工漂移或静态阻断；不开始源/生成物/基线事务", 4)
	}
	if err = m.revalidateGeneration(ctx, g, p); err != nil {
		return Rehearsal{}, err
	}
	if g.NoChanges {
		return Rehearsal{}, result.Fail("GENERATOR_ALREADY_CURRENT", "所选生成目录已一致或全部排除；不建立空预演事务", 4)
	}
	return m.rehearse(ctx, p, &g)
}

func generatedPath(copy, tool string) string { return "copies/" + copy + "/generated/" + tool }

func generatedRulePath(copy, tool string) string { return "copies/" + copy + "/rules/" + tool }
