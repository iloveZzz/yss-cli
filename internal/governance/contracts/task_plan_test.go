package contracts

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

func TestSpecTaskConsumesCheckpointPlanEntry(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := map[string]any{"feature_id": "feature.demo", "plan_review_ref": "review.json", "plan_approval_ref": "approval.json", "plan_user_decision_ref": "decision.json"}
	content, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "checkpoint.json"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	previous := kernel.SemanticValidators["plan-spec-entry"]
	t.Cleanup(func() {
		if previous == nil {
			delete(kernel.SemanticValidators, "plan-spec-entry")
		} else {
			kernel.SemanticValidators["plan-spec-entry"] = previous
		}
	})
	entryFailure := errors.New("current Plan entry is blocked")
	var rejection error
	calls := 0
	kernel.SemanticValidators["plan-spec-entry"] = func(_ *kernel.SemanticSession, ref string, _ map[string]string) error {
		calls++
		if ref != "checkpoint.json" {
			t.Fatalf("Plan entry must consume checkpoint, got %q", ref)
		}
		return rejection
	}
	check := func(task map[string]any) error {
		return verifySpecTaskPlanEntry(kernel.NewSemanticSession(context.Background(), root, nil), task)
	}
	for _, explicit := range []bool{true, false} {
		task := kernel.ApCopy(checkpoint)
		task["contract"] = map[string]any{"lifecycle_ref": "checkpoint.json"}
		if explicit {
			task["checkpoint_ref"] = "checkpoint.json"
		}
		if err := check(task); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"feature_id", "plan_review_ref", "plan_approval_ref", "plan_user_decision_ref", "plan_continuation_ref"} {
			changed := kernel.ApCopy(task)
			changed[key] = "unrelated"
			before := calls
			if err := check(changed); err == nil || !strings.Contains(err.Error(), key) || calls != before {
				t.Fatalf("must reject changed %s before entry validation: %v", key, err)
			}
		}
		missing := kernel.ApCopy(task)
		delete(missing, "plan_approval_ref")
		if err := check(missing); err == nil || !strings.Contains(err.Error(), "plan_approval_ref") {
			t.Fatalf("missing approval must be rejected: %v", err)
		}
		rejection = entryFailure
		if err := check(task); !errors.Is(err, entryFailure) {
			t.Fatalf("must preserve Plan rejection: %v", err)
		}
		rejection = nil
	}
}
