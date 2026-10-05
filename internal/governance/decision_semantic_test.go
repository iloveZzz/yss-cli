package governance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type apDecisionFixture struct {
	root, prefix, ref   string
	record, requirement map[string]any
}

func (f *apDecisionFixture) capture(t *testing.T, name, principal, actor, time, text, replyTo string) map[string]any {
	t.Helper()
	ref := f.prefix + name + ".json"
	msg := map[string]any{"id": name, "principal_ref": principal, "actor_kind": actor, "sent_at": time, "text": text, "original_ref": "test-only://" + f.prefix + name}
	if replyTo != "" {
		msg["reply_to"] = replyTo
	}
	b := apTestPut(t, f.root, ref, map[string]any{"source_kind": "session-export", "messages": []any{msg}})
	return map[string]any{"kind": "session-export", "ref": ref, "digest": apDigest(b), "message_id": name}
}
func (f *apDecisionFixture) present(t *testing.T) {
	request := apMap(f.record["request"])
	request["presented_source"] = f.capture(t, "presentation", "agent.orchestrator", "digital-human", "2026-09-05T01:02:00Z", renderDecisionRequestSemantic(request), "")
}
func (f *apDecisionFixture) respond(t *testing.T, text, decision, selection, time string, ids, scope []string) {
	if scope == nil {
		scope = []string{}
	}
	rows := apArray(f.record["responses"])
	name := fmt.Sprintf("reply-%d", len(rows))
	request := apMap(f.record["request"])
	source := f.capture(t, name, "person.requester", "biological-human", time, text, "test-only://"+f.prefix+"presentation")
	f.record["responses"] = append(rows, map[string]any{"source": source, "principal_ref": "person.requester", "responded_at": time, "text": text, "request_digest": decisionRequestDigest(request), "selection": selection, "item_ids": ids, "decision": decision, "approved_scope": scope})
}
func (f *apDecisionFixture) save(t *testing.T) { apTestPut(t, f.root, f.ref, f.record) }
func apTestDecision(t *testing.T, root, prefix, boundary, subject string, scope []string) *apDecisionFixture {
	t.Helper()
	if subject == "" {
		subject = prefix + "subject.txt"
		apTestPut(t, root, subject, "合成测试主体 v1\n")
	}
	raw, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(subject)))
	if e != nil {
		t.Fatal(e)
	}
	f := &apDecisionFixture{root: root, prefix: prefix, ref: prefix + "decision.json"}
	item := map[string]any{"id": "decision.demo", "boundary": boundary, "title": "确认合成测试范围", "subject": map[string]any{"ref": subject, "version": "v1", "digest": apDigest(raw)}, "basis": []any{}, "scope": scope, "changes": "增加合成测试范围", "risks": []any{}, "recommendation": "批准已展示范围", "next_actions": []any{"进入下一工作单元"}, "responder_ref": "person.requester"}
	f.record = map[string]any{"schema_version": 1, "kind": "user-decision", "request": map[string]any{"id": "request.demo", "requester_ref": "person.requester", "requester_source": f.capture(t, "requester", "person.requester", "biological-human", "2026-09-05T01:00:00Z", "请整理合成测试方案。", ""), "items": []any{item}}, "responses": []any{}}
	f.present(t)
	f.respond(t, "同意", "approved", "single", "2026-09-05T01:03:00Z", []string{"decision.demo"}, scope)
	f.save(t)
	f.requirement = map[string]any{"boundary": boundary, "subject_ref": subject, "scope": scope, "user_decision_ref": f.ref}
	return f
}
func apTestMutateMessage(t *testing.T, f *apDecisionFixture, source map[string]any, mutate func(map[string]any)) {
	t.Helper()
	s := newSemanticSession(context.Background(), f.root, nil)
	capture, e := s.doc(apText(source["ref"]))
	if e != nil {
		t.Fatal(e)
	}
	mutate(apRows(capture["messages"])[0])
	raw := apTestPut(t, f.root, apText(source["ref"]), capture)
	source["digest"] = apDigest(raw)
}

func TestUserDecisionFourProfilesCurrentAndRefusal(t *testing.T) {
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		t.Run(profile, func(t *testing.T) {
			root := apTestProfileRoot(t, profile)
			s := apTestSession(t, root)
			gate := ""
			for _, candidate := range apStrings(apMap(s.roles["user_decision_policy"])["gates"]) {
				if apFind(s.registry["gates"], "id", candidate) != nil {
					gate = candidate
					break
				}
			}
			if gate == "" {
				if profile != "frontend" || len(apArray(apMap(s.roles["user_decision_policy"])["gates"])) != 0 {
					t.Fatal("profile has no current published biological decision boundary")
				}
				// The frontend distribution explicitly has no automatic product
				// user-decision gates. This sample only reads original evidence
				// when a consumer explicitly supplies the current requirement.
				for _, candidate := range apStrings(apMap(s.roles["gate_policy"])["biological_human"]) {
					if apFind(s.registry["gates"], "id", candidate) != nil {
						gate = candidate
						break
					}
				}
				t.Log("not-applicable: frontend has no automatic user-decision gates; explicit original-evidence consumer only, no lifecycle authorization")
				if gate == "" {
					t.Fatal("profile has no independent explicit biological evidence boundary")
				}
			}
			f := apTestDecision(t, root, "", gate, "", []string{"unit.profile-decision"})
			apTestPut(t, root, "requirements.json", []any{f.requirement})
			t.Logf("profile-current-user-decision boundary=%s source=genuine-synthetic-session consumer=requirements.json", gate)
			if _, e := apTestRun(root, "user-decision", f.ref, map[string]string{"requirements": "requirements.json"}); e != nil {
				t.Fatal(e)
			}
			apTestExportNativeFixture(t, root, profile, "user-decision", "positive", []string{"evidence", "verify", "--kind", "user-decision", "--file", f.ref, "--requirements", "requirements.json"}, 0)
			if old, out, ran := apTestLegacy(t, "verify-user-decision", root, f.ref, "--requirements", "requirements.json"); ran && !old {
				t.Fatalf("fixed source rejected profile current decision: %s", out)
			}
			f.respond(t, "撤回批准", "revoked", "single", "2026-09-05T01:04:00Z", []string{"decision.demo"}, nil)
			f.save(t)
			if _, e := apTestRun(root, "user-decision", f.ref, map[string]string{"requirements": "requirements.json"}); e == nil {
				t.Fatal("profile consumed an explicitly revoked biological reply")
			}
			apTestExportNativeFixture(t, root, profile, "user-decision", "refusal", []string{"evidence", "verify", "--kind", "user-decision", "--file", f.ref, "--requirements", "requirements.json"}, 1)
			if old, out, ran := apTestLegacy(t, "verify-user-decision", root, f.ref, "--requirements", "requirements.json"); ran && old {
				t.Fatalf("fixed source accepted profile revocation: %s", out)
			}
		})
	}
}

func TestUserDecisionNativeAndFixedSourceMatrix(t *testing.T) {
	cases := []struct {
		name   string
		want   bool
		mutate func(*testing.T, *apDecisionFixture)
	}{
		{"approved-current", true, nil},
		{"absolute-current-root", true, func(t *testing.T, f *apDecisionFixture) {
			request := apMap(f.record["request"])
			subject := apMap(apRows(request["items"])[0]["subject"])
			subject["ref"] = filepath.Join(f.root, apText(subject["ref"]))
			f.requirement["subject_ref"] = subject["ref"]
			requester := apMap(request["requester_source"])
			requester["ref"] = filepath.Join(f.root, apText(requester["ref"]))
			f.present(t)
			f.record["responses"] = []any{}
			f.respond(t, "同意", "approved", "single", "2026-09-05T01:03:00Z", []string{"decision.demo"}, apStrings(f.requirement["scope"]))
			presentation := apMap(request["presented_source"])
			presentation["ref"] = filepath.Join(f.root, apText(presentation["ref"]))
			response := apMap(apRows(f.record["responses"])[0]["source"])
			response["ref"] = filepath.Join(f.root, apText(response["ref"]))
			f.requirement["user_decision_ref"] = filepath.Join(f.root, f.ref)
		}},
		{"source-agent-cannot-reply", false, func(t *testing.T, f *apDecisionFixture) {
			apTestMutateMessage(t, f, apMap(apRows(f.record["responses"])[0]["source"]), func(m map[string]any) { m["actor_kind"] = "digital-human" })
		}},
		{"subject-drift", false, func(t *testing.T, f *apDecisionFixture) {
			apTestPut(t, f.root, apText(f.requirement["subject_ref"]), "changed")
		}},
		{"source-byte-drift", false, func(t *testing.T, f *apDecisionFixture) {
			apTestPut(t, f.root, f.prefix+"reply-0.json", `{"source_kind":"session-export","messages":[]}`)
		}},
		{"request-digest-stale", false, func(t *testing.T, f *apDecisionFixture) {
			apRows(apMap(f.record["request"])["items"])[0]["changes"] = "changed after display"
		}},
		{"unlinked-message", false, func(t *testing.T, f *apDecisionFixture) {
			apTestMutateMessage(t, f, apMap(apRows(f.record["responses"])[0]["source"]), func(m map[string]any) { m["reply_to"] = "test-only://unrelated" })
		}},
		{"record-source-text-forgery", false, func(t *testing.T, f *apDecisionFixture) { apRows(f.record["responses"])[0]["text"] = "批准" }},
		{"delegation-absent", false, func(t *testing.T, f *apDecisionFixture) {
			apRows(apMap(f.record["request"])["items"])[0]["responder_ref"] = "person.someone-else"
		}},
		{"genuine-delegated-reply", true, func(t *testing.T, f *apDecisionFixture) { apTestDelegatedResponse(t, f, "2026-09-05T01:01:00Z") }},
		{"delegation-after-presentation", false, func(t *testing.T, f *apDecisionFixture) { apTestDelegatedResponse(t, f, "2026-09-05T01:04:00Z") }},
		{"approved-scope-escalation", false, func(t *testing.T, f *apDecisionFixture) {
			apRows(f.record["responses"])[0]["approved_scope"] = []any{"unauthorized"}
		}},
		{"latest-revocation-wins", false, func(t *testing.T, f *apDecisionFixture) {
			f.respond(t, "撤回批准", "revoked", "single", "2026-09-05T01:04:00Z", []string{"decision.demo"}, nil)
		}},
		{"later-approval-restores", true, func(t *testing.T, f *apDecisionFixture) {
			f.respond(t, "撤回批准", "revoked", "single", "2026-09-05T01:04:00Z", []string{"decision.demo"}, nil)
			f.respond(t, "同意", "approved", "single", "2026-09-05T01:05:00Z", []string{"decision.demo"}, []string{"unit.demo"})
		}},
		{"response-order-matters", false, func(t *testing.T, f *apDecisionFixture) {
			f.respond(t, "同意", "approved", "single", "2026-09-05T01:02:30Z", []string{"decision.demo"}, []string{"unit.demo"})
		}},
		{"presentation-required", false, func(t *testing.T, f *apDecisionFixture) {
			apTestMutateMessage(t, f, apMap(apMap(f.record["request"])["presented_source"]), func(m map[string]any) { m["text"] = "同意吗" })
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := apTestRoot(t)
			f := apTestDecision(t, root, "", "gate.spec-baseline-approved", "", []string{"unit.demo"})
			if tc.mutate != nil {
				tc.mutate(t, f)
			}
			f.save(t)
			apTestPut(t, root, "requirements.json", []any{f.requirement})
			_, err := apTestRun(root, "user-decision", f.ref, map[string]string{"requirements": "requirements.json"})
			if (err == nil) != tc.want {
				t.Fatalf("native pass=%v want=%v error=%v", err == nil, tc.want, err)
			}
			if old, output, ran := apTestLegacy(t, "verify-user-decision", root, f.ref, "--requirements", "requirements.json"); ran {
				if old != tc.want || old != (err == nil) {
					t.Fatalf("legacy pass=%v native=%v want=%v: %s", old, err == nil, tc.want, output)
				}
				t.Logf("fixed-source oracle exit accepted=%v", old)
			}
		})
	}
}

func apTestDelegatedResponse(t *testing.T, f *apDecisionFixture, delegatedAt string) {
	request := apMap(f.record["request"])
	item := apRows(request["items"])[0]
	item["responder_ref"] = "person.delegate"
	item["delegation_source"] = f.capture(t, "delegation", "person.requester", "biological-human", delegatedAt, "指定 person.delegate 回复 decision.demo", "")
	f.present(t)
	f.record["responses"] = []any{}
	f.respond(t, "同意", "approved", "single", "2026-09-05T01:05:00Z", []string{"decision.demo"}, []string{"unit.demo"})
	response := apRows(f.record["responses"])[0]
	response["principal_ref"] = "person.delegate"
	response["source"] = f.capture(t, "reply-0", "person.delegate", "biological-human", "2026-09-05T01:05:00Z", "同意", "test-only://"+f.prefix+"presentation")
}

func TestUserDecisionMultiItemAndCurrentSubset(t *testing.T) {
	for _, name := range []string{"explicit-all", "continue-not-approval", "partial-current", "partial-all-rejected"} {
		t.Run(name, func(t *testing.T) {
			root := apTestRoot(t)
			f := apTestDecision(t, root, "", "gate.spec-baseline-approved", "", []string{"unit.demo"})
			request := apMap(f.record["request"])
			second := apTestJSONClone(t, apRows(request["items"])[0])
			second["id"] = "decision.second"
			second["title"] = "第二事项"
			second["scope"] = []any{"unit.second"}
			request["items"] = append(apArray(request["items"]), second)
			f.present(t)
			f.record["responses"] = []any{}
			switch name {
			case "explicit-all":
				f.respond(t, "同意以上全部事项", "approved", "explicit-all", "2026-09-05T01:03:00Z", []string{"decision.demo", "decision.second"}, []string{"unit.demo", "unit.second"})
			case "continue-not-approval":
				f.respond(t, "继续", "approved", "explicit-all", "2026-09-05T01:03:00Z", []string{"decision.demo", "decision.second"}, []string{"unit.demo", "unit.second"})
			default:
				f.respond(t, "同意 decision.demo", "approved", "explicit-items", "2026-09-05T01:03:00Z", []string{"decision.demo"}, []string{"unit.demo"})
			}
			f.save(t)
			requirements := []any{f.requirement}
			if name != "partial-current" {
				requirements = append(requirements, map[string]any{"boundary": "gate.spec-baseline-approved", "subject_ref": f.requirement["subject_ref"], "scope": []any{"unit.second"}, "user_decision_ref": f.ref})
			}
			apTestPut(t, root, "requirements.json", requirements)
			extra := map[string]string{"requirements": "requirements.json"}
			_, err := apTestRun(root, "user-decision", f.ref, extra)
			want := name == "explicit-all" || name == "partial-current"
			if (err == nil) != want {
				t.Fatalf("pass=%v want=%v err=%v", err == nil, want, err)
			}
		})
	}
}

func TestUserDecisionPublicRequiresIndependentConsumer(t *testing.T) {
	for _, name := range []string{"absent", "empty", "malformed", "wrong-record", "wrong-scope", "checkpoint-current", "checkpoint-absolute", "checkpoint-empty", "checkpoint-forged", "ambiguous-consumers", "task-current", "task-invalid"} {
		t.Run(name, func(t *testing.T) {
			root := apTestRoot(t)
			f := apTestDecision(t, root, "", "gate.spec-baseline-approved", "", []string{"unit.demo"})
			extra := map[string]string{"requirements": "requirements.json"}
			requirement := apTestJSONClone(t, f.requirement)
			apTestPut(t, root, "requirements.json", []any{requirement})
			switch name {
			case "absent":
				extra = nil
			case "empty":
				apTestPut(t, root, "requirements.json", []any{})
			case "malformed":
				apTestPut(t, root, "requirements.json", []any{requirement, "not a requirement"})
			case "wrong-record":
				requirement["user_decision_ref"] = "other-decision.json"
				apTestPut(t, root, "requirements.json", []any{requirement})
			case "wrong-scope":
				requirement["scope"] = []any{"unit.unapproved"}
				apTestPut(t, root, "requirements.json", []any{requirement})
			case "checkpoint-current", "checkpoint-absolute", "checkpoint-empty", "checkpoint-forged", "ambiguous-consumers":
				cp := apTestCheckpoint(map[string]any{})
				apMap(cp["human_review"])["user_decisions"] = []any{requirement}
				if name == "checkpoint-empty" {
					apMap(cp["human_review"])["user_decisions"] = []any{}
				}
				if name == "checkpoint-forged" {
					cp = map[string]any{"human_review": cp["human_review"]}
				}
				apTestPut(t, root, "checkpoint.json", cp)
				extra = map[string]string{"checkpoint": "checkpoint.json"}
				if name == "checkpoint-absolute" {
					extra["checkpoint"] = filepath.Join(root, "checkpoint.json")
				}
				if name == "ambiguous-consumers" {
					extra["requirements"] = "requirements.json"
				}
			case "task-current", "task-invalid":
				task := apTestTask(t, root, 1)
				apTestPut(t, root, "yss-project.yaml", map[string]any{"schema_version": 1, "repository_mode": "template-source"})
				task["user_decisions"] = []any{requirement}
				if name == "task-invalid" {
					task["runtime_id"] = "runtime.unknown"
				}
				apTestPut(t, root, "task.json", task)
				extra = map[string]string{"task": "task.json"}
			}
			result, e := apTestRun(root, "user-decision", f.ref, extra)
			want := name == "checkpoint-current" || name == "checkpoint-absolute" || name == "task-current"
			if (e == nil) != want {
				t.Fatalf("%s pass=%v want=%v: %v", name, e == nil, want, e)
			}
			if name == "absent" {
				if !decisionTestCode(result, "USER_DECISION_CONTEXT_REQUIRED") {
					t.Fatalf("missing current consumer lacks explicit error: %v", e)
				}
				// Intentional public API difference: the fixed old CLI accepts the
				// candidate's own request as the expected scope. Native current
				// verification requires a separate consumer supplied expectation.
				if old, out, ran := apTestLegacy(t, "verify-user-decision", root, f.ref); ran && !old {
					t.Fatalf("expected documented legacy default accepted: %s", out)
				}
			}
		})
	}
}

func decisionTestCode(result any, code string) bool {
	r, ok := result.(*SemanticReport)
	if !ok {
		return false
	}
	for _, d := range r.Diagnostics {
		if d.Code == code {
			return true
		}
	}
	return false
}

func TestUserDecisionIndependentRequirementsDrift(t *testing.T) {
	root := apTestRoot(t)
	f := apTestDecision(t, root, "", "gate.spec-baseline-approved", "", []string{"unit.demo"})
	apTestPut(t, root, "requirements.json", []any{f.requirement})
	s := apTestSession(t, root)
	if e := s.verify("user-decision", f.ref, map[string]string{"requirements": "requirements.json"}); e != nil {
		t.Fatal(e)
	}
	requirement := apTestJSONClone(t, f.requirement)
	requirement["scope"] = []any{"unit.other"}
	apTestPut(t, root, "requirements.json", []any{requirement})
	if e := s.finish(); e == nil {
		t.Fatal("changed independent scope was not observed")
	}
}

func apTestAsset(t *testing.T, root, ref string, v any) map[string]any {
	return map[string]any{"ref": ref, "version": "v1", "digest": apDigest(apTestPut(t, root, ref, v))}
}
func apTestContinuation(t *testing.T, root string) (map[string]any, map[string]any, map[string]any, map[string]any) {
	basis := apTestAsset(t, root, "authorized-basis.json", map[string]any{"test_only": "original scope"})
	subject := apTestAsset(t, root, "current-subject.json", map[string]any{"test_only": "current unchanged scope"})
	external := apTestAsset(t, root, "external-policy.json", map[string]any{"status": "confirmed", "requirements": []any{}})
	db := map[string]any{}
	for _, field := range []string{"business_scope", "acceptance", "contract_commitments", "authorization", "risk_acceptance", "quality", "external_commitments"} {
		db[field] = []any{"fixed synthetic basis"}
	}
	mandate := map[string]any{"schema_version": 1, "kind": "delivery-authorization", "targets": []any{map[string]any{"boundary": "gate.spec-baseline-approved", "subject_ref": "current-subject.json", "scope": []any{"unit.demo"}, "decision_basis": db}}, "basis": []any{basis}, "external_policy": external}
	apTestPut(t, root, "mandate.json", mandate)
	source := apTestDecision(t, root, "source-", "delivery-scope", "mandate.json", []string{"unit.demo"})
	currentBasis := apTestAsset(t, root, "verification.json", map[string]any{"test_only": "fresh actual evidence"})
	review := map[string]any{"role_id": "role.product-manager", "runtime_id": "runtime.generic", "principal_ref": "agent.independent-reviewer", "drafter_principal_ref": "agent.drafter", "decision": "approved", "classification": "within-approved-scope", "material_changes": []any{}, "findings": []any{}, "boundary": "gate.spec-baseline-approved", "scope": []any{"unit.demo"}, "subject": subject, "basis": []any{currentBasis}, "decision_basis": db, "reason": "当前资产仍在原始已批准合成范围内", "comparison": map[string]any{"before": basis, "after": subject}}
	proof := map[string]any{"schema_version": 1, "kind": "approved-scope-continuation-v1", "boundary": "gate.spec-baseline-approved", "subject": subject, "scope": []any{"unit.demo"}, "source": source.requirement, "basis": []any{currentBasis}, "review": apTestAsset(t, root, "continuation-review.json", review), "external_decisions": []any{}}
	apTestPut(t, root, "continuation-proof.json", proof)
	requirement := map[string]any{"boundary": "gate.spec-baseline-approved", "subject_ref": "current-subject.json", "scope": []any{"unit.demo"}, "continuation_ref": "continuation-proof.json"}
	apTestPut(t, root, "continuation-requirement.json", requirement)
	return requirement, proof, review, mandate
}
func TestDecisionContinuationCurrentAndMaterialBoundaries(t *testing.T) {
	for _, name := range []string{"approved-current", "review-self-sign", "material-change", "malformed-empty-findings", "unresolved-important-risk", "new-scope", "chained-source", "external-policy-unconfirmed", "original-revoked"} {
		t.Run(name, func(t *testing.T) {
			root := apTestRoot(t)
			req, proof, review, mandate := apTestContinuation(t, root)
			switch name {
			case "review-self-sign":
				review["principal_ref"] = review["drafter_principal_ref"]
			case "material-change":
				review["material_changes"] = []any{"acceptance"}
			case "malformed-empty-findings":
				review["findings"] = map[string]any{}
			case "unresolved-important-risk":
				review["findings"] = []any{map[string]any{"id": "risk-1", "kind": "important-risk", "reason": "meaningful", "status": "open"}}
			case "new-scope":
				req["scope"] = []any{"unit.other"}
			case "chained-source":
				apMap(proof["source"])["continuation_ref"] = "other.json"
			case "external-policy-unconfirmed":
				mandate["external_policy"] = apTestAsset(t, root, "external-policy.json", map[string]any{"status": "unknown", "requirements": []any{}})
				apTestPut(t, root, "mandate.json", mandate)
			case "original-revoked":
				s := newSemanticSession(context.Background(), root, nil)
				r, e := s.doc("source-decision.json")
				if e != nil {
					t.Fatal(e)
				}
				apRows(r["responses"])[0]["decision"] = "revoked"
				apTestPut(t, root, "source-decision.json", r)
			}
			proof["review"] = apTestAsset(t, root, "continuation-review.json", review)
			apTestPut(t, root, "continuation-proof.json", proof)
			apTestPut(t, root, "continuation-requirement.json", req)
			apTestPut(t, root, "current-requirements.json", []any{req})
			_, err := apTestRun(root, "user-decision", "continuation-proof.json", map[string]string{"continuation": "true", "requirements": "current-requirements.json"})
			want := name == "approved-current"
			if (err == nil) != want {
				t.Fatalf("pass=%v want=%v err=%v", err == nil, want, err)
			}
			if old, out, ran := apTestLegacy(t, "verify-user-decision", root, "continuation-requirement.json", "--continuation"); ran {
				if old != (err == nil) {
					t.Fatalf("legacy=%v native=%v: %s", old, err == nil, out)
				}
			}
		})
	}
}

func TestDecisionContinuationPublicDoesNotSelfAuthorize(t *testing.T) {
	for _, name := range []string{"absent", "proof-with-consumer", "requirement-as-candidate", "wrong-scope", "wrong-proof", "empty"} {
		t.Run(name, func(t *testing.T) {
			root := apTestRoot(t)
			requirement, _, _, _ := apTestContinuation(t, root)
			extra := map[string]string{"continuation": "true", "requirements": "requirements.json"}
			ref := "continuation-proof.json"
			switch name {
			case "absent":
				delete(extra, "requirements")
			case "requirement-as-candidate":
				ref = "continuation-requirement.json"
			case "wrong-scope":
				requirement["scope"] = []any{"unit.other"}
			case "wrong-proof":
				requirement["continuation_ref"] = "other-proof.json"
			}
			rows := []any{requirement}
			if name == "empty" {
				rows = []any{}
			}
			apTestPut(t, root, "requirements.json", rows)
			result, e := apTestRun(root, "user-decision", ref, extra)
			if (e == nil) != (name == "proof-with-consumer") {
				t.Fatalf("%s: %v", name, e)
			}
			if name == "absent" {
				if !decisionTestCode(result, "USER_DECISION_CONTEXT_REQUIRED") {
					t.Fatal(e)
				}
				if old, out, ran := apTestLegacy(t, "verify-user-decision", root, "continuation-requirement.json", "--continuation"); ran && !old {
					t.Fatalf("legacy standalone requirement should pass: %s", out)
				}
			}
		})
	}
}

func TestDecisionReuseRequiresOriginalCoveredAssetsRisksAndConditions(t *testing.T) {
	for _, name := range []string{"covered", "extra-risk", "extra-condition", "asset-drift", "scope-expansion", "malformed-risk-array"} {
		t.Run(name, func(t *testing.T) {
			root := apTestRoot(t)
			f := apTestDecision(t, root, "", "gate.spec-baseline-approved", "", []string{"unit.demo"})
			item := apRows(apMap(f.record["request"])["items"])[0]
			asset := apCopy(apMap(item["subject"]))
			asset["boundary"] = item["boundary"]
			asset["scope"] = item["scope"]
			manifest := map[string]any{"schema_version": 1, "kind": "strategic-delivery-scope", "assets": []any{asset}, "risks": []any{}, "conditions": []any{"进入下一工作单元"}}
			reuse := map[string]any{"schema_version": 1, "kind": "strategic-decision-reuse-v1", "target_gate": "gate.strategic-design-handoff-approved", "scope_ref": "delivery-scope.json", "scope": []any{"unit.demo"}, "requirements": []any{f.requirement}}
			switch name {
			case "extra-risk":
				manifest["risks"] = []any{"unapproved risk"}
			case "extra-condition":
				manifest["conditions"] = []any{"new release commitment"}
			case "asset-drift":
				apTestPut(t, root, apText(asset["ref"]), "changed")
			case "scope-expansion":
				reuse["scope"] = []any{"unit.other"}
			case "malformed-risk-array":
				manifest["risks"] = map[string]any{}
			}
			apTestPut(t, root, "delivery-scope.json", manifest)
			apTestPut(t, root, "reuse.json", reuse)
			s := apTestSession(t, root)
			apMap(s.roles["user_decision_policy"])["required_capabilities"] = []any{"strategic-decision-reuse-v1"}
			record := map[string]any{"gate_id": "gate.strategic-design-handoff-approved", "subject_ref": "delivery-scope.json", "approval_scope": reuse["scope"], "decision_reuse_ref": "reuse.json"}
			_, err := assertApprovalUserDecisionSemantic(s, record)
			if (err == nil) != (name == "covered") {
				t.Fatalf("reuse %s: %v", name, err)
			}
		})
	}
}

func TestContinuationPreservesExternalMandatoryApproval(t *testing.T) {
	for _, name := range []string{"genuine-external-reply", "missing-external-reply", "wrong-external-principal", "agent-cannot-answer-external"} {
		t.Run(name, func(t *testing.T) {
			root := apTestRoot(t)
			req, proof, _, mandate := apTestContinuation(t, root)
			mandate["external_policy"] = apTestAsset(t, root, "external-policy.json", map[string]any{"status": "confirmed", "requirements": []any{map[string]any{"id": "external.security", "boundaries": []any{"gate.spec-baseline-approved"}, "principals": []any{"person.requester"}}}})
			apTestPut(t, root, "mandate.json", mandate)
			source := apTestDecision(t, root, "source-", "delivery-scope", "mandate.json", []string{"unit.demo"})
			proof["source"] = source.requirement
			f := apTestDecision(t, root, "external-", "gate.spec-baseline-approved", "current-subject.json", []string{"unit.demo"})
			reply := map[string]any{"obligation_id": "external.security", "principal_ref": "person.requester", "requirement": f.requirement}
			proof["external_decisions"] = []any{reply}
			switch name {
			case "missing-external-reply":
				proof["external_decisions"] = []any{}
			case "wrong-external-principal":
				reply["principal_ref"] = "person.other"
			case "agent-cannot-answer-external":
				apTestMutateMessage(t, f, apMap(apRows(f.record["responses"])[0]["source"]), func(m map[string]any) { m["actor_kind"] = "digital-human" })
				f.save(t)
			}
			apTestPut(t, root, "continuation-proof.json", proof)
			apTestPut(t, root, "continuation-requirement.json", req)
			apTestPut(t, root, "current-requirements.json", []any{req})
			_, err := apTestRun(root, "user-decision", "continuation-proof.json", map[string]string{"continuation": "true", "requirements": "current-requirements.json"})
			if (err == nil) != (name == "genuine-external-reply") {
				t.Fatalf("%s: %v", name, err)
			}
			if old, out, ran := apTestLegacy(t, "verify-user-decision", root, "continuation-requirement.json", "--continuation"); ran {
				if old != (err == nil) {
					t.Fatalf("legacy=%v native=%v: %s", old, err == nil, out)
				}
			}
		})
	}
}
