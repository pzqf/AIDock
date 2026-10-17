package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestReplayControlCLIHelpAndExplicitBinding(t *testing.T) {
	for _, action := range []string{"plan", "planned", "apply", "reverse", "history", "inspect", "recover"} {
		var out, err bytes.Buffer
		if n := Run(context.Background(), []string{"snapshot", "replay", action, "--help"}, &out, &err); n != 0 || !strings.Contains(out.String(), "Usage:") {
			t.Fatal(action, n, out.String(), err.String())
		}
		exit, v, _ := invoke(t, "snapshot", "replay", action, "--json")
		if exit == 0 || v["code"] != "SOURCE_BINDING_REQUIRED" {
			t.Fatal(action, exit, v)
		}
	}
}
