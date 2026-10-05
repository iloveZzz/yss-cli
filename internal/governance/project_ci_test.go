package governance_test

import (
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/governance"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectCIRequiresExplicitNativeScopeAndBindsReferences(t *testing.T) {
	root := stageFixture(t)
	applyStagePlan(t, root, "register", "seeds.json")
	if _, err := governance.Run("project-ci", "check", root, nil); err == nil {
		t.Fatal("partial checks masqueraded as full gate")
	}
	out, err := governance.Run("project-ci", "check", root, map[string]string{"scope": "native-go"})
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["full_project_governance"] != false || m["status"] != "scoped-passed" {
		t.Fatalf("unexpected scope: %#v", m)
	}
	put(t, root, "docs/.scratch/report/asset.json", `{"status":"approved","file_ref":"missing.md"}`)
	if _, err = governance.Run("project-ci", "check", root, map[string]string{"scope": "native-go"}); err == nil {
		t.Fatal("unreadable claimed reference accepted")
	}
}

func TestProjectCINativeInstallPlansTransactionAndPreservesManualWorkflow(t *testing.T) {
	root := stageFixture(t)
	put(t, root, "tools/yss-cli/go.mod", "module github.com/iloveZzz/yss-cli\n\ngo 1.27.1\n")
	put(t, root, "tools/yss-cli/cmd/yss/main.go", "package main\nfunc main() {}\n")
	args := map[string]string{"scope": "native-go", "cli-source": "tools/yss-cli", "branch": "main"}
	plan, err := governance.Run("project-ci", "install", root, args)
	if err != nil {
		t.Fatal(err)
	}
	ref := ".github/workflows/yss-governance-native.yml"
	if _, err = os.Stat(filepath.Join(root, ref)); !os.IsNotExist(err) {
		t.Fatal("install plan wrote workflow")
	}
	b, _ := json.Marshal(plan)
	put(t, root, "ci-plan.json", string(b))
	if _, err = governance.Run("project-ci", "install", root, map[string]string{"scope": "native-go", "apply": "true", "plan-file": "ci-plan.json"}); err != nil {
		t.Fatal(err)
	}
	workflow, _ := os.ReadFile(filepath.Join(root, ref))
	if !strings.Contains(string(workflow), "run ./cmd/yss project-ci") || strings.Contains(string(workflow), "yss-spec") || !strings.Contains(string(workflow), "--scope native-go") || strings.Contains(string(workflow), "setup-node") || strings.Contains(string(workflow), "setup-python") {
		t.Fatal("native scoped workflow contains wrong runtime")
	}
	put(t, root, ref, string(workflow)+"# human edit\n")
	if _, err = governance.Run("project-ci", "install", root, args); err == nil {
		t.Fatal("manual edit overwritten")
	}
}

func TestProjectCIPlanBindsSourceAndInstalledAdditionalRoots(t *testing.T) {
	root := stageFixture(t)
	applyStagePlan(t, root, "register", "seeds.json")
	put(t, root, "tools/yss-cli/go.mod", "module github.com/iloveZzz/yss-cli\n\ngo 1.27.1\n")
	put(t, root, "tools/yss-cli/cmd/yss/main.go", "package main\nfunc main() {}\n")
	put(t, root, "tools/yss-cli/assets/data.txt", "embedded resource")
	args := map[string]string{"scope": "native-go", "cli-source": "tools/yss-cli", "additional-path": "extra"}
	p, err := governance.Run("project-ci", "install", root, args)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(p)
	put(t, root, "ci-plan.json", string(b))
	put(t, root, "tools/yss-cli/assets/data.txt", "changed embedded resource")
	if _, err = governance.Run("project-ci", "apply", root, map[string]string{"scope": "native-go", "plan-file": "ci-plan.json"}); err == nil {
		t.Fatal("source drift plan accepted")
	}
	p, err = governance.Run("project-ci", "install", root, args)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(p)
	put(t, root, "ci-plan.json", string(b))
	if _, err = governance.Run("project-ci", "apply", root, map[string]string{"scope": "native-go", "plan-file": "ci-plan.json"}); err != nil {
		t.Fatal(err)
	}
	put(t, root, "extra/asset.json", `{"ref":"missing.json","digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000"}`)
	if _, err = governance.Run("project-ci", "check", root, map[string]string{"scope": "native-go"}); err == nil {
		t.Fatal("installed additional discovery root ignored")
	}
}

func TestProjectCITransitionEvaluatesOnlyWorkConditionsAndLegacyRemains(t *testing.T) {
	root := stageFixture(t)
	cp := "docs/.scratch/report/checkpoint.json"
	put(t, root, "seeds.json", `[{"id":"requirements","title":"要求","stage":"stage.plan","work_unit":"work-unit.plan-requirements","owner":"需求经理","scope":"要求","acceptance":["明确范围"],"source_refs":["CONTEXT.md"]},{"id":"decision","title":"决定","stage":"stage.plan","work_unit":"work-unit.stage-decision","owner":"需求经理","scope":"决定","acceptance":["明确决定"],"source_refs":["CONTEXT.md"]}]`)
	applyStagePlan(t, root, "register", "seeds.json")
	args := map[string]string{"scope": "native-go", "checkpoint": cp, "current-work-unit": "work-unit.plan-requirements", "next-work-unit": "work-unit.stage-decision"}
	if _, err := governance.Run("project-ci", "transition", root, args); err == nil {
		t.Fatal("incomplete current work accepted")
	}
	put(t, root, "patch.json", `{"id":"requirements","progress":"cancelled","cancellation_reason":"当前范围由用户撤销"}`)
	applyStagePlan(t, root, "update", "patch.json")
	out, err := governance.Run("project-ci", "transition", root, args)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["transition_allowed"] != "not-evaluated" || m["full_project_governance"] != false || m["approval_created"] != false {
		t.Fatalf("transition invented authority: %#v", m)
	}
	args["next-work-unit"] = "work-unit.unknown"
	if _, err = governance.Run("project-ci", "transition", root, args); err == nil {
		t.Fatal("unknown next workunit accepted")
	}
	legacy := []byte("# old managed workflow\n")
	put(t, root, ".github/workflows/yss-governance.yml", string(legacy))
	if _, err = governance.Run("project-ci", "install", root, map[string]string{"scope": "native-go", "cli-source": "tools/yss-cli"}); err == nil {
		t.Fatal("legacy workflow replaced without full equivalence")
	}
	got, _ := os.ReadFile(filepath.Join(root, ".github/workflows/yss-governance.yml"))
	if string(got) != string(legacy) {
		t.Fatal("legacy workflow changed")
	}
}
