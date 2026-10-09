package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/project"
)

func skillQuery(t *testing.T, root string, args ...string) (int, map[string]any) {
	t.Helper()
	var out, stderr bytes.Buffer
	args = append(args, "--root", root, "--json")
	code := Run(context.Background(), args, &out, &stderr)
	var env map[string]any
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("query %v: %s %s", args, out.String(), stderr.String())
	}
	return code, env
}

func TestSkillsQueriesAreReadOnlyAndRetainList(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := project.Build(root, "spec", "init", map[string]string{"projectName": "skills-query"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = project.Apply(p); err != nil {
		t.Fatal(err)
	}
	before := cliGovSnapshot(t, root)
	code, old := skillQuery(t, root, "skills", "list")
	if code != 0 {
		t.Fatal(old)
	}
	if _, ok := old["result"].([]any); !ok {
		t.Fatalf("old list changed: %v", old)
	}
	code, details := skillQuery(t, root, "skills", "list", "--details")
	if code != 0 {
		t.Fatalf("details rejected: %v", details)
	}
	result := details["result"].(map[string]any)
	if result["readOnly"] != true || result["verification"] != "metadata-only" {
		t.Fatal(result)
	}
	code, resolved := skillQuery(t, root, "skills", "resolve", "code-review", "codebase-design", "code-review", "--agent-runtime", "codex")
	if code != 0 {
		t.Fatal(resolved)
	}
	r := resolved["result"].(map[string]any)
	if r["status"] != "missing" || len(r["canonicalIds"].([]any)) != 2 || len(r["missing"].([]any)) != 2 {
		t.Fatal(r)
	}
	if !reflect.DeepEqual(before, cliGovSnapshot(t, root)) {
		t.Fatal("query wrote files")
	}
	code, again := skillQuery(t, root, "skills", "list")
	if code != 0 || !reflect.DeepEqual(old, again) {
		t.Fatal("legacy list is unstable")
	}
	for _, args := range [][]string{
		{"skills", "resolve", "code-review"},
		{"skills", "resolve", "code-review", "--agent-runtime", "codex", "--apply", "--plan-file", "/no-plan"},
		{"skills", "list", "--details", "--plan"},
		{"skills", "list", "--details", "--apply", "--plan-file", "/no-plan"},
		{"skills", "ensure", "code-review", "--details"},
		{"assets", "list", "--details"},
	} {
		code, env := skillQuery(t, root, args...)
		if code == 0 || env["code"] != "ARGUMENT" {
			t.Fatalf("query invalid args: %v -> %v", args, env)
		}
	}
	if !reflect.DeepEqual(before, cliGovSnapshot(t, root)) {
		t.Fatal("rejected query wrote files")
	}
}
