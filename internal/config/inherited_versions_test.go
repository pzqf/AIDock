package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestInheritedVersionMetadataContract(t *testing.T) {
	for _, kind := range []string{"valid", "duplicate-revision", "duplicate-id", "foreign-id", "unknown-field", "escape", "foreign-source"} {
		t.Run(kind, func(t *testing.T) {
			body, err := os.ReadFile("testdata/skills.yaml")
			if err != nil {
				t.Fatal(err)
			}
			var index map[string]any
			if json.Unmarshal(body, &index) != nil {
				t.Fatal("fixture")
			}
			primary := index["skills"].([]any)[0].(map[string]any)
			id := primary["id"].(string)
			primary["source"].(map[string]any)["path"] = "skills/" + id
			encoded, _ := json.Marshal(primary)
			var alternative map[string]any
			json.Unmarshal(encoded, &alternative)
			alternative["revision"] = "sha256:" + strings.Repeat("2", 64)
			association := map[string]any{"owner": "global", "scope_id": "global", "skill": primary, "available_versions": []any{alternative}}
			doc := map[string]any{"schema_version": 1, "owner": index["owner"], "scope_id": "server", "skills": []any{association}}
			switch kind {
			case "duplicate-revision":
				alternative["revision"] = primary["revision"]
			case "duplicate-id":
				doc["skills"] = []any{association, association}
			case "foreign-id":
				alternative["id"] = "22222222-2222-4222-8222-222222222222"
			case "unknown-field":
				association["latest_version"] = true
			case "escape":
				alternative["files"].([]any)[0].(map[string]any)["path"] = "../secret.md"
			case "foreign-source":
				alternative["source"].(map[string]any)["path"] = "other/skill"
			}
			encoded, _ = json.Marshal(doc)
			_, err = Validate("skill-inheritance", encoded)
			if kind == "valid" && err != nil {
				t.Fatal(err)
			}
			if kind != "valid" && err == nil {
				t.Fatal("invalid retained-version contract accepted", kind)
			}
		})
	}
}
