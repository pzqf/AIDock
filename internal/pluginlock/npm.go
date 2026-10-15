package pluginlock

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/workstore"
)

type npmPackage struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Resolved     string            `json:"resolved"`
	Integrity    string            `json:"integrity"`
	Link         bool              `json:"link"`
	Dependencies map[string]string `json:"dependencies"`
	Dev          map[string]string `json:"devDependencies"`
	Optional     map[string]string `json:"optionalDependencies"`
	Peers        map[string]string `json:"peerDependencies"`
	PeerMeta     map[string]struct {
		Optional bool `json:"optional"`
	} `json:"peerDependenciesMeta"`
}

var packagePath = regexp.MustCompile(`^(node_modules/(@[^/]+/)?[^/]+/)*node_modules/(@[^/]+/)?[^/]+$`)
var packageName = regexp.MustCompile(`^(@[a-zA-Z0-9_.-]+/)?[a-zA-Z0-9_.-]+$`)

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func strictDocument(data []byte, out any) error {
	v, err := config.Parse(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytesOf(v), out)
}
func packageID(p string) string {
	i := strings.LastIndex(p, "node_modules/")
	return strings.TrimPrefix(p[i:], "node_modules/")
}
func integrityOK(s string) bool {
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		return false
	}
	b, err := base64.StdEncoding.DecodeString(parts[1])
	return err == nil && (parts[0] == "sha512" && len(b) == 64 || parts[0] == "sha256" && len(b) == 32)
}
func resolvedOK(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

func checkNPM(p Plugin, docs map[string][]byte) error {
	if p.Resolution.Path != "package-lock.json" {
		return fmt.Errorf("npm v3 解析锁须为产物根目录的 package-lock.json")
	}
	data, ok := docs[p.Resolution.Path]
	if !ok || workstore.Hash(data) != p.Resolution.SHA256 {
		return fmt.Errorf("npm 解析锁文件缺失或摘要变化")
	}
	var lock struct {
		Version  int                   `json:"lockfileVersion"`
		Packages map[string]npmPackage `json:"packages"`
	}
	if err := strictDocument(data, &lock); err != nil {
		return fmt.Errorf("npm 解析锁格式无效: %w", err)
	}
	if lock.Version != 3 || lock.Packages == nil {
		return fmt.Errorf("只接受 npm package-lock v3 的完整 packages 清单")
	}
	if _, ok := lock.Packages[""]; !ok {
		return fmt.Errorf("npm 解析锁缺少根包记录")
	}
	want := map[string]Dependency{}
	for _, d := range p.Dependencies {
		want[d.Path] = d
	}
	if len(lock.Packages) != len(want)+1 {
		return fmt.Errorf("完整解析锁与声明的依赖数量不同")
	}
	primaryFound := false
	for _, loc := range keys(lock.Packages) {
		meta := lock.Packages[loc]
		if meta.Link {
			return fmt.Errorf("npm link/workspace 不是自包含锁定产物: %s", loc)
		}
		if loc != "" {
			if !packagePath.MatchString(loc) || workstore.SafeRelative(loc) != nil {
				return fmt.Errorf("npm 包路径无效: %s", loc)
			}
			id := meta.Name
			if id == "" {
				id = packageID(loc)
			}
			d := Dependency{Path: loc, Name: id, Version: meta.Version, Resolved: meta.Resolved, Integrity: meta.Integrity}
			if !reflect.DeepEqual(want[loc], d) || !fixed.MatchString(meta.Version) || !integrityOK(meta.Integrity) || !resolvedOK(meta.Resolved) {
				return fmt.Errorf("依赖缺项、浮动版本、来源/摘要无效或与解析锁不同: %s", loc)
			}
		}
		packageFile := path.Join(loc, "package.json")
		var installed npmPackage
		b, ok := docs[packageFile]
		if !ok || strictDocument(b, &installed) != nil {
			return fmt.Errorf("缺少有效的本地包元数据: %s", packageFile)
		}
		expectedName := meta.Name
		if expectedName == "" && loc != "" {
			expectedName = packageID(loc)
		}
		if installed.Version != meta.Version || expectedName != "" && installed.Name != expectedName {
			return fmt.Errorf("本地 package.json 版本/名称与解析锁不同: %s", loc)
		}
		if installed.Name == p.Source["package"] && installed.Version == p.Version {
			primaryFound = true
		}
		if !reflect.DeepEqual(installed.Dependencies, meta.Dependencies) || !reflect.DeepEqual(installed.Optional, meta.Optional) || !reflect.DeepEqual(installed.Peers, meta.Peers) || !reflect.DeepEqual(installed.PeerMeta, meta.PeerMeta) {
			return fmt.Errorf("本地依赖边声明与完整解析锁不同: %s", loc)
		}
		sets := []map[string]string{meta.Dependencies, meta.Peers, meta.Optional}
		if loc == "" {
			sets = append(sets, meta.Dev)
			if !reflect.DeepEqual(installed.Dev, meta.Dev) {
				return fmt.Errorf("根包开发依赖与解析锁不同")
			}
		}
		for i, deps := range sets {
			for _, id := range keys(deps) {
				if !packageName.MatchString(id) {
					return fmt.Errorf("无效依赖名: %s", id)
				}
				optional := false
				if i == 0 {
					_, optional = meta.Optional[id]
				}
				if i == 1 {
					optional = meta.PeerMeta[id].Optional
				}
				if i == 2 {
					optional = true
				}
				resolved, exists := resolvedDependency(loc, id, lock.Packages)
				if !exists && !optional {
					return fmt.Errorf("完整依赖图未闭合: %s -> %s", loc, id)
				}
				if exists {
					if err := checkRequirement(deps[id], resolved); err != nil {
						return fmt.Errorf("依赖版本约束不符: %s -> %s: %w", loc, id, err)
					}
				}
			}
		}
	}
	for _, file := range keys(docs) {
		if !strings.HasSuffix(file, "/package.json") {
			continue
		}
		loc := strings.TrimSuffix(file, "/package.json")
		if packagePath.MatchString(loc) {
			if _, ok := lock.Packages[loc]; !ok {
				return fmt.Errorf("存在解析锁未声明的本地依赖: %s", loc)
			}
		}
	}
	if p.Source["type"] == "npm" && !primaryFound {
		return fmt.Errorf("完整产物没有锁定来源包及其精确版本")
	}
	return nil
}

func resolvedDependency(from, id string, packages map[string]npmPackage) (npmPackage, bool) {
	for current := from; ; {
		if path.Base(current) != "node_modules" {
			loc := path.Join(current, "node_modules", id)
			if pkg, ok := packages[loc]; ok {
				if pkg.Name == "" {
					pkg.Name = packageID(loc)
				}
				return pkg, true
			}
		}
		if current == "" || current == "." {
			return npmPackage{}, false
		}
		current = path.Dir(current)
	}
}

func checkRequirement(spec string, resolved npmPackage) error {
	if len(spec) > 2048 {
		return fmt.Errorf("版本约束过长")
	}
	if strings.HasPrefix(spec, "npm:") {
		alias := strings.TrimPrefix(spec, "npm:")
		at := strings.LastIndex(alias, "@")
		if at <= 0 || !packageName.MatchString(alias[:at]) || alias[:at] != resolved.Name {
			return fmt.Errorf("npm 别名与解析包身份不符")
		}
		spec = alias[at+1:]
	}
	constraint, err := semver.NewConstraint(spec)
	if err != nil {
		return fmt.Errorf("未支持的 npm 版本约束 %q", spec)
	}
	version, err := semver.StrictNewVersion(strings.TrimPrefix(resolved.Version, "v"))
	if err != nil || !constraint.Check(version) {
		return fmt.Errorf("%s 不满足 %s", resolved.Version, spec)
	}
	return nil
}
