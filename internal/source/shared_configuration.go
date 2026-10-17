package source

import (
	"context"
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/rules"
	"github.com/pzqf/AIDock/internal/workstore"
)

// Shared configuration is projected by field ownership, never by copying a
// Profile or rules directory. Skills retain their independent decisions. Open
// config objects remain target-local unless a verified plugin manifest gives
// individual fields an explicit portable ownership contract.
type SharedConfiguration struct {
	ID      string                     `json:"id"`
	Name    string                     `json:"name"`
	Profile map[string]any             `json:"profile"`
	Rules   []RuleFile                 `json:"rules,omitempty"`
	Digest  string                     `json:"digest"`
	Plugin  *SharedPluginConfiguration `json:"plugin,omitempty"`
	Blocked string                     `json:"blocked_code,omitempty"`
	MCP     *SharedMCPDefinition       `json:"mcp,omitempty"`
}

const SharedProfileID = "profile/shared"
const SharedRulesID = "rules/shared"

func sharedProfileFields(id string) []string {
	if id == SharedProfileID {
		return []string{"name", "description", "extends", "plugins", "mcp"}
	}
	if id == SharedRulesID {
		return []string{"rules", "rules_override"}
	}
	return nil
}

func SharedConfigurationDigest(v SharedConfiguration) string {
	v.Digest = ""
	return workstore.Hash(jsonBytes(v))
}

func ValidateSharedConfiguration(v SharedConfiguration) error {
	if strings.HasPrefix(v.ID, SharedMCPPrefix) {
		return validateSharedMCPConfiguration(v)
	}
	if strings.HasPrefix(v.ID, SharedPluginPrefix) {
		return validateSharedPluginConfiguration(v)
	}
	bad := func() error {
		return result.Fail("REPLICA_CONFIGURATION_INVALID", "共享配置身份、字段所有权或摘要无效", 5)
	}
	fields := sharedProfileFields(v.ID)
	if fields == nil || v.Profile == nil || v.Plugin != nil || v.MCP != nil || v.Blocked != "" || len(v.Rules) > 500 || v.Digest != SharedConfigurationDigest(v) {
		return bad()
	}
	expectedName := "共享 Profile"
	if v.ID == SharedRulesID {
		expectedName = "共享规则与有序规则库"
	} else if len(v.Rules) != 0 {
		return bad()
	}
	if v.Name != expectedName {
		return bad()
	}
	allowed := map[string]bool{}
	for _, field := range fields {
		allowed[field] = true
	}
	profile := map[string]any{"schema_version": 2, "name": "shared-validation"}
	for key, value := range v.Profile {
		if !allowed[key] {
			return bad()
		}
		profile[key] = value
	}
	if v.ID == SharedProfileID && v.Profile["name"] == nil {
		return bad()
	}
	if _, err := config.Validate("profile", jsonBytes(profile)); err != nil {
		return bad()
	}
	seen := map[string]bool{}
	texts := []string{}
	for _, text := range array(v.Profile["rules"]) {
		texts = append(texts, text.(string))
	}
	for _, rule := range v.Rules {
		if !uuid.MatchString(rule.ID) || seen[rule.ID] || strings.TrimSpace(rule.Name) == "" {
			return bad()
		}
		seen[rule.ID] = true
		// Also validates names and each disabled body, using the same index schema
		// and compiler as ordinary rule-library edits.
		index := ruleIndex{Version: 1, Rules: []ruleReference{{ID: rule.ID, Name: rule.Name, Enabled: rule.Enabled}}}
		if _, err := config.Validate("rule-library", jsonBytes(index)); err != nil {
			return bad()
		}
		if _, err := rules.Compile(nil, []string{rule.Body}, ""); err != nil {
			return bad()
		}
		if rule.Enabled {
			texts = append(texts, rule.Body)
		}
	}
	if _, err := rules.Compile(nil, texts, ""); err != nil {
		return bad()
	}
	return nil
}

func (m *Manager) sharedConfigurations(t workstore.Tree, profile map[string]any) ([]SharedConfiguration, []string, error) {
	library, unmanaged, err := m.ruleLibrary(t)
	if err != nil {
		return nil, nil, err
	}
	notes := []string{}
	for _, key := range []string{"config_overrides", "config_remove"} {
		if v, ok := profile[key]; ok && string(jsonBytes(v)) != "{}\n" && string(jsonBytes(v)) != "[]\n" {
			notes = append(notes, "profile.yaml/"+key+"：只有已核验清单明确声明的可携带字段参与独立插件配置组，其余字段及本机覆盖保留")
		}
	}
	for _, p := range unmanaged {
		notes = append(notes, p+"：未登记规则文件，保留且不参与同步")
	}
	out := []SharedConfiguration{}
	cloned := cloneProfile(profile)
	for _, id := range []string{SharedProfileID, SharedRulesID} {
		v := SharedConfiguration{ID: id, Name: "共享 Profile", Profile: map[string]any{}}
		if id == SharedRulesID {
			v.Name, v.Rules = "共享规则与有序规则库", library
		}
		for _, key := range sharedProfileFields(id) {
			if value, ok := cloned[key]; ok {
				v.Profile[key] = value
			}
		}
		v.Digest = SharedConfigurationDigest(v)
		if err = ValidateSharedConfiguration(v); err != nil {
			return nil, nil, err
		}
		out = append(out, v)
	}
	lock, err := m.mcpLockState(context.Background(), t)
	if err != nil {
		return nil, nil, err
	}
	plugins, pluginNotes, err := m.sharedPluginConfigurations(t, profile, lock)
	if err != nil {
		return nil, nil, err
	}
	out = append(out, plugins...)
	notes = append(notes, pluginNotes...)
	mcps, mcpNotes, err := m.sharedMCPConfigurations(t, lock)
	if err != nil {
		return nil, nil, err
	}
	out = append(out, mcps...)
	notes = append(notes, mcpNotes...)
	sort.Strings(notes)
	return out, notes, nil
}

func (m *Manager) draftSharedConfigurations(before workstore.Tree, after *workstore.Tree, profile map[string]any, selected []SharedConfiguration) error {
	// Definitions and locks establish the future package/schema context before
	// any separately selected configuration fields are validated.
	if err := m.draftSharedMCPConfigurations(after, selected); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, v := range selected {
		if seen[v.ID] {
			return result.Fail("REPLICA_CONFIGURATION_INVALID", "共享配置决策重复", 5)
		}
		seen[v.ID] = true
		if err := ValidateSharedConfiguration(v); err != nil {
			return err
		}
		if v.MCP != nil {
			continue
		}
		if v.Plugin != nil {
			if err := m.draftSharedPluginConfiguration(*after, profile, v); err != nil {
				return err
			}
			continue
		}
		for _, key := range sharedProfileFields(v.ID) {
			delete(profile, key)
		}
		for key, value := range cloneProfile(v.Profile) {
			profile[key] = value
		}
		if v.ID == SharedRulesID {
			old, _, err := m.ruleLibrary(before)
			if err != nil {
				return err
			}
			// nil and [] both represent an explicitly empty ordered library.
			if len(old) == 0 && len(v.Rules) == 0 || string(jsonBytes(old)) == string(jsonBytes(v.Rules)) {
				continue
			}
			if err = m.draftRuleLibrary(before, after, ruleLibraryDigest(before), v.Rules); err != nil {
				return err
			}
		}
	}
	return nil
}
