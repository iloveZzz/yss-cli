package handoff

import (
	"context"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

func TestLegacyStrategicConsumptionIsUnsupported(t *testing.T) {
	s := kernel.NewSemanticSession(context.Background(), t.TempDir(), nil)
	err := ContractHandoffConsumption(s, map[string]any{"strategic_handoff": map[string]any{}}, nil)
	if kernel.SemanticCode(err) != "UNPORTED" {
		t.Fatalf("legacy strategic consumption must be unsupported, got %v", err)
	}
}
