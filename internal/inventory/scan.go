package inventory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	mdtext "github.com/yuin/goldmark/text"
)

type Finding struct {
	Status    string `json:"status"`
	Code      string `json:"code"`
	Detail    string `json:"detail"`
	Component string `json:"component,omitempty"`
}
type File struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	Type   string `json:"type"`
}
type Skill struct {
	CandidateID   string            `json:"candidate_id"`
	Project       string            `json:"project"`
	Scope         string            `json:"scope"`
	Copy          string            `json:"copy"`
	Source        string            `json:"source"`
	Role          string            `json:"role"`
	Path          string            `json:"path"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	PackageDigest string            `json:"package_digest"`
	StaticStatus  string            `json:"static_status"`
	Files         []File            `json:"files"`
	Findings      []Finding         `json:"findings"`
	Layers        map[string]string `json:"layers"`
}
type SourceReport struct {
	Project string `json:"project"`
	Scope   string `json:"scope"`
	Copy    string `json:"copy"`
	Path    string `json:"path"`
	Role    string `json:"role"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Skills  int    `json:"skills"`
}
type Report struct {
	CheckedAt string         `json:"checked_at"`
	ReadOnly  bool           `json:"read_only"`
	Skills    []Skill        `json:"skills"`
	Sources   []SourceReport `json:"sources"`
	Counts    map[string]int `json:"counts"`
}
type Options struct {
	Project, Scope, Copy string
	IncludeGenerated     bool
}

func Scan(ctx context.Context, m Manifest, o Options) (Report, error) {
	r := Report{CheckedAt: time.Now().Format(time.RFC3339), ReadOnly: true, Skills: []Skill{}, Sources: []SourceReport{}, Counts: map[string]int{}}
	matched, dirs := 0, 0
	for _, binding := range m.Bindings {
		if o.Project != "" && binding.Project != o.Project || o.Scope != "" && binding.Scope != o.Scope || o.Copy != "" && binding.Copy != o.Copy {
			continue
		}
		matched++
		root, rootErr := filepath.EvalSymlinks(binding.Root)
		for _, source := range binding.Sources {
			if err := ctx.Err(); err != nil {
				return r, err
			}
			sr := SourceReport{Project: binding.Project, Scope: binding.Scope, Copy: binding.Copy, Path: source.Path, Role: source.Role, Status: "PASS", Reason: "仅依据清单声明归类，生成关系尚未验证"}
			if source.Role == "generated" && !o.IncludeGenerated {
				sr.Status, sr.Reason = "SKIP", "生成镜像默认不重复纳入资产；可显式只读盘点"
				r.Sources = append(r.Sources, sr)
				continue
			}
			if rootErr != nil {
				sr.Status, sr.Reason = "FAIL", "绑定根不可读取"
				r.Sources = append(r.Sources, sr)
				continue
			}
			path := filepath.Join(root, filepath.FromSlash(source.Path))
			if _, err := checkedPath(root, path); err != nil {
				sr.Status, sr.Reason = "FAIL", err.Error()
				r.Sources = append(r.Sources, sr)
				continue
			}
			err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if err = ctx.Err(); err != nil {
					return err
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				if isLink(info) {
					if d.IsDir() {
						return fs.SkipDir
					}
					return nil
				}
				if !d.IsDir() {
					return nil
				}
				dirs++
				if dirs > 20000 {
					return fmt.Errorf("SCAN_LIMIT_REACHED: 源目录数量超限")
				}
				if excludedDir(d.Name()) {
					return fs.SkipDir
				}
				if _, err := os.Lstat(filepath.Join(p, "SKILL.md")); err == nil {
					if len(r.Skills) >= 10000 {
						return fmt.Errorf("SCAN_LIMIT_REACHED: 技能数量超限")
					}
					r.Skills = append(r.Skills, scanPackage(ctx, root, p, binding, source))
					sr.Skills++
					return fs.SkipDir
				}
				return nil
			})
			if err != nil {
				if ctx.Err() != nil {
					return r, ctx.Err()
				}
				sr.Status, sr.Reason = "FAIL", err.Error()
			}
			r.Sources = append(r.Sources, sr)
		}
	}
	if matched == 0 {
		return r, fmt.Errorf("没有匹配的明确绑定")
	}
	duplicates := map[string][]int{}
	for i, s := range r.Skills {
		if s.Role != "generated" {
			key := s.Project + "/" + s.Scope + "/" + s.Copy + "/" + strings.ToLower(s.Name)
			duplicates[key] = append(duplicates[key], i)
		}
	}
	for _, indices := range duplicates {
		if len(indices) > 1 {
			for _, i := range indices {
				r.Skills[i].Findings = append(r.Skills[i].Findings, Finding{Status: "FAIL", Code: "NATIVE_NAME_COLLISION", Detail: "同一绑定中的原生名称重复，不能自动覆盖"})
				r.Skills[i].StaticStatus = "FAIL"
				r.Skills[i].Layers["L1"] = "FAIL"
			}
		}
	}
	for _, s := range r.Skills {
		r.Counts[s.StaticStatus]++
	}
	sort.Slice(r.Skills, func(i, j int) bool {
		a, b := r.Skills[i], r.Skills[j]
		return a.Scope+a.Copy+a.Path < b.Scope+b.Copy+b.Path
	})
	return r, nil
}

func scanPackage(ctx context.Context, root, dir string, b Binding, source Source) Skill {
	rel, _ := filepath.Rel(root, dir)
	id := sha256.Sum256([]byte(b.Project + "/" + b.Scope + "/" + b.Copy + "/" + filepath.ToSlash(rel)))
	s := Skill{CandidateID: "candidate:" + hex.EncodeToString(id[:]), Project: b.Project, Scope: b.Scope, Copy: b.Copy, Source: source.Path, Role: source.Role, Path: filepath.ToSlash(rel), Name: filepath.Base(dir), StaticStatus: "PASS", Files: []File{}, Findings: []Finding{}, Layers: map[string]string{"L1": "UNKNOWN", "L2": "UNKNOWN", "L3": "UNKNOWN", "L4": "UNKNOWN"}}
	add := func(status, code, detail string) {
		s.Findings = append(s.Findings, Finding{Status: status, Code: code, Detail: detail})
		if status == "FAIL" {
			s.StaticStatus = "FAIL"
			s.Layers["L1"] = "FAIL"
		} else if status == "WARN" && s.StaticStatus == "PASS" {
			s.StaticStatus = "WARN"
		}
	}
	var body []byte
	total := int64(0)
	seen := map[string]bool{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if isLink(info) {
			add("WARN", "LINK_EXCLUDED", rel)
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != dir && excludedDir(d.Name()) {
				add("SKIP", "GENERATED_OR_CACHE_EXCLUDED", rel)
				return fs.SkipDir
			}
			return nil
		}
		if invalidName(d.Name()) {
			add("FAIL", "INVALID_FILE_NAME", rel)
			return nil
		}
		if sensitiveFile(d.Name()) {
			add("WARN", "SENSITIVE_FILE_EXCLUDED", rel)
			return nil
		}
		key := strings.ToLower(rel)
		if seen[key] {
			add("FAIL", "CASE_COLLISION", rel)
			return nil
		}
		seen[key] = true
		if len(s.Files) >= 2000 || total+info.Size() > 64<<20 {
			return fmt.Errorf("包超出 2000 文件/64 MB 上限")
		}
		data, err := readBounded(root, path, 10<<20)
		if err != nil {
			add("FAIL", "FILE_UNREADABLE", rel+": "+err.Error())
			return nil
		}
		total += int64(len(data))
		sum := sha256.Sum256(data)
		kind := "binary"
		if utf8.Valid(data) && !bytes.ContainsRune(data, 0) {
			kind = "text"
		}
		s.Files = append(s.Files, File{rel, int64(len(data)), hex.EncodeToString(sum[:]), kind})
		if rel == "SKILL.md" {
			body = data
		}
		return nil
	})
	if err != nil {
		add("FAIL", "PACKAGE_SCAN_FAILED", err.Error())
	}
	if body == nil {
		add("FAIL", "SKILL_DOCUMENT_UNREADABLE", "SKILL.md 未成功读取")
	} else {
		front, text, err := frontmatter(body)
		if err != nil {
			add("FAIL", "FRONTMATTER_INVALID", err.Error())
		} else {
			name, nameOK := front["name"].(string)
			desc, descOK := front["description"].(string)
			if !nameOK || !descOK || strings.TrimSpace(name) == "" || strings.TrimSpace(desc) == "" {
				add("FAIL", "NATIVE_METADATA_MISSING", "缺少字符串 name/description，需按实际工具版本协调")
			} else {
				s.Name, s.Description = name, desc
			}
			for _, target := range markdownDestinations(text) {
				if strings.ContainsAny(target, "<>") || strings.Contains(target, "${") || target == "URL" {
					add("WARN", "REFERENCE_TEMPLATE_UNRESOLVED", target)
					continue
				}
				u, err := url.Parse(target)
				if err != nil {
					add("WARN", "LINK_UNKNOWN", "无法解析引用")
					continue
				}
				if u.Scheme != "" || u.Host != "" || u.Path == "" {
					continue
				}
				linked := filepath.Join(dir, filepath.FromSlash(u.Path))
				if !inside(dir, linked) {
					add("WARN", "PROJECT_REFERENCE_UNDECLARED", target)
					continue
				}
				if _, err := checkedPath(root, linked); err != nil {
					add("FAIL", "REFERENCE_MISSING_OR_UNSAFE", target)
				}
			}
		}
	}
	add("UNKNOWN", "SOURCE_ROLE_DECLARED", "来源角色依据清单；尚无生成器/工具发现证据")
	add("UNKNOWN", "DEPENDENCIES_NOT_GOVERNED", "尚未登记 skills.yaml；动态依赖、工具兼容及执行权限未验证")
	add("UNKNOWN", "GENERATION_NOT_CHECKED", "只完成包的有限静态检查；尚未比较生成物，L1 不判完整通过")
	sort.Slice(s.Files, func(i, j int) bool { return s.Files[i].Path < s.Files[j].Path })
	encoded, _ := json.Marshal(s.Files)
	sum := sha256.Sum256(encoded)
	s.PackageDigest = "sha256:" + hex.EncodeToString(sum[:])
	return s
}

// Parsing the Markdown tree excludes fenced/inline code and supports reference
// links and images. Template examples remain explicitly unresolved.
func markdownDestinations(body []byte) []string {
	links := []string{}
	doc := goldmark.DefaultParser().Parse(mdtext.NewReader(body))
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch n := node.(type) {
			case *ast.Link:
				links = append(links, string(n.Destination))
			case *ast.Image:
				links = append(links, string(n.Destination))
			}
		}
		return ast.WalkContinue, nil
	})
	return links
}

func frontmatter(data []byte) (map[string]any, []byte, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	lines := bytes.Split(data, []byte("\n"))
	if len(lines) == 0 || strings.TrimSpace(string(lines[0])) != "---" {
		return nil, nil, fmt.Errorf("缺少 YAML frontmatter")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(string(lines[i])) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, nil, fmt.Errorf("frontmatter 未闭合")
	}
	v, err := config.Parse(bytes.Join(lines[1:end], []byte("\n")))
	if err != nil {
		return nil, nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("frontmatter 必须为 mapping")
	}
	return m, bytes.Join(lines[end+1:], []byte("\n")), nil
}

func splitRelative(path string) []string {
	if path == "." {
		return nil
	}
	return strings.Split(path, string(filepath.Separator))
}
func excludedDir(name string) bool {
	switch strings.ToLower(name) {
	case ".git", "node_modules", "__pycache__", ".venv", ".cache", ".revisions":
		return true
	}
	return false
}
func sensitiveFile(name string) bool {
	n := strings.ToLower(name)
	return n == ".env" || strings.HasPrefix(n, ".env.") || strings.HasSuffix(n, ".pem") || strings.HasSuffix(n, ".key") || n == "credentials.json" || n == "secrets.yaml"
}
