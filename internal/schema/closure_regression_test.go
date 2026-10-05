package schema_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/schema"
)

func TestYAMLCoreScalarAndKeyRegressions(t *testing.T) {
	cases := []struct {
		text string
		want any
	}{
		{"010", json.Number("10")}, {"012", json.Number("12")}, {"08", json.Number("8")},
		{"0o12", json.Number("10")}, {"0x12", json.Number("18")}, {"+10", json.Number("10")},
		{"-010", json.Number("-10")}, {"0b101", "0b101"}, {"1_000", "1_000"},
		{"-0o12", "-0o12"}, {"+0x12", "+0x12"}, {"0O12", "0O12"}, {"0X12", "0X12"},
		{"1_2.3", "1_2.3"}, {"001.20", json.Number("1.20")}, {"001.0e+2", json.Number("1.0e+2")},
		{".5", json.Number("0.5")}, {"1.", json.Number("1.0")}, {"1.e+2", json.Number("1.0e+2")},
		{"TrUe", "TrUe"}, {"nUlL", "nUlL"}, {"yes", "yes"}, {"2026-10-05", "2026-10-05"},
		{"TRUE", true}, {"Null", nil}, {`"010"`, "010"}, {`'true'`, "true"},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			value, err := schema.Parse([]byte("value: " + tc.text + "\n"))
			if err != nil {
				t.Fatal(err)
			}
			if got := value.(map[string]any)["value"]; got != tc.want {
				t.Fatalf("%s: got %#v want %#v", tc.text, got, tc.want)
			}
		})
	}
	for _, key := range []string{"0b101", "1_000", "-0o12", "yes"} {
		value, err := schema.Parse([]byte(key + ": ok\n"))
		if err != nil || value.(map[string]any)[key] != "ok" {
			t.Fatalf("Core string key %q: value=%v err=%v", key, value, err)
		}
	}
	for _, input := range []string{"010: no\n", "value: !!str 010\n", "value: &unused 010\n", "value: .Inf\n", "value: -.INF\n"} {
		if _, err := schema.Parse([]byte(input)); err == nil {
			t.Fatalf("ambiguous/non-JSON YAML accepted: %q", input)
		}
	}
}

func TestNodeYAMLCoreDifferential(t *testing.T) {
	root := os.Getenv("YSS_SCHEMA_CORPUS_ROOT")
	if root == "" {
		t.Skip("set YSS_SCHEMA_CORPUS_ROOT to run the existing Node YAML oracle")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is only an optional differential-test oracle")
	}
	inputs := []string{"010", "012", "08", "0o12", "-0o12", "+0o12", "0O12", "0x12", "-0x12", "+0x12", "0X12", "1_000", "1_2.3", "0b101", "001.20", "001.0e+2", ".5", "1.", "1.e+2", "Null", "nUlL", "TrUe", "TRUE", "yes", "2026-10-05", `"010"`, `'true'`}
	payload, _ := json.Marshal(inputs)
	cmd := exec.Command(node, "--input-type=module", "-e", `import { parseDocument } from './scripts/vendor/yaml.mjs';
let input=''; for await (const chunk of process.stdin) input+=chunk;
const output=JSON.parse(input).map(text => {
 const doc=parseDocument('value: '+text, {uniqueKeys:true,maxAliasCount:0,intAsBigInt:true});
 if(doc.errors.length || doc.warnings.length) throw new Error('unexpected oracle parse diagnostic');
 const value=doc.toJS().value; return {type:typeof value, value:typeof value==='bigint' ? value.toString() : value};
}); console.log(JSON.stringify(output));`)
	cmd.Dir = root
	cmd.Stdin = bytes.NewReader(payload)
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var oracle []struct {
		Type  string `json:"type"`
		Value any    `json:"value"`
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.UseNumber()
	if err := decoder.Decode(&oracle); err != nil || len(oracle) != len(inputs) {
		t.Fatalf("oracle output: %s err=%v", output, err)
	}
	for i, input := range inputs {
		value, err := schema.Parse([]byte("value: " + input + "\n"))
		if err != nil {
			t.Fatal(err)
		}
		got := value.(map[string]any)["value"]
		if oracle[i].Type == "bigint" || oracle[i].Type == "number" {
			number, ok := got.(json.Number)
			if !ok {
				t.Fatalf("%s: Node number became %T", input, got)
			}
			expected, ok1 := new(big.Rat).SetString(fmt.Sprint(oracle[i].Value))
			actual, ok2 := new(big.Rat).SetString(string(number))
			if !ok1 || !ok2 || expected.Cmp(actual) != 0 {
				t.Fatalf("%s: Node=%v Go=%v", input, oracle[i].Value, number)
			}
		} else if got != oracle[i].Value {
			t.Fatalf("%s: Node=%#v Go=%#v", input, oracle[i].Value, got)
		}
	}
	t.Logf("Node YAML Core differential cases=%d", len(inputs))
}

func TestRegexRejectsGoOnlySyntaxThroughPatternAndFormat(t *testing.T) {
	for _, pattern := range []string{`^\Qabc\E$`, `^[[:alpha:]]$`, `[[:digit:]]+`, `\x{61}`, `\E`, `\q`} {
		t.Run(pattern, func(t *testing.T) {
			dir := t.TempDir()
			doc, _ := json.Marshal(map[string]any{"type": "string", "pattern": pattern})
			path := put(t, dir, "schema.json", string(doc))
			if _, err := schema.ValidateValue(path, "abc"); err == nil || !strings.Contains(err.Error(), "SCHEMA_REGEX_INCOMPATIBLE") {
				t.Fatalf("pattern accepted with non-Python semantics: %v", err)
			}
			path = put(t, dir, "schema.json", `{"type":"string","format":"regex"}`)
			issues, err := schema.ValidateValue(path, pattern)
			if err != nil || len(issues) == 0 || !strings.Contains(issues[0].Message, "SCHEMA_REGEX_INCOMPATIBLE") {
				t.Fatalf("format accepted unsupported syntax: issues=%v err=%v", issues, err)
			}
		})
	}
}

func TestReferenceLikeDataRemainsOpaque(t *testing.T) {
	payload := map[string]any{"$id": "https://never-contact.invalid/id", "$ref": "https://never-contact.invalid/data.json", "$dynamicRef": "missing.json", "pattern": "(?=x)", "properties": map[string]any{"bad": map[string]any{"$ref": "missing.json"}}}
	for _, keyword := range []string{"const", "enum", "default", "examples", "x-extension"} {
		t.Run(keyword, func(t *testing.T) {
			dir := t.TempDir()
			data := any(payload)
			if keyword == "enum" || keyword == "examples" {
				data = []any{payload}
			}
			doc, _ := json.Marshal(map[string]any{keyword: data})
			path := put(t, dir, "schema.json", string(doc))
			issues, err := schema.ValidateValue(path, payload)
			if err != nil || len(issues) != 0 {
				t.Fatalf("data interpreted as schema: issues=%v err=%v", issues, err)
			}
		})
	}
}

func TestNestedIDsBindActualInlineSchemaAndPhysicalBase(t *testing.T) {
	t.Run("relative root and nested file bases", func(t *testing.T) {
		dir := t.TempDir()
		root := put(t, dir, "schema.json", `{"$id":"schemas/","$defs":{"nested":{"$id":"inner/item.json","$ref":"child.json"}},"$ref":"#/$defs/nested"}`)
		put(t, dir, "child.json", `{"type":"string"}`)
		put(t, dir, "schemas/inner/child.json", `{"type":"integer","minimum":2}`)
		issues, err := schema.ValidateValue(root, 2)
		if err != nil || len(issues) != 0 {
			t.Fatalf("scoped file lookup: issues=%v err=%v", issues, err)
		}
		issues, err = schema.ValidateValue(root, "wrong-directory")
		if err != nil || len(issues) == 0 {
			t.Fatalf("wrong directory bytes used: issues=%v err=%v", issues, err)
		}
	})
	t.Run("sibling inline IDs and cross-file alias", func(t *testing.T) {
		dir := t.TempDir()
		root := put(t, dir, "schema.json", `{"allOf":[{"$ref":"container.json"},{"$ref":"https://local.test/nested#number"}]}`)
		put(t, dir, "container.json", `{"$id":"https://local.test/container","type":"integer","$defs":{"a":{"$id":"https://local.test/a","$ref":"https://local.test/nested"},"b":{"$id":"https://local.test/nested","$dynamicAnchor":"number","type":"integer","minimum":2}}}`)
		issues, err := schema.ValidateValue(root, 2)
		if err != nil || len(issues) != 0 {
			t.Fatalf("inline alias lost: issues=%v err=%v", issues, err)
		}
		issues, err = schema.ValidateValue(root, 1)
		if err != nil || len(issues) == 0 {
			t.Fatalf("enclosing schema substituted for inline schema: issues=%v err=%v", issues, err)
		}
	})
	t.Run("nested dynamic ref to inline resource", func(t *testing.T) {
		dir := t.TempDir()
		root := put(t, dir, "schema.json", `{"$id":"https://local.test/root","$defs":{"nested":{"$id":"sub/","$dynamicAnchor":"value","type":"integer"}},"$dynamicRef":"https://local.test/sub/#value"}`)
		issues, err := schema.ValidateValue(root, 2)
		if err != nil || len(issues) != 0 {
			t.Fatalf("dynamic inline ID failed: issues=%v err=%v", issues, err)
		}
	})
	t.Run("inline ID conflict with local document", func(t *testing.T) {
		dir := t.TempDir()
		root := put(t, dir, "schema.json", `{"$defs":{"inline":{"$id":"https://local.test/shared","type":"integer"},"external":{"$ref":"child.json"}}}`)
		put(t, dir, "child.json", `{"$id":"https://local.test/shared","type":"string"}`)
		if _, err := schema.ValidateValue(root, 1); err == nil || !strings.Contains(err.Error(), "SCHEMA_ID_CONFLICT") {
			t.Fatalf("inline/file ID conflict accepted: %v", err)
		}
	})
}
