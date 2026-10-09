package governance

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// The shared mixed factory supplies the current API/technical/Slice approvals
// but does not aggregate its root engineering gate. Use the installed original
// rules and standard original-reply producer; never add or trim policy.
func qualifyLocalBackendMilestoneEngineering(t *testing.T, f localImplementationTestFixture) {
	t.Helper()
	program := `import fs from 'node:fs';import path from 'node:path';import {pathToFileURL} from 'node:url';
const [root,cpRef,sliceRef,source]=process.argv.slice(1),load=ref=>import(pathToFileURL(path.join(root,ref)).href),fixture=ref=>import(pathToFileURL(path.join(source,ref)).href);
const {read,json,hash}=await load('scripts/lib/strategic-handoff-io.mjs'),{attachArtifactApproval}=await fixture('scripts/fixtures/backend-delivery/approval-fixture.mjs');
const {countersignRuleForGate}=await load('scripts/lib/digital-human-roles.mjs'),{buildDecisionFixture}=await fixture('scripts/lib/testing/user-decision-fixture.mjs');
const {readSliceContract,sliceArchitectureEvidence}=await load('scripts/lib/slice-contract.mjs');
const {verifyArchitectureEvidence}=await load('scripts/lib/backend-architecture.mjs'),{loadSkillRegistry}=await load('scripts/lib/skill-registry.mjs');
const put=(ref,value)=>fs.writeFileSync(path.join(root,ref),json(value)),bound=ref=>({ref,digest:hash(fs.readFileSync(path.join(root,ref))).slice(7)});
const cp=read(path.join(root,cpRef)),registry=read(path.join(root,'.template-spec/process/lifecycle-registry.yaml')),roles=read(path.join(root,'.template-spec/agents/digital-human-roles.yaml'));
const slice=readSliceContract(sliceRef,{root}),sources=slice.sources,technical=sources.technical_design.ref,gate=registry.gates.find(x=>x.id==='gate.engineering-contract-approved');
if(!technical||!gate)throw Error('Current native engineering source policy is missing');
const proofRows=[];
for(const id of gate.requires_checks??[]){
 if(cp.checks[id]){proofRows.push(...cp.checks[id].basis);continue;}
 const definition=registry.checks.find(x=>x.id===id),ref=id.includes('openapi')?'api.yaml':id==='check.engineering-baseline-accepted'?'local-deployment.json':technical;
 const proof=countersignRuleForGate(roles.gate_policy,id)?attachArtifactApproval(root,ref,'synthetic-current-engineering-'+id,id):null;
 const context=proof?.binding.approval_context??{basis:[bound(ref)]},record=proof?read(path.join(root,proof.binding.approval_ref)):null;
 const basis=[...context.basis,...(proof?[bound(context.subject_ref),bound(proof.binding.approval_ref)]:[])];
 cp.checks[id]={status:'passed',applicable:true,...context,...(proof?{approval_ref:proof.binding.approval_ref,subject_digest:record.subject_digest}:{}),basis,
  reason:'Synthetic current engineering protocol review using installed native policy; not product certification.',evidence_refs:basis.map(x=>x.ref),evidence:Object.fromEntries(definition.evidence.map(kind=>[kind,[ref]]))};
 proofRows.push(...basis);
}
const architectureRefs=['engineering_baseline','manifest','repository_registration'].map(key=>sources[key]?.ref).filter(Boolean);
const approveGate=(id,subjectRef,extraBasis=[])=>{
 const definition=registry.gates.find(row=>row.id===id);if(!definition)throw Error('Native prerequisite gate is missing: '+id);
 if(cp.gates[id]){if(!['approved','not-applicable'].includes(cp.gates[id].status))throw Error('Existing gate is not current: '+id);return cp.gates[id];}
 if(id==='gate.backend-architecture-platform-approved'&&slice.contract.resolution.architecture_identity?.source_kind==='existing-registration'){
  const bindings=sliceArchitectureEvidence(slice.contract,sources);
  verifyArchitectureEvidence(slice.contract.resolution.architecture_identity,bindings,{root,registry:loadSkillRegistry(path.join(root,'.template-spec/agents/yss-skill-registry.yaml')),readOnly:true,assetRef:sliceRef});
  const basis=architectureRefs.map(bound);return cp.gates[id]={status:'not-applicable',reason:'Installed existing architecture validator verified the current registered architecture/platform and independent boundary review; no new architecture decision is required.',basis,evidence_refs:basis.map(row=>row.ref)};
 }
 const dependencies=(definition.requires_gates??[]).map(dep=>approveGate(dep,sources.engineering_baseline.ref,architectureRefs.map(bound)));
 for(const check of definition.requires_checks??[])if(!cp.checks[check])throw Error('Current native gate check missing: '+check);
 const approved=attachArtifactApproval(root,subjectRef,'synthetic-current-'+id,id),context=approved.binding.approval_context,proof=read(path.join(root,approved.binding.approval_ref));
 const dependencyBasis=dependencies.flatMap(row=>row.basis??[]),checkBasis=(definition.requires_checks??[]).flatMap(check=>cp.checks[check].basis);
 context.basis=[...new Map([...context.basis,...extraBasis,...dependencyBasis,...checkBasis].map(row=>[row.ref,row])).values()];
 put(context.subject_ref,{gate_id:id,...context});proof.basis=context.basis;proof.subject_digest=hash(fs.readFileSync(path.join(root,context.subject_ref))).slice(7);
 const d=buildDecisionFixture(path.join(root,'approvals/current-'+id+'-decision'),{boundary:id,scope:context.approval_scope,subjectRef:path.join(root,context.subject_ref)}),relative=ref=>path.relative(root,ref).split(path.sep).join('/');
 d.record.request.items[0].subject.ref=context.subject_ref;d.record.request.requester_source.ref=relative(d.record.request.requester_source.ref);d.present();d.record.responses=[];d.respond();d.record.request.presented_source.ref=relative(d.record.request.presented_source.ref);d.record.responses[0].source.ref=relative(d.record.responses[0].source.ref);d.save();proof.user_decision_ref=relative(d.ref);put(approved.binding.approval_ref,proof);
 const basis=[...context.basis,bound(context.subject_ref),bound(approved.binding.approval_ref)];
 const evidence=Object.fromEntries(definition.evidence.map(kind=>[kind,kind==='evidence.approval-record'?[approved.binding.approval_ref]:kind==='evidence.fresh-verification'?['local-deployment.json']:kind==='evidence.architecture-decision'||kind==='evidence.version-readiness'?architectureRefs:[technical]]));
 return cp.gates[id]={status:'approved',...context,approval_ref:approved.binding.approval_ref,subject_digest:proof.subject_digest,basis,
 reason:'Synthetic current independent engineering/architecture aggregate with original captured reply; not product certification.',evidence_refs:basis.map(row=>row.ref),evidence};
};
approveGate(gate.id,technical,[...proofRows,...architectureRefs.map(bound)]);
put(cpRef,cp);
`
	cmd := exec.Command("node", "--input-type=module", "-e", program, f.Root, f.CheckpointRef, f.SliceRef, governanceOracleRoot(t))
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("current native engineering aggregate: %v %s", err, raw)
	}
}

// A feature milestone does not narrow the already approved full Slice. The
// current backend evidence must suffice while the frontend remains unfinished.
func TestLocalBackendNativeMixedShortMilestone(t *testing.T) {
	seed := actualLocalImplementationNativeSeed(t)
	seedBefore := progressionInventory(t, seed)
	f := actualLocalImplementationFixture(t, seed, true)
	cp := semMap(mustParseContract(mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.CheckpointRef))))
	// Isolate the original terminal-reader defect from any aggregate gate.
	s := newSemanticSession(context.Background(), f.Root, map[string]string{"checkpoint": f.CheckpointRef})
	if err := progressionCheck(s, f.CheckpointRef, cp, "backend-delivery"); err != nil {
		t.Fatalf("full Spec current mixed backend evidence refused: %v", err)
	}
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	qualifyLocalBackendMilestoneEngineering(t, f)
	cp = semMap(mustParseContract(mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.CheckpointRef))))
	delete(semMap(cp["checks"]), "check.frontend-implementation-verified")
	apTestPut(t, f.Root, f.CheckpointRef, cp)
	before := progressionInventory(t, f.Root)
	setTarget := func(target string) {
		t.Helper()
		input := ProgressionTarget{SchemaVersion: 1, Kind: "lifecycle-progression-target", FeatureID: text(cp["feature_id"]), CheckpointRef: f.CheckpointRef, Target: target, IntentSource: "Synthetic full Spec backend milestone then continue", Consumers: []ProgressionConsumer{}}
		apTestPut(t, f.Root, "backend-target-input.json", input)
		plan := filepath.Join(t.TempDir(), "target-plan.json")
		if _, err := RunContext(context.Background(), "lifecycle", "target", f.Root, map[string]string{"checkpoint": f.CheckpointRef, "input": "backend-target-input.json", "plan": "true", "out": plan}); err != nil {
			t.Fatal(err)
		}
		if _, err := RunContext(context.Background(), "lifecycle", "target", f.Root, map[string]string{"apply": "true", "plan-file": plan}); err != nil {
			t.Fatal(err)
		}
	}
	setTarget("backend-deliverable")
	public, err := RunContext(context.Background(), "lifecycle", "target", f.Root, map[string]string{"checkpoint": f.CheckpointRef})
	if err != nil {
		t.Fatal(err)
	}
	p := semMap(semMap(public)["progression"])
	if p["reached"] != true || p["status"] != "reached" {
		t.Fatalf("current mixed Slice backend milestone not reached: %v %v", p["status"], p["reason"])
	}
	completion := semMap(p["completion"])
	if semMap(completion["milestone"])["status"] != "reached" || semMap(completion["profile"])["status"] == "reached" || semMap(completion["business"])["status"] == "reached" {
		t.Fatalf("short backend milestone reported whole completion: %#v", completion)
	}
	worker := map[string]string{"kind": "task", "file": f.TaskRef}
	if _, err := RunContext(context.Background(), "contract", "verify", f.Root, worker); err == nil {
		t.Fatal("reached short backend goal did not stop the existing frontend Worker")
	} else {
		apTestCode(t, err, "PROGRESSION_TARGET_BLOCKED")
	}
	t.Run("engineering-gate-still-required", func(t *testing.T) {
		raw := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.CheckpointRef))
		defer apTestPut(t, f.Root, f.CheckpointRef, raw)
		current := semMap(mustParseContract(raw))
		delete(semMap(current["gates"]), "gate.engineering-contract-approved")
		apTestPut(t, f.Root, f.CheckpointRef, current)
		result, err := progressionRead(context.Background(), f.Root, f.CheckpointRef)
		if err == nil && semMap(result["progression"])["reached"] == true {
			t.Fatal("backend milestone skipped its current engineering gate")
		}
	})
	// The common reader never grants a backend-only responsibility over a full
	// mixed Slice; specialized/legacy terminal consumers retain that check.
	t.Run("backend-only-responsibility-retains-scope", func(t *testing.T) {
		s := newSemanticSession(context.Background(), f.Root, map[string]string{"checkpoint": f.CheckpointRef})
		record, err := s.doc(f.BackendTerminalRef)
		if err != nil {
			t.Fatal(err)
		}
		delivery, err := s.doc(text(semMap(record["delivery"])["ref"]))
		if err != nil {
			t.Fatal(err)
		}
		if err = backendTerminalSliceScope(s, delivery); err == nil {
			t.Fatal("backend-only responsibility accepted the required frontend unit")
		}
	})
	t.Run("explicit-external-backend-does-not-fallback", func(t *testing.T) {
		configRef := filepath.ToSlash(filepath.Join(filepath.Dir(f.CheckpointRef), progressionFile))
		raw := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, configRef))
		defer apTestPut(t, f.Root, configRef, raw)
		intent := semMap(mustParseContract(raw))
		intent["consumers"] = []ProgressionConsumer{{Profile: "backend", Root: t.TempDir(), CheckpointRef: f.CheckpointRef}}
		apTestPut(t, f.Root, configRef, intent)
		s := newSemanticSession(context.Background(), f.Root, map[string]string{"checkpoint": f.CheckpointRef})
		if err := progressionCheck(s, f.CheckpointRef, cp, "backend-delivery"); err == nil {
			t.Fatal("shared terminal reader replaced explicit external Backend with local mixed evidence")
		}
		public, err := RunContext(context.Background(), "lifecycle", "target", f.Root, map[string]string{"checkpoint": f.CheckpointRef})
		if err == nil && semMap(semMap(public)["progression"])["reached"] == true {
			t.Fatal("public target replaced explicit external Backend with local mixed evidence")
		}
	})
	t.Run("permanent-scope-keeps-backend-only-terminal", func(t *testing.T) {
		apTestPut(t, f.Root, ".yss-execution-scope.yaml", "schema_version: 1\nscope_id: plan-to-backend\n")
		defer os.Remove(filepath.Join(f.Root, ".yss-execution-scope.yaml"))
		apTestPut(t, f.Root, ".yss-backend-delivery.json", mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.BackendTerminalRef)))
		defer os.Remove(filepath.Join(f.Root, ".yss-backend-delivery.json"))
		s := newSemanticSession(context.Background(), f.Root, map[string]string{"checkpoint": f.CheckpointRef})
		a, err := progressionBackendAuthorization(s, f.CheckpointRef, "", true)
		if err != nil || a.Mode != "execution-scope" {
			t.Fatalf("real permanent scope was not selected: %#v %v", a, err)
		}
		if err = progressionCheck(s, f.CheckpointRef, cp, "backend-delivery"); err == nil {
			t.Fatal("permanent backend-only scope consumed the full Spec mixed terminal")
		}
	})
	t.Run("missing-current-backend-evidence", func(t *testing.T) {
		raw := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.BackendTerminalRef))
		if err := os.Remove(filepath.Join(f.Root, f.BackendTerminalRef)); err != nil {
			t.Fatal(err)
		}
		defer apTestPut(t, f.Root, f.BackendTerminalRef, raw)
		result, err := progressionRead(context.Background(), f.Root, f.CheckpointRef)
		if err == nil && semMap(result["progression"])["reached"] == true {
			t.Fatal("backend milestone survived missing current backend terminal")
		}
	})
	setTarget("business-accepted")
	if _, err := RunContext(context.Background(), "contract", "verify", f.Root, worker); err != nil {
		t.Fatalf("pending business goal did not resume the original frontend Worker: %v", err)
	}
	after := progressionInventory(t, f.Root)
	for ref, descriptor := range before {
		if after[ref] != descriptor {
			t.Fatalf("short milestone continuation changed original approved CP/Task/Slice/Ticket bytes or mode: %s", ref)
		}
	}
	if !reflect.DeepEqual(seedBefore, progressionInventory(t, seed)) {
		t.Fatal("backend milestone fixture changed the native seed")
	}
}
