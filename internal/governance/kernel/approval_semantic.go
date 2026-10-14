package kernel

import (
	"bytes"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

const ApprovalRolesRef = ".template-spec/agents/digital-human-roles.yaml"

const ApprovalRegistryRef = ".template-spec/process/lifecycle-registry.yaml"

const ApprovalSkillsRef = ".template-spec/agents/yss-skill-registry.yaml"

const ApprovalSchemaRef = ".template-spec/process/schemas/approval-record.schema.json"

const ApprovalTaskSchemaRef = ".template-spec/process/schemas/digital-human-task-package.schema.json"

func ApMap(v any) map[string]any { m, _ := v.(map[string]any); return m }

func ApArray(v any) []any {
	switch a := v.(type) {
	case []any:
		return a
	case []string:
		out := make([]any, len(a))
		for i, x := range a {
			out[i] = x
		}
		return out
	case []map[string]any:
		out := make([]any, len(a))
		for i, x := range a {
			out[i] = x
		}
		return out
	}
	return nil
}

func ApIsArray(v any) bool {
	switch v.(type) {
	case []any, []string, []map[string]any:
		return true
	}
	return false
}

// ECMAScript canonical JSON converts every JSON number to IEEE-754 first.
// Go preserves json.Number spellings, which would otherwise change digests.
func apJSONNumber(value string) string {
	n, err := strconv.ParseFloat(value, 64)
	if err != nil && !math.IsInf(n, 0) || math.IsNaN(n) || math.IsInf(n, 0) {
		return "null"
	}
	if n == 0 {
		return "0"
	}
	abs := math.Abs(n)
	if abs >= 1e-6 && abs < 1e21 {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	s := strconv.FormatFloat(n, 'e', -1, 64)
	parts := strings.Split(s, "e")
	exponent, _ := strconv.Atoi(parts[1])
	sign := ""
	if exponent >= 0 {
		sign = "+"
	}
	return parts[0] + "e" + sign + strconv.Itoa(exponent)
}

func ApText(v any) string { s, _ := v.(string); return s }

func ApNumber(v any) int {
	switch x := v.(type) {
	case json.Number:
		n, valid := Integer(x)
		if !valid || n > int64(math.MaxInt) || n < int64(math.MinInt) {
			return -1
		}
		return int(n)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || x != math.Trunc(x) || x >= float64(math.MaxInt) || x < float64(math.MinInt) {
			return -1
		}
		return int(x)
	case int:
		return x
	case int64:
		if x > int64(math.MaxInt) || x < int64(math.MinInt) {
			return -1
		}
		return int(x)
	}
	return -1
}

func ApStrings(v any) []string {
	a := ApArray(v)
	out := make([]string, len(a))
	for i, x := range a {
		out[i] = ApText(x)
	}
	return out
}

func ApContains(v any, want string) bool {
	for _, s := range ApStrings(v) {
		if s == want {
			return true
		}
	}
	return false
}

func ApUnique(v any) bool {
	a := ApArray(v)
	if len(a) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, x := range a {
		s, ok := x.(string)
		if !ok || strings.TrimSpace(s) == "" || seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}

func ApSetEqual(a, b any) bool {
	if !ApUnique(a) || !ApUnique(b) || len(ApArray(a)) != len(ApArray(b)) {
		return false
	}
	for _, s := range ApStrings(a) {
		if !ApContains(b, s) {
			return false
		}
	}
	return true
}

func ApArraysEqual(a, b any) bool { return ApEqual(a, b) }

func ApHex(v any) string { return strings.TrimPrefix(ApText(v), "sha256:") }

func ApHashValid(v any, prefix bool) bool {
	s := ApText(v)
	if prefix {
		s = strings.TrimPrefix(s, "sha256:")
	}
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func ApFind(v any, key, want string) map[string]any {
	for _, x := range ApArray(v) {
		m := ApMap(x)
		if ApText(m[key]) == want {
			return m
		}
	}
	return nil
}

func ApCopy(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func ApTruthy(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		return x != "0"
	case int:
		return x != 0
	}
	return true
}

// apCanonical matches JSON.stringify(canonical(value)): integer property keys
// precede UTF-16-sorted remaining keys; JSON strings retain HTML and U+2028/2029.
// Arrays preserve order. Digests never depend on Go's map iteration order.
func ApCanonical(v any) []byte {
	var out bytes.Buffer
	var emit func(any)
	quote := func(s string) {
		var b bytes.Buffer
		e := json.NewEncoder(&b)
		e.SetEscapeHTML(false)
		_ = e.Encode(s)
		raw := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
		raw = bytes.ReplaceAll(raw, []byte(`\u2028`), []byte("\u2028"))
		raw = bytes.ReplaceAll(raw, []byte(`\u2029`), []byte("\u2029"))
		out.Write(raw)
	}
	less := func(a, b string) bool {
		x, y := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
		for i := 0; i < len(x) && i < len(y); i++ {
			if x[i] != y[i] {
				return x[i] < y[i]
			}
		}
		return len(x) < len(y)
	}
	emit = func(value any) {
		switch x := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			index := func(k string) (uint64, bool) {
				n, e := strconv.ParseUint(k, 10, 32)
				return n, e == nil && n < 4294967295 && strconv.FormatUint(n, 10) == k
			}
			sort.Slice(keys, func(i, j int) bool {
				a, ia := index(keys[i])
				b, ib := index(keys[j])
				if ia && ib {
					return a < b
				}
				if ia != ib {
					return ia
				}
				return less(keys[i], keys[j])
			})
			out.WriteByte('{')
			for i, k := range keys {
				if i > 0 {
					out.WriteByte(',')
				}
				quote(k)
				out.WriteByte(':')
				emit(x[k])
			}
			out.WriteByte('}')
		case []any:
			out.WriteByte('[')
			for i, a := range x {
				if i > 0 {
					out.WriteByte(',')
				}
				emit(a)
			}
			out.WriteByte(']')
		case []string:
			emit(ApArray(x))
		case []map[string]any:
			emit(ApArray(x))
		case string:
			quote(x)
		case json.Number:
			out.WriteString(apJSONNumber(string(x)))
		case float64:
			out.WriteString(apJSONNumber(strconv.FormatFloat(x, 'g', -1, 64)))
		case float32:
			out.WriteString(apJSONNumber(strconv.FormatFloat(float64(x), 'g', -1, 64)))
		case int:
			out.WriteString(apJSONNumber(strconv.Itoa(x)))
		case int64:
			out.WriteString(apJSONNumber(strconv.FormatInt(x, 10)))
		default:
			b, e := json.Marshal(value)
			if e != nil {
				out.WriteString("null")
			} else {
				out.Write(b)
			}
		}
	}
	emit(v)
	return out.Bytes()
}

func ApEqual(a, b any) bool { return bytes.Equal(ApCanonical(a), ApCanonical(b)) }

func ApDigest(v any) string {
	if b, ok := v.([]byte); ok {
		return "sha256:" + safefs.Digest(b)
	}
	if s, ok := v.(string); ok {
		return "sha256:" + safefs.Digest([]byte(s))
	}
	return "sha256:" + safefs.Digest(ApCanonical(v))
}

func SemanticDocumentDigest(value any) (string, error) {
	if _, err := json.Marshal(value); err != nil {
		return "", err
	}
	return safefs.Digest(ApCanonical(value)), nil
}

func ApFail(s *SemanticSession, code, message string) error { return s.Reject(code, message) }

func ApBasis(s *SemanticSession, v any, label, code string) ([]map[string]any, error) {
	a := ApArray(v)
	if len(a) == 0 {
		return nil, ApFail(s, code, label+" 缺少当前证据摘要")
	}
	refs := map[string]bool{}
	out := []map[string]any{}
	for _, x := range a {
		m := ApMap(x)
		ref := ApText(m["ref"])
		if err := RejectProgressionEvidence(s, ref); err != nil {
			return nil, err
		}
		if strings.TrimSpace(ref) == "" || refs[ref] || !ApHashValid(m["digest"], true) {
			return nil, ApFail(s, code, label+" 证据引用缺失、重复或摘要非法")
		}
		refs[ref] = true
		b, e := s.Bytes(ref)
		if e != nil {
			return nil, e
		}
		if safefs.Digest(b) != ApHex(m["digest"]) {
			return nil, ApFail(s, code, label+" 证据过期: "+ref)
		}
		out = append(out, map[string]any{"ref": ref, "digest": ApHex(m["digest"])})
	}
	return out, nil
}

func ApCovers(basis []map[string]any, row map[string]any) bool {
	for _, b := range basis {
		if ApText(b["ref"]) == ApText(row["ref"]) && ApHex(b["digest"]) == ApHex(row["digest"]) {
			return true
		}
	}
	return false
}

func ApRows(v any) []map[string]any {
	a := ApArray(v)
	out := make([]map[string]any, len(a))
	for i, x := range a {
		out[i] = ApMap(x)
	}
	return out
}
