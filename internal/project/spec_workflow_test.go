package project

import (
	"errors"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"os"
	"path/filepath"
	"testing"
)

func TestSpecAttachPreservesOrdinaryContext(t *testing.T) {
	for _, tc := range []struct {
		name, profile, workflow, identity, code string
		native                                  bool
	}{
		{name: "new-spec", profile: "spec", workflow: "matt-spec-v1"},
		{name: "legacy-spec", profile: "spec", code: "CONTEXT"},
		{name: "backend", profile: "backend", code: "CONTEXT"},
		{name: "unknown-workflow", profile: "spec", workflow: "unknown", code: "CONTEXT"},
		{name: "unknown-identity", profile: "spec", workflow: "matt-spec-v1", identity: "schema_version: 999\nrepository_mode: project-instance\n", code: "IDENTITY"},
		{name: "native-instance", profile: "spec", workflow: "matt-spec-v1", native: true, code: "SYNC_REQUIRED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := installationFixture()
			b.Profile = tc.profile
			item := b.Installations["governed"]
			if tc.workflow != "" {
				item.Distribution["specWorkflow"] = tc.workflow
			}
			root := t.TempDir()
			if tc.native {
				b = trustedBundle(t, "spec")
				root, _ = writeInstallationBundleFixture(t, b)
			}
			body := []byte("# Existing business context\nProject-owned terms and decisions.\n")
			if err := os.WriteFile(filepath.Join(root, "CONTEXT.md"), body, 0444); err != nil {
				t.Fatal(err)
			}
			if tc.identity != "" {
				if err := os.WriteFile(filepath.Join(root, "yss-project.yaml"), []byte(tc.identity), 0644); err != nil {
					t.Fatal(err)
				}
			}
			before, err := safefs.Describe(root, "CONTEXT.md")
			if err != nil {
				t.Fatal(err)
			}
			p, err := BuildWithOptions(root, tc.profile, "attach", nil, nil, nil, PlanningOptions{target: b})
			if tc.code == "" {
				if err != nil {
					t.Fatalf("new Spec attach requires legacy Context: %v", err)
				}
				for _, change := range p.Changes {
					if change.Path == "CONTEXT.md" {
						t.Fatal("attach planned to modify the user's Context")
					}
				}
			} else {
				var rejection *domain.Error
				if !errors.As(err, &rejection) || rejection.Code != tc.code {
					t.Fatalf("want %s, got %v", tc.code, err)
				}
			}
			if after, err := safefs.Describe(root, "CONTEXT.md"); err != nil || after != before {
				t.Fatalf("user Context bytes or mode changed: %v", err)
			}
		})
	}
}

func TestSpecWorkflowProvenanceRequired(t *testing.T) {
	root := t.TempDir()
	ref := ".template-spec/process/spec-workflow.json"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, ref)), 0755); err != nil {
		t.Fatal(err)
	}
	raw := []byte("{\"specWorkflow\":\"matt-spec-v1\"}\n")
	if err := os.WriteFile(filepath.Join(root, ref), raw, 0644); err != nil {
		t.Fatal(err)
	}
	desc, err := safefs.Describe(root, ref)
	if err != nil {
		t.Fatal(err)
	}
	id := &Identity{Root: root, Profile: domain.Profiles["spec"], Native: &Metadata{SchemaVersion: 5, Profile: "spec", Distribution: map[string]any{"installation": "governed", "specWorkflow": "matt-spec-v1"}, Managed: map[string]Managed{ref: {Baseline: desc, Applied: desc, Source: &identitymeta.BaselineSource{Kind: "bundle", Path: ref, TemplateDigest: desc.Digest}}}}}
	b := &bundle.Bundle{Profile: "spec", Distribution: map[string]any{"specWorkflow": "matt-spec-v1"}, Files: map[string]bundle.File{ref: {Digest: safefs.Digest(raw)}}}
	if err := ValidateSpecWorkflow(id, b); err != nil {
		t.Fatal(err)
	}
	good := id.Native.Managed[ref]
	unknown := good
	unknown.Source = &identitymeta.BaselineSource{Kind: "unavailable"}
	id.Native.Managed[ref] = unknown
	if err := ValidateSpecWorkflow(id, b); err == nil {
		t.Fatal("unavailable source accepted for new workflow")
	}
	id.Native.Managed[ref] = good
	delete(id.Native.Managed, ref)
	if err := ValidateSpecWorkflow(id, b); err == nil {
		t.Fatal("hand-written marker without managed policy accepted")
	}
	id.Native.Managed[ref] = Managed{Baseline: desc, Applied: desc, Source: &identitymeta.BaselineSource{Kind: "bundle", Path: ref, TemplateDigest: desc.Digest}}
	if err := os.WriteFile(filepath.Join(root, ref), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSpecWorkflow(id, b); err == nil {
		t.Fatal("changed workflow policy accepted")
	}
	if err := ValidateSpecWorkflow(&Identity{Profile: domain.Profiles["backend"]}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSpecGovernedWithoutProductAssets(t *testing.T) {
	b := trustedBundle(t, "spec")
	root := t.TempDir()
	p, err := buildCore(root, "spec", "init", nil, nil, nil, PlanningOptions{target: b})
	if err != nil {
		t.Fatal(err)
	}
	ops, err := nativePlanOperations(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Apply(root, "init", ops); err != nil {
		t.Fatal(err)
	}

	id, err := Detect(root, "spec", false)
	if err != nil {
		t.Fatal(err)
	}
	view, err := b.ForInstallation("governed")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSpecWorkflow(id, view); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "CONTEXT.md")); !os.IsNotExist(err) {
		t.Fatal("unexpected Context")
	}
}
