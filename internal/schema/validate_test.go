package schema_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/schema"
)

func put(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidateUses202012FormatAssertionsAndLocalReferences(t *testing.T) {
	dir := t.TempDir()
	root := put(t, dir, "schema.json", `{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://local.example/root","type":"object","required":["date"],"properties":{"date":{"$ref":"date.json"}},"unevaluatedProperties":false}`)
	put(t, dir, "date.json", `{"$id":"https://local.example/date.json","type":"string","format":"date"}`)
	data := put(t, dir, "input.yaml", "date: 2026-10-05\n")
	issues, err := schema.Validate(root, data)
	if err != nil || len(issues) != 0 {
		t.Fatalf("valid input: issues=%v err=%v", issues, err)
	}
	data = put(t, dir, "input.yaml", "date: impossible\n")
	issues, err = schema.Validate(root, data)
	if err != nil || len(issues) == 0 {
		t.Fatalf("invalid format accepted: issues=%v err=%v", issues, err)
	}
	if issues[0].Path != "/date" {
		t.Fatalf("issue path = %q", issues[0].Path)
	}
}

func TestValidateRejectsUnregisteredNetworkAndAbsoluteReferences(t *testing.T) {
	for _, ref := range []string{"https://remote.example/unknown.json", "file:///unknown.json", "/unknown.json"} {
		t.Run(ref, func(t *testing.T) {
			dir := t.TempDir()
			root := put(t, dir, "schema.json", `{"$ref":"`+ref+`"}`)
			data := put(t, dir, "data.json", `{}`)
			if _, err := schema.Validate(root, data); err == nil || !strings.Contains(err.Error(), "SCHEMA_OFFLINE") {
				t.Fatalf("network reference error = %v", err)
			}
		})
	}
}

func TestValidateRegexPreservesPythonEndAnchorAndRejectsOtherDialects(t *testing.T) {
	dir := t.TempDir()
	root := put(t, dir, "schema.json", `{"type":"string","pattern":"^value$"}`)
	data := put(t, dir, "data.json", `"value\n"`)
	issues, err := schema.Validate(root, data)
	if err != nil || len(issues) > 0 {
		t.Fatalf("Python final-newline semantics changed: issues=%v err=%v", issues, err)
	}
	for _, pattern := range []string{`\\bword\\b`, `a(?=b)`, `(a)\\1`} {
		root = put(t, dir, "schema.json", `{"type":"string","pattern":"`+pattern+`"}`)
		if _, err := schema.Validate(root, data); err == nil || !strings.Contains(err.Error(), "SCHEMA_REGEX_INCOMPATIBLE") {
			t.Fatalf("incompatible pattern %s: %v", pattern, err)
		}
	}
}

func TestValidateRegexUsesPinnedUnicodeClassesInsideAndOutsideBrackets(t *testing.T) {
	cases := []struct {
		pattern, value string
		valid          bool
	}{
		{`^\d+$`, "١٢３", true}, {`^\d+$`, "²", false}, {`^\d+$`, "\U00010d40", false},
		{`^\w+$`, "中文_²١", true}, {`^\w+$`, "e\u0301", false}, {`^\W+$`, "！？", true},
		{`^[\w]+$`, "中文_²", true}, {`^[^\W]+$`, "中文_²", true}, {`^[\D]+$`, "中文_", true},
		{`^\s+$`, "\u001c\u0085\u00a0\u2003", true}, {`^\s+$`, "\u200b", false}, {`^[\S]+$`, "中文", true},
		{`^[\u0000-\u001f\u007f]$`, "\u001f", true}, {`^[\u0000-\u001f\u007f]$`, "中", false},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+tc.value, func(t *testing.T) {
			dir := t.TempDir()
			doc, _ := json.Marshal(map[string]any{"type": "string", "pattern": tc.pattern})
			instance, _ := json.Marshal(tc.value)
			root := put(t, dir, "schema.json", string(doc))
			data := put(t, dir, "data.json", string(instance))
			issues, err := schema.Validate(root, data)
			if err != nil {
				t.Fatal(err)
			}
			if (len(issues) == 0) != tc.valid {
				t.Fatalf("value %q: issues=%v expected valid=%v", tc.value, issues, tc.valid)
			}
		})
	}
}

func TestValidateDoesNotTreatConstantDataAsSchemaKeywords(t *testing.T) {
	dir := t.TempDir()
	root := put(t, dir, "schema.json", `{"const":{"pattern":"(?=x)"}}`)
	data := put(t, dir, "data.json", `{"pattern":"(?=x)"}`)
	issues, err := schema.Validate(root, data)
	if err != nil || len(issues) > 0 {
		t.Fatalf("constant data scanned as schema: issues=%v err=%v", issues, err)
	}
}

func TestOfflineClosureConflictsAndDynamicReferences(t *testing.T) {
	t.Run("URI alias bound to local bytes", func(t *testing.T) {
		dir := t.TempDir()
		root := put(t, dir, "schema.json", `{"$id":"https://local.example/root","$defs":{"local":{"$ref":"child.json"}},"$ref":"https://local.example/child.json"}`)
		put(t, dir, "child.json", `{"$id":"https://local.example/child.json","type":"integer"}`)
		data := put(t, dir, "data.json", `4`)
		issues, err := schema.Validate(root, data)
		if err != nil || len(issues) > 0 {
			t.Fatalf("local URI alias not resolved: issues=%v err=%v", issues, err)
		}
	})
	t.Run("relative root id", func(t *testing.T) {
		dir := t.TempDir()
		root := put(t, dir, "schema.json", `{"$id":"relative-schema.json","type":"integer"}`)
		data := put(t, dir, "data.json", `4`)
		issues, err := schema.Validate(root, data)
		if err != nil || len(issues) > 0 {
			t.Fatalf("relative root id failed: issues=%v err=%v", issues, err)
		}
	})
	t.Run("different bytes same id", func(t *testing.T) {
		dir := t.TempDir()
		root := put(t, dir, "schema.json", `{"allOf":[{"$ref":"a.json"},{"$ref":"b.json"}]}`)
		put(t, dir, "a.json", `{"$id":"https://local.example/shared","type":"integer"}`)
		put(t, dir, "b.json", `{"$id":"https://local.example/shared","type":"string"}`)
		data := put(t, dir, "data.json", `4`)
		if _, err := schema.Validate(root, data); err == nil || !strings.Contains(err.Error(), "SCHEMA_ID_CONFLICT") {
			t.Fatalf("ID conflict accepted: %v", err)
		}
	})
	t.Run("recursive dynamicRef", func(t *testing.T) {
		dir := t.TempDir()
		root := put(t, dir, "schema.json", `{"$dynamicAnchor":"node","type":"object","required":["value"],"properties":{"value":{"type":"integer"},"children":{"type":"array","items":{"$dynamicRef":"#node"}}}}`)
		data := put(t, dir, "data.json", `{"value":1,"children":[{"value":2}]}`)
		issues, err := schema.Validate(root, data)
		if err != nil || len(issues) > 0 {
			t.Fatalf("dynamicRef failed: issues=%v err=%v", issues, err)
		}
		data = put(t, dir, "data.json", `{"value":1,"children":[{"value":"bad"}]}`)
		issues, err = schema.Validate(root, data)
		if err != nil || len(issues) == 0 || issues[0].Path != "/children/0/value" {
			t.Fatalf("dynamicRef nested failure: issues=%v err=%v", issues, err)
		}
	})
	t.Run("invalid external schema shape", func(t *testing.T) {
		dir := t.TempDir()
		root := put(t, dir, "schema.json", `{"$defs":{"unused":{"$ref":"invalid.json"}},"type":"integer"}`)
		put(t, dir, "invalid.json", `{"type":42}`)
		data := put(t, dir, "data.json", `4`)
		if _, err := schema.Validate(root, data); err == nil || !strings.Contains(err.Error(), "SCHEMA_COMPILE") {
			t.Fatalf("invalid closure schema accepted: %v", err)
		}
	})
	t.Run("no HTTP side effect", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			_, _ = w.Write([]byte(`{"type":"integer"}`))
		}))
		defer server.Close()
		dir := t.TempDir()
		root := put(t, dir, "schema.json", `{"$ref":"`+server.URL+`/schema.json"}`)
		data := put(t, dir, "data.json", `4`)
		if _, err := schema.Validate(root, data); err == nil {
			t.Fatal("remote ref accepted")
		}
		if requests.Load() != 0 {
			t.Fatal("validator performed network retrieval")
		}
	})
}

// The public local closure must resolve native Windows drive paths and retain
// URI-escaped filename bytes. The same case executes on every native runner.
func TestLocalSchemaReferenceNativePathWithReservedURIFilename(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "schema 空间#%")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	put(t, dir, "value #%.json", `{"type":"integer","minimum":2}`)
	root := put(t, dir, "main.json", `{"$ref":"value%20%23%25.json"}`)
	issues, err := schema.ValidateValue(root, json.Number("3"))
	if err != nil || len(issues) != 0 {
		t.Fatalf("local schema URI failed: %v %v", issues, err)
	}
	issues, err = schema.ValidateValue(root, json.Number("1"))
	if err != nil || len(issues) != 1 || issues[0].Code != "SCHEMA_VALIDATION" {
		t.Fatalf("local constraint lost: %v %v", issues, err)
	}
}
