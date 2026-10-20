package kernel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSpecStageGatesRequireMatchingCapability(t *testing.T) {
	root, p := policyFixture(t)
	profile := map[string]any{"schema_version": 3, "profile_id": "harness.spec-template", "status": "active", "required_capabilities": []string{RoleIndependentCapability, "stage-gates-v1"}, "lifecycle": map[string]any{}, "execution": map[string]any{}}
	writePolicyFixture(t, root, ".template-spec/process/harness-profile.yaml", profile)
	stage := map[string]any{"schema_version": 1, "capability": "stage-gates-v1", "gates": []any{}}
	for _, id := range []string{"gate.plan-approved", "gate.spec-baseline-approved", "gate.implementation-admitted", "gate.delivery-accepted"} {
		stage["gates"] = append(ApArray(stage["gates"]), map[string]any{"id": id, "work_stages": []string{"stage.plan"}, "checks": []string{}, "decision_mode": "user-or-continuation"})
	}
	p["stage_gates"] = stage
	registry := map[string]any{"schema_version": 1, "status": "active", "gates": stage["gates"], "checks": []any{}, "work_units": []any{}}
	writePolicyFixture(t, root, ".template-spec/process/lifecycle-registry.yaml", registry)
	writePolicyFixture(t, root, ".template-spec/process/orchestration-contract.yaml", p)
	current := NewSemanticSession(context.Background(), root, nil)
	if err := current.Authorities(); err != nil {
		t.Fatal(err)
	}
	if !current.StageGatesEnabled() {
		t.Fatal("explicit matching stage policy was not selected")
	}
	// This test exercises protocol selection before ordinary legacy gate policy.
	for _, scenario := range []string{"missing-profile", "missing-policy", "unknown-version", "backend"} {
		t.Run(scenario, func(t *testing.T) {
			candidate := ApCopy(p)
			prof := ApCopy(profile)
			switch scenario {
			case "missing-profile":
				prof["required_capabilities"] = []string{RoleIndependentCapability}
			case "missing-policy":
				delete(candidate, "stage_gates")
			case "unknown-version":
				ApMap(candidate["stage_gates"])["schema_version"] = 99
			case "backend":
				prof["profile_id"] = "harness.backend-delivery"
			}
			writePolicyFixture(t, root, ".template-spec/process/harness-profile.yaml", prof)
			writePolicyFixture(t, root, ".template-spec/process/orchestration-contract.yaml", candidate)
			s := NewSemanticSession(context.Background(), root, nil)
			if err := s.Authorities(); err == nil {
				t.Fatal("incomplete stage policy was accepted")
			}
		})
	}
	s := NewSemanticSession(context.Background(), root, nil)
	if s.StageGatesEnabled() {
		t.Fatal("unverified policy must not select stage gates")
	}
}

func TestStageProgressionLocationSelectsCheckpoint2OnlyWithStagePolicy(t *testing.T) {
	for _, row := range []struct {
		stage   bool
		version int
		pass    bool
	}{{true, 2, true}, {false, 2, false}, {true, 1, false}, {false, 1, true}} {
		root := t.TempDir()
		s := NewSemanticSession(context.Background(), root, nil)
		if row.stage {
			s.GovernanceVersion = 2
			s.Report.Profile = "spec"
			s.GovernancePolicy = map[string]any{"stage_gates": map[string]any{"capability": StageGatesCapability}}
		}
		writePolicyFixture(t, root, ".work/demo/checkpoint.json", map[string]any{"schema_version": row.version, "repository_mode": "project-instance", "feature_id": "feature.demo"})
		writePolicyFixture(t, root, ".template-spec/agents/issue-tracker.md", map[string]any{"tracker": map[string]any{"platform": "local-markdown", "root": ".work"}})
		if err := os.WriteFile(filepath.Join(root, ".template-spec/agents/issue-tracker.md"), []byte("---\ntracker:\n  platform: local-markdown\n  root: .work\n---\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".work/demo/map.md"), []byte("---\ncheckpoint_ref: .work/demo/checkpoint.json\n---\n# Demo\n"), 0600); err != nil {
			t.Fatal(err)
		}
		_, _, e := ProgressionLocation(s, ".work/demo/checkpoint.json")
		if (e == nil) != row.pass {
			t.Fatalf("stage=%v checkpoint=%d: %v", row.stage, row.version, e)
		}
	}
}
