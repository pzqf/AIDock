// Package pluginsecurity checks offline vulnerability references and signed
// assessments from explicitly enrolled evaluators. An unsigned negative result
// is neither a safety rating nor a grant; all execution grants are separate.
package pluginsecurity

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/pluginmanifest"
	"github.com/pzqf/AIDock/internal/pluginorigin"
	"github.com/pzqf/AIDock/internal/workstore"
)

type Candidate struct {
	Plugin               pluginlock.Plugin     `json:"plugin"`
	Manifest             pluginmanifest.Report `json:"manifest"`
	SourceVerified       bool                  `json:"source_verified"`
	RuntimeVerified      bool                  `json:"runtime_verified"`
	DependenciesVerified bool                  `json:"dependencies_verified"`
}
type Advisory struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	URL      string `json:"url"`
}
type Package struct {
	pluginmanifest.Dependency
	Advisories *[]Advisory `json:"advisories"`
}
type Snapshot struct {
	Version          int       `json:"schema_version"`
	Format           string    `json:"format"`
	SubjectSHA256    string    `json:"subject_sha256"`
	DefinitionSHA256 string    `json:"definition_sha256"`
	ManifestSHA256   string    `json:"manifest_sha256"`
	Provider         string    `json:"provider"`
	ReferenceURL     string    `json:"reference_url"`
	CollectedAt      string    `json:"collected_at"`
	ExpiresAt        string    `json:"expires_at"`
	Packages         []Package `json:"packages"`
}
type Check struct {
	Name            string              `json:"name"`
	SubjectSHA256   string              `json:"subject_sha256"`
	Status          string              `json:"status"`
	CVEStatus       string              `json:"cve_status"`
	Current         bool                `json:"current"`
	Covered         int                 `json:"covered"`
	Required        int                 `json:"required"`
	AdvisoryCount   int                 `json:"advisory_count"`
	TrustLevel      string              `json:"trust_level"`
	SnapshotSHA256  string              `json:"snapshot_sha256,omitempty"`
	Snapshot        *Snapshot           `json:"snapshot,omitempty"`
	Packages        []Package           `json:"packages"`
	Checks          []inventory.Finding `json:"checks"`
	Assessment      AssessmentResult    `json:"assessment"`
	ReferenceSource string              `json:"reference_source,omitempty"`
}

func bytesOf(v any) []byte { b, _ := json.Marshal(v); return b }
func JSON(v any) []byte    { b, _ := json.MarshalIndent(v, "", "  "); return append(b, '\n') }
func hash(v string) bool   { return len(v) == 64 && strings.Trim(v, "0123456789abcdef") == "" }
func decode(b []byte, v any) error {
	p, err := config.Parse(b)
	if err != nil {
		return err
	}
	d := json.NewDecoder(strings.NewReader(string(bytesOf(p))))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func https(v string) bool {
	u, e := url.Parse(v)
	return e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}
func Parse(b []byte) (Snapshot, error) {
	var s Snapshot
	if len(b) > 2<<20 {
		return s, fmt.Errorf("安全快照超过 2 MB")
	}
	if err := decode(b, &s); err != nil {
		return s, err
	}
	if s.Version != 1 || s.Format != "aidock-vulnerability-reference-v1" || !hash(s.SubjectSHA256) || !hash(s.DefinitionSHA256) || !hash(s.ManifestSHA256) || strings.TrimSpace(s.Provider) == "" || len(s.Provider) > 200 || !https(s.ReferenceURL) || s.Packages == nil || len(s.Packages) > 1000 {
		return s, fmt.Errorf("安全快照格式、来源、绑定或包清单无效")
	}
	start, err := time.Parse(time.RFC3339, s.CollectedAt)
	if err != nil {
		return s, fmt.Errorf("采集时间无效")
	}
	end, err := time.Parse(time.RFC3339, s.ExpiresAt)
	if err != nil || !end.After(start) || end.Sub(start) > 30*24*time.Hour {
		return s, fmt.Errorf("快照有效期须晚于采集时间且不超过 30 天")
	}
	seen := map[string]bool{}
	count := 0
	for _, p := range s.Packages {
		key := string(bytesOf(p.Dependency))
		if p.Name == "" || p.Ecosystem == "" || p.Version == "" || seen[key] || p.Advisories == nil {
			return s, fmt.Errorf("包重复、身份不完整或缺少明确 advisories 列表")
		}
		seen[key] = true
		ids := map[string]bool{}
		for _, a := range *p.Advisories {
			count++
			if count > 2000 || strings.TrimSpace(a.ID) == "" || len(a.ID) > 150 || ids[a.ID] || strings.TrimSpace(a.Summary) == "" || len(a.Summary) > 2000 || !https(a.URL) {
				return s, fmt.Errorf("漏洞记录重复或不完整")
			}
			ids[a.ID] = true
			switch a.Severity {
			case "unknown", "low", "moderate", "high", "critical":
			default:
				return s, fmt.Errorf("漏洞严重度无效")
			}
		}
	}
	return s, nil
}

// Only the root package and explicitly declared direct dependencies are in this
// reference lookup. Transitive resolution in plugins.lock is a separate check.
func Packages(c Candidate) []Package {
	out := []Package{}
	if c.Manifest.Manifest == nil {
		return out
	}
	m := c.Manifest.Manifest
	root := pluginmanifest.Dependency{Ecosystem: "generic", Name: m.Name, Version: c.Plugin.Version}
	if c.Plugin.Source["type"] == "npm" {
		root.Ecosystem = "npm"
		root.Name, _ = c.Plugin.Source["package"].(string)
	}
	out = append(out, Package{Dependency: root})
	if m.Dependencies != nil {
		for _, d := range *m.Dependencies {
			duplicate := false
			for _, p := range out {
				if p.Dependency == d {
					duplicate = true
				}
			}
			if !duplicate {
				out = append(out, Package{Dependency: d})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return string(bytesOf(out[i].Dependency)) < string(bytesOf(out[j].Dependency)) })
	return out
}
func Template(c Candidate, now time.Time) Snapshot {
	return Snapshot{Version: 1, Format: "aidock-vulnerability-reference-v1", SubjectSHA256: pluginorigin.For(c.Plugin).SHA256, DefinitionSHA256: c.Plugin.DefinitionSHA256, ManifestSHA256: c.Manifest.ManifestSHA256, Provider: "", ReferenceURL: "", CollectedAt: now.UTC().Format(time.RFC3339), ExpiresAt: now.UTC().Add(24 * time.Hour).Format(time.RFC3339), Packages: Packages(c)}
}
func Bound(s Snapshot, c Candidate) error {
	if s.SubjectSHA256 != pluginorigin.For(c.Plugin).SHA256 || s.DefinitionSHA256 != c.Plugin.DefinitionSHA256 || s.ManifestSHA256 != c.Manifest.ManifestSHA256 {
		return fmt.Errorf("快照未绑定当前来源、完整产物、运行时、权限和清单")
	}
	if c.Manifest.Status == "FAIL" || c.Manifest.ArtifactStatus != "PASS" || c.Manifest.ManifestStatus != "PASS" || c.Manifest.PermissionStatus != "PASS" || c.Manifest.Manifest == nil || c.Manifest.Manifest.Dependencies == nil {
		return fmt.Errorf("先完成完整产物、清单、权限及明确直接依赖声明检查")
	}
	allowed := map[pluginmanifest.Dependency]bool{}
	for _, p := range Packages(c) {
		allowed[p.Dependency] = true
	}
	for _, p := range s.Packages {
		if !allowed[p.Dependency] {
			return fmt.Errorf("快照包含不属于当前直接声明范围的包: %s/%s@%s", p.Ecosystem, p.Name, p.Version)
		}
	}
	return nil
}
func Verify(c Candidate, s *Snapshot, now time.Time) Check {
	out := Check{Name: c.Plugin.Name, SubjectSHA256: pluginorigin.For(c.Plugin).SHA256, Status: "UNKNOWN", CVEStatus: "MISSING", TrustLevel: "L0", Packages: Packages(c), Checks: []inventory.Finding{}}
	out.Required = len(out.Packages)
	add := func(status, code, detail string) {
		out.Checks = append(out.Checks, inventory.Finding{Status: status, Code: code, Detail: detail})
		if status == "FAIL" {
			out.Status = "FAIL"
		}
	}
	if s == nil {
		add("UNKNOWN", "MCP_SECURITY_REFERENCE_MISSING", "没有当前产物的本地漏洞参考快照；不能按无漏洞处理")
		return out
	}
	out.Snapshot = s
	out.SnapshotSHA256 = workstore.Hash(JSON(s))
	if err := Bound(*s, c); err != nil {
		out.CVEStatus = "STALE"
		add("UNKNOWN", "MCP_SECURITY_REFERENCE_STALE", err.Error())
		return out
	}
	start, _ := time.Parse(time.RFC3339, s.CollectedAt)
	end, _ := time.Parse(time.RFC3339, s.ExpiresAt)
	out.Current = !now.Before(start) && now.Before(end)
	if !out.Current {
		out.CVEStatus = "EXPIRED"
		add("UNKNOWN", "MCP_SECURITY_REFERENCE_EXPIRED", "漏洞参考快照已过期或采集时间在未来；保留旧结果与来源，不能作为当前通过证据")
	}
	for i, p := range out.Packages {
		for _, cached := range s.Packages {
			if p.Dependency == cached.Dependency {
				out.Packages[i].Advisories = cached.Advisories
				out.Covered++
				out.AdvisoryCount += len(*cached.Advisories)
			}
		}
	}
	if out.AdvisoryCount > 0 {
		out.CVEStatus = "FOUND"
		add("FAIL", "MCP_SECURITY_KNOWN_ADVISORY", fmt.Sprintf("当前版本及直接声明依赖存在 %d 条已记录风险；即使快照过期也不能自动解除，核实并更新证据后重新审查", out.AdvisoryCount))
	} else if out.Current && out.Covered == out.Required {
		out.CVEStatus = "NO_KNOWN_MATCHES"
		add("PASS", "MCP_SECURITY_REFERENCE_NO_MATCH", "在该参考快照覆盖的当前版本及直接声明依赖中未记录漏洞；来源内容未独立认证，不等于无恶意代码或插件安全")
	} else if out.Current {
		out.CVEStatus = "PARTIAL"
		add("UNKNOWN", "MCP_SECURITY_REFERENCE_PARTIAL", fmt.Sprintf("只覆盖 %d/%d 个当前版本与直接声明依赖，其余尚未检查", out.Covered, out.Required))
	}
	add("UNKNOWN", "MCP_SECURITY_ASSESSMENT_INCOMPLETE", "参考快照由用户导入；缺独立来源认证、恶意代码、静态分析及身份核实，保持 L0；未授权或启动程序")
	return out
}
