package source

import (
	"encoding/json"
	"path/filepath"
	"sort"

	"github.com/pzqf/AIDock/internal/config"
	"github.com/pzqf/AIDock/internal/pluginconfig"
	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

const SharedMCPPrefix = "mcp-definition/"

// The artifact's physical path is normalized out of shared identity. A target
// keeps its own already-matching mapping or prepares a separate version path.
// Artifact bytes, installations, origin evidence and grants never travel here.
type SharedMCPDefinition struct {
	Name       string             `json:"name"`
	Definition map[string]any     `json:"definition,omitempty"`
	Lock       *pluginlock.Plugin `json:"lock,omitempty"`
}

func sharedArtifactPath(p pluginlock.Plugin) string {
	return "mcp-artifacts/" + p.Name + "--" + p.Artifact.SHA256
}

func portableMCPDefinition(server map[string]any, lock pluginlock.Plugin, project string) error {
	bad := func() error {
		return result.Fail("MCP_SHARED_VALUE_BLOCKED", "MCP 声明含本机路径、回环地址、疑似凭据或不支持的传输；未提供共享正文", 4)
	}
	if server["transport"] != "stdio" {
		return bad()
	}
	permissions, _ := server["permissions"].(map[string]any)
	fs, _ := permissions["fs"].(map[string]any)
	for _, raw := range array(fs["paths"]) {
		s, ok := raw.(string)
		if !ok || !pluginconfig.SharedProjectPath(s, project) {
			return bad()
		}
	}
	net, _ := permissions["net"].(map[string]any)
	for _, raw := range array(net["allow"]) {
		s, ok := raw.(string)
		if !ok || !pluginconfig.SharedText(s) {
			return bad()
		}
	}
	for _, raw := range lock.Source {
		if s, ok := raw.(string); ok && !pluginconfig.SharedText(s) {
			return bad()
		}
	}
	encoded, _ := json.Marshal(lock.Dependencies)
	if pluginconfig.CredentialShape(string(encoded)) {
		return bad()
	}
	return nil
}

func validateSharedMCPConfiguration(v SharedConfiguration) error {
	bad := func() error {
		return result.Fail("REPLICA_CONFIGURATION_INVALID", "共享 MCP 身份、定义或精确锁不一致", 5)
	}
	p := v.MCP
	if p == nil || !sharedPluginName.MatchString(p.Name) || v.ID != SharedMCPPrefix+p.Name || v.Name != "MCP 共享定义与版本锁 · "+p.Name || v.Profile == nil || len(v.Profile) != 0 || v.Plugin != nil || len(v.Rules) != 0 || v.Digest != SharedConfigurationDigest(v) {
		return bad()
	}
	if v.Blocked != "" {
		if v.Blocked != "MCP_SHARED_VALUE_BLOCKED" && v.Blocked != "MCP_SHARED_LOCK_UNVERIFIED" {
			return bad()
		}
		if p.Definition != nil || p.Lock != nil {
			return bad()
		}
		return nil
	}
	if p.Definition == nil || p.Lock == nil || p.Definition["name"] != p.Name || p.Lock.Name != p.Name || p.Lock.Artifact.Path != sharedArtifactPath(*p.Lock) || p.Lock.DefinitionSHA256 != pluginlock.Definition(p.Definition) {
		return bad()
	}
	if _, err := config.Validate("mcp", jsonBytes(map[string]any{"schema_version": 1, "servers": []any{p.Definition}})); err != nil {
		return bad()
	}
	if _, err := pluginlock.Parse(jsonBytes(pluginlock.Lock{Version: 1, Plugins: []pluginlock.Plugin{*p.Lock}})); err != nil {
		return bad()
	}
	// Values are checked again against the destination's actual project path
	// before adoption; this structural check never opens a source program.
	if p.Definition["transport"] != "stdio" {
		return bad()
	}
	return nil
}

func (m *Manager) sharedMCPConfigurations(t workstore.Tree, state MCPLockState) ([]SharedConfiguration, []string, error) {
	out, notes := []SharedConfiguration{}, []string{}
	conf, err := m.mcpConfig(t)
	if err != nil {
		return out, notes, err
	}
	if len(array(conf["servers"])) == 0 {
		return out, notes, nil
	}
	if !state.Present {
		notes = append(notes, "mcp.yaml：尚无精确安装锁，保留各副本定义，不提供 MCP 共享组")
		return out, notes, nil
	}
	lock, err := pluginlock.Parse([]byte(state.Document))
	if err != nil {
		notes = append(notes, "plugins.lock：格式无效，未提供 MCP 共享组")
		return out, notes, nil
	}
	byName := map[string]pluginlock.Plugin{}
	for _, p := range lock.Plugins {
		byName[p.Name] = p
	}
	verified := map[string]bool{}
	for _, p := range state.Manifests {
		verified[p.Name] = p.ManifestStatus == "PASS" && p.ArtifactStatus == "PASS" && p.PermissionStatus == "PASS"
	}
	for _, raw := range array(conf["servers"]) {
		server := raw.(map[string]any)
		name := server["name"].(string)
		p, exists := byName[name]
		if !exists {
			notes = append(notes, "mcp.yaml/"+name+"：缺少精确安装锁，保留各副本原值")
			continue
		}
		v := SharedConfiguration{ID: SharedMCPPrefix + name, Name: "MCP 共享定义与版本锁 · " + name, Profile: map[string]any{}, MCP: &SharedMCPDefinition{Name: name}}
		if !verified[name] || p.DefinitionSHA256 != pluginlock.Definition(server) {
			v.Blocked = "MCP_SHARED_LOCK_UNVERIFIED"
		} else if err := portableMCPDefinition(server, p, filepath.Dir(m.Store.Root)); err != nil {
			v.Blocked = "MCP_SHARED_VALUE_BLOCKED"
		} else {
			p.Artifact.Path = sharedArtifactPath(p)
			v.MCP.Definition = cloneProfile(server)
			v.MCP.Lock = &p
		}
		if v.Blocked != "" {
			notes = append(notes, v.ID+"："+v.Blocked+"；整组阻断，未提供定义和锁正文")
		}
		v.Digest = SharedConfigurationDigest(v)
		if err := ValidateSharedConfiguration(v); err != nil {
			return out, notes, err
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, notes, nil
}

func (m *Manager) draftSharedMCPConfigurations(after *workstore.Tree, selected []SharedConfiguration) error {
	chosen := []SharedConfiguration{}
	seen := map[string]bool{}
	for _, v := range selected {
		if v.MCP != nil || len(v.ID) >= len(SharedMCPPrefix) && v.ID[:len(SharedMCPPrefix)] == SharedMCPPrefix {
			if err := ValidateSharedConfiguration(v); err != nil {
				return err
			}
			if v.Blocked != "" {
				return result.Fail(v.Blocked, "MCP 共享组未通过可携带性或完整锁核验，未采用源", 4)
			}
			if seen[v.MCP.Name] {
				return result.Fail("REPLICA_CONFIGURATION_INVALID", "MCP 共享组重复", 5)
			}
			seen[v.MCP.Name] = true
			chosen = append(chosen, v)
		}
	}
	if len(chosen) == 0 {
		return nil
	}
	conf, err := m.mcpConfig(*after)
	if err != nil {
		return err
	}
	lock := pluginlock.Lock{Version: 1, Plugins: []pluginlock.Plugin{}}
	if e, ok := after.Entries["plugins.lock"]; ok {
		body, err := m.Store.Data(e)
		if err != nil {
			return err
		}
		lock, err = pluginlock.Parse(body)
		if err != nil {
			return err
		}
	}
	servers := map[string]map[string]any{}
	for _, raw := range array(conf["servers"]) {
		v := raw.(map[string]any)
		servers[v["name"].(string)] = v
	}
	plugins := map[string]pluginlock.Plugin{}
	for _, p := range lock.Plugins {
		plugins[p.Name] = p
	}
	for _, v := range chosen {
		p := *v.MCP.Lock
		if err := portableMCPDefinition(v.MCP.Definition, p, filepath.Dir(m.Store.Root)); err != nil {
			return err
		}
		if current, ok := plugins[p.Name]; ok && current.Artifact.SHA256 == p.Artifact.SHA256 {
			p.Artifact.Path = current.Artifact.Path
		} else {
			p.Artifact.Path = sharedArtifactPath(p)
		}
		servers[p.Name] = cloneProfile(v.MCP.Definition)
		plugins[p.Name] = p
	}
	names := []string{}
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	serverList := []any{}
	for _, name := range names {
		serverList = append(serverList, servers[name])
	}
	conf["servers"] = serverList
	names = nil
	for name := range plugins {
		names = append(names, name)
	}
	sort.Strings(names)
	lock.Plugins = nil
	for _, name := range names {
		lock.Plugins = append(lock.Plugins, plugins[name])
	}
	for _, item := range []struct {
		path, kind string
		body       []byte
	}{{"mcp.yaml", "mcp", jsonBytes(conf)}, {"plugins.lock", "plugins-lock", jsonBytes(lock)}} {
		if _, err := config.Validate(item.kind, item.body); err != nil {
			return err
		}
		mode := uint32(0644)
		if old, ok := after.Entries[item.path]; ok {
			mode = old.Mode
			if data, err := m.Store.Data(old); err == nil && string(data) == string(item.body) {
				continue
			}
		}
		if err := m.Store.PutFile(after, item.path, item.body, mode); err != nil {
			return err
		}
	}
	return nil
}
