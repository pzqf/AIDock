package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginlock"
	"github.com/pzqf/AIDock/internal/workstore"
)

func copyPreparedArtifact(t *testing.T, m *Manager, from, to string) {
	t.Helper()
	tree, err := m.Store.Capture(filepath.Join(m.Store.Local, from))
	if err != nil {
		t.Fatal(err)
	}
	for name, e := range tree.Entries {
		target := filepath.Join(m.Store.Local, to, filepath.FromSlash(name))
		if e.Dir {
			if err := os.MkdirAll(target, 0755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		body, err := m.Store.Data(e)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, body, os.FileMode(e.Mode)); err != nil {
			t.Fatal(err)
		}
	}
}
func mcpSharedGroup(t *testing.T, m *Manager) SharedConfiguration {
	t.Helper()
	c, err := m.Shared()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range c.Configurations {
		if v.ID == SharedMCPPrefix+"project-memory" {
			return v
		}
	}
	t.Fatal("MCP shared unit missing")
	return SharedConfiguration{}
}
func editMCPFixture(t *testing.T, m *Manager, edit func(map[string]any)) {
	t.Helper()
	s, err := m.MCP()
	if err != nil {
		t.Fatal(err)
	}
	edit(s.Config)
	p, err := m.MCPPlan(context.Background(), MCPEdit{ExpectedSHA256: s.SHA256, Config: s.Config})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyMCP(context.Background(), p.Plan.ID, p.Plan.Digest); err != nil {
		t.Fatal(err)
	}
}
func TestSharedMCPDefinitionLockPreservesDestinationMappingAndUnrelatedDeclarations(t *testing.T) {
	src, _ := installFixture(t)
	dst, request := installFixture(t)
	copyPreparedArtifact(t, dst, request.ArtifactPath, "mcp-artifacts/target-mapping")
	request.ArtifactPath = "mcp-artifacts/target-mapping"
	refreshInstallLock(t, dst, request)
	sg, tg := mcpSharedGroup(t, src), mcpSharedGroup(t, dst)
	if sg.Digest != tg.Digest || sg.MCP.Lock.Artifact.Path == request.ArtifactPath {
		t.Fatal("machine path entered shared identity", sg.MCP.Lock.Artifact.Path, tg.MCP.Lock.Artifact.Path)
	}
	editMCPFixture(t, src, func(v map[string]any) { v["servers"].([]any)[0].(map[string]any)["enabled"] = true })
	editMCPFixture(t, dst, func(v map[string]any) {
		extra := cloneProfile(v["servers"].([]any)[0].(map[string]any))
		extra["name"] = "unrelated-service"
		extra["enabled"] = false
		v["servers"] = append(v["servers"].([]any), extra)
	})
	sg = mcpSharedGroup(t, src)
	before := capture(t, dst)
	after, _, err := dst.DraftSharedConfiguration(before, nil, []SharedConfiguration{sg}, src.Store)
	if err != nil {
		t.Fatal(err)
	}
	conf, err := dst.mcpConfig(after)
	if err != nil {
		t.Fatal(err)
	}
	if len(conf["servers"].([]any)) != 2 {
		t.Fatal("unrelated definition lost")
	}
	for _, raw := range conf["servers"].([]any) {
		v := raw.(map[string]any)
		if v["name"] == "project-memory" && v["enabled"] != true {
			t.Fatal(v)
		}
	}
	body, err := dst.Store.Data(after.Entries["plugins.lock"])
	if err != nil {
		t.Fatal(err)
	}
	lock, err := pluginlock.Parse(body)
	if err != nil || lock.Plugins[0].Artifact.Path != request.ArtifactPath {
		t.Fatal(lock, err)
	}
	for path, entry := range before.Entries {
		if path != "mcp.yaml" && path != "plugins.lock" && after.Entries[path] != entry {
			t.Fatal("unrelated source byte changed", path)
		}
	}
	unchangedManager(t, dst, before)
}
func TestSharedMCPNewVersionUsesSeparateTargetPathAndDoesNotPrepareOrInstall(t *testing.T) {
	src, request := installFixture(t)
	dst, _ := installFixture(t)
	oldTree, err := dst.Store.Capture(filepath.Join(dst.Store.Local, "mcp-artifacts/memory"))
	if err != nil {
		t.Fatal(err)
	}
	copyPreparedArtifact(t, src, request.ArtifactPath, "mcp-artifacts/publisher-new-version")
	request.ArtifactPath = "mcp-artifacts/publisher-new-version"
	request.Version = "2.0.0"
	request.Runtime.Version = "2.0.0"
	request.Runtime.Constraint = "=2.0.0"
	manifest, err := configArtifactManifest(t, src, request.ArtifactPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest["version"] = "2.0.0"
	write(t, filepath.Join(src.Store.Local, request.ArtifactPath, "plugin.yaml"), jsonBytes(manifest))
	refreshInstallLock(t, src, request)
	v := mcpSharedGroup(t, src)
	before := capture(t, dst)
	after, _, err := dst.DraftSharedConfiguration(before, nil, []SharedConfiguration{v}, src.Store)
	if err != nil {
		t.Fatal(err)
	}
	body, err := dst.Store.Data(after.Entries["plugins.lock"])
	if err != nil {
		t.Fatal(err)
	}
	lock, err := pluginlock.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	p := lock.Plugins[0]
	if p.Version != "2.0.0" || p.Artifact.Path == request.ArtifactPath || p.Artifact.Path == "mcp-artifacts/memory" || p.Artifact.Path != sharedArtifactPath(p) {
		t.Fatal(p.Artifact.Path)
	}
	if _, err := os.Stat(filepath.Join(dst.Store.Local, p.Artifact.Path)); !os.IsNotExist(err) {
		t.Fatal("source proposal materialized target artifact", err)
	}
	currentOld, err := dst.Store.Capture(filepath.Join(dst.Store.Local, "mcp-artifacts/memory"))
	if err != nil || workstore.Digest(currentOld) != workstore.Digest(oldTree) {
		t.Fatal("old version modified", err)
	}
	unchangedManager(t, dst, before)
}
func TestSharedMCPBlocksNonportableOrForgedDefinitionWithoutEchoingValues(t *testing.T) {
	m, _ := installFixture(t)
	good := mcpSharedGroup(t, m)
	before := capture(t, m)
	for _, kind := range []string{"lock", "local-path", "transport", "mixed-kind"} {
		t.Run(kind, func(t *testing.T) {
			v := good
			v.MCP = &SharedMCPDefinition{Name: good.MCP.Name, Definition: cloneProfile(good.MCP.Definition), Lock: new(pluginlock.Plugin)}
			*v.MCP.Lock = *good.MCP.Lock
			switch kind {
			case "lock":
				v.MCP.Lock.DefinitionSHA256 = strings.Repeat("0", 64)
			case "local-path":
				v.MCP.Definition["source"] = map[string]any{"type": "local", "path": "C:/PRIVATE_MACHINE_PATH"}
				v.MCP.Lock.Source = v.MCP.Definition["source"].(map[string]any)
				v.MCP.Lock.DefinitionSHA256 = pluginlock.Definition(v.MCP.Definition)
			case "transport":
				v.MCP.Definition["transport"] = "http"
				v.MCP.Lock.DefinitionSHA256 = pluginlock.Definition(v.MCP.Definition)
			case "mixed-kind":
				v.Plugin = &SharedPluginConfiguration{Name: "foreign"}
			}
			v.Digest = SharedConfigurationDigest(v)
			_, _, err := m.DraftSharedConfiguration(before, nil, []SharedConfiguration{v}, m.Store)
			if err == nil || strings.Contains(err.Error(), "PRIVATE_MACHINE_PATH") {
				t.Fatal(err)
			}
		})
	}
	unchangedManager(t, m, before)
}
