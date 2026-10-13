// Package bridge records explicitly selected external skill sources.
//
// A bridge is deliberately read-only in this first implementation. The
// external directory remains authoritative; AIDock only records its scope,
// scans complete packages, and compares the current candidate with an
// explicitly approved revision.
package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/workstore"
)

var nativeName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func severity(status string) int {
	return map[string]int{"PASS": 1, "UNKNOWN": 2, "WARN": 3, "STALE": 4, "FAIL": 5}[status]
}

type Registration struct {
	ID               string   `json:"id"`
	Project          string   `json:"project"`
	Scope            string   `json:"scope"`
	Copy             string   `json:"copy"`
	Root             string   `json:"root"`
	SourcePath       string   `json:"source_path"`
	SourceRole       string   `json:"source_role"`
	Format           string   `json:"format"`
	Generator        string   `json:"generator,omitempty"`
	Packages         []string `json:"packages,omitempty"`
	AllowEdit        bool     `json:"allow_edit,omitempty"`
	ApprovedRevision string   `json:"approved_revision,omitempty"`
	ApprovedAt       string   `json:"approved_at,omitempty"`
	ApprovedBy       string   `json:"approved_by,omitempty"`
}

type Index struct {
	Version int            `json:"schema_version"`
	Bridges []Registration `json:"bridges"`
}

type Finding struct {
	Status string `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

type Candidate struct {
	BridgeID string          `json:"bridge_id"`
	Revision string          `json:"revision,omitempty"`
	Skill    inventory.Skill `json:"skill"`
}

type BridgeReport struct {
	ID               string       `json:"id"`
	Project          string       `json:"project"`
	Scope            string       `json:"scope"`
	Copy             string       `json:"copy"`
	Root             string       `json:"root"`
	SourcePath       string       `json:"source_path"`
	SourceRole       string       `json:"source_role"`
	Format           string       `json:"format"`
	Generator        string       `json:"generator,omitempty"`
	AllowEdit        bool         `json:"allow_edit"`
	Status           string       `json:"status"`
	CurrentRevision  string       `json:"current_revision,omitempty"`
	RevisionFormat   string       `json:"revision_format"`
	ApprovedRevision string       `json:"approved_revision,omitempty"`
	Drift            bool         `json:"drift"`
	ReadOnly         bool         `json:"read_only"`
	Findings         []Finding    `json:"findings"`
	Candidates       []Candidate  `json:"candidates"`
	CheckedAt        string       `json:"checked_at"`
	ProjectPins      []ProjectPin `json:"project_pins,omitempty"`
}

// Project pins are separate from an optional manually registered whole-source
// baseline. Their presence never proves generator or tool execution capability.
type ProjectPin struct {
	SkillID          string `json:"skill_id"`
	ApprovalID       string `json:"approval_id"`
	PackagePath      string `json:"package_path"`
	ApprovedRevision string `json:"approved_revision"`
	CurrentRevision  string `json:"current_revision,omitempty"`
	Status           string `json:"status"`
	CacheAvailable   bool   `json:"cache_available"`
}

type Report struct {
	CheckedAt string         `json:"checked_at"`
	ReadOnly  bool           `json:"read_only"`
	Bridges   []BridgeReport `json:"bridges"`
	Counts    map[string]int `json:"counts"`
	Status    string         `json:"status"`
}

// Load validates a bridge index and resolves relative roots against the index
// file. It does not create directories or touch the registered source.
func Load(path string) (Index, error) {
	b, err := config.ReadFile(path)
	if err != nil {
		return Index{}, err
	}
	return Decode(path, b)
}

// Decode validates the exact observed bytes, so a plan can bind their hash
// without re-opening the index between hashing and parsing.
func Decode(path string, b []byte) (Index, error) {
	v, err := config.Validate("bridge", b)
	if err != nil {
		return Index{}, err
	}
	data, _ := json.Marshal(v)
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return idx, err
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return idx, err
	}
	return normalize(idx, base)
}

func normalize(idx Index, base string) (Index, error) {
	// Also validate direct Go callers before using any paths.
	data, err := json.Marshal(idx)
	if err != nil {
		return idx, err
	}
	if _, err = config.Validate("bridge", data); err != nil {
		return idx, err
	}
	idx.Bridges = append([]Registration(nil), idx.Bridges...)
	seen := map[string]bool{}
	for i := range idx.Bridges {
		b := &idx.Bridges[i]
		if seen[strings.ToLower(b.ID)] {
			return idx, fmt.Errorf("重复外部源登记 %s", b.ID)
		}
		seen[strings.ToLower(b.ID)] = true
		if strings.TrimSpace(b.Scope) == "" || strings.TrimSpace(b.Copy) == "" || strings.TrimSpace(b.Root) == "" || strings.Contains(b.Root, "${") {
			return idx, fmt.Errorf("外部源绑定缺少明确的 scope/copy/root")
		}
		if b.ApprovedRevision == "" && (b.ApprovedAt != "" || b.ApprovedBy != "") {
			return idx, fmt.Errorf("批准信息需要 approved_revision")
		}
		if b.ApprovedAt != "" {
			if _, err := time.Parse(time.RFC3339, b.ApprovedAt); err != nil {
				return idx, fmt.Errorf("无效批准时间: %w", err)
			}
		}
		if err := workstore.SafeRelative(b.SourcePath); err != nil {
			return idx, fmt.Errorf("bridge %s source_path: %w", b.ID, err)
		}
		for n, p := range b.Packages {
			if err := workstore.SafeRelative(p); err != nil {
				return idx, fmt.Errorf("bridge %s packages[%d]: %w", b.ID, n, err)
			}
			for _, earlier := range b.Packages[:n] {
				if sameOrInside(earlier, p) || sameOrInside(p, earlier) {
					return idx, fmt.Errorf("bridge %s packages overlap: %s / %s", b.ID, earlier, p)
				}
			}
		}
		if !filepath.IsAbs(b.Root) {
			if base == "" {
				return idx, fmt.Errorf("直接检查需要绝对外部根；相对根请使用 Load 解析")
			}
			b.Root = filepath.Join(base, b.Root)
		}
		b.Root, err = filepath.Abs(filepath.Clean(b.Root))
		if err != nil {
			return idx, err
		}
	}
	return idx, nil
}

func sameOrInside(parent, child string) bool {
	rel, err := filepath.Rel(strings.ToLower(filepath.Clean(parent)), strings.ToLower(filepath.Clean(child)))
	return err == nil && (rel == "." || (!filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}

func Check(ctx context.Context, idx Index) (Report, error) {
	r := Report{CheckedAt: time.Now().Format(time.RFC3339), ReadOnly: true, Bridges: []BridgeReport{}, Counts: map[string]int{}, Status: "PASS"}
	var err error
	idx, err = normalize(idx, "")
	if err != nil {
		return r, err
	}
	for _, registration := range idx.Bridges {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		br := BridgeReport{
			ID: registration.ID, Project: registration.Project, Scope: registration.Scope, Copy: registration.Copy,
			Root: registration.Root, SourcePath: registration.SourcePath, SourceRole: registration.SourceRole, Format: registration.Format, RevisionFormat: "external-v1",
			Generator: registration.Generator, AllowEdit: registration.AllowEdit, ApprovedRevision: registration.ApprovedRevision,
			ReadOnly: true, Findings: []Finding{}, Candidates: []Candidate{}, CheckedAt: r.CheckedAt,
		}
		add := func(status, code, detail string) {
			br.Findings = append(br.Findings, Finding{Status: status, Code: code, Detail: detail})
			if severity(status) > severity(br.Status) {
				br.Status = status
			}
		}
		for _, earlier := range r.Bridges {
			if earlier.Project == br.Project && earlier.Scope == br.Scope && earlier.Copy == br.Copy {
				a := filepath.Join(earlier.Root, filepath.FromSlash(earlier.SourcePath))
				b := filepath.Join(br.Root, filepath.FromSlash(br.SourcePath))
				if sameOrInside(a, b) || sameOrInside(b, a) {
					add("FAIL", "BRIDGE_BINDING_COLLISION", "同一 project/scope/copy 的源目录重叠")
				}
			}
		}
		if registration.Generator == "" {
			add("UNKNOWN", "EXTERNAL_GENERATOR_UNVERIFIED", "未登记生成器；AIDock 不执行或阻止外部生成器")
		} else {
			add("UNKNOWN", "EXTERNAL_GENERATOR_UNVERIFIED", "生成器仅作身份记录，未启动或验证其暂存/恢复能力")
		}
		if err := workstore.CheckLinks(registration.Root); err != nil {
			add("FAIL", "EXTERNAL_ROOT_UNSAFE", err.Error())
			r.Bridges = append(r.Bridges, br)
			continue
		}
		sourceRoot := filepath.Join(registration.Root, filepath.FromSlash(registration.SourcePath))
		if !workstore.Within(registration.Root, sourceRoot) {
			add("FAIL", "EXTERNAL_PATH_ESCAPE", "source_path 逃逸登记根")
			r.Bridges = append(r.Bridges, br)
			continue
		}
		if err := workstore.CheckLinks(sourceRoot); err != nil {
			add("FAIL", "EXTERNAL_SOURCE_UNSAFE", err.Error())
			r.Bridges = append(r.Bridges, br)
			continue
		}
		if info, err := os.Stat(sourceRoot); err != nil || !info.IsDir() {
			add("FAIL", "EXTERNAL_SOURCE_MISSING", "外部规范源目录不存在或不是目录")
			r.Bridges = append(r.Bridges, br)
			continue
		}
		sources := []inventory.Source{{Path: registration.SourcePath, Role: "external-source", Generator: registration.Generator}}
		if len(registration.Packages) > 0 {
			sources = nil
			for _, p := range registration.Packages {
				rel := filepath.ToSlash(filepath.Join(registration.SourcePath, p))
				pkgRoot := filepath.Join(registration.Root, filepath.FromSlash(rel))
				if err := workstore.CheckLinks(filepath.Join(pkgRoot, "SKILL.md")); err != nil {
					add("FAIL", "EXTERNAL_SOURCE_UNSAFE", err.Error())
					continue
				}
				if info, err := os.Stat(filepath.Join(pkgRoot, "SKILL.md")); err != nil || !info.Mode().IsRegular() {
					add("FAIL", "EXTERNAL_PACKAGE_MISSING", "登记的包缺少 SKILL.md 或路径不可读: "+p)
					continue
				}
				sources = append(sources, inventory.Source{Path: rel, Role: "external-source", Generator: registration.Generator})
			}
		}
		if len(sources) == 0 {
			r.Bridges = append(r.Bridges, br)
			continue
		}
		unsafe := false
		for _, s := range sources {
			if err := checkScanRoot(ctx, filepath.Join(registration.Root, filepath.FromSlash(s.Path))); err != nil {
				if ctx.Err() != nil {
					return r, ctx.Err()
				}
				add("FAIL", "EXTERNAL_SCAN_UNSAFE", err.Error())
				unsafe = true
			}
		}
		if unsafe {
			r.Bridges = append(r.Bridges, br)
			continue
		}
		manifest := inventory.Manifest{SchemaVersion: 1, Bindings: []inventory.Binding{{
			Project: registration.Project, Scope: registration.Scope, Copy: registration.Copy, Root: registration.Root,
			Sources: sources,
		}}}
		scan, err := inventory.Scan(ctx, manifest, inventory.Options{})
		if err != nil {
			if ctx.Err() != nil {
				return r, ctx.Err()
			}
			add("FAIL", "EXTERNAL_SCAN_FAILED", err.Error())
			r.Bridges = append(r.Bridges, br)
			continue
		}
		for _, source := range scan.Sources {
			if source.Status == "FAIL" {
				add("FAIL", "EXTERNAL_SCAN_FAILED", source.Reason)
			}
		}
		for _, skill := range scan.Skills {
			candidate := Candidate{BridgeID: registration.ID, Skill: skill}
			if !nativeName.MatchString(skill.Name) {
				add("FAIL", "NATIVE_NAME_INVALID", skill.Path+": 原生名需为小写字母、数字或连字符，最长 64")
			}
			for _, finding := range skill.Findings {
				if finding.Status == "FAIL" || finding.Status == "WARN" {
					add(finding.Status, finding.Code, skill.Path+": "+finding.Detail)
				}
				if finding.Code == "LINK_EXCLUDED" || finding.Code == "SENSITIVE_FILE_EXCLUDED" || finding.Code == "GENERATED_OR_CACHE_EXCLUDED" {
					add("FAIL", "EXTERNAL_PACKAGE_INCOMPLETE", skill.Path+": 含排除项，不生成完整候选 revision")
				}
			}
			if skill.StaticStatus != "FAIL" && br.Status != "FAIL" {
				candidate.Revision, err = packageRevision(ctx, filepath.Join(registration.Root, filepath.FromSlash(skill.Path)), skill)
				if err != nil {
					if ctx.Err() != nil {
						return r, ctx.Err()
					}
					add("FAIL", "EXTERNAL_PACKAGE_UNSTABLE", skill.Path+": "+err.Error())
				}
			}
			br.Candidates = append(br.Candidates, candidate)
		}
		if br.Status != "FAIL" && len(br.Candidates) > 0 {
			br.CurrentRevision = revision(registration, br.Candidates)
		}
		if len(br.Candidates) == 0 {
			if registration.ApprovedRevision != "" {
				add("FAIL", "EXTERNAL_APPROVED_SOURCE_EMPTY", "批准基线存在，但扫描范围内已无完整技能包")
			} else {
				add("WARN", "EXTERNAL_SOURCE_EMPTY", "扫描范围内没有包含 SKILL.md 的完整技能包")
			}
		}
		if registration.ApprovedRevision == "" {
			add("WARN", "EXTERNAL_SOURCE_UNAPPROVED", "登记文件未设置集合批准基线；项目包级批准另行记录，不会自动采纳当前候选")
		} else if br.CurrentRevision != "" && br.CurrentRevision != registration.ApprovedRevision {
			br.Drift = true
			add("STALE", "EXTERNAL_SOURCE_DRIFT", "外部源集合 revision 与登记基线不同；需核对并显式采纳，现有项目固定版本保持原值")
		}
		if br.Status == "" {
			br.Status = "PASS"
		}
		r.Bridges = append(r.Bridges, br)
	}
	// Native names share one slot within a project/scope/copy even when its
	// non-overlapping source directories are registered separately.
	names := map[string][]int{}
	for i, b := range r.Bridges {
		for _, candidate := range b.Candidates {
			key := strings.ToLower(b.Project + "/" + b.Scope + "/" + b.Copy + "/" + candidate.Skill.Name)
			names[key] = append(names[key], i)
		}
	}
	for _, indices := range names {
		if len(indices) < 2 {
			continue
		}
		seen := map[int]bool{}
		for _, i := range indices {
			if seen[i] {
				continue
			}
			seen[i] = true
			r.Bridges[i].Status = "FAIL"
			r.Bridges[i].CurrentRevision = ""
			r.Bridges[i].Findings = append(r.Bridges[i].Findings, Finding{"FAIL", "NATIVE_NAME_COLLISION", "同一 project/scope/copy 的外部技能原生名称重复"})
		}
	}
	for _, b := range r.Bridges {
		r.Counts[b.Status]++
		if severity(b.Status) > severity(r.Status) {
			r.Status = b.Status
		}
	}
	return r, nil
}

type revisionItem struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Revision string `json:"revision"`
}

func revision(registration Registration, candidates []Candidate) string {
	items := make([]revisionItem, 0, len(candidates))
	for _, candidate := range candidates {
		items = append(items, revisionItem{Path: candidate.Skill.Path, Name: candidate.Skill.Name, Revision: candidate.Revision})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Path == items[j].Path {
			return items[i].Revision < items[j].Revision
		}
		return items[i].Path < items[j].Path
	})
	b, _ := json.Marshal(struct {
		Format        string         `json:"revision_format"`
		SourcePath    string         `json:"source_path"`
		PackageFormat string         `json:"package_format"`
		Generator     string         `json:"generator"`
		Items         []revisionItem `json:"items"`
	}{"external-v1", registration.SourcePath, registration.Format, registration.Generator, items})
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
