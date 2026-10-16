package mcprelay

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestRelayNegotiatesOnlyImplementedProtocolVersions(t *testing.T) {
	for _, tc := range []struct{ offered, forwarded, returned string }{
		{"2026-08-01", "2025-11-25", "2025-11-25"},
		{"2025-11-25", "2025-11-25", "2024-11-05"},
		{"2025-06-18", "2025-06-18", "2025-11-25"},
		{"2024-11-05", "2024-11-05", "2024-11-05"},
	} {
		t.Run(tc.offered+"/"+tc.returned, func(t *testing.T) {
			var events []Event
			var mu sync.Mutex
			h := newHarness(t, testPolicy(&events, &mu))
			put(t, h.client, req(1, "initialize", map[string]any{"protocolVersion": tc.offered, "capabilities": map[string]any{"PRIVATE_CAPABILITY": true}, "clientInfo": map[string]string{"name": "PRIVATE_CLIENT"}}))
			forwarded := get(t, h.upstream, h.sr)
			var params struct {
				Protocol string `json:"protocolVersion"`
			}
			if json.Unmarshal(forwarded["params"], &params) != nil || params.Protocol != tc.forwarded || strings.Contains(string(forwarded["params"]), "PRIVATE") {
				t.Fatal(string(forwarded["params"]))
			}
			put(t, h.upstream, map[string]any{"jsonrpc": "2.0", "id": forwarded["id"], "result": map[string]any{"protocolVersion": tc.returned, "instructions": "PRIVATE_SERVER"}})
			result := get(t, h.client, h.cr)
			if json.Unmarshal(result["result"], &params) != nil || params.Protocol != tc.returned || strings.Contains(string(result["result"]), "PRIVATE") {
				t.Fatal(string(result["result"]))
			}
			put(t, h.client, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
			get(t, h.upstream, h.sr)
			h.list(t)
		})
	}
}

func TestRelayStripsStandardClientMetadataWithoutGrantingAccess(t *testing.T) {
	var events []Event
	var mu sync.Mutex
	h := newHarness(t, testPolicy(&events, &mu))
	h.ready(t)
	put(t, h.client, req(2, "tools/list", map[string]any{"_meta": map[string]any{"progressToken": "PRIVATE_PROGRESS"}}))
	forwarded := get(t, h.upstream, h.sr)
	if string(forwarded["params"]) != "{}" {
		t.Fatal(string(forwarded["params"]))
	}
	put(t, h.upstream, map[string]any{"jsonrpc": "2.0", "id": forwarded["id"], "result": map[string]any{"tools": []json.RawMessage{readDescriptor}}})
	get(t, h.client, h.cr)
	put(t, h.client, req(3, "tools/call", map[string]any{"name": "read", "arguments": map[string]string{"project": "A"}, "_meta": map[string]any{"progressToken": "PRIVATE_PROGRESS"}}))
	forwarded = get(t, h.upstream, h.sr)
	if strings.Contains(string(forwarded["params"]), "PRIVATE") || strings.Contains(string(forwarded["params"]), "_meta") {
		t.Fatal(string(forwarded["params"]))
	}
	put(t, h.upstream, map[string]any{"jsonrpc": "2.0", "id": forwarded["id"], "result": map[string]any{"project": "A", "level": "Public", "content": []any{}}})
	get(t, h.client, h.cr)
	for _, tc := range []struct {
		method string
		params map[string]any
		code   string
	}{
		{"tools/list", map[string]any{"_meta": "PRIVATE_INVALID"}, "MCP_PARAMS_INVALID"},
		{"tools/list", map[string]any{"_meta": map[string]any{}, "unknown": true}, "MCP_PARAMS_INVALID"},
		{"tools/call", map[string]any{"name": "save", "arguments": map[string]string{"project": "A"}, "_meta": map[string]any{}}, "AUTHORIZATION_REQUIRED"},
	} {
		put(t, h.client, req(4, tc.method, tc.params))
		assertCode(t, get(t, h.client, h.cr), tc.code)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Contains(string(bytesOf(events)), "PRIVATE") {
		t.Fatal("metadata leaked to audit")
	}
}

func TestRelayRefusesUnknownUpstreamVersionAndMalformedOffer(t *testing.T) {
	var events []Event
	var mu sync.Mutex
	h := newHarness(t, testPolicy(&events, &mu))
	for _, version := range []any{nil, 123, "", strings.Repeat("x", 65)} {
		put(t, h.client, req(1, "initialize", map[string]any{"protocolVersion": version}))
		assertCode(t, get(t, h.client, h.cr), "MCP_VERSION_UNSUPPORTED")
	}
	put(t, h.client, req(2, "initialize", map[string]string{"protocolVersion": "2026-08-01"}))
	forwarded := get(t, h.upstream, h.sr)
	put(t, h.upstream, map[string]any{"jsonrpc": "2.0", "id": forwarded["id"], "result": map[string]string{"protocolVersion": "2026-08-01"}})
	if err := <-h.done; err == nil {
		t.Fatal("unknown upstream version accepted")
	}
}
