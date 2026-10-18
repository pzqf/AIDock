package native

import (
	"context"
	"errors"
	"path/filepath"
	"sort"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

// This report contains observations and safe changes, never a write permit or
// source/native file bodies. Complete compiler material stays in privatefs.
type ConfigurationPreparation struct {
	ID        string               `json:"id"`
	Candidate string               `json:"candidate_digest"`
	Project   string               `json:"project"`
	Scope     string               `json:"scope"`
	Copy      string               `json:"copy"`
	Root      string               `json:"root"`
	Tools     []string             `json:"tools"`
	MCP       []string             `json:"mcp_names"`
	MCPReady  bool                 `json:"mcp_ready"`
	Status    string               `json:"status"`
	Fresh     bool                 `json:"fresh"`
	CanApply  bool                 `json:"can_apply"`
	Check     CheckReport          `json:"check"`
	Changes   []ReplayNativeChange `json:"native_changes"`
	Gates     []inventory.Finding  `json:"gates"`
	Digest    string               `json:"digest"`
	Boundary  string               `json:"boundary"`
}

type configurationPrepared struct {
	Version       int                        `json:"schema_version"`
	Report        ConfigurationPreparation   `json:"report"`
	Request       CheckRequest               `json:"request"`
	Native        Plan                       `json:"native"`
	Connections   []workstore.BatchTarget    `json:"connections"`
	Sessions      []source.MCPSessionRequest `json:"sessions"`
	MCPInputs     map[string]workstore.Tree  `json:"mcp_inputs"`
	MCPCode       string                     `json:"mcp_code,omitempty"`
	Executable    string                     `json:"executable"`
	ExecutableSHA string                     `json:"executable_sha256"`
	Home          string                     `json:"home"`
	Digest        string                     `json:"digest"`
}

func configurationPreparedDigest(p configurationPrepared) string {
	p.Digest, p.Report.Digest = "", ""
	return workstore.Hash(jsonData(p))
}

func preparedNativeContentDigest(p Plan) string {
	// Qualification is obtained after compilation and has its own exact check
	// binding. The compiler content digest is calculated before that receipt.
	p.Qualification = nil
	return contentDigest(p)
}
func (m *Manager) configurationPreparedFile(id string) string {
	return filepath.Join(m.connectionArea(), "configuration-prepared", id+".json")
}
func (m *Manager) compileConfigurationCandidate(ctx context.Context, c source.ConfigurationCandidate, req CheckRequest) (Plan, *Manager, error) {
	return m.compileFutureConfiguration(ctx, req, "configuration-candidate", func(use func(source.Export) error) error {
		return m.Source.WithCandidateExport(ctx, c, use)
	})
}

func (m *Manager) configurationCandidateConnections(ctx context.Context, c source.ConfigurationCandidate, tools []string, executable, home string) ([]workstore.BatchTarget, []source.MCPSessionRequest, error) {
	connections, sessions := []workstore.BatchTarget{}, []source.MCPSessionRequest{}
	err := m.Source.WithCandidateMCPClientConfigs(ctx, c, executable, home, func(configs []source.MCPClientConfiguration, wanted []source.MCPSessionRequest) error {
		requests, pointers := []MCPConnectionRequest{}, []*source.MCPClientConfiguration{}
		for i, s := range wanted {
			requests = append(requests, MCPConnectionRequest{Action: "install", Tools: tools, Session: s})
			pointers = append(pointers, &configs[i])
		}
		var err error
		connections, err = m.compileConnectionSet(tools, requests, pointers, executable, home, true)
		if err == nil {
			sessions = append(sessions, wanted...)
		}
		return err
	})
	return connections, sessions, err
}

// Observe native configuration even if a missing package or approval stops the
// connection compiler before it can read those files. A blocked observation
// must expire on later native edits just like a successful one does.
func (m *Manager) configurationMCPInputs(tools []string) (map[string]workstore.Tree, error) {
	as, err := selectAdapters(tools)
	if err != nil {
		return nil, err
	}
	paths := []string{connectionLedgerPath}
	for _, a := range as {
		if a.MCPConfig != nil {
			paths = append(paths, a.MCPConfig.Path)
		}
	}
	out := map[string]workstore.Tree{}
	for _, path := range paths {
		tree, err := m.connectionStore().CaptureFile(filepath.Join(m.Root, path))
		if err != nil {
			return nil, err
		}
		out[path] = tree
	}
	return out, nil
}

func preparationErrorCode(err error) (string, bool) {
	if err == nil {
		return "", true
	}
	var classified *result.Error
	if errors.As(err, &classified) {
		return classified.Code, true
	}
	return "", false
}

func (m *Manager) keepPreparedNativeAssets(compiler *Manager, p Plan) error {
	for _, t := range batchTargets(p) {
		for _, tree := range []workstore.Tree{t.Before, t.After} {
			for _, e := range tree.Entries {
				if e.Dir {
					continue
				}
				body, err := compiler.Source.Store.Data(e)
				if err != nil {
					return err
				}
				scratch := workstore.Tree{Entries: map[string]workstore.Entry{}}
				if err := m.connectionStore().PutFile(&scratch, "content", body, e.Mode); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (m *Manager) PrepareConfigurationCandidate(ctx context.Context, c source.ConfigurationCandidate, req CheckRequest, executable, home string) (ConfigurationPreparation, error) {
	out := ConfigurationPreparation{ID: workstore.ID(), Candidate: c.Digest(), Project: m.Source.Owner, Scope: m.Source.Scope, Copy: m.Source.Copy, Root: m.Root, Tools: []string{}, MCP: []string{}, Status: "NEEDS_CHECK", Fresh: true, Changes: []ReplayNativeChange{}, Gates: []inventory.Finding{}, Boundary: "重新编译此目标未来配置的完整技能、规则及全部启用 MCP。默认静态；只在明确 probe 时运行空配置临时工具发现，规则来源另行确认。不启动 MCP、不授予权限、不修改源或原生文件。CHECKED 只表示完整候选本次检查完成；正式批次须另行预览、固定应用快照并确认，不能用此摘要调用普通应用。L3/L4 仍未验证。"}
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || !filepath.IsAbs(home) || filepath.Join(home, "local") != m.Source.Store.Local {
		return out, result.Fail("MCP_CLIENT_CONFIG_BINDING_INVALID", "完整候选检查需要当前 home 与稳定绝对程序路径", 2)
	}
	scope, err := NormalizeApplicationScope(req.ApplicationScope)
	if err != nil || scope != "" || req.RuleProbe && !req.Probe {
		return out, result.Fail("INVALID_INPUT", "完整候选不能排除其它配置；规则来源核验须同时明确工具发现", 2)
	}
	req.ApplicationScope = ""
	p, compiler, err := m.compileConfigurationCandidate(ctx, c, req)
	if err != nil {
		return out, err
	}
	for _, target := range p.Targets {
		out.Tools = append(out.Tools, target.Adapter.ID)
	}
	inputs, err := m.configurationMCPInputs(out.Tools)
	if err != nil {
		return out, err
	}
	// Keep the complete requested set visible even when the first dependency
	// gate prevents connection compilation. A failed item is never an omission.
	if p.Source.ProfileReport != nil {
		section, _ := p.Source.ProfileReport.Effective["mcp"].(map[string]any)
		enabled := map[string]bool{}
		for _, raw := range arrayValues(section["enable"]) {
			if name, ok := raw.(string); ok {
				enabled[name] = true
			}
		}
		for _, raw := range arrayValues(section["disable"]) {
			if name, ok := raw.(string); ok {
				delete(enabled, name)
			}
		}
		for name := range enabled {
			out.MCP = append(out.MCP, name)
		}
		sort.Strings(out.MCP)
	}
	sha, err := executableDigest(executable)
	if err != nil {
		return out, err
	}
	out.Check, err = compiler.InspectDraftScoped(ctx, p.Targets, c.Digest(), req.Probe && !p.Blocked, req.RuleProbe && !p.Blocked, SkillsRulesScope, p.ExcludedChecks)
	if err != nil {
		return out, err
	}
	if p.Blocked {
		out.Status = "BLOCKED"
		out.Gates = append(out.Gates, inventory.Finding{Status: "FAIL", Code: "CONFIGURATION_NATIVE_PREFLIGHT_FAILED"})
	} else if req.Probe {
		q, err := compiler.QualifyDraft(p.Targets, c.Digest(), SkillsRulesScope, p.ExcludedChecks, out.Check)
		if err == nil {
			p.Qualification = &q
			out.Status = "CHECKED"
		} else if code, known := preparationErrorCode(err); known {
			out.Status = "NEEDS_ATTENTION"
			out.Gates = append(out.Gates, inventory.Finding{Status: "UNKNOWN", Code: code})
		} else {
			return out, err
		}
	}
	connections, sessions, connectionErr := m.configurationCandidateConnections(ctx, c, out.Tools, executable, home)
	connectionCode, known := preparationErrorCode(connectionErr)
	if !known {
		return out, connectionErr
	}
	if connectionErr == nil {
		out.MCPReady = true
		observed := []string{}
		for _, s := range sessions {
			observed = append(observed, s.Name)
		}
		if string(jsonData(observed)) != string(jsonData(out.MCP)) {
			return out, result.Fail("CONFIGURATION_MCP_SET_MISMATCH", "连接编译未覆盖全部启用 MCP", 5)
		}
	} else {
		out.Gates = append(out.Gates, inventory.Finding{Status: "UNKNOWN", Code: connectionCode})
		if out.Status != "BLOCKED" {
			out.Status = "NEEDS_ATTENTION"
		}
	}
	// Recompile after probes/authority reads. Never save mixed observations.
	fresh, _, err := m.compileConfigurationCandidate(ctx, c, req)
	if err != nil {
		return out, err
	}
	if fresh.ContentDigest != p.ContentDigest {
		return out, result.Fail("PLAN_STALE", "检查期间候选材料或原生文件变化", 3)
	}
	currentConnections, currentSessions, currentErr := m.configurationCandidateConnections(ctx, c, out.Tools, executable, home)
	currentCode, currentKnown := preparationErrorCode(currentErr)
	if !currentKnown {
		return out, currentErr
	}
	if currentCode != connectionCode || string(jsonData(currentConnections)) != string(jsonData(connections)) || string(jsonData(currentSessions)) != string(jsonData(sessions)) {
		return out, result.Fail("PLAN_STALE", "检查期间 MCP 授权、运行时或原生字段变化", 3)
	}
	currentInputs, err := m.configurationMCPInputs(out.Tools)
	if err != nil {
		return out, err
	}
	if string(jsonData(currentInputs)) != string(jsonData(inputs)) {
		return out, result.Fail("PLAN_STALE", "检查期间原生 MCP 文件或归属记录变化", 3)
	}
	if current, err := executableDigest(executable); err != nil || current != sha {
		return out, result.Fail("PLAN_STALE", "检查期间 AIDock 程序变化", 3)
	}
	for _, t := range append(batchTargets(p), connections...) {
		out.Changes = append(out.Changes, ReplayNativeChange{Path: t.Path, Changes: workstore.Changes(t.Before, t.After), Before: workstore.Digest(t.Before), After: workstore.Digest(t.After)})
	}
	if err = m.keepPreparedNativeAssets(compiler, p); err != nil {
		return out, err
	}
	stored := configurationPrepared{Version: 1, Report: out, Request: req, Native: p, Connections: connections, Sessions: sessions, MCPInputs: inputs, MCPCode: connectionCode, Executable: executable, ExecutableSHA: sha, Home: home}
	out.Digest = configurationPreparedDigest(stored)
	stored.Report, stored.Digest = out, out.Digest
	if err := save(m.configurationPreparedFile(out.ID), jsonData(stored)); err != nil {
		return out, err
	}
	return out, nil
}

func arrayValues(v any) []any {
	a, _ := v.([]any)
	return a
}

func (m *Manager) loadConfigurationPreparation(id string) (configurationPrepared, error) {
	var p configurationPrepared
	if !validID.MatchString(id) {
		return p, result.Fail("INVALID_INPUT", "需要当前目标的完整候选检查 UUID", 2)
	}
	raw, err := workstore.ReadFile(m.configurationPreparedFile(id))
	if err != nil {
		return p, err
	}
	if strict(raw, &p) != nil || p.Version != 1 || p.Report.ID != id || p.Report.Root != m.Root || p.Report.Project != m.Source.Owner || p.Report.Scope != m.Source.Scope || p.Report.Copy != m.Source.Copy || p.Report.CanApply || !validDigest.MatchString(p.Report.Candidate) || p.Digest != p.Report.Digest || p.Digest != configurationPreparedDigest(p) || p.Native.Kind != "configuration-candidate" || p.Native.Root != m.Root || p.Native.WriteSupported || p.Native.ContentDigest != preparedNativeContentDigest(p.Native) || p.Request.ApplicationScope != "" || filepath.Join(p.Home, "local") != m.Source.Store.Local {
		return configurationPrepared{}, result.Fail("CHECK_CORRUPT", "完整候选检查绑定或材料摘要不符", 5)
	}
	return p, nil
}

func (m *Manager) revalidateConfigurationPreparation(ctx context.Context, c source.ConfigurationCandidate, p configurationPrepared) error {
	if c.Digest() != p.Report.Candidate {
		return result.Fail("PLAN_STALE", "原完整候选身份已变化", 3)
	}
	sha, err := executableDigest(p.Executable)
	if err != nil || sha != p.ExecutableSHA {
		return result.Fail("PLAN_STALE", "原 AIDock 程序已变化", 3)
	}
	inputs, err := m.configurationMCPInputs(p.Report.Tools)
	if err != nil {
		return err
	}
	if string(jsonData(inputs)) != string(jsonData(p.MCPInputs)) {
		return result.Fail("PLAN_STALE", "原生 MCP 文件或归属记录变化", 3)
	}
	fresh, compiler, err := m.compileConfigurationCandidate(ctx, c, p.Request)
	if err != nil {
		return err
	}
	if fresh.ContentDigest != p.Native.ContentDigest {
		return result.Fail("PLAN_STALE", "完整候选、配置层或目标观察变化", 3)
	}
	if err := compiler.InspectionFresh(p.Report.Check); err != nil {
		return err
	}
	if p.Native.Qualification != nil {
		q, err := compiler.QualifyDraft(fresh.Targets, c.Digest(), SkillsRulesScope, fresh.ExcludedChecks, p.Report.Check)
		if err != nil {
			return err
		}
		if string(jsonData(q)) != string(jsonData(p.Native.Qualification)) {
			return result.Fail("CHECK_CORRUPT", "保存的工具资格不符", 5)
		}
	}
	connections, sessions, err := m.configurationCandidateConnections(ctx, c, p.Report.Tools, p.Executable, p.Home)
	code, known := preparationErrorCode(err)
	if !known {
		return err
	}
	if code != p.MCPCode || string(jsonData(connections)) != string(jsonData(p.Connections)) || string(jsonData(sessions)) != string(jsonData(p.Sessions)) {
		return result.Fail("PLAN_STALE", "全部启用 MCP 的授权、运行时或原生字段观察变化", 3)
	}
	return nil
}

func (m *Manager) ReadConfigurationPreparation(ctx context.Context, c source.ConfigurationCandidate, id string) (ConfigurationPreparation, error) {
	p, err := m.loadConfigurationPreparation(id)
	if err != nil {
		return ConfigurationPreparation{}, err
	}
	out := p.Report
	if err := m.revalidateConfigurationPreparation(ctx, c, p); err != nil {
		code, known := preparationErrorCode(err)
		if !known {
			code = "CONFIGURATION_PREPARATION_STALE"
		}
		out.Status, out.Fresh, out.MCPReady = "STALE", false, false
		out.Changes = []ReplayNativeChange{}
		out.Check.Stale, out.Check.StaleReason = true, code
		out.Gates = append(append([]inventory.Finding{}, out.Gates...), inventory.Finding{Status: "UNKNOWN", Code: code})
	}
	return out, nil
}
