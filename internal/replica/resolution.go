package replica

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

type Decision struct {
	Target string `json:"target"`
	Entry  string `json:"entry"`
	Choice string `json:"choice"`
}
type ResolveRequest struct {
	Request
	Comparison string     `json:"comparison_digest"`
	Decisions  []Decision `json:"decisions"`
}
type ProposedTarget struct {
	Copy                Binding            `json:"copy"`
	Before              workstore.Tree     `json:"before"`
	After               workstore.Tree     `json:"after"`
	Changes             []workstore.Change `json:"changes"`
	Preflight           source.Preflight   `json:"preflight"`
	ConfigurationDigest string             `json:"configuration_digest,omitempty"`
}
type Resolution struct {
	Version       int              `json:"schema_version"`
	Kind          string           `json:"kind"`
	ID            string           `json:"id"`
	Owner         string           `json:"owner"`
	Scope         string           `json:"scope_id"`
	Created       string           `json:"created"`
	Request       ResolveRequest   `json:"request"`
	Source        Binding          `json:"source"`
	Targets       []ProposedTarget `json:"targets"`
	ControlBefore workstore.Tree   `json:"control_before"`
	ControlAfter  workstore.Tree   `json:"control_after"`
	Digest        string           `json:"digest"`
	Boundary      string           `json:"boundary"`
	Repair        bool             `json:"repair_only,omitempty"`
}

func resolutionDigest(p Resolution) string { p.Digest = ""; return workstore.Hash(data(p)) }
func (m *Manager) resolutionDir() string {
	return filepath.Join(m.Source.Store.Local, "replica-resolutions", workstore.Hash([]byte(m.Source.Owner+"/"+m.Source.Scope)))
}
func saveLocal(path string, value any) error {
	b := data(value)
	if len(b) > workstore.MaxFile {
		return result.Fail("PLAN_TOO_LARGE", "完整计划超过 10 MB，缩小目标或条目范围", 4)
	}
	if err := workstore.CheckLinks(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".resolution-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func transfer(from, to *workstore.Store, trees ...workstore.Tree) error {
	for _, tree := range trees {
		for path, file := range tree.Entries {
			if file.Dir {
				continue
			}
			bytes, err := from.Data(file)
			if err != nil {
				return err
			}
			scratch := workstore.Tree{Entries: map[string]workstore.Entry{}}
			if err = to.PutFile(&scratch, path, bytes, file.Mode); err != nil {
				return err
			}
		}
	}
	return nil
}

// Resolve records explicit entry decisions and constructs complete candidates.
// Target reads and candidate construction only write immutable Local assets.
func (m *Manager) Resolve(ctx context.Context, req ResolveRequest) (Resolution, error) {
	return m.resolve(ctx, req, true)
}

func (m *Manager) resolve(ctx context.Context, req ResolveRequest, persist bool) (Resolution, error) {
	var out Resolution
	err := m.Control.Locked(func() error {
		var err error
		out, err = m.resolveLocked(ctx, req, persist)
		return err
	})
	return out, err
}

// resolveLocked rebuilds decisions while the caller owns the control lock.
func (m *Manager) resolveLocked(ctx context.Context, req ResolveRequest, persist bool) (Resolution, error) {
	var out Resolution
	err := func() error {
		if err := m.Control.Pending(); err != nil {
			return err
		}
		t, err := m.Control.Capture(m.Control.Root)
		if err != nil {
			return err
		}
		report, err := m.compare(ctx, req.Request, t)
		if err != nil {
			return err
		}
		if req.Comparison == "" || req.Comparison != report.Digest {
			return result.Fail("PLAN_STALE", "比较结果已变化，请重新比较并选择", 3)
		}
		decisions := map[string]Decision{}
		for _, d := range req.Decisions {
			key := d.Target + "/" + d.Entry
			if _, ok := decisions[key]; ok || d.Choice != "source" && d.Choice != "target" && d.Choice != "skip" {
				return result.Fail("REPLICA_DECISION_INVALID", "决策重复或不是 source/target/skip", 2)
			}
			decisions[key] = d
		}
		out = Resolution{Version: 1, Kind: "replica-resolution", ID: workstore.ID(), Owner: m.Source.Owner, Scope: m.Source.Scope, Created: time.Now().UTC().Format(time.RFC3339Nano), Request: req, Source: report.From, Targets: []ProposedTarget{}, ControlBefore: t, ControlAfter: workstore.Clone(t), Boundary: "当前候选本身不写入登记项目；可独立预演或经发布检查另行确认：完整 owned 技能及登记的共享 Profile、规则源按独立决策采用源版本。技能开关、本机覆盖、未登记文件和开放配置各自保留；保留目标/跳过不反向修改发布者。源、共同基线与生成物可联合回退，正式同步需另行预览并确认。"}
		publisher, err := m.copySource(report.From)
		if err != nil {
			return err
		}
		count := 0
		for _, target := range report.Targets {
			if target.Status != "READY" {
				return result.Fail("REPLICA_BINDING_BLOCKED", target.Copy.Name+": "+target.Reason, 4)
			}
			observer, err := m.copySource(target.Copy)
			if err != nil {
				return err
			}
			before, err := observer.Store.Capture(target.Copy.Root)
			if err != nil {
				return err
			}
			if workstore.Digest(before) != target.Snapshot {
				return result.Fail("PLAN_STALE", "目标在构建候选前变化", 3)
			}
			selected := []source.SharedEntry{}
			configurations := []source.SharedConfiguration{}
			base, _, err := m.baseline(t, req.From, target.Copy.ID)
			if err != nil {
				return err
			}
			advanced := false
			for _, entry := range target.Entries {
				key := target.Copy.ID + "/" + entry.ID
				d, ok := decisions[key]
				if entry.Status != "SAME" && !ok {
					return result.Fail("REPLICA_DECISION_REQUIRED", "逐项选择采用源、保留目标或跳过: "+target.Copy.Name+"/"+entry.Name, 4)
				}
				delete(decisions, key)
				if !ok || d.Choice != "source" || entry.Status == "SAME" {
					continue
				}
				if entry.Status == "LOCAL_EXCLUDED" || entry.Status == "EXTERNAL_REGENERATION_REQUIRED" {
					return result.Fail("REPLICA_AUTHORITY_UNSUPPORTED", entry.Reason, 4)
				}
				if entry.Source == nil {
					return result.Fail("REPLICA_REMOVAL_UNSUPPORTED", "缺失源不能隐式物理删除；先明确退役并保留包", 4)
				}
				if err = transfer(publisher.Store, m.Control, entry.Source.Tree); err != nil {
					return err
				}
				selected = append(selected, *entry.Source)
				base.Entries[entry.ID] = *entry.Source
				advanced = true
			}
			for _, entry := range target.Configurations {
				key := target.Copy.ID + "/" + entry.ID
				d, ok := decisions[key]
				if entry.Status != "SAME" && !ok {
					return result.Fail("REPLICA_DECISION_REQUIRED", "逐项选择共享配置: "+target.Copy.Name+"/"+entry.Name, 4)
				}
				delete(decisions, key)
				if !ok || d.Choice != "source" || entry.Status == "SAME" {
					continue
				}
				if entry.Status == "LOCAL_EXCLUDED" {
					return result.Fail("REPLICA_AUTHORITY_UNSUPPORTED", entry.Reason, 4)
				}
				if entry.Status == "MCP_DEFINITION_BLOCKED" || entry.Status == "MCP_DEFINITION_UNAVAILABLE" {
					return result.Fail("REPLICA_CONFIGURATION_BLOCKED", entry.Reason, 4)
				}
				coordinatedDefinition := false
				if strings.HasPrefix(entry.ID, source.SharedPluginPrefix) {
					name := strings.TrimPrefix(entry.ID, source.SharedPluginPrefix)
					for _, choice := range req.Decisions {
						if choice.Target == target.Copy.ID && choice.Entry == source.SharedMCPPrefix+name && choice.Choice == "source" {
							coordinatedDefinition = true
						}
					}
				}
				if entry.Status == "CONFIGURATION_CONTRACT_MISMATCH" && !coordinatedDefinition || entry.Status == "CONFIGURATION_VALUE_BLOCKED" {
					return result.Fail("REPLICA_CONFIGURATION_BLOCKED", entry.Reason, 4)
				}
				if entry.Source == nil || source.ValidateSharedConfiguration(*entry.Source) != nil {
					return result.Fail("REPLICA_CONFIGURATION_INVALID", "共享配置源缺失或无效", 5)
				}
				configurations = append(configurations, *entry.Source)
				if base.Configurations == nil {
					base.Configurations = map[string]source.SharedConfiguration{}
				}
				base.Configurations[entry.ID] = *entry.Source
				advanced = true
			}
			if len(selected) == 0 && len(configurations) == 0 {
				continue
			}
			configuration, err := observer.ConfigurationDigest()
			if err != nil {
				return err
			}
			after, check, err := observer.DraftSharedConfiguration(before, selected, configurations, m.Control)
			if err != nil {
				return err
			}
			if err = transfer(observer.Store, m.Control, before, after); err != nil {
				return err
			}
			currentConfiguration, err := observer.ConfigurationDigest()
			if err != nil {
				return err
			}
			if configuration != currentConfiguration {
				return result.Fail("PLAN_STALE", "构建候选期间目标本机配置或全局库变化", 3)
			}
			out.Targets = append(out.Targets, ProposedTarget{Copy: target.Copy, Before: before, After: after, Changes: workstore.Changes(before, after), Preflight: check, ConfigurationDigest: configuration})
			count += len(workstore.Changes(before, after))
			if advanced {
				if err = m.Control.PutFile(&out.ControlAfter, pairPath(req.From, target.Copy.ID), data(base), 0600); err != nil {
					return err
				}
			}
		}
		if len(decisions) > 0 {
			return result.Fail("REPLICA_DECISION_INVALID", "决策包含未选择的目标或条目", 2)
		}
		if count == 0 {
			return result.Fail("REPLICA_NOTHING_TO_APPLY", "所有条目均保留/跳过或已经一致", 4)
		}
		// Reobserve every selected copy, registry, branch and baseline after all
		// package transfers. Candidates never mix observations from two states.
		current, err := m.compare(ctx, req.Request, t)
		if err != nil {
			return err
		}
		if current.Digest != report.Digest {
			return result.Fail("PLAN_STALE", "构建候选期间源或目标变化", 3)
		}
		out.Version = 2
		out.Digest = resolutionDigest(out)
		if !persist {
			return nil
		}
		return saveLocal(filepath.Join(m.resolutionDir(), "plans", out.ID+".json"), out)
	}()
	return out, err
}
func (m *Manager) loadResolution(id, digest string) (Resolution, error) {
	var p Resolution
	if !uuid.MatchString(id) {
		return p, result.Fail("INVALID_INPUT", "需要当前绑定的副本计划 UUID", 2)
	}
	b, err := workstore.ReadFile(filepath.Join(m.resolutionDir(), "plans", id+".json"))
	if err != nil {
		return p, err
	}
	if err = decode(b, &p); err != nil {
		return p, err
	}
	if (p.Version != 1 && p.Version != 2) || p.Kind != "replica-resolution" || p.ID != id || p.Owner != m.Source.Owner || p.Scope != m.Source.Scope || p.Digest != resolutionDigest(p) || len(p.Targets) == 0 || p.Version == 2 && p.Repair {
		return p, result.Fail("BACKUP_INVALID", "副本计划身份或摘要损坏", 5)
	}
	if digest == "" || digest != p.Digest {
		return p, result.Fail("PLAN_CONFIRMATION_REQUIRED", "需要副本计划的完整摘要", 4)
	}
	for _, decision := range p.Request.Decisions {
		if p.Version == 1 && (decision.Entry == source.SharedProfileID || decision.Entry == source.SharedRulesID || strings.HasPrefix(decision.Entry, source.SharedPluginPrefix) || strings.HasPrefix(decision.Entry, source.SharedMCPPrefix)) {
			return p, result.Fail("BACKUP_INVALID", "旧副本计划不能携带共享配置决策", 5)
		}
	}
	for _, target := range p.Targets {
		if p.Version == 2 && target.ConfigurationDigest == "" {
			return p, result.Fail("BACKUP_INVALID", "共享配置候选缺少目标配置层观察", 5)
		}
		if err = m.validateBinding(target.Copy); err != nil {
			return p, err
		}
		if string(data(workstore.Changes(target.Before, target.After))) != string(data(target.Changes)) {
			return p, result.Fail("BACKUP_INVALID", "副本候选变化清单损坏", 5)
		}
		if p.Repair && workstore.Digest(target.Before) != workstore.Digest(target.After) {
			return p, result.Fail("BACKUP_INVALID", "纯生成修复不能改写技能源", 5)
		}
	}
	if p.Repair && (len(p.Request.Decisions) != 0 || workstore.Digest(p.ControlBefore) != workstore.Digest(p.ControlAfter)) {
		return p, result.Fail("BACKUP_INVALID", "纯生成修复不能携带源决策或推进共同基线", 5)
	}
	return p, nil
}
func (m *Manager) revalidate(ctx context.Context, p Resolution) error {
	return m.revalidateWith(ctx, p, false)
}

func (m *Manager) revalidateWith(ctx context.Context, p Resolution, controlLocked bool) error {
	if p.Version == 2 {
		// Rebuild from the current byte-locked observations and explicit choices.
		// A recomputed record digest cannot adopt edited sources or baselines.
		resolve := m.resolve
		if controlLocked {
			resolve = m.resolveLocked
		}
		expected, err := resolve(ctx, p.Request, false)
		if err != nil {
			return err
		}
		if len(expected.Targets) == len(p.Targets) {
			for i, target := range p.Targets {
				if target.Copy.ID == expected.Targets[i].Copy.ID && target.ConfigurationDigest != expected.Targets[i].ConfigurationDigest {
					return result.Fail("PLAN_STALE", "目标本机配置或全局库在构建候选后变化", 3)
				}
			}
		}
		if string(data(expected.Source)) != string(data(p.Source)) || string(data(resolutionSourceIdentity(expected.Targets))) != string(data(resolutionSourceIdentity(p.Targets))) || workstore.Digest(expected.ControlBefore) != workstore.Digest(p.ControlBefore) || workstore.Digest(expected.ControlAfter) != workstore.Digest(p.ControlAfter) {
			return result.Fail("REPLICA_CANDIDATE_INVALID", "候选源或基线与显式决策不一致；重算摘要不能采纳手改内容", 5)
		}
		return nil
	}
	var current Report
	var err error
	if controlLocked {
		var tree workstore.Tree
		tree, err = m.Control.Capture(m.Control.Root)
		if err == nil {
			current, err = m.compare(ctx, p.Request.Request, tree)
		}
	} else {
		current, err = m.Compare(ctx, p.Request.Request)
	}
	if err != nil {
		return err
	}
	if current.Digest != p.Request.Comparison {
		return result.Fail("PLAN_STALE", "源、目标、分支、本机策略或共同基线在确认前变化", 3)
	}
	return nil
}

// Installation, permission and runtime observations are live gates, never part
// of the proposed source identity. Preparing the exact future package must not
// invalidate its source proposal. Native compilation and all execution routes
// still rebuild and enforce the complete current preflight independently.
// Keep every source byte, layer digest, skill check and non-MCP finding bound.
func resolutionSourceIdentity(targets []ProposedTarget) []ProposedTarget {
	out := append([]ProposedTarget{}, targets...)
	rank := map[string]int{"PASS": 0, "WARN": 1, "UNKNOWN": 2, "FAIL": 3}
	for i := range out {
		check := out[i].Preflight
		check.ProfileChecks = nil
		check.Status = "PASS"
		for _, finding := range out[i].Preflight.ProfileChecks {
			if finding.Component == "mcp" {
				continue
			}
			check.ProfileChecks = append(check.ProfileChecks, finding)
			if rank[finding.Status] > rank[check.Status] {
				check.Status = finding.Status
			}
		}
		for _, skill := range check.Skills {
			if rank[skill.Status] > rank[check.Status] {
				check.Status = skill.Status
			}
		}
		out[i].Preflight = check
	}
	return out
}

// These names are reserved to keep future publication distinct from rehearsal.
func resolutionIdentity(owner, scope, id string) string {
	return strings.Join([]string{"replica-rehearsal", owner, scope, id}, "/")
}
