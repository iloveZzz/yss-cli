package lifecycle

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func daily2File(t *testing.T, root, ref string, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, ref), raw, 0644); err != nil {
		t.Fatal(err)
	}
	return "sha256:" + safefs.Digest(raw)
}

func daily2ReviewFixture(t *testing.T) *dailySession {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := kernel.NewSemanticSession(context.Background(), root, nil)
	s.GovernanceVersion = 2
	digest := strings.Repeat("a", 64)
	provenance := map[string]any{"tool": "local-command", "task_ref": "task.md", "execution_ref": "execution.json", "identity": map[string]any{"status": "unavailable", "reason": "tool does not expose session identity"}}
	daily2File(t, root, "task.md", map[string]any{"task_id": "daily"})
	actual := map[string]any{"tool": provenance["tool"], "task_ref": "task.md", "identity": provenance["identity"], "argv": []string{"true"}, "exit_code": json.Number("0"), "executed_at": "2026-01-01T00:00:00Z", "candidate_digest": digest, "scope": []string{"src"}, "result": "passed", "blocking_findings": []any{}}
	daily2File(t, root, "execution.json", actual)
	review := map[string]any{"result": "passed", "blocking_findings": []any{}, "candidate_digest": "sha256:" + digest, "assessment": map[string]any{"mode": "self-check", "provenance": provenance}}
	rd := daily2File(t, root, "review.json", review)
	review["ref"] = "review.json"
	review["digest"] = rd
	return &dailySession{s: s, repo: root, taskRef: "task.md", basis: map[string]string{}, report: &DailyReport{SemanticReport: s.Report, CandidateDigest: "sha256:" + digest}, record: map[string]any{"schema_version": json.Number("2"), "policy_version": json.Number("2"), "scope": map[string]any{"paths": []string{"src"}}, "implementation": map[string]any{"provenance": provenance}, "inputs": []any{}, "review": review}}
}

func TestDaily2TraceableSelfCheckWithoutActorRegistry(t *testing.T) {
	d := daily2ReviewFixture(t)
	if err := d.verifyReview(d.record["review"], ""); err != nil {
		t.Fatalf("ordinary traceable self-check rejected: %v", err)
	}
}

func TestDaily2StandardPolicyAndSameRequester(t *testing.T) {
	p := validStandardPolicy()
	p["version"] = json.Number("2")
	p["decision"].(map[string]any)["requester_may_confirm"] = true
	p["decision"].(map[string]any)["agent_may_generate_user_response"] = false
	d := standardSession(t, nil, map[string]any{"standard_decision": map[string]any{"decided_by": "human"}, "review": map[string]any{"reviewer_id": "human"}})
	d.s.GovernanceVersion = 2
	d.policy["standard"] = p
	d.readStandardPolicy()
	if d.standard == nil {
		t.Fatal("policy2 standard missing")
	}
	if err := d.standardIndependence("human"); err != nil {
		t.Fatalf("same real user must not be rejected by roles: %v", err)
	}
}

func TestDaily2APIFreezeDigestAloneCannotProveSequence(t *testing.T) {
	d := daily2ReviewFixture(t)
	d.report.APIContractDigest = d.report.CandidateDigest
	review := kernel.ApCopy(kernel.ApMap(d.record["review"]))
	review["api_digest"] = review["candidate_digest"]
	delete(review, "candidate_digest")
	review["digest"] = daily2File(t, d.repo, "api-review.json", review)
	review["ref"] = "api-review.json"
	argv := []any{"pnpm", "exec", "redocly", "lint", "api.yaml"}
	log := map[string]any{"argv": argv, "cwd": d.repo, "exit_code": json.Number("0"), "executed_at": "2026-01-01T00:00:00Z", "api_digest": d.report.APIContractDigest, "candidate_digest": d.report.CandidateDigest}
	ld := daily2File(t, d.repo, "lint.json", log)
	api := map[string]any{"tools": []any{map[string]any{"name": "@redocly/cli", "version": "2.0.8"}}, "candidate": map[string]any{"ref": "api.yaml"}, "lint": map[string]any{"tool": "@redocly/cli", "version": "2.0.8", "argv": argv, "exit_code": json.Number("0"), "api_digest": d.report.APIContractDigest, "log_ref": "lint.json", "log_digest": ld}, "compatibility": map[string]any{"tool": "yss-native-conservative", "api_digest": d.report.APIContractDigest}, "wire": map[string]any{"applicability": "not-applicable", "reason": "not a wrapper"}, "review": review, "freeze": map[string]any{"digest": d.report.APIContractDigest}, "contract_tests": []any{map[string]any{"command_index": json.Number("0"), "exit_code": json.Number("0"), "candidate_digest": d.report.CandidateDigest, "log_ref": "lint.json", "log_digest": ld}}}
	d.record["commands"] = []any{map[string]any{"argv": argv, "cwd": "."}}
	if err := d.verifyAPIEvidence(api, ""); kernel.SemanticCode(err) != "DAILY_EVIDENCE" {
		t.Fatal("digest-only freeze accepted without original Draft/review/Freeze/implementation chronology")
	}
}

func TestDaily2RequiredReviewFromOriginalTicketSection(t *testing.T) {
	d := daily2ReviewFixture(t)
	d.s.GovernancePolicy = map[string]any{"user_decision_policy": map[string]any{"source_kinds": []string{"session-export"}}}
	sd := daily2File(t, d.repo, "human.json", map[string]any{"source_kind": "session-export", "messages": []any{map[string]any{"id": "m", "actor_kind": "biological-human", "principal_ref": "human", "original_ref": "test-only://message", "sent_at": "2026-01-01T00:00:00Z", "text": "请独立复核 src/file.go"}}})
	requirement := map[string]any{"kind": "review-requirement", "mode": "independent-review", "scope": []string{"src/file.go"}, "source": map[string]any{"kind": "session-export", "ref": "human.json", "digest": sd, "message_id": "m"}}
	raw, _ := json.Marshal(requirement)
	section := "```json\n" + string(raw) + "\n```\n"
	d.body = "## Required review\n" + section
	d.s.EvidenceFragment = d.recordRef
	d.basis["#Required review"] = "sha256:" + safefs.Digest([]byte(section))
	if err := d.verifyReview(d.record["review"], ""); kernel.SemanticCode(err) != "EVIDENCE_REVIEW" {
		t.Fatalf("bound original child-path requirement must reject self-check: %v", err)
	}
}

func TestDaily2ReviewRootCannotBeGuessed(t *testing.T) {
	d := daily2ReviewFixture(t)
	implementation := kernel.ApMap(kernel.ApMap(d.record["implementation"])["provenance"])
	for _, root := range []string{"", "relative", filepath.Join(d.repo, "unknown")} {
		if _, err := d.s.RequiredReviewModeAtRoot(nil, implementation, []string{"src"}, root); err == nil {
			t.Fatalf("accepted unverified root %q", root)
		}
	}
	if _, err := d.s.RequiredReviewModeAtRoot(nil, nil, []string{"src"}, d.repo); err == nil {
		t.Fatal("accepted missing original task source")
	}
}

func TestDaily2DoesNotChangeLegacyReviewRequirement(t *testing.T) {
	d := daily2ReviewFixture(t)
	d.s.GovernanceVersion = 1
	if err := d.verifyReview(d.record["review"], "implementer"); err == nil {
		t.Fatal("legacy independent-review requirement accepted new self-check")
	}
	if err := d.recordVersion(); kernel.SemanticCode(err) != "DAILY_VERSION" {
		t.Fatalf("legacy policy accepted record2: %v", err)
	}
}

func TestDaily2RejectsNotRunProvenance(t *testing.T) {
	for _, kind := range []string{"implementation", "review"} {
		t.Run(kind, func(t *testing.T) {
			d := daily2ReviewFixture(t)
			raw, err := os.ReadFile(filepath.Join(d.repo, "execution.json"))
			if err != nil {
				t.Fatal(err)
			}
			var log map[string]any
			if err := json.Unmarshal(raw, &log); err != nil {
				t.Fatal(err)
			}
			log["status"] = "not-run"
			daily2File(t, d.repo, "execution.json", log)
			if kind == "implementation" {
				err = d.verifyImplementation()
			} else {
				err = d.verifyReview(d.record["review"], "")
			}
			if err == nil {
				t.Fatal("not-run original provenance accepted as passed")
			}
		})
	}
}
