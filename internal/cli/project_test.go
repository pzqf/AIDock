package cli

import (
	"context"
	"encoding/json"
	"github.com/pzqf/AIDock/internal/project"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectCommandsCreateAndUseRegisteredBinding(t *testing.T) {
	d := t.TempDir()
	home, root := filepath.Join(d, "home"), filepath.Join(d, "project")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(d, "request.json")
	b, _ := json.Marshal(map[string]any{"root": root, "name": "CLI 项目", "create": true})
	os.WriteFile(file, b, 0600)
	flags := []string{"--home", home, "--json"}
	exit, v, _ := invoke(t, append([]string{"project", "add", "--file", file}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	p := v["items"].(map[string]any)
	if _, e := os.Stat(filepath.Join(root, ".aidock")); !os.IsNotExist(e) {
		t.Fatal("preview wrote project")
	}
	exit, v, _ = invoke(t, append([]string{"project", "confirm", "--plan", p["id"].(string), "--digest", p["digest"].(string)}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, append([]string{"project", "list"}, flags...)...)
	if exit != 0 || len(v["items"].([]any)) != 1 {
		t.Fatal(exit, v)
	}
	binding := v["items"].([]any)[0].(map[string]any)["id"].(string)
	exit, v, _ = invoke(t, append([]string{"skill", "list", "--binding", binding}, flags...)...)
	if exit != 0 || v["items"].(map[string]any)["index"].(map[string]any)["scope_id"] != "workspace" {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, append([]string{"skill", "managed-list", "--binding", binding}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, append([]string{"skill", "managed-list", "--binding", binding, "--managed-root", filepath.Join(root, ".aidock")}, flags...)...)
	if exit != 2 || v["code"] != "INVALID_SOURCE_BINDING" {
		t.Fatal(exit, v)
	}
}

func TestProjectAndReplicaRemovalShareCanonicalPlan(t *testing.T) {
	d := t.TempDir()
	home := filepath.Join(d, "home")
	root := filepath.Join(d, "project")
	os.Mkdir(root, 0700)
	m, _ := project.New(filepath.Join(home, "local"))
	p, err := m.Plan(context.Background(), project.Request{Root: root, Name: "CLI removal", Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	flags := []string{"--home", home, "--json"}
	exit, v, _ := invoke(t, append([]string{"replica", "rm", "--binding", p.Binding.ID, "--copy", p.Binding.Copy}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	removal := v["items"].(map[string]any)
	if removal["kind"] != "project-removal" {
		t.Fatal(removal)
	}
	if _, err = m.Resolve(p.Binding.ID); err != nil {
		t.Fatal("preview removed binding", err)
	}
	exit, v, _ = invoke(t, append([]string{"project", "confirm", "--plan", removal["id"].(string), "--digest", removal["digest"].(string)}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, append([]string{"project", "list"}, flags...)...)
	if exit != 0 || len(v["items"].([]any)) != 0 {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, append([]string{"project", "rm", "--id", p.Binding.ID}, flags...)...)
	if exit == 0 || v["code"] != "PROJECT_NOT_REGISTERED" {
		t.Fatal(exit, v)
	}
	if _, err = os.Stat(filepath.Join(root, ".aidock", "project.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestProjectArchiveCLIWorksWithInactiveBindingFlag(t *testing.T) {
	d := t.TempDir()
	home := filepath.Join(d, "home")
	root := filepath.Join(d, "project")
	os.Mkdir(root, 0700)
	m, _ := project.New(filepath.Join(home, "local"))
	p, err := m.Plan(context.Background(), project.Request{Root: root, Name: "archive CLI", Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Apply(context.Background(), p.ID, p.Digest); err != nil {
		t.Fatal(err)
	}
	flags := []string{"--home", home, "--binding", p.Binding.ID, "--json"}
	for _, action := range []string{"archive", "restore"} {
		exit, v, _ := invoke(t, append([]string{"project", action, "--id", p.Binding.ID}, flags...)...)
		if exit != 0 {
			t.Fatal(exit, v)
		}
		plan := v["items"].(map[string]any)
		exit, v, _ = invoke(t, append([]string{"project", "confirm", "--plan", plan["id"].(string), "--digest", plan["digest"].(string)}, flags...)...)
		if exit != 0 {
			t.Fatal(exit, v)
		}
		if action == "archive" {
			exit, v, _ = invoke(t, append([]string{"project", "archived"}, flags...)...)
			if exit != 0 || len(v["items"].([]any)) != 1 {
				t.Fatal(exit, v)
			}
			exit, v, _ = invoke(t, append([]string{"skill", "managed-list"}, flags...)...)
			if exit == 0 || v["code"] != "PROJECT_ARCHIVED" {
				t.Fatal(exit, v)
			}
		}
	}
	if b, err := m.Resolve(p.Binding.ID); err != nil || b.Copy != p.Binding.Copy {
		t.Fatal(b, err)
	}
}
