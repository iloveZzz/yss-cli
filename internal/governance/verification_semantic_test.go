package governance

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

func TestVerificationExecutionOldOracleBindings(t *testing.T) {
	if _, e := exec.LookPath("node"); e != nil {
		t.Skip("development-only fixed old oracle unavailable")
	}
	old := governanceOracleRoot(t)
	producer := `import fs from 'node:fs';import path from 'node:path';import {crossRepoFixture} from './scripts/fixtures/slice-contract-v3/cross-repo-fixture.mjs';import {bindSyntheticEvidence} from './scripts/fixtures/slice-contract-v3/evidence-fixture.mjs';import {validateExecutionResult,loadCompilerContract} from './scripts/lib/implementation-contract-compiler.mjs';import {loadSkillRegistry} from './scripts/lib/skill-registry.mjs';
const f=crossRepoFixture();const a=f.approve();const result={schema_version:2,status:'implemented',work_unit_id:'work-unit.slice-backend',architecture_identity:f.identity,consumed_contract:{contract_id:f.contract.contract_id,contract_version:f.contract.contract_version,registry_digest:f.contract.resolution.registry_digest,compiler_contract_digest:f.contract.resolution.compiler_contract_digest,component_bindings_digest:f.contract.resolution.component_bindings_digest},changed_files:[{path:'src/main/java/Example.java',project_root:f.project}],verification_results:[{command:'./mvnw test',cwd:f.project,exit_code:0,executed_at:'2026-09-16T00:00:00Z'},{command:'node integration.mjs',cwd:f.project,dependency_roots:[f.peer],exit_code:0,executed_at:'2026-09-16T00:00:00Z'}],new_impacts:[]};
f.write('project/results/test.log','Synthetic actual-exit mechanism evidence; no Maven run.');f.write('project/results/joint.log','Synthetic joint evidence; no command run.');const current={root:f.root,registry:loadSkillRegistry(),compilerContract:loadCompilerContract(),approved_slice:a.binding};bindSyntheticEvidence(result,f.contract,current);const checks={valid:result,architecture:{...result,architecture_identity:{...result.architecture_identity,project_id:'different-project'}},dependency:{...result,verification_results:result.verification_results.map(row=>({...row,dependency_roots:[]}))}};const outcomes=Object.fromEntries(Object.entries(checks).map(([key,value])=>[key,validateExecutionResult(value,f.contract,current).status]));f.write('execution.json',JSON.stringify(result));console.log(JSON.stringify({root:f.root,ref:a.binding.ref,approval:a.binding.approval_ref,outcomes,has_components:!!f.contract.resolution.component_bindings_digest}));`
	cmd := exec.Command("node", "--input-type=module", "-e", producer, "synthetic-oracle")
	cmd.Dir = old
	tmp, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp)
	raw, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("fixed old execution producer: %v %s", e, raw)
	}
	var meta struct {
		Root          string            `json:"root"`
		Ref           string            `json:"ref"`
		Approval      string            `json:"approval"`
		Outcomes      map[string]string `json:"outcomes"`
		HasComponents bool              `json:"has_components"`
	}
	if e = json.Unmarshal(raw, &meta); e != nil {
		t.Fatalf("%v %s", e, raw)
	}
	t.Cleanup(func() { _ = os.RemoveAll(meta.Root) })
	if meta.Outcomes["valid"] != "accepted" || meta.Outcomes["architecture"] == "accepted" || meta.Outcomes["dependency"] == "accepted" {
		t.Fatalf("fixed old oracle outcomes: %+v", meta)
	}
	_ = apTestRoot(t)
	for ref, f := range apFixtureBundle.Files {
		if ref == ".template-spec/agents/issue-tracker.md" || !(strings.HasPrefix(ref, ".agents/skills/") || strings.HasPrefix(ref, ".template-spec/") || strings.HasPrefix(ref, "scripts/lib/")) {
			continue
		}
		if _, e = os.Stat(filepath.Join(meta.Root, filepath.FromSlash(ref))); e == nil {
			continue
		}
		b, e := f.Render(map[string]string{"projectName": "synthetic", "businessDomain": "test-only", "teamSize": "2"})
		if e != nil {
			t.Fatal(e)
		}
		apTestPut(t, meta.Root, ref, b)
	}
	apTestPut(t, meta.Root, ".template-spec/process/harness-profile.yaml", map[string]any{"schema_version": 2, "profile_id": "harness.spec-template"})
	for _, kind := range []string{"valid", "architecture", "dependency", "component-key"} {
		t.Run(kind, func(t *testing.T) {
			s := apTestSession(t, meta.Root)
			s.args["tool-root"] = old
			result, e := s.doc("execution.json")
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "architecture":
				semMap(result["architecture_identity"])["project_id"] = "different-project"
			case "dependency":
				semMap(semList(result["verification_results"])[1])["dependency_roots"] = []any{}
			case "component-key":
				if !meta.HasComponents {
					t.Skip("fixed fixture has no component closure")
				}
				delete(semMap(result["consumed_contract"]), "component_bindings_digest")
			}
			e = contractExecutionResult(s, result, map[string]string{"contract": meta.Ref, "approval-ref": meta.Approval})
			if (e == nil) != (kind == "valid") {
				t.Fatalf("native %s: %v", kind, e)
			}
			if e == nil {
				if e = s.finish(); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}

// The recorded exits/logs below are synthetic; verification never executes them.
func verificationFrontendFixture(t *testing.T) (string, string, map[string]any, map[string]any, func()) {
	t.Helper()
	root := contractTestRetainedRoot(t, apTestRoot(t), "frontend-verification")
	contractTestRules(t, root)
	project := filepath.Join(root, "app")
	contractRef := "docs/.scratch/frontend/scaffold-contract.json"
	manifestRef := "app/.yss/scaffold-generation.json"
	reportRef := "evidence/scaffold-verification.json"
	baseline, e := os.ReadFile(filepath.Join(root, ".agents/skills/yss-frontend-scaffold-generator/references/data-quality-v1.manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	commands := []any{"pnpm install --frozen-lockfile", "pnpm lint:check", "pnpm type-check", "pnpm build", "pnpm build:standalone"}
	contract := map[string]any{
		"schema_version": 4, "kind": "project-scaffold-contract", "contract_id": "frontend.synthetic.v1", "contract_version": "1", "status": "approved", "persisted_ref": contractRef, "current_version": true,
		"delivery_role": "frontend", "scaffold_kind": "frontend-yss-vue3", "generator_skill": "yss-frontend-scaffold-generator", "implementation_repository": project, "target_output_dir": project, "repository_scope": "external-repository", "init_git": false,
		"frontend":            map[string]any{"app_name": "synthetic-app", "microapp_name": "synthetic", "base_route": "/synthetic", "package_manager": "pnpm", "template": map[string]any{"kind": "bundled", "baseline_id": "data-quality-v1", "manifest_digest": "sha256:" + safefs.Digest(baseline)}, "openapi_impact": "not-applicable", "openapi_not_applicable_reason": "Synthetic static baseline"},
		"allowed_write_paths": []any{project}, "expected_evidence_files": []any{".yss/scaffold-generation.json"}, "verification_commands": commands, "approval": map[string]any{"approval_ref": "decision://synthetic", "approver": "synthetic-human"},
		"generation_policy": map[string]any{"mode": "initialize-only", "existing_target": "unsupported", "old_project_migration": "unsupported", "template_upgrade": "unsupported"},
	}
	bytes := apTestPut(t, root, contractRef, contract)
	apTestPut(t, root, "app/pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
	manifest := map[string]any{"schema_version": 4, "kind": "frontend-scaffold", "generation_mode": "controlled-generation", "contract_id": contract["contract_id"], "contract_version": contract["contract_version"], "contract_file_ref": filepath.Join(root, contractRef), "contract_digest": "sha256:" + safefs.Digest(bytes), "target_output_dir": project, "verification_commands": commands, "generated_files": []any{"pnpm-lock.yaml"}, "template": semMap(semMap(contract["frontend"])["template"])}
	rows := []any{}
	for i, command := range commands {
		stdout := filepath.Join(root, "evidence", strings.TrimPrefix(text(command), "pnpm ")+"-stdout.log")
		stderr := filepath.Join(root, "evidence", strings.TrimPrefix(text(command), "pnpm ")+"-stderr.log")
		// Log names are opaque persisted paths; command strings are never interpreted.
		stdout = filepath.Join(root, "evidence", string(rune('1'+i))+"-stdout.log")
		stderr = filepath.Join(root, "evidence", string(rune('1'+i))+"-stderr.log")
		apTestPut(t, root, filepath.ToSlash(strings.TrimPrefix(stdout, root+string(filepath.Separator))), "synthetic actual stdout\n")
		apTestPut(t, root, filepath.ToSlash(strings.TrimPrefix(stderr, root+string(filepath.Separator))), "")
		rows = append(rows, map[string]any{"command": command, "exit_code": 0, "executed_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), "stdout_ref": stdout, "stderr_ref": stderr})
	}
	report := map[string]any{"schema_version": 1, "kind": "frontend-scaffold-verification", "status": "passed", "project_root": project, "scaffold_manifest_ref": filepath.Join(root, manifestRef), "commands": rows}
	save := func() { apTestPut(t, root, manifestRef, manifest); apTestPut(t, root, reportRef, report) }
	save()
	return root, reportRef, manifest, report, save
}

func verificationTree(t *testing.T, root string) map[string]string {
	t.Helper()
	rows := map[string]string{}
	if e := filepath.WalkDir(root, func(file string, d fs.DirEntry, e error) error {
		if e != nil || d.IsDir() {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			target, e := os.Readlink(file)
			if e != nil {
				return e
			}
			ref, e := filepath.Rel(root, file)
			if e != nil {
				return e
			}
			rows[filepath.ToSlash(ref)] = "symlink:" + target
			return nil
		}
		b, e := os.ReadFile(file)
		if e != nil {
			return e
		}
		ref, e := filepath.Rel(root, file)
		rows[ref] = safefs.Digest(b)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	return rows
}

func TestVerificationFrontendScaffoldRecordedContract(t *testing.T) {
	root, ref, _, _, _ := verificationFrontendFixture(t)
	before := verificationTree(t, root)
	t.Setenv("PATH", "")
	if _, e := RunContext(context.Background(), "evidence", "verify", root, map[string]string{"kind": "verification", "file": ref}); e != nil {
		t.Fatal(e)
	}
	contractTestRetainFixture(t, root, "frontend-scaffold-verification", map[string]any{"group": "evidence", "action": "verify", "kind": "verification", "file": ref, "expected_exit": 0})
	if !contractSame(before, verificationTree(t, root)) {
		t.Fatal("read-only verification changed files")
	}
}

func TestVerificationFrontendScaffoldRejectsIndependentFindings(t *testing.T) {
	for _, mutate := range []string{"empty-manifest", "arbitrary-command", "future-time", "missing-logs", "wrong-project", "stale-contract", "exit-string", "unsafe-log"} {
		t.Run(mutate, func(t *testing.T) {
			root, ref, manifest, report, save := verificationFrontendFixture(t)
			switch mutate {
			case "empty-manifest":
				for k := range manifest {
					delete(manifest, k)
				}
			case "arbitrary-command":
				manifest["verification_commands"] = []any{"echo skipped"}
				report["commands"] = []any{map[string]any{"command": "echo skipped", "exit_code": 0}}
			case "future-time":
				semMap(semList(report["commands"])[0])["executed_at"] = "2099-01-01T00:00:00Z"
			case "missing-logs":
				delete(semMap(semList(report["commands"])[0]), "stdout_ref")
			case "wrong-project":
				report["project_root"] = root
			case "stale-contract":
				manifest["contract_digest"] = "sha256:" + strings.Repeat("0", 64)
			case "exit-string":
				semMap(semList(report["commands"])[0])["exit_code"] = "0"
			case "unsafe-log":
				semMap(semList(report["commands"])[0])["stdout_ref"] = "../escape.log"
			}
			save()
			before := verificationTree(t, root)
			if e := apTestSession(t, root).verify("verification", ref, nil); e == nil {
				t.Fatal("invalid persisted report passed")
			}
			if !contractSame(before, verificationTree(t, root)) {
				t.Fatal("rejection changed files")
			}
		})
	}
}

func TestVerificationCancelledAndDrift(t *testing.T) {
	root, ref, _, report, save := verificationFrontendFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := newSemanticSession(ctx, root, nil).verify("verification", ref, nil); e == nil {
		t.Fatal("cancelled validation passed")
	}
	s := apTestSession(t, root)
	if e := s.verify("verification", ref, nil); e != nil {
		t.Fatal(e)
	}
	semMap(semList(report["commands"])[0])["executed_at"] = time.Now().Format(time.RFC3339Nano)
	save()
	if e := s.finish(); e == nil {
		t.Fatal("changed report passed final observation")
	}
}

func TestVerificationDeletedSourceRequiresExplicitNull(t *testing.T) {
	for _, kind := range []string{"valid", "missing-digest", "non-null-digest", "missing-dependency", "external-missing-digest"} {
		t.Run(kind, func(t *testing.T) {
			root := apTestRoot(t)
			proof := apTestPut(t, root, "proof.log", "Synthetic evidence; never a tool run")
			apTestPut(t, root, "slice.json", map[string]any{"fixture": true})
			s := apTestSession(t, root)
			bound, e := s.bind("slice.json")
			if e != nil {
				t.Fatal(e)
			}
			unitRoot := root
			if kind == "external-missing-digest" {
				unitRoot, e = filepath.EvalSymlinks(t.TempDir())
				if e != nil {
					t.Fatal(e)
				}
				if e = s.registerExternalRoot(unitRoot, "slice.json"); e != nil {
					t.Fatal(e)
				}
			}
			check := map[string]any{"id": "verify.synthetic", "command": "go test ./...", "cwd": ".", "acceptance_refs": []any{"AC-1"}, "expected_evidence": []any{"proof.log"}, "dependency_roots": []any{unitRoot}}
			row := map[string]any{"verification_id": "verify.synthetic", "command": "go test ./...", "cwd": ".", "exit_code": 0, "executed_at": "2026-10-05T00:00:00Z", "acceptance_refs": []any{"AC-1"}, "evidence_refs": []any{"proof.log"}, "dependency_roots": []any{unitRoot}}
			source := map[string]any{"path": "deleted.txt", "deleted": true, "digest": nil}
			switch kind {
			case "missing-digest", "external-missing-digest":
				delete(source, "digest")
			case "non-null-digest":
				source["digest"] = "sha256:" + strings.Repeat("0", 64)
			case "missing-dependency":
				delete(row, "dependency_roots")
			}
			result := map[string]any{"evidence_binding_version": 1, "consumed_contract": map[string]any{"contract_digest": bound.Digest}, "verification_results": []any{row}, "evidence_files": []any{map[string]any{"path": "proof.log", "digest": "sha256:" + safefs.Digest(proof), "behavior_ref": "AC-1"}}, "source_bindings": []any{source}, "changed_files": []any{"deleted.txt"}}
			apTestPut(t, root, "result.json", result)
			parsed, e := s.doc("result.json")
			if e != nil {
				t.Fatal(e)
			}
			e = contractExecutionEvidence(s, parsed, &nativeSlice{Ref: "slice.json", Repositories: map[string]map[string]any{}}, map[string]any{"project_root": unitRoot, "verification": []any{check}})
			if (e == nil) != (kind == "valid") {
				t.Fatalf("%s: %v", kind, e)
			}
			if e == nil {
				if e = s.finish(); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}

func verificationTestFormalConsumer(t *testing.T, root, typedRef string) (string, string) {
	t.Helper()
	for _, ref := range []string{".template-spec/agents/issue-tracker.md", ".agents/skills/yss-product-lifecycle/references/orchestration-contract.yaml"} {
		if _, e := os.Stat(filepath.Join(root, ref)); e == nil {
			continue
		}
		f, exists := apFixtureBundle.Files[ref]
		if !exists {
			t.Fatalf("published consumer rules missing %s", ref)
		}
		raw, e := f.Render(map[string]string{"projectName": "synthetic", "businessDomain": "test-only", "teamSize": "2"})
		if e != nil {
			t.Fatal(e)
		}
		apTestPut(t, root, ref, raw)
	}
	s := apTestSession(t, root)
	layout, e := viewWorkLayout(s.v)
	if e != nil {
		t.Fatal(e)
	}
	roleID := "role.project-manager"
	role := taskRole(s, roleID)
	if role == nil {
		t.Fatal("fixture source lacks current project-manager role")
	}
	cpRef := layout.Root + "/report-consumer/checkpoint.json"
	taskRef := layout.Root + "/report-consumer/task.json"
	cp := apTestCheckpoint(map[string]any{})
	cp["feature_id"] = "report-consumer"
	cp["stage"], cp["mode"], cp["next_work_unit"] = "stage.plan", "orchestrate", "work-unit.plan-requirements"
	cp["phase_boundary"] = map[string]any{"decision": "continue", "reason": "Synthetic completed current Plan task; does not authorize product work"}
	cp["stage_trace"] = map[string]any{"stage": "stage.plan", "upstream_refs": []any{"CONTEXT.md"}, "artifact_refs": []any{typedRef}, "gate_decisions": []any{}, "downstream_impacts": []any{}, "completed_work_unit": "work-unit.plan-opportunity"}
	cp["context_reconciliation"] = map[string]any{"status": "reconciled", "ref": "CONTEXT.md", "evidence_refs": []any{"CONTEXT.md"}}
	apTestPut(t, root, cpRef, cp)
	proof := apTestPut(t, root, ".template-source/report-consumer/check.log", "Synthetic formal task actual-exit fixture only; no command executed.")
	result := map[string]any{"result_schema": "workflow-execution-result-v1", "work_unit": "work-unit.entry-triage", "workflow_reference": "AGENTS.md", "skill": "yss-product-lifecycle", "result": "completed", "changed_files": []any{}, "changed_artifacts": []any{typedRef}, "context_reconciliation": map[string]any{"status": "reconciled", "ref": "CONTEXT.md"}, "evidence_refs": []any{"CONTEXT.md", typedRef}, "deferred_seams": []any{}, "drift": []any{}, "violation": []any{}, "new_impacts": []any{}, "stale_candidates": []any{}, "next_route": "work-unit.plan-opportunity", "blocking_signals": []any{}}
	task := map[string]any{"schema_version": 1, "task_id": "synthetic.report-consumer", "work_unit_id": "work-unit.entry-triage", "actor_id": "synthetic.report-consumer", "role_id": "role.requirements-manager", "runtime_id": "runtime.generic", "execution_state": "Explorer", "workflow_status": "resolved", "stage_id": "stage.entry-triage", "skill_source": map[string]any{"registry_ref": approvalRolesRef, "defaults_ref": "taskPackageDefaults(role.requirements-manager)", "core_skills": role["core_skills"], "forbidden_skills": role["forbidden_skills"]}, "contract": map[string]any{"kind": "lifecycle-work-unit", "contract_id": "synthetic.consumer", "contract_version": 1, "status": "issued", "contract_ref": "AGENTS.md", "lifecycle_ref": approvalRegistryRef}, "checkpoint_ref": cpRef, "inputs": []any{"CONTEXT.md"}, "objective": "合成测试：从正式已声明产物核验当前交付报告", "allowed_write_paths": []any{}, "forbidden_actions": []any{"commit", "publish"}, "expected_outputs": []any{"当前只读交付校验"}, "expected_evidence_files": []any{typedRef, "CONTEXT.md"}, "verification_commands": []any{"synthetic formal verification"}, "verification_results": []any{map[string]any{"command": "synthetic formal verification", "exit_code": 0, "executed_at": time.Now().UTC().Format(time.RFC3339Nano), "evidence_ref": ".template-source/report-consumer/check.log", "evidence_digest": "sha256:" + safefs.Digest(proof)}}, "downstream_consumers": []any{"root.test"}, "convergence": map[string]any{"parent_work_unit": "work-unit.entry-triage", "convergence_ref": "AGENTS.md"}, "result": result}
	task["allowed_write_paths"] = []any{layout.Root + "/report-consumer/output.json"}
	task["work_unit_id"], task["stage_id"], task["execution_state"] = "work-unit.plan-opportunity", "stage.plan", "Worker"
	task["convergence"] = map[string]any{"parent_work_unit": "work-unit.plan-opportunity", "convergence_ref": "AGENTS.md"}
	result["work_unit"], result["next_route"] = "work-unit.plan-opportunity", "work-unit.plan-requirements"
	result["checkpoint_ref"] = cpRef
	result["changed_artifacts"] = []any{}
	task["role_id"] = roleID
	task["skill_source"] = map[string]any{"registry_ref": approvalRolesRef, "defaults_ref": "taskPackageDefaults(" + roleID + ")", "core_skills": role["core_skills"], "forbidden_skills": role["forbidden_skills"]}
	parentRef := layout.Root + "/report-consumer/parent-ticket.md"
	apTestPut(t, root, parentRef, "Synthetic parent Ticket bound to "+cpRef+"; no real work authorization.\n")
	source, e := s.bind("CONTEXT.md")
	if e != nil {
		t.Fatal(e)
	}
	evidence, e := s.bind(typedRef)
	if e != nil {
		t.Fatal(e)
	}
	cp["stage_tracking"] = StageTracking{SchemaVersion: 1, FeatureID: "report-consumer", CheckpointRef: cpRef, EntryStage: "stage.plan", Entry: TrackingEntry{Kind: "parent-ticket", Ref: parentRef}, Items: []WorkItem{{ID: "synthetic-report-consumer", Kind: "stage-work-item", Title: "合成测试：当前 Plan 产物消费", Stage: "stage.plan", WorkUnit: "work-unit.plan-opportunity", Owner: "synthetic.report-consumer", Scope: "synthetic test only", Acceptance: []string{"当前产物摘要匹配"}, Dependencies: []string{}, SourceRefs: []Binding{source}, Progress: "completed", SplitReasons: []string{}, Completion: []Completion{{Criterion: "当前产物摘要匹配", EvidenceRefs: []Binding{evidence}}}}}}
	apTestPut(t, root, cpRef, cp)
	apTestPut(t, root, taskRef, task)
	return cpRef, taskRef
}

func verificationTestPublicBackendReports(t *testing.T, root, old, cpRef, taskRef string) {
	t.Helper()
	_ = apTestRoot(t)
	for ref, f := range apFixtureBundle.Files {
		if !(strings.HasPrefix(ref, ".template-spec/") || strings.HasPrefix(ref, ".agents/skills/") || ref == "AGENTS.md") {
			continue
		}
		if _, e := os.Stat(filepath.Join(root, ref)); e == nil {
			continue
		}
		b, e := f.Render(map[string]string{"projectName": "synthetic", "businessDomain": "test-only", "teamSize": "2"})
		if e != nil {
			t.Fatal(e)
		}
		apTestPut(t, root, ref, b)
	}
	for _, kind := range []string{"backend-contract", "backend-deployment"} {
		ref := strings.TrimPrefix(kind, "backend-") + "-test.json"
		for _, state := range []string{"valid", "missing-consumer", "undeclared-output", "wrong-report"} {
			t.Run("public-"+kind+"-"+state, func(t *testing.T) {
				s := newSemanticSession(context.Background(), root, map[string]string{"tool-root": old})
				if e := s.authorities(); e != nil {
					t.Fatal(e)
				}
				opts := map[string]string{"checkpoint": cpRef, "task": taskRef}
				candidate := ref
				if state == "missing-consumer" {
					opts = map[string]string{}
				}
				if state == "wrong-report" {
					raw, e := os.ReadFile(filepath.Join(root, ref))
					if e != nil {
						t.Fatal(e)
					}
					apTestPut(t, root, "unregistered-report.json", raw)
					candidate = "unregistered-report.json"
				}
				if state == "undeclared-output" {
					task, e := s.doc(taskRef)
					if e != nil {
						t.Fatal(e)
					}
					task["expected_evidence_files"] = []any{"CONTEXT.md"}
					apTestPut(t, root, ".template-source/report-consumer/undeclared-task.json", task)
					opts["task"] = ".template-source/report-consumer/undeclared-task.json"
				}
				before := verificationTree(t, root)
				args := map[string]string{}
				for k, v := range opts {
					args[k] = v
				}
				args["kind"], args["file"], args["tool-root"] = "verification", candidate, old
				_, e := RunContext(context.Background(), "evidence", "verify", root, args)
				if (e == nil) != (state == "valid") {
					t.Fatalf("public specialized %s/%s: %v", kind, state, e)
				}
				if !contractSame(before, verificationTree(t, root)) {
					t.Fatal("public verification mutated inputs")
				}
				if state == "valid" {
					contractTestRetainFixture(t, root, "public-"+kind+"-verification", map[string]any{"group": "evidence", "action": "verify", "kind": "verification", "file": ref, "checkpoint": cpRef, "task": taskRef, "tool-root": old, "mount_roots": []any{old}, "expected_exit": 0})
				} else {
					metadata := map[string]any{"group": "evidence", "action": "verify", "kind": "verification", "file": candidate, "checkpoint": opts["checkpoint"], "task": opts["task"], "tool-root": old, "mount_roots": []any{old}, "expected_exit": 1}
					contractTestRetainFixture(t, root, "public-"+kind+"-verification-"+state, metadata)
				}
			})
		}
	}
}

func TestVerificationPublicPlanTaskCannotOwnDeliveryReport(t *testing.T) {
	root := apTestRoot(t)
	apTestPut(t, root, "synthetic-delivery.json", map[string]any{"delivery_id": "synthetic.marker", "verification": map[string]any{}, "environment": map[string]any{}, "authorizes_real_action": false})
	proof := apTestPut(t, root, "synthetic-evidence.log", "Synthetic recorded exit only; no application command executed.\n")
	report := map[string]any{"schema_version": 1, "kind": "backend-contract", "subject_digest": "sha256:" + strings.Repeat("b", 64), "results": []any{map[string]any{"command": "synthetic recorded check", "exit_code": 0, "executed_at": time.Now().UTC().Format(time.RFC3339Nano), "evidence": []any{map[string]any{"ref": "synthetic-evidence.log", "digest": "sha256:" + safefs.Digest(proof)}}}}}
	report["operation_ids"] = []any{"syntheticOperation"}
	report["coverage"] = []any{map[string]any{"source_id": "scenario.synthetic", "outcome": "success"}, map[string]any{"source_id": "scenario.synthetic", "outcome": "failure"}}
	apTestPut(t, root, "synthetic-report.json", report)
	cpRef, taskRef := verificationTestFormalConsumer(t, root, "synthetic-delivery.json")
	if _, e := apTestRun(root, "task", taskRef, nil); e != nil {
		t.Fatalf("legitimate completed Plan task: %v", e)
	}
	before := verificationTree(t, root)
	_, e := RunContext(context.Background(), "evidence", "verify", root, map[string]string{"kind": "verification", "file": "synthetic-report.json", "checkpoint": cpRef, "task": taskRef})
	if e == nil || !strings.Contains(e.Error(), "专项交付生产工作单元") {
		t.Fatalf("a valid Plan task gained specialized report ownership: %v", e)
	}
	if !contractSame(before, verificationTree(t, root)) {
		t.Fatal("refusing an unrelated consumer changed inputs")
	}
}

func TestVerificationPublicStrategicFinalizedDelivery(t *testing.T) {
	if _, e := exec.LookPath("node"); e != nil {
		t.Skip("development-only fixed finalized delivery producer unavailable")
	}
	old := governanceOracleRoot(t)
	source, e := os.MkdirTemp(contractTestOracleTMP(t), "strategic-source-")
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command("node", "--input-type=module", "-e", `import {fixture} from './scripts/fixtures/strategic-handoff/fixture.mjs';import {finalizeDelivery,openDelivery} from './scripts/lib/strategic-handoff.mjs';await fixture(process.argv[1],{handoffVersion:5,businessTickets:true});const exported=await finalizeDelivery({sourceRoot:process.argv[1],handoffRef:'handoff.yaml',zip:true});const checked=await openDelivery(exported.delivery,()=>({passed:true}));console.log(JSON.stringify({exported,checked}));`, source)
	cmd.Dir = old
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("old finalized delivery: %v %s", e, out)
	}
	var meta struct {
		Exported struct{ Delivery string }
		Checked  struct{ Passed bool }
	}
	if e = json.Unmarshal(out, &meta); e != nil || !meta.Checked.Passed {
		t.Fatalf("fixed finalization %v %s", e, out)
	}
	root := contractTestRetainedRoot(t, apTestProfileRoot(t, "design"), "strategic-verification")
	contractTestRules(t, root)
	distribution, e := bundle.Load("design")
	if e != nil {
		t.Fatal(e)
	}
	tracker, exists := distribution.Files[".template-spec/agents/issue-tracker.md"]
	if !exists {
		t.Fatal("published Design tracker rules absent")
	}
	trackerRaw, e := tracker.Render(map[string]string{"projectName": "synthetic-governance", "businessDomain": "test-only", "teamSize": "2"})
	if e != nil {
		t.Fatal(e)
	}
	apTestPut(t, root, ".template-spec/agents/issue-tracker.md", trackerRaw)
	layout, e := viewWorkLayout(apTestSession(t, root).v)
	if e != nil {
		t.Fatal(e)
	}
	cpRef := layout.Root + "/strategic-verification/checkpoint.json"
	prefix := "docs/deliveries/strategic/strategic-design-handoff.supplier/v1"
	if e = filepath.WalkDir(meta.Exported.Delivery, func(file string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		ref, err := filepath.Rel(meta.Exported.Delivery, file)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		apTestPut(t, root, prefix+"/"+filepath.ToSlash(ref), b)
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	handoff, e := os.ReadFile(filepath.Join(source, "handoff.yaml"))
	if e != nil {
		t.Fatal(e)
	}
	apTestPut(t, root, "handoff.yaml", handoff)
	cp := apTestCheckpoint(map[string]any{})
	cp["feature_id"] = "strategic-verification"
	cp["next_work_unit"] = "work-unit.plan-opportunity"
	cp["artifacts"] = map[string]any{"artifact.strategic-design-handoff": map[string]any{"status": "approved", "ref": "handoff.yaml", "digest": "sha256:" + safefs.Digest(handoff), "evidence_refs": []any{"handoff.yaml"}}}
	if _, e = os.Stat(filepath.Join(root, approvalRegistryRef)); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"valid", "missing-consumer", "wrong-consumer", "wrong-report", "future-time"} {
		t.Run(name, func(t *testing.T) {
			checkpoint := cp
			if name == "wrong-consumer" {
				checkpoint = apCopy(cp)
				checkpoint["artifacts"] = map[string]any{}
			}
			apTestPut(t, root, cpRef, checkpoint)
			ref := prefix + "/verification.json"
			if name == "wrong-report" {
				raw, e := os.ReadFile(filepath.Join(root, ref))
				if e != nil {
					t.Fatal(e)
				}
				apTestPut(t, root, "unregistered-strategic-report.json", raw)
				ref = "unregistered-strategic-report.json"
			}
			var saved []byte
			if name == "future-time" {
				saved, e = os.ReadFile(filepath.Join(root, ref))
				if e != nil {
					t.Fatal(e)
				}
				v, e := schema.Parse(saved)
				if e != nil {
					t.Fatal(e)
				}
				semMap(v)["executed_at"] = "2099-01-01T00:00:00Z"
				apTestPut(t, root, ref, v)
				defer apTestPut(t, root, ref, saved)
			}
			opts := map[string]string{"checkpoint": cpRef}
			if name == "missing-consumer" {
				opts = map[string]string{}
			}
			before := verificationTree(t, root)
			args := map[string]string{}
			for k, v := range opts {
				args[k] = v
			}
			args["kind"], args["file"], args["profile"] = "verification", ref, "design"
			_, e = RunContext(context.Background(), "evidence", "verify", root, args)
			if (e == nil) != (name == "valid") {
				t.Fatalf("public strategic report %s: %v", name, e)
			}
			if !contractSame(before, verificationTree(t, root)) {
				t.Fatal("strategic verify mutated evidence")
			}
			if name == "valid" {
				contractTestRetainFixture(t, root, "public-strategic-verification", map[string]any{"group": "evidence", "action": "verify", "kind": "verification", "file": ref, "checkpoint": cpRef, "profile": "design", "expected_exit": 0})
			}
		})
	}
	apTestPut(t, root, cpRef, cp)
}

func TestVerificationPublicBackendCompletedProducerReports(t *testing.T) {
	root := taskTestCompletedBackendProducerRoot(t)
	verificationTestPublicBackendReports(t, root, os.Getenv("YSS_LEGACY_ORACLE_ROOT"),
		"docs/.scratch/backend-consumer/checkpoint.json", "docs/.scratch/backend-consumer/task.json")
}
