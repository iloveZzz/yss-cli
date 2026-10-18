package contracts

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutionCandidateIncludesRealSourceNotEvidence(t *testing.T) {
	s, task := taskFixture(t)
	source, err := TaskBaselineSource(s, s.Root, kernel.Text(kernel.ApMap(task["baseline"])["commit"]), []string{"drafts"})
	if err != nil {
		t.Fatal(err)
	}
	compute := func() string {
		t.Helper()
		s = kernel.NewSemanticSession(s.Ctx, s.Root, nil)
		c, _, err := executionCandidate(s, s.Root, source, []string{"drafts", "evidence"}, task["input_bindings"])
		if err != nil {
			t.Fatal(err)
		}
		return kernel.Text(c["digest"])
	}
	before := compute()
	taskFile(t, s.Root, "evidence/check.json", map[string]any{"passed": true})
	if compute() != before {
		t.Fatal("evidence outside source roots changed candidate")
	}
	taskFile(t, s.Root, "drafts/new.json", map[string]any{"new": true})
	if compute() == before {
		t.Fatal("untracked code was omitted")
	}
	if err = os.Remove(filepath.Join(s.Root, "drafts/new.json")); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(filepath.Join(s.Root, "drafts/source.json"), filepath.Join(s.Root, "drafts/renamed.json")); err != nil {
		t.Fatal(err)
	}
	s = kernel.NewSemanticSession(s.Ctx, s.Root, nil)
	c, changed, err := executionCandidate(s, s.Root, source, []string{"drafts"}, task["input_bindings"])
	if err != nil || kernel.Text(c["digest"]) == before || len(changed) != 2 {
		t.Fatalf("rename must bind deletion and new path: %v %v", changed, err)
	}
	if _, _, err = executionCandidate(s, s.Root, source, []string{"drafts/renamed.json"}, task["input_bindings"]); err == nil {
		t.Fatal("out of scope deletion accepted")
	}
	if err = os.Symlink("../input.json", filepath.Join(s.Root, "drafts/link")); err != nil {
		t.Fatal(err)
	}
	s = kernel.NewSemanticSession(s.Ctx, s.Root, nil)
	if _, _, err = executionCandidate(s, s.Root, source, []string{"drafts"}, task["input_bindings"]); err == nil {
		t.Fatal("symlink accepted")
	}
}

func completedDraft(t *testing.T, configure ...func(*kernel.SemanticSession, map[string]any)) (*kernel.SemanticSession, map[string]any) {
	s, task := taskFixture(t)
	for _, apply := range configure {
		apply(s, task)
	}
	taskFile(t, s.Root, "drafts/spec.md", map[string]any{"current": "draft output"})
	candidate, err := draftExecutionCandidate(s, task)
	if err != nil {
		t.Fatal(err)
	}
	task["workflow_status"] = "resolved"
	task["verification_status"] = "executed"
	taskFile(t, s.Root, "drafts/check.json", map[string]any{"status": "reconciled"})
	result := map[string]any{"result_schema": "workflow-execution-result-v1", "work_unit": task["work_unit_id"], "workflow_reference": "input.json", "result": "completed", "skill": "yss-product-lifecycle", "changed_files": []string{"drafts/check.json", "drafts/spec.md"}, "context_reconciliation": map[string]any{"status": "reconciled", "ref": "drafts/check.json"}, "evidence_refs": []string{"drafts/check.json"}, "deferred_seams": []any{}, "drift": []any{}, "violation": []any{}, "new_impacts": []any{}, "stale_candidates": []any{}, "next_route": "work-unit.technical-design", "blocking_signals": []any{}}
	task["result"] = result
	p := kernel.ApMap(task["provenance"])
	actual := map[string]any{"phase": "execution", "tool": p["tool"], "task_ref": p["task_ref"], "identity": p["identity"], "command": "check drafts/spec.md", "exit_code": 0, "executed_at": "2026-01-01T00:00:00Z", "candidate_digest": candidate["digest"], "scope": task["allowed_write_paths"], "expected_outputs": task["expected_outputs"], "baseline": task["baseline"], "input_bindings": task["input_bindings"], "result": "passed", "blocking_findings": []any{}}
	taskFile(t, s.Root, "origin.json", actual)
	bytes, err := os.ReadFile(filepath.Join(s.Root, "origin.json"))
	if err != nil {
		t.Fatal(err)
	}
	task["verification_results"] = []any{map[string]any{"command": actual["command"], "status": "passed", "exit_code": 0, "executed_at": actual["executed_at"], "candidate_digest": candidate["digest"], "evidence_ref": "origin.json", "evidence_digest": safefs.Digest(bytes)}}
	taskFile(t, s.Root, "task.json", task)
	s.V = kernel.NewView(s.Root)
	return s, task
}
func TestTask3CompletedDraftUsesActualCurrentResults(t *testing.T) {
	for _, name := range []string{"passed", "not-run", "nonzero", "empty", "blocker", "dispatch-as-execution", "stale-original", "invented-artifact"} {
		t.Run(name, func(t *testing.T) {
			s, task := completedDraft(t)
			rows := kernel.ApRows(task["verification_results"])
			switch name {
			case "not-run":
				rows[0]["status"] = "not-run"
			case "nonzero":
				rows[0]["exit_code"] = 1
			case "empty":
				task["verification_results"] = []any{}
			case "blocker":
				kernel.ApMap(task["result"])["blocking_signals"] = []string{"unresolved"}
			case "dispatch-as-execution":
				task["workflow_status"] = "not-started"
			case "invented-artifact":
				kernel.ApMap(task["result"])["changed_artifacts"] = []string{"drafts/invented.md"}
			case "stale-original":
				taskFile(t, s.Root, "input.json", map[string]any{"changed": true})
			}
			taskFile(t, s.Root, "task.json", task)
			err := verifyRoleIndependentTask(s, "task.json", nil)
			if (err == nil) != (name == "passed") {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
}
func TestReviewImplementationComesFromCompletedBasis(t *testing.T) {
	for _, name := range []string{"one", "zero", "duplicate", "multiple", "stale-output", "not-completed"} {
		t.Run(name, func(t *testing.T) {
			s, task := completedDraft(t)
			c, err := draftExecutionCandidate(s, task)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(filepath.Join(s.Root, "task.json"))
			basis := []any{map[string]any{"ref": "task.json", "digest": safefs.Digest(raw)}}
			if name == "zero" {
				basis = []any{}
			}
			if name == "duplicate" {
				basis = append(basis, basis[0])
			}
			if name == "multiple" {
				other := ContractCopy(task)
				p := ContractCopy(kernel.ApMap(task["provenance"]))
				p["task_ref"] = "other.json"
				p["execution_ref"] = "other-log.json"
				other["provenance"] = p
				log, err := s.Doc("origin.json")
				if err != nil {
					t.Fatal(err)
				}
				log = ContractCopy(log)
				log["task_ref"] = "other.json"
				taskFile(t, s.Root, "other-log.json", log)
				logBytes, _ := os.ReadFile(filepath.Join(s.Root, "other-log.json"))
				row := ContractCopy(kernel.ApRows(task["verification_results"])[0])
				row["evidence_ref"] = "other-log.json"
				row["evidence_digest"] = safefs.Digest(logBytes)
				other["verification_results"] = []any{row}
				taskFile(t, s.Root, "other.json", other)
				raw, _ := os.ReadFile(filepath.Join(s.Root, "other.json"))
				basis = append(basis, map[string]any{"ref": "other.json", "digest": safefs.Digest(raw)})
			}
			if name == "stale-output" {
				taskFile(t, s.Root, "drafts/spec.md", map[string]any{"changed": true})
			}
			if name == "not-completed" {
				task["workflow_status"] = "active"
				taskFile(t, s.Root, "task.json", task)
				raw, _ = os.ReadFile(filepath.Join(s.Root, "task.json"))
				basis[0] = map[string]any{"ref": "task.json", "digest": safefs.Digest(raw)}
			}
			s.V = kernel.NewView(s.Root)
			p, ref, _, protection, err := trustedTaskImplementation(s, map[string]any{"basis": basis, "candidate": c})
			if (err == nil) != (name == "one") {
				t.Fatalf("%s: %v", name, err)
			}
			if name == "one" && (protection["repository_root"] != s.Root || !kernel.ApSetEqual(protection["paths"], task["expected_outputs"])) {
				t.Fatal("candidate protection not emitted from verified outputs")
			}
			if name == "one" && (ref != "task.json" || !kernel.ApEqual(p, task["provenance"])) {
				t.Fatal("source not taken from completed task")
			}
		})
	}
}

func TestTask3LogicalDispatchRefCannotRewriteExecution(t *testing.T) {
	s, task := taskFixture(t)
	taskFile(t, s.Root, "transport.json", task)
	if err := verifyRoleIndependentTask(s, "transport.json", map[string]string{"task-ref": "task.json"}); err != nil {
		t.Fatal(err)
	}
	if err := verifyRoleIndependentTask(s, "transport.json", nil); err == nil {
		t.Fatal("default file ref was overridden")
	}
	if err := verifyRoleIndependentTask(s, "transport.json", map[string]string{"task-ref": "../outside.json"}); err == nil {
		t.Fatal("escaping logical target accepted")
	}
	s, task = completedDraft(t)
	taskFile(t, s.Root, "transport.json", task)
	if err := verifyRoleIndependentTask(s, "transport.json", map[string]string{"task-ref": "task.json"}); err == nil {
		t.Fatal("completion accepted dispatch transport override")
	}
}

func TestDraftCandidateBindsEveryDeclaredOutput(t *testing.T) {
	s, task := completedDraft(t)
	task["expected_outputs"] = []string{"drafts/spec.md", "drafts/second.md"}
	taskFile(t, s.Root, "drafts/second.md", map[string]any{"value": "first"})
	s.V = kernel.NewView(s.Root)
	first, err := draftExecutionCandidate(s, task)
	if err != nil {
		t.Fatal(err)
	}
	taskFile(t, s.Root, "drafts/second.md", map[string]any{"value": "changed"})
	s.V = kernel.NewView(s.Root)
	second, err := draftExecutionCandidate(s, task)
	if err != nil {
		t.Fatal(err)
	}
	if kernel.ApEqual(first, second) {
		t.Fatal("second declared output was not bound")
	}
	task["expected_outputs"] = []string{"outside/spec.md"}
	if err = validateTaskOutputRefs(s, "task.json", task); err == nil {
		t.Fatal("outside output accepted")
	}
}

func completedReviewer(t *testing.T, rawAsset bool, configure ...func(*kernel.SemanticSession, map[string]any)) (*kernel.SemanticSession, map[string]any) {
	s, implementation := completedDraft(t, configure...)
	candidate, err := draftExecutionCandidate(s, implementation)
	if err != nil {
		t.Fatal(err)
	}
	if rawAsset {
		raw, _ := os.ReadFile(filepath.Join(s.Root, "drafts/spec.md"))
		candidate = map[string]any{"ref": "drafts/spec.md", "digest": safefs.Digest(raw)}
	}
	raw, _ := os.ReadFile(filepath.Join(s.Root, "task.json"))
	basis := map[string]any{"ref": "task.json", "digest": safefs.Digest(raw)}
	task := ContractCopy(implementation)
	task["execution_state"] = "Reviewer"
	task["allowed_write_paths"] = []string{"reviews"}
	task["inputs"] = []string{"input.json", "task.json"}
	task["input_bindings"] = append(kernel.ApArray(implementation["input_bindings"]), basis)
	task["expected_outputs"] = []string{"reviews/result.json"}
	task["expected_evidence_files"] = []string{"reviews/result.json"}
	baseline := ContractCopy(kernel.ApMap(implementation["baseline"]))
	source, err := TaskBaselineSource(s, s.Root, kernel.Text(baseline["commit"]), []string{"reviews"})
	if err != nil {
		t.Fatal(err)
	}
	baseline["digest"] = strings.TrimPrefix(ContractDigest(source), "sha256:")
	task["baseline"] = baseline
	provenance := ContractCopy(kernel.ApMap(implementation["provenance"]))
	provenance["task_ref"] = "review-task.json"
	provenance["execution_ref"] = "review-log.json"
	task["provenance"] = provenance
	scope, err := taskApprovalScope(s, implementation)
	if err != nil {
		t.Fatal(err)
	}
	task["review_context"] = map[string]any{"candidate": candidate, "policy": task["policy"], "check_ids": kernel.ApMap(task["skill_source"])["check_ids"], "approval_scope": scope, "basis": []any{basis}, "review": map[string]any{"mode": "self-check", "provenance": provenance}}
	log, err := s.Doc("origin.json")
	if err != nil {
		t.Fatal(err)
	}
	log = ContractCopy(log)
	log["task_ref"] = "review-task.json"
	log["candidate_digest"] = candidate["digest"]
	log["baseline"] = baseline
	log["input_bindings"] = task["input_bindings"]
	log["expected_outputs"] = task["expected_outputs"]
	log["readonly"] = true
	log["scope"] = scope
	taskFile(t, s.Root, "review-log.json", log)
	raw, _ = os.ReadFile(filepath.Join(s.Root, "review-log.json"))
	row := ContractCopy(kernel.ApRows(task["verification_results"])[0])
	row["evidence_ref"] = "review-log.json"
	row["candidate_digest"] = candidate["digest"]
	row["evidence_digest"] = safefs.Digest(raw)
	task["verification_results"] = []any{row}
	result := ContractCopy(kernel.ApMap(task["result"]))
	result["changed_files"] = []string{"reviews/result.json"}
	result["context_reconciliation"] = map[string]any{"status": "reconciled", "ref": "reviews/result.json"}
	result["evidence_refs"] = []string{"reviews/result.json"}
	task["result"] = result
	taskFile(t, s.Root, "reviews/result.json", map[string]any{"status": "reconciled"})
	taskFile(t, s.Root, "review-task.json", task)
	s.V = kernel.NewView(s.Root)
	return s, task
}

func TestCompletedReviewerUsesValidatedImplementation(t *testing.T) {
	s, task := completedReviewer(t, false)
	if err := verifyRoleIndependentTask(s, "review-task.json", nil); err != nil {
		t.Fatal(err)
	}
	kernel.ApMap(kernel.ApMap(task["review_context"])["review"])["mode"] = "independent-review"
	taskFile(t, s.Root, "review-task.json", task)
	s.V = kernel.NewView(s.Root)
	if err := verifyRoleIndependentTask(s, "review-task.json", nil); err == nil {
		t.Fatal("missing independent source accepted")
	}
}

func TestRawOutputReviewUsesCompletedTaskMembership(t *testing.T) {
	for _, scenario := range []string{"member", "not-output", "changed-output", "wrong-root"} {
		t.Run(scenario, func(t *testing.T) {
			s, task := completedDraft(t)
			raw, _ := os.ReadFile(filepath.Join(s.Root, "task.json"))
			assetBytes, _ := os.ReadFile(filepath.Join(s.Root, "drafts/spec.md"))
			candidate := map[string]any{"ref": "drafts/spec.md", "digest": safefs.Digest(assetBytes)}
			basis := []any{map[string]any{"ref": "task.json", "digest": safefs.Digest(raw)}}
			if scenario == "not-output" {
				taskFile(t, s.Root, "not-output.json", map[string]any{"current": "draft output"})
				candidate["ref"] = "not-output.json"
			}
			if scenario == "changed-output" {
				taskFile(t, s.Root, "drafts/spec.md", map[string]any{"current": "changed"})
			}
			if scenario == "wrong-root" {
				other := t.TempDir()
				taskFile(t, other, "drafts/spec.md", map[string]any{"current": "draft output"})
				candidate["ref"] = filepath.Join(other, "drafts/spec.md")
			}
			s.V = kernel.NewView(s.Root)
			p, ref, _, _, err := trustedTaskImplementation(s, map[string]any{"basis": basis, "candidate": candidate})
			if (err == nil) != (scenario == "member") {
				t.Fatalf("%s: %v", scenario, err)
			}
			if err == nil && (ref != "task.json" || !kernel.ApEqual(p, task["provenance"])) {
				t.Fatal("not actual completed source")
			}
		})
	}
}

func TestFormalTaskScopeComesFromCurrentFeatureAndBoundInput(t *testing.T) {
	s, task := taskFixture(t)
	task["feature_id"] = "feature.orders"
	task["checkpoint_ref"] = "checkpoint.json"
	taskFile(t, s.Root, "checkpoint.json", map[string]any{"feature_id": "feature.orders"})
	input := map[string]any{"feature_id": "feature.orders"}
	taskFile(t, s.Root, "input.json", input)
	raw, _ := os.ReadFile(filepath.Join(s.Root, "input.json"))
	task["input_bindings"] = []any{map[string]any{"ref": "input.json", "digest": safefs.Digest(raw)}}
	scope, err := taskApprovalScope(s, task)
	if err != nil || !kernel.ApSetEqual(scope, []string{"feature.orders"}) {
		t.Fatalf("formal scope: %v %v", scope, err)
	}
	taskFile(t, s.Root, "checkpoint.json", map[string]any{"feature_id": "feature.other"})
	s.V = kernel.NewView(s.Root)
	if _, err = taskApprovalScope(s, task); err == nil {
		t.Fatal("accepted different checkpoint feature")
	}
}

func TestTaskBaselineQueryReturnsActualSourceDigestWithoutWriteAuthority(t *testing.T) {
	s, task := taskFixture(t)
	input := map[string]any{"repository_root": s.Root, "commit": kernel.ApMap(task["baseline"])["commit"], "allowed_write_paths": task["allowed_write_paths"]}
	taskFile(t, s.Root, "baseline-query.json", input)
	s.V = kernel.NewView(s.Root)
	if err := s.Verify("task-baseline", "baseline-query.json", nil); err != nil {
		t.Fatal(err)
	}
	observed := kernel.ApMap(s.Report.Coverage["task_baseline"])
	if !kernel.ApEqual(observed["baseline"], task["baseline"]) || observed["execution_allowed"] != false {
		t.Fatalf("wrong baseline: %v", observed)
	}
	input["repository_root"] = filepath.Dir(s.Root)
	taskFile(t, s.Root, "baseline-query.json", input)
	s.V = kernel.NewView(s.Root)
	if err := s.Verify("task-baseline", "baseline-query.json", nil); err == nil {
		t.Fatal("query admitted unregistered root")
	}
}

func TestReview3DispatchBindsActualCompletedImplementationCandidate(t *testing.T) {
	s, task := completedReviewer(t, true)
	task["workflow_status"] = "not-started"
	task["verification_status"] = "not-executed"
	task["verification_results"] = []any{}
	delete(task, "result")
	p := kernel.ApMap(task["provenance"])
	origin := map[string]any{"phase": "dispatch", "dispatched_at": "2026-01-01T00:00:00Z", "tool": p["tool"], "task_ref": p["task_ref"], "identity": p["identity"], "candidate": kernel.ApMap(task["review_context"])["candidate"], "input_bindings": task["input_bindings"], "baseline": task["baseline"], "scope": task["allowed_write_paths"], "expected_outputs": task["expected_outputs"]}
	taskFile(t, s.Root, "review-log.json", origin)
	taskFile(t, s.Root, "review-task.json", task)
	s.V = kernel.NewView(s.Root)
	if err := verifyRoleIndependentTask(s, "review-task.json", nil); err != nil {
		t.Fatal(err)
	}
	kernel.ApMap(task["review_context"])["candidate"] = map[string]any{"ref": "input.json", "digest": kernel.ApRows(task["input_bindings"])[0]["digest"]}
	taskFile(t, s.Root, "review-task.json", task)
	s.V = kernel.NewView(s.Root)
	if err := verifyRoleIndependentTask(s, "review-task.json", nil); err == nil {
		t.Fatal("dispatch accepted unrelated candidate")
	}
}
