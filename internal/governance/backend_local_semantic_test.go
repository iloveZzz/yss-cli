package governance

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The fixed Node producer creates real synthetic approval, review and deployed
// evidence. Go consumes that output; no capability or completion flags are added.
func TestBackendLocalTerminalFixedSource(t *testing.T) {
	oracle := governanceOracleRoot(t)
	for _, apiRequired := range []bool{true, false} {
		t.Run(map[bool]string{true: "api", false: "no-api"}[apiRequired], func(t *testing.T) {
			program := `import path from 'node:path';import {pathToFileURL} from 'node:url';
const source=process.argv[1],load=ref=>import(pathToFileURL(path.join(source,ref)).href);
const {localBackendTerminalFixture,registerLocalBackendFeature}=await load('scripts/fixtures/backend-delivery/local-terminal-fixture.mjs');
const {completeBackendDelivery,verifyBackendDeliveryTerminal}=await load('scripts/lib/backend-delivery-terminal.mjs');
const f=localBackendTerminalFixture({apiRequired:process.argv[2]==='true'}),r=registerLocalBackendFeature(f);
const result=await completeBackendDelivery(f.root,r.terminal,{checkpointRef:r.checkpointRef});
const repeated=await verifyBackendDeliveryTerminal(f.root,{checkpointRef:r.checkpointRef});
console.log(JSON.stringify({root:f.root,checkpoint:r.checkpointRef,terminal:'.work/local/backend-delivery.json',result,repeated}));`
			cmd := exec.Command("node", "--input-type=module", "-e", program, oracle, map[bool]string{true: "true", false: "false"}[apiRequired])
			cmd.Dir = oracle
			raw, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("fixed local producer: %v %s", err, raw)
			}
			var f struct {
				Root       string         `json:"root"`
				Checkpoint string         `json:"checkpoint"`
				Terminal   string         `json:"terminal"`
				Result     map[string]any `json:"result"`
				Repeated   map[string]any `json:"repeated"`
			}
			if err := json.Unmarshal(raw, &f); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(f.Root) })
			check := func() error {
				s := newSemanticSession(context.Background(), f.Root, map[string]string{"checkpoint": f.Checkpoint, "tool-root": oracle})
				if err := s.authorities(); err != nil {
					return err
				}
				if err := s.verify("backend-terminal", f.Terminal, map[string]string{"checkpoint": f.Checkpoint}); err != nil {
					return err
				}
				return s.finish()
			}
			if err := check(); err != nil {
				t.Fatalf("native local terminal: %v (root=%s)", err, f.Root)
			}
			if !contractSame(f.Result, f.Repeated) || f.Result["business_completed"] != false || f.Result["release_authorized"] != false {
				t.Fatalf("producer terminal identity: %s", raw)
			}
			// A valid local record is not a replacement for explicitly requested
			// external backend inputs that are unavailable or not qualified.
			external := newSemanticSession(context.Background(), f.Root, map[string]string{"checkpoint": f.Checkpoint})
			if err := contractLocalFrontendBackend(external, f.Checkpoint, nil,
				ProgressionTarget{Consumers: []ProgressionConsumer{{Profile: "backend", Root: t.TempDir(), CheckpointRef: "checkpoint.json"}}},
				map[string]any{"backend": map[string]any{"input_status": "pending", "completion_status": "not-evaluated"}}, nil); err == nil {
				t.Fatal("missing explicit backend was replaced by a valid local terminal")
			}
			for _, variant := range []string{"missing-independent-review", "failed-deployment", "wrong-current-slice", "wrong-current-profile", "advanced-head"} {
				t.Run(variant, func(t *testing.T) {
					file := "review-result.json"
					if variant == "failed-deployment" {
						file = "deployment.json"
					} else if variant == "wrong-current-slice" || variant == "wrong-current-profile" {
						file = f.Checkpoint
					}
					before, err := os.ReadFile(filepath.Join(f.Root, file))
					if err != nil {
						t.Fatal(err)
					}
					defer apTestPut(t, f.Root, file, before)
					doc := semMap(mustParseContract(before))
					switch variant {
					case "missing-independent-review":
						delete(doc, "reviewer")
					case "failed-deployment":
						semMap(semList(doc["results"])[0])["exit_code"] = 1
					case "wrong-current-slice":
						semMap(semMap(doc["human_review"])["implementation"])["slice_contract_ref"] = "other-slice.yaml"
					case "wrong-current-profile":
						doc["profile_id"] = "harness.frontend-delivery"
					case "advanced-head":
						git := exec.Command("git", "-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.invalid", "commit", "--allow-empty", "-m", "Synthetic moved candidate")
						git.Dir = filepath.Join(f.Root, "project")
						if out, err := git.CombinedOutput(); err != nil {
							t.Fatalf("advance synthetic candidate: %v %s", err, out)
						}
					}
					if variant != "advanced-head" {
						apTestPut(t, f.Root, file, doc)
					}
					if variant == "failed-deployment" {
						// Bind the changed failed report so rejection exercises its
						// actual exit status, rather than only an old byte digest.
						deliveryRaw := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, "local-delivery.json"))
						terminalRaw := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.Terminal))
						defer apTestPut(t, f.Root, "local-delivery.json", deliveryRaw)
						defer apTestPut(t, f.Root, f.Terminal, terminalRaw)
						delivery := semMap(mustParseContract(deliveryRaw))
						semMap(semMap(delivery["verification"])["deployment"])["digest"] = backendHash(mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, file)))
						newDelivery := apTestPut(t, f.Root, "local-delivery.json", delivery)
						terminal := semMap(mustParseContract(terminalRaw))
						semMap(terminal["delivery"])["digest"] = backendHash(newDelivery)
						apTestPut(t, f.Root, f.Terminal, terminal)
					}
					if err := check(); err == nil {
						t.Fatal("incomplete or stale local evidence was accepted")
					}
				})
			}
		})
	}
}

// A current, genuinely reviewed Slice can require frontend implementation even
// when the product uses an existing UI baseline rather than new product design.
func TestLifecycleTargetCurrentApprovedFrontendSliceIsApplicable(t *testing.T) {
	oracle := governanceOracleRoot(t)
	program := `import fs from 'node:fs';import path from 'node:path';import {pathToFileURL} from 'node:url';
const source=process.argv[1],load=ref=>import(pathToFileURL(path.join(source,ref)).href);
const {localBackendTerminalFixture,registerLocalBackendFeature}=await load('scripts/fixtures/backend-delivery/local-terminal-fixture.mjs');
const {baselineFixture}=await load('scripts/fixtures/existing-ui-baseline/fixture.mjs');
const {read,hash}=await load('scripts/lib/strategic-handoff-io.mjs');
const {readSliceContract}=await load('scripts/lib/slice-contract.mjs');
const {compileStandardsCoverage}=await load('scripts/lib/backend-standards-coverage.mjs');
const {completeBackendDelivery}=await load('scripts/lib/backend-delivery-terminal.mjs');
const f=localBackendTerminalFixture(),baseline=baselineFixture(path.join(f.root,'existing-ui'));
registerLocalBackendFeature(f);
f.contract.basis.existing_ui_baseline=f.write('existing-ui/existing-ui-baseline.json',baseline.data);
f.contract.applicability.frontend={status:'required',baseline_kind:'existing-ui-baseline',ui_change:'none'};
f.contract.extensions.frontend={visual_baseline_case_ids:['submit']};
const approved=f.approve(),r=registerLocalBackendFeature({...f,binding:approved.binding});
const cp=read(path.join(f.root,r.checkpointRef)),bound=ref=>({ref,digest:hash(fs.readFileSync(path.join(f.root,ref))).slice(7)});
cp.gates['gate.slice-contract-approved'].basis=[bound(approved.binding.ref),bound(approved.binding.approval_ref)];
cp.gates['gate.slice-contract-approved'].evidence={'evidence.contract-approval':[approved.binding.approval_ref]};
f.write(r.checkpointRef,cp);
// Recreate the independent committed review over this actual new approved
// mixed Slice, rather than reusing the old backend-only contract digest.
const current=readSliceContract(approved.binding.ref,{root:f.root}).contract;
const coverage=compileStandardsCoverage({root:f.root,projectRoot:f.project,contract:current,scope_kind:'change',comparison_ref:f.state.review_input.review_base_ref});
f.write('coverage.json',coverage);f.state.review_input.standards_coverage_digest=hash(fs.readFileSync(path.join(f.root,'coverage.json'))).slice(7);
f.state.review_input.work_unit_id='work-unit.slice-backend';f.record.contract_digest=approved.binding.digest;
f.record.constraint_results=coverage.constraints.map(rule=>({axis:'Standards',skill:rule.skill,constraint_id:rule.constraint_id,constraint:'Synthetic independently verified current mixed scope',status:rule.applicability==='required'?'passed':'not-applicable',reason:'Synthetic source does not implement this constraint',applicability_basis:rule.applicability_basis,rule_ref:rule.rule_ref,rule_digest:rule.rule_digest,code_ref:'src/main/java/web/Boundary.java:1',evidence_ref:'checks.log',evidence_digest:f.record.verification_results[0].evidence_digest,review_notes:'Synthetic current full-text review only.'}));
f.save();f.write('review-state.json',f.state);f.delivery.slice_contract=approved.binding;f.verification();
fs.unlinkSync(path.join(f.root,'.work/local/progression-target.json'));
const terminal={...r.terminal,delivery:f.file('local-delivery.json'),review_state:f.file('review-state.json')};
const result=await completeBackendDelivery(f.root,terminal,{checkpointRef:r.checkpointRef});
console.log(JSON.stringify({root:f.root,checkpoint:r.checkpointRef,terminal:'.work/local/backend-delivery.json',result}));`
	cmd := exec.Command("node", "--input-type=module", "-e", program, oracle)
	cmd.Dir = oracle
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("existing approved frontend Slice fixture: %v %s", err, raw)
	}
	var f struct {
		Root, Checkpoint, Terminal string
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(f.Root) })
	s := newSemanticSession(context.Background(), f.Root, map[string]string{"checkpoint": f.Checkpoint})
	if err = s.authorities(); err != nil {
		t.Fatal(err)
	}
	cp, err := s.doc(f.Checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	na, err := progressionSliceNoFrontend(s, f.Checkpoint, cp)
	if err != nil || na {
		t.Fatalf("current approved frontend scope was treated as N/A: %t %v", na, err)
	}
	if err = s.finish(); err != nil {
		t.Fatal(err)
	}
	current := newSemanticSession(context.Background(), f.Root, map[string]string{"checkpoint": f.Checkpoint})
	if err = current.authorities(); err != nil {
		t.Fatal(err)
	}
	if err = backendBusinessLocalEvidence(current, f.Checkpoint); err != nil {
		t.Fatalf("actual mixed backend business evidence was refused: %v", err)
	}
	if err = current.finish(); err != nil {
		t.Fatal(err)
	}
	short := newSemanticSession(context.Background(), f.Root, map[string]string{"checkpoint": f.Checkpoint})
	if err = short.authorities(); err != nil {
		t.Fatal(err)
	}
	if err = short.verify("backend-terminal", f.Terminal, map[string]string{"checkpoint": f.Checkpoint}); err != nil {
		t.Fatalf("full Spec mixed backend milestone was refused: %v", err)
	}
	if err = short.finish(); err != nil {
		t.Fatal(err)
	}
}

func TestBackendReviewMixedSliceSelectedUnit(t *testing.T) {
	oracle := governanceOracleRoot(t)
	program := `import fs from 'node:fs';import path from 'node:path';import {pathToFileURL} from 'node:url';
const source=process.argv[1],load=ref=>import(pathToFileURL(path.join(source,ref)).href);
const {localBackendTerminalFixture,registerLocalBackendFeature}=await load('scripts/fixtures/backend-delivery/local-terminal-fixture.mjs');
const {baselineFixture}=await load('scripts/fixtures/existing-ui-baseline/fixture.mjs');
const {readSliceContract}=await load('scripts/lib/slice-contract.mjs');
const {compileStandardsCoverage}=await load('scripts/lib/backend-standards-coverage.mjs');
const {hash}=await load('scripts/lib/strategic-handoff-io.mjs');
const f=localBackendTerminalFixture(),baseline=baselineFixture(path.join(f.root,'existing-ui'));
registerLocalBackendFeature(f);
f.contract.basis.existing_ui_baseline=f.write('existing-ui/existing-ui-baseline.json',baseline.data);
f.contract.applicability.frontend={status:'required',baseline_kind:'existing-ui-baseline',ui_change:'none'};
f.contract.extensions.frontend={visual_baseline_case_ids:['submit']};
const approved=f.approve(),current=readSliceContract(approved.binding.ref,{root:f.root}).contract;
const coverage=compileStandardsCoverage({root:f.root,projectRoot:f.project,contract:current,scope_kind:'change',comparison_ref:f.state.review_input.review_base_ref});
f.write('coverage.json',coverage);f.state.review_input.standards_coverage_digest=hash(fs.readFileSync(path.join(f.root,'coverage.json'))).slice(7);
f.state.review_input.work_unit_id='work-unit.slice-backend';f.record.contract_digest=approved.binding.digest;
f.record.constraint_results=coverage.constraints.map(rule=>({axis:'Standards',skill:rule.skill,constraint_id:rule.constraint_id,constraint:'Synthetic independent current full-text review',status:rule.applicability==='required'?'passed':'not-applicable',reason:'Synthetic source does not implement this constraint',applicability_basis:rule.applicability_basis,rule_ref:rule.rule_ref,rule_digest:rule.rule_digest,code_ref:'src/main/java/web/Boundary.java:1',evidence_ref:'checks.log',evidence_digest:f.record.verification_results[0].evidence_digest,review_notes:'Synthetic independent backend review only.'}));
f.save();f.write('review-state.json',f.state);registerLocalBackendFeature({...f,binding:approved.binding});
console.log(JSON.stringify({root:f.root,slice:approved.binding.ref,approval:approved.binding.approval_ref,unit:f.state.review_input.work_unit_id}));`
	cmd := exec.Command("node", "--input-type=module", "-e", program, oracle)
	cmd.Dir = oracle
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("current approved mixed Slice review fixture: %v %s", err, raw)
	}
	var f struct{ Root, Slice, Approval, Unit string }
	if err = json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(f.Root) })
	s := newSemanticSession(context.Background(), f.Root, nil)
	if err = s.authorities(); err != nil {
		t.Fatal(err)
	}
	if err = s.verify("backend-review", "review-state.json", nil); err != nil {
		t.Fatalf("current independent backend unit review requires unrelated frontend acceptance: %v", err)
	}
	if err = s.finish(); err != nil {
		t.Fatal(err)
	}
	stateBefore := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, "review-state.json"))
	for _, variant := range []string{"missing-explicit-unit", "unknown-unit", "frontend-unit", "wrong-project-root"} {
		t.Run(variant, func(t *testing.T) {
			state := semMap(mustParseContract(stateBefore))
			input := semMap(state["review_input"])
			switch variant {
			case "missing-explicit-unit":
				delete(input, "work_unit_id")
			case "unknown-unit":
				input["work_unit_id"] = "work-unit.slice-unknown"
			case "frontend-unit":
				input["work_unit_id"] = "work-unit.slice-frontend"
			case "wrong-project-root":
				input["project_root"] = "."
			}
			apTestPut(t, f.Root, "review-state.json", state)
			defer apTestPut(t, f.Root, "review-state.json", stateBefore)
			rejected := newSemanticSession(context.Background(), f.Root, nil)
			if err := rejected.authorities(); err != nil {
				t.Fatal(err)
			}
			if err := rejected.verify("backend-review", "review-state.json", nil); err == nil {
				t.Fatal("caller changed the frozen backend responsibility view")
			}
		})
	}
	// The complete Slice still includes frontend work. Selecting a backend
	// unit does not give the whole contract an implementation authorization.
	whole := newSemanticSession(context.Background(), f.Root, nil)
	if err = whole.authorities(); err != nil {
		t.Fatal(err)
	}
	if err = whole.verify("slice", f.Slice, map[string]string{"approval-ref": f.Approval}); err == nil {
		t.Fatal("backend review made the whole mixed Slice ready without frontend inputs")
	}
}
