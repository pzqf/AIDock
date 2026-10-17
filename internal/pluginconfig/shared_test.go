package pluginconfig

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSharedProjectionPreservesRawVariablesNullEmptyAndLocalOwnership(t *testing.T) {
	schema := map[string]any{
		"directory": map[string]any{"type": "directory", "sync": "shared"},
		"languages": map[string]any{"type": "multi-select", "options": []any{"go"}, "default": []any{"go"}, "sync": "shared"},
		"optional":  map[string]any{"type": "string", "nullable": true, "sync": "shared"},
		"port":      map[string]any{"type": "integer", "sync": "local"},
		"private":   map[string]any{"type": "string", "required": true},
	}
	values := map[string]any{"directory": "${PROJECT}/index", "languages": []any{}, "optional": nil, "port": 8912, "private": "PRIVATE_LOCAL_SENTINEL"}
	p, err := ProjectShared(schema, Delivery, values, true, map[string]string{"PROJECT": t.TempDir()}, []string{"optional"})
	if err != nil || p.Values["directory"] != "${PROJECT}/index" || p.Values["optional"] != nil || len(p.Fields) != 3 || len(p.Remove) != 1 {
		t.Fatal(p, err)
	}
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), "PRIVATE_LOCAL_SENTINEL") || strings.Contains(string(b), "8912") || !strings.Contains(string(b), `"languages":[]`) {
		t.Fatal(string(b))
	}
	p, err = ProjectShared(schema, Delivery, nil, false, map[string]string{"PROJECT": t.TempDir()}, nil)
	if err != nil || len(p.Values) != 0 || len(p.Fields) != 3 {
		t.Fatal("defaults must not be materialized into shared overrides", p, err)
	}
}

func TestSharedProjectionBlocksMachineValuesSecretsAndUnknownDeclarations(t *testing.T) {
	for _, s := range []string{`C:\machine\index`, `/machine/index`, `\\server\share`, `${HOME}/index`, `${PROJECT}/../escape`, `localhost:8080`, `https://user:password@example.com`, "sk-PRIVATE_SENTINEL_LONG_KEY"} {
		t.Run(s, func(t *testing.T) {
			kind := "string"
			if strings.Contains(s, "../") {
				kind = "directory"
			}
			_, err := ProjectShared(map[string]any{"value": map[string]any{"type": kind, "sync": "shared"}}, Delivery, map[string]any{"value": s}, true, map[string]string{"PROJECT": t.TempDir(), "HOME": t.TempDir()}, nil)
			if err == nil || strings.Contains(err.Error(), s) {
				t.Fatal(err)
			}
		})
	}
	_, err := ProjectShared(map[string]any{"value": map[string]any{"type": "string", "sync": "anything"}}, Delivery, nil, false, nil, nil)
	if err == nil {
		t.Fatal("unknown ownership accepted")
	}
	_, err = ProjectShared(map[string]any{"value": map[string]any{"type": "multi-select", "sync": "shared", "options": []any{"go", "sk-PRIVATE_SENTINEL_LONG_KEY"}}}, Delivery, map[string]any{"value": []any{"sk-PRIVATE_SENTINEL_LONG_KEY"}}, true, nil, nil)
	if err == nil {
		t.Fatal("credential-like selected option accepted")
	}
	schema, values := map[string]any{}, map[string]any{}
	for _, name := range []string{"alpha", "bravo", "charlie", "delta", "echo"} {
		schema[name] = map[string]any{"type": "string", "sync": "shared"}
		values[name] = strings.Repeat("x", 4096)
	}
	if _, err := ProjectShared(schema, Delivery, values, true, nil, nil); err == nil {
		t.Fatal("oversized raw shared configuration accepted")
	}
}
