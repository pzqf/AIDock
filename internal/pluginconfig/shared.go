package pluginconfig

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"
)

// Shared contains raw, portable declarations, never expanded machine paths or
// schema fallbacks. Fields includes absent fields so adoption can remove an old
// override without replacing other fields belonging to the destination.
type Shared struct {
	Fields []string       `json:"fields"`
	Values map[string]any `json:"values"`
	Remove []string       `json:"remove"`
}

var machinePath = regexp.MustCompile(`(?i)([a-z]:[\\/]|localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\])`)
var credentialPattern = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{12,}|gh[pousr]_[a-z0-9]{20,}|-----BEGIN[^\r\n]*PRIVATE KEY|bearer\s+\S+|https?://[^/\s@]+:[^/\s@]+@)`)

// SharedText checks a declaration's ordinary string, not a credential alias.
// It recognizes known local/credential shapes; it is not a secret detector.
func SharedText(value string) bool { return portableValue(Field{Type: "string"}, value) }
func SharedProjectPath(value, project string) bool {
	f := Field{Type: "directory"}
	_, err := fieldValue(f, value, map[string]string{"PROJECT": project})
	return err == nil && portableValue(f, value)
}
func CredentialShape(value string) bool { return credentialPattern.MatchString(value) }

func portableValue(f Field, value any) bool {
	if items, ok := value.([]any); ok {
		for _, item := range items {
			if !portableValue(f, item) {
				return false
			}
		}
		return true
	}
	s, ok := value.(string)
	if !ok {
		return true
	}
	if machinePath.MatchString(s) || credentialPattern.MatchString(s) || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "\\") || strings.HasPrefix(s, "~/") || strings.HasPrefix(s, "~\\") {
		return false
	}
	if strings.Contains(strings.ReplaceAll(s, "${PROJECT}", ""), "${") {
		return false
	}
	if f.Type == "directory" {
		if s == "${PROJECT}" {
			return true
		}
		if !strings.HasPrefix(s, "${PROJECT}/") {
			return false
		}
		relative := strings.TrimPrefix(s, "${PROJECT}/")
		return relative != "" && !strings.Contains(relative, "\\") && path.Clean(relative) == relative && relative != ".." && !strings.HasPrefix(relative, "../")
	}
	return true
}

// ProjectShared requires explicit per-field ownership from the verified schema.
// Omitted sync is local. A bad declared shared value blocks the whole plugin
// unit; it is never silently omitted and then interpreted as a remote deletion.
// removed contains field names, not arbitrary pointers.
func ProjectShared(schema map[string]any, delivery string, overrides any, present bool, variables map[string]string, removed []string) (Shared, error) {
	out := Shared{Fields: []string{}, Values: map[string]any{}, Remove: []string{}}
	if delivery != Delivery {
		return out, invalid("PLUGIN_SHARED_CONTRACT_REQUIRED")
	}
	// Parse the same field DSL without requiring values supplied by another
	// layer. Still validate defaults and every metadata keyword.
	descriptions := map[string]any{}
	for name, raw := range schema {
		field, ok := raw.(map[string]any)
		if !ok {
			return out, invalid("PLUGIN_CONFIGURATION_SCHEMA_UNSUPPORTED")
		}
		copy := map[string]any{}
		for key, value := range field {
			copy[key] = value
		}
		copy["required"] = false
		descriptions[name] = copy
	}
	descriptor, err := Compile(descriptions, delivery, nil, false, variables)
	if err != nil {
		return out, err
	}
	values := map[string]any{}
	if present {
		var ok bool
		values, ok = overrides.(map[string]any)
		if !ok {
			return out, invalid("PLUGIN_SHARED_CONFIGURATION_INVALID")
		}
	}
	removals := map[string]bool{}
	for _, name := range removed {
		removals[name] = true
	}
	for _, field := range descriptor.Fields {
		if field.Sync != "shared" {
			continue
		}
		out.Fields = append(out.Fields, field.Name)
		if value, exists := values[field.Name]; exists {
			if _, err := fieldValue(field, value, variables); err != nil {
				return out, invalid("PLUGIN_SHARED_CONFIGURATION_INVALID")
			}
			if !portableValue(field, value) {
				return out, invalid("PLUGIN_SHARED_VALUE_BLOCKED")
			}
			out.Values[field.Name] = value
		}
		if removals[field.Name] {
			out.Remove = append(out.Remove, field.Name)
		}
	}
	if data, err := json.Marshal(out.Values); err != nil || len(data) > MaxBytes {
		return Shared{}, invalid("PLUGIN_SHARED_CONFIGURATION_INVALID")
	}
	return out, nil
}
