package governance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

func TestCurrentProgressionRetiresDesignConsumer(t *testing.T) {
	for _, tc := range []struct {
		policy  int
		profile string
		ok      bool
	}{{1, "design", false}, {2, "design", false}, {2, "backend", true}} {
		s := kernel.NewSemanticSession(context.Background(), t.TempDir(), nil)
		s.GovernanceVersion = tc.policy
		err := progressionCurrentConsumers(s, ProgressionTarget{Consumers: []ProgressionConsumer{{Profile: tc.profile}}})
		if (err == nil) != tc.ok {
			t.Fatalf("policy=%d profile=%s: %v", tc.policy, tc.profile, err)
		}
	}
}

func TestProgressionTargetConsumers(t *testing.T) {
	for _, profile := range []string{"", "backend", "design", "unknown"} {
		consumers := []ProgressionConsumer{}
		if profile != "" {
			consumers = append(consumers, ProgressionConsumer{Profile: profile, Root: filepath.Join(t.TempDir(), "consumer"), CheckpointRef: "work/checkpoint.json"})
		}
		target := ProgressionTarget{SchemaVersion: 1, Kind: "lifecycle-progression-target", Target: "product-design-completed", IntentSource: "user-request", Consumers: consumers}
		raw, err := json.Marshal(target)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = parseProgressionTarget(raw); (err == nil) != (profile == "" || profile == "backend") {
			t.Fatalf("%s: %v", profile, err)
		}
	}
}
