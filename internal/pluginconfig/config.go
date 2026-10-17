// Package pluginconfig compiles the field schema in plugin.yaml. It performs
// no filesystem access, secret lookup, process execution or implicit transport.
package pluginconfig

import (
	"bytes"
	"encoding/json"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pzqf/AIDock/internal/result"
	"github.com/pzqf/AIDock/internal/workstore"
)

const Delivery = "env-json-v1"
const Variable = "AIDOCK_PLUGIN_CONFIG"
const MaxBytes = 16 << 10

type Field struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required"`
	Nullable    bool     `json:"nullable"`
	Options     []string `json:"options,omitempty"`
	Default     any      `json:"default,omitempty"`
	HasDefault  bool     `json:"has_default"`
	Sync        string   `json:"sync,omitempty"`
}

// Compiled contains only ordinary configuration. Data never enters logs, audit
// events, native launch arguments or the public diagnostic descriptor.
type Compiled struct {
	Delivery string   `json:"delivery,omitempty"`
	SHA256   string   `json:"sha256,omitempty"`
	Fields   []Field  `json:"fields,omitempty"`
	Omitted  []string `json:"omitted_fields,omitempty"`
	Data     []byte   `json:"-"`
}

var key = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func invalid(code string) error {
	return result.Fail(code, "插件配置未匹配已核验清单的字段、类型或交付合同；未交付配置值", 4)
}

func number(v any) (float64, bool) {
	b, err := json.Marshal(v)
	if err != nil || len(b) == 0 || b[0] == '"' || b[0] == 'n' || b[0] == 't' || b[0] == 'f' {
		return 0, false
	}
	n, err := strconv.ParseFloat(string(b), 64)
	return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
}

func fieldValue(f Field, v any, variables map[string]string) (any, error) {
	bad := func() (any, error) { return nil, invalid("PLUGIN_CONFIGURATION_INVALID") }
	if v == nil {
		if f.Nullable {
			return nil, nil
		}
		return bad()
	}
	contains := func(s string) bool {
		for _, option := range f.Options {
			if option == s {
				return true
			}
		}
		return false
	}
	switch f.Type {
	case "string", "directory", "select":
		s, ok := v.(string)
		if !ok || len(s) > 4096 || strings.ContainsRune(s, 0) {
			return bad()
		}
		if f.Type == "select" {
			if !contains(s) {
				return bad()
			}
			return s, nil
		}
		for _, name := range []string{"PROJECT", "HOME"} {
			if strings.Contains(s, "${"+name+"}") {
				value := variables[name]
				if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
					return bad()
				}
				s = strings.ReplaceAll(s, "${"+name+"}", value)
			}
		}
		if strings.Contains(s, "${") || len(s) > 4096 {
			return bad()
		}
		if f.Type == "directory" {
			if !filepath.IsAbs(s) {
				return bad()
			}
			s = filepath.Clean(s)
		}
		return s, nil
	case "boolean":
		if _, ok := v.(bool); !ok {
			return bad()
		}
		return v, nil
	case "number", "integer":
		n, ok := number(v)
		if !ok || f.Type == "integer" && math.Trunc(n) != n {
			return bad()
		}
		return v, nil
	case "multi-select":
		items, ok := v.([]any)
		if !ok || len(items) > 100 {
			return bad()
		}
		seen := map[string]bool{}
		out := []string{}
		for _, raw := range items {
			s, ok := raw.(string)
			if !ok || !contains(s) || seen[s] {
				return bad()
			}
			seen[s] = true
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, invalid("PLUGIN_CONFIGURATION_SCHEMA_UNSUPPORTED")
	}
}

// Compile accepts the frozen design's field-based config_schema. A plugin must
// explicitly declare config_delivery: env-json-v1 before values are delivered.
// Legacy plugins with no overrides and no delivery retain their old startup.
func Compile(schema map[string]any, delivery string, overrides any, present bool, variables map[string]string, omitted ...string) (Compiled, error) {
	out := Compiled{}
	if delivery == "" && !present {
		return out, nil
	}
	if delivery != Delivery {
		return out, invalid("PLUGIN_CONFIGURATION_DELIVERY_UNSUPPORTED")
	}
	values := map[string]any{}
	if present {
		var ok bool
		values, ok = overrides.(map[string]any)
		if !ok {
			return out, invalid("PLUGIN_CONFIGURATION_INVALID")
		}
	}
	if len(schema) > 64 || len(values) > 64 {
		return out, invalid("PLUGIN_CONFIGURATION_INVALID")
	}
	names := []string{}
	for name := range schema {
		names = append(names, name)
	}
	sort.Strings(names)
	resultValues := map[string]any{}
	suppressed := map[string]bool{}
	for _, name := range omitted {
		suppressed[name] = true
	}
	for name := range values {
		if _, ok := schema[name]; !ok {
			return out, invalid("PLUGIN_CONFIGURATION_UNKNOWN_FIELD")
		}
	}
	for _, name := range names {
		if !key.MatchString(name) {
			return out, invalid("PLUGIN_CONFIGURATION_SCHEMA_UNSUPPORTED")
		}
		raw, ok := schema[name].(map[string]any)
		if !ok {
			return out, invalid("PLUGIN_CONFIGURATION_SCHEMA_UNSUPPORTED")
		}
		f := Field{Name: name, Label: name}
		for k, v := range raw {
			switch k {
			case "type", "label", "description":
				s, ok := v.(string)
				if !ok || len(s) > 512 || strings.ContainsRune(s, 0) {
					return out, invalid("PLUGIN_CONFIGURATION_SCHEMA_UNSUPPORTED")
				}
				switch k {
				case "type":
					f.Type = s
				case "label":
					f.Label = s
				case "description":
					f.Description = s
				}
			case "required", "nullable":
				b, ok := v.(bool)
				if !ok {
					return out, invalid("PLUGIN_CONFIGURATION_SCHEMA_UNSUPPORTED")
				}
				if k == "required" {
					f.Required = b
				} else {
					f.Nullable = b
				}
			case "default":
				f.Default = v
				f.HasDefault = true
			case "sync":
				s, ok := v.(string)
				if !ok || s != "shared" && s != "local" {
					return out, invalid("PLUGIN_CONFIGURATION_SCHEMA_UNSUPPORTED")
				}
				f.Sync = s
			case "options":
				a, ok := v.([]any)
				if !ok || len(a) > 100 {
					return out, invalid("PLUGIN_CONFIGURATION_SCHEMA_UNSUPPORTED")
				}
				seen := map[string]bool{}
				for _, v := range a {
					s, ok := v.(string)
					if !ok || s == "" || len(s) > 256 || strings.Contains(s, "${") || seen[s] {
						return out, invalid("PLUGIN_CONFIGURATION_SCHEMA_UNSUPPORTED")
					}
					seen[s] = true
					f.Options = append(f.Options, s)
				}
			default:
				return out, invalid("PLUGIN_CONFIGURATION_SCHEMA_UNSUPPORTED")
			}
		}
		switch f.Type {
		case "string", "directory", "boolean", "number", "integer":
			if len(f.Options) > 0 {
				return out, invalid("PLUGIN_CONFIGURATION_SCHEMA_UNSUPPORTED")
			}
		case "select", "multi-select":
			if len(f.Options) == 0 {
				return out, invalid("PLUGIN_CONFIGURATION_SCHEMA_UNSUPPORTED")
			}
		default:
			return out, invalid("PLUGIN_CONFIGURATION_SCHEMA_UNSUPPORTED")
		}
		if f.HasDefault {
			if _, err := fieldValue(f, f.Default, variables); err != nil {
				return out, invalid("PLUGIN_CONFIGURATION_SCHEMA_INVALID")
			}
		}
		v, exists := values[name]
		if !exists && f.HasDefault && !suppressed[name] {
			v, exists = f.Default, true
		}
		if !exists {
			if f.Required {
				return out, invalid("PLUGIN_CONFIGURATION_REQUIRED")
			}
			out.Fields = append(out.Fields, f)
			if suppressed[name] {
				out.Omitted = append(out.Omitted, name)
			}
			continue
		}
		v, err := fieldValue(f, v, variables)
		if err != nil {
			return out, err
		}
		resultValues[name] = v
		out.Fields = append(out.Fields, f)
	}
	data, err := json.Marshal(resultValues)
	if err != nil || len(data) > MaxBytes {
		return out, invalid("PLUGIN_CONFIGURATION_TOO_LARGE")
	}
	out.Delivery, out.Data, out.SHA256 = Delivery, data, workstore.Hash(data)
	return out, nil
}

func ValidateDelivery(data []byte, digest string) error {
	if len(data) == 0 && digest == "" {
		return nil
	}
	if len(data) == 0 || len(data) > MaxBytes || workstore.Hash(data) != digest {
		return invalid("PLUGIN_CONFIGURATION_BINDING_MISMATCH")
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil || value == nil {
		return invalid("PLUGIN_CONFIGURATION_INVALID")
	}
	if encoded, err := json.Marshal(value); err != nil || string(encoded) != string(data) {
		return invalid("PLUGIN_CONFIGURATION_INVALID")
	}
	return nil
}
