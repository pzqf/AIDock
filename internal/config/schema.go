package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"sync"

	"github.com/pzqf/AIDock/internal/mcppolicy"
	"github.com/pzqf/AIDock/internal/secref"
	"github.com/pzqf/AIDock/schemas"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var compiled struct {
	sync.Once
	items map[string]*jsonschema.Schema
	err   error
}

func Validate(kind string, data []byte) (any, error) {
	v, err := Parse(data)
	if err != nil {
		if kind == "mcp" {
			return nil, fmt.Errorf("MCP 配置无法解析；请核对格式、重复键与字段类型")
		}
		return nil, err
	}
	// Check sensitive fields before schema diagnostics can reflect their values.
	if kind == "mcp" {
		if doc, ok := v.(map[string]any); ok {
			if servers, ok := doc["servers"].([]any); ok {
				for _, raw := range servers {
					if s, ok := raw.(map[string]any); ok {
						if _, err := secref.Definition(s); err != nil {
							return nil, err
						}
						if _, err := mcppolicy.FromDefinition(s); err != nil {
							return nil, err
						}
					}
				}
			}
		}
	}

	compiled.Do(func() {
		c := jsonschema.NewCompiler()
		compiled.items = map[string]*jsonschema.Schema{}
		files, err := fs.Glob(schemas.Files, "*.json")
		if err != nil {
			compiled.err = err
			return
		}
		for _, name := range files {
			b, err := schemas.Files.ReadFile(name)
			if err != nil {
				compiled.err = err
				return
			}
			var doc any
			if err = json.Unmarshal(b, &doc); err != nil {
				compiled.err = err
				return
			}
			if err = c.AddResource("https://aidock.local/schemas/"+name, doc); err != nil {
				compiled.err = err
				return
			}
		}
		for _, name := range files {
			if name == "defs.schema.json" {
				continue
			}
			s, err := c.Compile("https://aidock.local/schemas/" + name)
			if err != nil {
				compiled.err = err
				return
			}
			compiled.items[name[:len(name)-len(".schema.json")]] = s
		}
	})
	if compiled.err != nil {
		return nil, compiled.err
	}
	s, found := compiled.items[kind]
	if !found {
		return nil, fmt.Errorf("未知配置类型 %q", kind)
	}
	// JSON round trip normalizes YAML integer types and rejects NaN/Infinity.
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	normal := json.NewDecoder(bytes.NewReader(b))
	normal.UseNumber()
	var normalized any
	if err = normal.Decode(&normalized); err != nil {
		return nil, err
	}
	if err = s.Validate(normalized); err != nil {
		return nil, err
	}
	if err = validateSemantics(kind, v); err != nil {
		return nil, err
	}
	return v, nil
}
