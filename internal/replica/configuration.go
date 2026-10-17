package replica

import (
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/source"
)

type ConfigurationDifference struct {
	ID             string                      `json:"id"`
	Name           string                      `json:"name"`
	Status         string                      `json:"status"`
	Reason         string                      `json:"reason"`
	Source         *source.SharedConfiguration `json:"source,omitempty"`
	Target         *source.SharedConfiguration `json:"target,omitempty"`
	Base           *source.SharedConfiguration `json:"base,omitempty"`
	Fields         []string                    `json:"changed_fields"`
	LocalOverrides []string                    `json:"local_overrides,omitempty"`
}

func configurationEqual(a, b *source.SharedConfiguration) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Digest == b.Digest
}

func configurationStatus(s, t, b *source.SharedConfiguration, hasBase bool) (string, string) {
	if configurationEqual(s, t) {
		return "SAME", "两端登记的共享字段及有序规则正文一致；不代表本机有效配置相同"
	}
	if !hasBase {
		return "BASELINE_UNKNOWN", "尚无该共享配置的共同确认基线，需要明确选边"
	}
	if configurationEqual(s, b) {
		return "TARGET_CHANGED", "仅目标共享配置发生变化；保留目标需要明确选择"
	}
	if configurationEqual(t, b) {
		return "SOURCE_CHANGED", "仅源共享配置发生变化；采用源包含字段移除及规则顺序/删除"
	}
	return "CONFLICT", "两端共享配置均偏离共同基线，需要明确选边或跳过"
}

func configurationExcluded(b Binding, configs ...*source.SharedConfiguration) bool {
	paths := []string{"profile.yaml"}
	for _, v := range configs {
		if v != nil && v.MCP != nil {
			paths = []string{"mcp.yaml", "plugins.lock"}
			break
		}
	}
	for _, v := range configs {
		if v != nil && v.ID == source.SharedRulesID {
			// Rules are one ordered unit. Any exclusion below rules protects the
			// whole unit rather than manufacturing a different merged order.
			paths = append(paths, "rules")
		}
	}
	for _, path := range paths {
		for _, exclude := range b.Excludes {
			exclude = strings.TrimSuffix(exclude, "/")
			if path == exclude || strings.HasPrefix(path, exclude+"/") || strings.HasPrefix(exclude, path+"/") {
				return true
			}
		}
	}
	return false
}

func configurationFields(s, t *source.SharedConfiguration) []string {
	out := []string{}
	if s == nil || t == nil {
		return out
	}
	if s.MCP != nil || t.MCP != nil {
		if s.MCP == nil || t.MCP == nil {
			return []string{"mcp.yaml / plugins.lock"}
		}
		keys := map[string]bool{}
		for key := range s.MCP.Definition {
			keys[key] = true
		}
		for key := range t.MCP.Definition {
			keys[key] = true
		}
		for key := range keys {
			if string(data(s.MCP.Definition[key])) != string(data(t.MCP.Definition[key])) {
				out = append(out, "mcp.yaml/"+s.MCP.Name+"/"+key)
			}
		}
		if string(data(s.MCP.Lock)) != string(data(t.MCP.Lock)) {
			out = append(out, "plugins.lock/"+s.MCP.Name)
		}
		sort.Strings(out)
		return out
	}
	keys := map[string]bool{}
	for key := range s.Profile {
		keys[key] = true
	}
	for key := range t.Profile {
		keys[key] = true
	}
	for key := range keys {
		if string(data(s.Profile[key])) != string(data(t.Profile[key])) {
			out = append(out, "profile.yaml/"+key)
		}
	}
	if string(data(s.Rules)) != string(data(t.Rules)) {
		out = append(out, "rules/index.yaml（顺序、启停、增删与正文）")
	}
	sort.Strings(out)
	return out
}

func configurationDifferences(from Binding, target Binding, s, t source.SharedCatalog, base Baseline) []ConfigurationDifference {
	sm, tm := map[string]*source.SharedConfiguration{}, map[string]*source.SharedConfiguration{}
	for _, v := range s.Configurations {
		v := v
		sm[v.ID] = &v
	}
	for _, v := range t.Configurations {
		v := v
		tm[v.ID] = &v
	}
	out := []ConfigurationDifference{}
	ids := []string{source.SharedProfileID, source.SharedRulesID}
	extra := map[string]bool{}
	for id := range sm {
		extra[id] = true
	}
	for id := range tm {
		extra[id] = true
	}
	for id := range base.Configurations {
		extra[id] = true
	}
	delete(extra, source.SharedProfileID)
	delete(extra, source.SharedRulesID)
	ordered := []string{}
	for id := range extra {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	ids = append(ids, ordered...)
	for _, id := range ids {
		sourceValue, targetValue := sm[id], tm[id]
		var previous *source.SharedConfiguration
		if v, ok := base.Configurations[id]; ok {
			previous = &v
		}
		status, reason := configurationStatus(sourceValue, targetValue, previous, previous != nil)
		if strings.HasPrefix(id, source.SharedMCPPrefix) {
			if sourceValue == nil {
				status, reason = "MCP_DEFINITION_UNAVAILABLE", "源没有可核验的 MCP 共享定义；缺失不解释为删除，目标定义与安装保留"
			} else if sourceValue.Blocked != "" || targetValue != nil && targetValue.Blocked != "" {
				status, reason = "MCP_DEFINITION_BLOCKED", "MCP 声明或精确锁未通过可携带性与完整产物核验；先在对应副本协调，整组不传播"
			} else if status == "SOURCE_CHANGED" {
				reason = "仅源 MCP 共享定义或精确锁变化；采用源仍需目标自己的产物、安装、权限和运行时核验，旧授权不随行"
			}
		}
		if strings.HasPrefix(id, source.SharedPluginPrefix) {
			if status == "SOURCE_CHANGED" {
				reason = "仅源插件共享字段变化；采用源包含值替换与明确移除，未声明字段仍保留"
			}
			if sourceValue == nil || targetValue == nil {
				status, reason = "CONFIGURATION_CONTRACT_MISMATCH", "两端均须自己登记并核验相同插件字段合同；缺失不视为删除，不传播安装和授权"
			} else if sourceValue.Blocked != "" || targetValue.Blocked != "" {
				status, reason = "CONFIGURATION_VALUE_BLOCKED", "声明共享的字段混入本机值、疑似凭据或不支持的配置；整组阻断，先在对应副本协调"
			} else if string(data(sourceValue.Plugin)) != string(data(targetValue.Plugin)) {
				status, reason = "CONFIGURATION_CONTRACT_MISMATCH", "两端插件清单、精确产物或共享字段声明不同；先分别核验固定版本，不借配置同步更换安装或权限"
			}
		}
		if configurationExcluded(from, sourceValue, previous) || configurationExcluded(target, sourceValue, targetValue, previous) {
			status, reason = "LOCAL_EXCLUDED", "本机排除保护此共享配置组；不产生部分规则顺序或接管文件"
		}
		name := id
		if sourceValue != nil {
			name = sourceValue.Name
		}
		masked := []string{}
		if sourceValue != nil && sourceValue.Plugin != nil && t.Preflight.ProfileReport != nil {
			for _, field := range sourceValue.Plugin.Fields {
				path := "/config_overrides/" + sourceValue.Plugin.Name + "/" + field
				for _, event := range t.Preflight.ProfileReport.Trace {
					if event.Layer == "local" && (event.Path == path && (event.Action == "set" || event.Action == "replace" || event.Action == "remove") || event.Action == "remove" && strings.HasPrefix(path, event.Path+"/")) {
						masked = append(masked, field)
						break
					}
				}
			}
		}
		out = append(out, ConfigurationDifference{ID: id, Name: name, Status: status, Reason: reason, Source: sourceValue, Target: targetValue, Base: previous, Fields: configurationFields(sourceValue, targetValue), LocalOverrides: masked})
	}
	return out
}
