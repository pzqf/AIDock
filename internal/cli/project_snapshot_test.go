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

type cancelConsoleOutput struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelConsoleOutput) Write(b []byte) (int, error) {
	n, err := w.Buffer.Write(b)
	if bytes.Contains(b, []byte(`"url":`)) {
		w.cancel()
	}
	return n, err
}

func TestProjectSnapshotCLIHelpAndBinding(t *testing.T) {
	for _, args := range [][]string{{"snapshot", "replay", "review", "11111111-1111-4111-8111-111111111111"}, {"snapshot", "replay", "permission", "plan"}, {"snapshot", "replay", "permission", "apply"}} {
		var out, err bytes.Buffer
		if n := Run(context.Background(), append(append([]string{}, args...), "--help"), &out, &err); n != 0 || !strings.Contains(out.String(), "Usage:") {
			t.Fatal(n, out.String(), err.String())
		}
		exit, v, _ := invoke(t, append(args, "--json")...)
		if exit == 0 || v["code"] != "SOURCE_BINDING_REQUIRED" {
			t.Fatal(exit, v)
		}
	}
	for _, args := range [][]string{{"snapshot", "list"}, {"snapshot", "check", "11111111-1111-4111-8111-111111111111"}, {"snapshot", "assess", "11111111-1111-4111-8111-111111111111"}, {"snapshot", "draft", "plan", "11111111-1111-4111-8111-111111111111"}, {"snapshot", "draft", "history"}, {"snapshot", "draft", "recover"}, {"snapshot", "draft", "inspect"}, {"snapshot", "draft", "apply"}, {"snapshot", "draft", "reverse", "11111111-1111-4111-8111-111111111111"}} {
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out = cancelConsoleOutput{cancel: cancel}
	var errOut bytes.Buffer
	if n := Run(ctx, append([]string{"config", "--no-open"}, flags...), &out, &errOut); n != 0 || !strings.Contains(out.String(), `"url":`) {
		t.Fatal("could not reopen console to repair damaged binding", n, out.String(), errOut.String())
	}
	after, err := os.ReadFile(filepath.Join(root, "binding.json"))
	if err != nil || string(after) != "PRIVATE_BROKEN_BINDING" {
		t.Fatal("console startup modified broken input", err)
	}
}
