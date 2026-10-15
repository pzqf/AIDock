// Package rules compiles ordered source text into a single owned Markdown
// block. It never executes content or changes the bytes outside that block.
package rules

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"

	"github.com/pzqf/AIDock/internal/result"
)

const Begin = "<!-- aidock:begin -->"
const End = "<!-- aidock:end -->"

type Result struct {
	Prefix, Managed, Suffix string
	Output                  []byte
	Digest                  string
}

type Block struct {
	Prefix, Managed, Suffix, Body, Digest string
}

// Inspect reads a single unambiguous block without adopting its ownership.
// Native imports and explicit drift decisions use the same parser as Compile.
func Inspect(before []byte) (Block, error) {
	if !utf8.Valid(before) || bytes.IndexByte(before, 0) >= 0 {
		return Block{}, fail("RULES_ENCODING_UNSUPPORTED", "规则目标须为无 NUL 的 UTF-8 文本，原字节保留")
	}
	start, end := bytes.Index(before, []byte(Begin)), bytes.Index(before, []byte(End))
	count := bytes.Count(before, []byte("<!-- aidock:"))
	if start < 0 && end < 0 && count == 0 {
		return Block{}, fail("RULES_BLOCK_MISSING", "文件没有可导入的托管区块")
	}
	if count != 2 || bytes.Count(before, []byte(Begin)) != 1 || bytes.Count(before, []byte(End)) != 1 || end <= start || !lineMarker(before, start, len(Begin)) || !lineMarker(before, end, len(End)) {
		return Block{}, fail("RULES_MARKERS_INVALID", "托管标记重复、嵌套、缺失或未独占一行；目标保留")
	}
	managed := string(before[start : end+len(End)])
	body := string(before[start+len(Begin) : end])
	if strings.HasPrefix(body, "\r\n") {
		body = body[2:]
	} else {
		body = strings.TrimPrefix(body, "\n")
	}
	if strings.HasSuffix(body, "\r\n") {
		body = body[:len(body)-2]
	} else {
		body = strings.TrimSuffix(body, "\n")
	}
	return Block{Prefix: string(before[:start]), Managed: managed, Suffix: string(before[end+len(End):]), Body: body, Digest: digest([]byte(managed))}, nil
}

func digest(v []byte) string          { h := sha256.Sum256(v); return hex.EncodeToString(h[:]) }
func fail(code, message string) error { return result.Fail(code, message, 5) }
func lineMarker(data []byte, at, size int) bool {
	left := at == 0 || data[at-1] == '\n' || at == 3 && bytes.Equal(data[:3], []byte{239, 187, 191})
	after := data[at+size:]
	return left && (len(after) == 0 || after[0] == '\n' || bytes.HasPrefix(after, []byte("\r\n")))
}

// Compile requires the previously recorded managed digest when a block exists.
// A matching marker alone does not authorize takeover of an unowned block.
func Compile(before []byte, rules []string, owned string) (Result, error) {
	r := Result{}
	if !utf8.Valid(before) || bytes.IndexByte(before, 0) >= 0 {
		return r, fail("RULES_ENCODING_UNSUPPORTED", "规则目标须为无 NUL 的 UTF-8 文本，原字节保留")
	}
	for _, rule := range rules {
		if !utf8.ValidString(rule) || strings.ContainsRune(rule, 0) || strings.Contains(rule, "aidock:begin") || strings.Contains(rule, "aidock:end") {
			return r, fail("RULES_SOURCE_INVALID", "共享规则包含非法文本或保留托管标记")
		}
	}
	start, end := bytes.Index(before, []byte(Begin)), bytes.Index(before, []byte(End))
	count := bytes.Count(before, []byte("<!-- aidock:"))
	if start >= 0 || end >= 0 || count > 0 {
		block, err := Inspect(before)
		if err != nil {
			return r, err
		}
		if owned == "" {
			return r, fail("RULES_OWNERSHIP_REQUIRED", "已有托管标记未登记归属，需显式导入协调；不会自动接管")
		}
		if block.Digest != owned {
			return r, fail("RULES_MANAGED_DRIFT", "托管区块已被手工修改；保留文件，请协调规范源与手改后重新预览")
		}
		r.Prefix, r.Suffix = block.Prefix, block.Suffix
	} else {
		if owned != "" {
			return r, fail("RULES_MANAGED_DRIFT", "已登记的托管区块被删除或目标丢失；不会静默重建")
		}
		r.Prefix = string(before)
		if len(before) > 0 && before[len(before)-1] != '\n' {
			r.Prefix += "\n"
		}
		r.Suffix = "\n"
	}
	r.Managed = Begin + "\n" + strings.Join(rules, "\n\n") + "\n" + End
	r.Digest = digest([]byte(r.Managed))
	r.Output = []byte(r.Prefix + r.Managed + r.Suffix)
	return r, nil
}
