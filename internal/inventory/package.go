package inventory

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// AnalyzePackage is limited to one explicit package. It does not execute files.
func AnalyzePackage(ctx context.Context, dir string) Skill {
	return scanPackage(ctx, dir, dir, Binding{Project: "owned-source", Scope: "package", Copy: "local"}, Source{Path: filepath.Base(dir), Role: "owned"})
}

// InspectPackage checks a captured package without reading a live source again.
// Markdown examples in code are excluded by the same AST as inventory scans.
func InspectPackage(files map[string][]byte) (string, []Finding, error) {
	findings := []Finding{}
	body, ok := files["SKILL.md"]
	if !ok {
		return "", findings, fmt.Errorf("缺少 SKILL.md")
	}
	front, _, e := frontmatter(body)
	if e != nil {
		return "", findings, e
	}
	name, _ := front["name"].(string)
	description, _ := front["description"].(string)
	if name == "" || description == "" {
		return "", findings, fmt.Errorf("缺少 name/description")
	}
	for file, data := range files {
		if !strings.HasSuffix(strings.ToLower(file), ".md") {
			continue
		}
		if file == "SKILL.md" {
			_, data, e = frontmatter(data)
			if e != nil {
				return "", findings, e
			}
		}
		for _, target := range markdownDestinations(data) {
			u, e := url.Parse(target)
			if e != nil {
				return "", findings, e
			}
			if u.Scheme != "" || u.Host != "" || u.Path == "" {
				continue
			}
			if strings.Contains(target, "${") || strings.ContainsAny(target, "<>") {
				findings = append(findings, Finding{"WARN", "REFERENCE_TEMPLATE_UNRESOLVED", file + ": " + target})
				continue
			}
			resolved := path.Clean(path.Join(path.Dir(file), u.Path))
			if resolved == ".." || strings.HasPrefix(resolved, "../") || strings.HasPrefix(u.Path, "/") {
				findings = append(findings, Finding{"WARN", "PROJECT_REFERENCE_UNDECLARED", file + ": " + target})
				continue
			}
			if _, ok = files[resolved]; !ok {
				return "", findings, fmt.Errorf("REFERENCE_MISSING_OR_UNSAFE: %s 引用 %s", file, target)
			}
		}
	}
	return name, findings, nil
}
