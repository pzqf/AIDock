package source

import (
	"encoding/json"
	"sort"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

const MCPRemovalsFile = "mcp-removals.yaml"

// This is a deliberate source deletion, never evidence of an uninstall or a
// permission change. Only the removed identity's digests travel between copies.
type MCPRemoval struct {
	Name             string `json:"name"`
	DefinitionSHA256 string `json:"definition_sha256"`
	LockSHA256       string `json:"lock_sha256"`
}

type mcpRemovalDocument struct {
	Version  int          `json:"schema_version"`
	Removals []MCPRemoval `json:"removals"`
}

func mcpRemovalsDigest(t workstore.Tree) string {
	if e, ok := t.Entries[MCPRemovalsFile]; ok {
		return e.Hash
	}
	return workstore.Hash(nil)
}

func validMCPRemoval(r MCPRemoval) bool {
	return sharedPluginName.MatchString(r.Name) && sharedHash.MatchString(r.DefinitionSHA256) && sharedHash.MatchString(r.LockSHA256)
}

func (m *Manager) mcpRemovals(t workstore.Tree) ([]MCPRemoval, error) {
	out := []MCPRemoval{}
	entry, present := t.Entries[MCPRemovalsFile]
	if !present {
		return out, nil
	}
	bad := func() ([]MCPRemoval, error) {
		return nil, result.Fail("MCP_REMOVALS_INVALID", "MCP 明确删除记录无效；请从源回退点恢复，不把损坏或缺失解释为删除", 5)
	}
	body, err := m.Store.Data(entry)
	if err != nil {
		return bad()
	}
	v, err := config.Validate("mcp-removals", body)
	if err != nil {
		return bad()
	}
	var doc mcpRemovalDocument
	if json.Unmarshal(jsonBytes(v), &doc) != nil {
		return bad()
	}
	seen := map[string]bool{}
	for _, r := range doc.Removals {
		if !validMCPRemoval(r) || seen[r.Name] {
			return bad()
		}
		seen[r.Name] = true
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Manager) putMCPRemovals(after *workstore.Tree, records map[string]MCPRemoval) error {
	if len(records) == 0 {
		delete(after.Entries, MCPRemovalsFile)
		return nil
	}
	doc := mcpRemovalDocument{Version: 1, Removals: []MCPRemoval{}}
	for _, r := range records {
		if !validMCPRemoval(r) {
			return result.Fail("MCP_REMOVALS_INVALID", "明确删除记录的原定义或版本锁摘要无效", 5)
		}
		doc.Removals = append(doc.Removals, r)
	}
	sort.Slice(doc.Removals, func(i, j int) bool { return doc.Removals[i].Name < doc.Removals[j].Name })
	body := jsonBytes(doc)
	if _, err := config.Validate("mcp-removals", body); err != nil {
		return result.Fail("MCP_REMOVALS_INVALID", "明确删除记录超过范围或格式限制", 5)
	}
	mode := uint32(0644)
	if old, ok := after.Entries[MCPRemovalsFile]; ok {
		mode = old.Mode
		if original, err := m.Store.Data(old); err == nil && string(original) == string(body) {
			return nil
		}
	}
	return m.Store.PutFile(after, MCPRemovalsFile, body, mode)
}

func mcpServersByName(conf map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, raw := range array(conf["servers"]) {
		s := raw.(map[string]any)
		out[s["name"].(string)] = s
	}
	return out
}

func (m *Manager) draftMCPRemovals(before workstore.Tree, after *workstore.Tree, req MCPEdit, next map[string]any) error {
	if req.ExpectedRemovals != "" && req.ExpectedRemovals != mcpRemovalsDigest(before) || req.ExpectedLock != "" && req.ExpectedLock != lockDigest(before) {
		return result.Fail("PLAN_STALE", "MCP 删除记录或安装锁已变化，请重新读取后确认", 3)
	}
	existing, err := m.mcpRemovals(before)
	if err != nil {
		return err
	}
	records := map[string]MCPRemoval{}
	for _, r := range existing {
		records[r.Name] = r
	}
	active := mcpServersByName(next)
	changed := false
	for name := range active {
		if _, exists := records[name]; exists {
			if req.ExpectedRemovals == "" {
				return result.Fail("MCP_REMOVAL_CONFIRMATION_REQUIRED", "重新引入已明确删除的同名定义，需要刚读取的删除记录摘要", 4)
			}
			delete(records, name)
			changed = true
		}
	}
	if len(req.SharedRemovals) > 0 {
		if m.Owner != "global" && m.Copy == "" {
			return result.Fail("COPY_BINDING_REQUIRED", "项目明确删除需要当前副本绑定，才能核对本机覆盖引用；请追加 --copy 或从已登记项目进入", 4)
		}
		if req.ExpectedRemovals == "" || req.ExpectedLock == "" {
			return result.Fail("MCP_REMOVAL_CONFIRMATION_REQUIRED", "传播明确删除需要刚读取的删除记录和安装锁摘要", 4)
		}
		previous, err := m.mcpConfig(before)
		if err != nil {
			return result.Fail("MCP_REMOVAL_SOURCE_INVALID", "先修复原 MCP 定义，再明确记录要同步的删除", 5)
		}
		old := mcpServersByName(previous)
		lock := pluginlock.Lock{Version: 1, Plugins: []pluginlock.Plugin{}}
		entry, lockPresent := before.Entries["plugins.lock"]
		if lockPresent {
			body, err := m.Store.Data(entry)
			if err != nil {
				return err
			}
			lock, err = pluginlock.Parse(body)
			if err != nil {
				return result.Fail("MCP_LOCK_INVALID", "先修复原安装锁，再明确移除其条目；未记录删除", 5)
			}
		}
		seen := map[string]bool{}
		for _, name := range req.SharedRemovals {
			if !sharedPluginName.MatchString(name) || seen[name] || old[name] == nil || active[name] != nil {
				return result.Fail("INVALID_INPUT", "明确删除须是本次移除的原有名称，不能重复、虚构或同时重新引入", 2)
			}
			seen[name] = true
			r := MCPRemoval{Name: name, DefinitionSHA256: pluginlock.Definition(old[name]), LockSHA256: workstore.Hash(nil)}
			for _, p := range lock.Plugins {
				if p.Name == name {
					p.Artifact.Path = sharedArtifactPath(p)
					r.LockSHA256 = workstore.Hash(jsonBytes(p))
				}
			}
			records[name] = r
			changed = true
		}
		if lockPresent {
			kept := []pluginlock.Plugin{}
			for _, p := range lock.Plugins {
				if !seen[p.Name] {
					kept = append(kept, p)
				}
			}
			if len(kept) != len(lock.Plugins) {
				lock.Plugins = kept
				if err = m.Store.PutFile(after, "plugins.lock", jsonBytes(lock), entry.Mode); err != nil {
					return err
				}
			}
		}
	}
	if changed {
		return m.putMCPRemovals(after, records)
	}
	return nil
}

// Rebuild only the allowed deletion effects from the actual before/after
// definitions. A recomputed local plan digest cannot delete unrelated locks,
// forge a deletion identity or widen this entry point to another source file.
func (m *Manager) validateMCPDefinitionPlan(p workstore.Plan) error {
	bad := func() error {
		return result.Fail("MCP_DEFINITION_INVALID", "MCP 保存只能修改定义及其明确删除对应的锁和删除记录", 5)
	}
	next, err := m.mcpConfig(p.After)
	if err != nil {
		return err
	}
	entry, ok := p.After.Entries["mcp.yaml"]
	if !ok {
		return bad()
	}
	mode := uint32(0644)
	if old, ok := p.Before.Entries["mcp.yaml"]; ok {
		mode = old.Mode
	}
	if err = m.validateMCPRemoval(p.Before, next); err != nil {
		return err
	}
	previous, err := m.mcpRemovals(p.Before)
	if err != nil {
		return err
	}
	current, err := m.mcpRemovals(p.After)
	if err != nil {
		return err
	}
	old := map[string]MCPRemoval{}
	for _, r := range previous {
		old[r.Name] = r
	}
	req := MCPEdit{ExpectedRemovals: mcpRemovalsDigest(p.Before), ExpectedLock: lockDigest(p.Before)}
	for _, r := range current {
		if v, ok := old[r.Name]; !ok || v != r {
			req.SharedRemovals = append(req.SharedRemovals, r.Name)
		}
	}
	expected := workstore.Clone(p.Before)
	body, err := m.Store.Data(entry)
	if err != nil {
		return err
	}
	if err = m.Store.PutFile(&expected, "mcp.yaml", body, mode); err != nil {
		return err
	}
	if err = m.draftMCPRemovals(p.Before, &expected, req, next); err != nil {
		return err
	}
	if workstore.Digest(expected) != workstore.Digest(p.After) {
		return bad()
	}
	return nil
}
