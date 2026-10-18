package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/replica"
	"github.com/pzqf/AIDock/internal/workstore"
)

func TestReplicaFullPublicationCLIHistoryAndStrictWriteIdentity(t *testing.T) {
	home, root := t.TempDir(), filepath.Join(t.TempDir(), ".aidock")
	for _, dir := range []string{filepath.Join(home, "local"), root} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"--home", home, "--managed-root", root, "--project", workstore.ID(), "--scope", "server", "--copy", workstore.ID(), "--json"}
	exit, v, stderr := invoke(t, append([]string{"replica", "publication", "full-history"}, args...)...)
	if exit != 0 || len(v["items"].([]any)) != 0 {
		t.Fatal(exit, v, stderr)
	}
	file := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(file, []byte(`{"plan_id":"x","digest":"x","checks":[],"checks":null}`), 0600); err != nil {
		t.Fatal(err)
	}
	exit, v, stderr = invoke(t, append([]string{"replica", "publication", "full-plan", "--file", file}, args...)...)
	if exit != 2 || v["code"] != "INVALID_INPUT" {
		t.Fatal(exit, v, stderr)
	}
	exit, v, stderr = invoke(t, append([]string{"replica", "publication", "full-show", "--publication", workstore.ID()}, args...)...)
	if exit != 4 || v["code"] != "PLAN_CONFIRMATION_REQUIRED" {
		t.Fatal(exit, v, stderr)
	}
	for _, action := range replica.FullPublicationOperationNames() {
		exit, v, stderr = invoke(t, append([]string{"replica", "publication", action, "--file", file}, args...)...)
		if exit != 2 || v["code"] != "INVALID_INPUT" {
			t.Fatal(action, exit, v, stderr)
		}
	}
	exit, v, stderr = invoke(t, append([]string{"replica", "publication", "full-apply", "--file", file, "--publication", workstore.ID()}, args...)...)
	if exit != 2 || v["code"] != "INVALID_INPUT" {
		t.Fatal("ambiguous confirmation accepted", exit, v, stderr)
	}
	exit, _, stderr = invoke(t, append([]string{"replica", "publication", "full-recover", "--publication", workstore.ID()}, args...)...)
	if exit == 0 || strings.Contains(stderr, "unknown flag") {
		t.Fatal("real recovery command is unavailable or invented success", exit, stderr)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("read/invalid requests changed source", entries, err)
	}
}
