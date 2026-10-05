package schema_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/schema"
)

func TestPythonDraft202012DifferentialCorpus(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python is only an optional differential-test oracle")
	}
	probe := exec.Command(python, "-c", "import jsonschema, referencing, unicodedata; assert unicodedata.unidata_version == '15.0.0'")
	if out, err := probe.CombinedOutput(); err != nil {
		t.Skipf("Python baseline unavailable: %s (%v)", out, err)
	}
	type comparison struct {
		Name                                 string `json:"name"`
		Schema                               any    `json:"schema"`
		Value                                any    `json:"value"`
		Want                                 bool   `json:"want"`
		BaselineMayBeMissingFormatDependency bool
	}
	cases := []comparison{}
	add := func(name, raw string, value any, want bool) {
		var doc any
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&doc); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, comparison{Name: name, Schema: doc, Value: value, Want: want})
	}
	add("large integer accepted", `{"type":"integer","const":9007199254740993}`, json.Number("9007199254740993"), true)
	add("large adjacent integer refused", `{"type":"integer","const":9007199254740993}`, json.Number("9007199254740992"), false)
	add("decimal multiple", `{"type":"number","multipleOf":0.25}`, json.Number("1.5"), true)
	add("decimal multiple refusal", `{"type":"number","multipleOf":0.25}`, json.Number("1.3"), false)
	add("Unicode codepoints", `{"type":"string","minLength":2,"maxLength":2}`, "中文", true)
	add("required boolean does not become integer", `{"type":"integer"}`, true, false)
	add("prefixItems with closed tail", `{"prefixItems":[{"type":"integer"}],"items":false}`, []any{1}, true)
	add("prefixItems rejects excess", `{"prefixItems":[{"type":"integer"}],"items":false}`, []any{1, 2}, false)
	add("unevaluated properties through allOf", `{"allOf":[{"properties":{"a":{"type":"integer"}}}],"unevaluatedProperties":false}`, map[string]any{"a": 1}, true)
	add("unevaluated property refused", `{"allOf":[{"properties":{"a":{"type":"integer"}}}],"unevaluatedProperties":false}`, map[string]any{"a": 1, "b": 2}, false)
	add("dependentRequired refuses missing", `{"dependentRequired":{"a":["b"]}}`, map[string]any{"a": 1}, false)
	add("contains bounds", `{"contains":{"type":"integer"},"minContains":2,"maxContains":2}`, []any{1, "a", 2}, true)
	add("contains excess", `{"contains":{"type":"integer"},"minContains":2,"maxContains":2}`, []any{1, 2, 3}, false)
	add("if then", `{"if":{"properties":{"kind":{"const":"x"}}},"then":{"required":["a"]}}`, map[string]any{"kind": "x"}, false)
	add("anyOf", `{"anyOf":[{"type":"integer"},{"type":"string"}]}`, false, false)
	add("oneOf multiple refusal", `{"oneOf":[{"type":"number"},{"type":"integer"}]}`, 1, false)
	add("date format", `{"format":"date"}`, "2026-10-05", true)
	add("invalid leap date", `{"format":"date"}`, "2025-02-29", false)
	add("date-time offset", `{"format":"date-time"}`, "2026-10-05T08:00:00+08:00", true)
	add("date-time lowercase", `{"format":"date-time"}`, "2026-10-05t08:00:00z", true)
	add("date-time no timezone", `{"format":"date-time"}`, "2026-10-05T08:00:00", false)
	cases[len(cases)-1].BaselineMayBeMissingFormatDependency = true
	for i, entry := range []struct {
		pattern, value string
		want           bool
	}{
		{`^\d+$`, "١２", true}, {`^\d+$`, "²", false}, {`^\d+$`, "\U00010d40", false},
		{`^\w+$`, "中文_²", true}, {`^\w+$`, "e\u0301", false}, {`^[^\W]+$`, "中文_²", true},
		{`^\s+$`, "\u001c\u0085\u00a0", true}, {`^\s+$`, "\u200b", false}, {`^x$`, "x\n", true},
	} {
		doc, _ := json.Marshal(map[string]any{"pattern": entry.pattern})
		add(fmt.Sprintf("Unicode regex %d", i), string(doc), entry.value, entry.want)
	}
	payload, _ := json.Marshal(cases)
	cmd := exec.Command(python, "-c", `import json,sys
from jsonschema import Draft202012Validator, FormatChecker
cases=json.load(sys.stdin)
results=[]
for case in cases:
    Draft202012Validator.check_schema(case['schema'])
    errors=list(Draft202012Validator(case['schema'],format_checker=FormatChecker()).iter_errors(case['value']))
    results.append({'valid':not errors,'paths':sorted('/'+'/'.join(str(p).replace('~','~0').replace('/','~1') for p in e.absolute_path) if e.absolute_path else '' for e in errors)})
json.dump(results,sys.stdout)`)
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var oracle []struct {
		Valid bool     `json:"valid"`
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(out, &oracle); err != nil {
		t.Fatal(err)
	}
	if len(oracle) != len(cases) {
		t.Fatal("oracle output length mismatch")
	}
	for i, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			if oracle[i].Valid != tc.Want && !tc.BaselineMayBeMissingFormatDependency {
				t.Fatalf("baseline disagrees with declared behavior: %s", out)
			}
			dir := t.TempDir()
			doc, _ := json.Marshal(tc.Schema)
			value, _ := json.Marshal(tc.Value)
			root := put(t, dir, "schema.json", string(doc))
			data := put(t, dir, "data.json", string(value))
			issues, err := schema.Validate(root, data)
			if err != nil {
				t.Fatal(err)
			}
			if (len(issues) == 0) != tc.Want {
				t.Fatalf("declared behavior mismatch: Go issues=%v want=%v", issues, tc.Want)
			}
			if (len(issues) == 0) != oracle[i].Valid {
				if !tc.BaselineMayBeMissingFormatDependency {
					t.Fatalf("differential mismatch: Python valid=%v Go issues=%v", oracle[i].Valid, issues)
				}
				t.Log("explicit stricter format behavior: Python environment lacks optional date-time checker; native Go rejects invalid RFC3339")
			}
		})
	}
	t.Logf("differential cases=%d; CPython Unicode15 Draft202012 + FormatChecker", len(cases))
}

func TestCurrentRepositorySchemaCompilation(t *testing.T) {
	root := os.Getenv("YSS_SCHEMA_CORPUS_ROOT")
	if root == "" {
		t.Skip("set YSS_SCHEMA_CORPUS_ROOT for live template schema inventory")
	}
	dir := t.TempDir()
	data := put(t, dir, "data.json", `null`)
	count := 0
	for _, scope := range []string{".template-spec", ".template-source", ".agents/skills"} {
		err := filepath.WalkDir(filepath.Join(root, scope), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".schema.json") {
				return nil
			}
			count++
			t.Run(strings.TrimPrefix(path, root+string(filepath.Separator)), func(t *testing.T) {
				if _, err := schema.Validate(path, data); err != nil {
					t.Error(err)
				}
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if count == 0 {
		t.Fatal("empty schema corpus")
	}
	t.Logf("current repository schema compilation count=%d", count)
}
