package project

import (
	"encoding/base64"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"os"
	"path/filepath"
	"testing"
)

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
	b := installationFixture()
	raw := []byte("{\"schemaVersion\":1,\"specWorkflow\":\"matt-spec-v1\",\"profile\":\"spec\"}\n")
	file := bundle.File{Data: base64.StdEncoding.EncodeToString(raw), Digest: safefs.Digest(raw), Mode: 0644, Ownership: "managed"}
	b.Files[SpecWorkflowRef] = file
	inst := b.Installations["governed"]
	inst.Initial[SpecWorkflowRef] = file
	for ref, body := range map[string]string{"yss-project.yaml": "schema_version: 2\nrepository_mode: project-instance\n", ".template-spec/process/harness-profile.yaml": "schema_version: 3\nprofile_id: harness.spec-template\n"} {
		f := bundle.File{Data: base64.StdEncoding.EncodeToString([]byte(body)), Digest: safefs.Digest([]byte(body)), Mode: 0644, Ownership: "managed"}
		b.Files[ref] = f
		inst.Initial[ref] = f
	}
	inst.Distribution["specWorkflow"] = "matt-spec-v1"
	b.Installations["governed"] = inst
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
