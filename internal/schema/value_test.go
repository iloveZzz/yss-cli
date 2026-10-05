package schema_test

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/schema"
)

func TestValidateValueNormalizesStructsAndPreservesExactNumbers(t *testing.T) {
	dir := t.TempDir()
	root := put(t, dir, "schema.json", `{"type":"object","required":["count"],"properties":{"count":{"type":"integer","const":9007199254740993}},"additionalProperties":false}`)
	type input struct {
		Count uint64 `json:"count"`
	}
	parsed, err := schema.Parse([]byte("count: 9007199254740993\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{input{9007199254740993}, map[string]any{"count": json.Number("9007199254740993")}, parsed} {
		issues, err := schema.ValidateValue(root, value)
		if err != nil || len(issues) != 0 {
			t.Fatalf("precision or tags changed for %T: issues=%v err=%v", value, issues, err)
		}
	}
	issues, err := schema.ValidateValue(root, input{9007199254740992})
	if err != nil || len(issues) == 0 || issues[0].Path != "/count" {
		t.Fatalf("neighboring integer accepted: issues=%v err=%v", issues, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "schema.json" {
		t.Fatalf("in-memory validation wrote files: %v", entries)
	}
}

func TestValidateValueMatchesFileDiagnostics(t *testing.T) {
	dir := t.TempDir()
	root := put(t, dir, "schema.json", `{"type":"object","properties":{"date":{"$ref":"date.json"},"item":{"type":"integer"}},"required":["date","item"],"unevaluatedProperties":false}`)
	put(t, dir, "date.json", `{"type":"string","format":"date"}`)
	type input struct {
		Date string `json:"date"`
		Item string `json:"item"`
	}
	value := input{"impossible", "bad"}
	file := put(t, dir, "input.json", `{"date":"impossible","item":"bad"}`)
	fromFile, err := schema.Validate(root, file)
	if err != nil {
		t.Fatal(err)
	}
	fromValue, err := schema.ValidateValue(root, value)
	if err != nil {
		t.Fatal(err)
	}
	if len(fromFile) != 2 || !reflect.DeepEqual(fromFile, fromValue) {
		t.Fatalf("different diagnostics: file=%#v value=%#v", fromFile, fromValue)
	}
}

func TestValidateValueRejectsNonJSONAndAmbiguousMarshalerResults(t *testing.T) {
	dir := t.TempDir()
	root := put(t, dir, "schema.json", `true`)
	cycle := map[string]any{}
	cycle["self"] = cycle
	for _, value := range []any{math.NaN(), math.Inf(1), func() {}, cycle, json.RawMessage(`{"x":1,"x":2}`), json.RawMessage(`"\ud800"`)} {
		if _, err := schema.ValidateValue(root, value); err == nil {
			t.Fatalf("non-strict value %T accepted", value)
		}
	}
}

func TestValidateValueUsesSameOfflineAndRegexCompilation(t *testing.T) {
	for _, tc := range []struct{ name, doc, code string }{
		{"remote", `{"$ref":"https://remote.invalid/schema.json"}`, "SCHEMA_OFFLINE"},
		{"unsupported regex", `{"type":"string","pattern":"a(?=b)"}`, "SCHEMA_REGEX_INCOMPATIBLE"},
		{"invalid schema", `{"type":42}`, "SCHEMA_COMPILE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			root := put(t, dir, "schema.json", tc.doc)
			if _, err := schema.ValidateValue(root, "ab"); err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("compile error=%v", err)
			}
		})
	}
}

func TestValidateValueSupportsScalarsAndNull(t *testing.T) {
	dir := t.TempDir()
	root := put(t, dir, "schema.json", `{"anyOf":[{"type":"null"},{"type":"string","pattern":"^\\w+$"},{"type":"number","const":1.25}]}`)
	for _, value := range []any{nil, "中文", json.Number("1.25")} {
		issues, err := schema.ValidateValue(root, value)
		if err != nil || len(issues) != 0 {
			t.Fatalf("scalar rejected: value=%v issues=%v err=%v", value, issues, err)
		}
	}
}
