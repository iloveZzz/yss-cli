package approval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

// Business facts and user messages are synthetic, using the same real source
// protocol as the decision tests; no product approval is created by these tests.
func stageResultFixture(t *testing.T) (*kernel.SemanticSession, map[string]any) {
	s, d, _ := trueDecisionFixture(t)
	item := kernel.ApRows(kernel.ApMap(d["request"])["items"])[0]
	item["boundary"] = "gate.plan-approved"
	request := kernel.ApMap(d["request"])
	source := map[string]any{}
	sourceRaw, _ := os.ReadFile(filepath.Join(s.Root, "reply-source.json"))
	if err := json.Unmarshal(sourceRaw, &source); err != nil {
		t.Fatal(err)
	}
	kernel.ApRows(source["messages"])[0]["text"] = renderDecisionRequestSemantic(request)
	decisionFile(t, s.Root, "reply-source.json", source)
	raw, _ := os.ReadFile(filepath.Join(s.Root, "reply-source.json"))
	kernel.ApMap(request["presented_source"])["digest"] = kernel.ApDigest(raw)
	answer := kernel.ApRows(d["responses"])[0]
	kernel.ApMap(answer["source"])["digest"] = kernel.ApDigest(raw)
	answer["request_digest"] = decisionRequestDigest(request)
	decisionFile(t, s.Root, "decision.json", d)
	s.Report.Profile = "spec"
	p := kernel.ApMap(s.GovernancePolicy["user_decision_policy"])
	p["profile_scope"] = map[string]any{"spec": map[string]any{"gates": []string{"gate.plan-approved"}, "work_units": map[string]any{}}}
	s.GovernancePolicy["stage_gates"] = map[string]any{"schema_version": 1, "capability": kernel.StageGatesCapability, "gates": []any{map[string]any{"id": "gate.plan-approved", "checks": []string{"check.fixture"}}}}
	decisionFile(t, s.Root, ".template-spec/process/schemas/stage-result.schema.json", map[string]any{"type": "object"})
	asset := kernel.ApMap(item["subject"])
	log := map[string]any{"phase": "execution", "argv": []string{"node", "check-plan"}, "exit_code": 0, "executed_at": "2026-01-01T00:00:00Z", "input_bindings": []any{asset}, "check_ids": []string{"check.fixture"}, "result": "passed", "blocking_findings": []any{}}
	decisionFile(t, s.Root, "check.json", log)
	raw, _ = os.ReadFile(filepath.Join(s.Root, "check.json"))
	proof := map[string]any{"ref": "check.json", "digest": kernel.ApDigest(raw)}
	raw, _ = os.ReadFile(filepath.Join(s.Root, "decision.json"))
	record := map[string]any{"schema_version": 1, "kind": "stage-result", "feature_id": "TICKET-1", "gate_id": "gate.plan-approved", "scope": []string{"TICKET-1"}, "assets": []any{asset}, "checks": []any{map[string]any{"id": "check.fixture", "result": "passed", "inputs": []any{asset}, "evidence": []any{proof}}}, "outcome": "approved", "blocking_findings": []any{}, "provenance": map[string]any{"tool": "node", "execution_ref": "check.json"}, "authorization": map[string]any{"kind": "user-decision", "ref": "decision.json", "digest": kernel.ApDigest(raw)}}
	decisionFile(t, s.Root, "stage.json", record)
	s.V = kernel.NewView(s.Root)
	return s, record
}

func TestStageResultUsesOneDecision(t *testing.T) {
	s, _ := stageResultFixture(t)
	if err := VerifyStageResult(s, "stage.json", nil); err != nil {
		t.Fatal(err)
	}
	if kernel.ApMap(s.Report.Coverage["stage_result"])["status"] != "approved" {
		t.Fatal("stage was not approved")
	}
}

func TestStageResultRejectsStaleOrExpandedEvidence(t *testing.T) {
	for _, scenario := range []string{"stale-asset", "stale-proof", "changed-scope", "failed-check", "missing-check", "unreviewed-asset", "fake-execution", "child-approval", "missing-authorization", "wrong-feature"} {
		t.Run(scenario, func(t *testing.T) {
			s, r := stageResultFixture(t)
			switch scenario {
			case "stale-asset":
				decisionFile(t, s.Root, "subject.json", map[string]any{"changed": true})
			case "stale-proof":
				decisionFile(t, s.Root, "check.json", map[string]any{"changed": true})
			case "wrong-feature":
				r["feature_id"] = "OTHER-2"
			case "changed-scope":
				r["scope"] = []string{"other"}
			case "failed-check":
				kernel.ApRows(r["checks"])[0]["result"] = "failed"
			case "missing-check":
				r["checks"] = []any{}
			case "unreviewed-asset":
				decisionFile(t, s.Root, "extra.json", map[string]any{"extra": true})
				raw, _ := os.ReadFile(filepath.Join(s.Root, "extra.json"))
				r["assets"] = append(kernel.ApArray(r["assets"]), map[string]any{"ref": "extra.json", "digest": kernel.ApDigest(raw)})
			case "fake-execution":
				l := map[string]any{}
				raw, _ := os.ReadFile(filepath.Join(s.Root, "check.json"))
				if err := json.Unmarshal(raw, &l); err != nil {
					t.Fatal(err)
				}
				l["exit_code"] = 1
				decisionFile(t, s.Root, "check.json", l)
				raw, _ = os.ReadFile(filepath.Join(s.Root, "check.json"))
				kernel.ApRows(kernel.ApRows(r["checks"])[0]["evidence"])[0]["digest"] = kernel.ApDigest(raw)
			case "child-approval":
				kernel.ApRows(r["checks"])[0]["approval_ref"] = "fake.json"
			case "missing-authorization":
				delete(r, "authorization")
			}
			decisionFile(t, s.Root, "stage.json", r)
			s.V = kernel.NewView(s.Root)
			if err := VerifyStageResult(s, "stage.json", nil); err == nil {
				t.Fatal("invalid stage accepted: " + scenario)
			}
		})
	}
}

func TestStageResultRejectsSelfBinding(t *testing.T) {
	s, r := stageResultFixture(t)
	r["assets"] = []any{map[string]any{"ref": "stage.json", "digest": "bad"}}
	decisionFile(t, s.Root, "stage.json", r)
	if err := VerifyStageResult(s, "stage.json", nil); err == nil {
		t.Fatal("self binding accepted")
	}
}

func TestStageResultRechecksOnlyAffectedInputs(t *testing.T) {
	s, r := stageResultFixture(t)
	// Changing one proof invalidates its consumer, with no new human decision.
	decisionFile(t, s.Root, "check.json", map[string]any{"changed": true})
	s.V = kernel.NewView(s.Root)
	if err := VerifyStageResult(s, "stage.json", nil); err == nil {
		t.Fatal("stale proof accepted")
	}
	coverage := kernel.ApMap(s.Report.Coverage["stage_result"])
	if coverage["status"] != "stale" || !kernel.ApSetEqual(coverage["affected_checks"], []string{"check.fixture"}) {
		t.Fatalf("unexpected impact: %#v", coverage)
	}
	if kernel.ApMap(r["authorization"])["kind"] != "user-decision" {
		t.Fatal("authorization was rewritten")
	}
}

func TestStageQualityDoesNotRequireChildApproval(t *testing.T) {
	s, r := stageResultFixture(t)
	subject := kernel.Text(kernel.ApRows(r["assets"])[0]["ref"])
	if err := VerifyStageQuality(s, "check.json", "check.fixture", subject); err != nil {
		t.Fatal(err)
	}
	log, err := s.Doc("check.json")
	if err != nil {
		t.Fatal(err)
	}
	log["approval_ref"] = "invented.json"
	decisionFile(t, s.Root, "check.json", log)
	s.V = kernel.NewView(s.Root)
	if err := VerifyStageQuality(s, "check.json", "check.fixture", subject); err == nil {
		t.Fatal("child approval accepted")
	}
	delete(log, "approval_ref")
	log["input_bindings"] = []any{}
	decisionFile(t, s.Root, "check.json", log)
	s.V = kernel.NewView(s.Root)
	if err := VerifyStageQuality(s, "check.json", "check.fixture", subject); err == nil {
		t.Fatal("unbound quality accepted")
	}
}

func TestStageResultRejectsUnrelatedCheckInputs(t *testing.T) {
	s, r := stageResultFixture(t)
	decisionFile(t, s.Root, "unrelated.json", map[string]any{"unrelated": true})
	raw, _ := os.ReadFile(filepath.Join(s.Root, "unrelated.json"))
	binding := map[string]any{"ref": "unrelated.json", "digest": kernel.ApDigest(raw)}
	check := kernel.ApRows(r["checks"])[0]
	check["inputs"] = []any{binding}
	log := map[string]any{}
	raw, _ = os.ReadFile(filepath.Join(s.Root, "check.json"))
	json.Unmarshal(raw, &log)
	log["input_bindings"] = []any{binding}
	decisionFile(t, s.Root, "check.json", log)
	raw, _ = os.ReadFile(filepath.Join(s.Root, "check.json"))
	kernel.ApRows(check["evidence"])[0]["digest"] = kernel.ApDigest(raw)
	decisionFile(t, s.Root, "stage.json", r)
	s.V = kernel.NewView(s.Root)
	if err := VerifyStageResult(s, "stage.json", nil); err == nil || kernel.SemanticCode(err) != "STAGE_CHECK_SCOPE" {
		t.Fatalf("unrelated evidence accepted: %v", err)
	}
}

func TestStageResultFindsBoundReviewRequirementWithoutOptionalProvenance(t *testing.T) {
	s, r := stageResultFixture(t)
	subject := kernel.Text(kernel.ApRows(r["assets"])[0]["ref"])
	requirement := capturedRequirement(t, s, "请独立复核 "+subject, []string{subject})
	check := kernel.ApRows(r["checks"])[0]
	check["inputs"] = append(kernel.ApArray(check["inputs"]), requirement)
	log := map[string]any{}
	raw, _ := os.ReadFile(filepath.Join(s.Root, "check.json"))
	json.Unmarshal(raw, &log)
	log["input_bindings"] = check["inputs"]
	decisionFile(t, s.Root, "check.json", log)
	raw, _ = os.ReadFile(filepath.Join(s.Root, "check.json"))
	kernel.ApRows(check["evidence"])[0]["digest"] = kernel.ApDigest(raw)
	decisionFile(t, s.Root, "stage.json", r)
	s.V = kernel.NewView(s.Root)
	if err := VerifyStageResult(s, "stage.json", nil); err == nil || kernel.SemanticCode(err) != "STAGE_REVIEW" {
		t.Fatalf("known independent review omitted: %v", err)
	}
}
