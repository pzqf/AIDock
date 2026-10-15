package replica

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/source"
	"github.com/pzqf/AIDock/internal/workstore"
)

type Request struct {
	From    string   `json:"from"`
	Targets []string `json:"targets"`
}
type Difference struct {
	ID      string              `json:"id"`
	Name    string              `json:"name"`
	Status  string              `json:"status"`
	Reason  string              `json:"reason"`
	Source  *source.SharedEntry `json:"source,omitempty"`
	Target  *source.SharedEntry `json:"target,omitempty"`
	Base    *source.SharedEntry `json:"base,omitempty"`
	Changes []workstore.Change  `json:"package_changes"`
	Fields  []string            `json:"changed_fields"`
}
type Target struct {
	Copy           Binding      `json:"copy"`
	Snapshot       string       `json:"snapshot"`
	BaselineDigest string       `json:"baseline_digest"`
	Status         string       `json:"status"`
	Reason         string       `json:"reason,omitempty"`
	Entries        []Difference `json:"entries"`
}
type Report struct {
	Owner          string   `json:"owner"`
	Scope          string   `json:"scope_id"`
	From           Binding  `json:"from"`
	SourceSnapshot string   `json:"source_snapshot"`
	RegistryDigest string   `json:"registry_digest"`
	Targets        []Target `json:"targets"`
	Digest         string   `json:"digest"`
	Boundary       string   `json:"boundary"`
}

func pairPath(a, b string) string {
	ids := []string{a, b}
	sort.Strings(ids)
	return "baselines/" + ids[0] + "/" + ids[1] + ".json"
}
func (m *Manager) baseline(t workstore.Tree, a, b string) (Baseline, string, error) {
	ids := []string{a, b}
	sort.Strings(ids)
	base := Baseline{Version: 1, Owner: m.Source.Owner, Scope: m.Source.Scope, Copies: ids, Entries: map[string]source.SharedEntry{}}
	e, ok := t.Entries[pairPath(a, b)]
	if !ok {
		return base, "MISSING", nil
	}
	bytes, err := m.Control.Data(e)
	if err != nil {
		return base, "", err
	}
	if err = decode(bytes, &base); err != nil || base.Version != 1 || base.Owner != m.Source.Owner || base.Scope != m.Source.Scope || len(base.Copies) != 2 || base.Copies[0] != ids[0] || base.Copies[1] != ids[1] || base.Entries == nil {
		return base, "", result.Fail("BASELINE_INVALID", "共同基线身份无效", 5)
	}
	for id, item := range base.Entries {
		if !uuid.MatchString(id) || item.ID != id || item.Digest != source.SharedDigest(item) {
			return base, "", result.Fail("BASELINE_INVALID", "共同基线条目摘要无效", 5)
		}
		for _, f := range item.Tree.Entries {
			if !f.Dir {
				if _, err = m.Control.Data(f); err != nil {
					return base, "", result.Fail("BASELINE_BACKUP_MISSING", "共同基线的完整包备份缺失", 5)
				}
			}
		}
	}
	return base, e.Hash, nil
}
func equal(a, b *source.SharedEntry) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Digest == b.Digest
}
func entryMap(c source.SharedCatalog) map[string]*source.SharedEntry {
	out := map[string]*source.SharedEntry{}
	for _, v := range c.Entries {
		copy := v
		out[v.ID] = &copy
	}
	return out
}
func excluded(b Binding, e *source.SharedEntry) bool {
	if e == nil {
		return false
	}
	p := "skills/" + e.Name
	for _, v := range b.Excludes {
		v = strings.TrimSuffix(v, "/")
		if p == v || strings.HasPrefix(p, v+"/") || strings.HasPrefix(v, p+"/") || e.Path == v || strings.HasPrefix(e.Path, v+"/") || strings.HasPrefix(v, e.Path+"/") {
			return true
		}
	}
	return false
}

func changedFields(s, t *source.SharedEntry) []string {
	fields := []string{}
	if s == nil || t == nil {
		return fields
	}
	if s.Enabled != t.Enabled {
		fields = append(fields, "enabled")
	}
	if s.Retired != t.Retired {
		fields = append(fields, "retired")
	}
	if s.Layer != t.Layer {
		fields = append(fields, "layer")
	}
	var sm, tm map[string]any
	_ = json.Unmarshal(data(s.Metadata), &sm)
	_ = json.Unmarshal(data(t.Metadata), &tm)
	for key, v := range sm {
		if string(data(v)) != string(data(tm[key])) {
			fields = append(fields, key)
		}
	}
	for key := range tm {
		if _, ok := sm[key]; !ok {
			fields = append(fields, key)
		}
	}
	sort.Strings(fields)
	return fields
}
func classify(s, t, b *source.SharedEntry, hasBase bool) (string, string) {
	if equal(s, t) {
		return "SAME", "两端共享包、元数据、启停与删除状态一致"
	}
	if !hasBase {
		return "BASELINE_UNKNOWN", "没有该条目的共同确认基线，需逐项选边；不能按时间推断"
	}
	if equal(s, b) {
		return "TARGET_CHANGED", "仅目标与共同基线不同；保留目标修改，发布需要显式选择"
	}
	if equal(t, b) {
		if s == nil || s.Retired {
			return "SOURCE_DELETED", "源移除或退役；需要明确删除传播决策"
		}
		return "SOURCE_CHANGED", "仅源与共同基线不同，可预览选择源版本"
	}
	return "CONFLICT", "两端均与共同基线不同，需显式选边或跳过"
}
func (m *Manager) compare(ctx context.Context, req Request, t workstore.Tree) (Report, error) {
	r, err := m.registry(t)
	if err != nil {
		return Report{}, err
	}
	bindings := map[string]Binding{}
	for _, b := range r.Copies {
		bindings[b.ID] = b
	}
	from, ok := bindings[req.From]
	if !ok || len(req.Targets) == 0 {
		return Report{}, result.Fail("REPLICA_SELECTION_REQUIRED", "请选择已登记的源与至少一个目标副本", 2)
	}
	seen := map[string]bool{req.From: true}
	for _, id := range req.Targets {
		if _, ok := bindings[id]; !ok || seen[id] {
			return Report{}, result.Fail("INVALID_INPUT", "目标副本不存在、重复或等于源", 2)
		}
		seen[id] = true
	}
	c, err := m.observe(from)
	if err != nil {
		return Report{}, err
	}
	out := Report{Owner: r.Owner, Scope: r.Scope, From: from, SourceSnapshot: c.Snapshot, RegistryDigest: workstore.Digest(t), Targets: []Target{}, Boundary: "只比较已登记项目技能源的完整包、元数据、开关和删除状态；项目文件、草稿、原生生成物、凭据及本机路径均不发布。缺共同基线不自动选边；外部生成器和整批发布/恢复未开放。"}
	sourceMap := entryMap(c)
	targets := append([]string{}, req.Targets...)
	sort.Strings(targets)
	for _, id := range targets {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		b := bindings[id]
		target := Target{Copy: b, Status: "READY", Entries: []Difference{}}
		observed, err := m.observe(b)
		if err != nil {
			target.Status, target.Reason = "UNAVAILABLE", err.Error()
			out.Targets = append(out.Targets, target)
			continue
		}
		target.Snapshot = observed.Snapshot
		base, baseDigest, err := m.baseline(t, from.ID, b.ID)
		if err != nil {
			return out, err
		}
		target.BaselineDigest = baseDigest
		actualFrom, actualTarget := branch(from.Root), branch(b.Root)
		if actualFrom != from.Branch || actualTarget != b.Branch {
			target.Status, target.Reason = "BINDING_STALE", "登记后的分支观察变化，需要重新核对绑定"
		} else if actualFrom != "UNKNOWN" && actualTarget != "UNKNOWN" && actualFrom != actualTarget {
			target.Status, target.Reason = "BRANCH_MISMATCH", "两端分支不同，不能直接发布"
		}
		targetMap := entryMap(observed)
		ids := map[string]bool{}
		for k := range sourceMap {
			ids[k] = true
		}
		for k := range targetMap {
			ids[k] = true
		}
		for k := range base.Entries {
			ids[k] = true
		}
		keys := []string{}
		for k := range ids {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			s, dst := sourceMap[key], targetMap[key]
			v, exists := base.Entries[key]
			var previous *source.SharedEntry
			if exists {
				previous = &v
			}
			status, reason := classify(s, dst, previous, exists)
			if excluded(from, s) || excluded(from, previous) || excluded(b, dst) || excluded(b, s) || excluded(b, previous) {
				status, reason = "LOCAL_EXCLUDED", "本机排除策略覆盖此完整包，保留本机状态"
			}
			name := key
			if s != nil {
				name = s.Name
			} else if dst != nil {
				name = dst.Name
			} else if previous != nil {
				name = previous.Name
			}
			if s != nil && s.Layer == "external" || dst != nil && dst.Layer == "external" {
				status, reason = "EXTERNAL_REGENERATION_REQUIRED", "外部权威源仅比较；尚缺逐目标暂存生成/恢复清单适配"
			}
			empty := workstore.Tree{Entries: map[string]workstore.Entry{}}
			st, dt := empty, empty
			if s != nil {
				st = s.Tree
			}
			if dst != nil {
				dt = dst.Tree
			}
			target.Entries = append(target.Entries, Difference{ID: key, Name: name, Status: status, Reason: reason, Source: s, Target: dst, Base: previous, Changes: workstore.Changes(dt, st), Fields: changedFields(s, dst)})
		}
		out.Targets = append(out.Targets, target)
	}
	out.Digest = workstore.Hash(data(out))
	return out, nil
}
func (m *Manager) Compare(ctx context.Context, req Request) (Report, error) {
	var out Report
	err := m.Control.Locked(func() error {
		if err := m.Control.Pending(); err != nil {
			return err
		}
		t, err := m.Control.Capture(m.Control.Root)
		if err != nil {
			return err
		}
		out, err = m.compare(ctx, req, t)
		return err
	})
	return out, err
}
func (m *Manager) BaselinePlan(ctx context.Context, req Request) (workstore.Plan, error) {
	var p workstore.Plan
	err := m.Control.Locked(func() error {
		if err := m.Control.Pending(); err != nil {
			return err
		}
		t, err := m.Control.Capture(m.Control.Root)
		if err != nil {
			return err
		}
		report, err := m.compare(ctx, req, t)
		if err != nil {
			return err
		}
		after := workstore.Clone(t)
		inputs := map[string]string{"from": req.From, "comparison": report.Digest}
		count := 0
		for _, target := range report.Targets {
			if target.Status != "READY" {
				return result.Fail("REPLICA_BINDING_BLOCKED", target.Reason, 4)
			}
			inputs["target/"+target.Copy.ID] = target.Snapshot
			base, _, err := m.baseline(t, req.From, target.Copy.ID)
			if err != nil {
				return err
			}
			for _, item := range target.Entries {
				if item.Status != "SAME" || item.Source == nil {
					continue
				}
				// Preserve an existing baseline when both sides independently converged.
				// Advancing it would silently accept changes; this endpoint initializes
				// missing per-entry baselines only, with an explicit confirmation.
				if _, ok := base.Entries[item.ID]; ok {
					continue
				}
				copy := *item.Source
				base.Entries[item.ID] = copy
				count++
				observer, err := m.copySource(report.From)
				if err != nil {
					return err
				}
				scratch := workstore.Tree{Exists: true, Entries: map[string]workstore.Entry{}}
				for path, file := range copy.Tree.Entries {
					if file.Dir {
						scratch.Entries[path] = file
						continue
					}
					b, err := observer.Store.Data(file)
					if err != nil {
						return err
					}
					if err = m.Control.PutFile(&scratch, path, b, file.Mode); err != nil {
						return err
					}
				}
			}
			if len(base.Entries) > 0 {
				if err = m.Control.PutFile(&after, pairPath(req.From, target.Copy.ID), data(base), 0600); err != nil {
					return err
				}
			}
		}
		if count == 0 {
			return result.Fail("BASELINE_NOTHING_TO_CONFIRM", "没有缺基线且两端一致的条目；差异不会自动设为基线", 4)
		}
		p, err = m.Control.PreviewBound("replica-baseline", t, after, inputs)
		return err
	})
	return p, err
}
func (m *Manager) validateBaselinePlan(p workstore.Plan) error {
	req := Request{From: p.Inputs["from"], Targets: []string{}}
	for key := range p.Inputs {
		if strings.HasPrefix(key, "target/") {
			req.Targets = append(req.Targets, strings.TrimPrefix(key, "target/"))
		}
	}
	current, err := m.compare(context.Background(), req, p.Before)
	if err != nil {
		return err
	}
	if current.Digest != p.Inputs["comparison"] {
		return result.Fail("PLAN_STALE", "源、目标、基线、本机排除或分支在确认前变化", 3)
	}
	return nil
}
