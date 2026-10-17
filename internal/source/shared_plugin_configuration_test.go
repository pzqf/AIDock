package source

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginconfig"
	"github.com/pzqf/AIDock/internal/workstore"
)

func sharedPluginFixture(t *testing.T) *Manager {
	t.Helper()
	m, req := installFixture(t)
	b, err := configArtifactManifest(t, m, req.ArtifactPath)
	if err != nil {
		t.Fatal(err)
	}
	b["config_delivery"] = pluginconfig.Delivery
	b["config_schema"] = map[string]any{
		"directory": map[string]any{"type": "directory", "sync": "shared"},
		"languages": map[string]any{"type": "multi-select", "options": []any{"go", "typescript"}, "sync": "shared"},
		"optional":  map[string]any{"type": "string", "nullable": true, "sync": "shared"},
		"message":   map[string]any{"type": "string"},
		"port":      map[string]any{"type": "integer", "sync": "local"},
	}
	write(t, filepath.Join(m.Store.Local, req.ArtifactPath, "plugin.yaml"), jsonBytes(b))
	refreshInstallLock(t, m, req)
	return m
}

func configArtifactManifest(t *testing.T, m *Manager, relative string) (map[string]any, error) {
	t.Helper()
	root := filepath.Join(m.Store.Local, relative)
	tree, err := m.Store.Capture(root)
	if err != nil {
		return nil, err
	}
	b, err := m.Store.Data(tree.Entries["plugin.yaml"])
	if err != nil {
		return nil, err
	}
	var v map[string]any
	err = json.Unmarshal(b, &v)
	return v, err
}

func saveSharedPluginProfile(t *testing.T, m *Manager, values map[string]any, removals []any) {
	t.Helper()
	p, err := m.Profile()
	if err != nil {
		t.Fatal(err)
	}
	p.Profile["config_overrides"] = map[string]any{"unregistered": map[string]any{"machine": "RETAIN_OTHER_PLUGIN"}}
	if values != nil {
		p.Profile["config_overrides"].(map[string]any)["project-memory"] = values
	}
	if removals == nil {
		delete(p.Profile, "config_remove")
	} else {
		p.Profile["config_remove"] = removals
	}
	plan, err := m.ProfilePlan(context.Background(), ProfileEdit{ExpectedSHA256: p.SHA256, Profile: p.Profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), plan.Plan.ID, plan.Plan.Digest); err != nil {
		t.Fatal(err)
	}
}

func portableGroup(t *testing.T, m *Manager) SharedConfiguration {
	t.Helper()
	c, err := m.Shared()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range c.Configurations {
		if v.ID == SharedPluginPrefix+"project-memory" {
			return v
		}
	}
	t.Fatal("portable plugin group missing")
	return SharedConfiguration{}
}

func TestSharedPluginConfigurationDraftPreservesLocalLayersAndOtherBytes(t *testing.T) {
	src, dst := sharedPluginFixture(t), sharedPluginFixture(t)
	dst.Copy = "target-copy"
	saveSharedPluginProfile(t, src, map[string]any{"directory": "${PROJECT}/index", "languages": []any{}, "optional": nil, "port": 8001, "message": "SOURCE_PRIVATE"}, nil)
	saveSharedPluginProfile(t, dst, map[string]any{"directory": "${PROJECT}/old", "languages": []any{"go"}, "optional": "old", "port": 9002, "message": "TARGET_PRIVATE"}, nil)
	saveLocal(t, dst, ProfileEdit{Profile: map[string]any{"schema_version": 2, "name": "local", "config_overrides": map[string]any{"project-memory": map[string]any{"languages": []any{"typescript"}}}}})
	selected := portableGroup(t, src)
	if strings.Contains(string(jsonBytes(selected)), "SOURCE_PRIVATE") || strings.Contains(string(jsonBytes(selected)), "8001") {
		t.Fatal("local fields entered portable unit")
	}
	before, _ := dst.Store.Capture(dst.Store.Root)
	ls, _ := dst.localProfileStore()
	localBefore, _ := ls.Capture(ls.Root)
	after, _, err := dst.DraftSharedConfiguration(before, nil, []SharedConfiguration{selected}, src.Store)
	if err != nil {
		t.Fatal(err)
	}
	_, p, err := dst.load(&after)
	if err != nil {
		t.Fatal(err)
	}
	values := p["config_overrides"].(map[string]any)["project-memory"].(map[string]any)
	if values["port"] != float64(9002) && values["port"] != 9002 {
		t.Fatal(values)
	}
	if values["message"] != "TARGET_PRIVATE" || values["optional"] != nil || string(jsonBytes(values["languages"])) != "[]\n" || values["directory"] != "${PROJECT}/index" {
		t.Fatal(values)
	}
	if p["config_overrides"].(map[string]any)["unregistered"].(map[string]any)["machine"] != "RETAIN_OTHER_PLUGIN" {
		t.Fatal("another plugin changed")
	}
	for path, e := range before.Entries {
		if path != "profile.yaml" && after.Entries[path] != e {
			t.Fatal("unrelated byte changed", path)
		}
	}
	idx, _, _ := dst.load(&after)
	preflight, err := dst.preflight(after, idx, p)
	if err != nil {
		t.Fatal(err)
	}
	languages := preflight.ProfileReport.Effective["config_overrides"].(map[string]any)["project-memory"].(map[string]any)["languages"].([]any)
	if len(languages) != 1 || languages[0] != "typescript" {
		t.Fatal("target Local precedence lost")
	}
	localAfter, _ := ls.Capture(ls.Root)
	now, _ := dst.Store.Capture(dst.Store.Root)
	if workstore.Digest(localBefore) != workstore.Digest(localAfter) || workstore.Digest(now) != workstore.Digest(before) {
		t.Fatal("candidate wrote source or Local layer")
	}
	// Absence in the selected shared unit removes only its owned override.
	delete(selected.Profile["config_overrides"].(map[string]any)["project-memory"].(map[string]any), "optional")
	selected.Digest = SharedConfigurationDigest(selected)
	after, _, err = dst.DraftSharedConfiguration(before, nil, []SharedConfiguration{selected}, src.Store)
	if err != nil {
		t.Fatal(err)
	}
	_, p, _ = dst.load(&after)
	values = p["config_overrides"].(map[string]any)["project-memory"].(map[string]any)
	if _, ok := values["optional"]; ok {
		t.Fatal("shared field removal not adopted")
	}
}

func TestSharedPluginConfigurationBlocksForgeryDriftSecretsAndAncestorRemoval(t *testing.T) {
	src, dst := sharedPluginFixture(t), sharedPluginFixture(t)
	saveSharedPluginProfile(t, src, map[string]any{"optional": "public"}, nil)
	good := portableGroup(t, src)
	lib := rulesLibrary(t, dst)
	defaults := ruleProfile("defaults")
	defaults["config_overrides"] = map[string]any{"project-memory": map[string]any{"optional": "inherited"}}
	write(t, filepath.Join(lib.Store.Root, "profile.yaml"), jsonBytes(defaults))
	dst.Library, dst.ProfileLibrary = lib, true
	saveSharedPluginProfile(t, dst, nil, []any{"/project-memory"})
	before, _ := dst.Store.Capture(dst.Store.Root)
	_, _, err := dst.DraftSharedConfiguration(before, nil, []SharedConfiguration{good}, src.Store)
	code(t, err, "PLUGIN_SHARED_LOCAL_REMOVAL_CONFLICT")
	saveSharedPluginProfile(t, dst, map[string]any{"optional": "local"}, nil)
	before, _ = dst.Store.Capture(dst.Store.Root)
	forged := good
	forged.Plugin = &SharedPluginConfiguration{Name: good.Plugin.Name, ManifestSHA256: good.Plugin.ManifestSHA256, ArtifactSHA256: good.Plugin.ArtifactSHA256, Fields: []string{"message"}}
	forged.Profile = map[string]any{"config_overrides": map[string]any{"project-memory": map[string]any{"message": "FORGED_PRIVATE_FIELD"}}}
	forged.Digest = SharedConfigurationDigest(forged)
	_, _, err = dst.DraftSharedConfiguration(before, nil, []SharedConfiguration{forged}, src.Store)
	code(t, err, "PLUGIN_SHARED_CONTRACT_MISMATCH")
	forged = good
	forged.Plugin = &SharedPluginConfiguration{Name: good.Plugin.Name, ManifestSHA256: strings.Repeat("0", 64), ArtifactSHA256: good.Plugin.ArtifactSHA256, Fields: good.Plugin.Fields}
	forged.Digest = SharedConfigurationDigest(forged)
	_, _, err = dst.DraftSharedConfiguration(before, nil, []SharedConfiguration{forged}, src.Store)
	code(t, err, "PLUGIN_SHARED_CONTRACT_MISMATCH")
	saveSharedPluginProfile(t, src, map[string]any{"optional": "sk-PRIVATE_SENTINEL_LONG_KEY"}, nil)
	blocked := portableGroup(t, src)
	if blocked.Blocked != "PLUGIN_SHARED_VALUE_BLOCKED" || strings.Contains(string(jsonBytes(blocked)), "PRIVATE_SENTINEL") {
		t.Fatal(blocked)
	}
	_, _, err = dst.DraftSharedConfiguration(before, nil, []SharedConfiguration{blocked}, src.Store)
	code(t, err, "PLUGIN_SHARED_VALUE_BLOCKED")
	now, _ := dst.Store.Capture(dst.Store.Root)
	if workstore.Digest(now) != workstore.Digest(before) {
		t.Fatal("rejected candidate wrote target")
	}
}
