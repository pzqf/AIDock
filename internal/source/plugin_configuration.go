package source

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/pluginconfig"
	"github.com/pzqf/AIDock/internal/pluginmanifest"
	"github.com/pzqf/AIDock/internal/profile"
	"github.com/pzqf/AIDock/internal/result"
)

type MCPConfiguration struct {
	Status           string `json:"status"`
	Code             string `json:"code,omitempty"`
	DeliveryObserved bool   `json:"delivery_observed"`
	pluginconfig.Compiled
}

// Use only the manifest from the same complete-artifact verification. Profile
// values cannot invent fields, a delivery channel, an entrypoint or authority.
func (m *Manager) compilePluginConfigurations(conf map[string]any, lock MCPLockState, report *profile.Report) (map[string]MCPConfiguration, []inventory.Finding) {
	out := map[string]MCPConfiguration{}
	checks := []inventory.Finding{}
	if report == nil {
		return out, checks
	}
	values, _ := report.Effective["config_overrides"].(map[string]any)
	known := map[string]bool{}
	for _, raw := range array(conf["servers"]) {
		known[raw.(map[string]any)["name"].(string)] = true
	}
	names := []string{}
	for name := range known {
		names = append(names, name)
	}
	sort.Strings(names)
	for name := range values {
		if !known[name] {
			checks = append(checks, inventory.Finding{Status: "UNKNOWN", Code: "PROFILE_CONFIGURATION_UNKNOWN_PLUGIN", Detail: "配置覆盖引用了未登记插件；不能应用或交付未知字段"})
		}
	}
	if len(values) > 64 {
		checks = append(checks, inventory.Finding{Status: "UNKNOWN", Code: "PROFILE_CONFIGURATION_INVALID", Detail: "插件配置数量超出支持范围"})
		return out, checks
	}
	home, _ := os.UserHomeDir()
	variables := map[string]string{"PROJECT": filepath.Dir(m.Store.Root), "HOME": home}
	for _, name := range names {
		value, present := values[name]
		var manifest *pluginmanifest.Manifest
		for _, r := range lock.Manifests {
			if r.Name == name && r.ManifestStatus == "PASS" && r.ArtifactStatus == "PASS" {
				manifest = r.Manifest
				break
			}
		}
		if manifest == nil {
			if present {
				out[name] = MCPConfiguration{Status: "FAIL", Code: "PLUGIN_CONFIGURATION_MANIFEST_REQUIRED"}
				checks = append(checks, inventory.Finding{Status: "UNKNOWN", Code: "PROFILE_CONFIGURATION_MANIFEST_REQUIRED", Subject: name, Detail: "插件配置需要当前锁定产物中已核验的清单；未交付值"})
			}
			continue
		}
		// A successful explicit config_remove must not be undone by applying
		// a schema fallback after the Profile merge. Later explicit replacements
		// can reinstate the field; merge-only ancestor events do not.
		omitted := []string{}
		for field := range manifest.ConfigSchema {
			path := "/config_overrides/" + strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1") + "/" + field
			removed := false
			for _, event := range report.Trace {
				if event.Path == path || strings.HasPrefix(path, event.Path+"/") {
					switch event.Action {
					case "remove":
						removed = true
					case "set", "replace":
						removed = false
					}
				}
			}
			if removed {
				omitted = append(omitted, field)
			}
		}
		compiled, err := pluginconfig.Compile(manifest.ConfigSchema, manifest.ConfigDelivery, value, present, variables, omitted...)
		if err != nil {
			code := "PLUGIN_CONFIGURATION_INVALID"
			var classified *result.Error
			if errors.As(err, &classified) {
				code = classified.Code
			}
			out[name] = MCPConfiguration{Status: "FAIL", Code: code}
			checks = append(checks, inventory.Finding{Status: "UNKNOWN", Code: "PROFILE_CONFIGURATION_INVALID", Subject: name, Detail: code + "：配置未匹配已核验插件的字段和交付合同，未使用配置值"})
			continue
		}
		if compiled.SHA256 != "" {
			out[name] = MCPConfiguration{Status: "PASS", Compiled: compiled}
			checks = append(checks, inventory.Finding{Status: "PASS", Code: "PLUGIN_CONFIGURATION_VALID", Subject: name, Detail: "有效字段、默认值和明确交付合同已核对；仅交付该插件，配置变化后须重新授权"})
		}
	}
	return out, checks
}
