// Package inventory performs bounded, read-only scans of explicitly selected sources.
package inventory

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pzqf/AIDock/internal/config"
)

type Source struct {
	Path      string `json:"path"`
	Role      string `json:"role"`
	Generator string `json:"generator,omitempty"`
}

type Binding struct {
	Project     string   `json:"project"`
	ProjectName string   `json:"project_name,omitempty"`
	Scope       string   `json:"scope"`
	Copy        string   `json:"copy"`
	Root        string   `json:"root"`
	Sources     []Source `json:"sources"`
}

type Manifest struct {
	SchemaVersion int       `json:"schema_version"`
	Name          string    `json:"name,omitempty"`
	Bindings      []Binding `json:"bindings"`
}

func Load(path string) (Manifest, error) {
	b, err := config.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	v, err := config.Validate("inventory", b)
	if err != nil {
		return Manifest{}, err
	}
	j, _ := json.Marshal(v)
	var m Manifest
	if err = json.Unmarshal(j, &m); err != nil {
		return m, err
	}
	seen := map[string]bool{}
	for i := range m.Bindings {
		binding := &m.Bindings[i]
		key := binding.Project + "/" + binding.Scope + "/" + binding.Copy
		if seen[key] {
			return m, fmt.Errorf("重复绑定 %s", key)
		}
		seen[key] = true
		if !filepath.IsAbs(binding.Root) {
			binding.Root = filepath.Join(filepath.Dir(path), binding.Root)
		}
		binding.Root, err = filepath.Abs(binding.Root)
		if err != nil {
			return m, err
		}
		for n, source := range binding.Sources {
			if err = safeRelative(source.Path); err != nil {
				return m, err
			}
			for _, earlier := range binding.Sources[:n] {
				if inside(earlier.Path, source.Path) || inside(source.Path, earlier.Path) {
					return m, fmt.Errorf("源目录重叠: %s / %s", earlier.Path, source.Path)
				}
			}
		}
	}
	return m, nil
}

func safeRelative(path string) error {
	if path == "" || path == "." || strings.ContainsAny(path, "\\:") || filepath.IsAbs(path) {
		return fmt.Errorf("源必须为明确的相对目录: %q", path)
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." || part == "" || invalidName(part) {
			return fmt.Errorf("非法相对路径 %q", path)
		}
	}
	return nil
}

func inside(root, path string) bool {
	rel, err := filepath.Rel(strings.ToLower(filepath.Clean(root)), strings.ToLower(filepath.Clean(path)))
	return err == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func invalidName(name string) bool {
	if strings.TrimRight(name, ". ") != name || strings.ContainsAny(name, ":<>\"|?*") {
		return true
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	return len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9'
}
