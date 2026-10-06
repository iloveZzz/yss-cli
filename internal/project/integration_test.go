package project

import (
	"encoding/base64"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"os"
	"path/filepath"
	"testing"
)

func freshRoot(t *testing.T) string {
	t.Helper()
	p, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return filepath.Join(p, "project")
}
func TestFourProfilesInitRepeatSyncAndWholeRollback(t *testing.T) {
	for _, name := range []string{"spec", "design", "backend", "frontend"} {
		t.Run(name, func(t *testing.T) {
			root := freshRoot(t)
			p, e := Build(root, name, "init", map[string]string{"projectName": "治理项目", "businessDomain": "数据填报"}, nil)
			if e != nil {
				t.Fatal(e)
			}
			if len(p.Conflicts) > 0 || len(p.Changes) < 10 {
				t.Fatalf("unexpected init plan")
			}
			if _, e = Apply(p); e != nil {
				t.Fatal(e)
			}
			id, e := Detect(root, "", false)
			if e != nil {
				t.Fatal(e)
			}
			if id.Profile.Name != name || id.Native.CLIVersion != domain.Version || id.Native.TemplateVersion != func() string {
				b, e := bundle.Load(name)
				if e != nil {
					t.Fatal(e)
				}
				if b.SchemaVersion == 2 {
					return "git:" + b.TemplateCommit
				}
				return domain.Profiles[name].LegacyVersion
			}() {
				t.Fatalf("version/profile separation failed: %+v", id.Native)
			}
			diff, e := Build(root, "", "diff", nil, nil)
			if e != nil {
				t.Fatal(e)
			}
			if len(diff.Changes) != 0 || len(diff.Conflicts) != 0 {
				t.Fatalf("init has drift: changes=%d conflicts=%v", len(diff.Changes), diff.Conflicts)
			}
			sync, e := Build(root, "", "sync", nil, nil)
			if e != nil {
				t.Fatal(e)
			}
			if len(sync.Changes) != 0 {
				t.Fatalf("repeat sync is not idempotent: %d", len(sync.Changes))
			}
			if _, e = transaction.Rollback(root); e != nil {
				t.Fatal(e)
			}
			if _, e = os.Stat(filepath.Join(root, MetadataFile)); !os.IsNotExist(e) {
				t.Fatal("whole rollback retained native metadata")
			}
		})
	}
}
func TestAttachPlanProtectsBusinessCustomContextAndIndex(t *testing.T) {
	root := freshRoot(t)
	if e := os.MkdirAll(filepath.Join(root, "src"), 0755); e != nil {
		t.Fatal(e)
	}
	_ = os.Mkdir(filepath.Join(root, ".git"), 0755)
	index := []byte("staged-business-index")
	business := []byte("export const value=1")
	b, e := bundle.Load("spec")
	if e != nil {
		t.Fatal(e)
	}
	context, e := b.Initial["CONTEXT.md"].Render(map[string]string{})
	if e != nil {
		t.Fatal(e)
	}
	context = append(context, []byte("\n用户自定义备注：必须保留。\n")...)
	_ = os.WriteFile(filepath.Join(root, ".git/index"), index, 0644)
	_ = os.WriteFile(filepath.Join(root, "src/app.js"), business, 0644)
	_ = os.WriteFile(filepath.Join(root, "CONTEXT.md"), context, 0644)
	p, e := Build(root, "spec", "attach", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Apply(p); e != nil {
		t.Fatal(e)
	}
	for ref, want := range map[string][]byte{".git/index": index, "src/app.js": business, "CONTEXT.md": context} {
		got, e := os.ReadFile(filepath.Join(root, ref))
		if e != nil || string(got) != string(want) {
			t.Fatalf("protected bytes changed: %s", ref)
		}
	}
}
func TestApplyRejectsDriftAndRehashedArbitraryPlan(t *testing.T) {
	root := freshRoot(t)
	p, e := Build(root, "spec", "init", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	data := []byte("overwrite business")
	p.Changes = append(p.Changes, Change{"src/business.go", domain.Descriptor{Type: "missing"}, domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: 0644}, base64.StdEncoding.EncodeToString(data), "managed"})
	p.Digest = planDigest(p)
	if _, e = Apply(p); e == nil {
		t.Fatal("edited/rehashed plan accepted")
	}
	if _, e = os.Stat(root); !os.IsNotExist(e) {
		t.Fatal("rejected plan mutated project")
	}
}
func TestIdentityRejectsMultipleProfilesAndUnknownSchema(t *testing.T) {
	root := freshRoot(t)
	_ = os.MkdirAll(root, 0755)
	_ = os.WriteFile(filepath.Join(root, ".yss-template.json"), []byte("{}"), 0644)
	_ = os.WriteFile(filepath.Join(root, ".yss-harness-backend.json"), []byte("{}"), 0644)
	if _, e := Detect(root, "spec", false); e == nil {
		t.Fatal("multiple family metadata accepted")
	}
	_ = os.Remove(filepath.Join(root, ".yss-template.json"))
	_ = os.Remove(filepath.Join(root, ".yss-harness-backend.json"))
	b, _ := json.Marshal(Metadata{SchemaVersion: 99, Profile: "spec"})
	_ = os.WriteFile(filepath.Join(root, MetadataFile), b, 0644)
	if _, e := Detect(root, "", false); e == nil {
		t.Fatal("unknown native metadata schema accepted")
	}
}
