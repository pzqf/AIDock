package cli

import "testing"

func TestMCPSessionCLIReadDoesNotLaunchAndProbeNeedsBoundReview(t *testing.T) {
	_, flags := mcpCLIArtifactFixture(t)
	flags = append(flags, "--copy", "session-cli-copy")
	exit, out, stderr := invoke(t, append([]string{"mcp", "session-check"}, flags...)...)
	if exit != 0 {
		t.Fatal(exit, out, stderr)
	}
	state := out["items"].(map[string]any)
	if state["audit_count"] != float64(0) || state["execution_ready"] != false {
		t.Fatal(out)
	}
	exit, out, _ = invoke(t, append([]string{"mcp", "session-check", "--probe"}, flags...)...)
	if exit == 0 || out["code"] != "INVALID_INPUT" {
		t.Fatal(exit, out)
	}
	exit, out, stderr = invoke(t, append([]string{"mcp", "session-recover"}, flags...)...)
	if exit != 0 || out["items"].(map[string]any)["recovered"] != float64(0) {
		t.Fatal(exit, out, stderr)
	}
}
