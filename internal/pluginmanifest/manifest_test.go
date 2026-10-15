package pluginmanifest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginlock"
)

func put(t *testing.T, root, rel string, b []byte) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0755); err != nil {
		t.Fatal(err)
	}
}

func manifestFixture(t *testing.T, change func(map[string]any), wrapper bool) (string, map[string]any, pluginlock.Plugin) {
	t.Helper()
	local := t.TempDir()
	root := filepath.Join(local, "mcp-artifacts", "fixture")
	source := map[string]any{"type": "npm", "package": "@fixture/tool", "version": "1.2.3"}
	permissions := map[string]any{"fs": map[string]any{"paths": []any{"${PROJECT}", "${HOME}/docs"}, "mode": "ro"}, "net": map[string]any{"allow": []any{}, "default": "deny"}, "exec": false}
	server := map[string]any{"name": "project-alias", "source": source, "permissions": permissions, "entrypoint": "main.js", "tier": 1, "enabled": false, "transport": "stdio"}
	manifest := map[string]any{"schema_version": 1, "name": "publisher-tool", "type": "mcp-server", "version": "1.2.3", "source": source, "permissions": permissions, "dependencies": []any{map[string]any{"ecosystem": "npm", "name": "child", "version": "2.0.0"}}}
	if change != nil {
		change(manifest)
	}
	if manifest != nil {
		put(t, root, "plugin.yaml", bytesOf(manifest))
	}
	put(t, root, "main.js", []byte("// fixture: never run"))
	put(t, root, "runtime", []byte("never execute fixture"))
	main := map[string]any{"name": "@fixture/tool", "version": "1.2.3", "dependencies": map[string]string{"child": "^2.0.0"}}
	packages := map[string]any{}
	if wrapper {
		packages[""] = map[string]any{"name": "wrapper", "version": "0.1.0", "dependencies": map[string]string{"@fixture/tool": "1.2.3"}}
		packages["node_modules/@fixture/tool"] = main
		packages["node_modules/@fixture/tool/node_modules/child"] = map[string]any{"name": "child", "version": "2.0.0", "dependencies": map[string]string{"leaf": "3.0.0"}}
		packages["node_modules/@fixture/tool/node_modules/leaf"] = map[string]any{"name": "leaf", "version": "3.0.0"}
	} else {
		packages[""] = main
		packages["node_modules/child"] = map[string]any{"name": "child", "version": "2.0.0", "dependencies": map[string]string{"leaf": "3.0.0"}}
		packages["node_modules/leaf"] = map[string]any{"name": "leaf", "version": "3.0.0"}
	}
	for path, raw := range packages {
		meta := raw.(map[string]any)
		put(t, root, filepath.ToSlash(filepath.Join(path, "package.json")), bytesOf(meta))
		if path != "" {
			meta["resolved"] = "https://registry.example.invalid/package.tgz"
			meta["integrity"] = "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, 64))
		}
	}
	put(t, root, "package-lock.json", bytesOf(map[string]any{"lockfileVersion": 3, "packages": packages}))
	prep := pluginlock.Preparation{Name: "project-alias", ArtifactPath: "mcp-artifacts/fixture", Runtime: pluginlock.Runtime{Kind: "native", Version: "1.0.0", Constraint: "=1.0.0", Executable: "runtime"}}
	p, err := pluginlock.Prepare(context.Background(), local, server, prep)
	if err != nil {
		t.Fatal(err)
	}
	return local, server, p
}

func inspect(t *testing.T, local string, server map[string]any, p pluginlock.Plugin) Report {
	t.Helper()
	c, observed, err := pluginlock.VerifyObserved(context.Background(), local, p, server)
	if err != nil {
		t.Fatal(err)
	}
	return Inspect(p, server, c, observed)
}

func has(r Report, code string) bool {
	for _, c := range r.Checks {
		if c.Code == code {
			return true
		}
	}
	return false
}

func TestManifestInspectsRootAndWrapperDirectDependenciesWithoutInstalling(t *testing.T) {
	for _, wrapper := range []bool{false, true} {
		t.Run(map[bool]string{false: "root", true: "wrapper"}[wrapper], func(t *testing.T) {
			local, s, p := manifestFixture(t, nil, wrapper)
			r := inspect(t, local, s, p)
			if r.Status != "PASS" || r.ManifestStatus != "PASS" || r.PermissionStatus != "PASS" || r.DependencyStatus != "PASS" || r.Manifest.Name != "publisher-tool" || len(*r.Manifest.Dependencies) != 1 {
				t.Fatalf("%+v", r)
			}
			if r.InstallationState != "PREPARED" || r.SBOMStatus != "UNKNOWN" || r.ExecutionReady {
				t.Fatal(r)
			}
			if len(p.Dependencies) < 2 {
				t.Fatal("fixture lost transitive dependencies")
			}
			c, _ := pluginlock.Verify(context.Background(), local, p, s)
			if c.TrustLevel != "L0" || c.ExecutionReady {
				t.Fatal(c)
			}
		})
	}
}

func TestManifestCredentialsMustMatchProjectDeclarationAsASet(t *testing.T) {
	local, s, p := manifestFixture(t, func(m map[string]any) { m["secrets"] = []any{"alpha_key", "beta_key"} }, false)
	s["secrets"] = []any{"beta_key", "alpha_key"}
	p.DefinitionSHA256 = pluginlock.Definition(s)
	r := inspect(t, local, s, p)
	if r.Status != "PASS" || !has(r, "MCP_MANIFEST_SECRETS_MATCH") || len(r.Manifest.Secrets) != 2 {
		t.Fatal(r)
	}
	s["secrets"] = []any{"alpha_key"}
	p.DefinitionSHA256 = pluginlock.Definition(s)
	r = inspect(t, local, s, p)
	if r.Status != "FAIL" || !has(r, "MCP_MANIFEST_SECRETS_MISMATCH") {
		t.Fatal(r)
	}
	for _, bad := range []any{"private_key", []any{"alpha_key", 1}, []any{"alpha_key", "alpha_key"}} {
		s["secrets"] = bad
		p.DefinitionSHA256 = pluginlock.Definition(s)
		r = inspect(t, local, s, p)
		if r.Status != "FAIL" || !has(r, "MCP_MANIFEST_SECRETS_MISMATCH") {
			t.Fatal("invalid declaration accepted")
		}
	}
}

func TestManifestRejectsContradictionsAndKeepsMissingEvidenceUnknown(t *testing.T) {
	for _, tc := range []struct {
		name, status, code string
		change             func(map[string]any)
	}{
		{"type", "FAIL", "MCP_MANIFEST_LOCK_MISMATCH", func(m map[string]any) { m["type"] = "cli" }},
		{"version", "FAIL", "MCP_MANIFEST_INVALID", func(m map[string]any) { m["version"] = "1.2.4" }},
		{"source", "FAIL", "MCP_MANIFEST_LOCK_MISMATCH", func(m map[string]any) {
			m["source"] = map[string]any{"type": "npm", "package": "other", "version": "1.2.3"}
		}},
		{"permission expansion", "FAIL", "MCP_MANIFEST_PERMISSIONS_MISMATCH", func(m map[string]any) {
			var perms map[string]any
			json.Unmarshal(bytesOf(m["permissions"]), &perms)
			perms["exec"] = true
			m["permissions"] = perms
		}},
		{"permission reordering", "PASS", "MCP_MANIFEST_PERMISSIONS_MATCH", func(m map[string]any) {
			var perms map[string]any
			json.Unmarshal(bytesOf(m["permissions"]), &perms)
			perms["fs"].(map[string]any)["paths"] = []any{"${HOME}/docs", "${PROJECT}"}
			m["permissions"] = perms
		}},
		{"omitted deps", "UNKNOWN", "MCP_MANIFEST_DEPENDENCIES_UNDECLARED", func(m map[string]any) { delete(m, "dependencies") }},
		{"empty deps", "FAIL", "MCP_MANIFEST_DEPENDENCIES_MISMATCH", func(m map[string]any) { m["dependencies"] = []any{} }},
		{"wrong pinned dep", "FAIL", "MCP_MANIFEST_DEPENDENCIES_MISMATCH", func(m map[string]any) { m["dependencies"].([]any)[0].(map[string]any)["version"] = "9.0.0" }},
		{"transitive as direct", "FAIL", "MCP_MANIFEST_DEPENDENCIES_MISMATCH", func(m map[string]any) {
			m["dependencies"] = append(m["dependencies"].([]any), map[string]any{"ecosystem": "npm", "name": "leaf", "version": "3.0.0"})
		}},
		{"undeclared credential", "FAIL", "MCP_MANIFEST_SECRETS_MISMATCH", func(m map[string]any) { m["secrets"] = []any{"private_key"} }},
		{"duplicate credential", "FAIL", "MCP_MANIFEST_INVALID", func(m map[string]any) { m["secrets"] = []any{"private_key", "private_key"} }},
		{"unknown field", "FAIL", "MCP_MANIFEST_INVALID", func(m map[string]any) { m["trust_level"] = "L4" }},
		{"floating dep", "FAIL", "MCP_MANIFEST_INVALID", func(m map[string]any) { m["dependencies"].([]any)[0].(map[string]any)["version"] = "^2.0.0" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local, s, p := manifestFixture(t, tc.change, false)
			r := inspect(t, local, s, p)
			if r.Status != tc.status || !has(r, tc.code) {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestManifestRequiresBoundUnchangedArtifactAndRawDocuments(t *testing.T) {
	for _, kind := range []string{"artifact changed", "unbound document", "unbound lock document", "missing manifest", "duplicate yaml", "typed lock", "wrong check"} {
		t.Run(kind, func(t *testing.T) {
			local, s, p := manifestFixture(t, nil, false)
			c, observed, err := pluginlock.VerifyObserved(context.Background(), local, p, s)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "artifact changed":
				put(t, filepath.Join(local, "mcp-artifacts", "fixture"), "main.js", []byte("changed"))
				c, observed, err = pluginlock.VerifyObserved(context.Background(), local, p, s)
			case "unbound document":
				observed.Documents["plugin.yaml"] = []byte("changed")
			case "unbound lock document":
				observed.Documents["package-lock.json"] = []byte("changed")
			case "missing manifest":
				delete(observed.Documents, "plugin.yaml")
			case "duplicate yaml":
				b := []byte("schema_version: 1\nname: one\nname: two\n")
				put(t, filepath.Join(local, "mcp-artifacts", "fixture"), "plugin.yaml", b)
				entries, docs, _ := pluginlock.Observe(context.Background(), filepath.Join(local, "mcp-artifacts", "fixture"))
				p.Artifact.Entries = entries
				p.Artifact.SHA256 = pluginlock.Digest(entries)
				c.ArtifactSHA256 = p.Artifact.SHA256
				observed = pluginlock.Observation{Entries: entries, Documents: docs}
			case "typed lock":
				p.Runtime.Constraint = "*"
			case "wrong check":
				c.Name = "another"
			}
			if err != nil {
				t.Fatal(err)
			}
			r := Inspect(p, s, c, observed)
			if r.Status == "PASS" || r.ManifestStatus == "PASS" || r.ExecutionReady {
				t.Fatal(r)
			}
		})
	}
}

func TestManifestParserRejectsAmbiguityAndDependencyAliases(t *testing.T) {
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["dependencies"] = nil },
		func(m map[string]any) {
			m["dependencies"] = []any{map[string]any{"ecosystem": "npm", "name": "child", "version": "latest"}}
		},
		func(m map[string]any) {
			m["dependencies"] = []any{map[string]any{"ecosystem": "pypi", "name": "Demo_pkg", "version": "1.0.0"}, map[string]any{"ecosystem": "pypi", "name": "demo.pkg", "version": "1.0.0"}}
		},
		func(m map[string]any) {
			m["dependencies"] = []any{map[string]any{"ecosystem": "generic", "name": "../escape", "version": "1.0.0"}}
		},
		func(m map[string]any) { m["version"] = "latest" },
	} {
		local, s, p := manifestFixture(t, change, false)
		r := inspect(t, local, s, p)
		if !has(r, "MCP_MANIFEST_INVALID") {
			t.Fatal(r)
		}
	}
	if _, err := Parse([]byte(strings.Repeat(" ", (1<<20)+1))); err == nil {
		t.Fatal("oversize manifest accepted")
	}
}

func TestManifestMissingOptionalResolutionStaysUnknown(t *testing.T) {
	local, s, p := manifestFixture(t, nil, false)
	root := filepath.Join(local, "mcp-artifacts", "fixture")
	b, err := os.ReadFile(filepath.Join(root, "package-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lock map[string]any
	json.Unmarshal(b, &lock)
	meta := lock["packages"].(map[string]any)[""].(map[string]any)
	meta["optionalDependencies"] = map[string]string{"platform-helper": "1.0.0"}
	put(t, root, "package-lock.json", bytesOf(lock))
	put(t, root, "package.json", bytesOf(meta))
	p, err = pluginlock.Prepare(context.Background(), local, s, pluginlock.Preparation{Name: p.Name, ArtifactPath: p.Artifact.Path, Runtime: p.Runtime})
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := pluginlock.VerifyObserved(context.Background(), local, p, s)
	if err != nil || c.DependencyStatus != "PASS" {
		t.Fatal(c, err)
	}
	r := inspect(t, local, s, p)
	if r.DependencyStatus != "UNKNOWN" || r.Status != "UNKNOWN" || !has(r, "MCP_MANIFEST_DEPENDENCIES_UNVERIFIED") {
		t.Fatal(r)
	}
}
