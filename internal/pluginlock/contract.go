// Package pluginlock reads portable installation locks and checks prepared local
// artifacts. It never downloads, installs, runs, or awards source trust.
package pluginlock

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/workstore"
)

const MaxEntries = 20000
const MaxArtifactBytes int64 = 512 << 20
const MaxFileBytes int64 = 256 << 20

type Lock struct {
	Version int      `json:"schema_version"`
	Plugins []Plugin `json:"plugins"`
}
type Plugin struct {
	Name             string         `json:"name"`
	DefinitionSHA256 string         `json:"definition_sha256"`
	Source           map[string]any `json:"source"`
	Version          string         `json:"version"`
	Platform         string         `json:"platform"`
	Entrypoint       string         `json:"entrypoint"`
	Runtime          Runtime        `json:"runtime"`
	Artifact         Artifact       `json:"artifact"`
	Resolution       Resolution     `json:"resolution"`
	Dependencies     []Dependency   `json:"dependencies"`
}
type Runtime struct {
	Kind       string `json:"kind"`
	Version    string `json:"version"`
	Constraint string `json:"constraint"`
	Executable string `json:"executable"`
}
type Artifact struct {
	Path    string  `json:"path"`
	SHA256  string  `json:"sha256"`
	Entries []Entry `json:"entries"`
}
type Entry struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	SHA256    string `json:"sha256,omitempty"`
	Bytes     int64  `json:"bytes"`
	Mode      uint32 `json:"mode"`
}
type Resolution struct {
	Format string `json:"format"`
	Path   string `json:"path,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}
type Dependency struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Resolved  string `json:"resolved"`
	Integrity string `json:"integrity"`
}

var fixed = regexp.MustCompile(`^(v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?|[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)
var name = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func bytesOf(v any) []byte { b, _ := json.Marshal(v); return b }
func hash(s string) bool   { return len(s) == 64 && strings.Trim(s, "0123456789abcdef") == "" }

// Definition binds source, transport, entrypoint and requested permissions/tier.
// The inclusion switch may change without silently reauthorizing a new payload.
func Definition(server map[string]any) string {
	bound := map[string]any{}
	for k, v := range server {
		if k != "enabled" {
			bound[k] = v
		}
	}
	return workstore.Hash(bytesOf(bound))
}

// Digest is independent of input ordering; case aliases are rejected separately.
func Digest(entries []Entry) string {
	tree := map[string]Entry{}
	for _, e := range entries {
		tree[e.Path] = e
	}
	return workstore.Hash(bytesOf(tree))
}

func Parse(data []byte) (Lock, error) {
	var out Lock
	v, err := config.Validate("plugins-lock", data)
	if err != nil {
		return out, fmt.Errorf("安装锁格式无效: %w", err)
	}
	if err = json.Unmarshal(bytesOf(v), &out); err != nil {
		return out, err
	}
	seen, paths := map[string]bool{}, []string{}
	totalBytes := int64(0)
	totalEntries := 0
	for _, p := range out.Plugins {
		key := strings.ToLower(p.Name)
		if !name.MatchString(p.Name) || seen[key] {
			return out, fmt.Errorf("安装锁名称重复或无效: %s", p.Name)
		}
		seen[key] = true
		for _, e := range p.Artifact.Entries {
			totalBytes += e.Bytes
		}
		totalEntries += len(p.Artifact.Entries)
		if totalBytes > MaxArtifactBytes || totalEntries > MaxEntries {
			return out, fmt.Errorf("安装锁的所有产物合计超过字节或文件数量限制")
		}
		if err = validate(p); err != nil {
			return out, fmt.Errorf("%s: %w", p.Name, err)
		}
		for _, old := range paths {
			a, b := strings.ToLower(old), strings.ToLower(p.Artifact.Path)
			if a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/") {
				return out, fmt.Errorf("本地产物目录重叠")
			}
		}
		paths = append(paths, p.Artifact.Path)
	}
	return out, nil
}

func validate(p Plugin) error {
	if !fixed.MatchString(p.Version) || !fixed.MatchString(p.Runtime.Version) || p.Runtime.Constraint != "="+p.Runtime.Version {
		return fmt.Errorf("产物与运行时必须使用精确版本，运行时约束须为 =版本")
	}
	if p.Source["type"] != "local" && p.Source["version"] != p.Version {
		return fmt.Errorf("来源版本与锁定版本不一致")
	}
	if p.Source["type"] == "local" {
		s, _ := p.Source["path"].(string)
		if workstore.SafeRelative(s) != nil {
			return fmt.Errorf("本地来源须为安全项目相对路径")
		}
	} else {
		field := "package"
		if p.Source["type"] == "github" {
			field = "url"
		}
		s, _ := p.Source[field].(string)
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("缺少来源标识")
		}
	}
	if !strings.HasPrefix(p.Artifact.Path, "mcp-artifacts/") || workstore.SafeRelative(p.Artifact.Path) != nil {
		return fmt.Errorf("本地产物必须位于 Local Root 的 mcp-artifacts 子目录")
	}
	entries := map[string]Entry{}
	total := int64(0)
	for _, e := range p.Artifact.Entries {
		key := strings.ToLower(e.Path)
		if workstore.SafeRelative(e.Path) != nil {
			return fmt.Errorf("产物路径无效: %s", e.Path)
		}
		if _, ok := entries[key]; ok {
			return fmt.Errorf("产物路径重复或大小写冲突: %s", e.Path)
		}
		entries[key] = e
		total += e.Bytes
		if e.Directory {
			if e.Bytes != 0 || e.SHA256 != "" {
				return fmt.Errorf("目录不能声明文件字节或摘要")
			}
		} else if !hash(e.SHA256) || e.Bytes > MaxFileBytes {
			return fmt.Errorf("文件摘要或大小无效: %s", e.Path)
		}
		if total > MaxArtifactBytes {
			return fmt.Errorf("产物超过总字节限制")
		}
	}
	for _, e := range p.Artifact.Entries {
		for d := path.Dir(e.Path); d != "."; d = path.Dir(d) {
			parent, ok := entries[strings.ToLower(d)]
			if !ok || !parent.Directory || parent.Path != d {
				return fmt.Errorf("缺少明确父目录或路径大小写不一致: %s", d)
			}
		}
	}
	for _, f := range []string{p.Entrypoint, p.Runtime.Executable} {
		e, ok := entries[strings.ToLower(f)]
		if workstore.SafeRelative(f) != nil || !ok || e.Directory || e.Path != f {
			return fmt.Errorf("入口或运行时不在完整文件清单: %s", f)
		}
	}
	if Digest(p.Artifact.Entries) != p.Artifact.SHA256 {
		return fmt.Errorf("完整产物清单摘要不匹配")
	}
	if p.Resolution.Format == "npm-package-lock-v3" {
		e, ok := entries[strings.ToLower(p.Resolution.Path)]
		if !ok || e.Directory || e.Path != p.Resolution.Path || e.SHA256 != p.Resolution.SHA256 {
			return fmt.Errorf("依赖解析锁不在完整文件清单或摘要不符")
		}
	} else if p.Resolution.Path != "" || p.Resolution.SHA256 != "" {
		return fmt.Errorf("声明模式不能冒充已验证的依赖解析锁")
	}
	deps := map[string]bool{}
	for _, d := range p.Dependencies {
		key := strings.ToLower(d.Path)
		if workstore.SafeRelative(d.Path) != nil || deps[key] || !fixed.MatchString(d.Version) || strings.TrimSpace(d.Name) == "" || strings.TrimSpace(d.Resolved) == "" || strings.TrimSpace(d.Integrity) == "" {
			return fmt.Errorf("依赖缺少路径、来源、固定版本、摘要或有重复")
		}
		deps[key] = true
		if e, ok := entries[strings.ToLower(d.Path+"/package.json")]; !ok || e.Directory || e.Path != d.Path+"/package.json" {
			return fmt.Errorf("依赖缺少 package.json: %s", d.Path)
		}
	}
	return nil
}
