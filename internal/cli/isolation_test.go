package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsolationCLIStaticAndExplicitHomeContract(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	exit, out, _ := invoke(t, "platform", "check", "--home", home, "--json")
	if exit != 3 || out["code"] != "EVIDENCE_INCOMPLETE" {
		t.Fatal(exit, out)
	}
	items := out["items"].(map[string]any)
	if items["probe"] != false || items["generator_ready"] != false || items["status"] == "PASS" {
		t.Fatal(items)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("static created home", err)
	}
	for _, args := range [][]string{{"platform", "check", "--probe"}, {"platform", "check", "--last"}, {"platform", "show", "--check", "d26f53ce-62ce-4d6b-b25a-1cbdca310bb0"}, {"platform", "pending"}, {"platform", "recover"}} {
		exit, out, _ = invoke(t, append(args, "--json")...)
		if exit != 4 || out["code"] != "SOURCE_BINDING_REQUIRED" {
			t.Fatal(exit, out)
		}
	}
	exit, out, _ = invoke(t, "platform", "check", "--last", "--home", home, "--json")
	if exit != 3 || out["items"] != nil {
		t.Fatal(exit, out)
	}
	exit, out, _ = invoke(t, "platform", "check", "--probe", "--last", "--home", home, "--json")
	if exit != 2 || out["code"] != "INVALID_INPUT" {
		t.Fatal(exit, out)
	}
	for _, flag := range []string{"--exe", "--root", "--command", "--allow-network"} {
		exit, _, _ = invoke(t, "platform", "check", flag, "bad", "--home", home, "--json")
		if exit != 2 {
			t.Fatal("caller supplied an execution boundary", flag, exit)
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("negative command created home", err)
	}
}
