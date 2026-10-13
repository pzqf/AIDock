package config

import (
	"fmt"
	"path"
	"strings"
	"time"
)

// These checks only validate owned metadata. Dependency resolution, package
// byte integrity, layer composition and runtime support remain separate gates.
func validateSemantics(kind string, value any) error {
	m, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if kind == "skill-lifecycle" {
		seen := map[string]bool{}
		for _, value := range m["retirements"].([]any) {
			r := value.(map[string]any)
			id := r["id"].(string)
			if seen[id] || strings.TrimSpace(r["reason"].(string)) == "" {
				return fmt.Errorf("退役记录 id 重复或原因为空")
			}
			if _, err := time.Parse(time.RFC3339, r["retired_at"].(string)); err != nil {
				return fmt.Errorf("无效退役时间: %w", err)
			}
			seen[id] = true
		}
	}
	if kind == "bridge" {
		seen := map[string]bool{}
		for _, value := range m["bridges"].([]any) {
			b := value.(map[string]any)
			id := strings.ToLower(b["id"].(string))
			if seen[id] {
				return fmt.Errorf("重复外部源登记 id")
			}
			seen[id] = true
			for _, field := range []string{"scope", "copy", "root"} {
				if strings.TrimSpace(b[field].(string)) == "" {
					return fmt.Errorf("外部源 %s 不能为空", field)
				}
			}
			if err := packageRelative(b["source_path"].(string)); err != nil {
				return err
			}
			packages, _ := b["packages"].([]any)
			for i, p := range packages {
				if err := packageRelative(p.(string)); err != nil {
					return err
				}
				for _, earlier := range packages[:i] {
					a, c := strings.ToLower(earlier.(string)), strings.ToLower(p.(string))
					if a == c || strings.HasPrefix(a, c+"/") || strings.HasPrefix(c, a+"/") {
						return fmt.Errorf("外部登记包路径重叠或大小写重复")
					}
				}
			}
			if b["approved_revision"] == nil && (b["approved_at"] != nil || b["approved_by"] != nil) {
				return fmt.Errorf("批准信息需要 approved_revision")
			}
			if stamp, ok := b["approved_at"].(string); ok {
				if _, err := time.Parse(time.RFC3339, stamp); err != nil {
					return err
				}
			}
		}
	}
	if kind == "profile" || kind == "profile-v1" {
		for _, key := range []string{"skills", "plugins", "mcp"} {
			if switches, ok := m[key].(map[string]any); ok {
				enabled := map[string]bool{}
				if ids, ok := switches["enable"].([]any); ok {
					for _, id := range ids {
						enabled[id.(string)] = true
					}
				}
				if ids, ok := switches["disable"].([]any); ok {
					for _, id := range ids {
						if enabled[id.(string)] {
							return fmt.Errorf("AMBIGUOUS_OVERRIDE: %s 中同一 id 同时 enable/disable", key)
						}
					}
				}
			}
		}
	}
	if kind != "skills" {
		return nil
	}
	ids := map[string]bool{}
	for _, value := range m["skills"].([]any) {
		s := value.(map[string]any)
		id := s["id"].(string)
		if ids[id] {
			return fmt.Errorf("同一 owner/scope 内重复技能 id")
		}
		ids[id] = true
		source := s["source"].(map[string]any)
		if (s["revision_format"] == nil) != (s["package_digest"] == nil) {
			return fmt.Errorf("语义修订必须同时登记 revision_format/package_digest")
		}
		if err := packageRelative(source["path"].(string)); err != nil {
			return err
		}
		if source["mode"] == "plugin" && (source["plugin_id"] == nil || source["lock_digest"] == nil) {
			return fmt.Errorf("插件技能必须固定 plugin_id/lock_digest")
		}
		if (s["base_source"] == nil) != (s["base_revision"] == nil) {
			return fmt.Errorf("覆盖必须同时登记 base_source/base_revision")
		}
		for _, pair := range [][2]string{{"requirements", "scripts"}, {"compatibility", "project_files"}, {"dependencies", "project_files"}} {
			section, _ := s[pair[0]].(map[string]any)
			items, _ := section[pair[1]].([]any)
			for _, item := range items {
				name, _ := item.(string)
				if pair[0] == "dependencies" {
					name = item.(map[string]any)["path"].(string)
				}
				if err := packageRelative(name); err != nil {
					return fmt.Errorf("%s.%s: %w", pair[0], pair[1], err)
				}
			}
		}
		seen := map[string]bool{}
		hasDocument := false
		for _, value := range s["files"].([]any) {
			file := value.(map[string]any)
			name := file["path"].(string)
			if err := packageRelative(name); err != nil {
				return err
			}
			key := strings.ToLower(name)
			if seen[key] {
				return fmt.Errorf("重复或大小写冲突的包路径")
			}
			seen[key] = true
			if name == "SKILL.md" {
				hasDocument = true
			}
		}
		if !hasDocument {
			return fmt.Errorf("技能包清单必须包含 SKILL.md")
		}
	}
	return nil
}

func packageRelative(name string) error {
	if name == "" || name == "." || path.IsAbs(name) || strings.ContainsAny(name, `\:<>"|?*`) || path.Clean(name) != name {
		return fmt.Errorf("技能包路径不是规范相对路径")
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." || part == "." || strings.TrimRight(part, ". ") != part {
			return fmt.Errorf("技能包路径逃逸或包含非法名称")
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		switch base {
		case "CON", "PRN", "AUX", "NUL":
			return fmt.Errorf("技能包包含 Windows 设备名")
		}
		if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9' {
			return fmt.Errorf("技能包包含 Windows 设备名")
		}
	}
	return nil
}
