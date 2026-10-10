package governance

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
)

func TestFrontendImplementationPlaceholdersUseWorkLayout(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	apTestPut(t, root, worklayout.TrackerRef, "---\ntracker:\n  platform: local-markdown\n  root: records/features\n  legacy_roots: [archive/features]\n---\n")
	for _, tc := range []struct {
		ref     string
		invalid bool
	}{{"records/features/example/plan.json", true}, {worklayout.DefaultRoot + "/example/plan.json", true}, {worklayout.HistoricalRoots[0] + "/example/plan.json", true}, {"archive/features/example/plan.json", true}, {"records/features/supplier/plan.json", false}, {"frontend/implementation-plan.json", false}, {"REPLACE_ME", true}, {"<frontend-plan>", true}} {
		t.Run(tc.ref, func(t *testing.T) {
			s := newSemanticSession(context.Background(), root, nil)
			err := frontendImplementationPlaceholders(s, `{"ref":"`+tc.ref+`"}`)
			if (err != nil) != tc.invalid {
				t.Fatalf("placeholder %s: %v", tc.ref, err)
			}
			if err == nil {
				if err = s.finish(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

type localImplementationTestFixture struct {
	Root                    string `json:"root"`
	CheckpointRef           string `json:"checkpointRef"`
	SliceRef                string `json:"sliceRef"`
	SliceID                 string `json:"sliceID"`
	BackendTerminalRef      string `json:"backendTerminalRef"`
	FrontendPlanRef         string `json:"frontendPlanRef"`
	FrontendVerificationRef string `json:"frontendVerificationRef"`
	FrontendWorkUnitID      string `json:"frontendWorkUnitId"`
	TaskRef                 string `json:"taskRef"`
	ProjectRoot             string `json:"projectRoot"`
}

func actualLocalImplementationFixture(t *testing.T, seed string, backendRequired bool, business ...bool) localImplementationTestFixture {
	return actualLocalImplementationFixtureForProfile(t, seed, "", backendRequired, business...)
}
func actualLocalImplementationFixtureForProfile(t *testing.T, seed, frontendSeed string, backendRequired bool, business ...bool) localImplementationTestFixture {
	t.Helper()
	oracle := governanceOracleRoot(t)
	program := `import fs from 'node:fs';import path from 'node:path';import {pathToFileURL} from 'node:url';
const [source,seed,backend,business,frontendSeed]=process.argv.slice(1);
const {localImplementationFixture}=await import(pathToFileURL(path.join(source,'scripts/fixtures/spec-baseline/local-implementation-fixture.mjs')).href);
const {attachArtifactApproval}=await import(pathToFileURL(path.join(source,'scripts/fixtures/backend-delivery/approval-fixture.mjs')).href);
const {read,json,hash}=await import(pathToFileURL(path.join(source,'scripts/lib/strategic-handoff-io.mjs')).href);
const {buildDecisionFixture}=await import(pathToFileURL(path.join(source,'scripts/lib/testing/user-decision-fixture.mjs')).href);
const f=await localImplementationFixture({nativeSeed:seed,backendRequired:backend==='true',...(frontendSeed?{nativeFrontendSeed:frontendSeed}:{})}),put=(ref,value)=>fs.writeFileSync(path.join(f.root,ref),json(value)),bound=ref=>({ref,digest:hash(fs.readFileSync(path.join(f.root,ref))).slice(7)});
const {compileSliceTaskPackage}=await import(pathToFileURL(path.join(f.root,'scripts/lib/slice-task-package.mjs')).href),taskRef='frontend/current-worker-task.json';
put(taskRef,compileSliceTaskPackage(f.taskBinding,{root:f.root,work_unit_id:f.frontendWorkUnitId,task_id:'task.local-current-frontend',actor_id:'synthetic-local-frontend-worker',runtime_id:'runtime.generic',execution_state:'Worker'}));
// Capture the original immutable implementation tree before its current review.
const report=read(path.join(f.root,f.frontendVerificationRef)),head=f.git('rev-parse','HEAD'),tree=f.git('rev-parse','HEAD^{tree}');
report.independent_review.candidate_digest=tree;put(f.frontendVerificationRef,report);
// Current independent frontend countersign uses the installed native role policy.
const approval=attachArtifactApproval(f.root,f.frontendVerificationRef,'synthetic-current-frontend-review','check.frontend-implementation-verified'),context=approval.binding.approval_context,proof=read(path.join(f.root,approval.binding.approval_ref));
const subject=read(path.join(f.root,context.subject_ref));
subject.review_input={scope_kind:'change',slice_contract_ref:f.sliceRef,work_unit_id:f.frontendWorkUnitId,project_root:f.project,
 review_mode:'committed',implementation_candidate_ref:head,candidate_digest:tree,review_base_ref:head,
 implementation_actor_id:'synthetic-local-frontend-worker',implementation_instance_id:'synthetic-local-frontend-worker-instance'};
put(context.subject_ref,subject);proof.subject_digest=hash(fs.readFileSync(path.join(f.root,context.subject_ref))).slice(7);put(approval.binding.approval_ref,proof);
const basis=[...context.basis,bound(context.subject_ref),bound(approval.binding.approval_ref)],cp=read(path.join(f.root,f.checkpointRef));
cp.checks['check.frontend-implementation-verified']={status:'passed',applicable:true,...context,approval_ref:approval.binding.approval_ref,subject_digest:proof.subject_digest,basis,reason:'Synthetic independent current frontend protocol review; not product certification.',evidence_refs:basis.map(row=>row.ref),evidence:{'evidence.frontend-implementation-verification':[f.frontendVerificationRef]}};
cp.artifacts['artifact.frontend-implementation-plan']={...cp.artifacts['artifact.frontend-implementation-plan'],status:'approved',digest:hash(fs.readFileSync(path.join(f.root,f.frontendPlanRef)))};
cp.artifacts['artifact.frontend-implementation-verification']={...cp.artifacts['artifact.frontend-implementation-verification'],status:'approved',digest:hash(fs.readFileSync(path.join(f.root,f.frontendVerificationRef)))};
if(business==='true') {
 put('business-rollback.log','Synthetic independent rollback observation; never a product release.');
 const accepted=attachArtifactApproval(f.root,f.frontendVerificationRef,'synthetic-current-local-business','gate.delivery-accepted'),context=accepted.binding.approval_context,proof=read(path.join(f.root,accepted.binding.approval_ref));
 const aggregate=new Map();for(const row of [...context.basis,...basis,bound('business-rollback.log')]){const old=aggregate.get(row.ref);if(old&&old.digest!==row.digest)throw Error('Synthetic current aggregate digest mismatch');aggregate.set(row.ref,row);}
 context.basis=[...aggregate.values()];put(context.subject_ref,{gate_id:'gate.delivery-accepted',...context});proof.basis=context.basis;proof.subject_digest=hash(fs.readFileSync(path.join(f.root,context.subject_ref))).slice(7);
 const confirmation=buildDecisionFixture(path.join(f.root,'approvals/local-business-current-decision'),{boundary:'gate.delivery-accepted',scope:context.approval_scope,subjectRef:path.join(f.root,context.subject_ref)}),relative=ref=>path.relative(f.root,ref).split(path.sep).join('/');
 confirmation.record.request.items[0].subject.ref=context.subject_ref;confirmation.record.request.requester_source.ref=relative(confirmation.record.request.requester_source.ref);confirmation.present();confirmation.record.responses=[];confirmation.respond();confirmation.record.request.presented_source.ref=relative(confirmation.record.request.presented_source.ref);confirmation.record.responses[0].source.ref=relative(confirmation.record.responses[0].source.ref);confirmation.save();
 proof.user_decision_ref=relative(confirmation.ref);put(accepted.binding.approval_ref,proof);
 const gateBasis=[...context.basis,bound(context.subject_ref),bound(accepted.binding.approval_ref)];
 cp.gates['gate.delivery-accepted']={status:'approved',...context,approval_ref:accepted.binding.approval_ref,subject_digest:proof.subject_digest,basis:gateBasis,reason:'Synthetic current independent frontend, backend and business acceptance; not product certification.',evidence_refs:gateBasis.map(row=>row.ref),evidence:{'evidence.fresh-verification':[f.frontendVerificationRef],'evidence.checkpoint-and-rollback':['business-rollback.log']}};
 cp.stage='stage.verification-release-retrospective';cp.next_work_unit='work-unit.release-and-retrospective';
}
put(f.checkpointRef,cp);
console.log(JSON.stringify({root:f.root,checkpointRef:f.checkpointRef,sliceRef:f.sliceRef,sliceID:f.sliceID,backendTerminalRef:f.backendTerminalRef,frontendPlanRef:f.frontendPlanRef,frontendVerificationRef:f.frontendVerificationRef,frontendWorkUnitId:f.frontendWorkUnitId,taskRef,projectRoot:f.project}));`
	withBusiness := len(business) != 0 && business[0]
	cmd := exec.Command("node", "--input-type=module", "-e", program, oracle, seed, fmt.Sprint(backendRequired), fmt.Sprint(withBusiness), frontendSeed)
	cmd.Dir = oracle
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual local implementation protocol: %v %s", err, raw)
	}
	var f localImplementationTestFixture
	if err = json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("local implementation result: %v %s", err, raw)
	}
	t.Cleanup(func() { _ = os.RemoveAll(f.Root) })
	authority := seed
	family := "spec"
	if frontendSeed != "" {
		authority, family = frontendSeed, "frontend"
	}
	for _, ref := range []string{".yss.json", ".template-spec/process/harness-profile.yaml", ".template-spec/process/lifecycle-registry.yaml", ".template-spec/agents/digital-human-roles.yaml", guidanceContractRef(family)} {
		if !bytes.Equal(mustReadSpecBaselineTestFile(t, filepath.Join(authority, ref)), mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, ref))) {
			t.Fatalf("local fixture changed native policy: %s", ref)
		}
	}
	return f
}

func actualLocalImplementationNativeSeed(t *testing.T) string {
	t.Helper()
	seed := specBaselineActualNativeSeed(t, "spec")
	// Install only the compiler and the fixture's shared preparation pipeline,
	// using the real public fixed-Bundle dependency closure. The pilot factory
	// prepares MVC before refining UI-only scope; this is not a UI routing rule.
	// Standards Coverage also reads every registered component source index.
	// Those Skills are review discovery knowledge, not this Slice's execution set.
	plan := filepath.Join(t.TempDir(), "local-compiler-plan.json")
	for _, args := range [][]string{{"skills", "ensure", "alibaba-java-code-style", "lombok", "mapstruct", "yss-cache", "yss-web-controller", "yss-dto", "yss-validation", "yss-exception", "yss-application", "yss-audit-log", "yss-distributed-id", "yss-excel-mvc", "yss-mybatis", "yss-resilience4j", "yss-security-algorithm", "yss-userinfo", "yss-implementation-contract-compiler", "yss-tactical-design", "yss-layered-mvc-scaffold-generator", "yss-ddd-scaffold-generator", "yss-frontend-scaffold-generator", "--root", seed, "--plan", "--out", plan, "--json"}, {"skills", "--root", seed, "--apply", "--plan-file", plan, "--json"}} {
		if raw, err := exec.Command(os.Getenv("YSS_NATIVE_BINARY"), args...).CombinedOutput(); err != nil {
			t.Fatalf("public required implementation closure: %v %s", err, raw)
		}
	}
	return seed
}

func TestLocalFrontendNativeCurrentImplementationEvidence(t *testing.T) {
	seed := actualLocalImplementationNativeSeed(t)
	seedBefore := progressionInventory(t, seed)
	f := actualLocalImplementationFixture(t, seed, false)
	cpBytes := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.CheckpointRef))
	cp := semMap(mustParseContract(cpBytes))
	delete(semMap(cp["checks"]), "check.frontend-implementation-verified")
	for _, id := range []string{"artifact.frontend-implementation-plan", "artifact.frontend-implementation-verification"} {
		asset := semMap(semMap(cp["artifacts"])[id])
		asset["status"] = "ready-for-human"
		delete(asset, "digest")
	}
	apTestPut(t, f.Root, f.CheckpointRef, cp)
	before := progressionInventory(t, f.Root)
	_, err := RunContext(context.Background(), "evidence", "verify", f.Root, map[string]string{"kind": "verification", "file": f.FrontendVerificationRef, "checkpoint": f.CheckpointRef})
	if err != nil {
		t.Fatalf("current local frontend implementation evidence: %v", err)
	}
	if _, err := RunContext(context.Background(), "contract", "verify", f.Root, map[string]string{"kind": "task", "file": f.TaskRef}); err != nil {
		t.Fatalf("actual generated local Worker without invented top-level checkpoint: %v", err)
	}
	if !reflect.DeepEqual(before, progressionInventory(t, f.Root)) || !reflect.DeepEqual(seedBefore, progressionInventory(t, seed)) {
		t.Fatal("readonly current frontend validation changed approved inputs or native seed")
	}

	t.Run("actual-public-short-goal-stops-old-worker-and-resumes", func(t *testing.T) {
		native := os.Getenv("YSS_NATIVE_BINARY")
		original := progressionInventory(t, f.Root)
		run := func(args ...string) ([]byte, error) { return exec.Command(native, args...).CombinedOutput() }
		worker := []string{"contract", "verify", "--root", f.Root, "--kind", "task", "--file", f.TaskRef, "--json"}
		if raw, err := run(worker...); err != nil {
			t.Fatalf("public original Worker: %v %s", err, raw)
		}
		for _, goal := range []string{"spec-approved", "business-accepted"} {
			input := ProgressionTarget{SchemaVersion: 1, Kind: "lifecycle-progression-target", FeatureID: text(cp["feature_id"]), CheckpointRef: f.CheckpointRef, Target: goal, IntentSource: "Synthetic explicit short milestone then continue", Consumers: []ProgressionConsumer{}}
			apTestPut(t, f.Root, "public-target-input.json", input)
			plan := filepath.Join(t.TempDir(), "target-plan.json")
			for _, args := range [][]string{{"lifecycle", "target", "--root", f.Root, "--checkpoint", f.CheckpointRef, "--input", "public-target-input.json", "--plan", "--out", plan, "--json"}, {"lifecycle", "target", "--root", f.Root, "--apply", "--plan-file", plan, "--json"}} {
				if raw, err := run(args...); err != nil {
					t.Fatalf("public target %s: %v %s", goal, err, raw)
				}
			}
			raw, err := run(worker...)
			if goal == "spec-approved" {
				if err == nil || !bytes.Contains(raw, []byte("PROGRESSION_TARGET_BLOCKED")) {
					t.Fatalf("reached short goal did not block existing Worker: %v %s", err, raw)
				}
			} else if err != nil {
				t.Fatalf("pending business goal did not resume original Worker: %v %s", err, raw)
			}
		}
		after := progressionInventory(t, f.Root)
		for ref, value := range original {
			if after[ref] != value {
				t.Fatalf("short stop/continue changed original approved asset/CP/Ticket/Task: %s", ref)
			}
		}
	})
	for _, variant := range []string{"wrong-explicit-checkpoint", "missing-current-registration", "ambiguous-current-registration"} {
		t.Run(variant, func(t *testing.T) {
			args := map[string]string{"kind": "task", "file": f.TaskRef}
			mapRef := filepath.ToSlash(filepath.Join(filepath.Dir(f.CheckpointRef), "map.md"))
			mapBytes := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, mapRef))
			switch variant {
			case "wrong-explicit-checkpoint":
				task := semMap(mustParseContract(mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.TaskRef))))
				args["checkpoint"] = text(semList(semMap(task["contract"])["gate_refs"])[0])
			case "missing-current-registration":
				if err := os.Remove(filepath.Join(f.Root, mapRef)); err != nil {
					t.Fatal(err)
				}
				defer apTestPut(t, f.Root, mapRef, mapBytes)
			case "ambiguous-current-registration":
				duplicate := filepath.ToSlash(filepath.Join(filepath.Dir(filepath.Dir(f.CheckpointRef)), "duplicate", "map.md"))
				apTestPut(t, f.Root, duplicate, mapBytes)
				defer os.Remove(filepath.Join(f.Root, duplicate))
			}
			before := progressionInventory(t, f.Root)
			if _, err := RunContext(context.Background(), "contract", "verify", f.Root, args); err == nil {
				t.Fatalf("public Worker accepted %s", variant)
			}
			if !reflect.DeepEqual(before, progressionInventory(t, f.Root)) {
				t.Fatal("invalid explicit/registered task validation wrote inputs")
			}
		})
	}
	planBytes := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.FrontendPlanRef))
	reportBytes := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.FrontendVerificationRef))
	for _, variant := range []string{"optional-slice-bindings-absent", "valid-future-date", "missing-interaction-case", "duplicate-interaction", "missing-visual-case", "failed-pnpm", "explicit-external-binding", "intent-as-console-evidence"} {
		t.Run(variant, func(t *testing.T) {
			defer apTestPut(t, f.Root, f.FrontendPlanRef, planBytes)
			defer apTestPut(t, f.Root, f.FrontendVerificationRef, reportBytes)
			report := semMap(mustParseContract(reportBytes))
			code := ""
			switch variant {
			case "optional-slice-bindings-absent":
				plan := semMap(mustParseContract(planBytes))
				for _, doc := range []map[string]any{plan, report} {
					delete(doc, "slice_id")
					delete(doc, "slice_contract_ref")
				}
				apTestPut(t, f.Root, f.FrontendPlanRef, plan)
				semMap(report["implementation_plan"])["digest"] = "sha256:" + safefs.Digest(mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.FrontendPlanRef)))
			case "valid-future-date":
				semMap(semList(report["actual_verification"])[0])["executed_at"] = "2036-01-01T00:00:00Z"
			case "missing-interaction-case":
				report["interaction_results"] = semList(report["interaction_results"])[1:]
				code = "FRONTEND_COVERAGE"
			case "duplicate-interaction":
				rows := semList(report["interaction_results"])
				report["interaction_results"] = append(rows, rows[0])
				code = "FRONTEND_INTERACTION"
			case "missing-visual-case":
				report["case_results"] = semList(report["case_results"])[1:]
				code = "FRONTEND_COVERAGE"
			case "failed-pnpm":
				semMap(semList(report["actual_verification"])[0])["exit_code"] = 1
				code = "FRONTEND_IMPLEMENTATION_FAILED"
			case "explicit-external-binding":
				report["frontend_delivery"] = map[string]any{"acceptance_ref": "missing-external-receipt.json"}
				code = "FRONTEND_BINDING"
			case "intent-as-console-evidence":
				configRef := filepath.ToSlash(filepath.Join(filepath.Dir(f.CheckpointRef), progressionFile))
				apTestPut(t, f.Root, configRef, ProgressionTarget{SchemaVersion: 1, Kind: "lifecycle-progression-target", FeatureID: text(cp["feature_id"]), CheckpointRef: f.CheckpointRef, Target: "spec-approved", IntentSource: "Synthetic raw proof boundary", Consumers: []ProgressionConsumer{}})
				defer os.Remove(filepath.Join(f.Root, configRef))
				report["console_evidence"] = map[string]any{"ref": configRef, "digest": "sha256:" + safefs.Digest(mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, configRef)))}
				code = "PROGRESSION_EVIDENCE"
			}
			apTestPut(t, f.Root, f.FrontendVerificationRef, report)
			before := progressionInventory(t, f.Root)
			_, err := RunContext(context.Background(), "evidence", "verify", f.Root, map[string]string{"kind": "verification", "file": f.FrontendVerificationRef, "checkpoint": f.CheckpointRef})
			if code == "" {
				if err != nil {
					t.Fatalf("valid existing raw protocol %s: %v", variant, err)
				}
			} else if err == nil {
				t.Fatalf("raw current evidence accepted %s", variant)
			} else {
				apTestCode(t, err, code)
			}
			if !reflect.DeepEqual(before, progressionInventory(t, f.Root)) {
				t.Fatal("raw protocol validator wrote evidence")
			}
		})
	}
	s := newSemanticSession(context.Background(), f.Root, map[string]string{"checkpoint": f.CheckpointRef})
	if err := progressionFrontendImplementationCompletion(s, f.CheckpointRef, cp); err == nil {
		t.Fatal("raw verification alone granted independent frontend completion")
	}
	apTestPut(t, f.Root, f.CheckpointRef, cpBytes)
	s = newSemanticSession(context.Background(), f.Root, map[string]string{"checkpoint": f.CheckpointRef})
	cp = semMap(mustParseContract(cpBytes))
	if err := progressionFrontendImplementationCompletion(s, f.CheckpointRef, cp); err != nil {
		t.Fatalf("current independent frontend completion: %v", err)
	}
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	// The approved scope and report stay fixed while implementation code drifts.
	t.Run("current-committed-code-drift", func(t *testing.T) {
		cmd := exec.Command("git", "ls-files", "src")
		cmd.Dir = f.ProjectRoot
		raw, err := cmd.Output()
		if err != nil || strings.TrimSpace(string(raw)) == "" {
			t.Fatalf("actual committed implementation inventory: %v %s", err, raw)
		}
		ref := strings.Split(strings.TrimSpace(string(raw)), "\n")[0]
		original := mustReadSpecBaselineTestFile(t, filepath.Join(f.ProjectRoot, ref))
		defer apTestPut(t, f.ProjectRoot, ref, original)
		apTestPut(t, f.ProjectRoot, ref, append(original, []byte("\n// synthetic candidate drift\n")...))
		s := newSemanticSession(context.Background(), f.Root, map[string]string{"checkpoint": f.CheckpointRef})
		if err := progressionFrontendImplementationCompletion(s, f.CheckpointRef, cp); err == nil {
			t.Fatal("fixed frontend approval survived current implementation code drift")
		}
	})
	t.Run("fresh-compiler-drift", func(t *testing.T) {
		ref := ".agents/skills/yss-implementation-contract-compiler/references/compiler-contract.yaml"
		original := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, ref))
		defer apTestPut(t, f.Root, ref, original)
		compiler := semMap(mustParseContract(original))
		compiler["schema_version"] = 99
		apTestPut(t, f.Root, ref, compiler)
		s := newSemanticSession(context.Background(), f.Root, map[string]string{"checkpoint": f.CheckpointRef})
		if err := frontendImplementationCurrent(s, f.CheckpointRef, cp, f.FrontendVerificationRef); err == nil {
			t.Fatal("frontend completion skipped current compiled Slice Fresh binding")
		}
	})
	t.Run("approved-unrelated-readable-prototype", func(t *testing.T) {
		plan := semMap(mustParseContract(planBytes))
		report := semMap(mustParseContract(reportBytes))
		defer apTestPut(t, f.Root, f.FrontendPlanRef, planBytes)
		defer apTestPut(t, f.Root, f.FrontendVerificationRef, reportBytes)
		apTestPut(t, f.Root, "frontend/unrelated-readable.html", "Synthetic unrelated prototype")
		defer os.Remove(filepath.Join(f.Root, "frontend/unrelated-readable.html"))
		plan["prototype_ref"], report["prototype_ref"] = "frontend/unrelated-readable.html", "frontend/unrelated-readable.html"
		apTestPut(t, f.Root, f.FrontendPlanRef, plan)
		semMap(report["implementation_plan"])["digest"] = "sha256:" + safefs.Digest(mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.FrontendPlanRef)))
		apTestPut(t, f.Root, f.FrontendVerificationRef, report)
		current := refreshFrontendTestReview(t, f, nil)
		s := newSemanticSession(context.Background(), f.Root, map[string]string{"checkpoint": f.CheckpointRef})
		if err := progressionFrontendImplementationCompletion(s, f.CheckpointRef, current); err == nil {
			t.Fatal("independently bound report substituted an unrelated readable prototype")
		} else {
			apTestCode(t, err, "FRONTEND_BINDING")
		}
	})
	t.Run("current-original-worktree-candidate", func(t *testing.T) {
		cmd := exec.Command("git", "rev-parse", "HEAD")
		cmd.Dir = f.ProjectRoot
		headBytes, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		head := strings.TrimSpace(string(headBytes))
		base := ".template-source/evidence/maintenance/frontend-current"
		ref := base + "/candidate-manifest.yaml"
		defer os.RemoveAll(filepath.Join(f.ProjectRoot, base))
		diffCmd := exec.Command("git", "diff", "--binary", "--full-index", head)
		diffCmd.Dir = f.ProjectRoot
		diff, err := diffCmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		var stream bytes.Buffer
		stream.WriteString("YSS-WORKTREE-CANDIDATE-V1\x00")
		stream.WriteByte(0x54)
		_ = binary.Write(&stream, binary.BigEndian, uint64(len(diff)))
		stream.Write(diff)
		digest := safefs.Digest(stream.Bytes())
		apTestPut(t, f.ProjectRoot, ref, map[string]any{"schema_version": 1, "candidate_kind": "yss-worktree-candidate-v1", "storage": "packed-stream", "review_mode": "worktree", "review_base_ref": head, "merge_base": head, "implementation_candidate_ref": "working-tree", "candidate_snapshot_ref": ref, "candidate_digest": digest, "tracked_diff_command": "git diff --binary --full-index " + head, "commit_list_command": "git log", "untracked_inventory_command": "git ls-files --others", "untracked_diff_command": "packed in candidate.bin", "untracked_files": []any{}, "untracked_path_bytes": []any{}, "excluded_paths": []any{base}, "snapshot_stream_ref": base + "/candidate.bin", "tracked_diff_ref": base + "/tracked.diff"})
		apTestPut(t, f.ProjectRoot, base+"/candidate.bin", stream.Bytes())
		apTestPut(t, f.ProjectRoot, base+"/tracked.diff", diff)
		report := semMap(mustParseContract(reportBytes))
		defer apTestPut(t, f.Root, f.FrontendVerificationRef, reportBytes)
		semMap(report["independent_review"])["candidate_digest"] = "sha256:" + digest
		apTestPut(t, f.Root, f.FrontendVerificationRef, report)
		input := map[string]any{"scope_kind": "change", "slice_contract_ref": f.SliceRef, "work_unit_id": f.FrontendWorkUnitID, "project_root": f.ProjectRoot, "review_mode": "worktree", "implementation_candidate_ref": "working-tree", "candidate_snapshot_ref": ref, "review_base_ref": head, "candidate_digest": digest}
		current := refreshFrontendTestReview(t, f, input)
		s := newSemanticSession(context.Background(), f.Root, map[string]string{"checkpoint": f.CheckpointRef})
		if err := progressionFrontendImplementationCompletion(s, f.CheckpointRef, current); err != nil {
			t.Fatalf("original current worktree candidate: %v", err)
		}
		if err := s.finish(); err != nil {
			t.Fatal(err)
		}
		apTestPut(t, f.ProjectRoot, "synthetic-unreviewed.txt", "Synthetic uncaptured implementation")
		defer os.Remove(filepath.Join(f.ProjectRoot, "synthetic-unreviewed.txt"))
		s = newSemanticSession(context.Background(), f.Root, map[string]string{"checkpoint": f.CheckpointRef})
		if err := progressionFrontendImplementationCompletion(s, f.CheckpointRef, current); err == nil {
			t.Fatal("frontend completion survived new uncaptured worktree bytes")
		}
	})
}

// Rebind a synthetic original review using the existing subject/record protocol.
// Native rules, roles, schemas, the original Slice and its approval stay untouched.
func refreshFrontendTestReview(t *testing.T, f localImplementationTestFixture, input map[string]any) map[string]any {
	t.Helper()
	cp := semMap(mustParseContract(mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.CheckpointRef))))
	check := semMap(semMap(cp["checks"])["check.frontend-implementation-verified"])
	subjectRef, proofRef := text(check["subject_ref"]), text(check["approval_ref"])
	refs := []string{f.CheckpointRef, subjectRef, proofRef}
	for _, ref := range refs {
		before := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, ref))
		t.Cleanup(func() { apTestPut(t, f.Root, ref, before) })
	}
	subject := semMap(mustParseContract(mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, subjectRef))))
	proof := semMap(mustParseContract(mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, proofRef))))
	if input != nil {
		subject["review_input"] = input
	}
	for _, row := range semList(subject["basis"]) {
		binding := semMap(row)
		binding["digest"] = safefs.Digest(mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, text(binding["ref"]))))
	}
	apTestPut(t, f.Root, subjectRef, subject)
	proof["basis"], proof["subject_digest"] = subject["basis"], safefs.Digest(mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, subjectRef)))
	for _, row := range semList(proof["artifact_bindings"]) {
		semMap(row)["digest"] = "sha256:" + safefs.Digest(mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.FrontendVerificationRef)))
	}
	apTestPut(t, f.Root, proofRef, proof)
	check["subject_digest"] = proof["subject_digest"]
	for _, row := range semList(check["basis"]) {
		binding := semMap(row)
		binding["digest"] = safefs.Digest(mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, text(binding["ref"]))))
	}
	for _, id := range []string{"artifact.frontend-implementation-plan", "artifact.frontend-implementation-verification"} {
		asset := semMap(semMap(cp["artifacts"])[id])
		asset["digest"] = "sha256:" + safefs.Digest(mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, text(asset["ref"]))))
	}
	apTestPut(t, f.Root, f.CheckpointRef, cp)
	return cp
}

func TestLocalFrontendNativeMixedBusinessGoalSequence(t *testing.T) {
	seed := actualLocalImplementationNativeSeed(t)
	seedBefore := progressionInventory(t, seed)
	f := actualLocalImplementationFixture(t, seed, true, true)
	before := progressionInventory(t, f.Root)
	cpBytes := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.CheckpointRef))
	cp := semMap(mustParseContract(cpBytes))
	ticketsRef := checkpointAsset(cp, "business_ticket_set_ref", "artifact.business-ticket-set")
	ticketBytes := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, ticketsRef))
	// A fresh default Spec feature accepts the whole business without a sidecar.
	result, err := progressionRead(context.Background(), f.Root, f.CheckpointRef)
	if err != nil {
		t.Fatal(err)
	}
	if p := semMap(result["progression"]); p["target"] != "business-accepted" || p["reached"] != true {
		t.Fatalf("current mixed default business not reached: status=%v reason=%v", p["status"], p["reason"])
	}
	for _, target := range []string{"spec-approved", "product-design-completed", "frontend-accepted", "business-accepted"} {
		input := ProgressionTarget{SchemaVersion: 1, Kind: "lifecycle-progression-target", FeatureID: text(cp["feature_id"]), CheckpointRef: f.CheckpointRef, Target: target, IntentSource: "Synthetic same-feature local milestone continuation", Consumers: []ProgressionConsumer{}}
		apTestPut(t, f.Root, "target-input.json", input)
		plan := filepath.Join(t.TempDir(), "target-plan.json")
		if _, err := progressionTargetRun(context.Background(), f.Root, map[string]string{"checkpoint": f.CheckpointRef, "input": "target-input.json", "plan": "true", "out": plan}); err != nil {
			t.Fatal(err)
		}
		if _, err := progressionTargetRun(context.Background(), f.Root, map[string]string{"apply": "true", "plan-file": plan}); err != nil {
			t.Fatal(err)
		}
		result, err := progressionRead(context.Background(), f.Root, f.CheckpointRef)
		if err != nil {
			t.Fatal(err)
		}
		p := semMap(result["progression"])
		if p["target"] != target || p["status"] != "reached" || p["reached"] != true || semMap(result["next_action"])["root_work_unit"] != cp["next_work_unit"] {
			t.Fatalf("current mixed target %s: %#v", target, p)
		}
	}
	after := progressionInventory(t, f.Root)
	for ref, descriptor := range before {
		if strings.HasPrefix(ref, ".yss/transactions/") {
			continue
		}
		if after[ref] != descriptor {
			t.Fatalf("goal continuation changed original approved bytes/mode: %s", ref)
		}
	}
	if !bytes.Equal(cpBytes, mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.CheckpointRef))) || !bytes.Equal(ticketBytes, mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, ticketsRef))) || !reflect.DeepEqual(seedBefore, progressionInventory(t, seed)) {
		t.Fatal("goal continuation changed original approval checkpoint, stable Ticket IDs or native seed")
	}
	t.Run("frontend-goal-requires-current-backend-input", func(t *testing.T) {
		configRef := filepath.ToSlash(filepath.Join(filepath.Dir(f.CheckpointRef), progressionFile))
		configBytes := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, configRef))
		defer apTestPut(t, f.Root, configRef, configBytes)
		config := semMap(mustParseContract(configBytes))
		config["target"] = "frontend-accepted"
		apTestPut(t, f.Root, configRef, config)
		raw := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.BackendTerminalRef))
		defer apTestPut(t, f.Root, f.BackendTerminalRef, raw)
		if err := os.Remove(filepath.Join(f.Root, f.BackendTerminalRef)); err != nil {
			t.Fatal(err)
		}
		before := progressionInventory(t, f.Root)
		result, err := progressionRead(context.Background(), f.Root, f.CheckpointRef)
		if err == nil && semMap(result["progression"])["reached"] == true {
			t.Fatal("frontend goal accepted missing current required backend input")
		}
		if !reflect.DeepEqual(before, progressionInventory(t, f.Root)) {
			t.Fatal("negative frontend qualification changed inputs")
		}
	})
	for _, variant := range []string{"missing-current-backend-terminal", "missing-independent-frontend-check", "explicit-frontend-consumer-without-current-receipt"} {
		t.Run(variant, func(t *testing.T) {
			configRef := filepath.ToSlash(filepath.Join(filepath.Dir(f.CheckpointRef), progressionFile))
			configBytes := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, configRef))
			defer apTestPut(t, f.Root, configRef, configBytes)
			switch variant {
			case "missing-current-backend-terminal":
				if f.BackendTerminalRef == "" {
					t.Fatal("mixed fixture lacks its real local backend terminal")
				}
				raw := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.BackendTerminalRef))
				defer apTestPut(t, f.Root, f.BackendTerminalRef, raw)
				if err := os.Remove(filepath.Join(f.Root, f.BackendTerminalRef)); err != nil {
					t.Fatal(err)
				}
			case "missing-independent-frontend-check":
				current := semMap(mustParseContract(cpBytes))
				delete(semMap(current["checks"]), "check.frontend-implementation-verified")
				apTestPut(t, f.Root, f.CheckpointRef, current)
				defer apTestPut(t, f.Root, f.CheckpointRef, cpBytes)
			case "explicit-frontend-consumer-without-current-receipt":
				config := semMap(mustParseContract(configBytes))
				config["consumers"] = []ProgressionConsumer{{Profile: "frontend", Root: t.TempDir(), CheckpointRef: f.CheckpointRef}}
				apTestPut(t, f.Root, configRef, config)
			}
			before := progressionInventory(t, f.Root)
			result, err := progressionRead(context.Background(), f.Root, f.CheckpointRef)
			if err == nil && semMap(result["progression"])["reached"] == true {
				t.Fatalf("whole business accepted %s", variant)
			}
			if !reflect.DeepEqual(before, progressionInventory(t, f.Root)) {
				t.Fatal("negative whole-business validation changed inputs")
			}
		})
	}
}

func localFrontendPolicyFixture(t *testing.T) string {
	t.Helper()
	oracle := governanceOracleRoot(t)
	program := `import path from 'node:path';import {pathToFileURL} from 'node:url';
const source=process.argv[1];const {localBackendTerminalFixture,registerLocalBackendFeature}=await import(pathToFileURL(path.join(source,'scripts/fixtures/backend-delivery/local-terminal-fixture.mjs')).href);
const f=localBackendTerminalFixture();registerLocalBackendFeature(f);console.log(JSON.stringify({root:f.root}));`
	cmd := exec.Command("node", "--input-type=module", "-e", program, oracle)
	cmd.Dir = oracle
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("current synthetic local policy fixture: %v %s", err, raw)
	}
	var f struct{ Root string }
	if err = json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(f.Root) })
	return f.Root
}

func TestLocalFrontendDeclaredPolicyFailsClosed(t *testing.T) {
	oracle := governanceOracleRoot(t)
	policyBytes, err := os.ReadFile(filepath.Join(oracle, guidanceContractRef("spec")))
	if err != nil {
		t.Fatal(err)
	}
	root := localFrontendPolicyFixture(t)
	profileBytes := mustReadSpecBaselineTestFile(t, filepath.Join(root, ".template-spec/process/harness-profile.yaml"))
	for _, variant := range []string{"current-policy", "old-no-marker", "unknown-version", "unknown-capability", "unknown-marker", "wrong-marker-type", "wrong-profile"} {
		t.Run(variant, func(t *testing.T) {
			defer apTestPut(t, root, guidanceContractRef("spec"), policyBytes)
			defer apTestPut(t, root, ".template-spec/process/harness-profile.yaml", profileBytes)
			contract := semMap(mustParseContract(policyBytes))
			policy := semMap(contract["progression_target"])
			switch variant {
			case "old-no-marker":
				delete(policy, "local_implementation_inputs")
			case "unknown-version":
				policy["schema_version"] = 99
			case "unknown-capability":
				policy["required_capabilities"] = []any{"lifecycle-target-v99"}
			case "unknown-marker":
				policy["local_implementation_inputs"] = "asserted-local-ready"
			case "wrong-marker-type":
				policy["local_implementation_inputs"] = true
			case "wrong-profile":
				profile := semMap(mustParseContract(profileBytes))
				profile["profile_id"] = "harness.backend-delivery"
				apTestPut(t, root, ".template-spec/process/harness-profile.yaml", profile)
			}
			apTestPut(t, root, guidanceContractRef("spec"), contract)
			before := progressionInventory(t, root)
			s := newSemanticSession(context.Background(), root, nil)
			selected, err := hasLocalImplementationInputs(s)
			if variant == "current-policy" {
				if err != nil || !selected {
					t.Fatalf("current policy not selected: %t %v", selected, err)
				}
			} else if variant == "old-no-marker" {
				if err != nil || selected {
					t.Fatalf("old policy changed route: %t %v", selected, err)
				}
			} else if err == nil || selected {
				t.Fatalf("unknown declared policy passed: %t %v", selected, err)
			}
			if !reflect.DeepEqual(before, progressionInventory(t, root)) {
				t.Fatal("policy selection wrote project state")
			}
		})
	}
}

func TestFrontendVerificationTaskPreservesExternalRoute(t *testing.T) {
	root := apTestRoot(t)
	// This routing regression deliberately supplies an unsupported external
	// record. It must reach the original receipt reader, without trying to read
	// a missing local CP or inventing a feature. It is not an acceptance fixture.
	apTestPut(t, root, "external-acceptance.json", map[string]any{"schema_version": 999})
	task := map[string]any{"work_unit_id": "work-unit.frontend-implementation-verification", "slice_id": "slice.external", "frontend_delivery": map[string]any{"acceptance_ref": "external-acceptance.json"}, "contract": map[string]any{"kind": "lifecycle-work-unit", "lifecycle_ref": approvalRegistryRef}}
	s := newSemanticSession(context.Background(), root, nil)
	if err := taskFrontendDeliverySemantic(s, task); err == nil {
		t.Fatal("unknown external record was accepted")
	} else {
		apTestCode(t, err, "CAPABILITY")
	}
	localRoot := localFrontendPolicyFixture(t)
	delete(task, "frontend_delivery")
	delete(semMap(task["contract"]), "lifecycle_ref")
	local := newSemanticSession(context.Background(), localRoot, nil)
	if err := taskFrontendDeliverySemantic(local, task); err == nil {
		t.Fatal("local formal verification invented a checkpoint")
	} else {
		apTestCode(t, err, "FRONTEND_DELIVERY")
	}
}

func TestLocalFrontendSpecialistPolicySelectsOwnApprovedAssets(t *testing.T) {
	root := localFrontendPolicyFixture(t)
	metadata := semMap(mustParseContract(mustReadSpecBaselineTestFile(t, filepath.Join(root, ".yss.json"))))
	metadata["profile"] = "frontend"
	metadata["profileId"] = "harness.frontend-delivery"
	apTestPut(t, root, ".yss.json", metadata)
	apTestPut(t, root, ".template-spec/process/harness-profile.yaml", map[string]any{"schema_version": 2, "profile_id": "harness.frontend-delivery"})
	oracle := governanceOracleRoot(t)
	contract := semMap(mustParseContract(mustReadSpecBaselineTestFile(t, filepath.Join(oracle, guidanceContractRef("spec")))))
	policy := semMap(contract["progression_target"])
	policy["local_implementation_inputs"] = "native-profile-current-feature-approved-assets"
	policy["writer_profiles"] = []any{}
	apTestPut(t, root, guidanceContractRef("frontend"), contract)
	s := newSemanticSession(context.Background(), root, nil)
	selected, err := hasLocalImplementationInputs(s)
	if err != nil || !selected {
		t.Fatalf("specialist policy rejected: %t %v", selected, err)
	}
	// Policy selection alone never replaces current feature, Plan/Spec or Slice validation.
	if err = contractLocalFrontendInputs(s, nil, map[string]string{"phase": "implementation", "checkpoint": "missing-checkpoint.yaml"}); err == nil {
		t.Fatal("missing approved input accepted")
	}
}
