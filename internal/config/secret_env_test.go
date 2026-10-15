package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestMCPEnvSchemaAndRedactedDiagnostics(t *testing.T) {
	b, err := os.ReadFile("testdata/mcp.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"valid", "literal", "duplicate", "null", "reserved", "undeclared", "secret-value", "bad-parse"} {
		t.Run(kind, func(t *testing.T) {
			var doc map[string]any
			_ = json.Unmarshal(b, &doc)
			s := doc["servers"].([]any)[0].(map[string]any)
			s["secrets"] = []any{"alpha"}
			s["env"] = map[string]any{"API_TOKEN": "${SECRET:alpha}"}
			switch kind {
			case "literal":
				s["env"] = map[string]any{"API_TOKEN": "private-sentinel"}
			case "null":
				s["env"] = nil
			case "reserved":
				s["env"] = map[string]any{"NODE_OPTIONS": "${SECRET:alpha}"}
			case "undeclared":
				s["secrets"] = []any{}
			case "secret-value":
				s["secrets"] = map[string]any{"alpha": "private-sentinel"}
			}
			data, _ := json.Marshal(doc)
			if kind == "duplicate" {
				data = []byte("schema_version: 1\nservers:\n  - env: {private-sentinel: first, private-sentinel: second}\n")
			}
			if kind == "bad-parse" {
				data = []byte("servers: [private-sentinel\n")
			}
			_, err := Validate("mcp", data)
			if (kind == "valid") != (err == nil) {
				t.Fatal("unexpected validation", err)
			}
			if err != nil && strings.Contains(err.Error(), "private-sentinel") {
				t.Fatal("value escaped error", err)
			}
		})
	}
}
