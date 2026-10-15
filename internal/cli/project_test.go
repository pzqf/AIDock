package cli

import (
	"encoding/json"
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
	exit, v, _ = invoke(t, append([]string{"skill", "managed-list", "--binding", binding}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, append([]string{"skill", "managed-list", "--binding", binding, "--managed-root", filepath.Join(root, ".aidock")}, flags...)...)
	if exit != 2 || v["code"] != "INVALID_SOURCE_BINDING" {
		t.Fatal(exit, v)
	}
}
