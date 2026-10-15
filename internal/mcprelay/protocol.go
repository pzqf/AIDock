// Package mcprelay implements the stdio protocol boundary of a managed session.
// It neither starts programs nor grants installation, isolation or project trust.
package mcprelay

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MaxFrame = 1 << 20

type Error struct{ Code string }

func (e *Error) Error() string { return e.Code }
func fail(code string) error   { return &Error{Code: code} }
func digest(b []byte) string   { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// decode rejects duplicate keys at every depth rather than letting a parser pick
// a different project, tool name or request identity from the one we authorize.
func decode(b []byte, out any) error {
	if len(b) > MaxFrame || !utf8.Valid(b) || !json.Valid(b) {
		return fail("MCP_FRAME_INVALID")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := unique(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fail("MCP_FRAME_INVALID")
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fail("MCP_FRAME_INVALID")
	}
	return nil
}

func unique(d *json.Decoder, depth int) error {
	if depth > 64 {
		return fail("MCP_FRAME_INVALID")
	}
	t, err := d.Token()
	if err != nil {
		return fail("MCP_FRAME_INVALID")
	}
	switch t {
	case json.Delim('{'):
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return fail("MCP_FRAME_INVALID")
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fail("MCP_FRAME_INVALID")
			}
			seen[name] = true
			if err := unique(d, depth+1); err != nil {
				return err
			}
		}
		if _, err := d.Token(); err != nil {
			return fail("MCP_FRAME_INVALID")
		}
	case json.Delim('['):
		for d.More() {
			if err := unique(d, depth+1); err != nil {
				return err
			}
		}
		if _, err := d.Token(); err != nil {
			return fail("MCP_FRAME_INVALID")
		}
	}
	return nil
}

func bytesOf(v any) []byte { b, _ := json.Marshal(v); return b }

// DescriptorDigest binds a trusted tool declaration including its schema and
// annotations. Server-supplied readOnlyHint never grants read authorization.
func DescriptorDigest(raw json.RawMessage) (string, error) {
	var object map[string]json.RawMessage
	if err := decode(raw, &object); err != nil || object == nil {
		return "", fail("MCP_TOOL_INVALID")
	}
	var canonical any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&canonical); err != nil {
		return "", fail("MCP_TOOL_INVALID")
	}
	return digest(bytesOf(canonical)), nil
}

type frame struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
	Result json.RawMessage
	Error  json.RawMessage
}

func parse(b []byte) (frame, error) {
	var v map[string]json.RawMessage
	if err := decode(b, &v); err != nil || v == nil {
		return frame{}, fail("MCP_FRAME_INVALID")
	}
	var version string
	if json.Unmarshal(v["jsonrpc"], &version) != nil || version != "2.0" {
		return frame{}, fail("MCP_FRAME_INVALID")
	}
	f := frame{ID: v["id"], Params: v["params"], Result: v["result"], Error: v["error"]}
	if raw, ok := v["method"]; ok {
		if json.Unmarshal(raw, &f.Method) != nil || f.Method == "" || len(f.Method) > 128 || f.Result != nil || f.Error != nil {
			return frame{}, fail("MCP_FRAME_INVALID")
		}
	} else if f.ID == nil || (f.Result == nil) == (f.Error == nil) || f.Params != nil {
		return frame{}, fail("MCP_FRAME_INVALID")
	}
	for key := range v {
		if key != "jsonrpc" && key != "id" && key != "method" && key != "params" && key != "result" && key != "error" {
			return frame{}, fail("MCP_FRAME_INVALID")
		}
	}
	if f.ID != nil {
		if _, err := idKey(f.ID); err != nil {
			return frame{}, err
		}
	}
	return f, nil
}

func idKey(id json.RawMessage) (string, error) {
	var s string
	if len(id) > 0 && id[0] == '"' {
		if json.Unmarshal(id, &s) != nil || len(s) > 256 {
			return "", fail("MCP_ID_INVALID")
		}
		return "s:" + s, nil
	}
	if len(id) == 0 || strings.ContainsAny(string(id), ".eE+") {
		return "", fail("MCP_ID_INVALID")
	}
	n, err := strconv.ParseInt(string(id), 10, 64)
	if err != nil || n < -9007199254740991 || n > 9007199254740991 {
		return "", fail("MCP_ID_INVALID")
	}
	return "n:" + strconv.FormatInt(n, 10), nil
}

func supported(version string) bool {
	return version == "2025-11-25" || version == "2025-06-18" || version == "2025-03-26" || version == "2024-11-05"
}
