package governance

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func taskTestCompletedBackendProducerRoot(t *testing.T) string {
	t.Helper()
	legacy := os.Getenv("YSS_LEGACY_ORACLE_ROOT")
	if legacy == "" {
		t.Skip("fixed legacy producer not selected")
	}
	root := os.Getenv("YSS_BACKEND_COMPLETED_TASK_ROOT")
	if root == "" {
		frozen, err := os.ReadFile("backend_terminal_semantic_test.go")
		if err != nil || safefs.Digest(frozen) != "5346e3ec844d2b3a56194cd8bd71ed1648d3ccb32757c3afe0c38885c93827bf" {
			t.Fatal("fixed backend producer body unavailable or changed", err)
		}
		section := string(frozen)
		at := strings.Index(section, "func TestBackendCurrentTerminalFixedSource(t *testing.T)")
		if at < 0 {
			t.Fatal("fixed producer absent")
		}
		section = section[at:]
		start := strings.Index(section, "script := `")
		if start < 0 {
			t.Fatal("fixed producer script absent")
		}
		start += len("script := `")
		end := strings.Index(section[start:], "`\n\tcmd := exec.Command")
		if end < 0 {
			t.Fatal("fixed producer script end absent")
		}
		cmd := exec.Command("node", "--input-type=module", "-e", section[start:start+end], legacy)
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixed current terminal: %v %s", err, raw)
		}
		var produced struct {
			Root   string         `json:"root"`
			Result map[string]any `json:"result"`
		}
		if err = json.Unmarshal(raw, &produced); err != nil || produced.Result["result"] != "backend-delivered" {
			t.Fatalf("fixed producer did not close current terminal: %v %s", err, raw)
		}
		root = produced.Root
		t.Logf("fixed completed producer raw %s", raw)
	}
	t.Logf("retained synthetic completed producer root=%s legacy-business-off=true authorizes_real_action=false", root)
	apTestPut(t, root, ".template-spec/process/harness-profile.yaml", map[string]any{"schema_version": 2, "profile_id": domain.Profiles["spec"].ID})
	// The original published terminal producer predates business_ticket_version.
	// This independent synthetic consumer explicitly selects its supported legacy
	// tracker mode. It does not alter delivery/review/package authority bytes.
	apTestPut(t, root, ".template-spec/agents/issue-tracker.md", "---\ntracker:\n  platform: local-markdown\n  root: docs/issues\n  lifecycle_tracking_version: 1\n---\nSynthetic registered legacy tracker. Business protocol remains off; no real authorization.\n")
	task := apTestTask(t, root, 1)
	task["work_unit_id"], task["stage_id"], task["workflow_status"] = "work-unit.backend-delivery", "stage.verification-release-retrospective", "resolved"
	task["allowed_write_paths"] = []any{".yss-backend-delivery.json"}
	cpRef := "docs/.scratch/backend-consumer/checkpoint.json"
	workflowRef := ".agents/skills/yss-product-lifecycle/references/orchestration-contract.yaml"
	apMap(task["contract"])["contract_ref"] = workflowRef
	task["checkpoint_ref"] = cpRef
	evidence := []any{"CONTEXT.md", "delivery.json", "review-state.json", ".yss-backend-delivery.json", "contract-test.json", "deployment-test.json"}
	task["expected_evidence_files"] = evidence
	task["verification_commands"] = []any{"synthetic canonical terminal mechanism check"}
	task["verification_results"] = []any{map[string]any{"command": "synthetic canonical terminal mechanism check", "exit_code": 0, "executed_at": time.Now().UTC().Format(time.RFC3339Nano), "evidence_ref": ".yss-backend-delivery.json"}}
	task["convergence"] = map[string]any{"parent_work_unit": "work-unit.backend-delivery", "convergence_ref": cpRef}
	task["result"] = map[string]any{"result_schema": "workflow-execution-result-v1", "work_unit": "work-unit.backend-delivery", "workflow_reference": workflowRef, "skill": "yss-product-lifecycle", "result": "completed", "changed_files": []any{}, "changed_artifacts": []any{}, "context_reconciliation": map[string]any{"status": "reconciled", "ref": "CONTEXT.md"}, "evidence_refs": evidence, "deferred_seams": []any{}, "drift": []any{}, "violation": []any{}, "new_impacts": []any{}, "stale_candidates": []any{}, "blocking_signals": []any{}, "next_route": nil, "checkpoint_ref": cpRef}
	cp := apTestCheckpoint(map[string]any{})
	cp["feature_id"], cp["mode"], cp["stage"], cp["status"], cp["next_work_unit"] = "backend-consumer", "audit", "stage.verification-release-retrospective", "routing", nil
	cp["context_reconciliation"] = map[string]any{"status": "reconciled", "ref": "CONTEXT.md", "evidence_refs": []any{"CONTEXT.md"}}
	cp["phase_boundary"] = map[string]any{"decision": "continue", "reason": "Synthetic current backend responsibility endpoint, never business completion"}
	cp["stage_trace"] = map[string]any{"stage": "stage.verification-release-retrospective", "upstream_refs": []any{"CONTEXT.md"}, "artifact_refs": evidence, "gate_decisions": []any{}, "downstream_impacts": []any{}, "completed_work_unit": "work-unit.backend-delivery"}
	apTestPut(t, root, cpRef, cp)
	apTestPut(t, root, "docs/.scratch/backend-consumer/task.json", task)
	return root
}

func TestTaskCompletedBackendProducerAuditFixedSource(t *testing.T) {
	root := taskTestCompletedBackendProducerRoot(t)
	ref, cpRef := "docs/.scratch/backend-consumer/task.json", "docs/.scratch/backend-consumer/checkpoint.json"
	baseline, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ref)))
	if err != nil {
		t.Fatal(err)
	}
	run := func(extra map[string]string) error {
		s := newSemanticSession(context.Background(), root, map[string]string{"tool-root": os.Getenv("YSS_LEGACY_ORACLE_ROOT")})
		if err := s.authorities(); err != nil {
			return err
		}
		opts := map[string]string{"checkpoint": cpRef}
		for key, value := range extra {
			opts[key] = value
		}
		if err := s.verify("backend-completed-producer-task", ref, opts); err != nil {
			return err
		}
		return s.finish()
	}
	if err = run(nil); err != nil {
		t.Fatal(err)
	}
	// The public task API keeps the old terminal recovery prohibition.
	if _, err = apTestRun(root, "task", ref, map[string]string{"tool-root": os.Getenv("YSS_LEGACY_ORACLE_ROOT")}); err == nil {
		t.Fatal("public task gained terminal recovery permission")
	} else {
		t.Logf("expected public task refusal retains legacy terminal scope: %v", err)
	}
	if _, err = apTestRun(root, "backend-completed-producer-task", ref, nil); err == nil {
		t.Fatal("internal audit kind became public")
	} else {
		t.Logf("expected public internal-kind refusal: %v", err)
	}
	for _, name := range []string{"active", "paused", "failed", "wrong-unit", "non-formal-contract", "stale-contract", "blocked-contract", "nonempty-next", "wrong-checkpoint", "wrong-result-checkpoint", "stale-evidence", "wrong-current-status", "missing-terminal-evidence", "missing-independent-review"} {
		t.Run(name, func(t *testing.T) {
			var task map[string]any
			decoder := json.NewDecoder(strings.NewReader(string(baseline)))
			decoder.UseNumber()
			if err := decoder.Decode(&task); err != nil {
				t.Fatal(err)
			}
			result := apMap(task["result"])
			var extra map[string]string
			switch name {
			case "active", "paused", "failed":
				task["workflow_status"] = name
			case "wrong-unit":
				task["work_unit_id"] = "work-unit.plan-opportunity"
			case "non-formal-contract":
				apMap(task["contract"])["kind"] = "read-only-intake"
			case "stale-contract", "blocked-contract":
				apMap(task["contract"])["status"] = strings.TrimSuffix(name, "-contract")
			case "nonempty-next":
				result["next_route"] = "work-unit.code-review"
			case "wrong-checkpoint":
				extra = map[string]string{"checkpoint": "wrong-current-checkpoint.json"}
			case "wrong-result-checkpoint":
				result["checkpoint_ref"] = "wrong-current-checkpoint.json"
			case "wrong-current-status":
				result["result"] = "blocked"
			case "stale-evidence":
				apMap(apArray(task["verification_results"])[0])["evidence_digest"] = "sha256:" + strings.Repeat("0", 64)
			case "missing-terminal-evidence":
				task["expected_evidence_files"] = []any{"CONTEXT.md", "delivery.json", "review-state.json"}
			case "missing-independent-review":
				task["expected_evidence_files"] = []any{"CONTEXT.md", "delivery.json", ".yss-backend-delivery.json"}
			}
			apTestPut(t, root, ref, task)
			if err := run(extra); err == nil {
				t.Fatalf("audit accepted %s", name)
			} else {
				t.Logf("expected refusal: %v", err)
			}
			apTestPut(t, root, ref, baseline)
		})
	}
	// Current delivery bytes, rather than the task's self-described evidence,
	// control terminal validity. Restore only this synthetic fixture afterwards.
	delivery, err := os.ReadFile(filepath.Join(root, "delivery.json"))
	if err != nil {
		t.Fatal(err)
	}
	apTestPut(t, root, "delivery.json", append(append([]byte(nil), delivery...), byte('\n')))
	if err = run(nil); err == nil {
		t.Fatal("audit accepted current delivery byte drift")
	}
	apTestPut(t, root, "delivery.json", delivery)
	if err = run(nil); err != nil {
		t.Fatal("restored current producer no longer validates", err)
	}
}

func taskTestLegacyCurrentProfile(t *testing.T, root string) (bool, string, bool) {
	t.Helper()
	source := os.Getenv("YSS_LEGACY_ORACLE_ROOT")
	if source == "" {
		return false, "", false
	}
	// The original task CLI has no --root. Use its published API with the
	// current profile's independent role/registry inputs and explicit IO root.
	script := `const fs=await import('node:fs');const path=await import('node:path');const {validateTaskPackage}=await import(process.argv[1]);const {parseDocument}=await import(process.argv[2]);const root=process.argv[3];const doc=ref=>parseDocument(fs.readFileSync(path.join(root,ref),'utf8')).toJS();try{validateTaskPackage(doc('profile-task.json'),{root,rolesDoc:doc('.template-spec/agents/digital-human-roles.yaml'),lifecycleDoc:doc('.template-spec/process/lifecycle-registry.yaml')});console.log('fixed task API current profile passed');}catch(error){console.error(error.message);process.exitCode=1;}`
	argv := []string{"--input-type=module", "-e", script, "file://" + filepath.Join(source, "scripts/lib/task-package.mjs"), "file://" + filepath.Join(source, "scripts/vendor/yaml.mjs"), root}
	cmd := exec.Command("node", argv...)
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		exitCode = 1
	}
	record, errJSON := json.Marshal(map[string]any{"test": t.Name(), "source": source, "argv": argv, "exit_code": exitCode, "raw_output": string(out), "interface": "validateTaskPackage(value,{root,rolesDoc,lifecycleDoc})"})
	if errJSON != nil {
		t.Fatal(errJSON)
	}
	t.Logf("legacy-oracle-record %s", record)
	return err == nil, string(out), true
}

func apTestTask(t *testing.T, root string, version int) map[string]any {
	t.Helper()
	s := apTestSession(t, root)
	role := taskRole(s, "role.backend-engineer")
	task := map[string]any{"schema_version": version, "task_id": "synthetic.task", "work_unit_id": "work-unit.technical-analysis", "actor_id": "agent.synthetic", "role_id": "role.backend-engineer", "runtime_id": "runtime.generic", "execution_state": "Worker", "workflow_status": "active", "stage_id": "stage.system-data-engineering", "skill_source": map[string]any{"registry_ref": approvalRolesRef, "defaults_ref": "taskPackageDefaults(role.backend-engineer)", "core_skills": role["core_skills"], "forbidden_skills": role["forbidden_skills"]}, "contract": map[string]any{"kind": "lifecycle-work-unit", "contract_id": "synthetic.contract", "contract_version": 1, "status": "issued", "contract_ref": "AGENTS.md", "lifecycle_ref": approvalRegistryRef}, "inputs": []any{"CONTEXT.md"}, "objective": "合成测试：校验公开只读治理入口", "allowed_write_paths": []any{"audit-output.json"}, "forbidden_actions": []any{"commit", "publish"}, "expected_outputs": []any{"合成报告"}, "expected_evidence_files": []any{"AGENTS.md"}, "verification_commands": []any{"fixed-test-command"}, "verification_results": []any{}, "downstream_consumers": []any{"root.test"}, "convergence": map[string]any{"parent_work_unit": "work-unit.technical-analysis", "convergence_ref": "AGENTS.md"}}
	if version == 2 {
		task["work_unit_id"] = "work-unit.entry-triage"
		task["stage_id"] = "stage.entry-triage"
		task["execution_state"] = "Explorer"
		task["verification_status"] = "not-executed"
		task["allowed_write_paths"] = []any{}
		task["verification_commands"] = []any{}
		task["contract"] = map[string]any{"kind": "read-only-intake", "contract_id": "synthetic.contract", "contract_version": 1, "status": "issued", "contract_ref": "AGENTS.md"}
		task["convergence"] = map[string]any{"parent_work_unit": "work-unit.entry-triage", "convergence_ref": "AGENTS.md"}
	}
	return task
}

func TestTaskFourProfilesCurrentReadOnlyAndRefusal(t *testing.T) {
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		t.Run(profile, func(t *testing.T) {
			root := apTestProfileRoot(t, profile)
			s := apTestSession(t, root)
			identity, e := s.doc(".template-spec/process/harness-profile.yaml")
			if e != nil {
				t.Fatal(e)
			}
			roleID := "role.requirements-manager"
			for _, audienceRole := range apStrings(apMap(identity["audience"])["target_user_roles"]) {
				if taskRole(s, audienceRole) != nil {
					roleID = audienceRole
					break
				}
			}
			role := taskRole(s, roleID)
			if role == nil {
				t.Fatal("profile has no current intake audience role")
			}
			task := apTestTask(t, root, 2)
			if apFind(s.registry["work_units"], "id", "work-unit.entry-triage") == nil {
				if apFind(s.registry["work_units"], "id", "work-unit.harness-entry") == nil {
					t.Fatal("profile does not publish either supported readonly intake unit")
				}
				task["work_unit_id"] = "work-unit.harness-entry"
				task["stage_id"] = "stage.harness-entry"
				apMap(task["convergence"])["parent_work_unit"] = "work-unit.harness-entry"
			}
			task["role_id"] = roleID
			task["skill_source"] = map[string]any{"registry_ref": approvalRolesRef, "defaults_ref": "taskPackageDefaults(" + roleID + ")", "core_skills": role["core_skills"], "forbidden_skills": role["forbidden_skills"]}
			apTestPut(t, root, "profile-task.json", task)
			t.Logf("profile-task role=%s work_unit=%s contract=read-only-intake verification=not-executed; product implementation/acceptance gates not applicable to Explorer", roleID, task["work_unit_id"])
			if _, e := apTestRun(root, "task", "profile-task.json", nil); e != nil {
				t.Fatal(e)
			}
			apTestExportNativeFixture(t, root, profile, "task", "positive", []string{"contract", "verify", "--kind", "task", "--file", "profile-task.json"}, 0)
			if old, out, ran := taskTestLegacyCurrentProfile(t, root); ran && !old {
				t.Fatalf("fixed source rejected profile readonly task: %s", out)
			}
			task["allowed_write_paths"] = []any{"src/unauthorized.go"}
			apTestPut(t, root, "profile-task.json", task)
			if _, e := apTestRun(root, "task", "profile-task.json", nil); e == nil {
				t.Fatal("profile readonly intake gained write permission")
			}
			apTestExportNativeFixture(t, root, profile, "task", "refusal", []string{"contract", "verify", "--kind", "task", "--file", "profile-task.json"}, 1)
			if old, out, ran := taskTestLegacyCurrentProfile(t, root); ran && old {
				t.Fatalf("fixed source accepted profile readonly write permission: %s", out)
			}
		})
	}
}

func TestTaskV2NativeReadOnlyAndBoundaries(t *testing.T) {
	for _, name := range []string{"active-readonly", "write-permission", "worker-state", "source-skills-changed", "fake-completed", "undeclared-result", "stale-unpaused", "unknown-runtime", "wrong-unit"} {
		t.Run(name, func(t *testing.T) {
			root := apTestRoot(t)
			task := apTestTask(t, root, 2)
			switch name {
			case "write-permission":
				task["allowed_write_paths"] = []any{"src/business.go"}
			case "worker-state":
				task["execution_state"] = "Worker"
			case "source-skills-changed":
				apMap(task["skill_source"])["core_skills"] = []any{"implement"}
			case "fake-completed":
				task["workflow_status"] = "resolved"
				task["result"] = map[string]any{"result": "completed"}
			case "undeclared-result":
				task["verification_status"] = "executed"
				task["verification_results"] = []any{map[string]any{"command": "unlisted", "exit_code": 0, "executed_at": "now", "evidence_ref": "AGENTS.md"}}
			case "stale-unpaused":
				apMap(task["contract"])["status"] = "stale"
			case "unknown-runtime":
				task["runtime_id"] = "runtime.unknown"
			case "wrong-unit":
				task["work_unit_id"] = "work-unit.technical-analysis"
			}
			apTestPut(t, root, "task.json", task)
			result, err := apTestRun(root, "task", "task.json", nil)
			if (err == nil) != (name == "active-readonly") {
				t.Fatalf("%s: %v", name, err)
			}
			if err == nil {
				r := result.(*SemanticReport)
				if !r.ReadOnly || r.ApprovalCreated || r.ExecutionAuthorization != "not-evaluated" {
					t.Fatalf("authorization leak: %+v", r)
				}
			}
		})
	}
}

func TestTaskV1ContractCannotPretendSliceOrOutrunPermission(t *testing.T) {
	for _, name := range []string{"valid-active", "slice-disguised", "out-of-root", "result-unallowed-file", "stale-active", "history-product-instance", "completed-no-evidence"} {
		t.Run(name, func(t *testing.T) {
			root := apTestRoot(t)
			task := apTestTask(t, root, 1)
			extra := map[string]string{}
			// The fixture uses the template-source harness to exercise v1 common rules
			// without manufacturing product tracking/approval assets.
			apTestPut(t, root, "yss-project.yaml", map[string]any{"schema_version": 1, "repository_mode": "template-source"})
			switch name {
			case "slice-disguised":
				task["work_unit_id"] = "work-unit.slice-implementation"
			case "out-of-root":
				task["allowed_write_paths"] = []any{"../business.go"}
			case "result-unallowed-file":
				task["result"] = map[string]any{"result": "blocked", "changed_files": []any{"business.go"}}
			case "stale-active":
				apMap(task["contract"])["status"] = "stale"
			case "history-product-instance":
				extra["history"] = "true"
				apTestPut(t, root, "yss-project.yaml", map[string]any{"schema_version": 1, "repository_mode": "project-instance"})
			case "completed-no-evidence":
				task["workflow_status"] = "resolved"
				task["result"] = map[string]any{"result": "completed"}
			}
			apTestPut(t, root, "task.json", task)
			_, err := apTestRun(root, "task", "task.json", extra)
			if (err == nil) != (name == "valid-active") {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
}

func TestReviewCapabilityCompilerUsesPolicyNotTitle(t *testing.T) {
	root := apTestRoot(t)
	s := apTestSession(t, root)
	compiled, err := compileReviewCapabilitiesSemantic(s, []any{"check.domain-strategy-approved", "check.stage-decision-package-approved"}, "role.product-manager", "Reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if !apEqual(compiled.capabilities, []string{"capability.business-rules"}) || !apEqual(compiled.skills, []string{"domain-modeling", "yss-stage-decision"}) || !apEqual(compiled.stages, []string{"stage.plan"}) {
		t.Fatalf("unexpected compilation: %+v", compiled)
	}
	_, err = compileReviewCapabilitiesSemantic(s, []any{"check.domain-strategy-approved"}, "role.requirements-manager", "Reviewer")
	if err == nil {
		t.Fatal("drafter title passed without review capability")
	}
	_, err = compileReviewCapabilitiesSemantic(s, []any{"check.domain-strategy-approved"}, "role.product-manager", "Worker")
	apTestCode(t, err, "REVIEW_CAPABILITY_STATE")
	_, err = compileReviewCapabilitiesSemantic(s, []any{"check.domain-strategy-approved", "check.domain-strategy-approved"}, "role.product-manager", "Reviewer")
	if err == nil {
		t.Fatal("duplicate current check passed")
	}
	_, err = compileReviewCapabilitiesSemantic(s, []any{"check.unregistered"}, "role.product-manager", "Reviewer")
	if err == nil {
		t.Fatal("unclassified check passed")
	}
}

func apTestFormalReview(t *testing.T, root string) (map[string]any, map[string]any, map[string]any, string) {
	t.Helper()
	s := apTestSession(t, root)
	boundary := "check.architecture-reviewed"
	roleID := "role.test-engineer"
	compiled, e := compileReviewCapabilitiesSemantic(s, []any{boundary}, roleID, "Reviewer")
	if e != nil {
		t.Fatal(e)
	}
	basis := []any{map[string]any{"ref": "review-basis.json", "digest": safefs.Digest(apTestPut(t, root, "review-basis.json", map[string]any{"test_only": true}))}}
	subjectDigest := safefs.Digest(apTestPut(t, root, "review-subject.json", map[string]any{"gate_id": boundary, "basis": basis, "drafter_principal_ref": "agent.drafter"}))
	row := map[string]any{"boundary": boundary, "subject_ref": "review-subject.json", "subject_digest": subjectDigest, "approval_scope": []any{"unit.demo"}, "basis": basis, "drafter_principal_ref": "agent.drafter"}
	state := apCopy(row)
	delete(state, "boundary")
	state["approval_ref"] = "review-approval.json"
	cp := apTestCheckpoint(map[string]any{boundary: state})
	apTestPut(t, root, "review-checkpoint.json", cp)
	rows := []any{row}
	checkpointDigest, e := semanticDocumentDigest(rows)
	if e != nil {
		t.Fatal(e)
	}
	candidate := map[string]any{"schema_version": 1, "kind": "review-subject", "current_approvals": rows, "source_checkpoint": map[string]any{"ref": "review-checkpoint.json", "binding_kind": "current-approvals-v1", "digest": checkpointDigest}}
	candidateDigest := safefs.Digest(apTestPut(t, root, "review-candidate.json", candidate))
	policy, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(approvalRolesRef)))
	if e != nil {
		t.Fatal(e)
	}
	rc := map[string]any{"implementation_actor_id": "agent.implementer", "check_ids": []any{boundary}, "capability_ids": compiled.capabilities, "candidate_ref": "review-candidate.json", "candidate_digest": candidateDigest, "policy_ref": approvalRolesRef, "policy_digest": safefs.Digest(policy), "reviewer_principal_ref": "agent.reviewer", "drafter_principal_ref": "agent.drafter", "approval_scope": []any{"unit.demo"}, "basis": basis, "current_approvals": rows}
	task := apTestTask(t, root, 1)
	role := taskRole(s, roleID)
	task["task_id"] = "synthetic.review"
	task["role_id"] = roleID
	task["actor_id"] = "agent.reviewer"
	task["execution_state"] = "Reviewer"
	task["stage_id"] = compiled.stages[0]
	task["review_context"] = rc
	task["checkpoint_ref"] = "review-checkpoint.json"
	task["skill_source"] = map[string]any{"registry_ref": approvalRolesRef, "defaults_ref": "taskPackageDefaults(" + roleID + ")", "core_skills": role["core_skills"], "forbidden_skills": role["forbidden_skills"], "review_skills": compiled.skills}
	task["contract"] = map[string]any{"kind": "lifecycle-work-unit", "contract_id": "synthetic.review-contract", "contract_version": 1, "status": "issued", "contract_ref": "review-candidate.json", "lifecycle_ref": "review-checkpoint.json"}
	task["convergence"] = map[string]any{"parent_work_unit": "work-unit.technical-analysis", "convergence_ref": "review-checkpoint.json"}
	task["allowed_write_paths"] = []any{"review-output.json"}
	task["inputs"] = []any{"review-checkpoint.json", approvalRolesRef, "review-candidate.json", "review-basis.json", "review-subject.json"}
	digest := safefs.Digest(apTestPut(t, root, "formal-review-task.json", task))
	expected := apCopy(row)
	expected["review_package"] = false
	expectedContext := apCopy(rc)
	expectedContext["review_task_ref"] = "formal-review-task.json"
	expectedContext["review_task_digest"] = digest
	expected["review_context"] = expectedContext
	state["review_context"] = expectedContext
	apTestPut(t, root, "review-checkpoint.json", cp)
	record := map[string]any{"schema_version": 2, "gate_id": boundary, "decision": "approved", "actor_kind": "digital-human", "role_id": roleID, "runtime_id": "runtime.generic", "principal_ref": "agent.reviewer", "drafter_principal_ref": "agent.drafter", "subject_ref": row["subject_ref"], "subject_digest": subjectDigest, "approval_scope": row["approval_scope"], "basis": basis, "review_task_ref": "formal-review-task.json", "review_task_digest": digest, "capability_ids": compiled.capabilities}
	apTestPut(t, root, "review-approval.json", record)
	return task, record, expected, boundary
}
func TestApprovalV2FormalTaskConsumerCurrentBinding(t *testing.T) {
	for _, name := range []string{"current", "explicit-task-current", "record-selects-other-task", "task-byte-drift", "self-review", "candidate-byte-drift", "input-write-scope", "wrong-capability"} {
		t.Run(name, func(t *testing.T) {
			root := apTestRoot(t)
			task, record, _, _ := apTestFormalReview(t, root)
			extra := map[string]string{"checkpoint": "review-checkpoint.json"}
			switch name {
			case "explicit-task-current":
				extra = map[string]string{"task": "formal-review-task.json"}
			case "record-selects-other-task":
				record["review_task_ref"] = "other-task.json"
			case "task-byte-drift":
				task["objective"] = "changed task"
				apTestPut(t, root, "formal-review-task.json", task)
			case "self-review":
				record["principal_ref"] = record["drafter_principal_ref"]
			case "candidate-byte-drift":
				apTestPut(t, root, "review-candidate.json", `{"changed":true}`)
			case "input-write-scope":
				task["allowed_write_paths"] = []any{"review-basis.json"}
				apTestPut(t, root, "formal-review-task.json", task)
			case "wrong-capability":
				record["capability_ids"] = []any{"capability.business-rules"}
			}
			apTestPut(t, root, "review-approval.json", record)
			_, err := apTestRun(root, "approval", "review-approval.json", extra)
			want := name == "current" || name == "explicit-task-current"
			if (err == nil) != want {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
}

func TestSemanticTaskAndApprovalCancellationIsError(t *testing.T) {
	root := apTestRoot(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RunContext(ctx, "contract", "verify", root, map[string]string{"kind": "task", "file": "missing.json"})
	apTestCode(t, err, "CANCELLED")
	// A digest's invalid source never counts as the empty source.
	s := apTestSession(t, root)
	_, err = approvalExpectedFromTaskSemantic(s, "missing.json", map[string]any{}, "check.architecture-reviewed", strings.Repeat("a", 64), nil)
	if err == nil {
		t.Fatal("missing formal task passed")
	}
}

func TestTaskSetCouplingPreservesSliceIdentityAndIndependence(t *testing.T) {
	root := apTestRoot(t)
	s := apTestSession(t, root)
	worker := map[string]any{"actor_id": "actor.worker", "execution_state": "Worker", "contract": map[string]any{"kind": "slice-implementation", "contract_id": "slice.demo", "contract_version": 1}}
	reviewer := apTestJSONClone(t, worker)
	reviewer["execution_state"] = "Reviewer"
	reviewer["actor_id"] = "actor.review"
	if err := validateTaskActorSetSemantic(s, []map[string]any{worker, reviewer}); err != nil {
		t.Fatal(err)
	}
	reviewer["actor_id"] = worker["actor_id"]
	apTestCode(t, validateTaskActorSetSemantic(s, []map[string]any{worker, reviewer}), "REVIEW_NOT_INDEPENDENT")
	reviewer["actor_id"] = "actor.review"
	apMap(reviewer["contract"])["contract_version"] = 2
	apTestCode(t, validateTaskActorSetSemantic(s, []map[string]any{worker, reviewer}), "TASK_SET_CONTRACT")
	apTestCode(t, validateTaskPackageSetSemantic(s, nil), "TASK_SET")
}
