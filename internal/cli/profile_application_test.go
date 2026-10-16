package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestProfileApplicationCLIContractAndMissingBinding(t *testing.T) {
	for _, action := range []string{"review", "plan", "apply", "history", "recover", "rollback-plan", "rollback-apply"} {
		var out, err bytes.Buffer
		if n := Run(context.Background(), []string{"tool", "profile", action, "--help"}, &out, &err); n != 0 || !strings.Contains(out.String(), "Usage:") {
			t.Fatal(action, n, out.String(), err.String())
		}
		out.Reset()
		err.Reset()
		code := Run(context.Background(), []string{"tool", "profile", action, "--json"}, &out, &err)
		var value struct {
			OK   bool
			Code string
		}
		if json.Unmarshal(out.Bytes(), &value) != nil || code == 0 || value.OK || value.Code != "SOURCE_BINDING_REQUIRED" {
			t.Fatal(action, code, out.String(), err.String())
		}
	}
}
