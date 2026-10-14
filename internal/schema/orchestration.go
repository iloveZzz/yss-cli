package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
)

const OrchestrationRef = ".template-spec/process/orchestration-contract.yaml"

// LoadOrchestration binds the manifest and ordered parts, including their names.
func LoadOrchestration(read func(string) ([]byte, error), ref string) (map[string]any, []string, string, error) {
	parse := func(raw []byte, name string) (map[string]any, error) {
		value, err := Parse(raw)
		if err != nil {
			return nil, err
		}
		m, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: 编排合同必须是对象", name)
		}
		return m, nil
	}
	raw, err := read(ref)
	if err != nil {
		return nil, nil, "", err
	}
	value, err := parse(raw, ref)
	if err != nil {
		return nil, nil, "", err
	}
	refs := []string{ref}
	hash := sha256.New()
	parts, multipart := value["parts"]
	if !multipart {
		hash.Write(raw)
		return value, refs, hex.EncodeToString(hash.Sum(nil)), nil
	}
	list, ok := parts.([]any)
	if !ok || len(list) == 0 {
		return nil, nil, "", fmt.Errorf("%s: parts 必须是非空数组", ref)
	}
	delete(value, "parts")
	frame := func(name string, bytes []byte) {
		fmt.Fprintf(hash, "%s\x00%d\x00", name, len(bytes))
		hash.Write(bytes)
		hash.Write([]byte{0})
	}
	frame(ref, raw)
	seen := map[string]bool{}
	for _, item := range list {
		part, ok := item.(string)
		if !ok || part == "" || strings.HasPrefix(part, "/") || strings.ContainsAny(part, "\\:#?\x00") {
			return nil, nil, "", fmt.Errorf("%s: 非法部件路径", ref)
		}
		for _, c := range part {
			if c < 32 {
				return nil, nil, "", fmt.Errorf("%s: 非法部件路径", ref)
			}
		}
		for _, segment := range strings.Split(part, "/") {
			if segment == "" || segment == "." || segment == ".." {
				return nil, nil, "", fmt.Errorf("%s: 非法部件路径", ref)
			}
		}
		name := path.Join(path.Dir(ref), part)
		if seen[name] {
			return nil, nil, "", fmt.Errorf("%s: 合同部件重复", name)
		}
		seen[name] = true
		bytes, err := read(name)
		if err != nil {
			return nil, nil, "", err
		}
		body, err := parse(bytes, name)
		if err != nil {
			return nil, nil, "", err
		}
		frame(name, bytes)
		refs = append(refs, name)
		for key, item := range body {
			if key == "parts" {
				return nil, nil, "", fmt.Errorf("%s: 部件不得声明 parts", name)
			}
			prior, exists := value[key]
			if !exists {
				value[key] = item
				continue
			}
			left, leftOK := prior.(map[string]any)
			right, rightOK := item.(map[string]any)
			if key != "work_unit_routes" || !leftOK || !rightOK {
				return nil, nil, "", fmt.Errorf("%s: 顶层策略 %s 重复", name, key)
			}
			for child, body := range right {
				if _, exists := left[child]; exists {
					return nil, nil, "", fmt.Errorf("%s: 工作单元 %s 重复", name, child)
				}
				left[child] = body
			}
		}
	}
	return value, refs, hex.EncodeToString(hash.Sum(nil)), nil
}
