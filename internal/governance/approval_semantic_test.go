package governance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

// Fixtures are synthetic test evidence; they never authorize a real user action.
func apTestPut(t *testing.T, root, ref string, v any) []byte {
	t.Helper()
	var b []byte
	switch value := v.(type) {
	case []byte:
		b = value
	case string:
		b = []byte(value)
	default:
		var err error
		b, err = json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(root, filepath.FromSlash(ref))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0644); err != nil {
		t.Fatal(err)
	}
	return b
}

var apFixtureBundle *bundle.Bundle
var apFixtureBundleErr error
var apFixtureBundleOnce sync.Once

func apTestRoot(t *testing.T) string {
	return apTestProfileRoot(t, "spec")
}

// Each profile uses its own committed distribution's authority bytes, including
// its published signing policy and audience. Only the missing spec identity is
// generated with the same schema and fields as native project initialization.
func apTestProfileRoot(t *testing.T, profile string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var b *bundle.Bundle
	if profile == "spec" {
		apFixtureBundleOnce.Do(func() { apFixtureBundle, apFixtureBundleErr = bundle.Load("spec") })
		b, err = apFixtureBundle, apFixtureBundleErr
	} else {
		b, err = bundle.Load(profile)
	}
	if err != nil {
		t.Fatal(err)
	}
	for ref, f := range b.Files {
		if strings.HasPrefix(ref, ".template-spec/process/schemas/") || ref == ".template-spec/process/harness-profile.yaml" || ref == approvalRolesRef || ref == approvalRegistryRef || ref == approvalSkillsRef || ref == "CONTEXT.md" || ref == "AGENTS.md" {
			raw, e := f.Render(map[string]string{"projectName": "synthetic-governance", "businessDomain": "test-only", "teamSize": "2"})
			if e != nil {
				t.Fatal(e)
			}
			apTestPut(t, root, ref, raw)
		}
	}
	apTestPut(t, root, "yss-project.yaml", map[string]any{"schema_version": 1, "repository_mode": "project-instance"})
	if _, err = os.Stat(filepath.Join(root, ".template-spec/process/harness-profile.yaml")); os.IsNotExist(err) {
		p := domain.Profiles[profile]
		apTestPut(t, root, ".template-spec/process/harness-profile.yaml", map[string]any{"schema_version": 2, "profile_id": p.ID, "instantiation": map[string]any{"cli_package": p.LegacyCommand, "metadata_file": p.Metadata, "template_source": p.TemplateSource}})
	} else if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{approvalRolesRef, approvalRegistryRef, ".template-spec/process/harness-profile.yaml"} {
		raw, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(ref)))
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("profile-authority profile=%s sourceCommit=%s sourceState=%s ref=%s sha256=%s", profile, b.TemplateCommit, b.SourceState, ref, safefs.Digest(raw))
	}
	return root
}
func apTestSession(t *testing.T, root string) *semanticSession {
	t.Helper()
	s := newSemanticSession(context.Background(), root, nil)
	if err := s.authorities(); err != nil {
		t.Fatal(err)
	}
	return s
}
func apTestCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s rejection, got pass", code)
	}
	var d *domain.Error
	if !errors.As(err, &d) {
		t.Fatalf("no structured error: %v", err)
	}
	if d.Code != code {
		t.Fatalf("code=%s want=%s: %v", d.Code, code, err)
	}
}

// Optional, explicitly selected external evidence output for a real native CLI
// smoke run. It contains synthetic protocol evidence and never real approvals.
func apTestExportNativeFixture(t *testing.T, root, profile, kind, state string, argv []string, expectedExit int) {
	t.Helper()
	base := os.Getenv("YSS_NATIVE_FIXTURE_DIR")
	if base == "" {
		return
	}
	if !filepath.IsAbs(base) || filepath.Clean(base) != base {
		t.Fatal("native fixture output must be an explicit canonical absolute directory")
	}
	target := filepath.Join(base, profile, kind, state)
	if _, e := os.Lstat(target); !os.IsNotExist(e) {
		t.Fatalf("refuse to overwrite native fixture evidence: %s (%v)", target, e)
	}
	if e := filepath.WalkDir(root, func(file string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("native fixture output refuses non-regular input: %s", file)
		}
		ref, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		apTestPut(t, target, filepath.ToSlash(ref), raw)
		return os.Chmod(filepath.Join(target, ref), info.Mode().Perm())
	}); e != nil {
		t.Fatal(e)
	}
	argv = append(append([]string{}, argv...), "--root", target, "--profile", profile, "--json")
	apTestPut(t, target, "cli-argv.json", map[string]any{"schema_version": 1, "synthetic_test_evidence": true, "profile": profile, "domain": kind, "case": state, "expected_exit": expectedExit, "argv": argv, "authorizes_real_action": false})
	t.Logf("native-fixture profile=%s domain=%s case=%s expected_exit=%d root=%s", profile, kind, state, expectedExit, target)
}
func apTestRun(root, kind, ref string, extra map[string]string) (any, error) {
	args := map[string]string{"kind": kind, "file": ref}
	for k, v := range extra {
		args[k] = v
	}
	group := "contract"
	if kind == "user-decision" || kind == "approval" {
		group = "evidence"
	}
	return RunContext(context.Background(), group, "verify", root, args)
}
func apTestJSONClone(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	raw, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	var out map[string]any
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if e = d.Decode(&out); e != nil {
		t.Fatal(e)
	}
	return out
}

func apTestCheckpoint(checks map[string]any) map[string]any {
	return map[string]any{"schema_version": 1, "repository_mode": "project-instance", "mode": "route", "status": "routing", "stage": "stage.entry-triage", "artifacts": map[string]any{}, "checks": checks, "gates": map[string]any{}, "context_reconciliation": map[string]any{"status": "pending", "ref": nil, "reason": "synthetic pending", "evidence_refs": []any{}}, "next_work_unit": "work-unit.plan-opportunity", "ticket_sync": map[string]any{"status": "pending", "refs": []any{}}, "verification": map[string]any{"commands": []any{}, "evidence_refs": []any{}}, "human_review": map[string]any{"required": false, "status": "not-applicable", "not_applicable": []any{}, "required_decisions": []any{}, "user_decisions": []any{}}, "git_checkpoint": map[string]any{"required": false, "status": "not-applicable"}, "blockers": []any{}, "rollback": []any{}}
}

func TestSemanticDocumentDigestECMAScriptGolden(t *testing.T) {
	raw := `{"b":1.0,"10":"ten","2":"two","01":"one","<":"<&>","z":1e-7,"a":1e-6,"huge":1e21,"large":9007199254740993,"neg":-0,"\uE000":2,"\uD83D\uDE00":1}`
	var value any
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	if e := d.Decode(&value); e != nil {
		t.Fatal(e)
	}
	want := `{"2":"two","10":"ten","01":"one","<":"<&>","a":0.000001,"b":1,"huge":1e+21,"large":9007199254740992,"neg":0,"z":1e-7,"😀":1,"` + "\uE000" + `":2}`
	if got := string(apCanonical(value)); got != want {
		t.Fatalf("canonical\ngot  %s\nwant %s", got, want)
	}
	hash, e := semanticDocumentDigest(value)
	if e != nil || hash != safefs.Digest([]byte(want)) {
		t.Fatalf("digest=%s err=%v", hash, e)
	}
	if _, e = semanticDocumentDigest(make(chan int)); e == nil {
		t.Fatal("unsupported document must not silently hash null")
	}
}

func TestApprovalExactJSONProtocolVersions(t *testing.T) {
	for _, raw := range []string{"1", "1.0", "1e0", "100e-2", "0.0100e2", "1e+0000"} {
		if got := apNumber(json.Number(raw)); got != 1 {
			t.Fatalf("exact supported version %s = %d", raw, got)
		}
	}
	for _, raw := range []string{"1.0000000000000000001", "1.00000000000000000001", "0.99999999999999999999", "10000000000000000001e-19", "1e-9999999999999999999999", "1e9999999999999999999999", "9223372036854775808", "-9223372036854775809", "01", "+1", "1.", " 1"} {
		if got := apNumber(json.Number(raw)); got != -1 {
			t.Fatalf("unrecognized/fractional version %s rounded to %d", raw, got)
		}
	}
	// Canonical document hashing deliberately retains the old ECMAScript
	// numeric model; exact protocol detection does not alter digest semantics.
	if got := string(apCanonical(json.Number("1.0000000000000000001"))); got != "1" {
		t.Fatalf("ECMAScript digest numeric behavior changed: %s", got)
	}
	root := apTestRoot(t)
	for _, raw := range []string{"1.0000000000000000001", "10000000000000000001e-19", "9223372036854775808"} {
		task := apTestTask(t, root, 2)
		task["schema_version"] = json.Number(raw)
		apTestPut(t, root, "unknown-numeric-version-task.json", task)
		if _, err := apTestRun(root, "task", "unknown-numeric-version-task.json", nil); err == nil {
			t.Fatalf("public verifier accepted unknown exact numeric schema %s", raw)
		}
	}
}

func apTestApproval(t *testing.T, root string) (map[string]any, map[string]any, string) {
	t.Helper()
	s := apTestSession(t, root)
	var gate string
	var policy map[string]any
	for _, bucket := range []string{"check_reviews", "digital_human_review"} {
		for _, row := range apRows(apMap(s.roles["gate_policy"])[bucket]) {
			if len(apStrings(row["countersigners"])) > 0 {
				gate = apText(row["gate"])
				policy = row
				break
			}
		}
		if gate != "" {
			break
		}
	}
	if gate == "" {
		t.Fatal("test needs a fixed-source professional check")
	}
	basis := []any{map[string]any{"ref": "basis.json", "digest": safefs.Digest(apTestPut(t, root, "basis.json", map[string]any{"test_only": true, "version": "v1"}))}}
	subject := map[string]any{"gate_id": gate, "basis": basis, "drafter_principal_ref": "agent.drafter"}
	subjectDigest := safefs.Digest(apTestPut(t, root, "subject.json", subject))
	record := map[string]any{"schema_version": 1, "gate_id": gate, "decision": "approved", "actor_kind": "digital-human", "role_id": apStrings(policy["countersigners"])[0], "runtime_id": "runtime.generic", "principal_ref": "agent.reviewer", "drafter_principal_ref": "agent.drafter", "subject_ref": "subject.json", "subject_digest": subjectDigest, "approval_scope": []any{"unit.demo"}, "basis": basis}
	state := map[string]any{"subject_ref": "subject.json", "subject_digest": subjectDigest, "approval_scope": []any{"unit.demo"}, "basis": basis, "drafter_principal_ref": "agent.drafter", "approval_ref": "approval.json"}
	cp := apTestCheckpoint(map[string]any{gate: state})
	if strings.HasPrefix(gate, "gate.") {
		state["status"] = "approved"
		state["reason"] = "Synthetic current profile signing-policy evidence"
		state["evidence_refs"] = []any{"approval.json"}
		cp["checks"] = map[string]any{}
		cp["gates"] = map[string]any{gate: state}
	}
	for _, bucket := range []string{"checks", "gates"} {
		if declaration := apFind(s.registry[bucket], "id", gate); declaration != nil {
			cp["stage"] = declaration["stage"]
		}
	}
	for _, unit := range apRows(s.registry["work_units"]) {
		if unit["stage"] == cp["stage"] {
			cp["next_work_unit"] = unit["id"]
			break
		}
	}
	apTestPut(t, root, "checkpoint.json", cp)
	apTestPut(t, root, "approval.json", record)
	return record, state, gate
}

func TestApprovalFourProfilesCurrentAndRefusal(t *testing.T) {
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		t.Run(profile, func(t *testing.T) {
			root := apTestProfileRoot(t, profile)
			record, _, gate := apTestApproval(t, root)
			t.Logf("profile-current-check boundary=%s signer=%s runtime=%s", gate, record["role_id"], record["runtime_id"])
			if _, e := apTestRun(root, "approval", "approval.json", map[string]string{"checkpoint": "checkpoint.json"}); e != nil {
				t.Fatal(e)
			}
			apTestExportNativeFixture(t, root, profile, "approval", "positive", []string{"evidence", "verify", "--kind", "approval", "--file", "approval.json", "--checkpoint", "checkpoint.json"}, 0)
			if old, out, ran := apTestLegacy(t, "verify-approval-record", root, "approval.json", "--checkpoint", filepath.Join(root, "checkpoint.json")); ran && !old {
				t.Fatalf("fixed source rejected profile current approval: %s", out)
			}
			record["principal_ref"] = record["drafter_principal_ref"]
			apTestPut(t, root, "approval.json", record)
			if _, e := apTestRun(root, "approval", "approval.json", map[string]string{"checkpoint": "checkpoint.json"}); e == nil {
				t.Fatal("profile approved its own draft")
			}
			apTestExportNativeFixture(t, root, profile, "approval", "refusal", []string{"evidence", "verify", "--kind", "approval", "--file", "approval.json", "--checkpoint", "checkpoint.json"}, 1)
			if old, out, ran := apTestLegacy(t, "verify-approval-record", root, "approval.json", "--checkpoint", filepath.Join(root, "checkpoint.json")); ran && old {
				t.Fatalf("fixed source accepted profile self approval: %s", out)
			}
		})
	}
}

func TestApprovalCurrentPublicBindingAndHistory(t *testing.T) {
	root := apTestRoot(t)
	record, _, _ := apTestApproval(t, root)
	result, err := apTestRun(root, "approval", "approval.json", map[string]string{"checkpoint": "checkpoint.json"})
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	report := result.(*SemanticReport)
	if !report.ReadOnly || report.ApprovalCreated || report.ExecutionAuthorization != "not-evaluated" {
		t.Fatalf("authorization leak: %+v", report)
	}
	_, err = apTestRun(root, "approval", "approval.json", nil)
	apTestCode(t, err, "APPROVAL_CONTEXT_REQUIRED")
	apTestPut(t, root, "fake-checkpoint.json", map[string]any{"checks": map[string]any{record["gate_id"].(string): map[string]any{}}})
	_, err = apTestRun(root, "approval", "approval.json", map[string]string{"checkpoint": "fake-checkpoint.json"})
	apTestCode(t, err, "SCHEMA")
	_, err = apTestRun(root, "approval", "approval.json", map[string]string{"history": "true"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = apTestRun(root, "approval", "approval.json", map[string]string{"history": "true", "checkpoint": "checkpoint.json"})
	if err == nil {
		t.Fatal("history with current context passed")
	}
	record["principal_ref"] = record["drafter_principal_ref"]
	apTestPut(t, root, "approval.json", record)
	_, err = apTestRun(root, "approval", "approval.json", map[string]string{"checkpoint": "checkpoint.json"})
	apTestCode(t, err, "APPROVAL_CURRENT_INVALID")
}

func TestApprovalCurrentRejectsProgressionReferencesAndKeepsHistory(t *testing.T) {
	for _, field := range []string{"evidence_refs", "subject_ref", "review_task_ref", "continuation_ref", "basis", "review_bundle_basis"} {
		t.Run(field, func(t *testing.T) {
			root := apTestRoot(t)
			record, state, gate := apTestApproval(t, root)
			ref := "nested/Progression-Target.JSON"
			raw := apTestPut(t, root, ref, "intent")
			s := apTestSession(t, root)
			expected, err := approvalExpectationFromState(s, gate, state)
			if err != nil {
				t.Fatal(err)
			}
			if err = assertCurrentApprovalSemantic(s, record, expected); err != nil {
				t.Fatalf("original current approval invalid: %v", err)
			}
			switch field {
			case "evidence_refs":
				record[field] = append(apArray(record[field]), ref)
			case "basis", "review_bundle_basis":
				record[field] = append(apArray(record[field]), map[string]any{"ref": ref, "digest": safefs.Digest(raw)})
			default:
				record[field] = ref
			}
			if err = assertCurrentApprovalSemantic(s, record, expected); semanticCode(err) != "PROGRESSION_EVIDENCE" {
				t.Fatalf("current approval accepted target %s: %v", field, err)
			}
			if field == "evidence_refs" {
				apTestPut(t, root, "approval.json", record)
				if _, err = apTestRun(root, "approval", "approval.json", map[string]string{"checkpoint": "checkpoint.json"}); semanticCode(err) != "PROGRESSION_EVIDENCE" {
					t.Fatalf("public current consumer accepted target evidence: %v", err)
				}
				if _, err = apTestRun(root, "approval", "approval.json", map[string]string{"history": "true"}); err != nil {
					t.Fatalf("historical schema-only reader changed: %v", err)
				}
			}
		})
	}
}

func TestApprovalCurrentRejectsScopeBasisRoleAndVetoDrift(t *testing.T) {
	for _, name := range []string{"scope", "basis-bytes", "author", "role", "veto", "unlisted", "subject-bytes"} {
		t.Run(name, func(t *testing.T) {
			root := apTestRoot(t)
			record, _, _ := apTestApproval(t, root)
			switch name {
			case "scope":
				record["approval_scope"] = []any{"other"}
			case "basis-bytes":
				apTestPut(t, root, "basis.json", `{"version":"changed"}`)
			case "author":
				record["drafter_principal_ref"] = "agent.forged"
			case "role":
				record["role_id"] = "role.lifecycle-orchestrator"
			case "veto":
				record["biological_veto"] = true
			case "unlisted":
				record["gate_id"] = "check.unknown"
			case "subject-bytes":
				apTestPut(t, root, "subject.json", `{"gate_id":"other"}`)
			}
			apTestPut(t, root, "approval.json", record)
			_, err := apTestRun(root, "approval", "approval.json", map[string]string{"checkpoint": "checkpoint.json"})
			if err == nil {
				t.Fatal("unsafe current approval passed")
			}
			if old, out, ran := apTestLegacy(t, "verify-approval-record", root, "approval.json", "--checkpoint", filepath.Join(root, "checkpoint.json")); ran {
				if old {
					t.Fatalf("legacy unexpectedly passed unsafe approval: %s", out)
				}
			}
		})
	}
}

func TestApprovalCurrentFixedSourceOracle(t *testing.T) {
	root := apTestRoot(t)
	apTestApproval(t, root)
	_, err := apTestRun(root, "approval", "approval.json", map[string]string{"checkpoint": "checkpoint.json"})
	if err != nil {
		t.Fatal(err)
	}
	if old, out, ran := apTestLegacy(t, "verify-approval-record", root, "approval.json", "--checkpoint", filepath.Join(root, "checkpoint.json")); ran {
		if !old {
			t.Fatalf("legacy positive approval rejected: %s", out)
		}
		t.Log("fixed-source current v1 approval agrees")
	}
}

func TestApprovalV2CannotSelectItsOwnTask(t *testing.T) {
	root := apTestRoot(t)
	record, state, gate := apTestApproval(t, root)
	s := apTestSession(t, root)
	record["schema_version"] = 2
	record["review_task_ref"] = "forged-task.json"
	record["review_task_digest"] = strings.Repeat("a", 64)
	record["capability_ids"] = []any{"capability.architecture-domain"}
	expected, e := approvalExpectationFromState(s, gate, state)
	if e != nil {
		t.Fatal(e)
	}
	apTestCode(t, assertCurrentApprovalSemantic(s, record, expected), "APPROVAL_CONTEXT_REQUIRED")
}

func TestReviewBundleV1V2IdentityAndCurrentBinding(t *testing.T) {
	for _, version := range []int{1, 2} {
		for _, name := range []string{"current", "history", "mixed-principal", "duplicate-boundary", "task-digest-mismatch", "missing-basis"} {
			t.Run(fmt.Sprintf("v%d/%s", version, name), func(t *testing.T) {
				root := apTestRoot(t)
				var record map[string]any
				cp := "checkpoint.json"
				taskID := "synthetic.review"
				workUnit := "work-unit.technical-analysis"
				if version == 1 {
					record, _, _ = apTestApproval(t, root)
				} else {
					task, r, _, _ := apTestFormalReview(t, root)
					record = r
					taskID = apText(task["task_id"])
					workUnit = apText(task["work_unit_id"])
					cp = "review-checkpoint.json"
				}
				record["evidence_refs"] = []any{apText(apRows(record["basis"])[0]["ref"])}
				b := map[string]any{"schema_version": version, "kind": "review-bundle", "bundle_id": "review-bundle.synthetic", "task_id": taskID, "work_unit_id": workUnit, "review_session_id": "session.synthetic", "role_id": record["role_id"], "runtime_id": record["runtime_id"], "principal_ref": record["principal_ref"], "reviews": []any{record}}
				if version == 2 {
					for _, field := range []string{"review_task_ref", "review_task_digest", "capability_ids", "basis"} {
						b[field] = record[field]
					}
				}
				switch name {
				case "mixed-principal":
					record["principal_ref"] = "agent.other"
				case "duplicate-boundary":
					b["reviews"] = []any{record, record}
				case "task-digest-mismatch":
					if version == 2 {
						b["review_task_digest"] = strings.Repeat("f", 64)
					} else {
						record["principal_ref"] = "agent.other"
					}
				case "missing-basis":
					record["basis"] = []any{}
				}
				apTestPut(t, root, "bundle.json", b)
				extra := map[string]string{"checkpoint": cp}
				if name == "history" {
					extra = map[string]string{"history": "true"}
				}
				_, err := apTestRun(root, "approval", "bundle.json", extra)
				want := name == "current" || name == "history"
				if (err == nil) != want {
					t.Fatalf("bundle %s v%d: %v", name, version, err)
				}
			})
		}
	}
}

// Optional fixed-source oracle is for differential test evidence only.
// Normal native governance tests never require Node or invoke a legacy runtime.
// Scripts registered in oracleFixtureScripts replay recorded verdicts by default
// (see oracle_fixture_test.go); all others keep the original live-only behavior.
func apTestLegacy(t *testing.T, script, root, ref string, args ...string) (bool, string, bool) {
	t.Helper()
	fixture := oracleFixtureFor(script)
	if fixture == "" {
		return apTestLegacyLive(t, script, root, ref, args...)
	}
	argv := append([]string{"--root", root}, args...)
	argv = append(argv, filepath.Join(root, filepath.FromSlash(ref)))
	request := oracleRequest{Script: script, Args: oracleRelative(argv, root), Tree: oracleTreeDigest(t, root)}
	code, output := oracleVerdict(t, fixture, request, t.Name(), func() (int, string) {
		source := os.Getenv("YSS_LEGACY_ORACLE_ROOT")
		if source == "" {
			t.Fatal("live/record 模式需要 YSS_LEGACY_ORACLE_ROOT")
		}
		code, output := apOracleRun(t, source, script, root, ref, args...)
		return code, oracleNormalize(output, [2]string{root, "<root>"}, [2]string{source, "<oracle>"})
	})
	return code == 0, output, true
}

func apTestLegacyLive(t *testing.T, script, root, ref string, args ...string) (bool, string, bool) {
	t.Helper()
	source := os.Getenv("YSS_LEGACY_ORACLE_ROOT")
	if source == "" {
		return false, "", false
	}
	exitCode, output := apOracleRun(t, source, script, root, ref, args...)
	argv := append([]string{filepath.Join(source, "scripts", script), "--root", root}, args...)
	argv = append(argv, filepath.Join(root, filepath.FromSlash(ref)))
	record, err := json.Marshal(map[string]any{"test": t.Name(), "source": source, "argv": argv, "exit_code": exitCode, "raw_output": output})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("legacy-oracle-record %s", record)
	return exitCode == 0, output, true
}

func apOracleRun(t *testing.T, source, script, root, ref string, args ...string) (int, string) {
	t.Helper()
	if _, e := exec.LookPath("node"); e != nil {
		t.Fatal(e)
	}
	argv := []string{filepath.Join(source, "scripts", script), "--root", root}
	argv = append(argv, args...)
	argv = append(argv, filepath.Join(root, filepath.FromSlash(ref)))
	cmd := exec.Command("node", argv...)
	cmd.Dir = source
	output, e := cmd.CombinedOutput()
	return oracleExitCode(e), string(output)
}
