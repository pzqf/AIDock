package inventory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}
func testManifest(root string) Manifest {
	return Manifest{SchemaVersion: 1, Bindings: []Binding{{Project: "11111111-1111-4111-8111-111111111111", Scope: "server", Copy: "one", Root: root, Sources: []Source{{Path: "skills", Role: "external-source"}}}}}
}
func packageFiles(t *testing.T, dir, name string) {
	t.Helper()
	write(t, filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: 测试完整包\n---\n[参考](references/guide.md)\n"))
	write(t, filepath.Join(dir, "references/guide.md"), []byte("指南\r\n"))
	write(t, filepath.Join(dir, "assets/data.bin"), []byte{0, 255, 1})
	write(t, filepath.Join(dir, "scripts/fail.py"), []byte("raise RuntimeError('MUST NOT RUN')\n"))
}

func treeHashes(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	m := map[string][32]byte{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			m[rel] = sha256.Sum256(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCompletePackageReadOnlyDeterministic(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "skills", "api")
	packageFiles(t, dir, "api")
	before := treeHashes(t, root)
	first, err := Scan(context.Background(), testManifest(root), Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Scan(context.Background(), testManifest(root), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Skills) != 1 || len(first.Skills[0].Files) != 4 {
		t.Fatalf("incomplete package: %+v", first)
	}
	s := first.Skills[0]
	if s.StaticStatus != "PASS" || s.Layers["L1"] != "UNKNOWN" || s.Layers["L2"] != "UNKNOWN" {
		t.Fatal("static success promoted to tool effectiveness", s)
	}
	if !strings.HasPrefix(s.CandidateID, "candidate:") {
		t.Fatal("unregistered package assigned stable identity")
	}
	if s.PackageDigest != second.Skills[0].PackageDigest {
		t.Fatal("package hashing non-deterministic")
	}
	if !reflect.DeepEqual(before, treeHashes(t, root)) {
		t.Fatal("scanner modified package")
	}
	write(t, filepath.Join(dir, "assets/data.bin"), []byte{0, 255, 2})
	third, err := Scan(context.Background(), testManifest(root), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if third.Skills[0].PackageDigest == s.PackageDigest {
		t.Fatal("binary attachment ignored by digest")
	}
}

func TestScopeAndMirrorBoundaries(t *testing.T) {
	root := t.TempDir()
	packageFiles(t, filepath.Join(root, "skills/a"), "same")
	packageFiles(t, filepath.Join(root, "mirrors/a"), "same")
	m := testManifest(root)
	m.Bindings[0].Sources = append(m.Bindings[0].Sources, Source{Path: "mirrors", Role: "generated"})
	client := m.Bindings[0]
	client.Project = "22222222-2222-4222-8222-222222222222"
	client.Scope = "client"
	m.Bindings = append(m.Bindings, client)
	r, err := Scan(context.Background(), m, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Skills) != 2 {
		t.Fatal("generated mirrors included by default")
	}
	for _, s := range r.Skills {
		if s.StaticStatus != "PASS" {
			t.Fatal("same name across scope collided", s)
		}
	}
	r, err = Scan(context.Background(), m, Options{Scope: "server", IncludeGenerated: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Skills) != 2 {
		t.Fatal("explicit mirror scan failed")
	}
	packageFiles(t, filepath.Join(root, "skills/b"), "same")
	r, err = Scan(context.Background(), m, Options{Scope: "server"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range r.Skills {
		if s.StaticStatus != "FAIL" {
			t.Fatal("same-scope name collision not detected")
		}
	}
}

func TestUnsafeReferencesAndSecretFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "skills/a")
	packageFiles(t, dir, "a")
	write(t, filepath.Join(dir, ".env"), []byte("API_KEY=must-not-leak"))
	write(t, filepath.Join(dir, "SKILL.md"), []byte("---\nname: a\ndescription: test\n---\n[missing](missing.md)\n[escape](../../private.md)\n"))
	r, err := Scan(context.Background(), testManifest(root), Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "must-not-leak") {
		t.Fatal("secret content leaked")
	}
	codes := map[string]bool{}
	for _, f := range r.Skills[0].Findings {
		codes[f.Code] = true
	}
	for _, code := range []string{"SENSITIVE_FILE_EXCLUDED", "REFERENCE_MISSING_OR_UNSAFE", "PROJECT_REFERENCE_UNDECLARED"} {
		if !codes[code] {
			t.Fatal("missing diagnosis", code)
		}
	}
	for _, f := range r.Skills[0].Files {
		if f.Path == ".env" {
			t.Fatal("sensitive file was hashed into distributable candidate")
		}
	}
}

func TestManifestRejectsEscapesAndOverlappingSources(t *testing.T) {
	for _, path := range []string{"../skills", "/absolute", "C:/secret", ".", "skills/CON", "skills/a:stream"} {
		if err := safeRelative(path); err == nil {
			t.Fatal("unsafe path accepted", path)
		}
	}
	dir := t.TempDir()
	m := testManifest(dir)
	m.Bindings[0].Sources = append(m.Bindings[0].Sources, Source{Path: "skills/child", Role: "owned"})
	b, _ := json.Marshal(m)
	path := filepath.Join(dir, "scan.yaml")
	write(t, path, b)
	if _, err := Load(path); err == nil {
		t.Fatal("overlapping sources accepted")
	}
}

func TestCanceledScanStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, testManifest(t.TempDir()), Options{}); err == nil {
		t.Fatal("canceled scan continued")
	}
}

func TestMarkdownExamplesAreNotMissingDependencies(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "skills/a")
	packageFiles(t, dir, "a")
	body := "---\nname: a\ndescription: test\n---\n```go\n[Router](ctx)\n```\n`[example](fake.md)`\n[template](./<需求名称>/phase.md)\n[actual][ref]\n\n[ref]: references/guide.md\n"
	write(t, filepath.Join(dir, "SKILL.md"), []byte(body))
	r, err := Scan(context.Background(), testManifest(root), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.Skills[0].Findings {
		if f.Code == "REFERENCE_MISSING_OR_UNSAFE" {
			t.Fatal("code or template incorrectly treated as dependency", f)
		}
	}
	if links := markdownDestinations([]byte("[actual][ref]\n\n[ref]: references/guide.md\n")); len(links) != 1 || links[0] != "references/guide.md" {
		t.Fatal(links)
	}
}
