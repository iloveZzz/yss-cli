package reading

import (
	"context"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildReadingBindsCurrentPolicyWithoutRoles(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			root, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			write := func(ref string, v any) {
				t.Helper()
				b, _ := json.Marshal(v)
				os.MkdirAll(filepath.Dir(filepath.Join(root, ref)), 0755)
				if e := os.WriteFile(filepath.Join(root, ref), b, 0644); e != nil {
					t.Fatal(e)
				}
			}
			write("yss-project.yaml", map[string]any{"schema_version": version, "repository_mode": "project-instance"})
			write(".work/demo/checkpoint.json", map[string]any{"feature_id": "demo", "schema_version": 1, "current_stage": "stage.plan", "artifacts": map[string]any{}})
			write(".template-spec/process/orchestration-contract.yaml", map[string]any{"schema_version": version})
			write(kernel.ApprovalRegistryRef, map[string]any{})
			os.MkdirAll(filepath.Join(root, ".template-spec/agents"), 0755)
			os.WriteFile(filepath.Join(root, kernel.TrackerRef), []byte("---\ntracker:\n  platform: local-markdown\n  root: .work\n---\n"), 0644)
			for _, name := range []string{"lifecycle-checkpoint", "reading-manifest", "reading-policy"} {
				write(".template-spec/process/schemas/"+name+".schema.json", map[string]any{"type": "object"})
			}
			s := kernel.NewSemanticSession(context.Background(), root, nil)
			s.GovernanceVersion = version
			b, e := BuildReadingMode(s, ".work/demo/checkpoint.json", true)
			if e != nil {
				t.Fatal(e)
			}
			_, roles := b.ProjectRefs[kernel.ApprovalRolesRef]
			_, policy := b.ProjectRefs[".template-spec/process/orchestration-contract.yaml"]
			if version == 2 && (roles || !policy) {
				t.Fatalf("new reading retained Roles or omitted policy: %v", b.ProjectRefs)
			}
			if version == 1 && !roles {
				t.Fatal("historical role dependency changed")
			}
			if len(b.Outputs) == 0 {
				t.Fatal("no managed output")
			}
			r, e := RenderReadingMode(s, ".work/demo/checkpoint.json", true)
			if e != nil || r.Build == nil {
				t.Fatalf("render: %v %+v", e, r)
			}
		})
	}
}
