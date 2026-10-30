//go:build !yss_nobundle

package reading_test

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance/reading"
	"github.com/iloveZzz/yss-cli/internal/project"
)

func writeQueryFile(t *testing.T, root, ref, data string) {
	t.Helper()
	path := filepath.Join(root, ref)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func installQueryProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	plan, err := project.Build(root, "backend", "init", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range plan.Changes {
		path := filepath.Join(root, change.Path)
		if change.After.Type == "directory" {
			if err = os.MkdirAll(path, 0755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if change.After.Type != "file" {
			continue
		}
		data, e := base64.StdEncoding.DecodeString(change.Data)
		if e != nil {
			t.Fatal(e)
		}
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, os.FileMode(change.After.Mode)); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestContractViewRejectsTamperedSchema(t *testing.T) {
	root := installQueryProject(t)
	const schemaRef = ".template-spec/process/schemas/slice-implementation-contract-v3.schema.json"
	writeQueryFile(t, root, "slice.json", `{"schema_version":3}`)
	sources := reading.Sources{Copy: func(v map[string]any) map[string]any {
		result := map[string]any{}
		for k, v := range v {
			result[k] = v
		}
		return result
	}}
	_, err := reading.Run(context.Background(), "contract", "view", root, map[string]string{"kind": "slice", "file": "slice.json"}, sources)
	if err == nil || strings.Contains(err.Error(), "受信 Bundle") {
		t.Fatalf("trusted schema must reject malformed contract, got %v", err)
	}
	writeQueryFile(t, root, schemaRef, `{"type":"object"}`)
	_, err = reading.Run(context.Background(), "contract", "view", root, map[string]string{"kind": "slice", "file": "slice.json"}, sources)
	if err == nil || !strings.Contains(err.Error(), "政策与受信 Bundle") {
		t.Fatalf("tampered schema must be rejected as untrusted, got %v", err)
	}
}

func TestProjectInstanceV1RequiresTrustedSource(t *testing.T) {
	root := t.TempDir()
	writeQueryFile(t, root, "yss-project.yaml", "schema_version: 1\nrepository_mode: project-instance\n")
	writeQueryFile(t, root, ".template-spec/process/schemas/slice-implementation-contract-v3.schema.json", `{"type":"object"}`)
	writeQueryFile(t, root, "slice.json", `{"schema_version":3}`)
	sources := reading.Sources{Copy: func(v map[string]any) map[string]any { return v }}
	_, err := reading.Run(context.Background(), "contract", "view", root, map[string]string{"kind": "slice", "file": "slice.json"}, sources)
	if err == nil || !strings.Contains(err.Error(), "缺少可核验的受信来源") {
		t.Fatalf("unproven v1 instance must be rejected, got %v", err)
	}
}

func TestContextViewAllowsTemplateSourceAndUserContent(t *testing.T) {
	for _, mode := range []string{"template-source", "project-instance"} {
		t.Run(mode, func(t *testing.T) {
			var root string
			if mode == "template-source" {
				root = t.TempDir()
				writeQueryFile(t, root, "yss-project.yaml", "schema_version: 1\nrepository_mode: template-source\n")
			} else {
				root = installQueryProject(t)
				writeQueryFile(t, root, ".template-spec/agents/issue-tracker.md", "user customized tracker\n")
			}
			writeQueryFile(t, root, "CONTEXT.md", "---\ncontext_schema_version: 1\n---\n## 流程术语\n\n| 术语 | 含义 | 英文标识 | 避免 / 备注 |\n|---|---|---|---|\n\n## 业务术语\n\n| 术语 | 含义 | 英文标识 | 适用业务责任区 | 避免 / 备注 |\n|---|---|---|---|---|\n| 订单 | 用户订单 | Order | Global | — |\n")
			sources := reading.Sources{ScanContext: func(string) error { return nil }}
			result, err := reading.Run(context.Background(), "context", "query", root, map[string]string{"view": "agent", "id": "Global/Order"}, sources)
			if err != nil {
				t.Fatal(err)
			}
			if result == nil {
				t.Fatal("context query returned no result")
			}
		})
	}
}

func TestContractViewRejectsUnsafeFileReferences(t *testing.T) {
	root := installQueryProject(t)
	writeQueryFile(t, root, "slice.json", `{"schema_version":3}`)
	if err := os.Symlink("slice.json", filepath.Join(root, "link.json")); err != nil {
		t.Fatal(err)
	}
	sources := reading.Sources{Copy: func(v map[string]any) map[string]any { return v }}
	for _, ref := range []string{filepath.Join(root, "slice.json"), "./slice.json", "sub/../slice.json", "link.json"} {
		t.Run(ref, func(t *testing.T) {
			_, err := reading.Run(context.Background(), "contract", "view", root, map[string]string{"kind": "slice", "file": ref}, sources)
			if err == nil {
				t.Fatalf("unsafe file reference %q accepted", ref)
			}
		})
	}
}
