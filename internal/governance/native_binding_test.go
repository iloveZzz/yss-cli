package governance

import (
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"testing"
)

func TestNativeBindingValidatesRetainedLegacyScopeAndBytes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		legacy   map[string]any
		lineage  bool
		mutate   bool
		wantPass bool
	}{
		{"native-only", nil, false, false, true},
		{"matching-history", map[string]any{"plugin": "yss-backend-delivery", "execution_scope": "plan-to-backend"}, true, false, true},
		{"conflicting-scope", map[string]any{"plugin": "yss-backend-delivery", "execution_scope": "strategic-design"}, false, false, false},
		{"changed-history", map[string]any{"plugin": "yss-backend-delivery", "execution_scope": "plan-to-backend"}, true, true, false},
		{"missing-history", nil, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := apTestRoot(t)
			native := map[string]any{"schema_version": 2, "plugin": "yss-backend-delivery", "execution_scope": "plan-to-backend"}
			if tc.legacy != nil {
				apTestPut(t, root, ".yss-plugin.json", tc.legacy)
			}
			if tc.lineage {
				sum := "absent"
				if tc.legacy != nil {
					raw, err := apTestSession(t, root).bytes(".yss-plugin.json")
					if err != nil {
						t.Fatal(err)
					}
					sum = safefs.Digest(raw)
				}
				native["legacy_binding"] = map[string]any{"path": ".yss-plugin.json", "sha256": sum}
			}
			if tc.mutate {
				tc.legacy["user_change"] = true
				apTestPut(t, root, ".yss-plugin.json", tc.legacy)
			}
			apTestPut(t, root, ".yss-backend-plugin.json", native)
			_, err := apTestSession(t, root).backendPluginBinding()
			if (err == nil) != tc.wantPass {
				t.Fatalf("pass=%v want=%v error=%v", err == nil, tc.wantPass, err)
			}
		})
	}
}
