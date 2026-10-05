package governance

import (
	"context"
	"os"
	"testing"
)

// The optional fixture is produced by the fixed legacy prepare/dispatch/complete
// cycle using synthetic actors and replies. It carries no real authorization.
func TestPlanFixedSourceAggregateDifferential(t *testing.T) {
	root := os.Getenv("YSS_PLAN_ORACLE_FIXTURE")
	if root == "" {
		t.Skip("fixed legacy aggregate fixture not configured")
	}
	s := newSemanticSession(context.Background(), root, map[string]string{"tool-root": os.Getenv("YSS_LEGACY_ORACLE_ROOT")})
	if err := s.authorities(); err != nil {
		t.Fatal(err)
	}
	const cp = "docs/.scratch/feature.demo/checkpoint.json"
	if err := s.verify("plan-spec-entry", cp, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.verify("plan-aggregate", cp, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
}
