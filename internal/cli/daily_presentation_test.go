package cli

import (
	"strings"
	"testing"
)

func TestDaily2RouteSummaryUsesApplicableReview(t *testing.T) {
	for _, version := range []int{1, 2} {
		summary := renderHumanSuccess("lifecycle", options{args: []string{"lifecycle", "route"}, values: map[string]string{}}, "spec", map[string]any{"kind": "lifecycle.route", "schema_version": version, "delivery_path": "daily"})
		want := "测试 → 独立审查"
		if version == 2 {
			want = "测试 → 适用复核"
		}
		if !strings.Contains(summary, want) {
			t.Fatalf("schema %d summary missing %q: %s", version, want, summary)
		}
	}
}
