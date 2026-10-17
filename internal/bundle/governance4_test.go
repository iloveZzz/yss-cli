package bundle

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestBundle4RequiresItsOwnCurrentGovernanceTuple(t *testing.T) {
	for _, scenario := range []string{"current", "old-identity", "old-profile", "wrong-profile", "missing-policy", "legacy-policy"} {
		t.Run(scenario, func(t *testing.T) {
			identity := map[string]any{"schema_version": 2, "repository_mode": "project-instance"}
			profile := map[string]any{"schema_version": 3, "profile_id": "harness.spec-template", "status": "active", "required_capabilities": []string{"role-independent-governance-v1"}}
			policy := map[string]any{"schema_version": 2, "governance_versions": map[string]any{"required_capability": "role-independent-governance-v1"}}
			switch scenario {
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
			if (err == nil) != (scenario == "current") {
				t.Fatalf("%s: %v", scenario, err)
			}
		})
	}
}
