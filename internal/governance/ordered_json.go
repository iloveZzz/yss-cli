package governance

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/schema"
	"go.yaml.in/yaml/v3"
)

// This contract preserves source insertion order instead of canonical key order.
func orderedAssetJSON(raw []byte, fields ...string) ([]byte, error) {
	if _, err := schema.Parse(raw); err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	node := &doc
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		node = node.Content[0]
	}
	for _, field := range fields {
		if node.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("JSON 路径不是对象")
		}
		var found *yaml.Node
		for i := 0; i < len(node.Content); i += 2 {
			if node.Content[i].Value == field {
				found = node.Content[i+1]
			}
		}
		if found == nil {
			return nil, fmt.Errorf("JSON 路径缺少 %s", field)
		}
		node = found
	}
	var emit func(*yaml.Node) ([]byte, error)
	emit = func(n *yaml.Node) ([]byte, error) {
		switch n.Kind {
		case yaml.SequenceNode:
			parts := []string{}
			for _, child := range n.Content {
				b, e := emit(child)
				if e != nil {
					return nil, e
				}
				parts = append(parts, string(b))
			}
			return []byte("[" + strings.Join(parts, ",") + "]"), nil
		case yaml.MappingNode:
			indices := []int{}
			for i := 0; i < len(n.Content); i += 2 {
				indices = append(indices, i)
			}
			index := func(k string) (uint64, bool) {
				v, e := strconv.ParseUint(k, 10, 32)
				return v, e == nil && v < 4294967295 && strconv.FormatUint(v, 10) == k
			}
			sort.SliceStable(indices, func(i, j int) bool {
				a, aa := index(n.Content[indices[i]].Value)
				b, bb := index(n.Content[indices[j]].Value)
				if aa != bb {
					return aa
				}
				if aa {
					return a < b
				}
				return false
			})
			var out bytes.Buffer
			out.WriteByte('{')
			for i, position := range indices {
				if i > 0 {
					out.WriteByte(',')
				}
				out.Write(apCanonical(n.Content[position].Value))
				out.WriteByte(':')
				b, e := emit(n.Content[position+1])
				if e != nil {
					return nil, e
				}
				out.Write(b)
			}
			out.WriteByte('}')
			return out.Bytes(), nil
		case yaml.ScalarNode:
			var source bytes.Buffer
			encoder := yaml.NewEncoder(&source)
			if err := encoder.Encode(n); err != nil {
				return nil, err
			}
			_ = encoder.Close()
			value, err := schema.Parse(source.Bytes())
			if err != nil {
				return nil, err
			}
			return apCanonical(value), nil
		default:
			return nil, fmt.Errorf("JSON 摘要不支持 alias 或多文档")
		}
	}
	return emit(node)
}
