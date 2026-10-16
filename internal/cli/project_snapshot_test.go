package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/source"
)

func TestProjectSnapshotCLIHelpAndBinding(t *testing.T) {
	for _, args := range [][]string{{"snapshot", "list"}, {"snapshot", "check", "11111111-1111-4111-8111-111111111111"}, {"snapshot", "assess", "11111111-1111-4111-8111-111111111111"}} {
		var out, err bytes.Buffer
		if n := Run(context.Background(), append(append([]string{}, args...), "--help"), &out, &err); n != 0 || !strings.Contains(out.String(), "Usage:") {
			t.Fatal(n, out.String(), err.String())
		}
		out.Reset()
		err.Reset()
		if n := Run(context.Background(), append(append([]string{}, args...), "--json"), &out, &err); n == 0 || !strings.Contains(out.String(), "SOURCE_BINDING_REQUIRED") {
			t.Fatal(n, out.String(), err.String())
		}
	}
}

func TestProjectSnapshotCLIReadsDespiteDamagedLocalBinding(t *testing.T) {
	d, home := t.TempDir(), t.TempDir()
	project, copyID := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	root := source.LocalProfileRoot(filepath.Join(home, "local"), project, "server", copyID)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "binding.json"), []byte("PRIVATE_BROKEN_BINDING"), 0600); err != nil {
		t.Fatal(err)
	}
	flags := []string{"--managed-root", filepath.Join(d, ".aidock"), "--native-root", d, "--home", home, "--project", project, "--scope", "server", "--copy", copyID, "--json"}
	exit, v, _ := invoke(t, append([]string{"snapshot", "list"}, flags...)...)
	if exit != 0 || v["ok"] != true {
		t.Fatal(exit, v)
	}
	exit, v, _ = invoke(t, append([]string{"tool", "history"}, flags...)...)
	if exit == 0 {
		t.Fatal("writer/history bootstrap bypassed current binding validation", v)
	}
}
