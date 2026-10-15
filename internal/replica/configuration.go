package replica

import (
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/source"
)

type ConfigurationDifference struct {
	ID     string                      `json:"id"`
	Name   string                      `json:"name"`
	Status string                      `json:"status"`
	Reason string                      `json:"reason"`
	Source *source.SharedConfiguration `json:"source,omitempty"`
	Target *source.SharedConfiguration `json:"target,omitempty"`
	Base   *source.SharedConfiguration `json:"base,omitempty"`
	Fields []string                    `json:"changed_fields"`
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
	for _, id := range []string{source.SharedProfileID, source.SharedRulesID} {
		sourceValue, targetValue := sm[id], tm[id]
		var previous *source.SharedConfiguration
		if v, ok := base.Configurations[id]; ok {
			previous = &v
		}
		status, reason := configurationStatus(sourceValue, targetValue, previous, previous != nil)
		if configurationExcluded(from, sourceValue, previous) || configurationExcluded(target, sourceValue, targetValue, previous) {
			status, reason = "LOCAL_EXCLUDED", "本机排除保护此共享配置组；不产生部分规则顺序或接管文件"
		}
		name := id
		if sourceValue != nil {
			name = sourceValue.Name
		}
		out = append(out, ConfigurationDifference{ID: id, Name: name, Status: status, Reason: reason, Source: sourceValue, Target: targetValue, Base: previous, Fields: configurationFields(sourceValue, targetValue)})
	}
	return out
}
