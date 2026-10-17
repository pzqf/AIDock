package native

import (
	"context"
	"path/filepath"

	"github.com/pzqf/AIDock/internal/mcprelay"
	"github.com/pzqf/AIDock/internal/mcpsession"
	"github.com/pzqf/AIDock/internal/nativeconfig"
	"github.com/pzqf/AIDock/internal/projectsnapshot"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Deterministic bindings preserve the no-op behavior of repeated compilation.
// Native before-images and unrelated handwritten fields are not source input.
func (m *Manager) appliedKey(bundle source.Export, sessions []source.MCPSessionRequest, adapters map[string]string, executableSHA string) string {
	return workstore.Hash(jsonData(struct {
		Root, Project, Scope, Copy, Source, Configuration, Executable string
		Sessions                                                      []source.MCPSessionRequest
		Adapters                                                      map[string]string
	}{m.Root, m.Source.Owner, m.Source.Scope, m.Source.Copy, bundle.Snapshot, bundle.ConfigurationDigest, executableSHA, sessions, adapters}))
}

func (m *Manager) appliedConfiguration(c source.MCPClientConfiguration, key string) source.MCPClientConfiguration {
	args := []string{}
	for i := 0; i < len(c.Args); i++ {
		if c.Args[i] == "--library-root" && i+1 < len(c.Args) {
			i++
			continue
		}
		args = append(args, c.Args[i])
	}
	c.Args = append(args, "--applied-key", key, "--native-root", m.Root)
	c.AppliedKey = key
	c.Boundary = "已应用视图的受管连接。启动及每次传输核对成功应用事务、原生托管字段与当前安装、授权、版本和完整产物；后续已保存草稿不替换此冻结配置。明确撤销仍立即阻断。不代表文件或网络隔离。"
	return c
}

type appliedRecord struct {
	Archive *projectsnapshot.Archive
	Record  projectsnapshot.Record
	Plan    ProfileApplicationPlan
	Frozen  source.FrozenMCP
	Ledger  connectionLedger
}

func (m *Manager) loadAppliedRecord(ctx context.Context, key string) (appliedRecord, error) {
	var out appliedRecord
	if !validDigest.MatchString(key) {
		return out, result.Fail("INVALID_INPUT", "已应用视图需要完整绑定摘要", 2)
	}
	a, records, js, err := m.projectSnapshotRecords(ctx)
	if err != nil {
		return out, err
	}
	for _, r := range records {
		if r.ActiveKey != key {
			continue
		}
		state, err := snapshotState(r, js)
		if err != nil {
			return out, err
		}
		if state != "committed" {
			continue
		}
		raw, err := workstore.ReadFile(filepath.Join(m.connectionArea(), "profile-plans", r.Transaction.ID+".json"))
		if err != nil {
			return out, err
		}
		var p ProfileApplicationPlan
		if strict(raw, &p) != nil || p.ActiveKey != key || p.Digest != profileApplicationDigest(p) || p.ID != r.Transaction.ID || p.Root != m.Root || string(jsonData(p.Transaction)) != string(jsonData(r.Transaction)) || m.appliedKey(r.Effective, p.Request.Sessions, r.Adapters, p.ExecutableSHA) != key {
			return out, result.Fail("APPLIED_VIEW_INVALID", "已应用绑定与原始计划或归档不一致", 5)
		}
		material, err := a.Materialize(ctx, r.ID, m.Source.Store)
		if err != nil {
			return out, err
		}
		native, err := a.MaterializeNative(ctx, r.ID, m.connectionStore())
		if err != nil {
			return out, err
		}
		if material.Commit != r.Commit || native.Commit != r.Commit || r.Configuration == nil {
			return out, result.Fail("APPLIED_VIEW_INVALID", "已应用归档引用或配置材料变化", 5)
		}
		var ledger connectionLedger
		found := false
		for _, t := range r.Transaction.Targets {
			if t.Path == connectionLedgerPath && t.After.Exists {
				body, err := m.connectionStore().Data(t.After.Entries[workstore.FileContent])
				if err != nil {
					return out, err
				}
				ledger, err = m.decodeConnectionLedger(body)
				if err != nil {
					return out, err
				}
				found = true
			}
		}
		if !found {
			ledger = connectionLedger{Tools: map[string]connectionTool{}}
		}
		return appliedRecord{a, r, p, source.FrozenMCP{Tree: r.Source, Effective: r.Effective, Configuration: *r.Configuration}, ledger}, nil
	}
	return out, result.Fail("APPLIED_VIEW_NOT_COMMITTED", "没有与绑定匹配的成功应用快照；归档意图不能启动插件", 4)
}

func (m *Manager) appliedArchiveCurrent(ctx context.Context, a appliedRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r, err := a.Archive.Metadata(ctx, a.Record.ID)
	if err != nil {
		return err
	}
	if r.Commit != a.Record.Commit || r.Digest != a.Record.Digest || r.ActiveKey != a.Plan.ActiveKey {
		return result.Fail("APPLIED_VIEW_INVALID", "已应用归档引用或完整摘要变化", 5)
	}
	sha, err := executableDigest(a.Plan.Executable)
	if err != nil || sha != a.Plan.ExecutableSHA {
		return result.Fail("PLAN_STALE", "已安装连接对应的 AIDock 程序变化，请重新检查并安装", 3)
	}
	return nil
}

func (m *Manager) withAppliedNative(ctx context.Context, a appliedRecord, req source.MCPSessionRequest, use func() error) error {
	return m.withAppliedNativeTool(ctx, a, req, "", use)
}
func (m *Manager) withAppliedNativeTool(ctx context.Context, a appliedRecord, req source.MCPSessionRequest, onlyTool string, use func() error) error {
	return m.profileApplicationBatch().WithCommitted(a.Record.Transaction.ID, a.Record.Transaction.Digest, func(j workstore.BatchJournal) error {
		if string(jsonData(j.Plan)) != string(jsonData(a.Record.Transaction)) {
			return result.Fail("APPLIED_VIEW_INVALID", "原始成功事务被替换", 5)
		}
		if err := m.appliedArchiveCurrent(ctx, a); err != nil {
			return err
		}
		live, err := m.connectionLedger()
		if err != nil {
			return err
		}
		req.Probe = false
		name := "aidock-" + req.Name
		for tool, wanted := range a.Ledger.Tools {
			if onlyTool != "" && tool != onlyTool {
				continue
			}
			expected, ok := wanted.Fields[name]
			if !ok || expected.AppliedKey != a.Plan.ActiveKey || string(jsonData(expected.Session)) != string(jsonData(req)) {
				continue
			}
			current, ok := live.Tools[tool]
			if !ok {
				continue
			}
			entry, ok := current.Fields[name]
			if !ok || string(jsonData(entry)) != string(jsonData(expected)) {
				continue
			}
			body, present, err := readConfiguration(filepath.Join(m.Root, current.Contract.Path))
			if err != nil {
				return err
			}
			if !present {
				continue
			}
			observed, err := nativeconfig.Inspect(current.Contract, body)
			if err != nil {
				return err
			}
			for _, field := range observed.Fields {
				if field.Name == name && field.SHA256 == expected.SHA256 {
					return use()
				}
			}
		}
		return result.Fail("APPLIED_VIEW_NOT_INSTALLED", "已应用连接已移除、替换或漂移；旧参数不可继续使用", 4)
	})
}

func (m *Manager) ServeAppliedMCPClient(ctx context.Context, key string, req source.MCPSessionRequest, executable string, client mcprelay.Endpoint) (mcpsession.Event, error) {
	a, err := m.loadAppliedRecord(ctx, key)
	if err != nil {
		return mcpsession.Event{}, err
	}
	if executable != a.Plan.Executable {
		return mcpsession.Event{}, result.Fail("PLAN_STALE", "受管连接须由原安装路径的 AIDock 程序启动", 3)
	}
	return m.Source.ServeFrozenMCPClient(ctx, a.Frozen, req, func(c context.Context, use func() error) error { return m.withAppliedNative(c, a, req, use) }, client)
}
