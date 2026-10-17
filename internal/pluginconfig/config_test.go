package pluginconfig

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileLayerResultDefaultsNullAndDelivery(t *testing.T) {
	root := t.TempDir()
	schema := map[string]any{
		"languages": map[string]any{"type": "multi-select", "options": []any{"go", "typescript"}, "default": []any{"go"}},
		"index_dir": map[string]any{"type": "directory", "default": "${PROJECT}/index"},
		"optional":  map[string]any{"type": "string", "nullable": true, "default": "previous"},
		"limit":     map[string]any{"type": "integer", "required": true},
	}
	c, err := Compile(schema, Delivery, map[string]any{"languages": []any{}, "optional": nil, "limit": json.Number("9007199254740993")}, true, map[string]string{"PROJECT": root})
	if err != nil || ValidateDelivery(c.Data, c.SHA256) != nil {
		t.Fatal(c, err)
	}
	var value map[string]any
	if json.Unmarshal(c.Data, &value) != nil || value["index_dir"] != filepath.Join(root, "index") || value["optional"] != nil || len(value["languages"].([]any)) != 0 {
		t.Fatal(string(c.Data))
	}
	again, err := Compile(schema, Delivery, map[string]any{"limit": json.Number("9007199254740993"), "optional": nil, "languages": []any{}}, true, map[string]string{"PROJECT": root})
	if err != nil || c.SHA256 != again.SHA256 {
		t.Fatal("configuration not deterministic", err)
	}
	if _, err = Compile(schema, "", nil, false, nil); err != nil {
		t.Fatal("legacy no-config startup changed", err)
	}
	if _, err = Compile(schema, "", map[string]any{}, true, nil); err == nil {
		t.Fatal("undeclared delivery accepted")
	}
	removed, err := Compile(map[string]any{"message": map[string]any{"type": "string", "default": "must not return"}}, Delivery, map[string]any{}, true, nil, "message")
	if err != nil || string(removed.Data) != "{}" || len(removed.Omitted) != 1 {
		t.Fatal("explicit removal reintroduced schema default", removed, err)
	}
	if ValidateDelivery(append(c.Data, ' '), c.SHA256) == nil {
		t.Fatal("changed payload accepted")
	}
	b, _ := json.Marshal(c)
	if strings.Contains(string(b), root) || strings.Contains(string(b), "9007199254740993") {
		t.Fatal("configuration values leaked in diagnostic descriptor")
	}
}

func TestCompileRejectsUnknownSchemaFieldsTypesAndUnresolvedVariablesWithoutEcho(t *testing.T) {
	for _, tc := range []struct {
		schema map[string]any
		value  any
	}{
		{map[string]any{"value": map[string]any{"type": "string"}}, map[string]any{"unknown": "PRIVATE_CONFIGURATION_SENTINEL"}},
		{map[string]any{"value": map[string]any{"type": "string"}}, map[string]any{"value": "${SECRET:private_key}"}},
		{map[string]any{"value": map[string]any{"type": "integer"}}, map[string]any{"value": 1.5}},
		{map[string]any{"value": map[string]any{"type": "directory"}}, map[string]any{"value": "relative/path"}},
		{map[string]any{"value": map[string]any{"type": "string", "command": "PRIVATE_CONFIGURATION_SENTINEL"}}, map[string]any{}},
		{map[string]any{"value": map[string]any{"type": "secret"}}, map[string]any{}},
		{map[string]any{"value": map[string]any{"type": "multi-select", "options": []any{"go"}}}, map[string]any{"value": []any{"go", "go"}}},
		{map[string]any{"value": map[string]any{"type": "string", "required": true}}, map[string]any{}},
		{map[string]any{"value": map[string]any{"type": "string"}}, map[string]any{"value": strings.Repeat("a", 4097)}},
	} {
		if _, err := Compile(tc.schema, Delivery, tc.value, true, nil); err == nil || strings.Contains(err.Error(), "PRIVATE_CONFIGURATION_SENTINEL") || strings.Contains(err.Error(), "private_key") {
			t.Fatal(err)
		}
	}
}
