package approval

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"path/filepath"
	"testing"
)

func TestContinuationComparisonRequiresCompleteActualChanges(t *testing.T) {
	for _, scenario := range []string{"presentation", "detail", "evidence", "protected", "unknown-class", "omitted", "extra", "null-is-not-missing", "undeclared-path", "body-change", "unknown-selector"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, _ := trueDecisionFixture(t)
			before := map[string]any{"decision": []string{"approved"}, "technical": map[string]any{"value": "old"}, "evidence": map[string]any{}}
			after := map[string]any{"decision": []string{"approved"}, "technical": map[string]any{"value": "new"}, "evidence": map[string]any{}}
			target := map[string]any{"boundary": "gate.spec-baseline-approved", "subject_ref": "current.json", "decision_basis": map[string]any{}, "continuation_paths": map[string]any{"implementation-detail": []string{"/technical"}, "evidence-supplement": []string{"/evidence"}}}
			for _, key := range decisionDimensions {
				kernel.ApMap(target["decision_basis"])[key] = []string{"/decision"}
			}
			class := "implementation-detail"
			changes := []any{map[string]any{"path": "/technical/value", "before": map[string]any{"present": true, "value": "old"}, "after": map[string]any{"present": true, "value": "new"}}}
			if scenario == "presentation" {
				after = before
				class = "presentation-only"
				changes = []any{}
			}
			if scenario == "evidence" {
				after = kernel.ApCopy(before)
				after["evidence"] = map[string]any{"new": nil}
				class = "evidence-supplement"
				changes = []any{map[string]any{"path": "/evidence/new", "before": map[string]any{"present": false}, "after": map[string]any{"present": true, "value": nil}}}
			}
			switch scenario {
			case "protected":
				after["decision"] = []string{"expanded"}
			case "unknown-class":
				class = "safe-change"
			case "omitted":
				changes = []any{}
			case "extra":
				changes = append(changes, changes[0])
			case "null-is-not-missing":
				kernel.ApMap(kernel.ApMap(changes[0])["before"])["value"] = nil
			case "undeclared-path":
				target["continuation_paths"] = map[string]any{}
			case "unknown-selector":
				kernel.ApMap(target["decision_basis"])["authorization"] = []string{"/future"}
			}
			decisionFile(t, s.Root, "before.json", before)
			decisionFile(t, s.Root, "current.json", after)
			if scenario == "body-change" {
				target["subject_ref"] = "current.md"
				os.WriteFile(filepath.Join(s.Root, "before.json"), []byte("---\ndecision: approved\n---\noriginal body"), 0644)
				os.WriteFile(filepath.Join(s.Root, "current.json"), []byte("---\ndecision: approved\n---\nchanged body"), 0644)
				for _, key := range decisionDimensions {
					kernel.ApMap(target["decision_basis"])[key] = []string{"/frontmatter/decision"}
				}
				delete(target, "continuation_paths")
				class = "presentation-only"
				changes = []any{}
			}
			a, _ := os.ReadFile(filepath.Join(s.Root, "before.json"))
			b, _ := os.ReadFile(filepath.Join(s.Root, "current.json"))
			decisionFile(t, s.Root, "delta.json", map[string]any{"schema_version": 1, "changes": changes})
			delta, _ := os.ReadFile(filepath.Join(s.Root, "delta.json"))
			afterRef := "current.json"
			if scenario == "body-change" {
				afterRef = "current.md"
			}
			comparison := map[string]any{"before": map[string]any{"ref": "before.json"}, "after": map[string]any{"ref": afterRef}, "differences": []any{map[string]any{"ref": "delta.json", "digest": safefs.Digest(delta), "classification": class, "reason": "actual current difference"}}, "material_changes": []any{}, "decision_basis_unchanged": true}
			err := compareContinuation(s, target, comparison, a, b)
			want := scenario == "presentation" || scenario == "detail" || scenario == "evidence"
			if (err == nil) != want {
				t.Fatalf("%s: %v", scenario, err)
			}
		})
	}
}

func TestDecisionSelectorsResolveConcreteArrayMembers(t *testing.T) {
	model := map[string]any{"items": []any{map[string]any{"scope": "approved"}}}
	if value, ok := pointerValue(model, "/items/0/scope"); !ok || value != "approved" {
		t.Fatal("concrete JSON Pointer array selector did not resolve")
	}
	for _, pointer := range []string{"/items/01/scope", "/items/-/scope", "/items/1/scope"} {
		if _, ok := pointerValue(model, pointer); ok {
			t.Fatalf("nonexistent or noncanonical index resolved: %s", pointer)
		}
	}
}
