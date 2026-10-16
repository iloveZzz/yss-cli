package scaffold

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// OMap is a JSON object that keeps JavaScript property order: integer-like keys
// first in ascending order, then the remaining keys in insertion order. Setting
// an existing key replaces its value in place, exactly like a JS object.
type OMap struct {
	keys []string
	vals map[string]any
}

// Num is a JSON number kept as its source literal until it is printed.
type Num string

func NewOMap() *OMap { return &OMap{vals: map[string]any{}} }

func (m *OMap) Set(key string, value any) *OMap {
	if _, ok := m.vals[key]; !ok {
		m.keys = append(m.keys, key)
	}
	m.vals[key] = value
	return m
}

func (m *OMap) Get(key string) (any, bool) {
	if m == nil {
		return nil, false
	}
	value, ok := m.vals[key]
	return value, ok
}

func (m *OMap) Has(key string) bool {
	_, ok := m.Get(key)
	return ok
}

func (m *OMap) Delete(key string) {
	if _, ok := m.vals[key]; !ok {
		return
	}
	delete(m.vals, key)
	for i, existing := range m.keys {
		if existing == key {
			m.keys = append(m.keys[:i], m.keys[i+1:]...)
			return
		}
	}
}

func (m *OMap) Len() int {
	if m == nil {
		return 0
	}
	return len(m.keys)
}

// Keys returns the keys in JS enumeration order.
func (m *OMap) Keys() []string {
	if m == nil {
		return nil
	}
	var indexes, others []string
	for _, key := range m.keys {
		if arrayIndex(key) {
			indexes = append(indexes, key)
		} else {
			others = append(others, key)
		}
	}
	if len(indexes) > 0 {
		sort.SliceStable(indexes, func(i, j int) bool {
			a, _ := strconv.ParseUint(indexes[i], 10, 64)
			b, _ := strconv.ParseUint(indexes[j], 10, 64)
			return a < b
		})
	}
	return append(indexes, others...)
}

// Clone copies the object and every nested object and array.
func (m *OMap) Clone() *OMap {
	if m == nil {
		return nil
	}
	out := NewOMap()
	for _, key := range m.keys {
		out.Set(key, cloneValue(m.vals[key]))
	}
	return out
}

func cloneValue(value any) any {
	switch v := value.(type) {
	case *OMap:
		return v.Clone()
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = cloneValue(item)
		}
		return out
	default:
		return value
	}
}

func arrayIndex(key string) bool {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return false
		}
	}
	n, err := strconv.ParseUint(key, 10, 64)
	return err == nil && n < math.MaxUint32
}

// ParseJSON decodes one JSON document with JSON.parse semantics: key order is
// preserved, a repeated key keeps its first position and last value.
func ParseJSON(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("JSON 末尾存在多余内容")
	}
	return value, nil
}

func decodeValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := NewOMap()
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("JSON 对象键无效")
				}
				value, err := decodeValue(decoder)
				if err != nil {
					return nil, err
				}
				m.Set(key, value)
			}
			if _, err = decoder.Token(); err != nil {
				return nil, err
			}
			return m, nil
		case '[':
			list := []any{}
			for decoder.More() {
				value, err := decodeValue(decoder)
				if err != nil {
					return nil, err
				}
				list = append(list, value)
			}
			if _, err = decoder.Token(); err != nil {
				return nil, err
			}
			return list, nil
		}
		return nil, fmt.Errorf("JSON 结构无效")
	case json.Number:
		return Num(t.String()), nil
	default:
		return token, nil
	}
}

// JSON renders a value like JSON.stringify(value) (indent == "") or
// JSON.stringify(value, null, indent).
func JSON(value any, indent string) string {
	var out strings.Builder
	writeJSON(&out, value, indent, "")
	return out.String()
}

func writeJSON(out *strings.Builder, value any, indent, current string) {
	switch v := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		if v {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case string:
		writeJSONString(out, v)
	case Num:
		f, err := strconv.ParseFloat(string(v), 64)
		if err != nil {
			out.WriteString("null")
			return
		}
		out.WriteString(jsNumber(f))
	case int:
		out.WriteString(strconv.Itoa(v))
	case int64:
		out.WriteString(strconv.FormatInt(v, 10))
	case float64:
		out.WriteString(jsNumber(v))
	case []string:
		list := make([]any, len(v))
		for i, item := range v {
			list[i] = item
		}
		writeJSON(out, list, indent, current)
	case []any:
		if len(v) == 0 {
			out.WriteString("[]")
			return
		}
		out.WriteByte('[')
		inner := current + indent
		for i, item := range v {
			if i > 0 {
				out.WriteByte(',')
			}
			if indent != "" {
				out.WriteByte('\n')
				out.WriteString(inner)
			}
			if item == nil {
				out.WriteString("null")
				continue
			}
			writeJSON(out, item, indent, inner)
		}
		if indent != "" {
			out.WriteByte('\n')
			out.WriteString(current)
		}
		out.WriteByte(']')
	case *OMap:
		keys := v.Keys()
		if len(keys) == 0 {
			out.WriteString("{}")
			return
		}
		out.WriteByte('{')
		inner := current + indent
		for i, key := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			if indent != "" {
				out.WriteByte('\n')
				out.WriteString(inner)
			}
			writeJSONString(out, key)
			out.WriteByte(':')
			if indent != "" {
				out.WriteByte(' ')
			}
			writeJSON(out, v.vals[key], indent, inner)
		}
		if indent != "" {
			out.WriteByte('\n')
			out.WriteString(current)
		}
		out.WriteByte('}')
	default:
		panic(fmt.Sprintf("scaffold: unsupported JSON value %T", value))
	}
}

func writeJSONString(out *strings.Builder, s string) {
	out.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			out.WriteString(`�`)
			i++
			continue
		}
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(out, `\u%04x`, r)
			} else {
				out.WriteString(s[i : i+size])
			}
		}
		i += size
	}
	out.WriteByte('"')
}

// jsNumber is Number.prototype.toString for finite doubles.
func jsNumber(f float64) string {
	if f == 0 {
		return "0"
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "null"
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	text := strconv.FormatFloat(f, 'e', -1, 64)
	mantissa, exponent, _ := strings.Cut(text, "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	e, _ := strconv.Atoi(exponent)
	k, n := len(digits), e+1
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	exp := n - 1
	expSign := "+"
	if exp < 0 {
		expSign, exp = "-", -exp
	}
	if k == 1 {
		return sign + digits + "e" + expSign + strconv.Itoa(exp)
	}
	return sign + digits[:1] + "." + digits[1:] + "e" + expSign + strconv.Itoa(exp)
}

// stable mirrors the platform digest helper: object keys sorted by UTF-16 order.
func stable(value any) any {
	switch v := value.(type) {
	case *OMap:
		keys := append([]string(nil), v.keys...)
		sort.Slice(keys, func(i, j int) bool { return utf16Less(keys[i], keys[j]) })
		out := NewOMap()
		for _, key := range keys {
			out.Set(key, stable(v.vals[key]))
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = stable(item)
		}
		return out
	default:
		return value
	}
}

// utf16Less is JavaScript's default string ordering (UTF-16 code units).
func utf16Less(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	for i := 0; i < len(ra) && i < len(rb); i++ {
		if ra[i] == rb[i] {
			continue
		}
		return utf16First(ra[i]) < utf16First(rb[i]) || utf16First(ra[i]) == utf16First(rb[i]) && ra[i] < rb[i]
	}
	return len(ra) < len(rb)
}

func utf16First(r rune) rune {
	if r >= 0x10000 {
		return 0xD800 + ((r - 0x10000) >> 10)
	}
	return r
}

// Value helpers for decoded JSON.

func asMap(value any) *OMap {
	m, _ := value.(*OMap)
	return m
}

func asList(value any) []any {
	list, _ := value.([]any)
	return list
}

func asString(value any) string {
	s, _ := value.(string)
	return s
}

func stringList(value any) []string {
	var out []string
	for _, item := range asList(value) {
		out = append(out, fmt.Sprint(item))
	}
	return out
}

func newMap(pairs ...any) *OMap {
	m := NewOMap()
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

func stringsToAny(list []string) []any {
	out := make([]any, len(list))
	for i, item := range list {
		out[i] = item
	}
	return out
}

// Plain converts decoded values into the standard library's map/slice types so
// they can be marshaled by encoding/json.
func Plain(value any) any {
	switch v := value.(type) {
	case *OMap:
		if v == nil {
			return nil
		}
		out := make(map[string]any, len(v.keys))
		for _, key := range v.keys {
			out[key] = Plain(v.vals[key])
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = Plain(item)
		}
		return out
	case Num:
		return json.Number(v)
	default:
		return value
	}
}
