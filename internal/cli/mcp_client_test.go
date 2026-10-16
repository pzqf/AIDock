package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPExecErrorsNeverWriteOrdinaryResultsToProtocol(t *testing.T) {
	for _, args := range [][]string{
		{"mcp", "exec", "--json"},
		{"mcp", "exec", "--unknown-private-flag", "PRIVATE_SENTINEL", "--json"},
		{"--json", "mcp", "exec", "--plugin", "PRIVATE_SENTINEL"},
	} {
		var out, diagnostic bytes.Buffer
		exit := Run(context.Background(), args, &out, &diagnostic)
		if exit == 0 || out.Len() != 0 || strings.Contains(diagnostic.String(), "PRIVATE_SENTINEL") {
			t.Fatal(exit, out.String(), diagnostic.String())
		}
		var message map[string]string
		if err := json.Unmarshal(diagnostic.Bytes(), &message); err != nil || message["code"] == "" {
			t.Fatal(err, diagnostic.String())
		}
	}
}
