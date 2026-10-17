package source

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/pluginconfig"
	"github.com/pzqf/AIDock/internal/pluginmanifest"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

const SharedPluginPrefix = "plugin-config/"

type SharedPluginConfiguration struct {
	Name           string   `json:"name"`
	ManifestSHA256 string   `json:"manifest_sha256"`
	ArtifactSHA256 string   `json:"artifact_sha256"`
	Fields         []string `json:"fields"`
}

var sharedFieldName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var sharedPluginName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
var sharedHash = regexp.MustCompile(`^[a-f0-9]{64}$`)

func sharedPointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

func validateSharedPluginConfiguration(v SharedConfiguration) error {
	bad := func() error {
		return result.Fail("REPLICA_CONFIGURATION_INVALID", "插件共享配置身份、合同或字段无效", 5)
	}
	p := v.Plugin
	if p == nil || !sharedPluginName.MatchString(p.Name) || v.ID != SharedPluginPrefix+p.Name || v.Name != "插件共享配置 · "+p.Name || !sharedHash.MatchString(p.ManifestSHA256) || !sharedHash.MatchString(p.ArtifactSHA256) || len(p.Fields) == 0 || len(p.Fields) > 64 || len(v.Rules) != 0 || v.Profile == nil || v.Digest != SharedConfigurationDigest(v) {
		return bad()
	}
	owned := map[string]bool{}
	previous := ""
	for _, field := range p.Fields {
		if !sharedFieldName.MatchString(field) || field <= previous {
			return bad()
		}
		owned[field], previous = true, field
	}
	if v.Blocked != "" {
		if v.Blocked != "PLUGIN_SHARED_VALUE_BLOCKED" && v.Blocked != "PLUGIN_SHARED_CONFIGURATION_INVALID" && v.Blocked != "PLUGIN_CONFIGURATION_SCHEMA_UNSUPPORTED" && v.Blocked != "PLUGIN_CONFIGURATION_SCHEMA_INVALID" {
			return bad()
		}
		if len(v.Profile) != 0 {
			return bad()
		}
		return nil
	}
	for key := range v.Profile {
		if key != "config_overrides" && key != "config_remove" {
			return bad()
		}
	}
	validation := cloneProfile(v.Profile)
	validation["schema_version"], validation["name"] = 2, "shared-plugin-validation"
	if _, err := config.Validate("profile", jsonBytes(validation)); err != nil {
		return bad()
	}
	if raw, exists := v.Profile["config_overrides"]; exists {
		values, ok := raw.(map[string]any)
		if !ok || len(values) != 1 {
			return bad()
		}
		fields, ok := values[p.Name].(map[string]any)
		if !ok {
			return bad()
		}
		for field := range fields {
			if !owned[field] {
				return bad()
			}
		}
	}
	seen := map[string]bool{}
	for _, raw := range array(v.Profile["config_remove"]) {
		pointer, ok := raw.(string)
		prefix := "/" + sharedPointer(p.Name) + "/"
		field := strings.TrimPrefix(pointer, prefix)
		if !ok || !strings.HasPrefix(pointer, prefix) || !owned[field] || seen[pointer] {
			return bad()
		}
		seen[pointer] = true
	}
	return nil
}

func (m *Manager) sharedPluginInputs(t workstore.Tree) (MCPLockState, map[string]string, error) {
	lock, err := m.mcpLockState(context.Background(), t)
	home, _ := os.UserHomeDir()
	return lock, map[string]string{"PROJECT": filepath.Dir(m.Store.Root), "HOME": home}, err
}

func sharedRemoved(profile map[string]any, name string, fields []string) []string {
	removed := []string{}
	prefix := "/" + sharedPointer(name)
	for _, field := range fields {
		for _, raw := range array(profile["config_remove"]) {
			pointer, _ := raw.(string)
			if pointer == prefix || pointer == prefix+"/"+field || strings.HasPrefix(pointer, prefix+"/"+field+"/") {
				removed = append(removed, field)
				break
			}
		}
	}
	return removed
}

func sharedManifestFields(r pluginmanifest.Report) []string {
	fields := []string{}
	if r.Manifest == nil || r.ManifestStatus != "PASS" || r.ArtifactStatus != "PASS" || r.Manifest.ConfigDelivery != pluginconfig.Delivery {
		return fields
	}
	for name, raw := range r.Manifest.ConfigSchema {
		if field, ok := raw.(map[string]any); ok && field["sync"] == "shared" && sharedFieldName.MatchString(name) {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	return fields
}

func (m *Manager) sharedPluginConfigurations(t workstore.Tree, profile map[string]any) ([]SharedConfiguration, []string, error) {
	out, notes := []SharedConfiguration{}, []string{}
	lock, variables, err := m.sharedPluginInputs(t)
	if err != nil {
		return out, notes, err
	}
	all, _ := profile["config_overrides"].(map[string]any)
	for _, r := range lock.Manifests {
		fields := sharedManifestFields(r)
		if len(fields) == 0 {
			continue
		}
		p := SharedPluginConfiguration{Name: r.Name, ManifestSHA256: r.ManifestSHA256, ArtifactSHA256: r.ArtifactSHA256, Fields: fields}
		v := SharedConfiguration{ID: SharedPluginPrefix + r.Name, Name: "插件共享配置 · " + r.Name, Profile: map[string]any{}, Plugin: &p}
		value, present := all[r.Name]
		projection, err := pluginconfig.ProjectShared(r.Manifest.ConfigSchema, r.Manifest.ConfigDelivery, value, present, variables, sharedRemoved(profile, r.Name, fields))
		if err != nil {
			v.Blocked = "PLUGIN_SHARED_CONFIGURATION_INVALID"
			if e, ok := err.(*result.Error); ok && e.Code == "PLUGIN_SHARED_VALUE_BLOCKED" {
				v.Blocked = e.Code
			}
			notes = append(notes, v.ID+"："+v.Blocked+"；整组阻断，未提供配置正文")
		} else {
			if len(projection.Values) > 0 {
				v.Profile["config_overrides"] = map[string]any{r.Name: projection.Values}
			}
			if len(projection.Remove) > 0 {
				removed := []any{}
				for _, field := range projection.Remove {
					removed = append(removed, "/"+sharedPointer(r.Name)+"/"+field)
				}
				v.Profile["config_remove"] = removed
			}
		}
		v.Digest = SharedConfigurationDigest(v)
		if err := ValidateSharedConfiguration(v); err != nil {
			return out, notes, err
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, notes, nil
}

func (m *Manager) draftSharedPluginConfiguration(t workstore.Tree, profile map[string]any, v SharedConfiguration) error {
	bad := func() error {
		return result.Fail("PLUGIN_SHARED_CONTRACT_MISMATCH", "插件共享配置要求目标自己已核验相同版本产物和明确字段合同；不复制安装或授权", 4)
	}
	if v.Blocked != "" {
		return result.Fail(v.Blocked, "插件共享配置被阻断，先协调本机值或疑似凭据；未采用源", 4)
	}
	lock, variables, err := m.sharedPluginInputs(t)
	if err != nil {
		return err
	}
	var manifest *pluginmanifest.Manifest
	for _, r := range lock.Manifests {
		if r.Name == v.Plugin.Name && r.ManifestSHA256 == v.Plugin.ManifestSHA256 && r.ArtifactSHA256 == v.Plugin.ArtifactSHA256 && string(jsonBytes(sharedManifestFields(r))) == string(jsonBytes(v.Plugin.Fields)) {
			manifest = r.Manifest
			break
		}
	}
	if manifest == nil {
		return bad()
	}
	name := v.Plugin.Name
	values, _ := v.Profile["config_overrides"].(map[string]any)
	value, present := values[name]
	projected, err := pluginconfig.ProjectShared(manifest.ConfigSchema, manifest.ConfigDelivery, value, present, variables, sharedRemoved(v.Profile, name, v.Plugin.Fields))
	if err != nil {
		return err
	}
	// Verify the entire incoming unit against the destination's own contract.
	// Rehashing a forged descriptor cannot turn local fields into shared fields.
	if string(jsonBytes(projected.Fields)) != string(jsonBytes(v.Plugin.Fields)) {
		return bad()
	}
	all, _ := profile["config_overrides"].(map[string]any)
	if all == nil {
		all = map[string]any{}
		profile["config_overrides"] = all
	}
	target, _ := all[name].(map[string]any)
	if target == nil {
		if _, ok := all[name]; ok {
			return bad()
		}
		target = map[string]any{}
	}
	owned := map[string]bool{}
	for _, field := range v.Plugin.Fields {
		owned[field] = true
		delete(target, field)
	}
	for field, value := range projected.Values {
		target[field] = value
	}
	if len(target) == 0 {
		delete(all, name)
	} else {
		all[name] = target
	}
	// A destination ancestor removal owns local fields too. Do not split or
	// weaken it implicitly just to accept a remote field.
	prefix := "/" + sharedPointer(name)
	removals := []any{}
	for _, raw := range array(profile["config_remove"]) {
		pointer, _ := raw.(string)
		if pointer == prefix {
			return result.Fail("PLUGIN_SHARED_LOCAL_REMOVAL_CONFLICT", "目标有整插件删除声明，需先在本机明确协调；共享选择不拆分本机声明", 4)
		}
		field := strings.TrimPrefix(pointer, prefix+"/")
		if !strings.HasPrefix(pointer, prefix+"/") || !owned[strings.Split(field, "/")[0]] {
			removals = append(removals, raw)
		}
	}
	for _, field := range projected.Remove {
		removals = append(removals, prefix+"/"+field)
	}
	if len(removals) == 0 {
		delete(profile, "config_remove")
	} else {
		profile["config_remove"] = removals
	}
	return nil
}
