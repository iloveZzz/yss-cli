package handoff

import "testing"

func TestReviewedSourceMustBeTheActualSubjectOrBasis(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record map[string]any
		ok     bool
	}{
		{"subject", map[string]any{"subject_ref": "source.yaml", "subject_digest": "abc"}, true},
		{"basis", map[string]any{"basis": []any{map[string]any{"ref": "source.yaml", "digest": "abc"}}}, true},
		{"changed-subject", map[string]any{"subject_ref": "source.yaml", "subject_digest": "old"}, false},
		{"unrelated-basis", map[string]any{"basis": []any{map[string]any{"ref": "other.yaml", "digest": "abc"}}}, false},
		{"declaration-only", map[string]any{"artifact_bindings": []any{map[string]any{"ref": "source.yaml", "digest": "abc"}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if actual := reviewedSource(tc.record, "source.yaml", "abc"); actual != tc.ok {
				t.Fatalf("reviewedSource = %v; want %v", actual, tc.ok)
			}
		})
	}
}
