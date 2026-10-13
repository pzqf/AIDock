package source

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/pzqf/AIDock/internal/result"
	"gopkg.in/yaml.v3"
)

var namePrefix = regexp.MustCompile(`^name[ \t]*:[ \t]*$`)

// Change only the name scalar. Keep BOM, line endings, comments, unknown native
// frontmatter fields and body bytes intact. Unusual YAML layouts fail closed.
func renameDocument(data []byte, oldName, newName string) ([]byte, error) {
	old, e := documentName(data)
	if e != nil {
		return nil, e
	}
	if old != oldName {
		return nil, result.Fail("SOURCE_PACKAGE_DRIFT", "正文原生名与索引不一致", 3)
	}
	lines := bytes.Split(data, []byte{'\n'})
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSuffix(string(lines[i]), "\r") == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, result.Fail("PACKAGE_INVALID", "frontmatter 未闭合", 5)
	}
	var doc yaml.Node
	if e = yaml.Unmarshal(bytes.Join(lines[1:end], []byte{'\n'}), &doc); e != nil {
		return nil, e
	}
	fail := func() ([]byte, error) {
		return nil, result.Fail("RENAME_FORMAT_UNSUPPORTED", "原生 name 必须为独立行标量；保留原文，请先用编辑调整格式", 4)
	}
	if len(doc.Content) != 1 || doc.Content[0].Style&yaml.FlowStyle != 0 {
		return fail()
	}
	mapping := doc.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i], mapping.Content[i+1]
		if key.Value != "name" {
			continue
		}
		if key.Column != 1 || key.Line != value.Line || value.Kind != yaml.ScalarNode || value.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
			return fail()
		}
		lineIndex := value.Line
		line := lines[lineIndex]
		start := value.Column - 1
		if start >= len(line) || !namePrefix.Match(line[:start]) {
			return fail()
		}
		tail := line[start:]
		finish := 0
		replacement := newName
		if tail[0] == '\'' || tail[0] == '"' {
			quote := tail[0]
			finish = 1
			closed := false
			for finish < len(tail) {
				c := tail[finish]
				finish++
				if quote == '"' && c == '\\' {
					finish++
					continue
				}
				if c == quote {
					if quote == '\'' && finish < len(tail) && tail[finish] == quote {
						finish++
						continue
					}
					closed = true
					break
				}
			}
			if !closed {
				return fail()
			}
			if quote == '\'' {
				replacement = "'" + newName + "'"
			} else {
				b, _ := json.Marshal(newName)
				replacement = string(b)
			}
		} else {
			finish = len(tail)
			if comment := bytes.IndexByte(tail, '#'); comment >= 0 {
				finish = comment
			}
			finish = len(bytes.TrimRight(tail[:finish], " \t\r"))
		}
		if finish > len(tail) {
			return fail()
		}
		changed := append([]byte{}, line[:start]...)
		changed = append(changed, []byte(replacement)...)
		changed = append(changed, tail[finish:]...)
		lines[lineIndex] = changed
		resultBytes := bytes.Join(lines, []byte{'\n'})
		name, e := documentName(resultBytes)
		if e != nil || name != newName {
			return fail()
		}
		return resultBytes, nil
	}
	return fail()
}
