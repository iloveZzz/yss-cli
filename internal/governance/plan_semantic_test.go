package governance

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlanAggregateCurrentIntentReferences(t *testing.T) {
	for _, variant := range []string{"primary-approval-alias", "gate-approval-alias", "review-subject-alias", "extra-evidence"} {
		t.Run(variant, func(t *testing.T) {
			root := apTestRoot(t)
			record, _, _ := apTestApproval(t, root)
			s := apTestSession(t, root)
			rule, err := approvalRule(s, "gate.plan-approved")
			if err != nil || len(semStrings(rule["countersigners"])) == 0 {
				t.Fatalf("current Plan signing policy: %v", err)
			}
			record["gate_id"] = "gate.plan-approved"
			record["role_id"] = semStrings(rule["countersigners"])[0]
			record["approval_scope"] = []any{"feature.demo"}
			record["user_decision_ref"] = "reply.json"
			original := apTestPut(t, root, "plan-approval.json", record)
			cp := map[string]any{"feature_id": "feature.demo", "plan_review_ref": "subject.json", "plan_approval_ref": "plan-approval.json", "plan_user_decision_ref": "reply.json", "gates": map[string]any{"gate.plan-approved": map[string]any{"approval_ref": "plan-approval.json"}}}
			review := map[string]any{"drafter_principal_ref": record["drafter_principal_ref"], "internal_checks": map[string]any{}}
			basis := semList(record["basis"])
			if err = s.planAggregateRecord(cp, review, basis, nil); err != nil {
				t.Fatalf("original current aggregate kernel invalid: %v", err)
			}
			intentRef := "nested/Progression-Target.JSON"
			switch variant {
			case "primary-approval-alias", "gate-approval-alias":
				apTestPut(t, root, intentRef, original)
				if variant == "primary-approval-alias" {
					cp["plan_approval_ref"] = intentRef
				} else {
					delete(cp, "plan_approval_ref")
					semMap(semMap(cp["gates"])["gate.plan-approved"])["approval_ref"] = intentRef
				}
			case "review-subject-alias":
				apTestPut(t, root, intentRef, mustReadSpecBaselineTestFile(t, filepath.Join(root, "subject.json")))
				cp["plan_review_ref"] = intentRef
				record["subject_ref"] = intentRef
				apTestPut(t, root, "plan-approval.json", record)
			case "extra-evidence":
				apTestPut(t, root, intentRef, "intent")
				record["evidence_refs"] = []any{intentRef}
				apTestPut(t, root, "plan-approval.json", record)
			}
			before := progressionInventory(t, root)
			if err = apTestSession(t, root).planAggregateRecord(cp, review, basis, nil); semanticCode(err) != "PROGRESSION_EVIDENCE" {
				t.Fatalf("direct current aggregate accepted intent %s: %v", variant, err)
			}
			if !reflect.DeepEqual(before, progressionInventory(t, root)) {
				t.Fatal("current aggregate refusal modified project bytes or modes")
			}
		})
	}
}

// The optional fixture is produced by the fixed legacy prepare/dispatch/complete
// cycle using synthetic actors and replies. It carries no real authorization.
func TestPlanFixedSourceAggregateDifferential(t *testing.T) {
	root := os.Getenv("YSS_PLAN_ORACLE_FIXTURE")
	if root == "" {
		t.Skip("fixed legacy aggregate fixture not configured")
	}
	s := newSemanticSession(context.Background(), root, map[string]string{"tool-root": os.Getenv("YSS_LEGACY_ORACLE_ROOT")})
	if err := s.authorities(); err != nil {
		t.Fatal(err)
	}
	const cp = "docs/.scratch/feature.demo/checkpoint.json"
	if err := s.verify("plan-spec-entry", cp, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.verify("plan-aggregate", cp, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
}
