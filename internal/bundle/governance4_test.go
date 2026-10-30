package bundle

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestBundle4RequiresItsOwnCurrentGovernanceTuple(t *testing.T) {
	for _, scenario := range []string{"current", "old-identity", "old-profile", "wrong-profile", "missing-policy", "legacy-policy", "stage-current", "stage-one-sided", "unknown-capability"} {
		t.Run(scenario, func(t *testing.T) {
			identity := map[string]any{"schema_version": 2, "repository_mode": "project-instance"}
			profile := map[string]any{"schema_version": 3, "profile_id": "harness.spec-template", "status": "active", "required_capabilities": []string{"role-independent-governance-v1"}}
			policy := map[string]any{"schema_version": 2, "governance_versions": map[string]any{"required_capability": "role-independent-governance-v1"}}
			switch scenario {
			case "stage-current":
				profile["required_capabilities"] = []string{"role-independent-governance-v1", "stage-gates-v1"}
				policy["stage_gates"] = map[string]any{"schema_version": 1, "capability": "stage-gates-v1"}
			case "stage-one-sided":
				profile["required_capabilities"] = []string{"role-independent-governance-v1", "stage-gates-v1"}
			case "unknown-capability":
				profile["required_capabilities"] = []string{"role-independent-governance-v1", "unknown-v1"}
			case "old-identity":
				identity["schema_version"] = 1
			case "old-profile":
				profile["schema_version"] = 2
			case "wrong-profile":
				profile["profile_id"] = "harness.backend-delivery"
			case "legacy-policy":
				delete(policy, "governance_versions")
			}
			files := map[string]File{}
			for ref, value := range map[string]any{"yss-project.yaml": identity, ".template-spec/process/harness-profile.yaml": profile, ".template-spec/process/orchestration-contract.yaml": policy} {
				raw, _ := json.Marshal(value)
				files[ref] = File{Data: base64.StdEncoding.EncodeToString(raw)}
			}
			if scenario == "missing-policy" {
				delete(files, ".template-spec/process/orchestration-contract.yaml")
			}
			err := validateGovernance4(files, "spec")
			if (err == nil) != (scenario == "current" || scenario == "stage-current") {
				t.Fatalf("%s: %v", scenario, err)
			}
		})
	}
}

func TestSpecWorkflowHasOwnPolicyWithoutStageKernel(t *testing.T) {
	files := map[string]File{}
	for ref, value := range map[string]any{
		"yss-project.yaml": map[string]any{"schema_version": 2, "repository_mode": "project-instance"},
		".template-spec/process/harness-profile.yaml": map[string]any{"schema_version": 3, "profile_id": "harness.spec-template", "status": "active", "spec_workflow": "matt-spec-v1"},
		".template-spec/process/spec-workflow.json":   map[string]any{"schemaVersion": 1, "specWorkflow": "matt-spec-v1", "profile": "spec"},
	} {
		raw, _ := json.Marshal(value)
		files[ref] = encodedFile(raw, 0644, "managed")
	}
	if err := validateGovernance4(files, "spec"); err != nil {
		t.Fatal(err)
	}
	delete(files, ".template-spec/process/spec-workflow.json")
	if err := validateGovernance4(files, "spec"); err == nil {
		t.Fatal("missing source policy accepted")
	}
}
