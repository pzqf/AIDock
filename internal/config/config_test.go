package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnedSkillMetadataRejectsEscapesAndIdentityConflicts(t *testing.T) {
	for _, change := range []string{"escape", "case collision", "missing document", "duplicate id", "unlocked plugin"} {
		t.Run(change, func(t *testing.T) {
			b, err := os.ReadFile("testdata/skills.yaml")
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err = json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			skills := m["skills"].([]any)
			s := skills[0].(map[string]any)
			files := s["files"].([]any)
			switch change {
			case "escape":
				files[0].(map[string]any)["path"] = "../secret.md"
			case "case collision":
				dup := map[string]any{}
				for k, v := range files[0].(map[string]any) {
					dup[k] = v
				}
				dup["path"] = "skill.md"
				s["files"] = append(files, dup)
			case "missing document":
				files[0].(map[string]any)["path"] = "guide.md"
			case "duplicate id":
				m["skills"] = append(skills, s)
			case "unlocked plugin":
				s["source"].(map[string]any)["mode"] = "plugin"
			}
			b, _ = json.Marshal(m)
			if _, err := Validate("skills", b); err == nil {
				t.Fatal("unsafe skill metadata accepted")
			}
		})
	}
}

func TestAmbiguousProfileSwitchRejected(t *testing.T) {
	data := []byte("schema_version: 1\nname: legacy\nskills:\n  enable: [same]\n  disable: [same]\n")
	if _, err := Validate("profile-v1", data); err == nil {
		t.Fatal("ambiguous override accepted")
	}
}

func TestRuleLibraryIdentityAndSchemaAreStrict(t *testing.T) {
	for _, bad := range []string{
		`{"schema_version":1,"rules":[{"id":"11111111-1111-4111-8111-111111111111","name":"one","enabled":true},{"id":"11111111-1111-4111-8111-111111111111","name":"two","enabled":false}]}`,
		`{"schema_version":1,"rules":[{"id":"11111111-1111-4111-8111-111111111111","name":"  ","enabled":true}]}`,
		`{"schema_version":1,"rules":[],"unexpected":true}`,
		`{"schema_version":2,"rules":[]}`,
	} {
		if _, err := Validate("rule-library", []byte(bad)); err == nil {
			t.Fatal("invalid rule index accepted", bad)
		}
	}
}

func TestGoldenSchemas(t *testing.T) {
	files, err := filepath.Glob("testdata/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			kind := strings.TrimSuffix(filepath.Base(path), ".yaml")
			if _, err = Validate(kind, b); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLifecycleSchemaRejectsInvalidRecords(t *testing.T) {
	b, e := os.ReadFile("testdata/skill-lifecycle.yaml")
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"duplicate", "unknown-field", "empty-reason", "long-reason", "time", "name", "revision"} {
		t.Run(kind, func(t *testing.T) {
			v, e := Parse(b)
			if e != nil {
				t.Fatal(e)
			}
			m := v.(map[string]any)
			rows := m["retirements"].([]any)
			r := rows[0].(map[string]any)
			switch kind {
			case "duplicate":
				m["retirements"] = append(rows, r)
			case "unknown-field":
				r["delete_files"] = true
			case "empty-reason":
				r["reason"] = "  "
			case "long-reason":
				r["reason"] = strings.Repeat("界", 501)
			case "time":
				r["retired_at"] = "yesterday"
			case "name":
				r["native_name"] = "BAD NAME"
			case "revision":
				r["revision"] = "latest"
			}
			data, _ := json.Marshal(m)
			if _, e = Validate("skill-lifecycle", data); e == nil {
				t.Fatal("invalid lifecycle accepted", kind)
			}
		})
	}
}

func TestRejectAmbiguousOrUnsafeYAML(t *testing.T) {
	cases := map[string]string{
		"duplicate":           "schema_version: 1\nid: 11111111-1111-4111-8111-111111111111\nid: second",
		"unknown field":       "schema_version: 1\nid: 11111111-1111-4111-8111-111111111111\npassword: hidden",
		"alias":               "schema_version: 1\nid: &id 11111111-1111-4111-8111-111111111111\nname: *id",
		"unsafe tag":          "schema_version: 1\nid: !Run command",
		"unsupported version": "schema_version: 99\nid: 11111111-1111-4111-8111-111111111111",
		"two documents":       "schema_version: 1\nid: 11111111-1111-4111-8111-111111111111\n---\nname: second",
		"non string key":      "schema_version: 1\n42: value",
		"non finite":          "schema_version: 1\nid: 11111111-1111-4111-8111-111111111111\nname: .inf",
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Validate("project", []byte(data)); err == nil {
				t.Fatal("unsafe document accepted")
			}
		})
	}
	if _, err := Parse([]byte{0xff}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	if _, err := Parse([]byte(strings.Repeat("x", MaxDocumentBytes+1))); err == nil {
		t.Fatal("oversized document accepted")
	}
}

func TestProfileVersionBoundary(t *testing.T) {
	b, err := os.ReadFile("testdata/profile-v1.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Validate("profile-v1", b); err != nil {
		t.Fatal(err)
	}
	if _, err = Validate("profile", b); err == nil {
		t.Fatal("legacy name references silently accepted as v2")
	}
}

func TestDataRootPrecedenceAndNoWrites(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.yaml")
	if err := os.WriteFile(settings, []byte("schema_version: 1\nhome: from-settings\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ flag, env, want, source string }{{"", "", "from-settings", "settings"}, {"", "from-env", "from-env", "environment"}, {"from-flag", "from-env", "from-flag", "flag"}}
	for _, c := range cases {
		r, err := ResolveRoots(RootOptions{FlagHome: c.flag, EnvHome: c.env, UserHome: dir, WorkingDir: dir, SettingsPath: settings})
		if err != nil {
			t.Fatal(err)
		}
		if r.Home != filepath.Join(dir, c.want) || r.Source != c.source {
			t.Fatalf("incorrect precedence: %+v", r)
		}
		if _, err = os.Stat(r.Home); !os.IsNotExist(err) {
			t.Fatal("resolution created selected home")
		}
	}
	r, err := ResolveRoots(RootOptions{UserHome: dir, WorkingDir: dir, SettingsPath: filepath.Join(dir, "absent.yaml")})
	if err != nil {
		t.Fatal(err)
	}
	if r.Home != filepath.Join(dir, ".aidock") || r.Source != "default" {
		t.Fatal(r)
	}
}

func TestRootOverlapRejected(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.yaml")
	for _, paths := range []string{"config_root: shared\nlocal_root: shared", "config_root: shared\nlocal_root: shared/nested"} {
		if err := os.WriteFile(settings, []byte("schema_version: 1\n"+paths+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ResolveRoots(RootOptions{UserHome: dir, WorkingDir: dir, SettingsPath: settings}); err == nil {
			t.Fatal("overlapping roots accepted")
		}
	}
}
