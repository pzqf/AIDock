package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestAppliedViewCLIHelpAndExplicitBinding(t *testing.T) {
	var out, err bytes.Buffer
	if n := Run(context.Background(), []string{"tool", "applied", "--help"}, &out, &err); n != 0 || !strings.Contains(out.String(), "Usage:") {
		t.Fatal(n, out.String(), err.String())
	}
	exit, v, _ := invoke(t, "tool", "applied", "--json")
	if exit == 0 || v["code"] != "SOURCE_BINDING_REQUIRED" {
		t.Fatal(exit, v)
	}
}
