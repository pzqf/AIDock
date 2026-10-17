package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplayCLIExplicitPhasesAndInvalidInputsDoNotInitializeHome(t *testing.T) {
	id, sha := "11111111-1111-4111-8111-111111111111", strings.Repeat("a", 64)
	for _, flags := range [][]string{{"--confirm"}, {"--plan", id}, {"--digest", sha}, {"--prepared", id}, {"--prepared", id, "--plan", id, "--digest", sha}, {"--kind", "project", "--copies", id}, {"--copies", id + "," + id}, {"--copies", ""}, {"--kind", "replica", "--prepared", id, "--digest", sha}} {
		home := filepath.Join(t.TempDir(), "not-created")
		args := append([]string{"replay", id}, flags...)
		args = append(args, "--home", home, "--json")
		exit, v, _ := invoke(t, args...)
		if exit != 2 || v["code"] != "INVALID_INPUT" {
			t.Fatal(args, exit, v)
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatal("invalid replay initialized a home")
		}
	}
	exit, v, _ := invoke(t, "replay", id, "--json")
	if exit != 4 || v["code"] != "SOURCE_BINDING_REQUIRED" {
		t.Fatal(exit, v)
	}
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"replay", "--help"}, &out, &stderr); code != 0 || !strings.Contains(out.String(), "--copies") || !strings.Contains(out.String(), "--confirm") || strings.Contains(out.String(), "预留") {
		t.Fatal(code, out.String(), stderr.String())
	}
}
