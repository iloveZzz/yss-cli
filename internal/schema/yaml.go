package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

func parseYAML(data []byte) (any, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return nil, fail("ASSET_PARSE", "", err.Error())
	}
	if len(doc.Content) != 1 {
		return nil, fail("ASSET_EMPTY", "", "input has no document")
	}
	if doc.Content[0].Kind == yaml.ScalarNode && doc.Content[0].ShortTag() == "!!null" && doc.Content[0].Value == "" {
		return nil, fail("ASSET_EMPTY", "", "input has no document value")
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		if err != nil {
			return nil, fail("ASSET_PARSE", "", err.Error())
		}
		return nil, fail("ASSET_MULTIPLE_DOCUMENTS", "", "only one YAML document is allowed")
	}
	return yamlValue(doc.Content[0], "", 0)
}

func yamlValue(node *yaml.Node, path string, depth int) (any, error) {
	if depth > 512 {
		return nil, fail("ASSET_DEPTH", path, "YAML nesting exceeds 512")
	}
	if node.Kind == yaml.AliasNode {
		return nil, fail("ASSET_ALIAS", path, "YAML aliases are prohibited")
	}
	if node.Anchor != "" {
		return nil, fail("ASSET_ALIAS", path, "YAML anchors are prohibited")
	}
	if node.Style&yaml.TaggedStyle != 0 {
		return nil, fail("ASSET_TAG", path, "explicit YAML tags are prohibited")
	}
	switch node.Kind {
	case yaml.MappingNode:
		if node.ShortTag() != "!!map" {
			return nil, fail("ASSET_TAG", path, "custom mapping tags are prohibited")
		}
		obj := map[string]any{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.ShortTag() == "!!merge" {
				return nil, fail("ASSET_KEY", path, "mapping keys must be strings; merge keys are prohibited")
			}
			keyValue, err := yamlValue(key, path, depth+1)
			if err != nil {
				return nil, err
			}
			if _, ok := keyValue.(string); !ok {
				return nil, fail("ASSET_KEY", path, "mapping keys must be strings")
			}
			child := path + "/" + pointerEscape(key.Value)
			if _, exists := obj[key.Value]; exists {
				return nil, fail("ASSET_DUPLICATE_KEY", child, fmt.Sprintf("duplicate mapping key at line %d", key.Line))
			}
			v, err := yamlValue(node.Content[i+1], child, depth+1)
			if err != nil {
				return nil, err
			}
			obj[key.Value] = v
		}
		return obj, nil
	case yaml.SequenceNode:
		if node.ShortTag() != "!!seq" {
			return nil, fail("ASSET_TAG", path, "custom sequence tags are prohibited")
		}
		arr := make([]any, 0, len(node.Content))
		for i, child := range node.Content {
			v, err := yamlValue(child, fmt.Sprintf("%s/%d", path, i), depth+1)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		return arr, nil
	case yaml.ScalarNode:
		return yamlCoreScalar(node, path)
	default:
		return nil, fail("ASSET_PARSE", path, "unsupported YAML node")
	}
}

// yaml.v3 resolves some YAML 1.1 numbers (octal leading zeros, binary and
// underscores). Resolve plain scalars ourselves using the existing YAML 1.2
// Core contract; quoted/block scalars remain strings regardless of spelling.
var (
	coreDecimal = regexp.MustCompile(`^[+-]?[0-9]+$`)
	coreOctal   = regexp.MustCompile(`^0o[0-7]+$`)
	coreHex     = regexp.MustCompile(`^0x[0-9a-fA-F]+$`)
	coreFloat   = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
)

func yamlCoreScalar(node *yaml.Node, path string) (any, error) {
	if node.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return node.Value, nil
	}
	text := node.Value
	switch text {
	case "", "~", "null", "Null", "NULL":
		return nil, nil
	case "true", "True", "TRUE":
		return true, nil
	case "false", "False", "FALSE":
		return false, nil
	case ".nan", ".NaN", ".NAN", ".inf", ".Inf", ".INF", "+.inf", "+.Inf", "+.INF", "-.inf", "-.Inf", "-.INF":
		return nil, fail("ASSET_NUMBER", path, "non-finite YAML number")
	}
	base, digits := 10, text
	switch {
	case coreOctal.MatchString(text):
		base, digits = 8, text[2:]
	case coreHex.MatchString(text):
		base, digits = 16, text[2:]
	case coreDecimal.MatchString(text):
	default:
		if !coreFloat.MatchString(text) {
			return text, nil
		}
		if _, err := strconv.ParseFloat(text, 64); err != nil {
			return nil, fail("ASSET_NUMBER", path, "non-finite or invalid YAML number")
		}
		// Normalize JSON spelling without rounding decimal source precision.
		text = strings.TrimPrefix(text, "+")
		sign := ""
		if strings.HasPrefix(text, "-") {
			sign, text = "-", text[1:]
		}
		mantissa, exponent := text, ""
		if index := strings.IndexAny(text, "eE"); index >= 0 {
			mantissa, exponent = text[:index], text[index:]
		}
		parts := strings.SplitN(mantissa, ".", 2)
		integer := strings.TrimLeft(parts[0], "0")
		if integer == "" {
			integer = "0"
		}
		text = sign + integer
		if len(parts) == 2 {
			fraction := parts[1]
			if fraction == "" {
				fraction = "0"
			}
			text += "." + fraction
		}
		text += exponent
		if !json.Valid([]byte(text)) {
			return nil, fail("ASSET_NUMBER", path, "non-JSON YAML number")
		}
		return json.Number(text), nil
	}
	n, ok := new(big.Int).SetString(digits, base)
	if !ok {
		return nil, fail("ASSET_NUMBER", path, "invalid YAML integer")
	}
	return json.Number(n.String()), nil
}
