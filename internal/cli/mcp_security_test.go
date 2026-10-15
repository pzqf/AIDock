package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pzqf/AIDock/internal/pluginsecurity"
)

func TestMCPSecurityCLIReferenceLifecycleKnownRiskAndExitCodes(t *testing.T) {
	_, flags := mcpCLIArtifactFixture(t)
	call := func(args ...string) (int, map[string]any) {
		t.Helper()
		exit, v, _ := invoke(t, append(args, flags...)...)
		return exit, v
	}
	exit, v := call("mcp", "security", "state")
	if exit != 0 {
		t.Fatal(exit, v)
	}
	exit, v = call("mcp", "security", "check")
	if exit != 3 || v["code"] != "EVIDENCE_INCOMPLETE" {
		t.Fatal(exit, v)
	}
	file := filepath.Join(t.TempDir(), "request.json")
	var state pluginsecurity.State
	raw, _ := json.Marshal(v["items"])
	json.Unmarshal(raw, &state)
	snapshot := state.Subjects[0]
	snapshot.Provider = "Isolated CLI reference"
	snapshot.ReferenceURL = "https://example.invalid/reference"
	for i := range snapshot.Packages {
		a := []pluginsecurity.Advisory{}
		snapshot.Packages[i].Advisories = &a
	}
	plan := func(req pluginsecurity.Edit) map[string]any {
		t.Helper()
		_, out := call("mcp", "security", "state")
		raw, _ := json.Marshal(out["items"])
		json.Unmarshal(raw, &state)
		req.ExpectedSHA256 = state.SHA256
		if err := os.WriteFile(file, pluginsecurity.JSON(req), 0600); err != nil {
			t.Fatal(err)
		}
		exit, out := call("mcp", "security", "plan", "--file", file)
		if exit != 0 {
			t.Fatal(exit, out)
		}
		return out["items"].(map[string]any)["plan"].(map[string]any)
	}
	p := plan(pluginsecurity.Edit{Action: "import", Document: string(pluginsecurity.JSON(snapshot))})
	exit, v = call("mcp", "security", "check")
	if exit != 3 || v["items"].(map[string]any)["results"].([]any)[0].(map[string]any)["cve_status"] != "MISSING" {
		t.Fatal("preview wrote cache", exit, v)
	}
	exit, v = call("mcp", "security", "apply", "--plan", p["id"].(string), "--expected-digest", p["digest"].(string))
	if exit != 0 {
		t.Fatal(exit, v)
	}
	exit, v = call("mcp", "security", "check")
	if exit != 3 || v["items"].(map[string]any)["results"].([]any)[0].(map[string]any)["cve_status"] != "NO_KNOWN_MATCHES" {
		t.Fatal(exit, v)
	}
	a := []pluginsecurity.Advisory{{ID: "CVE-2026-12345", Severity: "high", Summary: "CLI fixture only", URL: "https://example.invalid/advisory"}}
	snapshot.Packages[0].Advisories = &a
	p = plan(pluginsecurity.Edit{Action: "import", Document: string(pluginsecurity.JSON(snapshot))})
	exit, v = call("mcp", "security", "apply", "--plan", p["id"].(string), "--expected-digest", p["digest"].(string))
	if exit != 0 {
		t.Fatal(exit, v)
	}
	exit, v = call("mcp", "security", "check")
	if exit != 5 || v["code"] != "STATIC_CHECK_FAILED" {
		t.Fatal(exit, v)
	}
	p = plan(pluginsecurity.Edit{Action: "rollback", Snapshot: p["id"].(string)})
	exit, v = call("mcp", "security", "apply", "--plan", p["id"].(string), "--expected-digest", p["digest"].(string))
	if exit != 0 {
		t.Fatal(exit, v)
	}
	exit, _ = call("mcp", "security", "check")
	if exit != 3 {
		t.Fatal(exit)
	}
	exit, v = call("mcp", "security", "recover")
	if exit != 0 {
		t.Fatal(exit, v)
	}
}
