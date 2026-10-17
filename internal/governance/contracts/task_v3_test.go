package contracts

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func taskFile(t *testing.T, root, ref string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(filepath.Join(root, ref)), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, ref), raw, 0644); err != nil {
		t.Fatal(err)
	}
}
func taskFixture(t *testing.T) (*kernel.SemanticSession, map[string]any) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	taskFile(t, root, "drafts/source.json", map[string]any{"baseline": true})
	for _, args := range [][]string{{"init", "-q"}, {"add", "drafts/source.json"}, {"-c", "user.name=test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "base"}} {
		c := exec.Command("git", args...)
		c.Dir = root
		if out, e := c.CombinedOutput(); e != nil {
			t.Fatalf("git: %s %v", out, e)
		}
	}
	c := exec.Command("git", "rev-parse", "HEAD")
	c.Dir = root
	head, err := c.Output()
	if err != nil {
		t.Fatal(err)
	}
	taskFile(t, root, "yss-project.yaml", map[string]any{"schema_version": 2, "repository_mode": "project-instance"})
	taskFile(t, root, ".template-spec/process/harness-profile.yaml", map[string]any{"schema_version": 3, "profile_id": "harness.backend-delivery"})
	taskFile(t, root, kernel.ApprovalTaskSchemaRef, map[string]any{"type": "object"}) // Schema branches have separate real-template integration coverage.
	input := map[string]any{"status": "draft", "scope": map[string]any{"impacted_areas": []string{"backend"}}}
	taskFile(t, root, "input.json", input)
	b, _ := os.ReadFile(filepath.Join(root, "input.json"))
	bound := map[string]any{"ref": "input.json", "digest": safefs.Digest(b)}
	s := kernel.NewSemanticSession(context.Background(), root, nil)
	s.GovernanceVersion = 2
	s.GovernanceDigest = strings.Repeat("a", 64)
	s.Report.Profile = "backend"
	s.Registry = map[string]any{"stages": []any{map[string]any{"id": "stage.spec-architecture"}}, "work_units": []any{map[string]any{"id": "work-unit.spec-synthesis", "scope": "project-instance"}}}
	s.GovernancePolicy = map[string]any{"preparation_policy": map[string]any{"work_units": []string{"work-unit.spec-synthesis"}}, "work_unit_routes": map[string]any{"work-unit.spec-synthesis": map[string]any{"primary_skill": "yss-product-lifecycle", "check_ids": []string{"gate.spec-baseline-approved"}, "impact_check_ids": map[string]any{"backend": []string{}}}}, "gate_policy": map[string]any{"profiles": map[string]any{"backend": map[string]any{"user_decision_gates": []string{"gate.spec-baseline-approved"}}}}}
	baseline := map[string]any{"repository_root": root, "commit": strings.TrimSpace(string(head))}
	source, err := TaskBaselineSource(s, root, kernel.Text(baseline["commit"]), []string{"drafts"})
	if err != nil {
		t.Fatal(err)
	}
	baseline["digest"] = strings.TrimPrefix(ContractDigest(source), "sha256:")
	provenance := map[string]any{"tool": "codex", "task_ref": "task.json", "execution_ref": "origin.json", "identity": map[string]any{"status": "unavailable", "reason": "platform does not expose stable id"}}
	task := map[string]any{"schema_version": 3, "task_id": "TASK-1", "stage_id": "stage.spec-architecture", "profile": "backend", "policy": map[string]any{"ref": ".template-spec/process/orchestration-contract.yaml", "version": 2, "digest": s.GovernanceDigest}, "work_unit_id": "work-unit.spec-synthesis", "execution_state": "Drafter", "workflow_status": "not-started", "objective": "起草当前Spec", "acceptance": []string{"保留范围与验收"}, "inputs": []string{"input.json"}, "input_bindings": []any{bound}, "baseline": baseline, "provenance": provenance, "allowed_write_paths": []string{"drafts"}, "forbidden_actions": []string{"user-approval", "ready-for-agent"}, "expected_outputs": []string{"drafts/spec.md"}, "expected_evidence_files": []string{"drafts/check.json"}, "verification_commands": []string{"check drafts/spec.md"}, "verification_results": []any{}, "contract": map[string]any{"kind": "lifecycle-work-unit", "status": "issued", "contract_ref": "input.json", "lifecycle_ref": "input.json"}, "convergence": map[string]any{"parent_work_unit": "work-unit.spec-synthesis", "convergence_ref": "input.json"}, "downstream_consumers": []string{"work-unit.technical-design"}}
	selection, err := kernel.CompileTaskSelection(s, "work-unit.spec-synthesis", []string{"backend"})
	if err != nil {
		t.Fatal(err)
	}
	task["skill_source"] = selection
	origin := map[string]any{"phase": "dispatch", "dispatched_at": "2026-01-01T00:00:00Z", "tool": provenance["tool"], "task_ref": "task.json", "identity": provenance["identity"], "candidate": bound, "input_bindings": task["input_bindings"], "baseline": baseline, "scope": task["allowed_write_paths"]}
	taskFile(t, root, "origin.json", origin)
	taskFile(t, root, "task.json", task)
	return s, task
}
func TestTask3DraftNeedsCurrentFactsNotFutureExecutions(t *testing.T) {
	s, _ := taskFixture(t)
	if err := verifyTaskPackageSemantic(s, "task.json", nil); err != nil {
		t.Fatal(err)
	}
}

func TestTask3DispatchCannotClaimExecution(t *testing.T) {
	for _, state := range []string{"active", "paused", "failed", "resolved"} {
		t.Run(state, func(t *testing.T) {
			s, task := taskFixture(t)
			task["workflow_status"] = state
			taskFile(t, s.Root, "task.json", task)
			if err := verifyTaskPackageSemantic(s, "task.json", nil); err == nil || !strings.Contains(err.Error(), "尚未启用") {
				t.Fatalf("unimplemented execution must fail explicitly: %v", err)
			}
		})
	}
}

func TestTask3DraftRejectsMissingOrStaleFacts(t *testing.T) {
	cases := map[string]func(*kernel.SemanticSession, map[string]any){
		"unknown-stage":      func(s *kernel.SemanticSession, t map[string]any) { t["stage_id"] = "stage.unknown" },
		"empty-verification": func(s *kernel.SemanticSession, t map[string]any) { t["verification_commands"] = []string{} },
		"escape-evidence": func(s *kernel.SemanticSession, t map[string]any) {
			t["expected_evidence_files"] = []string{"../escape"}
		},
		"empty-evidence": func(s *kernel.SemanticSession, t map[string]any) { t["expected_evidence_files"] = []string{} },
		"fake-baseline": func(s *kernel.SemanticSession, t map[string]any) {
			kernel.ApMap(t["baseline"])["digest"] = strings.Repeat("b", 64)
		},
		"wrong-scope": func(s *kernel.SemanticSession, t map[string]any) { t["allowed_write_paths"] = []string{"other"} },
		"unknown-impact": func(s *kernel.SemanticSession, t map[string]any) {
			kernel.ApMap(t["skill_source"])["impacts"] = []string{"unknown"}
		},
		"missing-check": func(s *kernel.SemanticSession, t map[string]any) {
			kernel.ApMap(t["skill_source"])["check_ids"] = []string{}
		},
		"missing-forbidden-action": func(s *kernel.SemanticSession, t map[string]any) { t["forbidden_actions"] = []string{"user-approval"} },
		"worker":                   func(s *kernel.SemanticSession, t map[string]any) { t["execution_state"] = "Worker" },
		"claimed-result":           func(s *kernel.SemanticSession, t map[string]any) { t["result"] = map[string]any{"result": "completed"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s, task := taskFixture(t)
			mutate(s, task)
			taskFile(t, s.Root, "task.json", task)
			if err := verifyTaskPackageSemantic(s, "task.json", nil); err == nil {
				t.Fatal("invalid dispatch accepted")
			}
		})
	}
	t.Run("dirty-input", func(t *testing.T) {
		s, _ := taskFixture(t)
		taskFile(t, s.Root, "input.json", map[string]any{"changed": true})
		if err := verifyTaskPackageSemantic(s, "task.json", nil); err == nil {
			t.Fatal("stale input accepted")
		}
	})
	t.Run("dispatch-log-has-results", func(t *testing.T) {
		s, _ := taskFixture(t)
		raw, _ := os.ReadFile(filepath.Join(s.Root, "origin.json"))
		var log map[string]any
		json.Unmarshal(raw, &log)
		log["exit_code"] = 0
		taskFile(t, s.Root, "origin.json", log)
		if err := verifyTaskPackageSemantic(s, "task.json", nil); err == nil {
			t.Fatal("future result accepted")
		}
	})
	t.Run("frozen-tree-is-not-current-worktree", func(t *testing.T) {
		s, _ := taskFixture(t)
		taskFile(t, s.Root, "drafts/source.json", map[string]any{"current": true})
		if err := verifyTaskPackageSemantic(s, "task.json", nil); err != nil {
			t.Fatal(err)
		}
	})
}
