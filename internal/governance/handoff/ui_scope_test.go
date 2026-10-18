package handoff

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

func TestCurrentHandoffCannotWaiveUIEvidenceWhenImpactRequiresIt(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any, map[string]any, map[string]any)
		ok   bool
	}{
		{"backend-only", func(h, impact, config map[string]any) {}, true},
		{"ui-impact", func(h, impact, config map[string]any) { impact["ui"] = true }, false},
		{"frontend-impact", func(h, impact, config map[string]any) { impact["frontend"] = true }, false},
		{"unknown-ui", func(h, impact, config map[string]any) { delete(impact, "ui") }, false},
		{"existing-ui-with-ui-impact", func(h, impact, config map[string]any) {
			h["ui_baseline_kind"], impact["ui"] = "existing-ui-baseline", true
		}, false},
		{"active-frontend", func(h, impact, config map[string]any) {
			h["consumer_routes"] = []any{map[string]any{"capability": "frontend-engineering-design", "activation": "required"}}
		}, false},
		{"unexpected-ui-source", func(h, impact, config map[string]any) {
			h["source"] = map[string]any{"prototype_ref": map[string]any{}}
		}, false},
		{"unexpected-ui-approval", func(h, impact, config map[string]any) {
			config["approvals"] = map[string]any{"visual_baseline_ref": map[string]any{}}
		}, false},
		{"unexpected-prototype-config", func(h, impact, config map[string]any) {
			config["prototype"] = map[string]any{}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := kernel.NewSemanticSession(context.Background(), t.TempDir(), nil)
			s.GovernanceVersion = 2
			h := map[string]any{"schema_version": json.Number("5"), "ui_baseline_kind": "not-applicable"}
			impact, config := map[string]any{"ui": false, "frontend": false, "backend": true}, map[string]any{}
			tc.edit(h, impact, config)
			if err := contractHandoffUIScope(s, h, map[string]any{"impact_assessment": impact}, config); (err == nil) != tc.ok {
				t.Fatalf("UI applicability = %v; want allowed=%v", err, tc.ok)
			}
		})
	}
}
