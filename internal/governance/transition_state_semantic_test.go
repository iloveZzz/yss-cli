package governance

import "testing"

func TestTaskContextProjectionRetainsCheckpointEvidence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		claims   map[string]any
		accepted bool
	}{
		{"published-result", map[string]any{"status": "reconciled", "ref": "reconciliation.json"}, true},
		{"wrong-status", map[string]any{"status": "pending", "ref": "reconciliation.json"}, false},
		{"wrong-ref", map[string]any{"status": "reconciled", "ref": "other.json"}, false},
		{"result-injects-evidence", map[string]any{"status": "reconciled", "ref": "reconciliation.json", "evidence_refs": []any{}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := apTestRoot(t)
			const cpRef = "docs/.scratch/feature-demo/checkpoint.json"
			const taskRef = "task.json"
			current := map[string]any{"status": "reconciled", "ref": "reconciliation.json", "evidence_refs": []any{"context-evidence.json"}}
			apTestPut(t, root, cpRef, map[string]any{"context_reconciliation": current})
			apTestPut(t, root, taskRef, map[string]any{"checkpoint_ref": cpRef, "workflow_status": "resolved", "work_unit_id": "work-unit.demo", "result": map[string]any{"result": "completed", "work_unit": "work-unit.demo", "context_reconciliation": tc.claims}})
			s := apTestSession(t, root)
			result, err := s.transitionState(cpRef, taskRef)
			if !tc.accepted {
				apTestCode(t, err, "TRANSITION_STATE_DRIFT")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !apEqual(result["context_reconciliation"], current) {
				t.Fatal("task replaced checkpoint evidence binding")
			}
		})
	}
}
