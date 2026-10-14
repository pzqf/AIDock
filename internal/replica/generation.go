package replica

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

const mirrorGenerator = "aidock-owned-mirror-v1"

type GenerationRequest struct {
	Plan   string   `json:"plan_id"`
	Digest string   `json:"digest"`
	Tools  []string `json:"tools"`
}

type GenerationRepairRequest struct {
	Request
	Comparison string   `json:"comparison_digest"`
	Tools      []string `json:"tools"`
}

// Generation binds complete source inputs, output observations and generated
// trees in Local. A descriptor never grants permission to launch a command.
type Generation struct {
	Version    int               `json:"schema_version"`
	Kind       string            `json:"kind"`
	ID         string            `json:"id"`
	Plan       string            `json:"resolution_plan"`
	PlanDigest string            `json:"resolution_digest"`
	Created    string            `json:"created"`
	Generator  string            `json:"generator"`
	Tools      []string          `json:"tools"`
	Request    ResolveRequest    `json:"request"`
	Targets    []GeneratedTarget `json:"targets"`
	Blocked    bool              `json:"blocked"`
	Digest     string            `json:"digest"`
	Boundary   string            `json:"boundary"`
	Repair     bool              `json:"repair_only,omitempty"`
	NoChanges  bool              `json:"no_changes,omitempty"`
}

type GeneratedTarget struct {
	Copy         string               `json:"copy_id"`
	SourceBefore string               `json:"source_before"`
	SourceAfter  string               `json:"source_after"`
	Changes      []workstore.Change   `json:"source_changes"`
	Outputs      []native.Target      `json:"outputs"`
	Preserved    *native.Preservation `json:"preserved,omitempty"`
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
			if len(output.Changes) != 0 {
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
	if (g.Version != 1 && g.Version != 2) || g.Kind != "replica-generation" || !uuid.MatchString(g.ID) || g.Plan != p.ID || g.PlanDigest != p.Digest || g.Generator != mirrorGenerator || g.Digest != generationDigest(g) || len(g.Targets) != len(p.Targets) || len(g.Tools) < 1 || len(g.Tools) > 2 {
		return bad()
	}
	if g.Version == 2 && (g.Repair != p.Repair || g.NoChanges != generationNoChanges(g, p)) || g.Version == 1 && (g.Repair || g.NoChanges || p.Repair) {
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
		if g.Version == 2 && string(data(target.Preserved)) != string(data(preservation(candidate))) || g.Version == 1 && target.Preserved != nil {
			return bad()
		}
		if target.Copy != candidate.Copy.ID || target.SourceBefore != workstore.Digest(candidate.Before) || target.SourceAfter != workstore.Digest(candidate.After) || len(target.Outputs) != len(tools) || string(data(target.Changes)) != string(data(candidate.Changes)) {
			return bad()
		}
		seen := map[string]bool{}
		for _, output := range target.Outputs {
			a := output.Adapter
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
	p, err := m.loadResolution(req.Plan, req.Digest)
	if err != nil {
		return Generation{}, err
	}
	if err = m.revalidate(ctx, p); err != nil {
		return Generation{}, err
	}
	g := Generation{Version: 2, Kind: "replica-generation", ID: workstore.ID(), Plan: p.ID, PlanDigest: p.Digest, Created: time.Now().UTC().Format(time.RFC3339Nano), Generator: mirrorGenerator, Tools: append([]string{}, req.Tools...), Request: p.Request, Targets: []GeneratedTarget{}, Repair: p.Repair,
		Boundary: "仅暂存内置 owned 完整包编译器的生成候选与恢复清单。每个目标从各自候选重新生成，保留手写项；源、工具目录与真实基线不写。不运行第三方生成器、工具或技能脚本，不证明工具会话生效。正式跨项目发布仍关闭。"}
	if p.Repair {
		g.Boundary = "纯生成物修复：按每个目标自己已保存的技能源编译；不采用发布者版本、不处理源冲突、不改变源或共同基线。本机排除项与手写项保留。确认只在独立项目预演，可回退与撤销；不运行第三方脚本，不证明会话生效，正式发布仍关闭。"
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
		s, err := source.New(target.Copy.Root, m.Source.Store.Local, m.Source.Owner, m.Source.Scope)
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
		outputs, err := n.DraftTargetsPreserving(ctx, req.Tools, bundle, observations, keep)
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
		g.Targets = append(g.Targets, GeneratedTarget{Copy: target.Copy.ID, SourceBefore: workstore.Digest(target.Before), SourceAfter: workstore.Digest(target.After), Changes: target.Changes, Outputs: outputs, Preserved: keep})
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
	if g.Version == 1 {
		for _, target := range p.Targets {
			if preservation(target) != nil {
				return result.Fail("PLAN_STALE", "旧生成清单未包含本机排除保护，请重新暂存", 3)
			}
		}
	}
	for _, target := range g.Targets {
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
