package kernel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writePolicyFixture(t *testing.T, root, ref string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, ref)
	if err = os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(name, raw, 0644); err != nil {
		t.Fatal(err)
	}
}

func policyFixture(t *testing.T) (string, map[string]any) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writePolicyFixture(t, root, "yss-project.yaml", map[string]any{"schema_version": 2, "repository_mode": "project-instance"})
	writePolicyFixture(t, root, ".template-spec/process/harness-profile.yaml", map[string]any{"schema_version": 3, "profile_id": "harness.backend-delivery", "status": "active", "required_capabilities": []string{"role-independent-governance-v1"}, "lifecycle": map[string]any{}, "execution": map[string]any{}})
	registry := map[string]any{"schema_version": 1, "status": "active", "gates": []any{map[string]any{"id": "gate.spec-baseline-approved"}, map[string]any{"id": "gate.plan-approved"}}, "checks": []any{map[string]any{"id": "check.design-reviewed"}}, "work_units": []any{map[string]any{"id": "work-unit.technical-design"}, map[string]any{"id": "work-unit.slice-implementation"}, map[string]any{"id": "work-unit.implementation-repository-preparation"}, map[string]any{"id": "work-unit.spec-synthesis"}}}
	writePolicyFixture(t, root, ".template-spec/process/lifecycle-registry.yaml", registry)
	writePolicyFixture(t, root, ".template-spec/process/schemas/lifecycle-registry.schema.json", map[string]any{"type": "object"})
	versions := map[string]any{"required_capability": "role-independent-governance-v1", "activation": "native-consumers-and-bundle-closure-required-before-instance-writes", "legacy_policy": "read-verify-explicit-migrate-only-no-new-task-or-stage-write"}
	for key, version := range map[string]int{"project_identity": 2, "harness_profile": 3, "task": 3, "approval_record": 3, "review_bundle": 3, "continuation": 2, "delivery_authorization": 2, "daily": 2, "standard": 2, "user_decision": 1, "lifecycle_registry": 1, "native_bundle": 4, "native_metadata": 4, "source_lock": 5, "slice_implementation_contract": 4, "workflow_execution_result": 1} {
		versions[key] = map[string]any{"current": version}
	}
	gates := map[string]any{"automatic_checks": []any{}, "check_reviews": []any{map[string]any{"gate": "check.design-reviewed", "capability_ids": []string{"capability.technical-design"}}}, "technical_reviews": []any{map[string]any{"gate": "gate.spec-baseline-approved", "capability_ids": []string{"capability.technical-design"}}, map[string]any{"gate": "gate.plan-approved", "capability_ids": []string{"capability.technical-design"}}}, "user_decision_gates": []any{}, "contract_approval_gates": []any{}, "evidence_only": []any{}, "work_unit_reviews": []any{map[string]any{"work_unit": "work-unit.technical-design", "capability_ids": []string{"capability.technical-design"}}}}
	policy := map[string]any{"schema_version": 2, "governance_versions": versions, "gate_policy": map[string]any{"default_if_unlisted": "reject-unlisted", "profiles": map[string]any{"backend": gates}}, "technical_checks": []any{map[string]any{"id": "capability.technical-design", "profiles": []string{"backend"}, "review_skills": []string{"code-review"}}}, "user_decision_policy": map[string]any{"schema_version": 2, "source_kinds": []string{"session-export"}, "profile_scope": map[string]any{"backend": map[string]any{"gates": []string{"gate.spec-baseline-approved", "gate.plan-approved"}, "work_units": map[string]any{"work-unit.slice-implementation": "implementation-scope", "work-unit.implementation-repository-preparation": "scaffold-choice"}, "work_unit_gates": map[string]any{"work-unit.spec-synthesis": []string{"gate.spec-baseline-approved"}}}}, "continuation": map[string]any{"schema_version": 2, "kind": "approved-scope-continuation-v2", "boundaries_by_profile": map[string]any{"backend": []string{"gate.spec-baseline-approved", "gate.plan-approved", "implementation-scope"}}}}, "review_modes": map[string]any{"default": "self-check"}, "external_rules_policy": map[string]any{"states": map[string]any{"none-known": "proceed", "known-verifiable": "verify", "known-unverifiable": "block"}}}
	policy["work_unit_routes"] = map[string]any{"work-unit.spec-synthesis": map[string]any{"human_gate": "spec-baseline"}}
	writePolicyFixture(t, root, ".template-spec/process/orchestration-contract.yaml", policy)
	return root, policy
}

func TestRoleIndependentAuthoritiesWithoutRegistryOfActors(t *testing.T) {
	root, _ := policyFixture(t)
	s := NewSemanticSession(context.Background(), root, nil)
	if err := s.Authorities(); err != nil {
		t.Fatal(err)
	}
	if err := s.RequireGovernanceWrite(); SemanticCode(err) != "CAPABILITY" {
		t.Fatalf("partial consumer must not activate new writes: %v", err)
	}
	if len(s.Roles) != 0 {
		t.Fatal("new authority must not load roles")
	}
	if err := ProjectIdentity(s.V); err != nil {
		t.Fatal(err)
	}
}

func TestRoleIndependentAuthorityRejectsMixedAndMissingPolicy(t *testing.T) {
	for _, scenario := range []string{"missing-policy", "unknown-manifest", "mixed-task", "wrong-profile", "missing-user-policy", "foreign-gate"} {
		t.Run(scenario, func(t *testing.T) {
			root, p := policyFixture(t)
			switch scenario {
			case "missing-policy":
				if err := os.Remove(filepath.Join(root, ".template-spec/process/orchestration-contract.yaml")); err != nil {
					t.Fatal(err)
				}
			case "unknown-manifest":
				p["schema_version"] = 99
			case "mixed-task":
				p["governance_versions"].(map[string]any)["task"] = map[string]any{"current": 1}
			case "wrong-profile":
				writePolicyFixture(t, root, ".template-spec/process/harness-profile.yaml", map[string]any{"schema_version": 2, "profile_id": "harness.backend-delivery"})
			case "missing-user-policy":
				delete(p, "user_decision_policy")
			case "foreign-gate":
				p["gate_policy"].(map[string]any)["profiles"].(map[string]any)["backend"].(map[string]any)["user_decision_gates"] = []string{"gate.delivery-accepted"}
			}
			if scenario != "missing-policy" {
				writePolicyFixture(t, root, ".template-spec/process/orchestration-contract.yaml", p)
			}
			if err := NewSemanticSession(context.Background(), root, nil).Authorities(); err == nil {
				t.Fatal("accepted incomplete or mixed authorities")
			}
		})
	}
}

func TestLegacyAuthoritiesReadButCannotWriteAndNewConsumersFailClosed(t *testing.T) {
	root, _ := policyFixture(t)
	writePolicyFixture(t, root, "yss-project.yaml", map[string]any{"schema_version": 1, "repository_mode": "project-instance"})
	writePolicyFixture(t, root, ".template-spec/process/harness-profile.yaml", map[string]any{"schema_version": 2, "profile_id": "harness.backend-delivery"})
	writePolicyFixture(t, root, ".template-spec/process/orchestration-contract.yaml", map[string]any{"schema_version": 1})
	writePolicyFixture(t, root, ".template-spec/agents/digital-human-roles.yaml", map[string]any{"schema_version": 1, "status": "active", "user_decision_policy": map[string]any{"schema_version": 1, "gates": []any{}, "work_units": map[string]any{}}, "gate_policy": map[string]any{"default_if_unlisted": "reject-unlisted"}})
	s := NewSemanticSession(context.Background(), root, nil)
	if err := s.Authorities(); err != nil {
		t.Fatal(err)
	}
	if len(s.Roles) == 0 {
		t.Fatal("legacy source role policy was not loaded")
	}
	if err := s.RequireGovernanceWrite(); SemanticCode(err) != "GOVERNANCE_MIGRATION_REQUIRED" {
		t.Fatalf("legacy write: %v", err)
	}
	root, _ = policyFixture(t)
	s = NewSemanticSession(context.Background(), root, nil)
	if err := s.Authorities(); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify("task", "task.yaml", nil); SemanticCode(err) != "CAPABILITY" {
		t.Fatalf("unimplemented consumer: %v", err)
	}
	if s.GatePolicy()["technical_reviews"] == nil || ApContains(s.GatePolicy()["user_decision_gates"], "gate.delivery-accepted") {
		t.Fatal("profile gate selection failed")
	}
}

func TestRoleIndependentPolicyCompleteness(t *testing.T) {
	for _, scenario := range []string{"empty-gates", "missing-check", "missing-gate", "empty-user-gates", "missing-work-unit", "empty-work-unit-gates", "missing-continuation"} {
		t.Run(scenario, func(t *testing.T) {
			root, p := policyFixture(t)
			gates := ApMap(ApMap(ApMap(p["gate_policy"])["profiles"])["backend"])
			decisions := ApMap(p["user_decision_policy"])
			scope := ApMap(ApMap(decisions["profile_scope"])["backend"])
			switch scenario {
			case "empty-gates":
				for k := range gates {
					gates[k] = []any{}
				}
				scope["gates"] = []any{}
				scope["work_units"] = map[string]any{}
				scope["work_unit_gates"] = map[string]any{}
			case "missing-check":
				gates["check_reviews"] = []any{}
			case "missing-gate":
				gates["technical_reviews"] = []any{}
			case "empty-user-gates":
				scope["gates"] = []any{}
			case "missing-work-unit":
				delete(ApMap(scope["work_units"]), "work-unit.slice-implementation")
			case "empty-work-unit-gates":
				scope["work_unit_gates"] = map[string]any{}
			case "missing-continuation":
				ApMap(ApMap(decisions["continuation"])["boundaries_by_profile"])["backend"] = []string{"implementation-scope"}
			}
			writePolicyFixture(t, root, ".template-spec/process/orchestration-contract.yaml", p)
			if err := NewSemanticSession(context.Background(), root, nil).Authorities(); SemanticCode(err) != "GOVERNANCE_POLICY" {
				t.Fatalf("incomplete policy accepted: %v", err)
			}
		})
	}
}

func TestPolicyRoutesCannotLoseUserDecisionMappings(t *testing.T) {
	for _, scenario := range []string{"synchronized-removal", "unknown-human-gate"} {
		t.Run(scenario, func(t *testing.T) {
			root, p := policyFixture(t)
			decisions := ApMap(p["user_decision_policy"])
			scope := ApMap(ApMap(decisions["profile_scope"])["backend"])
			if scenario == "synchronized-removal" {
				scope["gates"] = []string{"gate.plan-approved"}
				ApMap(ApMap(decisions["continuation"])["boundaries_by_profile"])["backend"] = []string{"gate.plan-approved", "implementation-scope"}
				delete(ApMap(scope["work_unit_gates"]), "work-unit.spec-synthesis")
			} else {
				ApMap(ApMap(p["work_unit_routes"])["work-unit.spec-synthesis"])["human_gate"] = "gate.unknown"
				delete(ApMap(scope["work_unit_gates"]), "work-unit.spec-synthesis")
			}
			writePolicyFixture(t, root, ".template-spec/process/orchestration-contract.yaml", p)
			if err := NewSemanticSession(context.Background(), root, nil).Authorities(); SemanticCode(err) != "GOVERNANCE_POLICY" {
				t.Fatalf("route user gate disappeared from policy: %v", err)
			}
		})
	}
}

func TestPolicyAggregateHandoffRequiresExplicitSourceApprovalRelation(t *testing.T) {
	for _, scenario := range []string{"valid", "missing-source-approval", "wrong-technical-classification", "unknown-completion-relation"} {
		t.Run(scenario, func(t *testing.T) {
			root, p := policyFixture(t)
			session := NewSemanticSession(context.Background(), root, nil)
			registry, err := session.Doc(".template-spec/process/lifecycle-registry.yaml")
			if err != nil {
				t.Fatal(err)
			}
			handoffGate := map[string]any{"id": "gate.handoff-approved", "requires_gates": []string{"gate.spec-baseline-approved", "gate.plan-approved"}}
			registry["gates"] = append(ApArray(registry["gates"]), handoffGate)
			registry["work_units"] = append(ApArray(registry["work_units"]), map[string]any{"id": "work-unit.handoff"})
			ApMap(p["work_unit_routes"])["work-unit.handoff"] = map[string]any{"human_gate": "gate.handoff-approved"}
			relation := map[string]any{"work_unit": "work-unit.handoff", "gate": "gate.handoff-approved", "source_authority": "original-profile-and-current-approved-assets", "completion_requires": "current-source-approvals-and-immutable-package-verification"}
			p["progression_target"] = map[string]any{"external_strategic_handoff": relation}
			classes := ApMap(ApMap(ApMap(p["gate_policy"])["profiles"])["backend"])
			if scenario == "wrong-technical-classification" {
				classes["evidence_only"] = []string{"gate.handoff-approved"}
			} else {
				classes["technical_reviews"] = append(ApArray(classes["technical_reviews"]), map[string]any{"gate": "gate.handoff-approved", "capability_ids": []string{"capability.technical-design"}})
			}
			if scenario == "missing-source-approval" {
				handoffGate["requires_gates"] = []string{"gate.handoff-approved"}
			}
			if scenario == "unknown-completion-relation" {
				relation["completion_requires"] = "self-declared-complete"
			}
			writePolicyFixture(t, root, ".template-spec/process/lifecycle-registry.yaml", registry)
			writePolicyFixture(t, root, ".template-spec/process/orchestration-contract.yaml", p)
			err = NewSemanticSession(context.Background(), root, nil).Authorities()
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("%s: %v", scenario, err)
			}
		})
	}
}
