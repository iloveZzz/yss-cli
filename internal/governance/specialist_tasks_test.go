package governance

import (
	"context"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLocalFrontendSpecialistPublicAnalysisAndApprovedWorker(t *testing.T) {
	frontend := specBaselineActualNativeSeed(t, "frontend", true)
	program := `import fs from 'node:fs';import path from 'node:path';import {pathToFileURL} from 'node:url';
const root=process.argv[1],{planTracking,applyTracking}=await import(pathToFileURL(path.join(root,'scripts/lib/stage-tracking-migration.mjs')).href);
const base=path.join(root,'.work/analysis');fs.mkdirSync(base,{recursive:true});
const cp=JSON.parse(fs.readFileSync(path.join(root,'.template-spec/process/templates/lifecycle-checkpoint-template.json'),'utf8'));Object.assign(cp,{feature_id:'feature.analysis',repository_mode:'project-instance',stage:'stage.plan',next_work_unit:'work-unit.plan-requirements'});if(cp.stage_trace)cp.stage_trace.stage=cp.stage;fs.writeFileSync(path.join(base,'checkpoint.json'),JSON.stringify(cp));fs.writeFileSync(path.join(base,'map.md'),'---\ncheckpoint_ref: .work/analysis/checkpoint.json\n---\n');
const plan=planTracking(root,{checkpoint_ref:'.work/analysis/checkpoint.json',items:[{id:'requirements',title:'Synthetic local requirements',stage:'stage.plan',work_unit:'work-unit.plan-requirements',owner:'synthetic-analysis',scope:'Confirm same-side business requirements',acceptance:['Current source rules recorded'],source_refs:['CONTEXT.md']}]});applyTracking(root,plan);`
	if raw, err := exec.Command("node", "--input-type=module", "-e", program, frontend).CombinedOutput(); err != nil {
		t.Fatalf("actual public analysis tracking: %v %s", err, raw)
	}
	s := apTestSession(t, frontend)
	for _, unit := range []string{"work-unit.plan-requirements"} {
		stage, roleID := "stage.plan", "role.requirements-manager"
		if unit == "work-unit.prototype-design-v2" {
			stage, roleID = "stage.product-design", "role.product-manager"
		}
		role := taskRole(s, roleID)
		task := map[string]any{"schema_version": 1, "task_id": "synthetic.local-analysis", "work_unit_id": unit, "actor_id": "synthetic.analysis", "role_id": roleID, "runtime_id": "runtime.generic", "execution_state": "Drafter", "workflow_status": "active", "stage_id": stage, "skill_source": map[string]any{"registry_ref": approvalRolesRef, "defaults_ref": "taskPackageDefaults(" + roleID + ")", "core_skills": role["core_skills"], "forbidden_skills": role["forbidden_skills"]}, "contract": map[string]any{"kind": "lifecycle-work-unit", "contract_id": "synthetic.current", "contract_version": 1, "status": "issued", "contract_ref": guidanceContractRef("frontend"), "lifecycle_ref": approvalRegistryRef}, "inputs": []any{"CONTEXT.md"}, "objective": "Synthetic local analysis consumer; no real approval", "allowed_write_paths": []any{".work/analysis/plan.md"}, "checkpoint_ref": ".work/analysis/checkpoint.json", "forbidden_actions": []any{"publish"}, "expected_outputs": []any{"analysis"}, "expected_evidence_files": []any{"AGENTS.md"}, "verification_commands": []any{"synthetic check"}, "verification_results": []any{}, "downstream_consumers": []any{"test"}, "convergence": map[string]any{"parent_work_unit": unit, "convergence_ref": "AGENTS.md"}}
		apTestPut(t, frontend, "task.json", task)
		if raw, err := exec.Command(os.Getenv("YSS_NATIVE_BINARY"), "contract", "verify", "--root", frontend, "--kind", "task", "--file", "task.json", "--json").CombinedOutput(); err != nil {
			t.Fatalf("public local analysis %s: %v %s", unit, err, raw)
		}
		task["execution_state"] = "Worker"
		apTestPut(t, frontend, "task.json", task)
		if raw, err := exec.Command(os.Getenv("YSS_NATIVE_BINARY"), "contract", "verify", "--root", frontend, "--kind", "task", "--file", "task.json", "--json").CombinedOutput(); err == nil {
			t.Fatalf("analysis granted Worker: %s", raw)
		}
	}
	spec := actualLocalImplementationNativeSeed(t)
	f := actualLocalImplementationFixtureForProfile(t, spec, frontend, false)
	if raw, err := exec.Command(os.Getenv("YSS_NATIVE_BINARY"), "contract", "verify", "--root", f.Root, "--kind", "task", "--file", f.TaskRef, "--json").CombinedOutput(); err != nil {
		t.Fatalf("actual Frontend approved local Worker: %v %s", err, raw)
	}
}

func TestLocalFrontendSpecialistExternalBackendAndLocalBackendTerminal(t *testing.T) {
	oracle := governanceOracleRoot(t)
	seed := backendProfileTestNativeSeed(t)
	program := `import path from 'node:path';import {pathToFileURL} from 'node:url';
const [source,seed]=process.argv.slice(1),{backendProfileTerminalFixture}=await import(pathToFileURL(path.join(source,'scripts/fixtures/backend-delivery/backend-profile-terminal-fixture.mjs')).href);
const f=await backendProfileTerminalFixture({nativeSeed:seed,localEvidence:true});console.log(JSON.stringify({root:f.root,checkpoint:f.checkpointRef,terminal:f.terminalRef,slice:f.binding.ref,sliceID:f.contract.slice_id,specDigest:f.contract.basis.spec.digest,apiDigest:f.delivery.openapi.digest}));`
	raw, err := exec.Command("node", "--input-type=module", "-e", program, oracle, seed).CombinedOutput()
	if err != nil {
		t.Fatalf("current Backend local evidence fixture: %v %s", err, raw)
	}
	var f struct{ Root, Checkpoint, Terminal, Slice, SliceID, SpecDigest, APIDigest string }
	if err = json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(f.Root) })
	apTestPut(t, f.Root, "local-deployment.json", mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, "profile-deployment.json")))
	qualifyLocalBackendMilestoneEngineering(t, localImplementationTestFixture{Root: f.Root, CheckpointRef: f.Checkpoint, SliceRef: f.Slice})
	if raw, err = exec.Command(os.Getenv("YSS_NATIVE_BINARY"), "lifecycle", "status", "--root", f.Root, "--checkpoint", f.Checkpoint, "--json").CombinedOutput(); err != nil {
		t.Fatalf("public actual Backend local terminal: %v %s", err, raw)
	}
	var status map[string]any
	if err = json.Unmarshal(raw, &status); err != nil || semMap(semMap(status["result"])["progression"])["status"] != "reached" {
		t.Fatalf("Backend local responsibility did not reach its terminal: %v %s", err, raw)
	}
	front := specBaselineActualNativeSeed(t, "frontend", true)
	apTestPut(t, front, ".work/profile/map.md", "---\ncheckpoint_ref: .work/profile/checkpoint.json\n---\n")
	apTestPut(t, front, ".work/profile/checkpoint.json", map[string]any{"feature_id": "feature.profile"})
	bytes := apTestPut(t, front, "backend-dependency.json", map[string]any{"schema_version": 1, "kind": "frontend-backend-dependency", "root": f.Root, "checkpoint_ref": f.Checkpoint})
	c := &nativeSlice{Raw: map[string]any{"slice_id": f.SliceID, "extensions": map[string]any{"frontend": map[string]any{"backend_dependency": map[string]any{"ref": "backend-dependency.json", "digest": "sha256:" + safefs.Digest(bytes)}}}}, Basis: map[string]map[string]any{"spec": {"digest": f.SpecDigest}, "openapi_freeze": {"digest": f.APIDigest}}}
	consume := func() error {
		s := newSemanticSession(context.Background(), front, nil)
		if err := contractLocalFrontendBackend(s, ".work/profile/checkpoint.json", c, ProgressionTarget{}, nil, nil); err != nil {
			return err
		}
		return s.finish()
	}
	if err = consume(); err != nil {
		t.Fatalf("explicit current Backend dependency: %v", err)
	}
	checkpoint := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.Checkpoint))
	missingGate := semMap(mustParseContract(checkpoint))
	delete(semMap(missingGate["gates"]), "gate.engineering-contract-approved")
	apTestPut(t, f.Root, f.Checkpoint, missingGate)
	if consume() == nil {
		t.Fatal("unfinished Backend responsibility accepted")
	}
	apTestPut(t, f.Root, f.Checkpoint, checkpoint)
	c.Basis["openapi_freeze"]["digest"] = "sha256:" + safefs.Digest([]byte("stale"))
	if consume() == nil {
		t.Fatal("API mismatch accepted")
	}
	c.Basis["openapi_freeze"]["digest"] = f.APIDigest
	apTestPut(t, front, "backend-dependency.json", append(bytes, '\n'))
	if consume() == nil {
		t.Fatal("dependency byte drift accepted")
	}
	apTestPut(t, front, "backend-dependency.json", bytes)
	original, _ := os.ReadFile(filepath.Join(f.Root, "profile-deployment.json"))
	apTestPut(t, f.Root, "profile-deployment.json", append(original, '\n'))
	if consume() == nil {
		t.Fatal("backend actual evidence drift accepted")
	}
}
