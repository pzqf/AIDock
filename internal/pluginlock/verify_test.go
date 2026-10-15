package pluginlock

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/workstore"
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
func fixture(t *testing.T) (string, map[string]any, Preparation, Plugin) {
	t.Helper()
	local := t.TempDir()
	root := filepath.Join(local, "mcp-artifacts", "memory")
	src := map[string]any{"type": "npm", "package": "@fixture/memory", "version": "1.2.3"}
	server := map[string]any{"name": "memory", "source": src, "transport": "stdio", "entrypoint": "dist/index.js", "tier": 1, "enabled": false, "permissions": map[string]any{"fs": map[string]any{"paths": []any{"${PROJECT}"}, "mode": "ro"}, "net": map[string]any{"default": "deny", "allow": []any{}}, "exec": false}}
	put(t, root, "dist/index.js", []byte("// never executed\n"))
	put(t, root, "runtime/node", []byte("fake runtime: never execute"))
	put(t, root, ".hidden", []byte("included"))
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0755); err != nil {
		t.Fatal(err)
	}
	deps := map[string]string{"child": "2.0.0"}
	main := map[string]any{"name": "@fixture/memory", "version": "1.2.3", "dependencies": deps}
	child := map[string]any{"name": "child", "version": "2.0.0"}
	put(t, root, "package.json", bytesOf(main))
	put(t, root, "node_modules/child/package.json", bytesOf(child))
	lock := map[string]any{"lockfileVersion": 3, "packages": map[string]any{"": main, "node_modules/child": map[string]any{"version": "2.0.0", "resolved": "https://registry.example.invalid/child-2.0.0.tgz", "integrity": "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, 64))}}}
	put(t, root, "package-lock.json", bytesOf(lock))
	req := Preparation{Name: "memory", ArtifactPath: "mcp-artifacts/memory", Runtime: Runtime{Kind: "node", Version: "22.14.0", Constraint: "=22.14.0", Executable: "runtime/node"}}
	p, err := Prepare(context.Background(), local, server, req)
	if err != nil {
		t.Fatal(err)
	}
	return local, server, req, p
}
func code(c Check, want string) bool {
	for _, f := range c.Checks {
		if f.Code == want {
			return true
		}
	}
	return false
}

func TestCompleteLocalArtifactAndDependencyVerificationNeverGrantsTrust(t *testing.T) {
	local, s, _, p := fixture(t)
	c, err := Verify(context.Background(), local, p, s)
	if err != nil || c.ArtifactStatus != "PASS" || c.DependencyStatus != "PASS" || c.TrustLevel != "L0" || c.Status != "UNKNOWN" || c.ExecutionReady || !code(c, "MCP_SOURCE_TRUST_UNVERIFIED") {
		t.Fatal(c, err)
	}
	if c.Files != 6 || c.Bytes == 0 || c.ArtifactSHA256 != c.ObservedSHA256 {
		t.Fatal(c)
	}
	s["enabled"] = true
	c, err = Verify(context.Background(), local, p, s)
	if err != nil || code(c, "MCP_LOCK_DEFINITION_MISMATCH") {
		t.Fatal(c, err)
	}
	s["tier"] = 2
	c, _ = Verify(context.Background(), local, p, s)
	if !code(c, "MCP_LOCK_DEFINITION_MISMATCH") {
		t.Fatal(c)
	}
	p.Platform = "linux-arm64"
	if runtime.GOOS+"-"+runtime.GOARCH == p.Platform {
		p.Platform = "windows-amd64"
	}
	c, _ = Verify(context.Background(), local, p, s)
	if !code(c, "MCP_LOCK_PLATFORM_MISMATCH") {
		t.Fatal(c)
	}
}

func TestLocalArtifactChangesIncludeHiddenFilesEmptyDirectoriesAndExtras(t *testing.T) {
	for _, kind := range []string{"hidden", "empty", "extra", "runtime", "permission"} {
		t.Run(kind, func(t *testing.T) {
			local, s, _, p := fixture(t)
			root := filepath.Join(local, "mcp-artifacts", "memory")
			switch kind {
			case "hidden":
				put(t, root, ".hidden", []byte("changed"))
			case "empty":
				if err := os.Remove(filepath.Join(root, "empty")); err != nil {
					t.Fatal(err)
				}
			case "extra":
				put(t, root, ".unexpected", []byte("must not be ignored"))
			case "runtime":
				put(t, root, "runtime/node", []byte("changed runtime"))
			case "permission":
				if err := os.Chmod(filepath.Join(root, "runtime/node"), 0444); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(filepath.Join(root, "runtime/node"), 0755)
			}
			c, err := Verify(context.Background(), local, p, s)
			if err != nil || c.ArtifactStatus != "FAIL" || c.Status != "FAIL" || c.ExecutionReady {
				t.Fatal(c, err)
			}
		})
	}
}

func TestMalformedLocksRejectAmbiguityPathsFloatingVersionsAndSelfTrust(t *testing.T) {
	for _, kind := range []string{"unknown trust", "duplicate name", "case file", "path escape", "runtime absent", "floating", "digest", "parent", "source version", "overlap", "bad constraint", "absolute dependency"} {
		t.Run(kind, func(t *testing.T) {
			_, _, _, p := fixture(t)
			l := Lock{Version: 1, Plugins: []Plugin{p}}
			switch kind {
			case "unknown trust":
				var raw map[string]any
				json.Unmarshal(bytesOf(l), &raw)
				raw["plugins"].([]any)[0].(map[string]any)["trust_level"] = "L4"
				if _, err := Parse(bytesOf(raw)); err == nil {
					t.Fatal("accepted self trust")
				}
				return
			case "duplicate name":
				other := p
				other.Name = strings.ToUpper(p.Name)
				l.Plugins = append(l.Plugins, other)
			case "case file":
				e := p.Artifact.Entries[0]
				e.Path = strings.ToUpper(e.Path)
				p.Artifact.Entries = append(p.Artifact.Entries, e)
			case "path escape":
				p.Artifact.Path = "mcp-artifacts/../../outside"
			case "runtime absent":
				p.Runtime.Executable = "outside/node"
			case "floating":
				p.Version = "latest"
			case "digest":
				p.Artifact.SHA256 = strings.Repeat("0", 64)
			case "parent":
				for i, e := range p.Artifact.Entries {
					if e.Path == "runtime" {
						p.Artifact.Entries = append(p.Artifact.Entries[:i], p.Artifact.Entries[i+1:]...)
						break
					}
				}
				p.Artifact.SHA256 = Digest(p.Artifact.Entries)
			case "source version":
				p.Source["version"] = "9.9.9"
			case "overlap":
				other := p
				other.Name = "other"
				other.Artifact.Path += "/child"
				l.Plugins = append(l.Plugins, other)
			case "bad constraint":
				p.Runtime.Constraint = ">=22"
			case "absolute dependency":
				p.Dependencies[0].Path = "C:/outside"
			}
			l.Plugins[0] = p
			if _, err := Parse(bytesOf(l)); err == nil {
				t.Fatal("invalid lock accepted")
			}
		})
	}
}

func TestNPMCompleteResolutionMismatchCannotBeHiddenByNewLocalHashes(t *testing.T) {
	for _, kind := range []string{"missing graph", "extra installed", "wrong version", "missing integrity", "link", "wrong main", "duplicate JSON", "range mismatch", "unsupported range"} {
		t.Run(kind, func(t *testing.T) {
			local, s, req, _ := fixture(t)
			root := filepath.Join(local, "mcp-artifacts", "memory")
			b, err := os.ReadFile(filepath.Join(root, "package-lock.json"))
			if err != nil {
				t.Fatal(err)
			}
			var lock map[string]any
			json.Unmarshal(b, &lock)
			packages := lock["packages"].(map[string]any)
			child := packages["node_modules/child"].(map[string]any)
			switch kind {
			case "missing graph":
				delete(packages, "node_modules/child")
				if err = os.Remove(filepath.Join(root, "node_modules", "child", "package.json")); err != nil {
					t.Fatal(err)
				}
			case "extra installed":
				put(t, root, "node_modules/extra/package.json", []byte("{\"name\":\"extra\",\"version\":\"1.0.0\"}"))
			case "wrong version":
				put(t, root, "node_modules/child/package.json", []byte("{\"name\":\"child\",\"version\":\"3.0.0\"}"))
			case "missing integrity":
				delete(child, "integrity")
			case "link":
				child["link"] = true
			case "wrong main":
				s["source"].(map[string]any)["package"] = "@fixture/other"
			case "duplicate JSON":
				put(t, root, "node_modules/child/package.json", []byte("{\"name\":\"child\",\"version\":\"2.0.0\",\"version\":\"3.0.0\"}"))
			case "range mismatch", "unsupported range":
				spec := "^3.0.0"
				if kind == "unsupported range" {
					spec = "latest"
				}
				main := packages[""].(map[string]any)
				main["dependencies"] = map[string]string{"child": spec}
				put(t, root, "package.json", bytesOf(main))
			}
			put(t, root, "package-lock.json", bytesOf(lock))
			p, err := Prepare(context.Background(), local, s, req)
			if err != nil {
				if kind == "missing integrity" {
					return
				}
				t.Fatal(err)
			}
			c, err := Verify(context.Background(), local, p, s)
			if err != nil || c.ArtifactStatus != "PASS" || c.DependencyStatus != "FAIL" || c.ExecutionReady {
				t.Fatal(c, err)
			}
		})
	}
}

func TestNPMResolvedVersionsMustSatisfySupportedRangesAndAliases(t *testing.T) {
	for _, spec := range []string{"^2.0.0", "~2.0", "2.x", ">=1.0.0 <3.0.0", "1.0.0 || >=2.0.0", "*", "=2.0.0", "2.0.0 - 2.9.9", "npm:child@^2.0.0"} {
		t.Run(spec, func(t *testing.T) {
			local, s, req, _ := fixture(t)
			root := filepath.Join(local, "mcp-artifacts", "memory")
			var lock map[string]any
			b, err := os.ReadFile(filepath.Join(root, "package-lock.json"))
			if err != nil {
				t.Fatal(err)
			}
			json.Unmarshal(b, &lock)
			main := lock["packages"].(map[string]any)[""].(map[string]any)
			main["dependencies"] = map[string]string{"child": spec}
			put(t, root, "package.json", bytesOf(main))
			put(t, root, "package-lock.json", bytesOf(lock))
			p, err := Prepare(context.Background(), local, s, req)
			if err != nil {
				t.Fatal(err)
			}
			c, err := Verify(context.Background(), local, p, s)
			if err != nil || c.DependencyStatus != "PASS" || c.ExecutionReady {
				t.Fatal(c, err)
			}
		})
	}
	if checkRequirement("npm:other@^2.0.0", npmPackage{Name: "child", Version: "2.0.0"}) == nil {
		t.Fatal("alias identity mismatch accepted")
	}
}

func TestNoLinksOutsideLocalRootOrCancelledReads(t *testing.T) {
	local, s, req, p := fixture(t)
	for _, bad := range []string{"../outside", "mcp-artifacts/../../outside", "C:/outside", "mcp-artifacts"} {
		req.ArtifactPath = bad
		if _, err := Prepare(context.Background(), local, s, req); err == nil {
			t.Fatal("read outside approved root", bad)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Verify(ctx, local, p, s); err != context.Canceled {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(local, "mcp-artifacts", "memory", "escape")); err != nil {
		t.Skip("symlink privilege unavailable")
	}
	c, err := Verify(context.Background(), local, p, s)
	if err != nil || !code(c, "MCP_ARTIFACT_UNAVAILABLE") || c.ArtifactStatus != "FAIL" {
		t.Fatal(c, err)
	}
}

func TestRuntimeLargerThanSourceDocumentLimitIsStreamedReadOnly(t *testing.T) {
	local, s, req, _ := fixture(t)
	root := filepath.Join(local, "mcp-artifacts", "memory")
	put(t, root, "runtime/node", make([]byte, workstore.MaxFile+1))
	p, err := Prepare(context.Background(), local, s, req)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Verify(context.Background(), local, p, s)
	if err != nil || c.ArtifactStatus != "PASS" || c.Bytes <= workstore.MaxFile {
		t.Fatal(c, err)
	}
}
