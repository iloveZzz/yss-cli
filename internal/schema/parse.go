// Package schema provides strict, JSON-compatible asset parsing and offline
// JSON Schema validation. It never invokes another language runtime.
package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Error identifies an input or schema resource failure separately from an
// instance validation issue. Paths are JSON Pointers where available.
type Error struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s: %s", e.Code, e.Path, e.Message) }

func fail(code, path, message string) error { return &Error{Code: code, Path: path, Message: message} }

// Parse returns map[string]any, []any, json.Number, string, bool, or nil.
// JSON numbers retain their source precision. JSON object keys must be unique.
func Parse(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, fail("ASSET_UTF8", "", "input is not valid UTF-8")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fail("ASSET_EMPTY", "", "input is empty")
	}
	if !json.Valid(data) {
		return parseYAML(data)
	}
	return parseJSON(data)
}

func parseJSON(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, fail("ASSET_UTF8", "", "input is not valid UTF-8")
	}
	if err := checkJSONUnicode(data); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseJSONValue(dec, "")
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fail("ASSET_PARSE", "", "unexpected content after document")
	}
	return v, nil
}

// encoding/json replaces lone UTF-16 surrogates with U+FFFD. Reject them
// explicitly so distinct source strings cannot silently become the same asset.
func checkJSONUnicode(data []byte) error {
	inString := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[i] != '\\' || i+1 >= len(data) {
			continue
		}
		if data[i+1] != 'u' {
			i++
			continue
		}
		if i+6 > len(data) {
			continue
		}
		value, err := strconv.ParseUint(string(data[i+2:i+6]), 16, 16)
		if err != nil {
			continue
		}
		if value >= 0xd800 && value <= 0xdbff {
			if i+12 > len(data) || data[i+6] != '\\' || data[i+7] != 'u' {
				return fail("ASSET_UNICODE", "", fmt.Sprintf("unpaired UTF-16 high surrogate at byte %d", i))
			}
			low, err := strconv.ParseUint(string(data[i+8:i+12]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return fail("ASSET_UNICODE", "", fmt.Sprintf("unpaired UTF-16 high surrogate at byte %d", i))
			}
			i += 11
			continue
		}
		if value >= 0xdc00 && value <= 0xdfff {
			return fail("ASSET_UNICODE", "", fmt.Sprintf("unpaired UTF-16 low surrogate at byte %d", i))
		}
		i += 5
	}
	return nil
}

func parseJSONValue(dec *json.Decoder, path string) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, fail("ASSET_PARSE", path, err.Error())
	}
	switch delim := tok.(type) {
	case json.Delim:
		switch delim {
		case '{':
			obj := map[string]any{}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, fail("ASSET_PARSE", path, err.Error())
				}
				key, ok := keyTok.(string)
				if !ok {
					return nil, fail("ASSET_KEY", path, "object key must be a string")
				}
				child := path + "/" + pointerEscape(key)
				if _, exists := obj[key]; exists {
					return nil, fail("ASSET_DUPLICATE_KEY", child, "duplicate object key")
				}
				value, err := parseJSONValue(dec, child)
				if err != nil {
					return nil, err
				}
				obj[key] = value
			}
			if _, err := dec.Token(); err != nil {
				return nil, fail("ASSET_PARSE", path, err.Error())
			}
			return obj, nil
		case '[':
			arr := []any{}
			for dec.More() {
				value, err := parseJSONValue(dec, fmt.Sprintf("%s/%d", path, len(arr)))
				if err != nil {
					return nil, err
				}
				arr = append(arr, value)
			}
			if _, err := dec.Token(); err != nil {
				return nil, fail("ASSET_PARSE", path, err.Error())
			}
			return arr, nil
		default:
			return nil, fail("ASSET_PARSE", path, "unexpected closing delimiter")
		}
	default:
		return tok, nil
	}
}

func pointerEscape(value string) string {
	var out bytes.Buffer
	for _, r := range value {
		switch r {
		case '~':
			out.WriteString("~0")
		case '/':
			out.WriteString("~1")
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

// LoadFile reads one asset without modifying it.
func LoadFile(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ASSET_READ: %s: %w", path, err)
	}
	var v any
	if strings.EqualFold(filepath.Ext(path), ".json") {
		if !utf8.Valid(data) {
			return nil, fail("ASSET_UTF8", path, "input is not valid UTF-8")
		}
		v, err = parseJSON(data)
	} else {
		v, err = Parse(data)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return v, nil
}
