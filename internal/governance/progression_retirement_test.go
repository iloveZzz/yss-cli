package governance

import (
	"context"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

func TestLegacyStrategicDeliveryNeverReady(t *testing.T) {
	s := kernel.NewSemanticSession(context.Background(), t.TempDir(), nil)
	for _, cp := range []map[string]any{{}, {"strategic_delivery_ref": "historical/delivery-record.json"}} {
		if code := kernel.SemanticCode(progressionCheck(s, "checkpoint.json", cp, "strategic-handoff-delivery")); code != "UNPORTED" {
			t.Fatalf("legacy check = %q, want UNPORTED", code)
		}
		status, _, _ := guidanceTargetInputSession(context.Background(), s, "spec", "checkpoint.json", cp, "/consumer", "backend", "initialized", nil)
		if status != "blocked" {
			t.Fatalf("legacy intake = %q, want blocked", status)
		}
	}
	consumer := ProgressionConsumer{Profile: "backend", Root: "/consumer", CheckpointRef: "checkpoint.json"}
	target := ProgressionTarget{Target: "backend-deliverable", Consumers: []ProgressionConsumer{consumer}}
	rows, _ := progressionConsumers(s, "spec", "checkpoint.json", nil, target)
	if row := kernel.SemMap(rows["backend"]); row["input_status"] != "blocked" {
		t.Fatalf("consumer = %v", row)
	}
	row := map[string]any{"input_status": "verified"}
	if progressionConsumerAwaitable(s, "spec", "checkpoint.json", nil, target, consumer, map[string]any{"backend": row}, nil) || row["source_ready"] != false {
		t.Fatalf("legacy source ready: %v", row)
	}

}
