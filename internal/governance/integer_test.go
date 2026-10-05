package governance

import (
	"encoding/json"
	"testing"
)

func TestIntegerExactJSONValues(t *testing.T) {
	valid := map[string]int64{
		"0": 0, "-0": 0, "1.0": 1, "1e0": 1, "100e-2": 1,
		"0.0100e2": 1, "1e+0000": 1, "-1.000e000": -1,
		"9223372036854775807.0":               9223372036854775807,
		"-9223372036854775808e0":              -9223372036854775808,
		"0e999999999999999999999999999999999": 0,
	}
	for raw, want := range valid {
		t.Run(raw, func(t *testing.T) {
			got, ok := integer(json.Number(raw))
			if !ok || got != want {
				t.Fatalf("%s: %d/%v, want %d", raw, got, ok, want)
			}
		})
	}
	for _, raw := range []string{"1.1", "2.0000000000000000001", "1e-1", "1e99999999999999999999999999999", "1e-99999999999999999999999", "9223372036854775808", "-9223372036854775809", "01", "+1", "1.", " 1", "NaN", "1e"} {
		t.Run(raw, func(t *testing.T) {
			if _, ok := integer(json.Number(raw)); ok {
				t.Fatalf("accepted %s", raw)
			}
		})
	}
	for _, v := range []any{1, float64(1), "1", nil} {
		if _, ok := integer(v); ok {
			t.Fatalf("accepted non JSON number %T", v)
		}
	}
}
