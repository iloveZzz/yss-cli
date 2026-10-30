package bundle

import "testing"

func TestSpecGovernedMinimalInstallation(t *testing.T) {
	b := &Bundle{Profile: "spec", Files: map[string]File{
		".agents/skills/yss-product-lifecycle/SKILL.md":                    encodedFile([]byte("life"), 0644, "managed"),
		".agents/skills/yss-product-lifecycle/references/spec-workflow.md": encodedFile([]byte("method"), 0644, "managed"),
		".template-spec/process/spec-workflow.json":                        encodedFile([]byte("policy"), 0644, "managed"),
		"AGENTS.md":       encodedFile([]byte("entry"), 0644, "managed"),
		"checkpoint.json": encodedFile([]byte("old"), 0644, "managed"),
	}}
	cfg := InstallationPolicy{SpecWorkflow: "matt-spec-v1", Skills: []string{"yss-product-lifecycle"}, Files: []string{"AGENTS.md", ".template-spec/process/spec-workflow.json"}}
	got, err := selectSpecGoverned(b, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Distribution["installation"] != "governed" || got.Distribution["specWorkflow"] != "matt-spec-v1" {
		t.Fatal("wrong workflow")
	}
	if len(got.Initial) != 4 {
		t.Fatalf("wrong initial resources: %v", got.Initial)
	}
	if _, ok := got.Initial["checkpoint.json"]; ok {
		t.Fatal("old stage asset installed")
	}
	cfg.Skills = []string{"missing"}
	if _, err := selectSpecGoverned(b, cfg); err == nil {
		t.Fatal("missing entry accepted")
	}
	cfg.SpecWorkflow = "unknown"
	if _, err := selectSpecGoverned(b, cfg); err == nil {
		t.Fatal("unknown workflow accepted")
	}
	b.Profile = "backend"
	cfg.SpecWorkflow = "matt-spec-v1"
	if _, err := selectSpecGoverned(b, cfg); err == nil {
		t.Fatal("Spec workflow applied to Backend")
	}
}

func TestSpecGovernedCatalogExcludesImplementation(t *testing.T) {
	life := ".agents/skills/yss-product-lifecycle/SKILL.md"
	research := ".agents/skills/yss-research/SKILL.md"
	impl := ".agents/skills/implement/SKILL.md"
	f := encodedFile([]byte("skill"), 0644, "managed")
	b := &Bundle{SchemaVersion: 5, Profile: "spec", SkillRequirements: map[string]Requirement{"yss-research": {Paths: []string{"old-stage.yaml"}}}, Files: map[string]File{life: f, research: f, impl: f, "old-stage.yaml": f}, Installations: map[string]Installation{
		"governed": {Initial: map[string]File{life: f}, Distribution: map[string]any{"installation": "governed", "specWorkflow": "matt-spec-v1"}, SkillReferences: map[string][]string{"yss-research": {}}},
	}}
	view, err := b.ForInstallation("governed")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := view.Files[research]; !ok {
		t.Fatal("optional research unavailable")
	}
	if len(view.SkillRequirements["yss-research"].Paths) != 0 {
		t.Fatal("optional skill kept old stage requirement")
	}
	if _, ok := view.Files[impl]; ok {
		t.Fatal("implementation leaked into Spec --full/ensure catalog")
	}
	if _, ok := view.Files["old-stage.yaml"]; ok {
		t.Fatal("old stage leaked into catalog")
	}
	if _, ok := b.Files[impl]; !ok {
		t.Fatal("source bundle mutated")
	}
}
