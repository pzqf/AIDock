package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/mcppolicy"
)

func TestMCPToolPolicyValidationAndLegacyOmission(t *testing.T) {
	b, err := os.ReadFile("testdata/mcp.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"legacy", "valid", "null", "invalid", "ambiguous"} {
		t.Run(kind, func(t *testing.T) {
			var doc map[string]any
			json.Unmarshal(b, &doc)
			s := doc["servers"].([]any)[0].(map[string]any)
			switch kind {
			case "valid":
				s["tool_policy"] = mcppolicy.Example()
			case "null":
				s["tool_policy"] = nil
			case "invalid":
				s["tool_policy"] = map[string]any{"private-sentinel": true}
			case "ambiguous":
				s["tool_policy"] = map[string]any{"adapter": mcppolicy.Adapter, "tools": []any{nil}}
			}
			raw, _ := json.Marshal(doc)
			_, err := Validate("mcp", raw)
			if (kind == "legacy" || kind == "valid") != (err == nil) {
				t.Fatal(err)
			}
			if err != nil && strings.Contains(err.Error(), "private-sentinel") {
				t.Fatal("policy value leaked", err)
			}
		})
	}
}
