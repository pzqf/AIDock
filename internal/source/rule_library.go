package source

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/rules"
	"github.com/pzqf/AIDock/internal/workstore"
)

const ruleLibraryFile = "rules/index.yaml"

func cloneProfile(v map[string]any) map[string]any {
	var out map[string]any
	_ = json.Unmarshal(jsonBytes(v), &out)
	return out
}

func profileWithRules(v map[string]any, texts []string) map[string]any {
	out := cloneProfile(v)
	existing := array(v["rules"])
	same := len(existing) == len(texts)
	if same {
		for i, text := range texts {
			if existing[i] != text {
				same = false
				break
			}
		}
	}
	if same {
		return out
	}
	items := []any{}
	for _, text := range texts {
		items = append(items, text)
	}
	out["rules"] = items
	return out
}

type RuleFile struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Body    string `json:"body"`
}

type ruleReference struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}
type ruleIndex struct {
	Version int             `json:"schema_version"`
	Rules   []ruleReference `json:"rules"`
}

func ruleFilePath(id string) string             { return "rules/" + id + ".md" }
func ruleLibraryDigest(t workstore.Tree) string { return workstore.Digest(subtree(t, "rules")) }

// Only indexed UUID files are owned. Other files are reported and preserved,
// and neither their names nor their contents imply a place in the rule order.
func (m *Manager) ruleLibrary(t workstore.Tree) ([]RuleFile, []string, error) {
	items, unmanaged := []RuleFile{}, []string{}
	index := ruleIndex{Version: 1, Rules: []ruleReference{}}
	if entry, ok := t.Entries[ruleLibraryFile]; ok {
		data, err := m.Store.Data(entry)
		if err != nil {
			return nil, nil, err
		}
		value, err := config.Validate("rule-library", data)
		if err != nil {
			return nil, nil, result.Fail("RULE_LIBRARY_INVALID", err.Error(), 5)
		}
		if err = json.Unmarshal(jsonBytes(value), &index); err != nil {
			return nil, nil, err
		}
	}
	owned := map[string]bool{ruleLibraryFile: true}
	for _, ref := range index.Rules {
		p := ruleFilePath(ref.ID)
		if owned[p] || strings.TrimSpace(ref.Name) == "" {
			return nil, nil, result.Fail("RULE_LIBRARY_INVALID", "规则身份重复或名称为空", 5)
		}
		owned[p] = true
		entry, ok := t.Entries[p]
		if !ok || entry.Dir {
			return nil, nil, result.Fail("RULE_FILE_MISSING", "规则文件缺失："+p, 5)
		}
		data, err := m.Store.Data(entry)
		if err != nil {
			return nil, nil, err
		}
		if _, err = rules.Compile(nil, []string{string(data)}, ""); err != nil {
			return nil, nil, err
		}
		items = append(items, RuleFile{ID: ref.ID, Name: ref.Name, Enabled: ref.Enabled, Body: string(data)})
	}
	for p, entry := range t.Entries {
		if strings.HasPrefix(p, "rules/") && !entry.Dir && !owned[p] {
			unmanaged = append(unmanaged, p)
		}
	}
	sort.Strings(unmanaged)
	return items, unmanaged, nil
}

func (m *Manager) sourceRules(t workstore.Tree, profile map[string]any) ([]string, error) {
	texts := []string{}
	for _, value := range array(profile["rules"]) {
		texts = append(texts, value.(string))
	}
	items, _, err := m.ruleLibrary(t)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.Enabled {
			texts = append(texts, item.Body)
		}
	}
	if _, err = rules.Compile(nil, texts, ""); err != nil {
		return nil, err
	}
	return texts, nil
}

func (m *Manager) draftRuleLibrary(before workstore.Tree, after *workstore.Tree, expected string, values []RuleFile) error {
	if expected == "" || expected != ruleLibraryDigest(before) {
		return result.Fail("PLAN_STALE", "规则库在编辑后变化；草稿保留，请核对后重新读取", 3)
	}
	old, _, err := m.ruleLibrary(before)
	if err != nil {
		return err
	}
	owned := map[string]bool{}
	for _, v := range old {
		owned[ruleFilePath(v.ID)] = true
	}
	index := ruleIndex{Version: 1, Rules: []ruleReference{}}
	seen := map[string]bool{}
	for _, value := range values {
		if value.ID == "" {
			value.ID = workstore.ID()
		}
		if !uuid.MatchString(value.ID) || seen[value.ID] || strings.TrimSpace(value.Name) == "" {
			return result.Fail("RULE_LIBRARY_INVALID", "规则需要唯一 UUID 和非空名称", 5)
		}
		seen[value.ID] = true
		p := ruleFilePath(value.ID)
		entry, exists := before.Entries[p]
		if exists && (!owned[p] || entry.Dir) {
			return result.Fail("RULE_FILE_UNOWNED", "不会接管未登记文件："+p, 3)
		}
		if _, err = rules.Compile(nil, []string{value.Body}, ""); err != nil {
			return err
		}
		mode := uint32(0644)
		if exists {
			mode = entry.Mode
		}
		if err = m.Store.PutFile(after, p, []byte(value.Body), mode); err != nil {
			return err
		}
		index.Rules = append(index.Rules, ruleReference{ID: value.ID, Name: value.Name, Enabled: value.Enabled})
	}
	data := jsonBytes(index)
	if _, err = config.Validate("rule-library", data); err != nil {
		return result.Fail("RULE_LIBRARY_INVALID", fmt.Sprintf("规则索引不合法：%v", err), 5)
	}
	for _, value := range old {
		if !seen[value.ID] {
			delete(after.Entries, ruleFilePath(value.ID))
		}
	}
	// Do not create an index merely by saving an unchanged empty library.
	if _, exists := before.Entries[ruleLibraryFile]; !exists && len(values) == 0 {
		return nil
	}
	mode := uint32(0644)
	if entry, ok := before.Entries[ruleLibraryFile]; ok {
		mode = entry.Mode
	}
	return m.Store.PutFile(after, ruleLibraryFile, data, mode)
}
