package native

import (
	"context"
	"path/filepath"
	"sort"
	"time"

	"github.com/pzqf/AIDock/internal/privatefs"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

// The skill/rule check remains independently reviewable. Joining it with MCP
// never broadens its qualification or authorizes a new plugin.
type ProfileApplicationRequest struct {
	NativePlan   string                     `json:"native_plan"`
	NativeDigest string                     `json:"native_digest"`
	Sessions     []source.MCPSessionRequest `json:"sessions"`
}

type ProfileApplicationPlan struct {
	Version       int                       `json:"schema_version"`
	ID            string                    `json:"id"`
	Root          string                    `json:"root"`
	Request       ProfileApplicationRequest `json:"request"`
	Executable    string                    `json:"executable"`
	ExecutableSHA string                    `json:"executable_sha256"`
	Home          string                    `json:"home"`
	Transaction   workstore.BatchPlan       `json:"transaction"`
	Changes       int                       `json:"changes"`
	Digest        string                    `json:"digest"`
	Boundary      string                    `json:"boundary"`
}

func profileApplicationDigest(p ProfileApplicationPlan) string {
	p.Digest = ""
	return workstore.Hash(jsonData(p))
}

func (m *Manager) profileApplicationBatch() *workstore.Batch {
	b := m.connectionBatch()
	b.Identity = "profile/" + m.Source.Owner + "/" + m.Source.Scope + "/" + m.Source.Copy
	b.Hook = m.applicationHook
	for _, a := range Adapters() {
		b.Paths = append(b.Paths, a.Directory, rulePath(a.ID))
		b.FilePaths = append(b.FilePaths, rulePath(a.ID))
	}
	return b
}

func (m *Manager) profileApplicationNative(ctx context.Context, req ProfileApplicationRequest) (Plan, error) {
	p, err := m.Diff(ctx, req.NativePlan, req.NativeDigest)
	if err != nil {
		return p, err
	}
	if m.Source.Copy == "" || p.ApplicationScope != SkillsRulesScope || p.Blocked || !p.WriteSupported || p.Qualification == nil {
		return p, result.Fail("PROFILE_APPLICATION_CHECK_REQUIRED", "先在当前副本准备技能与规则应用，通过显式工具发现及所需规则来源检查；联合操作不降低检查门槛", 4)
	}
	return p, nil
}

// Import immutable skill/rule assets into the same restricted bucket as raw
// native configuration backups. Nothing is copied from a live upstream path.
func (m *Manager) profileApplicationAssets(p Plan) ([]workstore.BatchTarget, error) {
	targets := batchTargets(p)
	for _, t := range targets {
		for _, tree := range []workstore.Tree{t.Before, t.After} {
			for _, e := range tree.Entries {
				if e.Dir {
					continue
				}
				data, err := m.Source.Store.Data(e)
				if err != nil {
					return nil, err
				}
				scratch := workstore.Tree{Entries: map[string]workstore.Entry{}}
				if err = m.connectionStore().PutFile(&scratch, "content", data, e.Mode); err != nil {
					return nil, err
				}
			}
		}
	}
	return targets, nil
}

func (m *Manager) withProfileApplication(ctx context.Context, req ProfileApplicationRequest, p Plan, executable, home string, use func([]workstore.BatchTarget) error) error {
	if len(req.Sessions) > 64 {
		return result.Fail("INVALID_INPUT", "联合应用最多包含64个明确的 MCP", 2)
	}
	for _, s := range req.Sessions {
		if s.Probe || !validName.MatchString(s.Name) {
			return result.Fail("INVALID_INPUT", "联合应用只接受已审查的连接绑定，不接受启动请求", 2)
		}
	}
	return m.Source.WithProfileMCPClientConfigs(ctx, req.Sessions, executable, home, func(now source.Export, configs []source.MCPClientConfiguration) error {
		if workstore.Hash(jsonData(now)) != workstore.Hash(jsonData(p.Source)) {
			return result.Fail("PLAN_STALE", "联合应用前技能源、有效配置或 MCP 证据已变化，请重新准备", 3)
		}
		tools := []string{}
		for _, t := range p.Targets {
			tools = append(tools, t.Adapter.ID)
			if t.Rule != nil && t.Rule.Override != nil {
				current, err := m.Source.Store.CaptureFile(filepath.Join(m.Root, t.Rule.Override.Path))
				if err != nil || workstore.Digest(current) != workstore.Digest(t.Rule.Override.Before) {
					return result.Fail("PLAN_STALE", "联合应用前规则优先级输入已变化", 3)
				}
			}
		}
		requests := []MCPConnectionRequest{}
		configurations := []*source.MCPClientConfiguration{}
		for i, s := range req.Sessions {
			requests = append(requests, MCPConnectionRequest{Action: "install", Tools: tools, Session: s})
			configurations = append(configurations, &configs[i])
		}
		connections, err := m.compileConnectionSet(tools, requests, configurations, executable, home, true)
		if err != nil {
			return err
		}
		targets, err := m.profileApplicationAssets(p)
		if err != nil {
			return err
		}
		// Repeat the time and binary checks under the authority locks, after
		// all potentially expensive artifact validation and compilation.
		if p.Qualification == nil {
			return result.Fail("NATIVE_CHECK_REQUIRED", "联合应用缺少工具检查资格", 4)
		}
		expires, err := time.Parse(time.RFC3339Nano, p.Qualification.Expires)
		if err != nil || !time.Now().Before(expires) {
			return result.Fail("CHECK_STALE", "工具检查已过期，请重新准备", 3)
		}
		for _, t := range p.Qualification.Tools {
			binary, e := m.probes().resolve(t.Tool)
			if e != nil || binary.Path != t.Executable || binary.Digest != t.BinaryDigest {
				return result.Fail("CHECK_STALE", "联合事务开始前工具程序已变化", 3)
			}
		}
		return use(append(targets, connections...))
	})
}

func (m *Manager) ProfileApplicationPlan(ctx context.Context, req ProfileApplicationRequest, executable, home string) (ProfileApplicationPlan, error) {
	var out ProfileApplicationPlan
	p, err := m.profileApplicationNative(ctx, req)
	if err != nil {
		return out, err
	}
	if err = privatefs.Ensure(m.connectionArea()); err != nil {
		return out, err
	}
	sha, err := executableDigest(executable)
	if err != nil {
		return out, err
	}
	req.Sessions = append([]source.MCPSessionRequest{}, req.Sessions...)
	sort.Slice(req.Sessions, func(i, j int) bool { return req.Sessions[i].Name < req.Sessions[j].Name })
	out = ProfileApplicationPlan{Version: 1, Root: m.Root, Request: req, Executable: executable, ExecutableSHA: sha, Home: home, Boundary: "一次确认共同更新所选工具的完整技能包、规则托管区块、有效 Profile 中全部已授权 MCP 连接及字段归属；移除本工具已托管而当前停用的连接，保留未托管字段。全部目标共用备份、失败恢复、回退与撤销。备份原始配置仅在受限本机目录。安装不启动插件、不授予权限，不提升技能 L3/L4；当前受管 MCP 仍为明确确认的 Tier 0 非隔离 Public 查询。其它尚未适配的 Profile 字段继续阻断。"}
	err = m.withProfileApplication(ctx, req, p, executable, home, func(targets []workstore.BatchTarget) error {
		current, e := executableDigest(executable)
		if e != nil || current != sha {
			return result.Fail("PLAN_STALE", "联合预览期间 AIDock 程序已变化", 3)
		}
		out.Transaction, e = m.profileApplicationBatch().Preview("apply", targets)
		if e != nil {
			return e
		}
		out.ID = out.Transaction.ID
		for _, t := range targets {
			out.Changes += len(workstore.Changes(t.Before, t.After))
		}
		out.Digest = profileApplicationDigest(out)
		return save(filepath.Join(m.connectionArea(), "profile-plans", out.ID+".json"), jsonData(out))
	})
	return out, err
}

func (m *Manager) loadProfileApplication(id, digest string) (ProfileApplicationPlan, error) {
	var p ProfileApplicationPlan
	if !validID.MatchString(id) || !validDigest.MatchString(digest) {
		return p, result.Fail("INVALID_INPUT", "需要联合应用计划 UUID 与完整摘要", 2)
	}
	data, err := workstore.ReadFile(filepath.Join(m.connectionArea(), "profile-plans", id+".json"))
	if err != nil {
		return p, err
	}
	if strict(data, &p) != nil || p.Version != 1 || p.Root != m.Root || p.ID != id || p.Transaction.ID != id || p.Transaction.Identity != m.profileApplicationBatch().Identity || p.Digest != digest || profileApplicationDigest(p) != digest {
		return p, result.Fail("PLAN_STALE", "联合应用计划身份或摘要不匹配", 3)
	}
	return p, nil
}

func (m *Manager) ApplyProfileApplication(ctx context.Context, id, digest string) (workstore.BatchJournal, error) {
	var out workstore.BatchJournal
	if err := privatefs.Ensure(m.connectionArea()); err != nil {
		return out, err
	}
	plan, err := m.loadProfileApplication(id, digest)
	if err != nil {
		return out, err
	}
	if plan.Changes == 0 {
		return out, result.Fail("PROFILE_APPLICATION_CURRENT", "技能、规则与有效 MCP 连接均已一致，无需重复安装", 3)
	}
	p, err := m.profileApplicationNative(ctx, plan.Request)
	if err != nil {
		return out, err
	}
	sha, err := executableDigest(plan.Executable)
	if err != nil || sha != plan.ExecutableSHA {
		return out, result.Fail("PLAN_STALE", "AIDock 程序已变化，请重新生成联合预览", 3)
	}
	err = m.withProfileApplication(ctx, plan.Request, p, plan.Executable, plan.Home, func(targets []workstore.BatchTarget) error {
		if workstore.Hash(jsonData(targets)) != workstore.Hash(jsonData(plan.Transaction.Targets)) {
			return result.Fail("PLAN_STALE", "联合应用的目标、归属或编译结果已变化", 3)
		}
		current, e := executableDigest(plan.Executable)
		if e != nil || current != plan.ExecutableSHA {
			return result.Fail("PLAN_STALE", "联合事务开始前 AIDock 程序已变化", 3)
		}
		out, e = m.profileApplicationBatch().Apply(ctx, plan.Transaction.ID, plan.Transaction.Digest)
		return e
	})
	return out, err
}

func (m *Manager) ProfileApplicationHistory() ([]workstore.BatchJournal, error) {
	return m.profileApplicationBatch().History()
}

func (m *Manager) RecoverProfileApplication() (int, error) {
	if err := privatefs.Ensure(m.connectionArea()); err != nil {
		return 0, err
	}
	return m.profileApplicationBatch().Recover()
}

func (m *Manager) ReverseProfileApplication(ctx context.Context, transaction string) (workstore.BatchPlan, error) {
	if !validID.MatchString(transaction) {
		return workstore.BatchPlan{}, result.Fail("INVALID_INPUT", "需要当前绑定最新的联合应用事务 UUID", 2)
	}
	if err := privatefs.Ensure(m.connectionArea()); err != nil {
		return workstore.BatchPlan{}, err
	}
	p, err := m.profileApplicationBatch().Reverse(transaction)
	if err != nil {
		return p, err
	}
	return p, m.withConnectionRestore(ctx, p, func() error { return nil })
}

func (m *Manager) ApplyProfileApplicationReverse(ctx context.Context, id, digest string) (workstore.BatchJournal, error) {
	var out workstore.BatchJournal
	if err := privatefs.Ensure(m.connectionArea()); err != nil {
		return out, err
	}
	p, err := m.profileApplicationBatch().LoadPlan(id)
	if err != nil {
		return out, err
	}
	if p.Digest != digest || (p.Action != "rollback" && p.Action != "undo-rollback") {
		return out, result.Fail("PLAN_KIND_MISMATCH", "需要联合应用的回退或撤销计划摘要", 2)
	}
	err = m.withConnectionRestore(ctx, p, func() error {
		var e error
		out, e = m.profileApplicationBatch().Apply(ctx, id, digest)
		return e
	})
	return out, err
}
