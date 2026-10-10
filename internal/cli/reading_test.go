package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

func TestReadingCLICompatibilityAndMarkdown(t *testing.T) {
	f := newDailyFixture(t)
	b, e := bundle.Load("spec")
	if e != nil {
		t.Fatal(e)
	}
	compilerRef := ".agents/skills/yss-implementation-contract-compiler/references/compiler-contract.yaml"
	for _, ref := range []string{"CONTEXT.md", ".template-spec/process/lifecycle-registry.yaml", compilerRef} {
		raw, e := b.Files[ref].Render(nil)
		if e != nil {
			t.Fatal(e)
		}
		cliGovPut(t, f.root, ref, raw)
	}
	cliGovPut(t, f.root, ".template-spec/process/harness-profile.yaml", []byte("schema_version: 2\nprofile_id: harness.spec-template\n"))
	compilerRaw, e := b.Files[compilerRef].Render(nil)
	if e != nil {
		t.Fatal(e)
	}
	v, e := schema.Parse(compilerRaw)
	if e != nil {
		t.Fatal(e)
	}
	c := map[string]any{}
	for _, field := range v.(map[string]any)["slice_contract_required"].(map[string]any)["root"].([]any) {
		c[field.(string)] = []any{}
	}
	for section, fields := range v.(map[string]any)["slice_contract_required"].(map[string]any) {
		if section == "root" {
			continue
		}
		target := c
		if section != "root" {
			target = map[string]any{}
			c[section] = target
		}
		for _, field := range fields.([]any) {
			target[field.(string)] = []any{}
		}
	}
	c["schema_version"] = 2
	c["contract_id"] = "slice.legacy"
	c["contract_version"] = "v1"
	c["status"] = "draft"
	c["slice_id"] = "legacy"
	c["work_units"] = []any{map[string]any{"contract_id": "slice.legacy", "contract_version": "v1", "work_unit": map[string]any{"id": "work-unit.one", "primary_skill": "tdd", "behavior": "保留兼容约束"}}}
	raw, _ := json.Marshal(map[string]any{"slice_contract": c})
	cliGovPut(t, f.root, "slice.json", raw)
	before, _ := json.Marshal(cliGovSnapshot(t, f.root))
	var out, stderr bytes.Buffer
	args := []string{"contract", "view", "--kind", "slice", "--file", "slice.json", "--root", f.root}
	if code := Run(context.Background(), args, &out, &stderr); code != 0 || !strings.HasPrefix(out.String(), "# Slice") {
		t.Fatalf("Markdown: %d %s %s", code, out.String(), stderr.String())
	}
	out.Reset()
	if code := Run(context.Background(), append(args, "--json"), &out, &stderr); code != 0 {
		t.Fatalf("JSON: %d %s", code, out.String())
	}
	var env map[string]any
	if e = json.Unmarshal(out.Bytes(), &env); e != nil {
		t.Fatal(e)
	}
	if env["outputVersion"] != float64(1) || env["result"].(map[string]any)["view"] != "review" || strings.Contains(out.String(), "markdown") {
		t.Fatal(out.String())
	}
	out.Reset()
	if code := Run(context.Background(), append(args, "--view", "task", "--unit", "work-unit.one", "--json"), &out, &stderr); code != 0 || !strings.Contains(out.String(), "new_impacts") {
		t.Fatalf("v2 task stop conditions: %d %s", code, out.String())
	}
	out.Reset()
	if code := Run(context.Background(), []string{"context", "query", "--root", f.root, "--json"}, &out, &stderr); code != 0 {
		t.Fatal(out.String())
	}
	if e = json.Unmarshal(out.Bytes(), &env); e != nil {
		t.Fatal(e)
	}
	legacy := env["result"].(map[string]any)
	if _, ok := legacy["business_terms"]; !ok {
		t.Fatal("legacy Context protocol changed")
	}
	if _, ok := legacy["view"]; ok {
		t.Fatal("view leaked into old protocol")
	}
	out.Reset()
	if code := Run(context.Background(), []string{"lifecycle", "query", "--root", f.root, "--view", "agent", "--id", "stage.plan", "--json"}, &out, &stderr); code != 0 {
		t.Fatal(out.String())
	}
	after, _ := json.Marshal(cliGovSnapshot(t, f.root))
	if !bytes.Equal(before, after) {
		t.Fatal("reading modified project")
	}
}

func TestReadingCLIFlagsStayScoped(t *testing.T) {
	for _, args := range [][]string{{"contract", "verify", "--view", "review"}, {"context", "verify", "--view", "agent"}, {"stage", "query", "--view", "agent"}, {"lifecycle", "query", "--view", "full"}, {"contract", "view", "--kind", "scaffold"}} {
		var out, err bytes.Buffer
		if code := Run(context.Background(), append(args, "--json"), &out, &err); code != 2 {
			t.Fatalf("unscoped flags accepted: %v %d %s", args, code, out.String())
		}
	}
	var out, err bytes.Buffer
	if Run(context.Background(), []string{"contract", "view", "--help"}, &out, &err) != 0 || !strings.Contains(out.String(), "--view") {
		t.Fatal(out.String())
	}
	if capabilities()["readingViews"] == nil {
		t.Fatal("missing capability discovery")
	}
}
