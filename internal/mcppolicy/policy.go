// Package mcppolicy defines reviewed tool classifications and a deliberately
// narrow project/Public protocol adapter. Declarations never grant execution.
package mcppolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/pzqf/AIDock/internal/mcprelay"
)

const Adapter = "scoped-public-query-v1"

type Tool struct {
	Access     string          `json:"access"`
	Descriptor json.RawMessage `json:"descriptor"`
}
type Policy struct {
	Adapter string `json:"adapter"`
	Tools   []Tool `json:"tools"`
}

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func invalid() error {
	return fmt.Errorf("MCP_TOOL_POLICY_INVALID: 工具策略格式、描述或范围合同无效；仅支持 scoped-public-query-v1，不接受重复键、别名、空值或未知字段")
}
func bytesOf(v any) []byte { b, _ := json.Marshal(v); return b }
func object(raw []byte, allowed ...string) (map[string]json.RawMessage, error) {
	// The relay's canonical decoder checks duplicate keys at every depth, UTF-8,
	// depth and size before ordinary JSON decoding can discard information.
	if _, err := mcprelay.DescriptorDigest(raw); err != nil {
		return nil, invalid()
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, invalid()
	}
	for key, v := range m {
		found := false
		for _, k := range allowed {
			if key == k {
				found = true
			}
		}
		if !found || string(v) == "null" {
			return nil, invalid()
		}
	}
	return m, nil
}
func text(raw json.RawMessage, max int) (string, bool) {
	var s string
	err := json.Unmarshal(raw, &s)
	return s, err == nil && string(raw) != "null" && len(s) <= max
}

// Parse accepts only the fixed adapter and exact tool descriptions. Access is
// independently reviewed; readOnlyHint or other server annotations grant none.
func Parse(raw []byte) (Policy, error) {
	var out Policy
	if len(raw) > 256<<10 {
		return out, invalid()
	}
	m, err := object(raw, "adapter", "tools")
	if err != nil || len(m) != 2 || json.Unmarshal(raw, &out) != nil || out.Adapter != Adapter || out.Tools == nil || len(out.Tools) > 64 {
		return Policy{}, invalid()
	}
	var tools []json.RawMessage
	if json.Unmarshal(m["tools"], &tools) != nil {
		return Policy{}, invalid()
	}
	seen := map[string]bool{}
	for i, t := range tools {
		v, err := object(t, "access", "descriptor")
		if err != nil || len(v) != 2 || (out.Tools[i].Access != "read" && out.Tools[i].Access != "write") {
			return Policy{}, invalid()
		}
		name, err := Descriptor(v["descriptor"])
		if err != nil || seen[strings.ToLower(name)] {
			return Policy{}, invalid()
		}
		seen[strings.ToLower(name)] = true
	}
	return out, nil
}

// FromDefinition distinguishes omission (legacy readable, no tools authorized)
// from an explicit invalid/null declaration. No process is consulted.
func FromDefinition(server map[string]any) (*Policy, error) {
	v, exists := server["tool_policy"]
	if !exists {
		return nil, nil
	}
	p, err := Parse(bytesOf(v))
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Descriptor accepts only Public-reviewable MCP metadata and the adapter's
// exact four-string input schema. There are no extension or hidden _meta fields.
func Descriptor(raw []byte) (string, error) {
	m, err := object(raw, "name", "title", "description", "inputSchema", "annotations")
	if err != nil {
		return "", invalid()
	}
	name, ok := text(m["name"], 64)
	if !ok || !namePattern.MatchString(name) {
		return "", invalid()
	}
	for key, max := range map[string]int{"title": 256, "description": 8192} {
		if v, exists := m[key]; exists {
			if _, ok := text(v, max); !ok {
				return "", invalid()
			}
		}
	}
	schema, err := object(m["inputSchema"], "type", "properties", "required", "additionalProperties")
	if err != nil || len(schema) != 4 || string(schema["type"]) != `"object"` || string(schema["additionalProperties"]) != "false" {
		return "", invalid()
	}
	properties, err := object(schema["properties"], "project", "scope", "copy", "query")
	if err != nil || len(properties) != 4 {
		return "", invalid()
	}
	for _, v := range properties {
		p, err := object(v, "type")
		if err != nil || len(p) != 1 || string(p["type"]) != `"string"` {
			return "", invalid()
		}
	}
	var required []string
	if json.Unmarshal(schema["required"], &required) != nil || len(required) != 4 {
		return "", invalid()
	}
	seen := map[string]bool{}
	for _, k := range required {
		if properties[k] == nil || seen[k] {
			return "", invalid()
		}
		seen[k] = true
	}
	if v, exists := m["annotations"]; exists {
		a, err := object(v, "title", "readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint")
		if err != nil {
			return "", invalid()
		}
		for k, v := range a {
			if k == "title" {
				if _, ok := text(v, 256); !ok {
					return "", invalid()
				}
				continue
			}
			if string(v) != "true" && string(v) != "false" {
				return "", invalid()
			}
		}
	}
	return name, nil
}

func (p Policy) Digest() string { h, _ := mcprelay.DescriptorDigest(bytesOf(p)); return h }
func (p Policy) Names() []string {
	out := []string{}
	for _, t := range p.Tools {
		name, _ := Descriptor(t.Descriptor)
		out = append(out, name)
	}
	return out
}
func (p Policy) Relay(b mcprelay.Binding, recheck func(context.Context) error, audit func(mcprelay.Event) error) (mcprelay.Policy, error) {
	validated, err := Parse(bytesOf(p))
	if err != nil || b.Project == "" || b.Scope == "" || b.Copy == "" || b.Plugin == "" || b.DataLevel != "Public" || b.Destination != "unknown-external-client" || recheck == nil || audit == nil {
		return mcprelay.Policy{}, invalid()
	}
	tools := map[string]mcprelay.Tool{}
	for _, t := range validated.Tools {
		name, _ := Descriptor(t.Descriptor)
		h, _ := mcprelay.DescriptorDigest(t.Descriptor)
		tools[name] = mcprelay.Tool{Access: t.Access, DescriptorSHA256: h}
	}
	return mcprelay.Policy{Binding: b, Tools: tools, Recheck: recheck, Audit: audit, Arguments: arguments, Result: result}, nil
}

func bound(m map[string]json.RawMessage, b mcprelay.Binding) bool {
	for k, want := range map[string]string{"project": b.Project, "scope": b.Scope, "copy": b.Copy} {
		got, ok := text(m[k], 4096)
		if !ok || got != want {
			return false
		}
	}
	return true
}
func arguments(b mcprelay.Binding, _ string, raw json.RawMessage) error {
	m, err := object(raw, "project", "scope", "copy", "query")
	if err != nil || len(m) != 4 || !bound(m, b) {
		return invalid()
	}
	if _, ok := text(m["query"], 8192); !ok {
		return invalid()
	}
	return nil
}
func result(b mcprelay.Binding, method string, raw json.RawMessage) error {
	if method == "tools/list" {
		_, err := Descriptor(raw)
		return err
	}
	m, err := object(raw, "project", "scope", "copy", "level", "content", "isError")
	if err != nil || len(m) < 5 || !bound(m, b) {
		return invalid()
	}
	level, ok := text(m["level"], 32)
	if !ok || level != "Public" || b.DataLevel != "Public" || b.Destination != "unknown-external-client" {
		return invalid()
	}
	if v, exists := m["isError"]; exists && string(v) != "true" && string(v) != "false" {
		return invalid()
	}
	var content []json.RawMessage
	if len(raw) > 256<<10 || json.Unmarshal(m["content"], &content) != nil || content == nil || len(content) > 64 {
		return invalid()
	}
	for _, v := range content {
		item, err := object(v, "type", "text")
		if err != nil || len(item) != 2 || string(item["type"]) != `"text"` {
			return invalid()
		}
		if _, ok := text(item["text"], 256<<10); !ok {
			return invalid()
		}
	}
	return nil
}

// Example is a draft template, never an observed or preapproved tool. The actual
// plugin must expose the exact reviewed descriptor before the relay lists it.
func Example() Policy {
	return Policy{Adapter: Adapter, Tools: []Tool{{Access: "read", Descriptor: json.RawMessage(`{"name":"project_query","description":"检索当前项目明确标为公开的资料","inputSchema":{"type":"object","properties":{"project":{"type":"string"},"scope":{"type":"string"},"copy":{"type":"string"},"query":{"type":"string"}},"required":["project","scope","copy","query"],"additionalProperties":false}}`)}}}
}
