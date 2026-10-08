package governance

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestProfileGuidanceExistingEmptyAndNonemptyTargets(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if status, problem := guidanceEngineering(root, "design"); status != "not-initialized" || problem != "" {
		t.Fatalf("empty safe target cannot initialize: %s %s", status, problem)
	}
	if err := os.WriteFile(filepath.Join(root, "business.txt"), []byte("existing business asset"), 0600); err != nil {
		t.Fatal(err)
	}
	before := verificationTree(t, root)
	if status, problem := guidanceEngineering(root, "design"); status != "needs-attention" || problem == "" {
		t.Fatalf("ordinary nonempty target missed attach boundary: %s %s", status, problem)
	}
	if !contractSame(before, verificationTree(t, root)) {
		t.Fatal("target guidance changed existing business assets")
	}
}

func guidanceTestRoot(t *testing.T, profile string) string {
	t.Helper()
	root := apTestRoot(t)
	apTestPut(t, root, "yss-project.yaml", map[string]any{"schema_version": 1, "repository_mode": "project-instance"})
	apTestPut(t, root, ".template-spec/process/harness-profile.yaml", map[string]any{"profile_id": profile})
	apTestPut(t, root, "CONTEXT.md", []byte("---\ncontext_schema_version: 1\n---\n# 业务上下文\n\n## 业务术语\n\n| 术语 | 含义 | 英文标识 | 适用业务责任区 | 避免 / 备注 |\n|---|---|---|---|---|\n"))
	return root
}

func TestProfileGuidanceOldPolicyAndNoNeighborInference(t *testing.T) {
	root := guidanceTestRoot(t, "harness.spec-template")
	g := ProfileGuidance(context.Background(), root, "", nil)
	if g.Status != "unsupported" {
		t.Fatalf("old policy: %+v", g)
	}
	apTestPut(t, root, guidanceContractRef("spec"), map[string]any{"schema_version": 1, "profile_guidance": map[string]any{
		"schema_version": 1, "links_file": ".yss-profile-links.json", "routes": map[string]any{"spec": map[string]any{"targets": []string{"design"}, "default": "continue-current-spec"}},
	}})
	before := verificationTree(t, root)
	g = ProfileGuidance(context.Background(), root, "", nil)
	if len(g.Recommendations) != 1 || g.Recommendations[0].EngineeringStatus != "unlinked" || g.Recommendations[0].TargetRoot != "" || g.Recommendations[0].InputStatus != "pending-verification" {
		t.Fatalf("unknown input/target fabricated: %+v", g)
	}
	if !contractSame(before, verificationTree(t, root)) {
		t.Fatal("readonly guidance wrote files")
	}
}

func TestProfileGuidanceLinkedMissingAndWrongTarget(t *testing.T) {
	root := guidanceTestRoot(t, "harness.business-ddd-strategy-handoff")
	apTestPut(t, root, guidanceContractRef("design"), map[string]any{"schema_version": 1, "profile_guidance": map[string]any{
		"schema_version": 1, "links_file": ".yss-profile-links.json", "routes": map[string]any{"design": map[string]any{"targets": []string{"backend", "frontend"}}},
	}})
	missing := filepath.Join(apTestRoot(t), "backend")
	wrong := guidanceTestRoot(t, "harness.backend-delivery")
	apTestPut(t, root, ".yss-profile-links.json", map[string]any{"schema_version": 1, "links": map[string]string{"backend": missing, "frontend": wrong}})
	g := ProfileGuidance(context.Background(), root, "", nil)
	if len(g.Recommendations) != 2 || g.Recommendations[0].EngineeringStatus != "not-initialized" || g.Recommendations[1].EngineeringStatus != "needs-attention" {
		t.Fatalf("%+v", g)
	}
	for _, command := range g.Recommendations[1].Commands {
		if len(command) > 1 && command[1] == "attach" {
			t.Fatal("conflicting Profile was treated as an ordinary attach target")
		}
	}
}

func TestProfileGuidanceSourceApprovalDoesNotQualifyTargetInputs(t *testing.T) {
	for _, engineering := range []string{"unlinked", "not-initialized", "initialized"} {
		t.Run(engineering, func(t *testing.T) {
			sourceRoot := apTestRoot(t)
			cp := map[string]any{"feature_id": "feature.supplier", "version": "v1"}
			apTestPut(t, sourceRoot, "checkpoint.json", cp)
			targetRoot := apTestRoot(t)
			before := verificationTree(t, targetRoot)
			status, _, command := guidanceTargetInput(context.Background(), newSemanticSession(context.Background(), sourceRoot, nil), "spec", "checkpoint.json", cp, targetRoot, "design", engineering)
			if status != "waiting-input" {
				t.Fatalf("%s inferred target intake: %s", engineering, status)
			}
			if engineering == "initialized" && (len(command) < 3 || command[2] != "export") {
				t.Fatalf("missing receipt did not offer source export: %v", command)
			}
			if !contractSame(before, verificationTree(t, targetRoot)) {
				t.Fatal("target input query wrote files")
			}
		})
	}
}

func TestProfileGuidanceUsesRegisteredCheckpointWithoutGuessingDirectorySlug(t *testing.T) {
	root := apTestRoot(t)
	cp := map[string]any{"feature_id": "feature.supplier"}
	apTestPut(t, root, "intake-checkpoint.json", cp)
	apTestPut(t, root, "docs/.scratch/feature.supplier/map.md", "---\ncheckpoint_ref: intake-checkpoint.json\n---\n# 已登记工作\n")
	ref, _, err := guidanceRegisteredCheckpoint(newSemanticSession(context.Background(), root, nil), "docs/.scratch", "feature.supplier")
	if err != nil || ref != "intake-checkpoint.json" {
		t.Fatalf("registered checkpoint ignored: %s %v", ref, err)
	}
	apTestPut(t, root, "second-checkpoint.json", cp)
	apTestPut(t, root, "docs/.scratch/supplier/map.md", "---\ncheckpoint_ref: second-checkpoint.json\n---\n# 第二入口\n")
	if _, _, err := guidanceRegisteredCheckpoint(newSemanticSession(context.Background(), root, nil), "docs/.scratch", "feature.supplier"); err == nil {
		t.Fatal("ambiguous registration qualified intake")
	}
}

func TestProfileGuidanceSpecStrategicHandoffRoutesAndIntake(t *testing.T) {
	oracle := governanceOracleRoot(t)
	if _, err := os.Stat(filepath.Join(oracle, "scripts/fixtures/spec-baseline/fixture.mjs")); err != nil {
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		t.Skip("fixed historical template has no Spec-baseline fixture; old-policy rejection is tested separately")
	}
	source := specBaselineTestNativeSeed(t, "spec")
	baselineParent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	baselineSource := filepath.Join(baselineParent, "baseline-spec")
	backend, frontend := specBaselineTestNativeSeed(t, "backend"), specBaselineTestNativeSeed(t, "frontend")
	for profile, root := range map[string]string{"spec": source, "backend": backend, "frontend": frontend} {
		base := oracle
		if profile != "spec" {
			base = filepath.Join(oracle, "submodules/yss-harness-"+profile+"-agent")
		}
		for _, ref := range []string{guidanceContractRef(profile), ".template-spec/process/lifecycle-registry.yaml", ".template-spec/agents/yss-skill-registry.yaml"} {
			raw, err := os.ReadFile(filepath.Join(base, ref))
			if err != nil {
				t.Fatal(err)
			}
			apTestPut(t, root, ref, raw)
		}
	}
	program := `import fs from 'node:fs';import path from 'node:path';
import {fixture} from './scripts/fixtures/strategic-handoff/fixture.mjs';
import {approvedSpecFixture} from './scripts/fixtures/spec-baseline/fixture.mjs';
import {finalizeDelivery,importBundle} from './scripts/lib/strategic-handoff.mjs';
const root=process.argv[1];await fixture(root,{handoffVersion:5,businessTickets:true});
const result=await finalizeDelivery({sourceRoot:root,handoffRef:'handoff.yaml'});
for(const target of process.argv.slice(2,4)){fs.copyFileSync(path.join(root,'CONTEXT.md'),path.join(target,'CONTEXT.md'));await importBundle({bundle:result.delivery,targetRoot:target});}
await approvedSpecFixture(process.argv[4],{nativeSeed:root});
console.log(JSON.stringify({delivery:path.relative(root,result.delivery).split(path.sep).join('/')+'/delivery-record.json'}));`
	cmd := exec.Command("node", "--input-type=module", "-e", program, source, backend, frontend, baselineSource)
	cmd.Dir = oracle
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Spec Handoff fixture: %v %s", err, out)
	}
	var produced struct{ Delivery string }
	if err := json.Unmarshal(out, &produced); err != nil {
		t.Fatal(err)
	}
	cpRef := "current-checkpoint.json"
	cp := map[string]any{"feature_id": "feature.supplier", "artifacts": map[string]any{"artifact.strategic-handoff-delivery": map[string]any{"ref": produced.Delivery, "status": "approved"}}, "blockers": []any{}}
	apTestPut(t, source, cpRef, cp)
	apTestPut(t, source, ".yss-profile-links.json", map[string]any{"schema_version": 1, "links": map[string]string{"backend": backend, "frontend": frontend}})
	sourceSession := newSemanticSession(context.Background(), source, nil)
	sourceProfile, err := sourceSession.doc(".template-spec/process/harness-profile.yaml")
	if err != nil || sourceProfile["profile_id"] != "harness.spec-template" {
		t.Fatalf("fixture did not retain its genuine Spec identity: %v %v", sourceProfile, err)
	}
	report := map[string]any{"schema_version": 1, "test": t.Name(), "fixture_kind": "synthetic-approved-assets-with-native-profile-identity", "source_profile": sourceProfile, "source_checkpoint": cp, "source_policy_ref": guidanceContractRef("spec"), "source_policy_digest": "sha256:" + safefs.Digest(mustReadSpecBaselineTestFile(t, filepath.Join(source, guidanceContractRef("spec")))), "targets": map[string]any{}, "negative_source_checks": map[string]any{}}
	for key, ref := range map[string]string{"source_native_metadata": ".yss.json", "source_handoff": "handoff.yaml", "source_delivery": produced.Delivery} {
		doc, err := sourceSession.doc(ref)
		if err != nil {
			t.Fatal(err)
		}
		report[key] = doc
	}
	before := verificationTree(t, source)
	g := ProfileGuidance(context.Background(), source, cpRef, cp)
	for _, target := range []string{"backend", "frontend"} {
		var found *ProfileRecommendation
		for i := range g.Recommendations {
			if g.Recommendations[i].Profile == target {
				found = &g.Recommendations[i]
			}
		}
		if found == nil || found.Recommendation != "required" || found.InputStatus != "pending-verification" {
			t.Fatalf("actual Spec Handoff lost %s consumer/receipt: %+v", target, g)
		}
		for _, command := range found.Commands {
			if strings.Contains(strings.Join(command, " "), "--kind spec-baseline") {
				t.Fatalf("Spec → %s incorrectly consumed a Design baseline: %v", target, command)
			}
		}
	}
	refs, err := ProfilePreparationSource(context.Background(), source, cpRef, []string{"backend", "frontend"})
	if err != nil || !semHas(refs, produced.Delivery) {
		t.Fatalf("joint preparation did not bind actual Spec delivery: %v %v", refs, err)
	}
	evidence := []map[string]any{}
	for _, ref := range refs {
		evidence = append(evidence, map[string]any{"ref": ref, "sha256": "sha256:" + safefs.Digest(mustReadSpecBaselineTestFile(t, filepath.Join(source, ref)))})
	}
	report["joint_preparation_source_evidence"] = evidence
	report["unregistered_target_guidance"] = g
	if !contractSame(before, verificationTree(t, source)) {
		t.Fatal("Spec delivery guidance wrote source assets")
	}
	baselineCPRef := "docs/.scratch/feature.supplier/checkpoint.json"
	baselineCP, err := newSemanticSession(context.Background(), baselineSource, nil).doc(baselineCPRef)
	if err != nil {
		t.Fatal(err)
	}
	defaultGuidance := ProfileGuidance(context.Background(), baselineSource, baselineCPRef, baselineCP)
	report["approved_spec_without_explicit_handoff"] = defaultGuidance
	if defaultGuidance.Default != "continue-current-spec" {
		t.Fatalf("Spec baseline default changed: %+v", defaultGuidance)
	}
	for _, recommendation := range defaultGuidance.Recommendations {
		if recommendation.Profile == "design" && recommendation.Recommendation != "optional" {
			t.Fatalf("Spec without explicit Handoff lost optional Design: %+v", defaultGuidance)
		}
		if recommendation.Profile != "design" && semHas([]string{"required", "optional"}, recommendation.Recommendation) {
			t.Fatalf("Spec baseline alone qualified a delivery Profile: %+v", defaultGuidance)
		}
	}
	baselineWithBadHandoff := semMap(mustParseContract(mustMarshalContract(baselineCP)))
	semMap(baselineWithBadHandoff["artifacts"])["artifact.strategic-handoff-delivery"] = map[string]any{"ref": "missing/delivery-record.json", "status": "approved"}
	apTestPut(t, baselineSource, baselineCPRef, baselineWithBadHandoff)
	if _, _, err := guidanceSource(newSemanticSession(context.Background(), baselineSource, nil), "spec", baselineCPRef, baselineWithBadHandoff); err == nil {
		t.Fatal("invalid explicit delivery fell back to the genuinely approved Spec baseline")
	}
	blockedGuidance := ProfileGuidance(context.Background(), baselineSource, baselineCPRef, nil)
	report["invalid_explicit_handoff_with_approved_baseline"] = blockedGuidance
	for _, recommendation := range blockedGuidance.Recommendations {
		if recommendation.InputStatus != "blocked" || semHas([]string{"required", "optional"}, recommendation.Recommendation) {
			t.Fatalf("invalid explicit delivery did not block guidance: %+v", blockedGuidance)
		}
	}
	apTestPut(t, baselineSource, baselineCPRef, baselineCP)

	receiptRef := "docs/handoffs/strategic-design-handoff.supplier/v1/import-receipt.json"
	for target, root := range map[string]string{"backend": backend, "frontend": frontend} {
		s := apTestSession(t, root)
		receipt, err := s.doc(receiptRef)
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := contractOpenHandoff(s, text(receipt["package_ref"]))
		if err != nil {
			t.Fatal(err)
		}
		reconRef := "target-context.json"
		apTestPut(t, root, reconRef, map[string]any{"schema_version": 1, "repository_mode": "project-instance", "stage": "stage.system-data-engineering", "work_unit": "work-unit.technical-analysis", "status": "reconciled", "context_snapshot": bundle.Handoff["source_context_snapshot"], "changes": map[string]any{"added": []any{}, "updated": []any{}, "deprecated": []any{}}, "unresolved_terms": []any{}, "evidence_refs": []any{receiptRef}})
		apTestPut(t, root, "target-checkpoint.json", map[string]any{"feature_id": "feature.supplier", "context_reconciliation": map[string]any{"status": "reconciled", "ref": reconRef}})
		apTestPut(t, root, trackerRef, "---\ntracker:\n  platform: local-markdown\n  root: docs/.scratch\n---\n# 已登记当前工作\n")
		apTestPut(t, root, "docs/.scratch/supplier/map.md", "---\ncheckpoint_ref: target-checkpoint.json\n---\n# 接收工作\n")
		reconRaw := mustReadSpecBaselineTestFile(t, filepath.Join(root, reconRef))
		status, missing, command := guidanceTargetInput(context.Background(), newSemanticSession(context.Background(), source, nil), "spec", cpRef, cp, root, target, "initialized")
		if status != "verified" || missing != "" || strings.Contains(strings.Join(command, " "), "spec-baseline") {
			t.Fatalf("actual %s strategic Receipt/Context was not verified: %s %s %v", target, status, missing, command)
		}
		targetReport := map[string]any{"receipt_ref": receiptRef, "receipt": receipt, "registered_checkpoint": "target-checkpoint.json", "context_reconciliation_ref": reconRef, "valid_context_input_status": status, "command": command}
		semMap(report["targets"])[target] = targetReport
		// A valid receiver record cannot replace the current source's package.
		current := append([]byte{}, mustReadSpecBaselineTestFile(t, filepath.Join(root, "CONTEXT.md"))...)
		apTestPut(t, root, "CONTEXT.md", strings.ReplaceAll(string(current), "提供产品或服务的主体", "不相关的客户语义"))
		altered, err := contextContractBytes(mustReadSpecBaselineTestFile(t, filepath.Join(root, "CONTEXT.md")))
		if err != nil {
			t.Fatal(err)
		}
		termsDigest, err := termsDigest(altered["business_terms"].([]Term))
		if err != nil {
			t.Fatal(err)
		}
		reconciliation, err := newSemanticSession(context.Background(), root, nil).doc(reconRef)
		if err != nil {
			t.Fatal(err)
		}
		semMap(reconciliation["context_snapshot"])["document_digest"] = altered["document_digest"]
		semMap(reconciliation["context_snapshot"])["referenced_terms_digest"] = termsDigest
		apTestPut(t, root, reconRef, reconciliation)
		status, missing, _ = guidanceTargetInput(context.Background(), newSemanticSession(context.Background(), source, nil), "spec", cpRef, cp, root, target, "initialized")
		if status != "blocked" {
			t.Fatalf("%s Context drift qualified intake: %s", target, status)
		}
		targetReport["self_consistent_but_conflicting_context_input_status"] = status
		targetReport["context_conflict_diagnostic"] = missing
		apTestPut(t, root, "CONTEXT.md", current)
		apTestPut(t, root, reconRef, reconRaw)
	}
	currentGuidance := ProfileGuidance(context.Background(), source, cpRef, cp)
	report["registered_target_guidance"] = currentGuidance
	for _, recommendation := range currentGuidance.Recommendations {
		if recommendation.Profile == "design" {
			if recommendation.Recommendation != "not-applicable" {
				t.Fatalf("completed Spec Handoff recommended Design again: %+v", currentGuidance)
			}
			continue
		}
		if recommendation.InputStatus != "verified" {
			t.Fatalf("registered target intake did not qualify: %+v", recommendation)
		}
		command := strings.Join(recommendation.Commands[0], " ")
		if !strings.Contains(command, "--kind consumption") || recommendation.Profile == "frontend" && !strings.Contains(command, "--consumer frontend") {
			t.Fatalf("wrong strategic consumer command: %s", command)
		}
	}
	rawCheckpoint := semMap(mustParseContract(mustMarshalContract(cp)))
	delete(semMap(rawCheckpoint["artifacts"]), "artifact.strategic-handoff-delivery")
	rawCheckpoint["handoff_ref"] = "handoff.yaml"
	apTestPut(t, source, cpRef, rawCheckpoint)
	rawRoutes, _, err := guidanceSource(newSemanticSession(context.Background(), source, nil), "spec", cpRef, rawCheckpoint)
	if err != nil || rawRoutes["backend"] != "required" || rawRoutes["frontend"] != "required" {
		t.Fatalf("approved raw Spec Handoff did not retain both consumers: %v %v", rawRoutes, err)
	}
	if _, err := ProfilePreparationSource(context.Background(), source, cpRef, []string{"backend", "frontend"}); err != nil {
		t.Fatalf("approved raw Spec Handoff did not qualify joint source preparation: %v", err)
	}
	for target, root := range map[string]string{"backend": backend, "frontend": frontend} {
		status, missing, _ := guidanceTargetInput(context.Background(), newSemanticSession(context.Background(), source, nil), "spec", cpRef, rawCheckpoint, root, target, "initialized")
		if status != "verified" || missing != "" {
			t.Fatalf("raw Spec Handoff did not bind the real %s receipt: %s %s", target, status, missing)
		}
	}
	report["approved_raw_handoff_routes"] = rawRoutes
	apTestPut(t, source, cpRef, cp)
	for _, variant := range []string{"bad-explicit-delivery", "stale-declared-delivery", "stale-handoff", "stale-source-spec"} {
		t.Run(variant, func(t *testing.T) {
			candidate := semMap(mustParseContract(mustMarshalContract(cp)))
			if variant == "bad-explicit-delivery" {
				semMap(semMap(candidate["artifacts"])["artifact.strategic-handoff-delivery"])["ref"] = "missing/delivery-record.json"
			} else if variant == "stale-declared-delivery" {
				semMap(semMap(candidate["artifacts"])["artifact.strategic-handoff-delivery"])["status"] = "stale"
			} else {
				delete(semMap(candidate["artifacts"]), "artifact.strategic-handoff-delivery")
				candidate["handoff_ref"] = "handoff.yaml"
				if variant == "stale-source-spec" {
					original := mustReadSpecBaselineTestFile(t, filepath.Join(source, "source/spec.md"))
					apTestPut(t, source, "source/spec.md", append(append([]byte{}, original...), []byte("\n未批准的当前 Spec 修改\n")...))
					defer apTestPut(t, source, "source/spec.md", original)
				} else {
					handoff, err := newSemanticSession(context.Background(), source, nil).doc("handoff.yaml")
					if err != nil {
						t.Fatal(err)
					}
					handoff["status"] = "draft"
					original := mustReadSpecBaselineTestFile(t, filepath.Join(source, "handoff.yaml"))
					apTestPut(t, source, "handoff.yaml", handoff)
					defer apTestPut(t, source, "handoff.yaml", original)
				}
			}
			apTestPut(t, source, cpRef, candidate)
			defer apTestPut(t, source, cpRef, cp)
			_, _, sourceErr := guidanceSource(newSemanticSession(context.Background(), source, nil), "spec", cpRef, candidate)
			if sourceErr == nil {
				t.Fatal("invalid explicit Spec Handoff fell back to another source")
			}
			_, prepareErr := ProfilePreparationSource(context.Background(), source, cpRef, []string{"backend", "frontend"})
			if prepareErr == nil {
				t.Fatal("joint preparation ignored an invalid explicit Spec Handoff")
			}
			if semanticCode(sourceErr) == "WAITING_PROFILE_INPUT" || semanticCode(prepareErr) == "WAITING_PROFILE_INPUT" {
				t.Fatal("explicit invalid Handoff fell back to the unapproved Spec baseline")
			}
			semMap(report["negative_source_checks"])[variant] = map[string]any{"source_rejected": true, "source_code": semanticCode(sourceErr), "source_error": sourceErr.Error(), "joint_preparation_rejected": true, "preparation_code": semanticCode(prepareErr)}
		})
	}
	if reportPath := os.Getenv("YSS_PROFILE_GUIDANCE_REPORT"); reportPath != "" {
		report["status"] = "passed"
		bytes, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(reportPath, append(bytes, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
