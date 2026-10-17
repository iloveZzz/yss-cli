package handoff

import (
	"context"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"testing"
)

func TestSourceApprovalContextVersionIsChosenByItsOwnPolicy(t *testing.T) {
	for _, c := range []struct {
		name       string
		governance int
		version    any
		ok         bool
	}{
		{"legacy", 1, nil, true}, {"current", 2, json.Number("2"), true}, {"missing-current", 2, nil, false}, {"new-on-legacy", 1, json.Number("2"), false}, {"future", 2, json.Number("3"), false}, {"string-is-not-a-version", 2, "2", false}, {"unknown-policy", 0, nil, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := kernel.NewSemanticSession(context.Background(), t.TempDir(), nil)
			s.GovernanceVersion = c.governance
			value := map[string]any{}
			if c.version != nil {
				value["schema_version"] = c.version
			}
			if err := sourceApprovalContextVersion(s, value); (err == nil) != c.ok {
				t.Fatalf("version/policy selection: %v", err)
			}
		})
	}
}
