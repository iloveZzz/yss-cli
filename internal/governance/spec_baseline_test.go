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
	for _, variant := range []string{"draft-spec", "missing-current-spec", "template-source-checkpoint", "pending-context", "inherited-source", "conflicting-spec-ref", "unbound-current-spec", "unapproved-current-plan", "unapproved-stage", "asserted-unapproved-stage", "wrong-kind-stage", "wrong-kind-strategy", "missing-current-strategy"} {
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
	contextAfter, err := os.ReadFile(filepath.Join(targetRoot, "CONTEXT.md"))
	if err != nil || !bytes.Equal(contextBefore, contextAfter) {
		t.Fatal("baseline import replaced target Context")
	}
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
