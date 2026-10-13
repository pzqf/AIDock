package source

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenameDocumentPreservesEveryOtherByte(t *testing.T) {
	for _, name := range []string{"guide", "'guide'", `"guide"`, `"gu\u0069de"`} {
		for _, ending := range []string{"\n", "\r\n"} {
			for _, bom := range []string{"", "\ufeff"} {
				t.Run(name+ending+bom, func(t *testing.T) {
					original := []byte(bom + strings.ReplaceAll("---\n# keep\nname: "+name+"   # 原生名\ndescription: '说明'\ncustom: {nested: value}\n---\n\nbody guide 原样\n", "\n", ending))
					wantName := "guide-v2"
					if strings.HasPrefix(name, "'") {
						wantName = "'guide-v2'"
					}
					if strings.HasPrefix(name, `"`) {
						wantName = `"guide-v2"`
					}
					want := bytes.Replace(original, []byte("name: "+name), []byte("name: "+wantName), 1)
					got, e := renameDocument(original, "guide", "guide-v2")
					if e != nil || !bytes.Equal(got, want) {
						t.Fatalf("byte preservation failed: %v\n%q\n%q", e, got, want)
					}
				})
			}
		}
	}
}
func TestRenameUnusualFormatsFailWithoutChangingSource(t *testing.T) {
	for _, front := range []string{`{name: guide, description: example}`, "name: >-\n  guide\ndescription: example", "'name': guide\ndescription: example", "name: \"gu\\\n  ide\"\ndescription: example"} {
		t.Run(front, func(t *testing.T) {
			original := []byte("---\n" + front + "\n---\nbody\n")
			_, e := renameDocument(original, "guide", "guide-v2")
			code(t, e, "RENAME_FORMAT_UNSUPPORTED")
		})
	}
	_, e := renameDocument(body("different"), "guide", "guide-v2")
	code(t, e, "SOURCE_PACKAGE_DRIFT")
}
func TestRenameStableIdentityPackageRevisionAndRollback(t *testing.T) {
	m := manager(t)
	pkg := t.TempDir()
	doc := []byte("\ufeff---\r\nname: 'guide' # keep\r\ndescription: example\r\ncustom: yes\r\n---\r\nbody guide\r\n")
	files := map[string][]byte{"SKILL.md": doc, "binary.bin": {0, 255, 1}, "scripts/never.ps1": []byte("throw 'never run'"), "notes.md": []byte("draft")}
	for p, b := range files {
		write(t, filepath.Join(pkg, p), b)
	}
	p := apply(t, m, Request{Action: "import", PackagePath: pkg, SourceRole: "local-original"})
	old := current(t, m).Index.Skills[0]
	before := capture(t, m)
	r := apply(t, m, Request{Action: "rename", ID: p.SkillID, Name: "guide-v2"})
	s := current(t, m)
	skill := s.Index.Skills[0]
	if skill.ID != old.ID || skill.Source != old.Source || skill.Name != "guide-v2" || skill.Revision == old.Revision || switches(s.Profile)["pins"].(map[string]any)[skill.ID] != skill.Revision {
		t.Fatal(skill)
	}
	if r.Impact.NativeBefore != "guide" || r.Impact.NativeAfter != "guide-v2" || r.Impact.NativeCleanup != "PENDING_NATIVE_APPLY" || !r.PendingApply {
		t.Fatal(r)
	}
	for path, b := range files {
		original, e := os.ReadFile(filepath.Join(m.Store.Root, ".revisions", old.ID, strings.TrimPrefix(old.Revision, "sha256:"), path))
		if e != nil || !bytes.Equal(original, b) {
			t.Fatal("old revision lost", path, e)
		}
		got, e := os.ReadFile(filepath.Join(m.Store.Root, "skills", skill.ID, path))
		if path == "SKILL.md" {
			b = bytes.Replace(b, []byte("name: 'guide'"), []byte("name: 'guide-v2'"), 1)
		}
		if e != nil || !bytes.Equal(got, b) {
			t.Fatal("package changed", path, e)
		}
	}
	reverted := apply(t, m, Request{Action: "rollback", Snapshot: r.Plan.ID})
	if current(t, m).Index.Skills[0].Name != "guide" {
		t.Fatal("rename rollback failed")
	}
	for path, e := range before.Entries {
		if !strings.HasPrefix(path, ".revisions/") && reverted.Plan.After.Entries[path] != e {
			t.Fatal("rollback changed original", path)
		}
	}
	apply(t, m, Request{Action: "rollback", Snapshot: reverted.Plan.ID})
	if current(t, m).Index.Skills[0].Name != "guide-v2" {
		t.Fatal("rename undo failed")
	}
	other := apply(t, m, Request{Action: "create", Name: "taken", Description: "example"})
	apply(t, m, Request{Action: "retire", ID: other.SkillID, Reason: "unused"})
	_, e := m.Plan(context.Background(), Request{Action: "rename", ID: p.SkillID, Name: "taken"})
	code(t, e, "NATIVE_NAME_COLLISION")
}
