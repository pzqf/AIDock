package profile

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/pzqf/AIDock/internal/result"
)

const skillID = "11111111-1111-4111-8111-111111111111"

var revision = "sha256:" + strings.Repeat("a", 64)

func document(name string, fields map[string]any) map[string]any {
	v := map[string]any{"schema_version": 2, "name": name}
	for k, x := range fields {
		v[k] = x
	}
	return v
}
func encoded(v any) string { b, _ := json.Marshal(v); return string(b) }
func TestMCPDefinitionDefaultsAndSessionChoiceRemainExplicit(t *testing.T) {
	in := Input{Catalog: Catalog{MCP: []string{"memory"}}, MCPDefaults: map[string]bool{"memory": true}, Project: document("project", map[string]any{"mcp": map[string]any{"disable": []any{"memory"}}}), Session: document("session", map[string]any{"mcp": map[string]any{"enable": []any{"memory"}}})}
	before := encoded(in)
	r, err := Resolve(in)
	if err != nil {
		t.Fatal(err)
	}
	if encoded(r.Effective["mcp"]) != `{"disable":[],"enable":["memory"]}` || len(r.Trace) != 3 || r.Trace[0].Layer != "mcp-definition" || r.Trace[2].Previous != "project" || encoded(in) != before {
		t.Fatal(r)
	}
	in.MCPDefaults["missing"] = false
	if _, err = Resolve(in); err == nil {
		t.Fatal("unknown definition default accepted")
	}
}
func TestDeterministicFiveLayerResolutionPreservesInputsAndExplainsOverrides(t *testing.T) {
	input := Input{Catalog: Catalog{Skills: map[string][]string{skillID: {revision}}, Plugins: []string{"graph"}, MCP: []string{"memory"}},
		Defaults: document("default", map[string]any{"plugins": map[string]any{"enable": []any{"graph", "graph"}}, "rules": []any{"global"}, "config_overrides": map[string]any{"graph": map[string]any{"langs": []any{"go"}, "keep": true, "nil": 1}}}),
		Profiles: []map[string]any{
			document("base", map[string]any{"rules": []any{"base"}, "skills": map[string]any{"enable": []any{skillID}, "pins": map[string]any{skillID: revision}}}),
			document("go", map[string]any{"extends": "base", "rules": []any{"go"}, "config_overrides": map[string]any{"graph": map[string]any{"langs": []any{}}}}),
		},
		Project: document("project", map[string]any{"extends": "go", "plugins": map[string]any{"disable": []any{"graph"}}, "config_overrides": map[string]any{"graph": map[string]any{"nil": nil}}, "rules": []any{"project"}}),
		Local:   document("copy", map[string]any{"skills": map[string]any{"disable": []any{skillID}}, "config_remove": []any{"/graph/keep"}}),
		Session: document("temporary", map[string]any{"skills": map[string]any{"enable": []any{skillID}}, "rules": []any{"session"}}),
	}
	before := encoded(input)
	r, err := Resolve(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Layers) != 6 || encoded(r.Effective["rules"]) != `["global","base","go","project","session"]` {
		t.Fatal(r)
	}
	if encoded(object(r.Effective["plugins"])["disable"]) != `["graph"]` || encoded(object(r.Effective["skills"])["enable"]) != `["`+skillID+`"]` {
		t.Fatal(r.Effective)
	}
	if got := encoded(r.Effective["config_overrides"]); got != `{"graph":{"langs":[],"nil":null}}` {
		t.Fatal(got)
	}
	if before != encoded(input) {
		t.Fatal("resolution mutated caller layers")
	}
	var last Event
	for _, e := range r.Trace {
		if e.Path == "/skills/"+skillID {
			last = e
		}
	}
	if last.Layer != "session" || last.Previous != "local" || last.Action != "enable" {
		t.Fatal(last)
	}
	for i := 0; i < 20; i++ {
		again, e := Resolve(input)
		if e != nil || !reflect.DeepEqual(again, r) {
			t.Fatal("nondeterministic resolution", e)
		}
	}
}
func TestRulesReplacementAndJSONObjectArraySemantics(t *testing.T) {
	r, err := Resolve(Input{Defaults: document("global", map[string]any{"rules": []any{"duplicate", "duplicate"}, "config_overrides": map[string]any{"a": map[string]any{"old": 1}, "arr": []any{"a", "b", "c"}, "a/b": map[string]any{"~key": true}}}), Project: document("project", map[string]any{"rules_override": true, "rules": []any{}, "config_overrides": map[string]any{"a": false}, "config_remove": []any{"/arr/1", "/a~1b/~0key"}})})
	if err != nil {
		t.Fatal(err)
	}
	if encoded(r.Effective["rules"]) != `[]` || encoded(r.Effective["config_overrides"]) != `{"a":false,"a/b":{},"arr":["a","c"]}` {
		t.Fatal(r.Effective)
	}
	r, err = Resolve(Input{Defaults: document("global", map[string]any{"rules": []any{"x", "x"}}), Project: document("project", map[string]any{"rules": []any{"x"}})})
	if err != nil || encoded(r.Effective["rules"]) != `["x","x","x"]` {
		t.Fatal(r, err)
	}
}
func TestInvalidProfilesNeverChooseASilentWinner(t *testing.T) {
	base := document("base", map[string]any{"config_overrides": map[string]any{"a": map[string]any{"b": true, "c": 1}}})
	cases := []struct {
		name, code string
		in         Input
	}{
		{"cycle", "PROFILE_EXTENDS_CYCLE", Input{Profiles: []map[string]any{document("a", map[string]any{"extends": "b"}), document("b", map[string]any{"extends": "a"})}, Project: document("p", map[string]any{"extends": "a"})}},
		{"missing parent", "PROFILE_EXTENDS_MISSING", Input{Project: document("p", map[string]any{"extends": "missing"})}},
		{"duplicate name", "PROFILE_NAME_AMBIGUOUS", Input{Profiles: []map[string]any{base, base}, Project: document("p", nil)}},
		{"ambiguous switch", "AMBIGUOUS_OVERRIDE", Input{Project: document("p", map[string]any{"plugins": map[string]any{"enable": []any{"a"}, "disable": []any{"a"}}})}},
		{"unknown id", "PROFILE_UNKNOWN_ID", Input{Project: document("p", map[string]any{"mcp": map[string]any{"disable": []any{"unknown"}}})}},
		{"missing pin", "PROFILE_PIN_REQUIRED", Input{Catalog: Catalog{Skills: map[string][]string{skillID: {revision}}}, Project: document("p", map[string]any{"skills": map[string]any{"disable": []any{skillID}}})}},
		{"unknown revision", "PROFILE_REVISION_UNAVAILABLE", Input{Catalog: Catalog{Skills: map[string][]string{skillID: {revision}}}, Project: document("p", map[string]any{"skills": map[string]any{"pins": map[string]any{skillID: "sha256:" + strings.Repeat("b", 64)}}})}},
		{"overlapping patch", "AMBIGUOUS_OVERRIDE", Input{Defaults: base, Project: document("p", map[string]any{"config_overrides": map[string]any{"a": map[string]any{"b": false}}, "config_remove": []any{"/a"}})}},
		{"missing removal", "PROFILE_REMOVE_MISSING", Input{Defaults: base, Project: document("p", map[string]any{"config_remove": []any{"/a/missing"}})}},
		{"bad escape", "PROFILE_POINTER_INVALID", Input{Project: document("p", map[string]any{"config_remove": []any{"/a~2b"}})}},
		{"root removal", "PROFILE_POINTER_INVALID", Input{Project: document("p", map[string]any{"config_remove": []any{"a"}})}},
		{"parent local", "PROFILE_LAYER_INVALID", Input{Local: document("p", map[string]any{"extends": "base"})}},
		{"empty", "PROFILE_EMPTY", Input{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolve(tc.in)
			var typed *result.Error
			if !errors.As(err, &typed) || typed.Code != tc.code {
				t.Fatal(err)
			}
		})
	}
}
