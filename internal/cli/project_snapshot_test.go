package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestProjectSnapshotCLIHelpAndBinding(t *testing.T) {
	for _, args := range [][]string{{"snapshot", "list"}, {"snapshot", "check", "11111111-1111-4111-8111-111111111111"}} {
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
