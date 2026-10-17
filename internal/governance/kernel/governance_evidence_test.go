package kernel

import (
	"context"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"path/filepath"
	"testing"
)

func evidenceFixture(t *testing.T) (*SemanticSession, map[string]any, map[string]any) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "candidate.txt"), []byte("frozen candidate"), 0644); err != nil {
		t.Fatal(err)
	}
	candidate := map[string]any{"ref": "candidate.txt", "digest": safefs.Digest([]byte("frozen candidate"))}
	writePolicyFixture(t, root, "task.json", map[string]any{"task_id": "TASK-1"})
	provenance := map[string]any{"tool": "shell", "task_ref": "task.json", "execution_ref": "execution.json", "identity": map[string]any{"status": "unavailable", "reason": "shell has no stable session id"}}
	execution := map[string]any{"tool": "shell", "task_ref": "task.json", "identity": provenance["identity"], "command": "go test ./...", "exit_code": 0, "executed_at": "2026-01-01T00:00:00Z", "candidate_digest": candidate["digest"], "scope": []string{"src/"}, "result": "passed", "blocking_findings": []any{}, "readonly": true}
	writePolicyFixture(t, root, "execution.json", execution)
	return NewSemanticSession(context.Background(), root, nil), candidate, provenance
}

func TestTraceableSelfCheckWithoutStableIdentity(t *testing.T) {
	s, c, p := evidenceFixture(t)
	review := map[string]any{"mode": "self-check", "provenance": p}
	if err := s.ValidateReviewEvidence(review, c, []string{"src/"}, "self-check", p, "task.json"); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateReviewEvidence(review, c, []string{"src/"}, "independent-review", p, "task.json"); err == nil {
		t.Fatal("self-check replaced mandatory independent review")
	}
	review["independence"] = map[string]any{}
	if err := s.ValidateReviewEvidence(review, c, []string{"src/"}, "self-check", p, "task.json"); err == nil {
		t.Fatal("self-check carried fake independence")
	}
}

func TestVerificationUsesOriginalCommandAndCandidate(t *testing.T) {
	for _, scenario := range []string{"passed", "failed", "not-run", "different-command", "stale-original", "empty-log", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			s, c, _ := evidenceFixture(t)
			raw, err := os.ReadFile(filepath.Join(s.Root, "execution.json"))
			if err != nil {
				t.Fatal(err)
			}
			result := map[string]any{"command": "go test ./...", "status": "passed", "exit_code": 0, "executed_at": "2026-01-01T00:00:00Z", "candidate_digest": c["digest"], "evidence_ref": "execution.json", "evidence_digest": safefs.Digest(raw)}
			switch scenario {
			case "failed":
				result["status"] = "failed"
				result["exit_code"] = 1
			case "not-run":
				result["status"] = "not-run"
				delete(result, "exit_code")
				result["reason"] = "not run"
			case "different-command":
				result["command"] = "go vet ./..."
			case "stale-original":
				c["digest"] = safefs.Digest([]byte("changed"))
				os.WriteFile(filepath.Join(s.Root, "candidate.txt"), []byte("changed"), 0644)
				result["candidate_digest"] = c["digest"]
			case "empty-log":
				os.WriteFile(filepath.Join(s.Root, "execution.json"), []byte("{}"), 0644)
				result["evidence_digest"] = safefs.Digest([]byte("{}"))
			case "symlink":
				if err = os.Symlink(filepath.Join(s.Root, "execution.json"), filepath.Join(s.Root, "alias.json")); err != nil {
					t.Fatal(err)
				}
				result["evidence_ref"] = "alias.json"
			}
			err = s.ValidateVerificationResults([]any{result}, []string{"go test ./..."}, c)
			if (err == nil) != (scenario == "passed") {
				t.Fatalf("%s: %v", scenario, err)
			}
		})
	}
}

func independentFixture(t *testing.T) (*SemanticSession, map[string]any, map[string]any) {
	s, c, implementation := evidenceFixture(t)
	implementation["identity"] = map[string]any{"status": "available", "principal_ref": "same-account", "session_ref": "implementation-session"}
	actual, _ := s.EvidenceFact("execution.json")
	actual["identity"] = implementation["identity"]
	writePolicyFixture(t, s.Root, "execution.json", actual)
	reviewer := ApCopy(implementation)
	reviewer["execution_ref"] = "review.json"
	reviewer["task_ref"] = "review-task.json"
	writePolicyFixture(t, s.Root, "review-task.json", map[string]any{"task_id": "REVIEW-1"})
	actual["task_ref"] = reviewer["task_ref"]
	reviewer["identity"] = map[string]any{"status": "available", "principal_ref": "same-account", "session_ref": "review-session"}
	actual["identity"] = reviewer["identity"]
	writePolicyFixture(t, s.Root, "review.json", actual)
	raw, err := os.ReadFile(filepath.Join(s.Root, "review.json"))
	if err != nil {
		t.Fatal(err)
	}
	for ref, p := range map[string]map[string]any{"implementation-context.json": implementation, "review-context.json": reviewer} {
		writePolicyFixture(t, s.Root, ref, map[string]any{"source_kind": "session-export", "session_ref": ApMap(p["identity"])["session_ref"], "messages": []any{map[string]any{"id": "message-1", "original_ref": Text(ApMap(p["identity"])["session_ref"]) + "/message-1", "principal_ref": "same-account", "sent_at": "2026-01-01T00:00:00Z", "text": "executed " + Text(p["execution_ref"]) + " for " + Text(c["digest"])}}})
	}
	ind := map[string]any{"implementation": implementation, "reviewer": reviewer, "implementation_context_ref": "implementation-context.json", "reviewer_context_ref": "review-context.json", "evidence": map[string]any{"ref": "review.json", "digest": safefs.Digest(raw)}, "candidate": c, "scope": []string{"src/"}, "readonly": true}
	return NewSemanticSession(context.Background(), s.Root, nil), c, map[string]any{"mode": "independent-review", "provenance": reviewer, "independence": ind}
}

func TestIndependentReviewRequiresActualSeparateContexts(t *testing.T) {
	for _, scenario := range []string{"same-account-separate-contexts", "same-context", "same-original-source", "missing-trace", "open-finding", "candidate-tampered", "source-mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			s, c, r := independentFixture(t)
			ind := ApMap(r["independence"])
			trusted := ApCopy(ApMap(ind["implementation"]))
			switch scenario {
			case "same-context":
				ApMap(ApMap(ind["reviewer"])["identity"])["session_ref"] = "implementation-session"
			case "same-original-source":
				capture, _ := s.EvidenceFact("review-context.json")
				ApRows(capture["messages"])[0]["original_ref"] = "implementation-session/message-1"
				writePolicyFixture(t, s.Root, "review-context.json", capture)
				s = NewSemanticSession(context.Background(), s.Root, nil)
			case "missing-trace":
				writePolicyFixture(t, s.Root, "review-context.json", map[string]any{"source_kind": "session-export", "session_ref": "review-session", "messages": []any{}})
			case "open-finding":
				actual, _ := s.EvidenceFact("review.json")
				actual["blocking_findings"] = []string{"P1"}
				writePolicyFixture(t, s.Root, "review.json", actual)
				s = NewSemanticSession(context.Background(), s.Root, nil)
			case "candidate-tampered":
				os.WriteFile(filepath.Join(s.Root, "candidate.txt"), []byte("tampered"), 0644)
			case "source-mismatch":
				ApMap(r["provenance"])["tool"] = "made-up-tool"
			}
			err := s.ValidateReviewEvidence(r, c, []string{"src/"}, "independent-review", trusted, "task.json")
			if (err == nil) != (scenario == "same-account-separate-contexts") {
				t.Fatalf("%s: %v", scenario, err)
			}
		})
	}
}

func TestExternalRequirementsUseCurrentSourceAndScope(t *testing.T) {
	for _, scenario := range []string{"none-known", "known-unverifiable", "satisfied", "no-expiry", "wrong-candidate", "revoked", "expired", "unknown-rule", "different-accounts-required", "distinct-real-principals"} {
		t.Run(scenario, func(t *testing.T) {
			s, c, _ := evidenceFixture(t)
			rule := map[string]any{"source": "platform:review/1", "scope": []string{"src/"}, "required_principals": []string{"reviewer"}}
			if scenario == "unknown-rule" {
				rule["special-organization-rule"] = true
			}
			if scenario == "different-accounts-required" || scenario == "distinct-real-principals" {
				rule["distinct_principals"] = true
			}
			writePolicyFixture(t, s.Root, "rule.json", rule)
			raw, _ := os.ReadFile(filepath.Join(s.Root, "rule.json"))
			ruleAsset := map[string]any{"ref": "rule.json", "digest": safefs.Digest(raw)}
			result := map[string]any{"source": "platform:review/1", "scope": []string{"src/"}, "rule": ruleAsset, "subject": c, "status": "satisfied", "revoked": false, "verified_at": "2026-01-01T00:00:00Z", "expires_at": "2099-01-01T00:00:00Z", "original_ref": "platform:review/1/result", "principals": []string{"reviewer"}, "implementation_principal_ref": "reviewer"}
			var implementation map[string]any
			if scenario == "distinct-real-principals" {
				implementation = map[string]any{"identity": map[string]any{"status": "available", "principal_ref": "actual-author"}}
				result["implementation_principal_ref"] = "actual-author"
			}
			if scenario == "no-expiry" {
				delete(result, "expires_at")
			}
			if scenario == "revoked" {
				result["revoked"] = true
			}
			if scenario == "expired" {
				result["expires_at"] = "2020-01-01T00:00:00Z"
			}
			if scenario == "wrong-candidate" {
				result["subject"] = map[string]any{"ref": "other.txt", "digest": c["digest"]}
			}
			writePolicyFixture(t, s.Root, "result.json", result)
			raw, _ = os.ReadFile(filepath.Join(s.Root, "result.json"))
			requirement := map[string]any{"source": "platform:review/1", "scope": []string{"src/"}, "rule": ruleAsset, "result": map[string]any{"ref": "result.json", "digest": safefs.Digest(raw)}, "status": "satisfied"}
			rules := map[string]any{"state": "known-verifiable", "requirements": []any{requirement}}
			if scenario == "none-known" {
				rules = map[string]any{"state": "none-known"}
			}
			if scenario == "known-unverifiable" {
				rules = map[string]any{"state": "known-unverifiable"}
			}
			err := s.ValidateExternalRules(rules, c, []string{"src/"}, implementation)
			if (err == nil) != (scenario == "none-known" || scenario == "satisfied" || scenario == "no-expiry" || scenario == "distinct-real-principals") {
				t.Fatalf("%s: %v", scenario, err)
			}
		})
	}
}

func TestIndependentReviewCannotBorrowImplementationSession(t *testing.T) {
	s, c, r := independentFixture(t)
	ind := ApMap(r["independence"])
	trusted := ApCopy(ApMap(ind["implementation"]))
	// A really implemented and self-checked. B's separate real log must not replace A.
	ind["implementation"], ind["reviewer"] = ind["reviewer"], ind["implementation"]
	ind["implementation_context_ref"], ind["reviewer_context_ref"] = ind["reviewer_context_ref"], ind["implementation_context_ref"]
	r["provenance"] = ind["reviewer"]
	raw, err := os.ReadFile(filepath.Join(s.Root, "execution.json"))
	if err != nil {
		t.Fatal(err)
	}
	ind["evidence"] = map[string]any{"ref": "execution.json", "digest": safefs.Digest(raw)}
	if err = s.ValidateReviewEvidence(r, c, []string{"src/"}, "independent-review", trusted, "task.json"); err == nil {
		t.Fatal("actual implementer self-check accepted as independent by swapping real sessions")
	}
}

func TestReviewRequiresTrustedImplementationTask(t *testing.T) {
	for _, scenario := range []string{"missing-task", "wrong-task", "missing-provenance", "different-execution"} {
		t.Run(scenario, func(t *testing.T) {
			s, c, r := independentFixture(t)
			trusted := ApCopy(ApMap(ApMap(r["independence"])["implementation"]))
			task := "task.json"
			switch scenario {
			case "missing-task":
				task = ""
			case "wrong-task":
				task = "review-task.json"
			case "missing-provenance":
				trusted = nil
			case "different-execution":
				trusted["execution_ref"] = "review.json"
			}
			if err := s.ValidateReviewEvidence(r, c, []string{"src/"}, "independent-review", trusted, task); err == nil {
				t.Fatal("missing or mismatched trusted implementation accepted")
			}
		})
	}
}

func TestDerivedCandidateKeepsFileBoundary(t *testing.T) {
	s, c, p := evidenceFixture(t)
	c["digest"] = safefs.Digest([]byte("native computed source inventory"))
	raw, err := s.EvidenceFact("execution.json")
	if err != nil {
		t.Fatal(err)
	}
	raw["candidate_digest"] = c["digest"]
	writePolicyFixture(t, s.Root, "execution.json", raw)
	s = NewSemanticSession(context.Background(), s.Root, nil)
	review := map[string]any{"mode": "self-check", "provenance": p}
	if err = s.ValidateReviewEvidenceDigest(review, c, []string{"src/"}, "self-check", p, "task.json", c); err != nil {
		t.Fatal(err)
	}
	if err = s.ValidateReviewEvidence(review, c, []string{"src/"}, "self-check", p, "task.json"); err == nil {
		t.Fatal("file API accepted derived digest without checking bytes")
	}
	bytes, err := os.ReadFile(filepath.Join(s.Root, "execution.json"))
	if err != nil {
		t.Fatal(err)
	}
	row := map[string]any{"command": raw["command"], "status": "passed", "exit_code": 0, "executed_at": raw["executed_at"], "candidate_digest": c["digest"], "evidence_ref": "execution.json", "evidence_digest": safefs.Digest(bytes)}
	if err = s.ValidateVerificationResultsDigest([]any{row}, []string{"go test ./..."}, Text(c["digest"])); err != nil {
		t.Fatal(err)
	}
	if err = s.ValidateVerificationResults([]any{row}, []string{"go test ./..."}, c); err == nil {
		t.Fatal("file result API skipped actual bytes")
	}
}

func TestExternalReviewCannotChooseImplementationPrincipal(t *testing.T) {
	s, c, p := evidenceFixture(t)
	p["identity"] = map[string]any{"status": "available", "principal_ref": "actual-author", "session_ref": "actual-session"}
	actual, _ := s.EvidenceFact("execution.json")
	actual["identity"] = p["identity"]
	writePolicyFixture(t, s.Root, "execution.json", actual)
	rule := map[string]any{"source": "platform:rule", "scope": []string{"src/"}, "distinct_principals": true}
	writePolicyFixture(t, s.Root, "rule.json", rule)
	raw, _ := os.ReadFile(filepath.Join(s.Root, "rule.json"))
	rb := map[string]any{"ref": "rule.json", "digest": safefs.Digest(raw)}
	result := map[string]any{"source": "platform:rule", "scope": []string{"src/"}, "rule": rb, "subject": c, "status": "satisfied", "revoked": false, "verified_at": "2026-01-01T00:00:00Z", "expires_at": "2099-01-01T00:00:00Z", "original_ref": "platform:result", "principals": []string{"actual-author"}, "implementation_principal_ref": "invented-author"}
	writePolicyFixture(t, s.Root, "result.json", result)
	raw, _ = os.ReadFile(filepath.Join(s.Root, "result.json"))
	external := map[string]any{"source": "platform:rule", "scope": []string{"src/"}, "rule": rb, "result": map[string]any{"ref": "result.json", "digest": safefs.Digest(raw)}, "subject": c, "status": "satisfied"}
	review := map[string]any{"mode": "external-required-review", "provenance": p, "external": external}
	s = NewSemanticSession(context.Background(), s.Root, nil)
	if err := s.ValidateReviewEvidence(review, c, []string{"src/"}, "external-required-review", p, "task.json"); err == nil {
		t.Fatal("captured result chose a fake implementation principal")
	}
}

func TestReviewMemberCandidateKeepsImplementationComposition(t *testing.T) {
	s, c, r := independentFixture(t)
	p := ApMap(ApMap(r["independence"])["implementation"])
	composite := map[string]any{"ref": "task.json", "digest": safefs.Digest([]byte("all outputs plus frozen baseline"))}
	log, _ := s.EvidenceFact("execution.json")
	log["candidate_digest"] = composite["digest"]
	writePolicyFixture(t, s.Root, "execution.json", log)
	capture, _ := s.EvidenceFact("implementation-context.json")
	ApRows(capture["messages"])[0]["text"] = "executed execution.json for " + Text(composite["digest"])
	writePolicyFixture(t, s.Root, "implementation-context.json", capture)
	s = NewSemanticSession(context.Background(), s.Root, nil)
	if err := s.ValidateReviewEvidenceDigest(r, c, []string{"src/"}, "independent-review", p, "task.json", composite); err != nil {
		t.Fatal(err)
	}
}
