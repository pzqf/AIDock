// Package pluginmanifest inspects the manifest of a byte-verified prepared
// artifact. It does not register an installation, generate an installed SBOM,
// execute a scanner, or award a trust level.
package pluginmanifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/workstore"
)

type Dependency struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
	Version   string `json:"version"`
}

type Manifest struct {
	SchemaVersion int            `json:"schema_version"`
	Name          string         `json:"name"`
	Type          string         `json:"type"`
	Version       string         `json:"version"`
	Source        map[string]any `json:"source"`
	Description   string         `json:"description,omitempty"`
	Permissions   map[string]any `json:"permissions"`
	Dependencies  *[]Dependency  `json:"dependencies,omitempty"`
}

type Report struct {
	Name                  string              `json:"name"`
	Status                string              `json:"status"`
	ArtifactSHA256        string              `json:"artifact_sha256"`
	ArtifactStatus        string              `json:"artifact_status"`
	ManifestSHA256        string              `json:"manifest_sha256,omitempty"`
	Document              string              `json:"document,omitempty"`
	Manifest              *Manifest           `json:"manifest,omitempty"`
	DefinitionPermissions map[string]any      `json:"definition_permissions,omitempty"`
	ManifestStatus        string              `json:"manifest_status"`
	PermissionStatus      string              `json:"permission_status"`
	DependencyStatus      string              `json:"dependency_status"`
	InstallationState     string              `json:"installation_state"`
	SBOMStatus            string              `json:"sbom_status"`
	Checks                []inventory.Finding `json:"checks"`
	ExecutionReady        bool                `json:"execution_ready"`
}

var exact = regexp.MustCompile(`^(v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?|[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)
var npmName = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)
var genericName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]*$`)
var pypiName = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$`)
var pypiSeparators = regexp.MustCompile(`[-_.]+`)

func bytesOf(v any) []byte { b, _ := json.Marshal(v); return b }

// Parse keeps dependency omission distinct from an explicit empty list. Older
// manifests remain readable but cannot claim to declare all direct dependencies.
func Parse(data []byte) (Manifest, error) {
	var out Manifest
	if len(data) > 1<<20 {
		return out, fmt.Errorf("plugin.yaml 超过 1 MB")
	}
	v, err := config.Validate("plugin", data)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(bytesOf(v), &out); err != nil {
		return out, err
	}
	if strings.TrimSpace(out.Name) != out.Name || out.Name == "" || !exact.MatchString(out.Version) {
		return out, fmt.Errorf("manifest 名称不能为空或含首尾空白，版本必须精确固定")
	}
	if out.Source["type"] != "local" && out.Source["version"] != out.Version {
		return out, fmt.Errorf("manifest 来源版本须与插件版本一致")
	}
	if out.Dependencies != nil {
		seen := map[string]bool{}
		for _, d := range *out.Dependencies {
			pattern, key := genericName, strings.ToLower(d.Name)
			switch d.Ecosystem {
			case "npm":
				pattern = npmName
			case "pypi":
				pattern, key = pypiName, pypiSeparators.ReplaceAllString(key, "-")
			}
			key = d.Ecosystem + ":" + key
			if !pattern.MatchString(d.Name) || strings.Contains(d.Name, "..") || !exact.MatchString(d.Version) || seen[key] {
				return out, fmt.Errorf("依赖名称/固定版本无效或重复: %s/%s", d.Ecosystem, d.Name)
			}
			seen[key] = true
		}
	}
	return out, nil
}

func (r *Report) add(status, code, detail string) {
	r.Checks = append(r.Checks, inventory.Finding{Status: status, Code: code, Detail: detail})
	if status == "FAIL" || r.Status != "FAIL" && status == "UNKNOWN" {
		r.Status = status
	}
}

// Inspect consumes documents from exactly the lock verification read, rejects
// detached/forged observations, and never opens a manifest path a second time.
func Inspect(p pluginlock.Plugin, server map[string]any, check pluginlock.Check, observed pluginlock.Observation) Report {
	r := Report{Name: p.Name, Status: "PASS", ArtifactSHA256: p.Artifact.SHA256, ManifestStatus: "UNKNOWN", PermissionStatus: "UNKNOWN", DependencyStatus: "UNKNOWN", InstallationState: "PREPARED", SBOMStatus: "UNKNOWN", Checks: []inventory.Finding{}}
	r.ArtifactStatus = check.ArtifactStatus
	if server != nil {
		r.DefinitionPermissions, _ = server["permissions"].(map[string]any)
	}
	if check.Name != p.Name || check.ArtifactSHA256 != p.Artifact.SHA256 || check.ArtifactStatus != "PASS" || pluginlock.Digest(observed.Entries) != p.Artifact.SHA256 {
		status := "UNKNOWN"
		if check.ArtifactStatus == "FAIL" {
			status = "FAIL"
		}
		r.add(status, "MCP_MANIFEST_ARTIFACT_UNVERIFIED", "完整产物尚未匹配安装锁；清单内容不作为有效证据")
		return r
	}
	for _, f := range check.Checks {
		if f.Code == "MCP_LOCK_PLATFORM_MISMATCH" || f.Code == "MCP_LOCK_DEFINITION_MISMATCH" || f.Code == "MCP_RUNTIME_NOT_EXECUTABLE" {
			r.add(f.Status, f.Code, f.Detail)
		}
	}
	if check.DependencyStatus == "FAIL" {
		r.DependencyStatus = "FAIL"
		r.add("FAIL", "MCP_MANIFEST_RESOLUTION_INVALID", "完整依赖解析锁与本地产物不一致；直接依赖声明不能替代此核验")
	}
	if _, err := pluginlock.Parse(bytesOf(pluginlock.Lock{Version: 1, Plugins: []pluginlock.Plugin{p}})); err != nil {
		r.ManifestStatus = "FAIL"
		r.add("FAIL", "MCP_MANIFEST_LOCK_INVALID", "安装锁对象无效: "+err.Error())
		return r
	}
	entries := map[string]pluginlock.Entry{}
	for _, e := range observed.Entries {
		entries[e.Path] = e
	}
	for path, data := range observed.Documents {
		e, ok := entries[path]
		if !ok || e.Directory || e.SHA256 != workstore.Hash(data) || e.Bytes != int64(len(data)) {
			r.ManifestStatus = "FAIL"
			r.add("FAIL", "MCP_MANIFEST_BYTES_UNBOUND", "清单/依赖元数据原字节与已核验产物不符: "+path)
			return r
		}
	}
	data, ok := observed.Documents["plugin.yaml"]
	if !ok {
		r.add("UNKNOWN", "MCP_MANIFEST_MISSING", "产物根目录缺少 plugin.yaml；尚不能核对插件清单、权限与直接依赖")
		return r
	}
	r.ManifestSHA256 = workstore.Hash(data)
	r.Document = string(data)
	manifest, err := Parse(data)
	if err != nil {
		r.ManifestStatus = "FAIL"
		r.add("FAIL", "MCP_MANIFEST_INVALID", "plugin.yaml 格式或语义无效: "+err.Error())
		return r
	}
	r.Manifest, r.ManifestStatus = &manifest, "PASS"
	if manifest.Type != "mcp-server" || manifest.Version != p.Version || string(bytesOf(manifest.Source)) != string(bytesOf(p.Source)) {
		r.ManifestStatus = "FAIL"
		r.add("FAIL", "MCP_MANIFEST_LOCK_MISMATCH", "manifest 类型、精确版本或完整来源与安装锁不一致")
	} else {
		r.add("PASS", "MCP_MANIFEST_VALID", "plugin.yaml 有效，类型、固定版本和完整来源匹配；清单名称保留原声明，可与项目别名不同，不证明发布者身份")
	}
	if server == nil || pluginlock.Definition(server) != p.DefinitionSHA256 || !permissionsEqual(manifest.Permissions, server["permissions"]) {
		r.PermissionStatus = "FAIL"
		r.add("FAIL", "MCP_MANIFEST_PERMISSIONS_MISMATCH", "manifest 权限与当前已保存定义不一致；请核对文件、网络和子进程请求，权限声明不自动授予权限")
	} else {
		r.PermissionStatus = "PASS"
		r.add("PASS", "MCP_MANIFEST_PERMISSIONS_MATCH", "manifest 文件、网络和子进程权限与当前定义一致；运行授权和强制隔离另行判断")
	}
	if manifest.Dependencies == nil {
		r.add("UNKNOWN", "MCP_MANIFEST_DEPENDENCIES_UNDECLARED", "manifest 未提供 dependencies；不能把缺失字段视为没有依赖")
	} else if p.Resolution.Format != "npm-package-lock-v3" || check.DependencyStatus != "PASS" {
		r.add("UNKNOWN", "MCP_MANIFEST_DEPENDENCIES_UNVERIFIED", "已读取直接依赖声明；当前产物缺少可核验的完整解析锁，不能证明声明完整或已安装")
	} else if err := verifyNPM(*manifest.Dependencies, p, observed); err != nil {
		if errors.Is(err, pluginlock.ErrDirectDependencyUnavailable) {
			r.add("UNKNOWN", "MCP_MANIFEST_DEPENDENCIES_UNVERIFIED", err.Error())
		} else {
			r.DependencyStatus = "FAIL"
			r.add("FAIL", "MCP_MANIFEST_DEPENDENCIES_MISMATCH", err.Error())
		}
	} else {
		r.DependencyStatus = "PASS"
		r.add("PASS", "MCP_MANIFEST_DEPENDENCIES_MATCH", "manifest 直接运行依赖与 package.json 声明及 npm v3 锁定解析一致；不将传递依赖冒充直接声明")
	}
	return r
}

func permissionsEqual(a map[string]any, raw any) bool {
	// Set order is irrelevant, while paths, domain text and all modes stay exact.
	canonical := func(v any) string {
		b := bytesOf(v)
		var copy map[string]any
		if json.Unmarshal(b, &copy) != nil || copy == nil {
			return ""
		}
		for group, field := range map[string]string{"fs": "paths", "net": "allow"} {
			m, ok := copy[group].(map[string]any)
			if !ok {
				return ""
			}
			items, ok := m[field].([]any)
			if !ok {
				return ""
			}
			sort.Slice(items, func(i, j int) bool { return fmt.Sprint(items[i]) < fmt.Sprint(items[j]) })
		}
		return string(bytesOf(copy))
	}
	return canonical(a) != "" && canonical(a) == canonical(raw)
}

func verifyNPM(deps []Dependency, p pluginlock.Plugin, observed pluginlock.Observation) error {
	want, err := pluginlock.DirectRuntimeDependencies(p, observed.Documents)
	if err != nil {
		return err
	}
	if len(deps) != len(want) {
		return fmt.Errorf("manifest 直接依赖数量与 package.json 运行/可选/peer 依赖不一致；缺失的平台可选依赖也不能冒充已核验")
	}
	for _, d := range deps {
		if d.Ecosystem != "npm" || want[d.Name] != d.Version {
			return fmt.Errorf("manifest 直接依赖与已解析的固定版本不一致: %s", d.Name)
		}
	}
	return nil
}
