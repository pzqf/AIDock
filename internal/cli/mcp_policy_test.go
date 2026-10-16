package cli

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/mcppolicy"
)

func TestMCPPolicyTemplateIsDraftAndDoesNotNeedAProject(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not-created-home")
	exit, v, stderr := invoke(t, "mcp", "policy-template", "--home", home, "--json")
	if exit != 0 {
		t.Fatal(exit, v, stderr)
	}
	raw, err := json.Marshal(v["items"])
	if err != nil {
		t.Fatal(err)
	}
	p, err := mcppolicy.Parse(raw)
	if err != nil || p.Digest() != mcppolicy.Example().Digest() {
		t.Fatal(p, err)
	}
}
