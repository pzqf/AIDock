package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const MaxDocumentBytes = 10 << 20

func ReadFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxDocumentBytes {
		return nil, fmt.Errorf("配置不是普通文件或超过 10 MB")
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxDocumentBytes {
		return nil, fmt.Errorf("配置超过 10 MB")
	}
	return b, nil
}

// Parse rejects ambiguous YAML before either native-frontmatter or owned Schema
// validation. It never constructs application-defined YAML objects.
func Parse(data []byte) (any, error) {
	if len(data) > MaxDocumentBytes || !utf8.Valid(data) {
		return nil, fmt.Errorf("配置超过 10 MB 或不是有效 UTF-8")
	}
	dec := yaml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("只接受一个 YAML 文档")
	}
	count := 0
	return convert(&doc, 0, &count)
}

func convert(n *yaml.Node, depth int, count *int) (any, error) {
	*count++
	if depth > 64 || *count > 100000 {
		return nil, fmt.Errorf("YAML 嵌套或节点数量超限")
	}
	if n.Anchor != "" || n.Kind == yaml.AliasNode {
		return nil, fmt.Errorf("不接受 YAML anchor/alias")
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) != 1 {
			return nil, fmt.Errorf("空文档")
		}
		return convert(n.Content[0], depth+1, count)
	case yaml.MappingNode:
		if n.Tag != "!!map" {
			return nil, fmt.Errorf("非法 mapping 标签")
		}
		m := map[string]any{}
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i]
			if key.Tag != "!!str" || key.Kind != yaml.ScalarNode || key.Anchor != "" {
				return nil, fmt.Errorf("配置键必须为字符串")
			}
			if _, found := m[key.Value]; found {
				return nil, fmt.Errorf("重复键 %q（第 %d 行）", key.Value, key.Line)
			}
			v, err := convert(n.Content[i+1], depth+1, count)
			if err != nil {
				return nil, err
			}
			m[key.Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		if n.Tag != "!!seq" {
			return nil, fmt.Errorf("非法 sequence 标签")
		}
		a := make([]any, 0, len(n.Content))
		for _, child := range n.Content {
			v, err := convert(child, depth+1, count)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		return a, nil
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!str", "!!timestamp":
			return n.Value, nil
		case "!!int", "!!bool", "!!float", "!!null":
			var v any
			if err := n.Decode(&v); err != nil {
				return nil, err
			}
			return v, nil
		default:
			return nil, fmt.Errorf("非法 YAML 标签 %q", n.Tag)
		}
	}
	return nil, fmt.Errorf("不支持 YAML 节点")
}
