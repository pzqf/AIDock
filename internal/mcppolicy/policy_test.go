package mcppolicy

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pzqf/AIDock/internal/mcprelay"
)

func TestPolicyStrictShapeAndIndependentClassification(t *testing.T) {
	p := Example()
	if _, err := Parse(bytesOf(p)); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"unknown", "alias", "null", "duplicate", "array", "adapter", "access", "name", "case-name", "schema", "schema-extra", "schema-required", "annotations", "metadata", "large"} {
		t.Run(kind, func(t *testing.T) {
			raw := string(bytesOf(Example()))
			switch kind {
			case "unknown":
				raw = strings.Replace(raw, `"adapter":`, `"private-sentinel":true,"adapter":`, 1)
			case "alias":
				raw = strings.Replace(raw, `"adapter":`, `"Adapter":`, 1)
			case "null":
				raw = strings.Replace(raw, `"tools":[`, `"tools":null,"other":[`, 1)
			case "duplicate":
				raw = strings.Replace(raw, `"access":"read"`, `"access":"write","access":"read"`, 1)
			case "array":
				raw = `[]`
			case "adapter":
				raw = strings.Replace(raw, Adapter, "unreviewed", 1)
			case "access":
				raw = strings.Replace(raw, `"access":"read"`, `"access":"auto"`, 1)
			case "name":
				raw = strings.Replace(raw, "project_query", "tools/list", 1)
			case "case-name":
				q := Example()
				q.Tools = append(q.Tools, Tool{"read", json.RawMessage(strings.Replace(string(q.Tools[0].Descriptor), "project_query", "Project_Query", 1))})
				raw = string(bytesOf(q))
			case "schema":
				raw = strings.Replace(raw, `"additionalProperties":false`, `"additionalProperties":true`, 1)
			case "schema-extra":
				raw = strings.Replace(raw, `"properties":{`, `"properties":{"private-sentinel":{"type":"string"},`, 1)
			case "schema-required":
				raw = strings.Replace(raw, `"required":["project"`, `"required":["query"`, 1)
			case "annotations":
				raw = strings.Replace(raw, `"inputSchema":`, `"annotations":{"readOnlyHint":"private-sentinel"},"inputSchema":`, 1)
			case "metadata":
				raw = strings.Replace(raw, `"inputSchema":`, `"_meta":{"private-sentinel":true},"inputSchema":`, 1)
			case "large":
				raw = strings.Repeat(" ", 256<<10) + raw
			}
			if _, err := Parse([]byte(raw)); err == nil || strings.Contains(err.Error(), "private-sentinel") {
				t.Fatal("invalid policy accepted or echoed", err)
			}
		})
	}
	// Server hints never change the separately chosen write classification.
	p.Tools[0].Access = "write"
	p.Tools[0].Descriptor = json.RawMessage(strings.Replace(string(p.Tools[0].Descriptor), `"inputSchema":`, `"annotations":{"readOnlyHint":true},"inputSchema":`, 1))
	q, err := p.Relay(testBinding(), func(context.Context) error { return nil }, func(mcprelay.Event) error { return nil })
	if err != nil || q.Tools["project_query"].Access != "write" {
		t.Fatal(q, err)
	}
	if _, err := p.Relay(testBinding(), nil, func(mcprelay.Event) error { return nil }); err == nil {
		t.Fatal("nil recheck")
	}
}

func testBinding() mcprelay.Binding {
	return mcprelay.Binding{Project: "project-a", Scope: "server", Copy: "copy-a", Plugin: "owned", DataLevel: "Public", Destination: "unknown-external-client"}
}
func TestScopeAdapterRejectsUnfilteredParametersAndResults(t *testing.T) {
	p, err := Example().Relay(testBinding(), func(context.Context) error { return nil }, func(mcprelay.Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	good := `{"project":"project-a","scope":"server","copy":"copy-a","query":"public question"}`
	if err := p.Arguments(p.Binding, "project_query", json.RawMessage(good)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{strings.Replace(good, "project-a", "project-b", 1), strings.Replace(good, "server", "client", 1), strings.Replace(good, "copy-a", "copy-b", 1), strings.Replace(good, `"query":`, `"project_id":"other","query":`, 1), strings.Replace(good, `"query":"public question"`, `"query":{"project":"other"}`, 1), strings.Replace(good, `"query":`, `"Query":`, 1), strings.Replace(good, `"query":`, `"project":"other","query":`, 1), `null`} {
		if p.Arguments(p.Binding, "project_query", json.RawMessage(raw)) == nil {
			t.Fatal("accepted arguments", raw)
		}
	}
	result := `{"project":"project-a","scope":"server","copy":"copy-a","level":"Public","content":[{"type":"text","text":"public answer"}]}`
	if p.Result(p.Binding, "project_query", json.RawMessage(result)) != nil {
		t.Fatal("public result denied")
	}
	for _, raw := range []string{strings.Replace(result, "Public", "Internal", 1), strings.Replace(result, "project-a", "project-b", 1), strings.Replace(result, "copy-a", "copy-b", 1), strings.Replace(result, `"content":`, `"structuredContent":{"secret":"private-sentinel"},"content":`, 1), strings.Replace(result, `"type":"text","text":"public answer"`, `"type":"resource","resource":{"uri":"private"}`, 1), strings.Replace(result, `"text":"public answer"`, `"text":"public answer","_meta":{}`, 1), strings.Replace(result, `"level":"Public"`, `"level":"Internal","level":"Public"`, 1)} {
		if p.Result(p.Binding, "project_query", json.RawMessage(raw)) == nil {
			t.Fatal("accepted result", raw)
		}
	}
	b := testBinding()
	b.Destination = "remote-provider"
	if _, err := Example().Relay(b, func(context.Context) error { return nil }, func(mcprelay.Event) error { return nil }); err == nil {
		t.Fatal("unknown destination raised authority")
	}
}

func TestReviewedPolicyDrivesRealRelayAndDeniedCallsNeverReachServer(t *testing.T) {
	policy := Example()
	write := policy.Tools[0]
	write.Access = "write"
	write.Descriptor = json.RawMessage(strings.Replace(string(write.Descriptor), "project_query", "project_save", 1))
	policy.Tools = append(policy.Tools, write)
	var forwards atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, edge := net.Pipe()
	upstream, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	p, err := policy.Relay(testBinding(), func(context.Context) error { return nil }, func(mcprelay.Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- mcprelay.Serve(ctx, mcprelay.Endpoint{Reader: edge, Writer: edge}, mcprelay.Endpoint{Reader: upstream, Writer: upstream}, p)
	}()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		d := json.NewDecoder(server)
		e := json.NewEncoder(server)
		for {
			var v map[string]json.RawMessage
			if d.Decode(&v) != nil {
				return
			}
			if v["id"] == nil {
				continue
			}
			var method string
			json.Unmarshal(v["method"], &method)
			var value any
			switch method {
			case "initialize":
				value = map[string]any{"protocolVersion": "2025-11-25"}
			case "tools/list":
				value = map[string]any{"tools": []json.RawMessage{policy.Tools[0].Descriptor, policy.Tools[1].Descriptor, json.RawMessage(`{"name":"unknown"}`)}}
			case "tools/call":
				forwards.Add(1)
				var args struct{ Arguments struct{ Query string } }
				json.Unmarshal(v["params"], &args)
				level := "Public"
				if args.Arguments.Query == "private" {
					level = "Internal"
				}
				value = map[string]any{"project": "project-a", "scope": "server", "copy": "copy-a", "level": level, "content": []any{map[string]string{"type": "text", "text": "answer"}}}
			}
			if e.Encode(map[string]any{"jsonrpc": "2.0", "id": v["id"], "result": value}) != nil {
				return
			}
		}
	}()
	d := json.NewDecoder(client)
	e := json.NewEncoder(client)
	id := 0
	request := func(method string, params any) map[string]json.RawMessage {
		t.Helper()
		id++
		client.SetDeadline(time.Now().Add(time.Second))
		if err := e.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
			t.Fatal(err)
		}
		var v map[string]json.RawMessage
		if err := d.Decode(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if request("initialize", map[string]string{"protocolVersion": "2025-11-25"})["error"] != nil {
		t.Fatal("initialize")
	}
	if e.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}) != nil {
		t.Fatal("notification")
	}
	var list struct{ Tools []any }
	json.Unmarshal(request("tools/list", map[string]any{})["result"], &list)
	if len(list.Tools) != 2 {
		t.Fatal(list)
	}
	args := map[string]string{"project": "project-a", "scope": "server", "copy": "copy-a", "query": "public"}
	if request("tools/call", map[string]any{"name": "project_query", "arguments": args})["error"] != nil {
		t.Fatal("public read failed")
	}
	for _, name := range []string{"project_save", "unknown"} {
		if request("tools/call", map[string]any{"name": name, "arguments": args})["error"] == nil {
			t.Fatal("write/unknown forwarded")
		}
	}
	args["project"] = "other"
	if request("tools/call", map[string]any{"name": "project_query", "arguments": args})["error"] == nil {
		t.Fatal("other project forwarded")
	}
	if forwards.Load() != 1 {
		t.Fatal("denied requests reached server", forwards.Load())
	}
	args["project"] = "project-a"
	args["query"] = "private"
	if request("tools/call", map[string]any{"name": "project_query", "arguments": args})["error"] == nil {
		t.Fatal("private result forwarded")
	}
	client.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("relay did not finish")
	}
	<-serverDone
}
