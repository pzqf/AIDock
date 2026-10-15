package plugininstall

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

// ArtifactUsers checks every project installation registry in this Local Root.
// A corrupt or unfinished registry cannot silently lose a retention reference.
func ArtifactUsers(local, artifactPath string) ([]Binding, error) {
	out := []Binding{}
	if err := workstore.CheckLinks(local); err != nil {
		return out, err
	}
	files, err := os.ReadDir(local)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	for _, f := range files {
		if !strings.HasPrefix(f.Name(), "plugin-install-") || !hash(strings.TrimPrefix(f.Name(), "plugin-install-")) {
			continue
		}
		root := filepath.Join(local, f.Name())
		if err := workstore.CheckLinks(root); err != nil {
			return out, err
		}
		data, err := workstore.ReadFile(filepath.Join(root, "binding.json"))
		if err != nil {
			return out, result.Fail("INSTALL_REFERENCE_UNAVAILABLE", "安装登记绑定不可读；保留共享产物并先恢复登记："+err.Error(), 5)
		}
		var binding Binding
		if err = decode(data, &binding); err != nil {
			return out, err
		}
		m, err := New(local, binding)
		if err != nil {
			return out, err
		}
		if m.Store.Root != root {
			return out, result.Fail("INSTALL_REFERENCE_UNAVAILABLE", "安装登记位置与绑定不符", 5)
		}
		state, err := m.State(nil)
		if err != nil {
			return out, err
		}
		if state.Recovery {
			return out, result.Fail("INSTALL_RECOVERY_REQUIRED", "存在未完成的项目安装登记，先恢复再回退共享产物", 6)
		}
		for _, c := range state.Checks {
			if c.Status == "FAIL" {
				return out, result.Fail(c.Code, c.Detail, 5)
			}
		}
		for _, e := range state.Installations {
			if strings.EqualFold(e.Record.Plugin.Artifact.Path, artifactPath) {
				out = append(out, binding)
				break
			}
		}
	}
	return out, nil
}
