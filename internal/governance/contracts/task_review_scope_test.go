package contracts

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"path/filepath"
	"testing"
)

func TestReview3b2RejectsImplementationWriteScope(t *testing.T) {
	for _, state := range []string{"Reviewer", "Verifier"} {
		t.Run(state, func(t *testing.T) {
			s, implementation := completedDraft(t)
			candidate, err := draftExecutionCandidate(s, implementation)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(s.Root, "task.json"))
			if err != nil {
				t.Fatal(err)
			}
			basis := map[string]any{"ref": "task.json", "digest": safefs.Digest(raw)}
			task := ContractCopy(implementation)
			task["execution_state"] = state
			task["inputs"] = []string{"input.json", "task.json"}
			task["input_bindings"] = append(kernel.ApArray(implementation["input_bindings"]), basis)
			task["expected_outputs"] = []string{"review report"}
			p := ContractCopy(kernel.ApMap(task["provenance"]))
			p["task_ref"] = "review-task.json"
			p["execution_ref"] = "review-log.json"
			task["provenance"] = p
			task["review_context"] = map[string]any{"candidate": candidate, "policy": task["policy"], "check_ids": kernel.ApMap(task["skill_source"])["check_ids"], "approval_scope": implementation["allowed_write_paths"], "basis": []any{basis}, "review": map[string]any{"mode": "self-check", "provenance": p}}
			log, err := s.Doc("origin.json")
			if err != nil {
				t.Fatal(err)
			}
			log = ContractCopy(log)
			log["task_ref"] = "review-task.json"
			log["input_bindings"] = task["input_bindings"]
			log["expected_outputs"] = task["expected_outputs"]
			log["readonly"] = true
			taskFile(t, s.Root, "review-log.json", log)
			raw, err = os.ReadFile(filepath.Join(s.Root, "review-log.json"))
			if err != nil {
				t.Fatal(err)
			}
			row := ContractCopy(kernel.ApRows(task["verification_results"])[0])
			row["evidence_ref"] = "review-log.json"
			row["evidence_digest"] = safefs.Digest(raw)
			task["verification_results"] = []any{row}
			taskFile(t, s.Root, "review-task.json", task)
			s.V = kernel.NewView(s.Root)
			if err = verifyRoleIndependentTask(s, "review-task.json", nil); kernel.SemanticCode(err) != "TASK_REVIEW" {
				t.Fatalf("expected candidate write protection for %s, got %v", state, err)
			}
		})
	}
}

func TestReviewCandidateWriteProtectionUsesActualSourceRoots(t *testing.T) {
	s, implementation := taskFixture(t)
	source, err := TaskBaselineSource(s, s.Root, kernel.Text(kernel.ApMap(implementation["baseline"])["commit"]), []string{"drafts"})
	if err != nil {
		t.Fatal(err)
	}
	// This is the real frozen Git source consumed by the Worker candidate algorithm.
	if _, _, err = executionCandidate(s, s.Root, source, []string{"drafts", "evidence"}, implementation["input_bindings"]); err != nil {
		t.Fatal(err)
	}
	protection := map[string]any{"repository_root": s.Root, "paths": source["roots"]}
	for _, tc := range []struct {
		name, root, write string
		changed           []string
		reject            bool
	}{
		{"source-root", s.Root, "drafts", nil, true},
		{"source-descendant", s.Root, "drafts/new.java", nil, true},
		{"changed-source", s.Root, "evidence", []string{"drafts/source.json"}, true},
		{"same-repo-evidence", s.Root, "evidence", []string{"evidence/review.json"}, false},
		{"different-repo-same-name", filepath.Join(s.Root, "other"), "drafts", nil, false},
		{"nested-repo-source", filepath.Join(s.Root, "drafts"), "new.java", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := map[string]any{"baseline": map[string]any{"repository_root": tc.root}, "allowed_write_paths": []string{tc.write}, "result": map[string]any{"changed_files": tc.changed}}
			err := validateReviewCandidateWrites(s, task, protection)
			if (err != nil) != tc.reject {
				t.Fatalf("reject=%t: %v", tc.reject, err)
			}
		})
	}
	// Concrete draft outputs do not reserve the implementation's broad write allowlist.
	protection["paths"] = []string{"drafts/source.json"}
	task := map[string]any{"baseline": map[string]any{"repository_root": s.Root}, "allowed_write_paths": []string{"drafts/evidence"}, "result": map[string]any{"changed_files": []string{"drafts/evidence/review.json"}}}
	if err = validateReviewCandidateWrites(s, task, protection); err != nil {
		t.Fatal(err)
	}
}
