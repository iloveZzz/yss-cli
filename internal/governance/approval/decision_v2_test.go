package approval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

func decisionFile(t *testing.T, root, ref string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(filepath.Join(root, ref)), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, ref), raw, 0644); err != nil {
		t.Fatal(err)
	}
}
func trueDecisionFixture(t *testing.T) (*kernel.SemanticSession, map[string]any, map[string]any) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	decisionFile(t, root, decisionSchemaRef, map[string]any{"type": "object"})
	decisionFile(t, root, "subject.json", map[string]any{"scope": "formed implementation"})
	raw, _ := os.ReadFile(filepath.Join(root, "subject.json"))
	asset := map[string]any{"ref": "subject.json", "version": "1", "digest": kernel.ApDigest(raw)}
	item := map[string]any{"id": "D1", "boundary": "implementation-scope", "title": "实施明确范围", "subject": asset, "basis": []any{}, "changes": "当前已形成方案", "scope": []string{"TICKET-1"}, "risks": []string{}, "recommendation": "批准", "next_actions": []string{"实施TICKET-1"}, "responder_ref": "human"}
	original := map[string]any{"id": "request", "actor_kind": "biological-human", "principal_ref": "human", "original_ref": "conversation:request", "sent_at": "2026-01-01T00:00:00Z", "text": "准备此方案"}
	presentation := map[string]any{"id": "presentation", "actor_kind": "agent", "principal_ref": "agent", "original_ref": "conversation:presentation", "sent_at": "2026-01-01T00:01:00Z"}
	response := map[string]any{"id": "response", "actor_kind": "biological-human", "principal_ref": "human", "original_ref": "conversation:response", "sent_at": "2026-01-01T00:02:00Z", "text": "批准", "reply_to": "conversation:presentation"}
	request := map[string]any{"id": "REQ-1", "requester_ref": "human", "items": []any{item}}
	source := func(ref, id string) map[string]any {
		raw, _ := os.ReadFile(filepath.Join(root, ref))
		return map[string]any{"kind": "session-export", "ref": ref, "digest": kernel.ApDigest(raw), "message_id": id}
	}
	decisionFile(t, root, "request-source.json", map[string]any{"source_kind": "session-export", "messages": []any{original}})
	request["requester_source"] = source("request-source.json", "request")
	presentation["text"] = renderDecisionRequestSemantic(request)
	decisionFile(t, root, "reply-source.json", map[string]any{"source_kind": "session-export", "messages": []any{presentation, response}})
	request["presented_source"] = source("reply-source.json", "presentation")
	answer := map[string]any{"source": source("reply-source.json", "response"), "principal_ref": "human", "responded_at": response["sent_at"], "text": "批准", "request_digest": decisionRequestDigest(request), "decision": "approved", "selection": "single", "item_ids": []string{"D1"}, "approved_scope": []string{"TICKET-1"}}
	record := map[string]any{"schema_version": 1, "request": request, "responses": []any{answer}}
	requirement := map[string]any{"boundary": "implementation-scope", "subject_ref": "subject.json", "scope": []string{"TICKET-1"}, "user_decision_ref": "decision.json"}
	decisionFile(t, root, "decision.json", record)
	decisionFile(t, root, "requirements.json", requirement)
	s := kernel.NewSemanticSession(context.Background(), root, nil)
	s.GovernanceVersion = 2
	s.Report.Profile = "backend"
	s.GovernancePolicy = map[string]any{"user_decision_policy": map[string]any{"source_kinds": []string{"session-export"}, "profile_scope": map[string]any{"backend": map[string]any{"gates": []string{}, "work_units": map[string]any{"work-unit.slice-implementation": "implementation-scope"}}}}}
	return s, record, requirement
}
func TestPolicy2RealDecisionDoesNotNeedRoles(t *testing.T) {
	s, _, _ := trueDecisionFixture(t)
	if err := s.Verify("user-decision", "decision.json", map[string]string{"requirements": "requirements.json"}); err != nil {
		t.Fatal(err)
	}
}

func TestCapturedIndependentRequirementCannotBeAgentOrUnboundScope(t *testing.T) {
	for _, scenario := range []string{"human", "markdown", "agent", "wrong-scope", "negated", "stale"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, _ := trueDecisionFixture(t)
			message := map[string]any{"id": "require", "actor_kind": "biological-human", "principal_ref": "human", "original_ref": "chat:require", "sent_at": "2026-01-01T00:00:00Z", "text": "请独立复核 drafts"}
			if scenario == "agent" {
				message["actor_kind"] = "agent"
			}
			if scenario == "wrong-scope" {
				message["text"] = "请独立复核 other"
			}
			if scenario == "negated" {
				message["text"] = "不要独立复核 drafts"
			}
			decisionFile(t, s.Root, "require-source.json", map[string]any{"source_kind": "session-export", "messages": []any{message}})
			raw, _ := os.ReadFile(filepath.Join(s.Root, "require-source.json"))
			requirement := map[string]any{"kind": "review-requirement", "mode": "independent-review", "scope": []string{"drafts"}, "source": map[string]any{"kind": "session-export", "ref": "require-source.json", "digest": kernel.ApDigest(raw), "message_id": "require"}}
			decisionFile(t, s.Root, "required-review.json", requirement)
			raw, _ = os.ReadFile(filepath.Join(s.Root, "required-review.json"))
			ref := "required-review.json"
			if scenario == "markdown" {
				ref = "required-review.md"
				raw = append(append([]byte("---\n"), raw...), []byte("\n---\nActual captured requirement")...)
				if err := os.WriteFile(filepath.Join(s.Root, ref), raw, 0644); err != nil {
					t.Fatal(err)
				}
			}
			binding := map[string]any{"ref": ref, "digest": strings.TrimPrefix(kernel.ApDigest(raw), "sha256:")}
			if scenario == "stale" {
				decisionFile(t, s.Root, "require-source.json", map[string]any{"changed": true})
			}
			mode, err := s.RequiredReviewMode(nil, nil, []string{"drafts"}, []any{binding})
			if (err == nil) != (scenario == "human" || scenario == "markdown") {
				t.Fatalf("%s: %v", scenario, err)
			}
			if err == nil && mode != "independent-review" {
				t.Fatal("captured user requirement downgraded")
			}
		})
	}
}

func TestRequiredReviewScopeUsesRepositoryPathIntersection(t *testing.T) {
	for _, scenario := range []string{"child", "parent", "trailing-slash", "disjoint", "prefix-boundary", "escape", "case-ambiguity", "symlink", "unknown-root", "registered-root"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, _ := trueDecisionFixture(t)
			requested, actual := []string{"drafts/spec.md"}, []string{"drafts"}
			var implementation map[string]any
			wantMode, wantError := "independent-review", false
			switch scenario {
			case "parent":
				requested, actual = []string{"drafts"}, []string{"drafts/spec.md"}
			case "trailing-slash":
				requested = []string{"drafts/"}
			case "disjoint":
				requested = []string{"docs"}
				wantMode = "self-check"
			case "prefix-boundary":
				requested = []string{"draftstuff"}
				wantMode = "self-check"
			case "escape":
				requested = []string{"../drafts"}
				wantError = true
			case "case-ambiguity":
				requested = []string{"DRAFTS/spec.md"}
				wantError = true
			case "symlink":
				if err := os.MkdirAll(filepath.Join(s.Root, "drafts"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Join(s.Root, "drafts", "link")); err != nil {
					t.Fatal(err)
				}
				requested = []string{"drafts/link/file.md"}
				wantError = true
			case "unknown-root", "registered-root":
				external, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				decisionFile(t, s.Root, "implementation.json", map[string]any{"baseline": map[string]any{"repository_root": external}})
				implementation = map[string]any{"task_ref": "implementation.json"}
				if scenario == "registered-root" {
					s.ExternalViews[external] = kernel.NewView(external)
				} else {
					wantError = true
				}
			}
			binding := capturedRequirement(t, s, "请独立复核 "+strings.Join(requested, "、"), requested)
			mode, err := s.RequiredReviewMode(nil, implementation, actual, []any{binding})
			if wantError {
				if err == nil {
					t.Fatalf("ambiguous/unsafe scope silently accepted: %q", mode)
				}
			} else if err != nil || mode != wantMode {
				t.Fatalf("%s: mode=%q err=%v", scenario, mode, err)
			}
		})
	}
}

func capturedRequirement(t *testing.T, s *kernel.SemanticSession, text string, scope []string) map[string]any {
	t.Helper()
	decisionFile(t, s.Root, "captured-message.json", map[string]any{"source_kind": "session-export", "messages": []any{map[string]any{"id": "R1", "actor_kind": "biological-human", "principal_ref": "human", "original_ref": "chat:R1", "sent_at": "2026-01-01T00:00:00Z", "text": text}}})
	raw, _ := os.ReadFile(filepath.Join(s.Root, "captured-message.json"))
	decisionFile(t, s.Root, "captured-requirement.json", map[string]any{"kind": "review-requirement", "mode": "independent-review", "scope": scope, "source": map[string]any{"kind": "session-export", "ref": "captured-message.json", "digest": kernel.ApDigest(raw), "message_id": "R1"}})
	raw, _ = os.ReadFile(filepath.Join(s.Root, "captured-requirement.json"))
	return map[string]any{"ref": "captured-requirement.json", "digest": strings.TrimPrefix(kernel.ApDigest(raw), "sha256:")}
}

func TestReviewRequirementMatchesOnlyCompleteExplicitRequest(t *testing.T) {
	for _, row := range []struct {
		text  string
		scope []string
		valid bool
	}{
		{"请独立复核 drafts", []string{"drafts"}, true}, {"  请独立复核 drafts。 \n", []string{"drafts"}, true},
		{"Please independently review drafts.", []string{"drafts"}, true}, {"请独立复核 drafts、docs", []string{"drafts", "docs"}, true},
		{"drafts 无需进行独立复核", []string{"drafts"}, false}, {"drafts 是独立服务", []string{"drafts"}, false},
		{"不要独立复核 drafts", []string{"drafts"}, false}, {"如果失败，请独立复核 drafts", []string{"drafts"}, false},
		{"引用：请独立复核 drafts", []string{"drafts"}, false}, {"请对 drafts 做独立审查", []string{"drafts"}, false},
		{"请独立复核 drafts。无需实际复核", []string{"drafts"}, false}, {"Please independently review drafts, docs.", []string{"drafts, docs"}, false},
	} {
		t.Run(row.text, func(t *testing.T) {
			s, _, _ := trueDecisionFixture(t)
			binding := capturedRequirement(t, s, row.text, row.scope)
			mode, err := s.RequiredReviewMode(nil, nil, []string{"drafts"}, []any{binding})
			if (err == nil) != row.valid || row.valid && mode != "independent-review" {
				t.Fatalf("mode=%q err=%v", mode, err)
			}
		})
	}
	s, _, _ := trueDecisionFixture(t)
	mode, err := s.RequiredReviewMode(nil, nil, []string{"drafts"})
	if err != nil || mode != "self-check" {
		t.Fatalf("ordinary unbound work: %q %v", mode, err)
	}
}
