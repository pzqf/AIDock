// Package nativeconfig edits registered MCP server fields without serializing
// the surrounding user configuration. It grants no authority to execute MCPs.
package nativeconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

const MaxBytes = 1 << 20

type Contract struct {
	Path      string `json:"path"`
	Format    string `json:"format"`
	Namespace string `json:"namespace"`
	Reference string `json:"reference,omitempty"`
	Checked   string `json:"contract_checked,omitempty"`
}

type Field struct {
	Name           string   `json:"name"`
	Path           []string `json:"path"`
	SHA256         string   `json:"sha256"`
	Representation string   `json:"representation"`
}

type Observation struct {
	SHA256 string  `json:"sha256"`
	Bytes  int     `json:"bytes"`
	Fields []Field `json:"fields"`
}

// Proposal is deliberately in-memory. The native transaction owner must bind
// path, adapter, source and original bytes, lock the actual file, and recheck all
// inputs before committing it together with this updated ownership ledger.
type Proposal struct {
	Bytes []byte
	Owned map[string]string
}

type Error struct{ Code string }

func (e *Error) Error() string { return e.Code }
func fail(code string) error   { return &Error{Code: code} }

var managedName = regexp.MustCompile(`^aidock-[a-z0-9][a-z0-9-]{0,190}$`)

func hash(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func validContract(c Contract) bool {
	return c.Format == "json" && c.Namespace == "mcpServers" || c.Format == "toml" && c.Namespace == "mcp_servers"
}

type span struct{ start, end int }
type document struct {
	data           []byte
	fields         map[string]span
	representation map[string]string
	json           *jsonNode
}

func parse(c Contract, data []byte) (document, error) {
	d := document{data: data, fields: map[string]span{}, representation: map[string]string{}}
	if !validContract(c) {
		return d, fail("CONFIG_CONTRACT_UNSUPPORTED")
	}
	if len(data) > MaxBytes {
		return d, fail("CONFIG_TOO_LARGE")
	}
	if !utf8.Valid(data) {
		return d, fail("CONFIG_ENCODING_INVALID")
	}
	if c.Format == "json" {
		n, err := parseJSON(data)
		if err != nil {
			return d, err
		}
		d.json = n
		if n.kind != '{' {
			return d, fail("CONFIG_OBJECT_REQUIRED")
		}
		if ns := n.member(c.Namespace); ns != nil {
			if ns.value.kind != '{' {
				return d, fail("CONFIG_NAMESPACE_INVALID")
			}
			for _, v := range ns.value.members {
				if v.value.kind != '{' {
					return d, fail("CONFIG_SERVER_INVALID")
				}
				d.fields[v.key] = span{v.start, v.value.end}
				d.representation[v.key] = "json-member"
			}
		}
		return d, nil
	}
	var decoded map[string]any
	// Full TOML validation, including duplicate definitions. Error messages from
	// parsers can contain secret values, so expose fixed codes only.
	if err := toml.Unmarshal(data, &decoded); err != nil {
		return d, fail("CONFIG_TOML_INVALID")
	}
	if raw, ok := decoded[c.Namespace]; ok {
		ns, ok := raw.(map[string]any)
		if !ok {
			return d, fail("CONFIG_NAMESPACE_INVALID")
		}
		for name, value := range ns {
			if _, ok := value.(map[string]any); !ok {
				return d, fail("CONFIG_SERVER_INVALID")
			}
			// Unmarked TOML fields remain readable but cannot be claimed by
			// guessing the boundaries of adjacent handwritten comments/tables.
			d.fields[name] = span{-1, -1}
			d.representation[name] = "toml-unmanaged"
		}
	}
	marked, err := tomlBlocks(c, data)
	if err != nil {
		return d, err
	}
	for name, s := range marked {
		if _, ok := d.fields[name]; !ok {
			return d, fail("CONFIG_MARKER_INVALID")
		}
		var isolated map[string]any
		if err := toml.Unmarshal(data[s.start:s.end], &isolated); err != nil {
			return d, fail("CONFIG_MARKER_INVALID")
		}
		// An unmarked descendant table can extend a marked server. Never let
		// its fields escape the owned hash or silently affect a launcher.
		if !reflect.DeepEqual(isolated[c.Namespace].(map[string]any)[name], decoded[c.Namespace].(map[string]any)[name]) {
			return d, fail("CONFIG_MARKER_INVALID")
		}
		d.fields[name] = s
		d.representation[name] = "toml-marked-table"
	}
	return d, nil
}

func Inspect(c Contract, data []byte) (Observation, error) {
	d, err := parse(c, data)
	if err != nil {
		return Observation{}, err
	}
	out := Observation{SHA256: hash(data), Bytes: len(data), Fields: []Field{}}
	names := sortedKeys(d.fields)
	for _, name := range names {
		s := d.fields[name]
		digest := ""
		if s.start >= 0 {
			digest = hash(data[s.start:s.end])
		}
		out.Fields = append(out.Fields, Field{Name: name, Path: []string{c.Namespace, name}, SHA256: digest, Representation: d.representation[name]})
	}
	return out, nil
}

func sortedKeys[V any](values map[string]V) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// Merge refuses even byte-identical unowned collisions. Prefixes, comments and
// marker text do not confer ownership: a matching caller-held hash is required.
// Desired values are JSON server objects for both output formats. This API is
// internal and must only receive compiler-generated managed-launcher objects.
func Merge(c Contract, before []byte, owned map[string]string, desired map[string]json.RawMessage) (Proposal, error) {
	d, err := parse(c, before)
	if err != nil {
		return Proposal{}, err
	}
	for name, digest := range owned {
		if !managedName.MatchString(name) || len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" {
			return Proposal{}, fail("CONFIG_OWNERSHIP_INVALID")
		}
		s, ok := d.fields[name]
		if !ok || s.start < 0 || hash(before[s.start:s.end]) != digest {
			return Proposal{}, fail("CONFIG_MANAGED_DRIFT")
		}
	}
	for name, value := range desired {
		if !managedName.MatchString(name) {
			return Proposal{}, fail("CONFIG_OWNERSHIP_INVALID")
		}
		n, err := parseJSON(value)
		if err != nil || n.kind != '{' {
			return Proposal{}, fail("CONFIG_DESIRED_INVALID")
		}
		if _, exists := d.fields[name]; exists {
			if _, ours := owned[name]; !ours {
				return Proposal{}, fail("CONFIG_UNMANAGED_COLLISION")
			}
		}
	}
	after := bytes.Clone(before)
	// Apply in stable name order, reparsing offsets after each edit. Never rebuild
	// a user object, table, or file from a decoded map.
	for _, name := range sortedKeys(owned) {
		if _, keep := desired[name]; !keep {
			after, err = edit(c, after, name, nil)
			if err != nil {
				return Proposal{}, err
			}
		}
	}
	for _, name := range sortedKeys(desired) {
		after, err = edit(c, after, name, desired[name])
		if err != nil {
			return Proposal{}, err
		}
	}
	final, err := parse(c, after)
	if err != nil {
		return Proposal{}, fail("CONFIG_LOSSLESS_UNSUPPORTED")
	}
	out := Proposal{Bytes: after, Owned: map[string]string{}}
	for name := range desired {
		s, ok := final.fields[name]
		if !ok || s.start < 0 {
			return Proposal{}, fail("CONFIG_LOSSLESS_UNSUPPORTED")
		}
		out.Owned[name] = hash(after[s.start:s.end])
	}
	return out, nil
}

func edit(c Contract, data []byte, name string, value json.RawMessage) ([]byte, error) {
	d, err := parse(c, data)
	if err != nil {
		return nil, err
	}
	if c.Format == "json" {
		return editJSON(d, c.Namespace, name, value)
	}
	s, exists := d.fields[name]
	if exists && s.start < 0 {
		return nil, fail("CONFIG_LOSSLESS_UNSUPPORTED")
	}
	if value == nil {
		if !exists {
			return data, nil
		}
		return splice(data, s.start, s.end, nil), nil
	}
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		return nil, fail("CONFIG_DESIRED_INVALID")
	}
	// Round-trip desired JSON numbers to TOML-safe native primitives; launcher
	// values currently consist only of strings, booleans and string arrays.
	if !launcherValues(object) {
		return nil, fail("CONFIG_DESIRED_INVALID")
	}
	body, err := toml.Marshal(object)
	if err != nil {
		return nil, fail("CONFIG_DESIRED_INVALID")
	}
	newline := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		newline = "\r\n"
	}
	block := []byte(beginMarker(name) + "\n[" + c.Namespace + "." + name + "]\n" + string(body) + endMarker(name) + "\n")
	if newline != "\n" {
		block = bytes.ReplaceAll(block, []byte("\n"), []byte(newline))
	}
	if exists {
		return splice(data, s.start, s.end, block), nil
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(bytes.Clone(data), []byte(newline)...)
	}
	return append(bytes.Clone(data), block...), nil
}

func launcherValues(m map[string]any) bool {
	for _, v := range m {
		switch value := v.(type) {
		case string, bool:
		case []any:
			for _, item := range value {
				if _, ok := item.(string); !ok {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

func beginMarker(name string) string { return "# >>> AIDock MCP " + name + " >>>" }
func endMarker(name string) string   { return "# <<< AIDock MCP " + name + " <<<" }

func tomlBlocks(c Contract, data []byte) (map[string]span, error) {
	out := map[string]span{}
	comments := map[int]bool{}
	parser := unstable.Parser{KeepComments: true}
	parser.Reset(data)
	for parser.NextExpression() {
		n := parser.Expression()
		if n.Kind == unstable.Comment {
			comments[int(n.Raw.Offset)] = true
		}
	}
	if parser.Error() != nil {
		return nil, fail("CONFIG_TOML_INVALID")
	}
	// Check markers as standalone lines only. Multiline TOML strings containing
	// these lines are conservatively rejected rather than mistaken for authority.
	start, current, offset := -1, "", 0
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		text := strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
		if strings.Contains(text, "AIDock MCP") && (strings.Contains(text, ">>>") || strings.Contains(text, "<<<")) {
			if !comments[offset] {
				return nil, fail("CONFIG_MARKER_INVALID")
			}
			if strings.HasPrefix(text, "# >>> AIDock MCP ") && strings.HasSuffix(text, " >>>") {
				name := strings.TrimSuffix(strings.TrimPrefix(text, "# >>> AIDock MCP "), " >>>")
				if !managedName.MatchString(name) || current != "" {
					return nil, fail("CONFIG_MARKER_INVALID")
				}
				if _, exists := out[name]; exists {
					return nil, fail("CONFIG_MARKER_INVALID")
				}
				start, current = offset, name
			} else if current != "" && text == endMarker(current) {
				end := offset + len(line)
				var isolated map[string]any
				if err := toml.Unmarshal(data[start:end], &isolated); err != nil {
					return nil, fail("CONFIG_MARKER_INVALID")
				}
				ns, ok := isolated[c.Namespace].(map[string]any)
				if len(isolated) != 1 || !ok || len(ns) != 1 || ns[current] == nil {
					return nil, fail("CONFIG_MARKER_INVALID")
				}
				out[current] = span{start, end}
				start, current = -1, ""
			} else {
				return nil, fail("CONFIG_MARKER_INVALID")
			}
		}
		offset += len(line)
	}
	if current != "" {
		return nil, fail("CONFIG_MARKER_INVALID")
	}
	return out, nil
}

func splice(data []byte, start, end int, value []byte) []byte {
	out := make([]byte, 0, len(data)-(end-start)+len(value))
	out = append(out, data[:start]...)
	out = append(out, value...)
	return append(out, data[end:]...)
}

// jsonNode tracks original member/value offsets and detects decoded duplicate
// keys at every depth. encoding/json alone silently accepts duplicate keys.
type jsonNode struct {
	kind       byte
	start, end int
	members    []jsonMember
}
type jsonMember struct {
	key          string
	start, comma int
	value        *jsonNode
}

func (n *jsonNode) member(key string) *jsonMember {
	for i := range n.members {
		if n.members[i].key == key {
			return &n.members[i]
		}
	}
	return nil
}

type jsonParser struct {
	data           []byte
	pos            int
	invalidUnicode bool
}

func (p *jsonParser) ws() {
	for p.pos < len(p.data) && strings.ContainsRune(" \t\r\n", rune(p.data[p.pos])) {
		p.pos++
	}
}
func parseJSON(data []byte) (*jsonNode, error) {
	if len(data) > MaxBytes || !utf8.Valid(data) || !json.Valid(data) {
		return nil, fail("CONFIG_JSON_INVALID")
	}
	p := jsonParser{data: data}
	n, err := p.value(0)
	if err != nil {
		return nil, err
	}
	if p.invalidUnicode {
		return nil, fail("CONFIG_ENCODING_INVALID")
	}
	p.ws()
	if p.pos != len(data) {
		return nil, fail("CONFIG_JSON_INVALID")
	}
	return n, nil
}
func (p *jsonParser) stringEnd() int {
	p.pos++
	for p.pos < len(p.data) {
		b := p.data[p.pos]
		p.pos++
		if b == '\\' {
			if p.data[p.pos] != 'u' {
				p.pos++
				continue
			}
			p.pos++
			value, _ := strconv.ParseUint(string(p.data[p.pos:p.pos+4]), 16, 16)
			p.pos += 4
			if value >= 0xd800 && value <= 0xdbff {
				if p.pos+6 <= len(p.data) && string(p.data[p.pos:p.pos+2]) == `\u` {
					low, err := strconv.ParseUint(string(p.data[p.pos+2:p.pos+6]), 16, 16)
					if err == nil && low >= 0xdc00 && low <= 0xdfff {
						p.pos += 6
						continue
					}
				}
				p.invalidUnicode = true
			} else if value >= 0xdc00 && value <= 0xdfff {
				p.invalidUnicode = true
			}
		} else if b == '"' {
			break
		}
	}
	return p.pos
}
func (p *jsonParser) value(depth int) (*jsonNode, error) {
	if depth > 128 {
		return nil, fail("CONFIG_NESTING_LIMIT")
	}
	p.ws()
	n := &jsonNode{kind: p.data[p.pos], start: p.pos}
	switch n.kind {
	case '{':
		p.pos++
		p.ws()
		keys := map[string]bool{}
		for p.data[p.pos] != '}' {
			start := p.pos
			end := p.stringEnd()
			var key string
			if err := json.Unmarshal(p.data[start:end], &key); err != nil {
				return nil, fail("CONFIG_JSON_INVALID")
			}
			if keys[key] {
				return nil, fail("CONFIG_DUPLICATE_KEY")
			}
			keys[key] = true
			p.ws()
			p.pos++
			child, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			p.ws()
			m := jsonMember{key: key, start: start, comma: -1, value: child}
			if p.data[p.pos] == ',' {
				m.comma = p.pos
				p.pos++
				p.ws()
			}
			n.members = append(n.members, m)
		}
		p.pos++
	case '[':
		p.pos++
		p.ws()
		for p.data[p.pos] != ']' {
			if _, err := p.value(depth + 1); err != nil {
				return nil, err
			}
			p.ws()
			if p.data[p.pos] == ',' {
				p.pos++
				p.ws()
			}
		}
		p.pos++
	case '"':
		p.stringEnd()
	default:
		for p.pos < len(p.data) && !strings.ContainsRune(",]} \t\r\n", rune(p.data[p.pos])) {
			p.pos++
		}
	}
	n.end = p.pos
	return n, nil
}

func editJSON(d document, namespace, name string, value json.RawMessage) ([]byte, error) {
	if value != nil {
		var compact bytes.Buffer
		if err := json.Compact(&compact, value); err != nil {
			return nil, fail("CONFIG_DESIRED_INVALID")
		}
		value = compact.Bytes()
	}
	ns := d.json.member(namespace)
	if ns == nil {
		if value == nil {
			return d.data, nil
		}
		desired := map[string]json.RawMessage{name: value}
		encoded, err := json.Marshal(desired)
		if err != nil {
			return nil, fail("CONFIG_DESIRED_INVALID")
		}
		return insertJSON(d.data, d.json, namespace, encoded), nil
	}
	object := ns.value
	if existing := object.member(name); existing != nil {
		if value != nil {
			// Canonicalize only the managed value, preserving its key and spacing.
			var compact bytes.Buffer
			if err := json.Compact(&compact, value); err != nil {
				return nil, fail("CONFIG_DESIRED_INVALID")
			}
			return splice(d.data, existing.value.start, existing.value.end, compact.Bytes()), nil
		}
		start, end := existing.start, existing.value.end
		if existing.comma >= 0 {
			end = existing.comma + 1
		} else {
			for i := range object.members {
				if object.members[i].key == name && i > 0 {
					start = object.members[i-1].comma
					break
				}
			}
		}
		return splice(d.data, start, end, nil), nil
	}
	if value == nil {
		return d.data, nil
	}
	return insertJSON(d.data, object, name, value), nil
}
func insertJSON(data []byte, object *jsonNode, key string, value []byte) []byte {
	encoded, _ := json.Marshal(key)
	member := append(append(encoded, ':', ' '), value...)
	at := object.end - 1
	if len(object.members) > 0 {
		last := object.members[len(object.members)-1]
		at = last.value.end
		member = append([]byte(",\n"), member...)
	}
	return splice(data, at, at, member)
}

// DescribeError avoids rendering parser snippets or configuration values.
func DescribeError(err error) string {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return "CONFIG_READ_FAILED"
}
