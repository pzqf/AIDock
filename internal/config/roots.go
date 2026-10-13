package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type RootOptions struct {
	FlagHome     string
	EnvHome      string
	UserHome     string
	WorkingDir   string
	SettingsPath string
}

type Roots struct {
	Home   string `json:"home"`
	Config string `json:"config_root"`
	Local  string `json:"local_root"`
	Source string `json:"source"`
}

// ResolveRoots reads settings but creates no directories. Settings are always
// located outside the selected home so there is no recursive resolution.
func ResolveRoots(o RootOptions) (Roots, error) {
	if o.WorkingDir == "" {
		o.WorkingDir, _ = os.Getwd()
	}
	if o.UserHome == "" {
		o.UserHome, _ = os.UserHomeDir()
	}
	settings := o.SettingsPath
	if settings == "" {
		settings = filepath.Join(o.UserHome, ".aidock", "settings.yaml")
	}
	var saved struct {
		SchemaVersion int    `json:"schema_version"`
		Home          string `json:"home"`
		Config        string `json:"config_root"`
		Local         string `json:"local_root"`
	}
	if b, err := ReadFile(settings); err == nil {
		v, err := Validate("settings", b)
		if err != nil {
			return Roots{}, fmt.Errorf("用户配置无效: %w", err)
		}
		j, _ := json.Marshal(v)
		if err = json.Unmarshal(j, &saved); err != nil {
			return Roots{}, err
		}
	} else if !os.IsNotExist(err) {
		return Roots{}, err
	}
	home, source, base := filepath.Join(o.UserHome, ".aidock"), "default", o.WorkingDir
	if saved.Home != "" {
		home, source, base = saved.Home, "settings", filepath.Dir(settings)
	}
	if o.EnvHome != "" {
		home, source, base = o.EnvHome, "environment", o.WorkingDir
	}
	if o.FlagHome != "" {
		home, source, base = o.FlagHome, "flag", o.WorkingDir
	}
	home, err := absolute(home, base)
	if err != nil {
		return Roots{}, err
	}
	configRoot, localRoot := filepath.Join(home, "config"), filepath.Join(home, "local")
	if saved.Config != "" {
		configRoot, err = absolute(saved.Config, home)
		if err != nil {
			return Roots{}, err
		}
	}
	if saved.Local != "" {
		localRoot, err = absolute(saved.Local, home)
		if err != nil {
			return Roots{}, err
		}
	}
	// Existing ancestors are resolved even when the final directories do not exist.
	resolvedConfig, err := resolveAncestors(configRoot)
	if err != nil {
		return Roots{}, err
	}
	resolvedLocal, err := resolveAncestors(localRoot)
	if err != nil {
		return Roots{}, err
	}
	if within(resolvedConfig, resolvedLocal) || within(resolvedLocal, resolvedConfig) {
		return Roots{}, fmt.Errorf("Config Root 与 Local Root 重叠")
	}
	return Roots{Home: home, Config: resolvedConfig, Local: resolvedLocal, Source: source}, nil
}

func absolute(path, base string) (string, error) {
	if strings.Contains(path, "${") {
		return "", fmt.Errorf("数据根包含未解析变量")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	return filepath.Abs(filepath.Clean(path))
}

func resolveAncestors(path string) (string, error) {
	parent, tail := path, []string{}
	for {
		if _, err := os.Lstat(parent); err == nil {
			real, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return "", err
			}
			for i := len(tail) - 1; i >= 0; i-- {
				real = filepath.Join(real, tail[i])
			}
			return real, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", fmt.Errorf("无法解析路径 %s", path)
		}
		tail = append(tail, filepath.Base(parent))
		parent = next
	}
}

func within(parent, child string) bool {
	// Conservative case folding is also safe for read-only non-Windows builds.
	rel, err := filepath.Rel(strings.ToLower(parent), strings.ToLower(child))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
