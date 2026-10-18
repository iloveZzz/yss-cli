package kernel

import (
	"context"
	"testing"
)

func TestTask3SelectsChecksFromBoundImpact(t *testing.T) {
	s := NewSemanticSession(context.Background(), t.TempDir(), nil)
	s.GovernanceVersion = 2
	s.GovernanceDigest = "policy"
	s.Report.Profile = "backend"
	s.Registry = map[string]any{"work_units": []any{map[string]any{"id": "work-unit.technical-design"}}, "checks": []any{map[string]any{"id": "check.design-reviewed"}}}
	s.GovernancePolicy = map[string]any{"work_unit_routes": map[string]any{"work-unit.technical-design": map[string]any{"primary_skill": "yss-technical-design", "supporting_skills": []string{}, "check_ids": []string{"check.design-reviewed"}, "impact_check_ids": map[string]any{"backend": []string{}, "api": []string{"check.design-reviewed"}}}}, "technical_checks": []any{map[string]any{"id": "capability.design", "profiles": []string{"backend"}, "review_skills": []string{"code-review"}}}, "gate_policy": map[string]any{"profiles": map[string]any{"backend": map[string]any{"check_reviews": []any{map[string]any{"gate": "check.design-reviewed", "capability_ids": []string{"capability.design"}}}}}}}
	// This exercises the production task skill-source entry, with no Roles or actor registry.
	task := map[string]any{"schema_version": 3, "work_unit_id": "work-unit.technical-design", "skill_source": map[string]any{"policy_ref": ".template-spec/process/orchestration-contract.yaml", "policy_digest": "policy", "work_unit_id": "work-unit.technical-design", "impacts": []string{"backend"}, "skills": []string{"yss-technical-design", "code-review"}, "check_ids": []string{"check.design-reviewed"}}}
	if err := ValidateTaskSkillSourceSemantic(s, task, true); err != nil {
		t.Fatal(err)
	}
	ApMap(task["skill_source"])["check_ids"] = []string{}
	if err := ValidateTaskSkillSourceSemantic(s, task, true); err == nil {
		t.Fatal("caller removed mandatory checks")
	}
	ApMap(task["skill_source"])["impacts"] = []string{"unknown"}
	if err := ValidateTaskSkillSourceSemantic(s, task, true); err == nil {
		t.Fatal("unknown impact classified as low risk")
	}
}
