// Package profile implements the deterministic configuration merge in design
// 19.6. Resolution is pure: it does not load files, run tools or grant access.
package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/result"
)

type Catalog struct {
	Skills  map[string][]string `json:"skills"`
	Plugins []string            `json:"plugins"`
	MCP     []string            `json:"mcp"`
}
type Input struct {
	Defaults    map[string]any   `json:"defaults,omitempty"`
	Profiles    []map[string]any `json:"profiles,omitempty"`
	Project     map[string]any   `json:"project,omitempty"`
	Local       map[string]any   `json:"local,omitempty"`
	Session     map[string]any   `json:"session,omitempty"`
	Catalog     Catalog          `json:"catalog"`
	MCPDefaults map[string]bool  `json:"mcp_defaults,omitempty"`
	// PluginMCP is an explicit registry mapping, never a same-name guess. Only
	// aliases actually mentioned in a Profile participate in both namespaces.
	PluginMCP map[string]string `json:"plugin_mcp,omitempty"`
}
type Layer struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Digest string `json:"digest"`
}
type Event struct {
	Path     string `json:"path"`
	Layer    string `json:"layer"`
	Action   string `json:"action"`
	Value    any    `json:"value"`
	Previous string `json:"previous_layer,omitempty"`
}
type Report struct {
	Version   int            `json:"schema_version"`
	Layers    []Layer        `json:"layers"`
	Effective map[string]any `json:"effective"`
	Trace     []Event        `json:"trace"`
	Digest    string         `json:"digest"`
	Boundary  string         `json:"boundary"`
}

func hash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func invalid(code, detail string) error { return result.Fail(code, detail, 5) }
func validate(v map[string]any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, invalid("PROFILE_INVALID", err.Error())
	}
	x, err := config.Validate("profile", b)
	if err != nil {
		code := "PROFILE_INVALID"
		if strings.Contains(err.Error(), "AMBIGUOUS_OVERRIDE") {
			code = "AMBIGUOUS_OVERRIDE"
		}
		return nil, invalid(code, err.Error())
	}
	return x.(map[string]any), nil
}
func keys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func array(v any) []any { a, _ := v.([]any); return a }
func object(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
func pointer(s string) string          { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }
func child(parent, name string) string { return parent + "/" + pointer(name) }
func overlaps(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// Resolve validates every supplied layer, then applies global defaults,
// selected extends ancestors, project, local and session in that order. An
// ancestor shared by defaults and the selected profile is applied only once.
func Resolve(in Input) (Report, error) {
	r := Report{Version: 1, Layers: []Layer{}, Effective: map[string]any{}, Trace: []Event{},
		Boundary: "按显式身份与修订清单进行确定性合并；清单声明不代替完整包校验。不加载工具、不执行技能，不代表授权、原生应用或会话生效。临时会话层不持久保存。"}
	named := map[string]map[string]any{}
	for _, raw := range in.Profiles {
		v, err := validate(raw)
		if err != nil {
			return r, err
		}
		name := v["name"].(string)
		if named[name] != nil {
			return r, invalid("PROFILE_NAME_AMBIGUOUS", "重复 Profile 名称："+name)
		}
		named[name] = v
	}
	known := map[string]map[string]bool{"skills": {}, "plugins": {}, "mcp": {}}
	for id := range in.Catalog.Skills {
		known["skills"][id] = true
	}
	for _, id := range in.Catalog.Plugins {
		known["plugins"][id] = true
	}
	for _, id := range in.Catalog.MCP {
		known["mcp"][id] = true
	}
	for plugin, mcp := range in.PluginMCP {
		if !known["plugins"][plugin] || !known["mcp"][mcp] {
			return r, invalid("PROFILE_UNKNOWN_ID", "插件类型映射必须引用已登记的插件与 MCP 身份")
		}
	}
	switches := map[string]map[string]bool{"skills": {}, "plugins": {}, "mcp": {}}
	pins := map[string]any{}
	values := map[string]any{}
	rules := []any{}
	provenance := map[string]string{}
	add := func(layer, p, action string, value any) {
		// Trace values describe the value at this step, not a mutable map that
		// a later layer can change through aliasing.
		bytes, _ := json.Marshal(value)
		var captured any
		_ = json.Unmarshal(bytes, &captured)
		r.Trace = append(r.Trace, Event{Path: p, Layer: layer, Action: action, Value: captured, Previous: provenance[p]})
		provenance[p] = layer
	}
	// Definition defaults preserve the original enabled declaration. Explicit
	// Profile layers can override it, and omission inherits the previous layer.
	if len(in.MCPDefaults) > 0 {
		r.Layers = append(r.Layers, Layer{ID: "mcp-definition", Name: "MCP 定义默认声明", Digest: hash(in.MCPDefaults)})
		for _, id := range keys(in.MCPDefaults) {
			if !known["mcp"][id] {
				return r, invalid("PROFILE_UNKNOWN_ID", "MCP 默认声明引用了未登记身份："+id)
			}
			enabled := in.MCPDefaults[id]
			switches["mcp"][id] = enabled
			action := "disable"
			if enabled {
				action = "enable"
			}
			add("mcp-definition", child("/mcp", id), action, enabled)
		}
	}
	apply := func(id string, v map[string]any) error {
		// The generic plugin and its typed MCP switch are one asset. Opposite
		// choices in one layer are ambiguous; a later layer can override either.
		for _, plugin := range keys(in.PluginMCP) {
			mcp := in.PluginMCP[plugin]
			choice := func(kind, name string) (bool, bool) {
				for _, action := range []string{"enable", "disable"} {
					for _, raw := range array(object(v[kind])[action]) {
						if raw == name {
							return action == "enable", true
						}
					}
				}
				return false, false
			}
			p, hasP := choice("plugins", plugin)
			m, hasM := choice("mcp", mcp)
			if hasP && hasM && p != m {
				return invalid("AMBIGUOUS_OVERRIDE", "同层插件与 MCP 开关相互矛盾："+plugin)
			}
		}
		r.Layers = append(r.Layers, Layer{ID: id, Name: v["name"].(string), Digest: hash(v)})
		r.Effective["name"] = v["name"]
		if description, ok := v["description"]; ok {
			r.Effective["description"] = description
		}
		for _, kind := range []string{"plugins", "skills", "mcp"} {
			section := object(v[kind])
			for _, action := range []string{"enable", "disable"} {
				unique := map[string]bool{}
				for _, value := range array(section[action]) {
					unique[value.(string)] = true
				}
				for _, key := range keys(unique) {
					if !known[kind][key] {
						return invalid("PROFILE_UNKNOWN_ID", kind+" 引用了未登记身份："+key)
					}
					switches[kind][key] = action == "enable"
					add(id, child("/"+kind, key), action, action == "enable")
					if target := in.PluginMCP[key]; kind == "plugins" && target != "" {
						switches["mcp"][target] = action == "enable"
						add(id, child("/mcp", target), action, action == "enable")
					}
					if kind == "mcp" {
						for _, alias := range keys(in.PluginMCP) {
							if _, active := switches["plugins"][alias]; active && in.PluginMCP[alias] == key {
								switches["plugins"][alias] = action == "enable"
								add(id, child("/plugins", alias), action, action == "enable")
							}
						}
					}
				}
			}
		}
		for _, key := range keys(object(object(v["skills"])["pins"])) {
			rev := object(object(v["skills"])["pins"])[key].(string)
			if !known["skills"][key] {
				return invalid("PROFILE_UNKNOWN_ID", "pin 引用了未登记技能："+key)
			}
			found := false
			for _, current := range in.Catalog.Skills[key] {
				if current == rev {
					found = true
				}
			}
			if !found {
				return invalid("PROFILE_REVISION_UNAVAILABLE", "不能取得完整固定版本："+key+" @ "+rev)
			}
			pins[key] = rev
			add(id, child("/skills/pins", key), "pin", rev)
		}
		setPaths := []string{}
		var merge func(map[string]any, map[string]any, string)
		merge = func(dst, src map[string]any, prefix string) {
			for _, key := range keys(src) {
				p, value := child(prefix, key), src[key]
				if obj, ok := value.(map[string]any); ok {
					base, exists := dst[key].(map[string]any)
					if !exists {
						base = map[string]any{}
						dst[key] = base
						setPaths = append(setPaths, p)
						add(id, "/config_overrides"+p, "replace", map[string]any{})
					}
					if len(obj) == 0 {
						setPaths = append(setPaths, p)
						add(id, "/config_overrides"+p, "merge", map[string]any{})
					}
					merge(base, obj, p)
				} else {
					dst[key] = value
					setPaths = append(setPaths, p)
					add(id, "/config_overrides"+p, "set", value)
				}
			}
		}
		merge(values, object(v["config_overrides"]), "")
		removes := []string{}
		for _, raw := range array(v["config_remove"]) {
			p := raw.(string)
			parts, err := parsePointer(p)
			if err != nil {
				return err
			}
			for _, set := range setPaths {
				if overlaps(set, p) {
					return invalid("AMBIGUOUS_OVERRIDE", "同层设置与删除路径重叠："+set+" / "+p)
				}
			}
			for _, old := range removes {
				if overlaps(old, p) {
					return invalid("AMBIGUOUS_OVERRIDE", "同层删除路径重叠："+old+" / "+p)
				}
			}
			if _, err = remove(values, parts); err != nil {
				return invalid("PROFILE_REMOVE_MISSING", p+"："+err.Error())
			}
			removes = append(removes, p)
			add(id, "/config_overrides"+p, "remove", nil)
		}
		if v["rules_override"] == true {
			rules = []any{}
			add(id, "/rules", "replace", []any{})
		}
		for _, rule := range array(v["rules"]) {
			rules = append(rules, rule)
			add(id, "/rules/"+strconv.Itoa(len(rules)-1), "append", rule)
		}
		return nil
	}
	applied, visiting := map[string]bool{}, map[string]bool{}
	var ancestor func(string, int) error
	ancestor = func(name string, depth int) error {
		if visiting[name] || depth > 64 {
			return invalid("PROFILE_EXTENDS_CYCLE", "Profile 继承环或超过 64 层："+name)
		}
		if applied[name] {
			return nil
		}
		v := named[name]
		if v == nil {
			return invalid("PROFILE_EXTENDS_MISSING", "缺少继承 Profile："+name)
		}
		visiting[name] = true
		if parent, _ := v["extends"].(string); parent != "" {
			if err := ancestor(parent, depth+1); err != nil {
				return err
			}
		}
		if err := apply("extends:"+name, v); err != nil {
			return err
		}
		visiting[name] = false
		applied[name] = true
		return nil
	}
	for _, layer := range []struct {
		id  string
		raw map[string]any
	}{{"defaults", in.Defaults}, {"project", in.Project}, {"local", in.Local}, {"session", in.Session}} {
		if layer.raw == nil {
			continue
		}
		v, err := validate(layer.raw)
		if err != nil {
			return r, err
		}
		if parent, _ := v["extends"].(string); parent != "" {
			if layer.id == "local" || layer.id == "session" {
				return r, invalid("PROFILE_LAYER_INVALID", "本机和临时层不能引入新的 extends 链")
			}
			if err = ancestor(parent, 0); err != nil {
				return r, err
			}
		}
		if err = apply(layer.id, v); err != nil {
			return r, err
		}
	}
	for _, kind := range []string{"plugins", "skills", "mcp"} {
		on, off := []any{}, []any{}
		for _, id := range keys(switches[kind]) {
			if kind == "skills" && pins[id] == nil {
				return r, invalid("PROFILE_PIN_REQUIRED", "技能开关缺少可取得的精确 pin："+id)
			}
			if switches[kind][id] {
				on = append(on, id)
			} else {
				off = append(off, id)
			}
		}
		r.Effective[kind] = map[string]any{"enable": on, "disable": off}
	}
	if len(r.Layers) == 0 {
		return r, invalid("PROFILE_EMPTY", "至少需要一个明确的 Profile 层")
	}
	object(r.Effective["skills"])["pins"] = pins
	r.Effective["schema_version"] = 2
	r.Effective["config_overrides"] = values
	r.Effective["rules"] = rules
	r.Effective["rules_override"] = true
	r.Digest = hash(r)
	return r, nil
}

func parsePointer(p string) ([]string, error) {
	if !strings.HasPrefix(p, "/") {
		return nil, invalid("PROFILE_POINTER_INVALID", "config_remove 需要非根 JSON Pointer："+p)
	}
	out := strings.Split(p[1:], "/")
	for i, part := range out {
		for j := 0; j < len(part); j++ {
			if part[j] == '~' {
				if j+1 >= len(part) || part[j+1] != '0' && part[j+1] != '1' {
					return nil, invalid("PROFILE_POINTER_INVALID", "非法 JSON Pointer 转义："+p)
				}
				j++
			}
		}
		out[i] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
	}
	return out, nil
}
func remove(current any, parts []string) (any, error) {
	key := parts[0]
	switch v := current.(type) {
	case map[string]any:
		value, ok := v[key]
		if !ok {
			return nil, fmt.Errorf("字段不存在")
		}
		if len(parts) == 1 {
			delete(v, key)
			return v, nil
		}
		next, err := remove(value, parts[1:])
		if err != nil {
			return nil, err
		}
		v[key] = next
		return v, nil
	case []any:
		i, err := strconv.Atoi(key)
		if err != nil || i < 0 || i >= len(v) || strconv.Itoa(i) != key {
			return nil, fmt.Errorf("数组索引不存在或不规范")
		}
		if len(parts) == 1 {
			return append(v[:i], v[i+1:]...), nil
		}
		next, err := remove(v[i], parts[1:])
		if err != nil {
			return nil, err
		}
		v[i] = next
		return v, nil
	default:
		return nil, fmt.Errorf("路径穿过非容器值")
	}
}
