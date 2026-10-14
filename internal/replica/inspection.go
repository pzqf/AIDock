package replica

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/native"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

const inspectionPolicy = "owned-candidate-inspection-v1"

type GenerationCheckRequest struct {
	Plan   string `json:"plan_id"`
	Digest string `json:"digest"`
	Probe  bool   `json:"probe"`
}

type PublicationGate struct {
	Code     string `json:"code"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
	Required bool   `json:"required"`
	Copy     string `json:"copy_id,omitempty"`
	Tool     string `json:"tool,omitempty"`
}

type CandidateCheck struct {
	Copy       Binding              `json:"copy"`
	Source     string               `json:"candidate_source"`
	Inspection native.CheckReport   `json:"inspection"`
	Preserved  *native.Preservation `json:"preserved,omitempty"`
}

// GenerationCheck is diagnostic evidence, never a native qualification or a
// publication plan. It binds each copy's candidate, tools and observed outputs.
type GenerationCheck struct {
	Version           int               `json:"schema_version"`
	Kind              string            `json:"kind"`
	ID                string            `json:"id"`
	Owner             string            `json:"owner"`
	Scope             string            `json:"scope_id"`
	Created           string            `json:"created"`
	Expires           string            `json:"expires"`
	Generation        string            `json:"generation_id"`
	GenerationDigest  string            `json:"generation_digest"`
	Probe             bool              `json:"probe"`
	CandidateStatus   string            `json:"candidate_status"`
	PublicationStatus string            `json:"publication_status"`
	WriteSupported    bool              `json:"write_supported"`
	NoChanges         bool              `json:"no_changes"`
	Repair            bool              `json:"repair_only"`
	Targets           []CandidateCheck  `json:"targets"`
	Gates             []PublicationGate `json:"gates"`
	Digest            string            `json:"digest"`
	Stale             bool              `json:"stale"`
	StaleReason       string            `json:"stale_reason,omitempty"`
	Boundary          string            `json:"boundary"`
	Policy            string            `json:"inspection_policy,omitempty"`
}

func generationCheckDigest(r GenerationCheck) string {
	r.Digest, r.Stale, r.StaleReason = "", false, ""
	return workstore.Hash(data(r))
}
func (m *Manager) generationChecksDir() string {
	return filepath.Join(m.resolutionDir(), "checks")
}

func (m *Manager) candidateNative(copy Binding) (*native.Manager, error) {
	s, err := source.New(copy.Root, m.Source.Store.Local, m.Source.Owner, m.Source.Scope)
	if err != nil {
		return nil, err
	}
	return native.New(s, filepath.Dir(copy.Root))
}

// checkGenerationCompiler recomputes all outputs from the immutable source
// candidates. A recomputed descriptor checksum cannot bless altered packages,
// paths, frontmatter or exclusion policy as executable discovery inputs.
func (m *Manager) checkGenerationCompiler(ctx context.Context, g Generation, p Resolution) error {
	for i, target := range g.Targets {
		n, err := m.candidateNative(p.Targets[i].Copy)
		if err != nil {
			return err
		}
		bundle, err := n.Source.ExportCandidate(p.Targets[i].After)
		if err != nil {
			return err
		}
		observations := map[string]workstore.Tree{}
		for _, output := range target.Outputs {
			observations[output.Adapter.ID] = output.Before
		}
		outputs, err := n.DraftTargetsPreserving(ctx, g.Tools, bundle, observations, preservation(p.Targets[i]))
		if err != nil {
			return err
		}
		if string(data(outputs)) != string(data(target.Outputs)) {
			return result.Fail("GENERATOR_MANIFEST_INVALID", "暂存生成结果与当前完整包编译器不一致；重新预览", 5)
		}
	}
	return nil
}

func (m *Manager) CheckGeneration(ctx context.Context, req GenerationCheckRequest) (GenerationCheck, error) {
	g, p, err := m.loadGeneration(req.Plan, req.Digest)
	if err != nil {
		return GenerationCheck{}, err
	}
	if err = m.revalidateGeneration(ctx, g, p); err != nil {
		return GenerationCheck{}, err
	}
	if err = m.checkGenerationCompiler(ctx, g, p); err != nil {
		return GenerationCheck{}, err
	}
	now := time.Now().UTC()
	r := GenerationCheck{Version: 1, Kind: "replica-generation-check", ID: workstore.ID(), Owner: m.Source.Owner, Scope: m.Source.Scope, Created: now.Format(time.RFC3339Nano), Expires: now.Add(24 * time.Hour).Format(time.RFC3339Nano), Generation: g.ID, GenerationDigest: g.Digest, Probe: req.Probe, CandidateStatus: "PASS", PublicationStatus: "BLOCKED", NoChanges: g.NoChanges, Repair: g.Repair, Targets: []CandidateCheck{}, Gates: []PublicationGate{},
		Policy: inspectionPolicy, Boundary: "发布预检只保存 Local 检查记录。显式探测逐副本使用其暂存候选在独立临时项目验证发现，不执行技能或外部生成器；不修改源、原生目标、共同基线或页面草稿。L2 通过不等于 L3/L4 会话生效。外部生成器与正式发布总验收未完成，检查记录不能授权同步或安装。"}
	gate := func(code, status, detail, copy, tool string, required bool) {
		r.Gates = append(r.Gates, PublicationGate{code, status, detail, required, copy, tool})
		if required && copy != "" {
			if status == "FAIL" {
				r.CandidateStatus = "FAIL"
			} else if status != "PASS" && r.CandidateStatus != "FAIL" {
				r.CandidateStatus = "UNKNOWN"
			}
		}
	}
	for i, target := range g.Targets {
		if err = ctx.Err(); err != nil {
			return r, err
		}
		copy := p.Targets[i].Copy
		n, err := m.candidateNative(copy)
		if err != nil {
			return r, err
		}
		for _, output := range target.Outputs {
			for _, pkg := range output.Packages {
				if err = transfer(m.Control, n.Source.Store, pkg.Tree); err != nil {
					return r, err
				}
			}
		}
		// One blocked output stops the whole explicit probe batch. Static
		// diagnoses remain available for all copies, with a concrete reason.
		inspect := m.inspectDraft
		if inspect == nil {
			inspect = func(ctx context.Context, n *native.Manager, targets []native.Target, snapshot string, probe bool) (native.CheckReport, error) {
				return n.InspectDraft(ctx, targets, snapshot, probe)
			}
		}
		check, err := inspect(ctx, n, target.Outputs, target.SourceAfter, req.Probe && !g.Blocked)
		if err != nil {
			return r, err
		}
		r.Targets = append(r.Targets, CandidateCheck{copy, target.SourceAfter, check, target.Preserved})
		for j, output := range target.Outputs {
			tool := output.Adapter.ID
			status, detail := "PASS", "完整候选生成与归属检查通过；手写项与本机排除项保留"
			if output.Status == "FAIL" {
				status, detail = "FAIL", "生成检查存在占用或漂移阻断；先解决后重新预览"
			}
			gate("GENERATED_CANDIDATE", status, detail, copy.ID, tool, true)
			codes := native.DraftSemantics(output)
			status, detail = "PASS", "完整包条件在当前原生文件安装语义范围内"
			if len(codes) != 0 {
				status, detail = "FAIL", "原生文件安装语义尚不支持："+strings.Join(codes, "、")
			}
			gate("NATIVE_SEMANTICS", status, detail, copy.ID, tool, true)
			t := check.Tools[j]
			status, detail = "UNKNOWN", "尚未显式验证此候选的精确版本与隔离发现"
			if req.Probe && g.Blocked {
				detail = "整批生成检查有阻断项，本次未启动工具"
			} else if !t.Installed {
				detail = "没有可直接运行的工具程序"
			} else if t.L2 == "PASS" || t.L2 == "SKIPPED" && len(output.Packages) == 0 && t.Version != "" {
				verified := false
				for _, v := range output.Adapter.Verified {
					verified = verified || t.Version == v
				}
				if verified {
					status, detail = "PASS", "精确版本 "+t.Version+" 的候选完整包隔离发现通过"
					if len(output.Packages) == 0 {
						detail = "精确版本已核对；无待生成启用包，未运行发现初始化；保留项不在检查范围"
					}
				} else {
					detail = "此精确版本尚未通过原生文件安装验收"
				}
			} else if t.L2 == "FAIL" {
				status, detail = "FAIL", "工具未发现唯一且元数据一致的候选技能"
			}
			gate("CANDIDATE_DISCOVERY", status, detail, copy.ID, tool, true)
			gate("SESSION_LOADING", "UNKNOWN", "实际项目新会话正文加载与能力使用未验证", copy.ID, tool, false)
		}
	}
	gate("EXTERNAL_GENERATOR_ACCEPTANCE", "UNAVAILABLE", "外部生成器暂存/恢复清单适配与故障验收尚未完成", "", "", true)
	gate("PUBLICATION_ACCEPTANCE", "UNAVAILABLE", "正式副本发布与整批恢复总验收尚未完成；当前入口不写登记项目", "", "", true)
	if g.NoChanges {
		gate("NO_CHANGES", "SKIPPED", "所选源、生成物和基线无需变化，不建立空发布事务", "", "", false)
	}
	// Recheck all observations and every executable after the entire batch.
	// A slow later copy cannot make an earlier copy's result silently current.
	if err = m.revalidateGeneration(ctx, g, p); err != nil {
		return r, err
	}
	for _, target := range r.Targets {
		n, err := m.candidateNative(target.Copy)
		if err != nil {
			return r, err
		}
		if err = n.InspectionFresh(target.Inspection); err != nil {
			return r, err
		}
	}
	r.Digest = generationCheckDigest(r)
	return r, saveLocal(filepath.Join(m.generationChecksDir(), r.ID+".json"), r)
}

func (m *Manager) loadGenerationCheck(id string) (GenerationCheck, error) {
	var r GenerationCheck
	if !uuid.MatchString(id) {
		return r, result.Fail("INVALID_INPUT", "需要当前项目的候选检查 UUID", 2)
	}
	b, err := workstore.ReadFile(filepath.Join(m.generationChecksDir(), id+".json"))
	if os.IsNotExist(err) {
		return r, result.Fail("CHECK_NOT_FOUND", "当前项目没有此候选检查", 2)
	}
	if err != nil {
		return r, err
	}
	if err = decode(b, &r); err != nil || r.Version != 1 || r.Kind != "replica-generation-check" || r.ID != id || r.Owner != m.Source.Owner || r.Scope != m.Source.Scope || !uuid.MatchString(r.Generation) || r.Digest != generationCheckDigest(r) || r.WriteSupported || r.PublicationStatus != "BLOCKED" || len(r.Targets) == 0 {
		return r, result.Fail("CHECK_CORRUPT", "候选检查身份、摘要或发布边界损坏", 5)
	}
	return r, nil
}

func (m *Manager) GenerationCheckShow(ctx context.Context, id string) (GenerationCheck, error) {
	r, err := m.loadGenerationCheck(id)
	if err != nil {
		return r, err
	}
	stale := func(reason string) (GenerationCheck, error) {
		r.Stale, r.StaleReason = true, reason
		return r, nil
	}
	if r.Policy != inspectionPolicy {
		return stale("候选检查规则已变化或缺少当前规则绑定；保留历史，请重新检查")
	}
	created, ce := time.Parse(time.RFC3339Nano, r.Created)
	expires, ee := time.Parse(time.RFC3339Nano, r.Expires)
	if ce != nil || ee != nil || created.After(time.Now().UTC()) || !time.Now().Before(expires) || !expires.After(created) || expires.Sub(created) > 24*time.Hour {
		return stale("候选检查超过有效期或时间无效")
	}
	g, p, err := m.loadGeneration(r.Generation, r.GenerationDigest)
	if err != nil {
		return stale("无法核对原生成清单；保留历史证据，请重新预览")
	}
	if err = m.revalidateGeneration(ctx, g, p); err != nil {
		if ctx.Err() != nil {
			return r, ctx.Err()
		}
		return stale("源、目标、绑定、分支、基线或适配器已变化；" + err.Error())
	}
	for _, target := range r.Targets {
		n, err := m.candidateNative(target.Copy)
		if err != nil {
			return stale("无法核对原目标；" + err.Error())
		}
		if err = n.InspectionFresh(target.Inspection); err != nil {
			return stale(err.Error())
		}
	}
	return r, nil
}

// History exposes immutable historical observations, without starting tools or
// observing every old project's files. Show recomputes selected freshness.
func (m *Manager) GenerationCheckHistory() ([]GenerationCheck, error) {
	if err := workstore.CheckLinks(m.generationChecksDir()); err != nil {
		return nil, err
	}
	files, err := os.ReadDir(m.generationChecksDir())
	if os.IsNotExist(err) {
		return []GenerationCheck{}, nil
	}
	if err != nil {
		return nil, err
	}
	rows := []GenerationCheck{}
	for _, f := range files {
		if strings.HasPrefix(f.Name(), ".resolution-") {
			continue
		}
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			return nil, result.Fail("CHECK_CORRUPT", "候选检查目录含未知记录", 5)
		}
		r, err := m.loadGenerationCheck(strings.TrimSuffix(f.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, rows[i].Created)
		b, _ := time.Parse(time.RFC3339Nano, rows[j].Created)
		if a.Equal(b) {
			return rows[i].ID > rows[j].ID
		}
		return a.After(b)
	})
	return rows, nil
}
