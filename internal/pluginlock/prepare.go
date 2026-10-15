package pluginlock

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/pzqf/AIDock/internal/workstore"
)

type Preparation struct {
	Name         string  `json:"name"`
	ArtifactPath string  `json:"artifact_path"`
	Version      string  `json:"version"`
	Runtime      Runtime `json:"runtime"`
}

// Prepare inventories only an explicitly selected directory under Local Root.
// Its self-computed hashes establish a baseline, never verified upstream origin.
func Prepare(ctx context.Context, local string, server map[string]any, req Preparation) (Plugin, error) {
	var out Plugin
	if server == nil || server["name"] != req.Name {
		return out, fmt.Errorf("先保存所选 MCP 定义")
	}
	if workstore.SafeRelative(req.ArtifactPath) != nil || len(req.ArtifactPath) <= len("mcp-artifacts/") || req.ArtifactPath[:len("mcp-artifacts/")] != "mcp-artifacts/" {
		return out, fmt.Errorf("只读取 Local Root 的 mcp-artifacts 子目录")
	}
	entries, docs, err := Observe(ctx, filepath.Join(local, filepath.FromSlash(req.ArtifactPath)))
	if err != nil {
		return out, err
	}
	src := server["source"].(map[string]any)
	version := req.Version
	if src["type"] != "local" {
		version, _ = src["version"].(string)
		if req.Version != "" && req.Version != version {
			return out, fmt.Errorf("产物版本须与已保存来源版本一致")
		}
	}
	out = Plugin{Name: req.Name, DefinitionSHA256: Definition(server), Source: src, Version: version, Platform: runtime.GOOS + "-" + runtime.GOARCH, Entrypoint: server["entrypoint"].(string), Runtime: req.Runtime, Artifact: Artifact{Path: req.ArtifactPath, SHA256: Digest(entries), Entries: entries}, Resolution: Resolution{Format: "declared"}, Dependencies: []Dependency{}}
	if b, ok := docs["package-lock.json"]; ok {
		var lock struct {
			Version  int                   `json:"lockfileVersion"`
			Packages map[string]npmPackage `json:"packages"`
		}
		if err = strictDocument(b, &lock); err != nil || lock.Version != 3 || lock.Packages == nil {
			return out, fmt.Errorf("本地 package-lock.json 必须是有效 npm v3 完整解析锁")
		}
		out.Resolution = Resolution{Format: "npm-package-lock-v3", Path: "package-lock.json", SHA256: workstore.Hash(b)}
		for loc, meta := range lock.Packages {
			if loc == "" {
				continue
			}
			if !packagePath.MatchString(loc) {
				return out, fmt.Errorf("npm 包路径无效: %s", loc)
			}
			id := meta.Name
			if id == "" {
				id = packageID(loc)
			}
			out.Dependencies = append(out.Dependencies, Dependency{Path: loc, Name: id, Version: meta.Version, Resolved: meta.Resolved, Integrity: meta.Integrity})
		}
		sort.Slice(out.Dependencies, func(i, j int) bool { return out.Dependencies[i].Path < out.Dependencies[j].Path })
	}
	if _, err = Parse(bytesOf(Lock{Version: 1, Plugins: []Plugin{out}})); err != nil {
		return out, err
	}
	return out, nil
}
