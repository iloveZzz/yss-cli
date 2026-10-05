package schema_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/schema"
)

func TestParsePreservesLargeJSONNumbersAndRejectsDuplicateKeys(t *testing.T) {
	v, err := schema.Parse([]byte(`{"id":9007199254740993,"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	obj := v.(map[string]any)
	if got := obj["id"]; got != json.Number("9007199254740993") {
		t.Fatalf("large id = %#v", got)
	}
	if _, err := schema.Parse([]byte(`{"id":1,"id":2}`)); err == nil {
		t.Fatal("duplicate keys accepted")
	}
}

func TestLoadFileEnforcesJSONAndNestedDuplicateAndUnicodePointers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "asset.json")
	for _, input := range []string{`{"a":1,}`, `{"value":{"a/b~c":1,"a/b~c":2}}`, `{"n": NaN}`} {
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := schema.LoadFile(path); err == nil {
			t.Fatalf("malformed JSON accepted: %s", input)
		}
	}
	if _, err := schema.Parse([]byte("# empty document\n")); err == nil {
		t.Fatal("empty YAML document accepted")
	}
	if _, err := schema.Parse([]byte{0xff}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func TestParseRejectsUnpairedUTF16Escapes(t *testing.T) {
	for _, input := range []string{`"\ud800"`, `"\udfff"`, `{"key\ud800":"value"}`} {
		if _, err := schema.Parse([]byte(input)); err == nil {
			t.Fatalf("unpaired surrogate accepted: %s", input)
		}
	}
	v, err := schema.Parse([]byte(`"\ud83d\ude00"`))
	if err != nil || v != "😀" {
		t.Fatalf("valid surrogate pair: %v %v", v, err)
	}
	v, err = schema.Parse([]byte(`"\\ud800"`))
	if err != nil || v != `\ud800` {
		t.Fatalf("literal escape: %v %v", v, err)
	}
}

func TestYAMLRejectsDuplicateKeysAliasesAndNonJSONValues(t *testing.T) {
	for _, input := range []string{
		"name: one\nname: two\n", "base: &x {a: 1}\ncopy: *x\n", "1: non-string-key\n", "value: .nan\n", "value: !custom x\n", "---\na: 1\n---\nb: 2\n", "a:\n  <<: {b: 1}\n",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := schema.Parse([]byte(input)); err == nil {
				t.Fatal("unsafe YAML accepted")
			}
		})
	}
	v, err := schema.Parse([]byte("name: example\ncount: 9007199254740993\nflag: false\nitems: [one, two]\ndate: 2026-10-05\n"))
	if err != nil {
		t.Fatal(err)
	}
	obj := v.(map[string]any)
	if obj["count"] != json.Number("9007199254740993") || obj["flag"] != false || obj["date"] != "2026-10-05" {
		t.Fatalf("unexpected YAML value: %#v", obj)
	}
}
