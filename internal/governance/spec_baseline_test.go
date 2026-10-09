package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

func mustReadSpecBaselineTestFile(t *testing.T, path string) []byte {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestSpecBaselineImportWriteScope(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"CONTEXT.md", ".yss.json", "docs/spec-baselines/../../CONTEXT.md", "docs/spec-baselines/spec-baseline.f/v1/package/../../../../CONTEXT.md"} {
		if err := ValidateSpecBaselineImportTransaction(root, "design", []string{ref}); err == nil {
			t.Fatalf("unexpected write permission: %s", ref)
		}
	}
	if err := ValidateSpecBaselineImportTransaction(root, "design", []string{"docs/spec-baselines/spec-baseline.f/v1/package/payload/files/spec.md", "docs/spec-baselines/spec-baseline.f/v1/receipt.json"}); err != nil {
		t.Fatal(err)
	}
}

func TestSpecBaselineVerifySelectorsAreExclusive(t *testing.T) {
	_, err := specBaselineRun(context.Background(), "verify", t.TempDir(), map[string]string{"package": "package", "file": "receipt.json"})
	if err == nil {
		t.Fatal("ambiguous baseline selector accepted")
	}
}

func TestSpecBaselinePackageLimitMatchesDesignReader(t *testing.T) {
	s := newSemanticSession(context.Background(), t.TempDir(), nil)
	if err := specBaselineLimits(s, 20000, 100<<20); err != nil {
		t.Fatal(err)
	}
	for _, input := range [][2]int{{2, 120 << 20}, {1, (100 << 20) + 1}, {20001, 1}} {
		apTestCode(t, specBaselineLimits(s, input[0], input[1]), "SPEC_BASELINE_LIMIT")
	}
}

func TestSpecBaselineWorkingSetPreservesBusinessIdentity(t *testing.T) {
	root, setRef, ticket, set, save := contractTestBusiness(t)
	ticket["status"] = "draft"
	set["status"] = "draft"
	save()
	ticketRef := text(semMap(semList(set["tickets"])[0])["ref"])
	rawTicket, err := os.ReadFile(filepath.Join(root, ticketRef))
	if err != nil {
		t.Fatal(err)
	}
	apTestPut(t, root, ticketRef, append(rawTicket, []byte("\n[批准 Spec](../spec.md#FR-001) 与 [资料](https://example.invalid/spec?q=1)。\n")...))
	manifest := map[string]any{"baseline_id": "spec-baseline.demo", "version": "v1", "source": map[string]any{"business_ticket_set_ref": setRef}, "files": []any{map[string]any{"original_ref": setRef}, map[string]any{"original_ref": ticketRef}, map[string]any{"original_ref": text(semMap(set["spec"])["ref"])}}}
	for _, v := range semList(manifest["files"]) {
		ref := text(semMap(v)["original_ref"])
		raw, err := os.ReadFile(filepath.Join(root, ref))
		if err != nil {
			t.Fatal(err)
		}
		apTestPut(t, root, baselineFileRef(ref), raw)
	}
	s := newSemanticSession(context.Background(), root, nil)
	working, ops, err := specBaselineWorkingSet(s, manifest, "docs/spec-baselines/spec-baseline.demo/v1")
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		apTestPut(t, root, op.Path, op.Data)
	}
	got, err := s.doc(text(working["business_ticket_set_ref"]))
	if err != nil {
		t.Fatal(err)
	}
	if got["id"] != set["id"] || got["version"] != set["version"] {
		t.Fatal("business identity changed")
	}
	row := semMap(semList(got["tickets"])[0])
	raw, err := os.ReadFile(filepath.Join(root, text(row["ref"])))
	if err != nil {
		t.Fatal(err)
	}
	if row["digest"] != "sha256:"+safefs.Digest(raw) {
		t.Fatal("editable ticket bytes are not bound")
	}
	imported, err := s.doc(text(row["ref"]))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema_version", "id", "version", "requirement_refs", "acceptance_refs", "dependencies"} {
		if !contractSame(imported[key], ticket[key]) {
			t.Fatalf("lost stable %s", key)
		}
	}
	if semMap(imported["spec"])["ref"] == semMap(ticket["spec"])["ref"] {
		t.Fatal("source-relative spec reference was not rebound")
	}
	link := regexp.MustCompile(`\[批准 Spec\]\(([^)]+)\)`).FindSubmatch(raw)
	if len(link) != 2 {
		t.Fatal("lost business prose link")
	}
	resolved, err := contractLocalDependency(text(row["ref"]), string(link[1]))
	if err != nil || resolved != semMap(imported["spec"])["ref"] || !bytes.HasSuffix(link[1], []byte("#FR-001")) {
		t.Fatalf("business body link did not follow the frozen Spec mapping: %s %v", resolved, err)
	}
	if !bytes.Contains(raw, []byte("[资料](https://example.invalid/spec?q=1)")) {
		t.Fatal("external business documentation link changed")
	}
}

func TestSpecBaselineContextRequiresRealSourceTerms(t *testing.T) {
	root := apTestRoot(t)
	apTestPut(t, root, "CONTEXT.md", "---\ncontext_schema_version: 1\n---\n## 流程术语\n| 术语 | 含义 | 英文标识 | 避免 / 备注 |\n|---|---|---|---|\n| Spec | 规格 | — | |\n## 业务术语\n| 术语 | 含义 | 英文标识 | 适用业务责任区 | 避免 / 备注 |\n|---|---|---|---|---|\n| 报告 | 提交的报告 | Report | Reporting | 避免：表单 |\n")
	packageRef := "docs/spec-baselines/spec-baseline.demo/v1/package"
	sourceContext, err := os.ReadFile(filepath.Join(root, "CONTEXT.md"))
	if err != nil {
		t.Fatal(err)
	}
	apTestPut(t, root, packageRef+"/payload/files/source-context.snapshot.md", sourceContext)
	manifest := map[string]any{"source": map[string]any{"spec_ref": "spec.yaml"}}
	apTestPut(t, root, packageRef+"/manifest.json", manifest)
	contract, err := contextContractBytes(sourceContext)
	if err != nil {
		t.Fatal(err)
	}
	terms := contract["business_terms"].([]Term)
	if len(terms) == 0 {
		t.Fatal("fixture needs a business term")
	}
	termRef := terms[0].TermRef
	apTestPut(t, root, packageRef+"/"+baselineFileRef("spec.yaml"), map[string]any{"context_snapshot": map[string]any{"term_refs": []any{termRef}}})
	for _, kind := range []string{"matching", "empty-scope", "meaning-conflict"} {
		t.Run(kind, func(t *testing.T) {
			apTestPut(t, root, "CONTEXT.md", sourceContext)
			refs := []any{termRef}
			if kind == "empty-scope" {
				refs = []any{}
			}
			if kind == "meaning-conflict" {
				apTestPut(t, root, "CONTEXT.md", strings.Replace(string(sourceContext), terms[0].Meaning, "冲突业务含义", 1))
			}
			receiptRef := "docs/spec-baselines/spec-baseline.demo/v1/receipt.json"
			recon := map[string]any{"evidence_refs": []any{receiptRef, packageRef + "/payload/files/source-context.snapshot.md"}, "context_snapshot": map[string]any{"term_refs": refs}}
			apTestPut(t, root, "reconciliation.json", recon)
			err := specBaselineContextEvidence(newSemanticSession(context.Background(), root, nil), "reconciliation.json", receiptRef, packageRef)
			if (err == nil) != (kind == "matching") {
				t.Fatalf("%s: %v", kind, err)
			}
		})
	}
}

// The producer contains synthetic independent reviewers and user replies only.
// Native readers and transactions consume its current, scoped approval records.
func TestSpecBaselineNativePlanApplyRecovery(t *testing.T) {
	if os.Getenv("YSS_SPEC_BASELINE_CRASH_ROOT") != "" {
		t.Skip("parent-only integration")
	}
	oracle := governanceOracleRoot(t)
	fixture := filepath.Join(oracle, "scripts/fixtures/spec-baseline/fixture.mjs")
	if _, err := os.Stat(fixture); err != nil {
		t.Skip("fixed historical template has no Spec-baseline fixture")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot, targetRoot := filepath.Join(base, "spec"), filepath.Join(base, "design")
	program := `import fs from 'node:fs';import path from 'node:path';
import {approvedSpecFixture,designFixture} from '` + governanceOracleURL(fixture) + `';
import {read,hash} from '` + governanceOracleURL(filepath.Join(oracle, "scripts/lib/strategic-handoff-io.mjs")) + `';
const source=await approvedSpecFixture(process.argv[1],{nativeSeed:process.argv[3]});
const setRef=source.state.artifacts['artifact.business-ticket-set'].ref,set=read(path.join(source.root,setRef));
for(const row of set.tickets){fs.appendFileSync(path.join(source.root,row.ref),'\n[批准 Spec](../spec.md#FR-001)\n');row.digest=hash(fs.readFileSync(path.join(source.root,row.ref)));}
fs.writeFileSync(path.join(source.root,setRef),JSON.stringify(set));const review=read(path.join(source.root,set.review_ref));review.subject_digest=hash(fs.readFileSync(path.join(source.root,setRef)));fs.writeFileSync(path.join(source.root,set.review_ref),JSON.stringify(review));
await designFixture(process.argv[2],{nativeSeed:process.argv[4]});`
	specSeed, designSeed := specBaselineTestNativeSeed(t, "spec"), specBaselineTestNativeSeed(t, "design")
	cmd := exec.Command("node", "--input-type=module", "-e", program, sourceRoot, targetRoot, specSeed, designSeed)
	cmd.Dir = oracle
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthetic current approval fixture: %v %s", err, out)
	}
	checkpointRef := "docs/.scratch/feature.supplier/checkpoint.json"
	checkpointRaw, err := os.ReadFile(filepath.Join(sourceRoot, checkpointRef))
	if err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"draft-spec", "missing-current-spec", "template-source-checkpoint", "pending-context", "inherited-source", "conflicting-spec-ref", "unbound-current-spec", "unapproved-current-plan", "unapproved-stage", "asserted-unapproved-stage", "wrong-kind-stage", "wrong-kind-strategy", "missing-current-strategy", "progression-plan-primary-alias", "progression-plan-extra-evidence"} {
		t.Run("source-current-"+variant, func(t *testing.T) {
			candidate := semMap(mustParseContract(checkpointRaw))
			switch variant {
			case "draft-spec":
				semMap(semMap(candidate["artifacts"])["artifact.spec"])["status"] = "draft"
			case "missing-current-spec":
				delete(semMap(candidate["artifacts"]), "artifact.spec")
			case "template-source-checkpoint":
				candidate["repository_mode"] = "template-source"
			case "pending-context":
				semMap(candidate["context_reconciliation"])["status"] = "pending"
			case "inherited-source":
				candidate["upstream_spec_baseline"] = map[string]any{"receipt_ref": "missing-receipt.json", "receipt_digest": "sha256:" + strings.Repeat("0", 64)}
			case "conflicting-spec-ref":
				candidate["spec_ref"] = "other-spec.md"
			case "unbound-current-spec":
				gate := semMap(semMap(candidate["gates"])["gate.spec-baseline-approved"])
				basis := []any{}
				for _, row := range semList(gate["basis"]) {
					if semMap(row)["ref"] != semMap(semMap(candidate["artifacts"])["artifact.spec"])["ref"] {
						basis = append(basis, row)
					}
				}
				gate["basis"] = basis
			case "unapproved-current-plan":
				semMap(semMap(candidate["artifacts"])["artifact.plan"])["ref"] = "source/unapproved-plan.md"
				apTestPut(t, sourceRoot, "source/unapproved-plan.md", "未批准的当前 Plan 语义变化。\n")
			case "wrong-kind-stage":
				semMap(semMap(candidate["artifacts"])["artifact.stage-decision-package"])["ref"] = "source/strategy.yaml"
			case "wrong-kind-strategy":
				semMap(semMap(candidate["artifacts"])["artifact.domain-strategy"])["ref"] = "source/stage.yaml"
			case "missing-current-strategy":
				delete(semMap(candidate["artifacts"]), "artifact.domain-strategy")
			case "progression-plan-primary-alias", "progression-plan-extra-evidence":
				approvalRef := first(text(candidate["plan_approval_ref"]), text(semMap(semMap(candidate["gates"])["gate.plan-approved"])["approval_ref"]))
				original := mustReadSpecBaselineTestFile(t, filepath.Join(sourceRoot, approvalRef))
				intentRef := "source/nested/Progression-Target.JSON"
				if variant == "progression-plan-primary-alias" {
					apTestPut(t, sourceRoot, intentRef, original)
					candidate["plan_approval_ref"] = intentRef
				} else {
					apTestPut(t, sourceRoot, intentRef, "synthetic intent")
					record := semMap(mustParseContract(original))
					record["evidence_refs"] = append(semList(record["evidence_refs"]), intentRef)
					apTestPut(t, sourceRoot, approvalRef, record)
					defer apTestPut(t, sourceRoot, approvalRef, original)
				}
				defer os.Remove(filepath.Join(sourceRoot, intentRef))
			case "unapproved-stage", "asserted-unapproved-stage":
				stage := semMap(mustParseContract(mustReadSpecBaselineTestFile(t, filepath.Join(sourceRoot, "source/stage.yaml"))))
				semMap(stage["impact_assessment"])["ui"] = false
				semMap(stage["impact_assessment"])["frontend"] = false
				newStageRef := "source/unapproved-stage.yaml"
				apTestPut(t, sourceRoot, newStageRef, stage)
				semMap(semMap(candidate["artifacts"])["artifact.stage-decision-package"])["ref"] = newStageRef
				if variant == "asserted-unapproved-stage" {
					gate := semMap(semMap(candidate["gates"])["gate.plan-approved"])
					gate["basis"] = append(semList(gate["basis"]), map[string]any{"ref": newStageRef, "digest": safefs.Digest(mustReadSpecBaselineTestFile(t, filepath.Join(sourceRoot, newStageRef)))})
				}
			}
			apTestPut(t, sourceRoot, checkpointRef, candidate)
			defer apTestPut(t, sourceRoot, checkpointRef, checkpointRaw)
			rejectedOutput := filepath.Join(base, "rejected-"+variant)
			_, err := specBaselineRun(context.Background(), "export", sourceRoot, map[string]string{"checkpoint": checkpointRef, "out": rejectedOutput})
			want := "SPEC_BASELINE_CURRENT"
			if variant == "conflicting-spec-ref" {
				// The current schema forbids this obsolete top-level selector.
				want = "SCHEMA"
			}
			if variant == "unapproved-stage" || variant == "asserted-unapproved-stage" {
				want = "SPEC_BASELINE_SCOPE"
			}
			if variant == "wrong-kind-stage" || variant == "wrong-kind-strategy" {
				want = "SCHEMA"
			}
			if variant == "missing-current-strategy" {
				want = "SPEC_BASELINE_SOURCE"
			}
			if variant == "progression-plan-primary-alias" || variant == "progression-plan-extra-evidence" {
				want = "PROGRESSION_EVIDENCE"
			}
			apTestCode(t, err, want)
			if _, err := os.Stat(rejectedOutput); !os.IsNotExist(err) {
				t.Fatal("invalid source created a baseline output directory")
			}
		})
	}
	contextBefore, err := os.ReadFile(filepath.Join(targetRoot, "CONTEXT.md"))
	if err != nil {
		t.Fatal(err)
	}
	packageRoot := filepath.Join(sourceRoot, "native-baseline")
	if _, err := specBaselineRun(context.Background(), "export", sourceRoot, map[string]string{"checkpoint": "docs/.scratch/feature.supplier/checkpoint.json", "out": packageRoot}); err != nil {
		t.Fatal(err)
	}
	planFile := filepath.Join(base, "import-plan.json")
	if _, err := specBaselineRun(context.Background(), "import", targetRoot, map[string]string{"package": packageRoot, "plan": "true", "out": planFile}); err != nil {
		t.Fatal(err)
	}
	planBytes, err := os.ReadFile(planFile)
	if err != nil {
		t.Fatal(err)
	}
	var plan specBaselineImportPlan
	if err = json.Unmarshal(planBytes, &plan); err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := os.ReadFile(filepath.Join(packageRoot, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(filepath.Join(packageRoot, "manifest.json"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(packageRoot, "manifest.json"), append(append([]byte{}, manifestRaw...), '\n'), 0444); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(filepath.Join(packageRoot, "manifest.json"), 0444); err != nil {
		t.Fatal(err)
	}
	if _, err := specBaselineRun(context.Background(), "import", targetRoot, map[string]string{"apply": "true", "plan-file": planFile}); err == nil {
		t.Fatal("saved plan ignored package raw-byte drift")
	}
	if _, err = os.Stat(filepath.Join(targetRoot, "docs/spec-baselines")); !os.IsNotExist(err) {
		t.Fatalf("drift wrote target: %v", err)
	}
	if err = os.Chmod(filepath.Join(packageRoot, "manifest.json"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(packageRoot, "manifest.json"), manifestRaw, 0444); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(filepath.Join(packageRoot, "manifest.json"), 0444); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(base, "before-commit.marker")
	crash := exec.Command(os.Args[0], "-test.run=^TestSpecBaselineImportCrashHelper$")
	crash.Env = append(os.Environ(), "YSS_SPEC_BASELINE_CRASH_ROOT="+targetRoot, "YSS_SPEC_BASELINE_CRASH_PLAN="+planFile, "YSS_SPEC_BASELINE_CRASH_MARKER="+marker)
	if err = crash.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = crash.Process.Kill() })
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err = os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("import transaction did not reach the precommit interruption")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = crash.Process.Kill()
	_ = crash.Wait()
	status, err := transaction.Status(targetRoot)
	if err != nil || len(status.Pending) != 1 {
		t.Fatalf("import interruption was not retained: %+v %v", status, err)
	}
	validate := func(summary transaction.Summary, paths []string) error {
		if summary.Kind != "spec-baseline-import" {
			return fmt.Errorf("wrong family: %s", summary.Kind)
		}
		return ValidateSpecBaselineImportTransaction(targetRoot, "design", paths)
	}
	result, err := transaction.RecoverKindContextWithValidator(context.Background(), targetRoot, "spec-baseline-import", validate)
	if err != nil || result.Status != "recovered" {
		t.Fatalf("import recovery: %+v %v", result, err)
	}
	for _, op := range plan.Operations {
		if _, err = os.Stat(filepath.Join(targetRoot, op.Path)); !os.IsNotExist(err) {
			t.Fatalf("recovery retained imported file %s: %v", op.Path, err)
		}
	}
	if _, err := specBaselineRun(context.Background(), "import", targetRoot, map[string]string{"apply": "true", "plan-file": planFile}); err != nil {
		t.Fatal(err)
	}
	receiptRef := path.Join("docs/spec-baselines", plan.BaselineID, plan.Version, "receipt.json")
	if _, err := specBaselineRun(context.Background(), "verify", targetRoot, map[string]string{"file": receiptRef}); err != nil {
		t.Fatal(err)
	}
	// A captured checkpoint remains immutable; later legal source progress is
	// current when the same original approvals and stable assets still qualify.
	for _, variant := range []string{"legal-progress", "spec-byte-drift", "approval-byte-drift", "context-byte-drift"} {
		t.Run("receipt-current-"+variant, func(t *testing.T) {
			currentCP := semMap(mustParseContract(checkpointRaw))
			changedRef := checkpointRef
			before := checkpointRaw
			if variant == "legal-progress" {
				currentCP["stage"] = "stage.product-design"
				currentCP["next_work_unit"] = "work-unit.prototype-design-v2"
				apTestPut(t, sourceRoot, checkpointRef, currentCP)
			} else {
				if variant == "spec-byte-drift" {
					changedRef = text(semMap(semMap(currentCP["artifacts"])["artifact.spec"])["ref"])
				} else if variant == "approval-byte-drift" {
					changedRef = text(semMap(semMap(currentCP["gates"])["gate.spec-baseline-approved"])["approval_ref"])
				} else {
					changedRef = "CONTEXT.md"
				}
				before = mustReadSpecBaselineTestFile(t, filepath.Join(sourceRoot, changedRef))
				apTestPut(t, sourceRoot, changedRef, append(append([]byte{}, before...), '\n'))
			}
			defer apTestPut(t, sourceRoot, changedRef, before)
			receiver := newSemanticSession(context.Background(), targetRoot, nil)
			manifest, snapshot, err := verifySpecBaselineReceipt(receiver, receiptRef, false)
			if err != nil {
				t.Fatal(err)
			}
			source := newSemanticSession(context.Background(), sourceRoot, nil)
			err = guidanceCurrentSpecBaseline(source, checkpointRef, currentCP, manifest, snapshot)
			if (err == nil) != (variant == "legal-progress") {
				t.Fatalf("source currentness %s: %v", variant, err)
			}
		})
	}
	contextAfter, err := os.ReadFile(filepath.Join(targetRoot, "CONTEXT.md"))
	if err != nil || !bytes.Equal(contextBefore, contextAfter) {
		t.Fatal("baseline import replaced target Context")
	}
	// Changing this session's goal cannot turn an approved Spec milestone into
	// a completed Profile/business, or replace its genuine next work unit.
	originalEvidence := map[string]any{}
	for _, row := range semList(semMap(mustParseContract(manifestRaw))["files"]) {
		ref := text(semMap(row)["original_ref"])
		if ref == "" {
			continue
		}
		info, err := os.Lstat(filepath.Join(sourceRoot, ref))
		if err != nil {
			t.Fatal(err)
		}
		originalEvidence[ref] = map[string]any{"mode": info.Mode().String(), "digest": safefs.Digest(mustReadSpecBaselineTestFile(t, filepath.Join(sourceRoot, ref)))}
	}
	packageBefore, receiptBefore := progressionInventory(t, packageRoot), mustReadSpecBaselineTestFile(t, filepath.Join(targetRoot, receiptRef))
	preserved := func() {
		t.Helper()
		for ref, observation := range originalEvidence {
			info, err := os.Lstat(filepath.Join(sourceRoot, ref))
			if err != nil || !contractSame(observation, map[string]any{"mode": info.Mode().String(), "digest": safefs.Digest(mustReadSpecBaselineTestFile(t, filepath.Join(sourceRoot, ref)))}) {
				t.Fatalf("goal change altered original approved evidence: %s %v", ref, err)
			}
		}
		if !contractSame(packageBefore, progressionInventory(t, packageRoot)) || !bytes.Equal(receiptBefore, mustReadSpecBaselineTestFile(t, filepath.Join(targetRoot, receiptRef))) {
			t.Fatal("goal change altered frozen package or Receipt bytes/modes")
		}
	}
	apTestPut(t, sourceRoot, "docs/.scratch/feature.supplier/map.md", "---\ncheckpoint_ref: "+checkpointRef+"\n---\n# Synthetic registered feature\n")
	apTestPut(t, sourceRoot, "target-input.json", ProgressionTarget{SchemaVersion: 1, Kind: "lifecycle-progression-target", FeatureID: "feature.supplier", CheckpointRef: checkpointRef, Target: "spec-approved", IntentSource: "Synthetic session milestone", Consumers: []ProgressionConsumer{}})
	targetPlan := filepath.Join(base, "short-target-plan.json")
	if _, err := progressionTargetRun(context.Background(), sourceRoot, map[string]string{"checkpoint": checkpointRef, "input": "target-input.json", "plan": "true", "out": targetPlan}); err != nil {
		t.Fatal(err)
	}
	if _, err := progressionTargetRun(context.Background(), sourceRoot, map[string]string{"apply": "true", "plan-file": targetPlan}); err != nil {
		t.Fatal(err)
	}
	short, err := progressionRead(context.Background(), sourceRoot, checkpointRef)
	if err != nil {
		t.Fatal(err)
	}
	p := semMap(short["progression"])
	completion := semMap(p["completion"])
	if p["status"] != "reached" || p["reached"] != true || semMap(completion["profile"])["status"] == "reached" || semMap(completion["business"])["status"] == "reached" || semMap(short["next_action"])["root_work_unit"] != semMap(mustParseContract(checkpointRaw))["next_work_unit"] {
		t.Fatalf("short goal changed Profile/business qualification or true continuation: %#v", short)
	}
	if !bytes.Equal(checkpointRaw, mustReadSpecBaselineTestFile(t, filepath.Join(sourceRoot, checkpointRef))) {
		t.Fatal("short goal altered source approval checkpoint")
	}
	preserved()
	for _, executionState := range []string{"Drafter", "Worker"} {
		task := map[string]any{"execution_state": executionState, "workflow_status": "active", "work_unit_id": "work-unit.technical-analysis", "allowed_write_paths": []any{"design.md"}, "checkpoint_ref": checkpointRef, "contract": map[string]any{"kind": "lifecycle-work-unit"}}
		if err := progressionTaskEntry(newSemanticSession(context.Background(), sourceRoot, nil), task); semanticCode(err) != "PROGRESSION_TARGET_BLOCKED" {
			t.Fatalf("reached approved Spec admitted %s task: %v", executionState, err)
		}
	}
	// Continuing is an intent change only; the same current approvals then
	// qualify the remaining writer under the default whole-business goal.
	apTestPut(t, sourceRoot, "target-input.json", ProgressionTarget{SchemaVersion: 1, Kind: "lifecycle-progression-target", FeatureID: "feature.supplier", CheckpointRef: checkpointRef, Target: "business-accepted", IntentSource: "Synthetic authorized continuation", Consumers: []ProgressionConsumer{}})
	continuePlan := filepath.Join(base, "continue-target-plan.json")
	if _, err := progressionTargetRun(context.Background(), sourceRoot, map[string]string{"checkpoint": checkpointRef, "input": "target-input.json", "plan": "true", "out": continuePlan}); err != nil {
		t.Fatal(err)
	}
	if _, err := progressionTargetRun(context.Background(), sourceRoot, map[string]string{"apply": "true", "plan-file": continuePlan}); err != nil {
		t.Fatal(err)
	}
	continuation := newSemanticSession(context.Background(), sourceRoot, nil)
	if err := progressionTaskEntry(continuation, map[string]any{"execution_state": "Drafter", "workflow_status": "active", "work_unit_id": "work-unit.technical-analysis", "allowed_write_paths": []any{"design.md"}, "checkpoint_ref": checkpointRef, "contract": map[string]any{"kind": "lifecycle-work-unit"}}); err != nil {
		t.Fatalf("current approval continuation rejected: %v", err)
	}
	if err := continuation.finish(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(checkpointRaw, mustReadSpecBaselineTestFile(t, filepath.Join(sourceRoot, checkpointRef))) {
		t.Fatal("continuation altered source approval checkpoint")
	}
	if _, err := specBaselineRun(context.Background(), "verify", targetRoot, map[string]string{"file": receiptRef}); err != nil {
		t.Fatalf("goal change invalidated existing Receipt: %v", err)
	}
	preserved()
	reused, err := buildSpecBaselineImport(context.Background(), targetRoot, packageRoot)
	if err != nil || !reused.Reused || len(reused.Operations) != 0 {
		t.Fatalf("idempotent import: %+v %v", reused, err)
	}
	workingRef := path.Join("docs/spec-baselines", plan.BaselineID, plan.Version, "working-set.json")
	workingRaw, err := os.ReadFile(filepath.Join(targetRoot, workingRef))
	if err != nil {
		t.Fatal(err)
	}
	var working map[string]any
	if err = json.Unmarshal(workingRaw, &working); err != nil {
		t.Fatal(err)
	}
	importedTicketRef := text(semMap(working["assets"])["source/business-tickets/BT-001.md"])
	closureSession := newSemanticSession(context.Background(), targetRoot, nil)
	if _, err := contractHandoffClosure(closureSession, importedTicketRef, &nativeHandoff{Handoff: map[string]any{"source": map[string]any{}}, Config: map[string]any{"approvals": map[string]any{}, "additional_files": []any{}}}); err != nil {
		t.Fatalf("native imported Ticket Markdown link cannot form a final source closure: %v", err)
	}
	if err := closureSession.finish(); err != nil {
		t.Fatal(err)
	}
	semMap(working["assets"])[text(semMap(semMap(mustParseContract(manifestRaw))["source"])["spec_ref"])] = "outside/spec.md"
	apTestPut(t, targetRoot, workingRef, working)
	if _, err := specBaselineRun(context.Background(), "verify", targetRoot, map[string]string{"file": receiptRef}); err == nil {
		t.Fatal("receipt accepted tampered working-set")
	}
	apTestPut(t, targetRoot, workingRef, workingRaw)
	identityRaw, err := os.ReadFile(filepath.Join(sourceRoot, ".yss.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if err = json.Unmarshal(identityRaw, &metadata); err != nil {
		t.Fatal(err)
	}
	metadata["cliVersion"] = "1.0.1"
	apTestPut(t, sourceRoot, ".yss.json", metadata)
	changedPackage := filepath.Join(base, "different-current-package")
	if _, err := specBaselineRun(context.Background(), "export", sourceRoot, map[string]string{"checkpoint": "docs/.scratch/feature.supplier/checkpoint.json", "out": changedPackage}); err != nil {
		t.Fatal(err)
	}
	if _, err := buildSpecBaselineImport(context.Background(), targetRoot, changedPackage); err == nil {
		t.Fatal("same identity/version with different valid package contents was accepted")
	}
	apTestPut(t, sourceRoot, ".yss.json", identityRaw)
	result, err = transaction.RollbackKindContextWithValidator(context.Background(), targetRoot, "spec-baseline-import", validate)
	if err != nil || result.Status != "rolled-back" {
		t.Fatalf("import rollback: %+v %v", result, err)
	}
	if _, err = os.Stat(filepath.Join(targetRoot, receiptRef)); !os.IsNotExist(err) {
		t.Fatal("rollback retained baseline receipt")
	}
}

func TestSpecBaselineImportCrashHelper(t *testing.T) {
	root := os.Getenv("YSS_SPEC_BASELINE_CRASH_ROOT")
	if root == "" {
		t.Skip("subprocess crash helper")
	}
	raw, err := os.ReadFile(os.Getenv("YSS_SPEC_BASELINE_CRASH_PLAN"))
	if err != nil {
		t.Fatal(err)
	}
	var plan specBaselineImportPlan
	if err = json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Root != root || plan.Digest != baselinePlanDigest(plan) {
		t.Fatal("wrong saved import plan")
	}
	_, err = transaction.ApplyContextWithValidation(context.Background(), root, "spec-baseline-import", plan.Operations, plan.Guards, nil, func() error {
		if err := os.WriteFile(os.Getenv("YSS_SPEC_BASELINE_CRASH_MARKER"), []byte("before-commit"), 0600); err != nil {
			return err
		}
		select {}
	})
	t.Fatal("parent should kill this pending transaction", err)
}

func specBaselineTestNativeSeed(t *testing.T, profile string) string {
	t.Helper()
	root := apTestProfileRoot(t, profile)
	b, e := bundle.Load(profile)
	if e != nil {
		t.Fatal(e)
	}
	// Install the fixed Bundle's initial policy and Skills before synthetic signing.
	vars := map[string]string{"projectName": "synthetic-governance", "businessDomain": "test-only", "teamSize": "2"}
	for ref, f := range b.Initial {
		if !strings.HasPrefix(ref, ".agents/skills/") && !strings.HasPrefix(ref, ".template-spec/process/") && !strings.HasPrefix(ref, ".template-spec/agents/") {
			continue
		}
		raw, err := f.Render(vars)
		if err != nil {
			t.Fatal(err)
		}
		apTestPut(t, root, ref, raw)
		if err := os.Chmod(filepath.Join(root, filepath.FromSlash(ref)), os.FileMode(f.Mode)); err != nil {
			t.Fatal(err)
		}
	}
	p := domain.Profiles[profile]
	apTestPut(t, root, ".yss.json", identitymeta.Metadata{SchemaVersion: 1, Profile: profile, ProfileID: p.ID, ProtocolVersion: domain.ProtocolVersion, CLIVersion: domain.Version, TemplateVersion: p.LegacyVersion, LegacyCLIVersion: p.LegacyVersion, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash, TemplateSourceState: b.SourceState, Managed: map[string]identitymeta.Managed{}, BaselineDigest: safefs.Digest([]byte("{}")), Variables: map[string]string{}, Distribution: map[string]any{}})
	s := newSemanticSession(context.Background(), root, nil)
	authority, e := s.doc(".template-spec/process/harness-profile.yaml")
	if e != nil {
		t.Fatal(e)
	}
	inst := semMap(authority["instantiation"])
	if inst == nil {
		inst = map[string]any{}
		authority["instantiation"] = inst
	}
	inst["cli_package"] = "yss"
	inst["metadata_file"] = ".yss.json"
	inst["native_profile"] = profile
	apTestPut(t, root, ".template-spec/process/harness-profile.yaml", authority)
	return root
}

func TestSpecBaselineNativeSeedInitialPolicyClosure(t *testing.T) {
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		t.Run(profile, func(t *testing.T) {
			root := specBaselineTestNativeSeed(t, profile)
			b, err := bundle.Load(profile)
			if err != nil {
				t.Fatal(err)
			}
			vars := map[string]string{"projectName": "synthetic-governance", "businessDomain": "test-only", "teamSize": "2"}
			refs := []string{}
			for ref := range b.Initial {
				if strings.HasPrefix(ref, ".agents/skills/") || strings.HasPrefix(ref, ".template-spec/process/") || strings.HasPrefix(ref, ".template-spec/agents/") {
					refs = append(refs, ref)
				}
			}
			sort.Strings(refs)
			skills := 0
			for _, ref := range refs {
				if ref == ".template-spec/process/harness-profile.yaml" {
					continue // Its existing native identity transform is checked below.
				}
				f := b.Initial[ref]
				want, err := f.Render(vars)
				if err != nil {
					t.Fatal(err)
				}
				file := filepath.Join(root, filepath.FromSlash(ref))
				info, err := os.Lstat(file)
				if err != nil {
					t.Fatalf("missing Bundle initial fixture asset %s: %v", ref, err)
				}
				if !info.Mode().IsRegular() || domain.FileMode(uint32(info.Mode().Perm())) != domain.FileMode(f.Mode) || !bytes.Equal(mustReadSpecBaselineTestFile(t, file), want) {
					t.Fatalf("Bundle initial fixture bytes/mode drifted: %s", ref)
				}
				if strings.HasPrefix(ref, ".agents/skills/") {
					skills++
				}
			}
			if skills == 0 {
				t.Fatal("native approval seed omitted installed Skill closure")
			}
			s := newSemanticSession(context.Background(), root, nil)
			if err := s.authorities(); err != nil {
				t.Fatalf("Bundle initial seed identity/policy: %v", err)
			}
			if s.report.Profile != profile {
				t.Fatalf("native seed Profile changed: %s", s.report.Profile)
			}
		})
	}
}

func specBaselineActualNativeSeed(t *testing.T, profile string, full ...bool) string {
	t.Helper()
	binary := os.Getenv("YSS_NATIVE_BINARY")
	if binary == "" {
		t.Skip("actual fixed native CLI binary not configured")
	}
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, profile)
	args := []string{"init", "--profile", profile, "--root", root, "--project-name", "synthetic-progression-" + profile, "--business-domain", "test-only", "--team-size", "2", "--json"}
	if len(full) != 0 && full[0] {
		args = append(args, "--full")
	}
	cmd := exec.Command(binary, args...)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("actual native %s init: %v %s", profile, err, raw)
	}
	return root
}

// These current synthetic approvals exercise the native evaluator against the
// published target policy. They are protocol evidence, never product approvals.
func TestLifecycleTargetNativeProductDesignApplicability(t *testing.T) {
	oracle := governanceOracleRoot(t)
	seed := specBaselineActualNativeSeed(t, "spec")
	seedBefore := progressionInventory(t, seed)
	for _, productDesign := range []bool{false, true} {
		t.Run(fmt.Sprintf("product-design-%t", productDesign), func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(base, "spec")
			program := `import fs from 'node:fs';import path from 'node:path';
import {approvedSpecFixture} from '` + governanceOracleURL(filepath.Join(oracle, "scripts/fixtures/spec-baseline/fixture.mjs")) + `';
import {hash} from '` + governanceOracleURL(filepath.Join(oracle, "scripts/lib/strategic-handoff-io.mjs")) + `';
const productDesign=process.argv[3]==='true';const f=await approvedSpecFixture(process.argv[1],{nativeSeed:process.argv[2],productDesign});
fs.copyFileSync(path.join(process.argv[2],'.template-spec/agents/digital-human-roles.yaml'),path.join(f.root,'.template-spec/agents/digital-human-roles.yaml'));
f.state.stage='stage.product-design';f.state.next_work_unit='work-unit.technical-analysis';
if(!productDesign){const ref=f.state.artifacts['artifact.stage-decision-package'].ref;f.state.gates['gate.product-design-approved']={status:'not-applicable',reason:'已批准当前影响评估不要求产品设计',evidence_refs:[ref],basis:[{ref,digest:hash(fs.readFileSync(path.join(f.root,ref)))}]};}
f.plan.write(f.checkpointRef,f.state);f.plan.write('docs/.scratch/feature.supplier/map.md','---\ncheckpoint_ref: '+f.checkpointRef+'\n---\n# Synthetic active feature\n');`
			cmd := exec.Command("node", "--input-type=module", "-e", program, root, seed, fmt.Sprint(productDesign))
			cmd.Dir = oracle
			if raw, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("current approved fixture: %v %s", err, raw)
			}
			for _, ref := range []string{".yss.json", ".template-spec/process/harness-profile.yaml", ".template-spec/process/lifecycle-registry.yaml", ".template-spec/agents/digital-human-roles.yaml", guidanceContractRef("spec")} {
				if !bytes.Equal(mustReadSpecBaselineTestFile(t, filepath.Join(seed, ref)), mustReadSpecBaselineTestFile(t, filepath.Join(root, ref))) {
					t.Fatalf("current applicability fixture changed actual native identity/policy: %s", ref)
				}
			}
			cpRef := "docs/.scratch/feature.supplier/checkpoint.json"
			cpBefore := mustReadSpecBaselineTestFile(t, filepath.Join(root, cpRef))
			apTestPut(t, root, "target-input.json", ProgressionTarget{SchemaVersion: 1, Kind: "lifecycle-progression-target", FeatureID: "feature.supplier", CheckpointRef: cpRef, Target: "product-design-completed", IntentSource: "Synthetic qualified design boundary", Consumers: []ProgressionConsumer{}})
			planFile := filepath.Join(base, "target-plan.json")
			if _, err := progressionTargetRun(context.Background(), root, map[string]string{"checkpoint": cpRef, "input": "target-input.json", "plan": "true", "out": planFile}); err != nil {
				t.Fatal(err)
			}
			if _, err := progressionTargetRun(context.Background(), root, map[string]string{"apply": "true", "plan-file": planFile}); err != nil {
				t.Fatal(err)
			}
			before := progressionInventory(t, root)
			result, err := progressionRead(context.Background(), root, cpRef)
			if err != nil {
				t.Fatal(err)
			}
			p := semMap(result["progression"])
			want := "not-applicable"
			if productDesign {
				want = "pending"
			}
			if p["status"] != want || p["reached"] != !productDesign || semMap(result["next_action"])["root_work_unit"] != "work-unit.technical-analysis" || !contractSame(before, progressionInventory(t, root)) || !bytes.Equal(cpBefore, mustReadSpecBaselineTestFile(t, filepath.Join(root, cpRef))) {
				t.Fatalf("current applicability/readonly/continuation qualification: %#v", result)
			}
			if !productDesign {
				if semMap(semMap(p["completion"])["stage"])["status"] != "not-applicable" {
					t.Fatal("stage and qualified milestone applicability disagree")
				}
				for _, state := range []string{"Drafter", "Worker"} {
					task := map[string]any{"execution_state": state, "workflow_status": "active", "work_unit_id": "work-unit.technical-analysis", "checkpoint_ref": cpRef, "allowed_write_paths": []any{"design.md"}, "contract": map[string]any{"kind": "lifecycle-work-unit"}}
					if err := progressionTaskEntry(newSemanticSession(context.Background(), root, nil), task); semanticCode(err) != "PROGRESSION_TARGET_BLOCKED" {
						t.Fatalf("qualified N/A admitted %s writer: %v", state, err)
					}
				}
				for _, variant := range []string{"missing-current-basis", "applicable-true", "missing-reason", "current-impact-drift"} {
					t.Run(variant, func(t *testing.T) {
						cp := semMap(mustParseContract(cpBefore))
						gate := semMap(semMap(cp["gates"])["gate.product-design-approved"])
						changedRef, original := cpRef, cpBefore
						if variant == "missing-current-basis" {
							gate["basis"] = []any{}
						} else if variant == "applicable-true" {
							gate["applicable"] = true
						} else if variant == "missing-reason" {
							gate["reason"] = ""
						} else {
							changedRef = checkpointAsset(cp, "stage_decision_package_ref", "artifact.stage-decision-package")
							original = mustReadSpecBaselineTestFile(t, filepath.Join(root, changedRef))
							stage := semMap(mustParseContract(original))
							semMap(stage["impact_assessment"])["ui"] = true
							apTestPut(t, root, changedRef, stage)
						}
						defer apTestPut(t, root, changedRef, original)
						if changedRef == cpRef {
							apTestPut(t, root, cpRef, cp)
						}
						result, err := progressionRead(context.Background(), root, cpRef)
						if err != nil {
							t.Fatal(err)
						}
						if p := semMap(result["progression"]); p["status"] != "blocked" || p["reached"] != false {
							t.Fatalf("unqualified design applicability accepted: %s %#v", variant, p)
						}
					})
				}
			}
		})
	}
	if !contractSame(seedBefore, progressionInventory(t, seed)) {
		t.Fatal("synthetic current fixtures changed actual native seed")
	}
}

func TestLifecycleTargetNativeApplicableProductDesignCompleted(t *testing.T) {
	oracle := governanceOracleRoot(t)
	specSeed, designSeed := specBaselineActualNativeSeed(t, "spec"), specBaselineActualNativeSeed(t, "design")
	seedBefore := map[string]map[string]string{specSeed: progressionInventory(t, specSeed), designSeed: progressionInventory(t, designSeed)}
	cmd := exec.Command("node", filepath.Join(oracle, "scripts/fixtures/spec-baseline/scenarios.mjs"))
	cmd.Dir = oracle
	cmd.Env = append(os.Environ(), "YSS_SPEC_BASELINE_NATIVE_SEED="+specSeed, "YSS_DESIGN_BASELINE_NATIVE_SEED="+designSeed, "YSS_SPEC_BASELINE_NATIVE_CLI="+os.Getenv("YSS_NATIVE_BINARY"))
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual applicable native Spec/Design intake: %v %s", err, raw)
	}
	var f struct {
		Root         string `json:"root"`
		Spec         string `json:"source_root"`
		Design       string `json:"design_root"`
		Checkpoint   string `json:"checkpoint_ref"`
		Baseline     string `json:"baseline_package"`
		NativeIntake bool   `json:"native_intake"`
	}
	if err = json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(f.Root) })
	if !f.NativeIntake {
		t.Fatal("actual applicable native Spec/Design test did not use the public native intake chain")
	}
	// These are synthetic original replies and browser records, produced and
	// verified by existing protocols against the actual installed policy.
	program := `import fs from 'node:fs';import path from 'node:path';
import {read,json,hash} from '` + governanceOracleURL(filepath.Join(oracle, "scripts/lib/strategic-handoff-io.mjs")) + `';
import {attachArtifactApproval} from '` + governanceOracleURL(filepath.Join(oracle, "scripts/fixtures/backend-delivery/approval-fixture.mjs")) + `';
import {buildDecisionFixture} from '` + governanceOracleURL(filepath.Join(oracle, "scripts/lib/testing/user-decision-fixture.mjs")) + `';
import {validatePrototypeEvidence,prototypeDecisionSnapshot} from '` + governanceOracleURL(filepath.Join(oracle, ".agents/skills/yss-prototype-stage/scripts/prototype-contract.mjs")) + `';
const [src,dst,cpRef,specSeed,designSeed]=process.argv.slice(1),feature='feature.supplier';
const put=(root,ref,value)=>{const file=path.join(root,ref);fs.mkdirSync(path.dirname(file),{recursive:true});fs.writeFileSync(file,typeof value==='string'?value:json(value));};
const bound=(root,ref)=>({ref,digest:hash(fs.readFileSync(path.join(root,ref))).slice(7)});
for(const [root,seed]of [[src,specSeed],[dst,designSeed]])fs.copyFileSync(path.join(seed,'.template-spec/agents/digital-human-roles.yaml'),path.join(root,'.template-spec/agents/digital-human-roles.yaml'));
const handoff=read(path.join(dst,'handoff.yaml')),prototype=handoff.source.prototype_ref,context=prototype.approval_context,productRef=handoff.package_export.approvals.prototype_ref.record_ref;
const ev={schema_version:4,feature:'supplier',prototype_ref:'preview/index.html',prototype_profile:'H2',profile_kind:'flow-review',
profile_decision:{decision_to_inform:'合成审批流程校准',risk_assumptions:['合成状态核验'],trigger_results:[{trigger:'flow-change',matched:true,evidence_ref:'evidence/offline.log'}],calculated_profile:'H2',override:{applied:false,direction:'none',reason:'not-applicable',evidence_ref:'not-applicable'}},
upstream_refs:{spec_ref:handoff.source.spec_ref.persisted_ref,interaction_spec_ref:'source/interaction.md',low_fidelity_ref:'source/low.md',state_matrix_ref:'source/states.md',prototype_review_ref:'source/prototype.md'},
source_visual:{kind:'design-system',ideation_status:'not-applicable',selected_ref:'DESIGN.md',reuse_reason:'合成复用规范校准'},
design_baseline:{canonical_design_ref:'DESIGN.md',canonical_design_digest:hash('synthetic-design'),project_design_ref:'.template-spec/design/design.md',project_token_refs:['.template-spec/design/tokens/theme.json'],project_token_baseline_digest:hash('synthetic-tokens'),project_override_reviewed:true},
visual_baseline:{manifest_ref:'source/visual/visual-baseline.yaml',baseline_id:'visual-baseline.supplier',version:'v1',digest:hash('synthetic-visual'),status:'approved',case_ids:['desktop','narrow']},
browser_delivery:{delivery_kind:'static-directory',entry_ref:'preview/index.html',rendered_nonblank:true,prototype_digest:hash(fs.readFileSync(path.join(dst,prototype.persisted_ref))),viewports:[{name:'desktop',size:'1440x900',result:'passed',case_ids:['desktop']},{name:'narrow',size:'390x844',result:'passed',case_ids:['narrow']}],console_result:'passed',console_ref:'evidence/offline.log',delivery_contract:'offline-html-v1',resource_manifest_ref:'preview/yss-prototype-adapter.json',offline_verification_ref:'evidence/offline.json',offline_verification_result:'passed'},
design_qa:{mode:'design-contract',report_ref:'docs/.scratch/supplier/verification/design-qa.md',result:'passed',axes:{visual:'passed',layout:'passed',interaction:'passed',content:'passed',accessibility:'passed',cross_platform:'passed'}},
profile_evidence:{flow_review:{implementation:{framework:'html-css-js',runtime_build_required:false},main_flow_result:'passed',exceptional_state_result:'passed',exceptional_state_ref:'evidence/offline.log',keyboard_result:'passed',focus_result:'passed',contrast_result:'passed',zoom_200_result:'passed',reduced_motion_result:'passed',scenario_replay_ref:'evidence/offline.log',scenario_reset_result:'passed',visual_regression:{applicable:false,result:'not-applicable',evidence_ref:'evidence/offline.log'},prototype_library_facts:{applicable:false,component_basis:'html-css-js'}}},
implementation_handoff:{prototype_code_reusable:false,production_component_assumptions:['合成原型只确认体验语义'],verification_targets:[{behavior:'核验生产组件状态和交互',target_stage:'frontend-implementation-verification'}]},
review:{result:'approved',review_ref:'source/prototype.md'},user_confirmation:{result:'approved',confirmation_ref:productRef,confirmed_decision:'接受合成当前流程',operable_scope:['synthetic-ui-scope'],simulations_or_gaps:[]},gaps:[],blockers:[]};
const decision=buildDecisionFixture(path.join(dst,'approvals/prototype-evidence-decision'),{boundary:'gate.product-design-approved',scope:ev.user_confirmation.operable_scope,subjectContent:JSON.stringify(prototypeDecisionSnapshot(ev))});
ev.user_confirmation.user_decision_ref=decision.ref;ev.user_confirmation.decision_subject_ref=decision.requirement.subject_ref;
const validated=validatePrototypeEvidence(ev,{projectRoot:dst});if(validated.errors.length)throw Error(validated.errors.join('; '));
put(dst,'current-prototype-evidence.json',ev);put(dst,'current-prototype-validation.json',{schema_version:1,kind:'deliverable-verification',result:'passed',errors:[],subject_ref:'current-prototype-evidence.json',subject_digest:hash(fs.readFileSync(path.join(dst,'current-prototype-evidence.json'))),validator:'validatePrototypeEvidence',synthetic_fixture:true});
const reviewed=attachArtifactApproval(dst,'current-prototype-evidence.json','synthetic-prototype-reviewed','check.prototype-reviewed'),reviewProof=read(path.join(dst,reviewed.binding.approval_ref)),reviewBasis=[...reviewed.binding.approval_context.basis,bound(dst,reviewed.binding.approval_context.subject_ref),bound(dst,reviewed.binding.approval_ref)];
const cp=read(path.join(dst,'intake-checkpoint.json')),registry=read(path.join(dst,'.template-spec/process/lifecycle-registry.yaml'));
cp.stage='stage.product-design';cp.next_work_unit='work-unit.business-ticket-formalization';cp.checks={};
cp.checks['check.prototype-reviewed']={status:'passed',applicable:true,...reviewed.binding.approval_context,approval_ref:reviewed.binding.approval_ref,subject_digest:reviewProof.subject_digest,basis:reviewBasis,reason:'合成独立当前原型审查',evidence_refs:reviewBasis.map(row=>row.ref),evidence:{'evidence.prototype-review-result':[reviewed.binding.approval_ref]}};
const verifiedBasis=[bound(dst,'current-prototype-evidence.json'),bound(dst,'current-prototype-validation.json')],verifiedKinds=registry.checks.find(row=>row.id==='check.prototype-verified').evidence;
cp.checks['check.prototype-verified']={status:'passed',applicable:true,reason:'标准原型验证器核验当前H2离线协议及原始回复',basis:verifiedBasis,evidence_refs:verifiedBasis.map(row=>row.ref),evidence:Object.fromEntries(verifiedKinds.map(kind=>[kind,['current-prototype-evidence.json','current-prototype-validation.json']]))};
// The final product decision really covers the two newly completed checks.
// Re-present the changed aggregate before producing its original reply.
const completed=attachArtifactApproval(dst,'current-prototype-evidence.json','synthetic-product-completed','gate.product-design-approved'),completeContext=completed.binding.approval_context,completeProof=read(path.join(dst,completed.binding.approval_ref));
const aggregateBasis=new Map();for(const row of [...completeContext.basis,...reviewBasis,...verifiedBasis,bound(dst,prototype.persisted_ref)]){const previous=aggregateBasis.get(row.ref);if(previous&&previous.digest!==row.digest)throw Error('合成聚合依据原字节冲突');aggregateBasis.set(row.ref,row);}completeContext.basis=[...aggregateBasis.values()];
put(dst,completeContext.subject_ref,{gate_id:'gate.product-design-approved',...completeContext});
completeProof.basis=completeContext.basis;completeProof.subject_digest=hash(fs.readFileSync(path.join(dst,completeContext.subject_ref))).slice(7);
const confirm=buildDecisionFixture(path.join(dst,'approvals/current-product-aggregate-decision'),{boundary:'gate.product-design-approved',scope:completeContext.approval_scope,subjectRef:path.join(dst,completeContext.subject_ref)}),rel=value=>path.relative(dst,value).split(path.sep).join('/');
confirm.record.request.items[0].subject.ref=completeContext.subject_ref;confirm.record.request.requester_source.ref=rel(confirm.record.request.requester_source.ref);confirm.present();confirm.record.responses=[];confirm.respond();confirm.record.request.presented_source.ref=rel(confirm.record.request.presented_source.ref);confirm.record.responses[0].source.ref=rel(confirm.record.responses[0].source.ref);confirm.save();
completeProof.user_decision_ref=rel(confirm.ref);put(dst,completed.binding.approval_ref,completeProof);
const productBasis=[...completeContext.basis,bound(dst,completeContext.subject_ref),bound(dst,completed.binding.approval_ref)];
cp.gates['gate.product-design-approved']={status:'approved',...completeContext,subject_digest:completeProof.subject_digest,approval_ref:completed.binding.approval_ref,reason:'实际当前来源产品决定和独立原型审查已核验',basis:productBasis,evidence_refs:productBasis.map(row=>row.ref),evidence:{'evidence.prototype-confirmation':[completed.binding.approval_ref]}};
put(dst,'intake-checkpoint.json',cp);put(dst,'docs/.scratch/'+feature+'/map.md','---\ncheckpoint_ref: intake-checkpoint.json\n---\n# Synthetic current applicable Design\n');
put(src,'docs/.scratch/'+feature+'/map.md','---\ncheckpoint_ref: '+cpRef+'\n---\n# Synthetic current Spec\n');
console.log(JSON.stringify({receipt_ref:cp.upstream_spec_baseline.receipt_ref,verification_ref:'current-prototype-evidence.json'}));`
	cmd = exec.Command("node", "--input-type=module", "-e", program, f.Spec, f.Design, f.Checkpoint, specSeed, designSeed)
	cmd.Dir = oracle
	raw, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual applicable product approval/prototype protocols: %v %s", err, raw)
	}
	var refs struct {
		Receipt      string `json:"receipt_ref"`
		Verification string `json:"verification_ref"`
	}
	if err = json.Unmarshal(raw, &refs); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{f.Spec, specSeed}, {f.Design, designSeed}} {
		for _, ref := range []string{".yss.json", ".template-spec/process/harness-profile.yaml", ".template-spec/process/lifecycle-registry.yaml", ".template-spec/agents/digital-human-roles.yaml", guidanceContractRef(map[bool]string{true: "spec", false: "design"}[pair[0] == f.Spec])} {
			if !bytes.Equal(mustReadSpecBaselineTestFile(t, filepath.Join(pair[0], ref)), mustReadSpecBaselineTestFile(t, filepath.Join(pair[1], ref))) {
				t.Fatalf("applicable product fixture changed actual native policy: %s", ref)
			}
		}
	}
	cpBefore := mustReadSpecBaselineTestFile(t, filepath.Join(f.Spec, f.Checkpoint))
	designBefore := mustReadSpecBaselineTestFile(t, filepath.Join(f.Design, "intake-checkpoint.json"))
	receiptBefore := mustReadSpecBaselineTestFile(t, filepath.Join(f.Design, refs.Receipt))
	baselineBefore := progressionInventory(t, f.Baseline)
	apTestPut(t, f.Spec, "target-input.json", ProgressionTarget{SchemaVersion: 1, Kind: "lifecycle-progression-target", FeatureID: "feature.supplier", CheckpointRef: f.Checkpoint, Target: "product-design-completed", IntentSource: "Synthetic actual applicable product goal", Consumers: []ProgressionConsumer{{Profile: "design", Root: f.Design, CheckpointRef: "intake-checkpoint.json"}}})
	planFile := filepath.Join(f.Root, "product-goal-plan.json")
	if _, err = progressionTargetRun(context.Background(), f.Spec, map[string]string{"checkpoint": f.Checkpoint, "input": "target-input.json", "plan": "true", "out": planFile}); err != nil {
		t.Fatal(err)
	}
	if _, err = progressionTargetRun(context.Background(), f.Spec, map[string]string{"apply": "true", "plan-file": planFile}); err != nil {
		t.Fatal(err)
	}
	before := progressionInventory(t, f.Spec)
	result, err := progressionRead(context.Background(), f.Spec, f.Checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	p := semMap(result["progression"])
	if p["status"] != "reached" || p["reached"] != true || semMap(semMap(result["coordination"])["design"])["input_status"] != "verified" {
		t.Fatalf("actual applicable product target failed: status=%v reason=%v coordination=%#v", p["status"], p["reason"], result["coordination"])
	}
	if semMap(semMap(p["completion"])["profile"])["status"] == "reached" || semMap(semMap(p["completion"])["business"])["status"] == "reached" {
		t.Fatal("product completion reported whole business/Profile complete")
	}
	// The actual native local facade can prepare against these current original
	// Spec/Design approvals before a future implementation Slice or backend.
	// It still cannot issue formal frontend inputs without the approved Slice.
	local := exec.Command(os.Getenv("YSS_NATIVE_BINARY"), "contract", "verify", "--root", f.Spec, "--kind", "frontend-delivery", "--file", f.Checkpoint, "--checkpoint", f.Checkpoint, "--phase", "contract", "--json")
	localRaw, err := local.CombinedOutput()
	if err != nil {
		t.Fatalf("native current local preparation facade: %v %s", err, localRaw)
	}
	var facade struct {
		Status string          `json:"status"`
		Code   string          `json:"code"`
		Result *SemanticReport `json:"result"`
	}
	if err = json.Unmarshal(localRaw, &facade); err != nil {
		t.Fatal(err)
	}
	if facade.Status != "ok" || facade.Code != "OK" || facade.Result == nil || !facade.Result.ReadOnly || facade.Result.ApprovalCreated || facade.Result.ExecutionAuthorization != "not-evaluated" || facade.Result.Coverage["delivery_mode"] != "local-approved-assets" || facade.Result.Coverage["phase"] != "contract" || facade.Result.Coverage["ready_for_agent"] != false || facade.Result.Coverage["inputs_current"] != true || facade.Result.Coverage["slice_id"] != nil {
		t.Fatalf("local preparation overstated execution: %s", localRaw)
	}
	formal := exec.Command(os.Getenv("YSS_NATIVE_BINARY"), "contract", "verify", "--root", f.Spec, "--kind", "frontend-delivery", "--file", f.Checkpoint, "--checkpoint", f.Checkpoint, "--phase", "inputs", "--json")
	if raw, err := formal.CombinedOutput(); err == nil {
		t.Fatalf("formal frontend inputs invented a future approved Slice/backend: %s", raw)
	}
	if !contractSame(before, progressionInventory(t, f.Spec)) || !bytes.Equal(cpBefore, mustReadSpecBaselineTestFile(t, filepath.Join(f.Spec, f.Checkpoint))) || !bytes.Equal(designBefore, mustReadSpecBaselineTestFile(t, filepath.Join(f.Design, "intake-checkpoint.json"))) || !bytes.Equal(receiptBefore, mustReadSpecBaselineTestFile(t, filepath.Join(f.Design, refs.Receipt))) || !contractSame(baselineBefore, progressionInventory(t, f.Baseline)) {
		t.Fatal("applicable goal changed approved checkpoints, Receipt or frozen source package")
	}
	for _, variant := range []string{"missing-independent-prototype-review", "current-prototype-evidence-drift"} {
		t.Run(variant, func(t *testing.T) {
			ref, before := "intake-checkpoint.json", designBefore
			if variant == "current-prototype-evidence-drift" {
				ref = refs.Verification
				before = mustReadSpecBaselineTestFile(t, filepath.Join(f.Design, ref))
				apTestPut(t, f.Design, ref, append(append([]byte{}, before...), '\n'))
			} else {
				cp := semMap(mustParseContract(before))
				delete(semMap(cp["checks"]), "check.prototype-reviewed")
				apTestPut(t, f.Design, ref, cp)
			}
			defer apTestPut(t, f.Design, ref, before)
			result, err := progressionRead(context.Background(), f.Spec, f.Checkpoint)
			if err != nil {
				t.Fatal(err)
			}
			if semMap(result["progression"])["reached"] != false {
				t.Fatalf("applicable product accepted %s: %#v", variant, result)
			}
		})
	}
	for seed, before := range seedBefore {
		if !contractSame(before, progressionInventory(t, seed)) {
			t.Fatal("applicable protocol fixture changed actual native seed")
		}
	}
}

func TestLifecycleTargetNativeSpecDesignBusinessSequence(t *testing.T) {
	oracle := governanceOracleRoot(t)
	cleanupFixture := func(root string) {
		t.Cleanup(func() {
			if t.Failed() && os.Getenv("YSS_E7_RETAIN_FAILED_ROOTS") == "1" {
				t.Logf("retained synthetic E7 diagnostic root (not acceptance): %s", root)
				return
			}
			_ = os.RemoveAll(root)
		})
	}
	specSeed, designSeed := specBaselineActualNativeSeed(t, "spec"), specBaselineActualNativeSeed(t, "design")
	backendSeed := backendProfileTestNativeSeed(t)
	t.Log("actual native Spec, Design and Backend seeds prepared")
	seedBefore := map[string]map[string]string{}
	for _, seed := range []string{specSeed, designSeed, backendSeed} {
		seedBefore[seed] = progressionInventory(t, seed)
	}
	cmd := exec.Command("node", filepath.Join(oracle, "scripts/fixtures/spec-baseline/scenarios.mjs"))
	cmd.Dir = oracle
	cmd.Env = append(os.Environ(), "YSS_SPEC_BASELINE_NO_DESIGN=1", "YSS_SPEC_BASELINE_NATIVE_SEED="+specSeed, "YSS_DESIGN_BASELINE_NATIVE_SEED="+designSeed, "YSS_SPEC_BASELINE_NATIVE_CLI="+os.Getenv("YSS_NATIVE_BINARY"))
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual approved Spec/Design intake chain: %v %s", err, raw)
	}
	var f struct {
		Root         string `json:"root"`
		Spec         string `json:"source_root"`
		Design       string `json:"design_root"`
		Checkpoint   string `json:"checkpoint_ref"`
		Baseline     string `json:"baseline_package"`
		NativeIntake bool   `json:"native_intake"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	cleanupFixture(f.Root)
	if !f.NativeIntake {
		t.Fatal("actual native Spec/Design/business test did not use the public native intake chain")
	}
	t.Logf("actual approved Spec/Design intake chain prepared: spec=%s checkpoint=%s design=%s baseline=%s", f.Spec, f.Checkpoint, f.Design, f.Baseline)
	// All new records below are synthetic current evidence produced by existing
	// approval/finalization protocols; installed target policy is unchanged.
	program := `import fs from 'node:fs';import path from 'node:path';
import {read,json,hash} from '` + governanceOracleURL(filepath.Join(oracle, "scripts/lib/strategic-handoff-io.mjs")) + `';
import {finalizeDelivery,importBundle} from '` + governanceOracleURL(filepath.Join(oracle, "scripts/lib/strategic-handoff.mjs")) + `';
import {attachArtifactApproval} from '` + governanceOracleURL(filepath.Join(oracle, "scripts/fixtures/backend-delivery/approval-fixture.mjs")) + `';
import {backendProfileTerminalFixture} from '` + governanceOracleURL(filepath.Join(oracle, "scripts/fixtures/backend-delivery/backend-profile-terminal-fixture.mjs")) + `';
import {reconcile} from '` + governanceOracleURL(filepath.Join(oracle, "scripts/fixtures/spec-baseline/fixture.mjs")) + `';
import {buildDecisionFixture} from '` + governanceOracleURL(filepath.Join(oracle, "scripts/lib/testing/user-decision-fixture.mjs")) + `';
const [src,dst,cpRef,seed,backendSeed]=process.argv.slice(1),put=(root,ref,value)=>{const file=path.join(root,ref);fs.mkdirSync(path.dirname(file),{recursive:true});fs.writeFileSync(file,typeof value==='string'?value:json(value));},bound=(root,ref)=>({ref,digest:hash(fs.readFileSync(path.join(root,ref))).slice(7)});
const cp=read(path.join(dst,'intake-checkpoint.json')),receipt=read(path.join(dst,cp.upstream_spec_baseline.receipt_ref)),manifest=read(path.join(dst,receipt.package_ref,'manifest.json')),stageRef=receipt.package_ref+'/payload/files/'+manifest.source.stage_decision_package_ref;
const handoff=read(path.join(dst,'handoff.yaml')),approval=handoff.package_export.approvals.handoff,record=read(path.join(dst,approval.record_ref));
cp.stage='stage.product-design';cp.next_work_unit='work-unit.strategic-design-handoff';
cp.artifacts['artifact.stage-decision-package']={status:'approved',ref:stageRef,evidence_refs:[cp.upstream_spec_baseline.receipt_ref]};
cp.gates['gate.product-design-approved']={status:'not-applicable',reason:'当前批准原Spec影响评估不要求产品设计',evidence_refs:[stageRef],basis:[bound(dst,stageRef)]};
const finalized=await finalizeDelivery({sourceRoot:dst,handoffRef:'handoff.yaml'}),deliveryRef=path.relative(dst,finalized.delivery).split(path.sep).join('/'),recordRef=deliveryRef+'/delivery-record.json',verificationRef=deliveryRef+'/verification.json';
const handoffBasis=[...record.basis,bound(dst,record.subject_ref),bound(dst,approval.record_ref)];
const gateId='gate.strategic-design-handoff-approved',definition=read(path.join(dst,'.template-spec/process/lifecycle-registry.yaml')).gates.find(row=>row.id===gateId),freshRefs=record.evidence_refs.filter(ref=>record.basis.some(row=>row.ref===ref));
const evidenceByKind={'evidence.approval-record':[approval.record_ref],'evidence.strategic-design-handoff':['handoff.yaml'],'evidence.fresh-verification':freshRefs};
const handoffEvidence=Object.fromEntries(definition.evidence.map(kind=>{const refs=evidenceByKind[kind];if(!refs?.length||refs.some(ref=>!handoffBasis.some(row=>row.ref===ref)))throw Error('Installed Design gate evidence has no current original bound proof: '+kind);return[kind,refs];}));
cp.gates[gateId]={status:'approved',subject_ref:record.subject_ref,approval_ref:approval.record_ref,approval_scope:record.approval_scope,drafter_principal_ref:record.drafter_principal_ref,subject_digest:record.subject_digest,basis:handoffBasis,reason:'合成当前独立交接审查与真实验包已核验',evidence_refs:handoffBasis.map(row=>row.ref),evidence:handoffEvidence};
cp.artifacts['artifact.strategic-design-handoff']={status:'approved',ref:'handoff.yaml',digest:hash(fs.readFileSync(path.join(dst,'handoff.yaml'))),evidence_refs:[recordRef,verificationRef]};cp.verification.strategic_delivery={record_ref:recordRef,bundle_digest:finalized.bundle_digest};
put(dst,'intake-checkpoint.json',cp);put(dst,'docs/.scratch/feature.supplier/map.md','---\ncheckpoint_ref: intake-checkpoint.json\n---\n# Synthetic current Design feature\n');
put(src,'docs/.scratch/feature.supplier/map.md','---\ncheckpoint_ref: '+cpRef+'\n---\n# Synthetic current Spec feature\n');
// This is frozen verification evidence, not a local Spec Handoff. The
// explicitly bound Design retains ownership of its original working files.
fs.cpSync(finalized.delivery,path.join(src,deliveryRef),{recursive:true});
put(src,'business-rollback.log','Synthetic independent rollback evidence, not a production deployment.');
const accepted=attachArtifactApproval(src,verificationRef,'synthetic-business-acceptance','gate.delivery-accepted'),proof=read(path.join(src,accepted.binding.approval_ref)),context=accepted.binding.approval_context;
const current=read(path.join(src,cpRef)),sourceStage=current.artifacts['artifact.stage-decision-package'].ref;
// This current business checkpoint uses the installed stable Plan asset ID;
// the original approved Plan and frozen baseline checkpoint stay unchanged.
if(current.artifacts['artifact.plan']){current.artifacts['artifact.plan-record']=current.artifacts['artifact.plan'];delete current.artifacts['artifact.plan'];}
context.basis.push(bound(src,'business-rollback.log'),bound(src,sourceStage));put(src,context.subject_ref,{gate_id:'gate.delivery-accepted',...context});proof.basis=context.basis;proof.subject_digest=hash(fs.readFileSync(path.join(src,context.subject_ref))).slice(7);
const confirmation=buildDecisionFixture(path.join(src,'approvals/current-business-aggregate-decision'),{boundary:'gate.delivery-accepted',scope:context.approval_scope,subjectRef:path.join(src,context.subject_ref)}),relative=value=>path.relative(src,value).split(path.sep).join('/');
confirmation.record.request.items[0].subject.ref=context.subject_ref;confirmation.record.request.requester_source.ref=relative(confirmation.record.request.requester_source.ref);confirmation.present();confirmation.record.responses=[];confirmation.respond();confirmation.record.request.presented_source.ref=relative(confirmation.record.request.presented_source.ref);confirmation.record.responses[0].source.ref=relative(confirmation.record.responses[0].source.ref);confirmation.save();
proof.user_decision_ref=relative(confirmation.ref);put(src,accepted.binding.approval_ref,proof);
current.checks['check.frontend-implementation-verified']={status:'not-applicable',applicable:false,reason:'当前批准Spec影响评估明确没有前端实现影响',basis:[bound(src,sourceStage)]};
const basis=[...context.basis,bound(src,context.subject_ref),bound(src,accepted.binding.approval_ref)];
current.gates['gate.delivery-accepted']={status:'approved',...context,approval_ref:accepted.binding.approval_ref,subject_digest:proof.subject_digest,basis,reason:'合成独立验收、真实正式包验证与回滚证据，仅测试使用',evidence_refs:basis.map(row=>row.ref),evidence:{'evidence.fresh-verification':[verificationRef],'evidence.checkpoint-and-rollback':['business-rollback.log']}};
current.stage='stage.verification-release-retrospective';current.next_work_unit='work-unit.release-and-retrospective';put(src,'business-completed-checkpoint.json',current);
const backend=await backendProfileTerminalFixture({nativeSeed:backendSeed,strategicInput:{delivery:finalized.delivery,bundle_digest:finalized.bundle_digest,route_id:'route.backend'}});
const intake=await importBundle({bundle:finalized.delivery,targetRoot:backend.root}),intakeRecord=read(path.join(backend.root,intake.receipt_ref));
reconcile(backend.root,'business-intake-context.json',{receiptRef:intake.receipt_ref,sourceContextRef:intakeRecord.package_ref+'/payload/files/source-context.snapshot.md'});
const backendCP=read(path.join(backend.root,backend.checkpointRef));backendCP.feature_id=current.feature_id;backendCP.context_reconciliation={status:'reconciled',ref:'business-intake-context.json',evidence_refs:[intake.receipt_ref]};put(backend.root,backend.checkpointRef,backendCP);
console.log(JSON.stringify({receipt_ref:cp.upstream_spec_baseline.receipt_ref,delivery_ref:deliveryRef,source_stage:sourceStage,backend_root:backend.root,backend_checkpoint:backend.checkpointRef,backend_slice:backend.binding.ref,backend_terminal:backend.terminalRef,backend_receipt:intake.receipt_ref}));`
	cmd = exec.Command("node", "--input-type=module", "-e", program, f.Spec, f.Design, f.Checkpoint, specSeed, backendSeed)
	cmd.Dir = oracle
	raw, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual finalized Design and current business approval protocols: %v %s", err, raw)
	}
	var refs struct {
		Receipt           string `json:"receipt_ref"`
		Delivery          string `json:"delivery_ref"`
		BackendRoot       string `json:"backend_root"`
		BackendCheckpoint string `json:"backend_checkpoint"`
		BackendSlice      string `json:"backend_slice"`
		BackendTerminal   string `json:"backend_terminal"`
		BackendReceipt    string `json:"backend_receipt"`
	}
	if err := json.Unmarshal(raw, &refs); err != nil {
		t.Fatal(err)
	}
	cleanupFixture(refs.BackendRoot)
	t.Logf("actual Design finalization and current Backend terminal prepared: backend=%s checkpoint=%s terminal=%s delivery=%s", refs.BackendRoot, refs.BackendCheckpoint, refs.BackendTerminal, refs.Delivery)
	qualifyLocalBackendMilestoneEngineering(t, localImplementationTestFixture{Root: refs.BackendRoot, CheckpointRef: refs.BackendCheckpoint, SliceRef: refs.BackendSlice})
	t.Log("current native Backend engineering prerequisites qualified")
	for _, ref := range []string{".yss.json", ".template-spec/process/harness-profile.yaml", ".template-spec/process/lifecycle-registry.yaml", ".template-spec/process/schemas/lifecycle-registry.schema.json", ".template-spec/process/schemas/lifecycle-checkpoint.schema.json", ".template-spec/agents/digital-human-roles.yaml", guidanceContractRef("spec")} {
		seedInfo, e := os.Lstat(filepath.Join(specSeed, ref))
		if e != nil {
			t.Fatal(e)
		}
		copyInfo, e := os.Lstat(filepath.Join(f.Spec, ref))
		if e != nil || copyInfo.Mode() != seedInfo.Mode() || !bytes.Equal(mustReadSpecBaselineTestFile(t, filepath.Join(specSeed, ref)), mustReadSpecBaselineTestFile(t, filepath.Join(f.Spec, ref))) {
			t.Fatalf("current business fixture changed actual native identity/approval policy: %s", ref)
		}
	}
	for _, ref := range []string{".yss.json", ".template-spec/process/harness-profile.yaml", ".template-spec/process/lifecycle-registry.yaml", ".template-spec/process/schemas/lifecycle-registry.schema.json", ".template-spec/process/schemas/lifecycle-checkpoint.schema.json", ".template-spec/agents/digital-human-roles.yaml", guidanceContractRef("backend")} {
		seedInfo, e := os.Lstat(filepath.Join(backendSeed, ref))
		if e != nil {
			t.Fatal(e)
		}
		copyInfo, e := os.Lstat(filepath.Join(refs.BackendRoot, ref))
		if e != nil || copyInfo.Mode() != seedInfo.Mode() || !bytes.Equal(mustReadSpecBaselineTestFile(t, filepath.Join(backendSeed, ref)), mustReadSpecBaselineTestFile(t, filepath.Join(refs.BackendRoot, ref))) {
			t.Fatalf("current implementation fixture changed actual native Backend policy: %s", ref)
		}
	}
	for _, ref := range []string{".yss.json", ".template-spec/process/harness-profile.yaml", ".template-spec/process/lifecycle-registry.yaml", ".template-spec/process/schemas/lifecycle-registry.schema.json", ".template-spec/process/schemas/lifecycle-checkpoint.schema.json", ".template-spec/agents/digital-human-roles.yaml", guidanceContractRef("design")} {
		seedInfo, e := os.Lstat(filepath.Join(designSeed, ref))
		if e != nil {
			t.Fatal(e)
		}
		copyInfo, e := os.Lstat(filepath.Join(f.Design, ref))
		if e != nil || copyInfo.Mode() != seedInfo.Mode() || !bytes.Equal(mustReadSpecBaselineTestFile(t, filepath.Join(designSeed, ref)), mustReadSpecBaselineTestFile(t, filepath.Join(f.Design, ref))) {
			t.Fatalf("current strategic fixture changed actual native Design policy: %s", ref)
		}
	}
	consumer := ProgressionConsumer{Profile: "design", Root: f.Design, CheckpointRef: "intake-checkpoint.json"}
	backendConsumer := ProgressionConsumer{Profile: "backend", Root: refs.BackendRoot, CheckpointRef: refs.BackendCheckpoint}
	sourceCP, designCP := mustReadSpecBaselineTestFile(t, filepath.Join(f.Spec, f.Checkpoint)), mustReadSpecBaselineTestFile(t, filepath.Join(f.Design, consumer.CheckpointRef))
	completed := semMap(mustParseContract(mustReadSpecBaselineTestFile(t, filepath.Join(f.Spec, "business-completed-checkpoint.json"))))
	// This is the current progress record, separate from the frozen baseline.
	// Track the already approved Spec using its original approval bytes, rather
	// than generating a Task or using a future acceptance report as its basis.
	parentRef := path.Join(path.Dir(f.Checkpoint), "parent-ticket.md")
	apTestPut(t, f.Spec, parentRef, "Synthetic current business parent Ticket bound to "+f.Checkpoint+"; no product authorization.\n")
	trackBinding := func(ref string) Binding {
		return Binding{Ref: ref, Digest: "sha256:" + safefs.Digest(mustReadSpecBaselineTestFile(t, filepath.Join(f.Spec, ref)))}
	}
	specRef := checkpointAsset(completed, "spec_ref", "artifact.spec")
	specApprovalRef := text(semMap(semMap(completed["gates"])["gate.spec-baseline-approved"])["approval_ref"])
	completed["stage_tracking"] = StageTracking{SchemaVersion: 1, FeatureID: text(completed["feature_id"]), CheckpointRef: f.Checkpoint, EntryStage: "stage.spec-architecture", Entry: TrackingEntry{Kind: "parent-ticket", Ref: parentRef}, Items: []WorkItem{{ID: "synthetic-current-approved-spec", Kind: "stage-work-item", Title: "合成测试：已批准 Spec 的当前追踪", Stage: "stage.spec-architecture", WorkUnit: "work-unit.spec-synthesis", Owner: "synthetic.spec-drafter", Scope: "synthetic current feature only", Acceptance: []string{"当前 Spec 与原批准记录可核验"}, Dependencies: []string{}, SourceRefs: []Binding{trackBinding(specRef)}, Progress: "completed", SplitReasons: []string{}, Completion: []Completion{{Criterion: "当前 Spec 与原批准记录可核验", EvidenceRefs: []Binding{trackBinding(specApprovalRef)}}}}}}
	apTestPut(t, f.Spec, "business-completed-checkpoint.json", completed)
	completedCP := mustReadSpecBaselineTestFile(t, filepath.Join(f.Spec, "business-completed-checkpoint.json"))
	receiptBefore, baselineBefore := mustReadSpecBaselineTestFile(t, filepath.Join(f.Design, refs.Receipt)), progressionInventory(t, f.Baseline)
	deliveryBefore := progressionInventory(t, filepath.Join(f.Design, refs.Delivery))
	backendPackageBefore := progressionInventory(t, filepath.Join(refs.BackendRoot, "backend-package"))
	backendReceiptBefore := mustReadSpecBaselineTestFile(t, filepath.Join(refs.BackendRoot, refs.BackendReceipt))
	backendCPBefore := mustReadSpecBaselineTestFile(t, filepath.Join(refs.BackendRoot, refs.BackendCheckpoint))
	approvedBefore := map[string][]byte{}
	manifest := semMap(mustParseContract(mustReadSpecBaselineTestFile(t, filepath.Join(f.Baseline, "manifest.json"))))
	for _, item := range semList(manifest["files"]) {
		ref := text(semMap(item)["original_ref"])
		if ref != "" && ref != f.Checkpoint {
			approvedBefore[filepath.Join(f.Spec, ref)] = mustReadSpecBaselineTestFile(t, filepath.Join(f.Spec, ref))
		}
	}
	for _, cpBytes := range [][]byte{completedCP, designCP} {
		for _, row := range semMap(semMap(mustParseContract(cpBytes))["gates"]) {
			for _, key := range []string{"approval_ref", "subject_ref"} {
				ref := text(semMap(row)[key])
				if ref != "" {
					root := f.Spec
					if bytes.Equal(cpBytes, designCP) {
						root = f.Design
					}
					approvedBefore[filepath.Join(root, ref)] = mustReadSpecBaselineTestFile(t, filepath.Join(root, ref))
				}
			}
		}
	}
	for _, target := range []string{"spec-approved", "product-design-completed", "business-accepted"} {
		t.Run(target, func(t *testing.T) {
			if target == "product-design-completed" {
				current := semMap(mustParseContract(sourceCP))
				current["stage"], current["next_work_unit"] = "stage.product-design", "work-unit.technical-analysis"
				apTestPut(t, f.Spec, f.Checkpoint, current)
			}
			goalCP := mustReadSpecBaselineTestFile(t, filepath.Join(f.Spec, f.Checkpoint))
			apTestPut(t, f.Spec, "goal-input.json", ProgressionTarget{SchemaVersion: 1, Kind: "lifecycle-progression-target", FeatureID: "feature.supplier", CheckpointRef: f.Checkpoint, Target: target, IntentSource: "Synthetic authorized sequence", Consumers: []ProgressionConsumer{consumer}})
			planFile := filepath.Join(f.Root, target+"-plan.json")
			if _, err := progressionTargetRun(context.Background(), f.Spec, map[string]string{"checkpoint": f.Checkpoint, "input": "goal-input.json", "plan": "true", "out": planFile}); err != nil {
				t.Fatal(err)
			}
			if _, err := progressionTargetRun(context.Background(), f.Spec, map[string]string{"apply": "true", "plan-file": planFile}); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(goalCP, mustReadSpecBaselineTestFile(t, filepath.Join(f.Spec, f.Checkpoint))) {
				t.Fatal("intent transaction rewrote current stage/approvals")
			}
			if target == "business-accepted" {
				pending, err := progressionRead(context.Background(), f.Spec, f.Checkpoint)
				if err != nil || semMap(pending["progression"])["reached"] != false || semMap(pending["progression"])["status"] != "pending" {
					t.Fatalf("raising goal completed an unaccepted business: %#v %v", pending, err)
				}
				// Existing independent acceptance supplies actual current gate/proof
				// facts. This legal progress is distinct from changing the goal.
				apTestPut(t, f.Spec, f.Checkpoint, completedCP)
				// A genuine independent business gate with a typed strategic
				// verification still cannot close required backend implementation.
				strategicOnly, err := progressionRead(context.Background(), f.Spec, f.Checkpoint)
				if err != nil || semMap(strategicOnly["progression"])["reached"] != false || semMap(semMap(semMap(strategicOnly["progression"])["completion"])["business"])["status"] == "reached" {
					t.Fatalf("strategic-only report completed applicable implementation: %#v %v", strategicOnly, err)
				}
				apTestPut(t, f.Spec, "goal-input.json", ProgressionTarget{SchemaVersion: 1, Kind: "lifecycle-progression-target", FeatureID: "feature.supplier", CheckpointRef: f.Checkpoint, Target: target, IntentSource: "Synthetic current implementation consumer binding", Consumers: []ProgressionConsumer{consumer, backendConsumer}})
				planFile = filepath.Join(f.Root, "business-current-consumers-plan.json")
				if _, err := progressionTargetRun(context.Background(), f.Spec, map[string]string{"checkpoint": f.Checkpoint, "input": "goal-input.json", "plan": "true", "out": planFile}); err != nil {
					t.Fatal(err)
				}
				if _, err := progressionTargetRun(context.Background(), f.Spec, map[string]string{"apply": "true", "plan-file": planFile}); err != nil {
					t.Fatal(err)
				}
				if _, err := RunContext(context.Background(), "lifecycle", "verify", f.Spec, map[string]string{"checkpoint": f.Checkpoint}); err != nil {
					t.Fatalf("public current business checkpoint prerequisite: %v", err)
				}
			}
			result, err := progressionRead(context.Background(), f.Spec, f.Checkpoint)
			if err != nil {
				t.Fatal(err)
			}
			p := semMap(result["progression"])
			wantNext := semMap(mustParseContract(goalCP))["next_work_unit"]
			if target == "business-accepted" {
				wantNext = "work-unit.release-and-retrospective"
			}
			if p["reached"] != true || semMap(semMap(result["coordination"])["design"])["input_status"] != "verified" || semMap(semMap(result["coordination"])["design"])["completion_status"] != "reached" || semMap(result["next_action"])["root_work_unit"] != wantNext {
				t.Fatalf("actual current goal/consumer completion failed: status=%v reason=%v coordination=%#v", p["status"], p["reason"], result["coordination"])
			}
			if target == "product-design-completed" && p["status"] != "not-applicable" {
				t.Fatal("qualified approved design applicability was lost")
			}
			if target == "business-accepted" && (semMap(semMap(p["completion"])["business"])["status"] != "reached" || semMap(semMap(p["completion"])["profile"])["status"] != "reached") {
				t.Fatal("actual whole business/Profile completion did not close")
			}
			if target == "business-accepted" {
				reportRef := refs.Delivery + "/verification.json"
				args := map[string]string{"kind": "verification", "file": reportRef, "checkpoint": f.Checkpoint}
				if _, err := RunContext(context.Background(), "evidence", "verify", f.Spec, args); err != nil {
					t.Fatalf("public current explicit Design frozen report: %v", err)
				}
				copyRef := "different-design-verification.json"
				raw := mustReadSpecBaselineTestFile(t, filepath.Join(f.Spec, reportRef))
				apTestPut(t, f.Spec, copyRef, append(append([]byte{}, raw...), '\n'))
				args["file"] = copyRef
				if _, err := RunContext(context.Background(), "evidence", "verify", f.Spec, args); semanticCode(err) != "VERIFICATION_CURRENT_REQUIRED" {
					t.Fatalf("same parsed strategic report with different original bytes accepted: %v", err)
				}
				if err := os.Remove(filepath.Join(f.Spec, copyRef)); err != nil {
					t.Fatal(err)
				}
			}
			if target != "business-accepted" && (semMap(semMap(p["completion"])["business"])["status"] == "reached" || semMap(semMap(p["completion"])["profile"])["status"] == "reached") {
				t.Fatal("short milestone reported an unaccepted whole business/Profile complete")
			}
			if !bytes.Equal(designCP, mustReadSpecBaselineTestFile(t, filepath.Join(f.Design, consumer.CheckpointRef))) || !bytes.Equal(receiptBefore, mustReadSpecBaselineTestFile(t, filepath.Join(f.Design, refs.Receipt))) || !contractSame(baselineBefore, progressionInventory(t, f.Baseline)) || !contractSame(deliveryBefore, progressionInventory(t, filepath.Join(f.Design, refs.Delivery))) {
				t.Fatal("target continuation altered current checkpoint, original Receipt or frozen packages")
			}
			if !bytes.Equal(backendCPBefore, mustReadSpecBaselineTestFile(t, filepath.Join(refs.BackendRoot, refs.BackendCheckpoint))) || !bytes.Equal(backendReceiptBefore, mustReadSpecBaselineTestFile(t, filepath.Join(refs.BackendRoot, refs.BackendReceipt))) || !contractSame(backendPackageBefore, progressionInventory(t, filepath.Join(refs.BackendRoot, "backend-package"))) {
				t.Fatal("target changes altered current implementation checkpoint, Receipt or full Backend package")
			}
			for file, before := range approvedBefore {
				if !bytes.Equal(before, mustReadSpecBaselineTestFile(t, file)) {
					t.Fatalf("target continuation altered original approval bytes: %s", file)
				}
			}
			for _, root := range []string{f.Spec, f.Design} {
				cpRef := f.Checkpoint
				if root == f.Design {
					cpRef = consumer.CheckpointRef
				}
				cp := semMap(mustParseContract(mustReadSpecBaselineTestFile(t, filepath.Join(root, cpRef))))
				setRef := checkpointAsset(cp, "business_ticket_set_ref", "artifact.business-ticket-set")
				set := semMap(mustParseContract(mustReadSpecBaselineTestFile(t, filepath.Join(root, setRef))))
				if len(semList(set["tickets"])) != 1 || semMap(semList(set["tickets"])[0])["id"] != "BT-001" {
					t.Fatal("goal continuation replaced the stable business Ticket IDs")
				}
			}
		})
	}
	for _, variant := range []string{"consumer-terminal-pending", "same-feature-wrong-checkpoint", "stale-receipt", "receipt-version-mismatch", "current-context-drift"} {
		t.Run(variant, func(t *testing.T) {
			root, ref := f.Design, consumer.CheckpointRef
			if variant == "same-feature-wrong-checkpoint" {
				ref = "docs/.scratch/feature.supplier/map.md"
			} else if variant == "stale-receipt" || variant == "receipt-version-mismatch" {
				ref = refs.Receipt
			} else if variant == "current-context-drift" {
				ref = "CONTEXT.md"
			}
			before := mustReadSpecBaselineTestFile(t, filepath.Join(root, ref))
			defer apTestPut(t, root, ref, before)
			if variant == "consumer-terminal-pending" {
				cp := semMap(mustParseContract(before))
				delete(semMap(cp["gates"]), "gate.strategic-design-handoff-approved")
				apTestPut(t, root, ref, cp)
			} else if variant == "same-feature-wrong-checkpoint" {
				other := "other-current-checkpoint.json"
				apTestPut(t, root, other, designCP)
				defer os.Remove(filepath.Join(root, other))
				apTestPut(t, root, ref, "---\ncheckpoint_ref: "+other+"\n---\n# Different explicitly registered checkpoint for same feature\n")
			} else if variant == "receipt-version-mismatch" {
				receipt := semMap(mustParseContract(before))
				receipt["version"] = "v2"
				apTestPut(t, root, ref, receipt)
			} else if variant == "current-context-drift" {
				meaning := []byte("提供产品或服务的主体")
				if bytes.Count(before, meaning) != 1 {
					t.Fatal("current Context fixture must contain one bound Supplier meaning")
				}
				apTestPut(t, root, ref, bytes.Replace(before, meaning, []byte("负责已修订业务责任范围的供应商主体"), 1))
			} else {
				apTestPut(t, root, ref, append(append([]byte{}, before...), '\n'))
			}
			result, err := progressionRead(context.Background(), f.Spec, f.Checkpoint)
			if err != nil {
				t.Fatal(err)
			}
			p, row := semMap(result["progression"]), semMap(semMap(result["coordination"])["design"])
			if p["reached"] != false || p["status"] == "reached" || semMap(semMap(p["completion"])["profile"])["status"] == "reached" || semMap(semMap(p["completion"])["business"])["status"] == "reached" {
				t.Fatalf("current whole business accepted unqualified %s consumer: status=%v reason=%v row=%#v", variant, p["status"], p["reason"], row)
			}
		})
	}
	for seed, before := range seedBefore {
		if !contractSame(before, progressionInventory(t, seed)) {
			t.Fatalf("current business protocol fixture changed its actual native seed: %s", seed)
		}
	}
}
