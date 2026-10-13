package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func invoke(t *testing.T, args ...string) (int, map[string]any, string) {
	t.Helper()
	var out, err bytes.Buffer
	exit := Run(context.Background(), args, &out, &err)
	var v map[string]any
	if out.Len() > 0 {
		if e := json.Unmarshal(out.Bytes(), &v); e != nil {
			t.Fatalf("stdout is not one JSON result: %s; %v", out.String(), e)
		}
	}
	return exit, v, err.String()
}

func TestVersionJSON(t *testing.T) {
	exit, v, _ := invoke(t, "version", "--json")
	if exit != 0 || v["code"] != "OK" || v["schema_version"] != float64(1) {
		t.Fatal(exit, v)
	}
}

func TestReservedCommandsNeverWrite(t *testing.T) {
	home := filepath.Join(t.TempDir(), "never-created")
	for _, args := range [][]string{{"replica", "apply"}, {"memory", "save"}, {"plugin", "install"}, {"bootstrap", "https://example.invalid"}} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			args = append(args, "--home", home, "--json")
			exit, v, _ := invoke(t, args...)
			if exit != 4 || v["code"] != "FEATURE_NOT_IMPLEMENTED" || v["ok"] != false {
				t.Fatal(exit, v)
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatal("reserved command created data roots")
			}
		})
	}
}

func TestProtocolCommandKeepsStdoutClean(t *testing.T) {
	exit, v, stderr := invoke(t, "mcp", "exec", "--project", "11111111-1111-4111-8111-111111111111", "--plugin", "fixture", "--json")
	if exit != 4 || v != nil || !strings.Contains(stderr, "FEATURE_NOT_IMPLEMENTED") {
		t.Fatal(exit, v, stderr)
	}
}

func TestUnknownFlagReturnsStructuredInputError(t *testing.T) {
	exit, v, _ := invoke(t, "version", "--unknown", "--json")
	if exit != 2 || v["code"] != "INVALID_INPUT" {
		t.Fatal(exit, v)
	}
}

func TestHelpDeclaresCommandSurfaceAndAliases(t *testing.T) {
	var out, err bytes.Buffer
	r := &runner{out: &out, err: &err}
	root := r.command()
	for _, path := range [][]string{{"plan"}, {"apply"}, {"push"}, {"pull"}, {"bootstrap"}, {"export"}, {"diff"}, {"replay"}, {"project", "list"}, {"replica", "plan"}, {"plugin", "update"}, {"skill", "show"}, {"skill", "scan"}, {"skill", "bridge-check"}, {"skill", "bridge", "check"}, {"skill", "plan"}, {"skill", "apply"}, {"mcp", "exec"}, {"memory", "search"}, {"doctor"}, {"audit"}, {"update"}, {"config"}, {"ui"}} {
		cmd, rest, e := root.Find(path)
		if e != nil || cmd == root || len(rest) != 0 {
			t.Fatal("missing command", path, e)
		}
	}
}

func TestBridgeCheckIsReadOnlyAndNeedsExplicitFile(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "external")
	if err := os.MkdirAll(filepath.Join(root, "skills", "guide"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "guide", "SKILL.md"), []byte("---\nname: guide\ndescription: fixture\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	bridge := filepath.Join(dir, "bridge.yaml")
	body := "schema_version: 1\nbridges:\n  - id: 22222222-2222-4222-8222-222222222222\n    project: 11111111-1111-4111-8111-111111111111\n    scope: server\n    copy: one\n    root: " + root + "\n    source_path: skills\n    source_role: external-source\n    format: native-skill\n    generator: fixture\n    packages: [guide]\n"
	if err := os.WriteFile(bridge, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	exit, v, _ := invoke(t, "skill", "bridge-check", "--file", bridge, "--json")
	if exit != 3 || v["code"] != "EVIDENCE_INCOMPLETE" {
		t.Fatal(exit, v)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "guide", "SKILL.md")); err != nil {
		t.Fatal("bridge check removed external source", err)
	}
}

func TestFixtureScanAndStaticDoctorBoundaries(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AIDOCK_HOME", filepath.Join(dir, "home"))
	manifest := filepath.Join("..", "..", "examples", "fixture.scan.yaml")
	exit, v, _ := invoke(t, "skill", "scan", "--manifest", manifest, "--json")
	if exit != 0 {
		t.Fatal(exit, v)
	}
	report := v["items"].(map[string]any)
	if report["read_only"] != true || len(report["skills"].([]any)) != 2 {
		t.Fatal(report)
	}
	exit, v, _ = invoke(t, "doctor", "--manifest", manifest, "--json")
	if exit != 3 || v["code"] != "EVIDENCE_INCOMPLETE" {
		t.Fatal("doctor falsely passed unverified environment", exit, v)
	}
	exit, v, _ = invoke(t, "doctor", "--probe", "--json")
	if exit != 4 || v["code"] != "FEATURE_NOT_IMPLEMENTED" {
		t.Fatal(exit, v)
	}
	if _, err := os.Stat(filepath.Join(dir, "home")); !os.IsNotExist(err) {
		t.Fatal("readonly operations initialized data roots")
	}
}
