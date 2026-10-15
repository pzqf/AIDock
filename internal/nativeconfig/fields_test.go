package nativeconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

var jsonContract = Contract{Path: ".mcp.json", Format: "json", Namespace: "mcpServers"}
var tomlContract = Contract{Path: ".codex/config.toml", Format: "toml", Namespace: "mcp_servers"}

func expectCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func desired(names ...string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for _, name := range names {
		out[name] = json.RawMessage(`{ "command": "aidock", "args": ["mcp", "exec", "--plugin", "demo"], "enabled": false }`)
	}
	return out
}

func TestMergePreservesUnmanagedBytesAndIsDeterministic(t *testing.T) {
	for _, test := range []struct {
		name      string
		c         Contract
		original  string
		protected []string
	}{
		{"json", jsonContract, "{\r\n \"model\" : \"keep\\tme\", \"mcpServers\": { \"custom\"  : { \"command\":\"personal\", \"env\": {\"TOKEN\":\"SENSITIVE\"} } },\r\n \"extra\": [1, 2, {\"x\":\"中文\"}]\r\n}\r\n", []string{`"model" : "keep\tme"`, `"custom"  : { "command":"personal", "env": {"TOKEN":"SENSITIVE"} }`, `"extra": [1, 2, {"x":"中文"}]`}},
		{"toml", tomlContract, "# personal comment\r\nmodel = 'keep' # inline\r\n[mcp_servers.custom]\r\ncommand = 'personal'\r\nenv = { TOKEN = 'SENSITIVE' }\r\n# keep final comment\r\n", []string{"# personal comment\r\nmodel = 'keep' # inline\r\n[mcp_servers.custom]\r\ncommand = 'personal'\r\nenv = { TOKEN = 'SENSITIVE' }\r\n# keep final comment\r\n"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := []byte(test.original)
			copyOfBefore := bytes.Clone(before)
			want := desired("aidock-b", "aidock-a")
			first, err := Merge(test.c, before, nil, want)
			if err != nil {
				t.Fatal(err)
			}
			for _, raw := range test.protected {
				if !bytes.Contains(first.Bytes, []byte(raw)) {
					t.Fatalf("lost unmanaged bytes %q in %s", raw, first.Bytes)
				}
			}
			for i := 0; i < 20; i++ {
				repeated, err := Merge(test.c, before, nil, want)
				if err != nil || !bytes.Equal(repeated.Bytes, first.Bytes) || !reflect.DeepEqual(repeated.Owned, first.Owned) {
					t.Fatalf("not deterministic: %v", err)
				}
			}
			second, err := Merge(test.c, first.Bytes, first.Owned, want)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(second.Bytes, first.Bytes) || !reflect.DeepEqual(second.Owned, first.Owned) {
				t.Fatalf("not idempotent:\n%s\n%s", first.Bytes, second.Bytes)
			}
			removed, err := Merge(test.c, second.Bytes, second.Owned, desired())
			if err != nil {
				t.Fatal(err)
			}
			for _, raw := range test.protected {
				if !bytes.Contains(removed.Bytes, []byte(raw)) {
					t.Fatalf("removal lost unmanaged bytes %q", raw)
				}
			}
			if len(removed.Owned) != 0 || bytes.Contains(removed.Bytes, []byte("aidock-a")) || bytes.Contains(removed.Bytes, []byte("aidock-b")) {
				t.Fatal("owned removal incomplete")
			}
			if !bytes.Equal(before, copyOfBefore) {
				t.Fatal("input mutated")
			}
			obs, err := Inspect(test.c, first.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			if len(obs.Fields) != 3 || obs.Fields[0].Name != "aidock-a" || obs.Fields[1].Name != "aidock-b" || obs.Fields[2].Name != "custom" {
				t.Fatalf("unstable field ordering: %+v", obs)
			}
			encoded, _ := json.Marshal(obs)
			if bytes.Contains(encoded, []byte("SENSITIVE")) || bytes.Contains(encoded, []byte("personal")) {
				t.Fatal("observation disclosed values")
			}
		})
	}
}

func TestJSONFieldPositionsAndRemoval(t *testing.T) {
	for _, original := range []string{
		`{}`,
		`{"mcpServers":{}}`,
		`{"mcpServers":{"custom":{"args":[{"nested": "}\\\""}]}}}`,
		"{\n\t\"mcpServers\": {\n\t\t\"custom\": {\"command\":\"x\"}\n\t}\n}",
	} {
		first, err := Merge(jsonContract, []byte(original), nil, desired("aidock-a", "aidock-b", "aidock-c"))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"aidock-a", "aidock-b", "aidock-c"} {
			want := desired("aidock-a", "aidock-b", "aidock-c")
			delete(want, name)
			p, err := Merge(jsonContract, first.Bytes, first.Owned, want)
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid(p.Bytes) || bytes.Contains(p.Bytes, []byte(name)) {
				t.Fatalf("invalid delete %s in %s", name, p.Bytes)
			}
		}
	}
}

func TestCollisionDriftAndBadOwnership(t *testing.T) {
	for _, c := range []Contract{jsonContract, tomlContract} {
		before := []byte(`{}`)
		if c.Format == "toml" {
			before = nil
		}
		first, err := Merge(c, before, nil, desired("aidock-a"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = Merge(c, first.Bytes, nil, desired("aidock-a"))
		expectCode(t, err, "CONFIG_UNMANAGED_COLLISION")
		modified := bytes.Replace(first.Bytes, []byte("aidock\""), []byte("changed\""), 1)
		if c.Format == "toml" {
			modified = bytes.Replace(first.Bytes, []byte("command = 'aidock'"), []byte("command = 'changed'"), 1)
		}
		if bytes.Equal(modified, first.Bytes) {
			t.Fatalf("fixture did not change: %s", first.Bytes)
		}
		_, err = Merge(c, modified, first.Owned, desired("aidock-a"))
		expectCode(t, err, "CONFIG_MANAGED_DRIFT")
		_, err = Merge(c, before, first.Owned, nil)
		expectCode(t, err, "CONFIG_MANAGED_DRIFT")
		_, err = Merge(c, before, nil, desired("custom"))
		expectCode(t, err, "CONFIG_OWNERSHIP_INVALID")
		_, err = Merge(c, before, map[string]string{"aidock-a": "forged"}, nil)
		expectCode(t, err, "CONFIG_OWNERSHIP_INVALID")
	}
}

func TestTOMLUnmarkedRepresentationsCannotBeClaimed(t *testing.T) {
	for _, original := range []string{
		"[mcp_servers.aidock-a]\ncommand = 'aidock'\n",
		"mcp_servers = { aidock-a = { command = 'aidock' } }\n",
		"mcp_servers.aidock-a.command = 'aidock'\n",
	} {
		obs, err := Inspect(tomlContract, []byte(original))
		if err != nil || len(obs.Fields) != 1 || obs.Fields[0].SHA256 != "" {
			t.Fatalf("unmarked observation: %+v, %v", obs, err)
		}
		_, err = Merge(tomlContract, []byte(original), nil, desired("aidock-a"))
		expectCode(t, err, "CONFIG_UNMANAGED_COLLISION")
		_, err = Merge(tomlContract, []byte(original), map[string]string{"aidock-a": hash([]byte(original))}, nil)
		expectCode(t, err, "CONFIG_MANAGED_DRIFT")
	}
	_, err := Merge(tomlContract, []byte("mcp_servers = { custom = { command = 'x' } }\n"), nil, desired("aidock-new"))
	expectCode(t, err, "CONFIG_LOSSLESS_UNSUPPORTED")
}

func TestTOMLMarkersCannotClaimOtherFields(t *testing.T) {
	first, err := Merge(tomlContract, nil, nil, desired("aidock-a"))
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{
		append(bytes.Clone(first.Bytes), []byte("[mcp_servers.aidock-a.env]\nSECRET = 'elsewhere'\n")...),
		[]byte(beginMarker("aidock-a") + "\nmodel = 'owned?'\n[mcp_servers.aidock-a]\ncommand='x'\n" + endMarker("aidock-a") + "\n"),
		[]byte("description = '''\n" + string(first.Bytes) + "'''\n[mcp_servers.aidock-a]\ncommand='outside'\n"),
		bytes.Replace(first.Bytes, []byte(endMarker("aidock-a")), []byte(endMarker("aidock-b")), 1),
		append(bytes.Clone(first.Bytes), []byte(endMarker("aidock-a"))...),
	} {
		_, err = Inspect(tomlContract, data)
		expectCode(t, err, "CONFIG_MARKER_INVALID")
	}
}

func TestInvalidInputsNeverExposeValues(t *testing.T) {
	for _, test := range []struct {
		c           Contract
		input, code string
	}{
		{jsonContract, `{"TOKEN":"secret","TOKEN":"secret2"}`, "CONFIG_DUPLICATE_KEY"},
		{jsonContract, `{"nested":[{"x":1,"\u0078":2}]}`, "CONFIG_DUPLICATE_KEY"},
		{jsonContract, `{"mcpServers":{"a":null}}`, "CONFIG_SERVER_INVALID"},
		{jsonContract, `{"mcpServers":[]}`, "CONFIG_NAMESPACE_INVALID"},
		{jsonContract, `[]`, "CONFIG_OBJECT_REQUIRED"},
		{jsonContract, `{"TOKEN":"super-secret"`, "CONFIG_JSON_INVALID"},
		{jsonContract, `{"\ud800": "unpaired"}`, "CONFIG_ENCODING_INVALID"},
		{jsonContract, `{"key": "\udc00"}`, "CONFIG_ENCODING_INVALID"},
		{tomlContract, "TOKEN='super-secret'\nTOKEN='duplicate'", "CONFIG_TOML_INVALID"},
		{tomlContract, "mcp_servers=[]", "CONFIG_NAMESPACE_INVALID"},
		{tomlContract, "[mcp_servers]\na='bad'", "CONFIG_SERVER_INVALID"},
		{jsonContract, "\xef\xbb\xbf{}", "CONFIG_JSON_INVALID"},
		{tomlContract, string([]byte{255}), "CONFIG_ENCODING_INVALID"},
		{jsonContract, strings.Repeat(" ", MaxBytes+1), "CONFIG_TOO_LARGE"},
		{Contract{Format: "yaml", Namespace: "model"}, "{}", "CONFIG_CONTRACT_UNSUPPORTED"},
	} {
		_, err := Inspect(test.c, []byte(test.input))
		expectCode(t, err, test.code)
		if strings.Contains(err.Error(), "secret") {
			t.Fatal("error disclosed configuration value")
		}
	}
}

func TestJSONSurrogatePairAndEscapedLiteralRemainDistinct(t *testing.T) {
	for _, input := range []string{`{"mcpServers":{"emoji-\ud83d\ude00":{}}}`, `{"mcpServers":{"literal-\\ud800":{}}}`} {
		if _, err := Inspect(jsonContract, []byte(input)); err != nil {
			t.Fatal(err)
		}
	}
}
