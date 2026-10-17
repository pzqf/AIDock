package profile

import "testing"

func TestRegisteredPluginMCPUsesLayerOrderAndRejectsSameLayerContradiction(t *testing.T) {
	catalog := Catalog{Plugins: []string{"service"}, MCP: []string{"service"}}
	base := Input{Catalog: catalog, MCPDefaults: map[string]bool{"service": true}}
	legacy, err := Resolve(Input{Catalog: catalog, MCPDefaults: base.MCPDefaults, Project: document("p", nil)})
	base.PluginMCP = map[string]string{"service": "service"}
	base.Project = document("p", nil)
	withMapping, err2 := Resolve(base)
	if err != nil || err2 != nil || legacy.Digest != withMapping.Digest {
		t.Fatal("unused mapping changed legacy report", err, err2)
	}
	base.Project = document("p", map[string]any{"plugins": map[string]any{"disable": []any{"service"}}})
	base.Local = document("local", map[string]any{"mcp": map[string]any{"enable": []any{"service"}}})
	r, err := Resolve(base)
	if err != nil || encoded(object(r.Effective["mcp"])["enable"]) != `["service"]` || encoded(object(r.Effective["plugins"])["enable"]) != `["service"]` {
		t.Fatal(r, err)
	}
	base.Local = document("local", map[string]any{"mcp": map[string]any{"enable": []any{"service"}}, "plugins": map[string]any{"disable": []any{"service"}}})
	if _, err := Resolve(base); err == nil {
		t.Fatal("same layer contradiction accepted")
	}
}
