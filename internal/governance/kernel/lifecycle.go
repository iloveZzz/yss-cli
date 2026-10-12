package kernel

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

func Object(v any) (map[string]any, bool) { m, ok := v.(map[string]any); return m, ok }

func Text(v any) string { s, _ := v.(string); return s }

var integerNumberSyntax = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// integer compares exact JSON numeric values without rounding through float64.
// Scale is bounded before expanding digits, including hostile huge exponents.
func Integer(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok || !integerNumberSyntax.MatchString(string(n)) {
		return 0, false
	}
	raw := string(n)
	negative := strings.HasPrefix(raw, "-")
	if negative {
		raw = raw[1:]
	}
	mantissa, exponent := raw, "0"
	if i := strings.IndexAny(raw, "eE"); i >= 0 {
		mantissa, exponent = raw[:i], raw[i+1:]
	}
	fraction := 0
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		fraction = len(mantissa) - i - 1
		mantissa = mantissa[:i] + mantissa[i+1:]
	}
	digits := strings.TrimLeft(mantissa, "0")
	if digits == "" {
		return 0, true
	}
	expNegative := strings.HasPrefix(exponent, "-")
	exponent = strings.TrimPrefix(strings.TrimPrefix(exponent, "+"), "-")
	exponent = strings.TrimLeft(exponent, "0")
	if exponent == "" {
		exponent = "0"
	}
	if expNegative {
		exponent = "-" + exponent
	}
	exp, err := strconv.ParseInt(exponent, 10, 64)
	bound := int64(len(raw)) + 20
	if err != nil || exp > bound || exp < -bound {
		return 0, false
	}
	scale := exp - int64(fraction)
	if scale < 0 {
		remove := -scale
		if remove >= int64(len(digits)) {
			return 0, false
		}
		tail := digits[len(digits)-int(remove):]
		if strings.Trim(tail, "0") != "" {
			return 0, false
		}
		digits = digits[:len(digits)-int(remove)]
	} else {
		if int64(len(digits))+scale > 19 {
			return 0, false
		}
		digits += strings.Repeat("0", int(scale))
	}
	if negative {
		digits = "-" + digits
	}
	result, err := strconv.ParseInt(digits, 10, 64)
	return result, err == nil
}

func FindRegistry(m map[string]any, id string) (string, map[string]any, bool) {
	for _, kind := range []string{"stages", "work_units", "gates", "artifacts", "evidence", "checks"} {
		rows, _ := m[kind].([]any)
		for _, v := range rows {
			row, _ := Object(v)
			if Text(row["id"]) == id {
				return kind, row, true
			}
		}
	}
	return "", nil, false
}
