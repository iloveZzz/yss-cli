package governance

import (
	"context"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

func TestCurrentProgressionRetiresDesignConsumer(t *testing.T) {
	for _, tc := range []struct {
		policy  int
		profile string
		ok      bool
	}{{1, "design", true}, {2, "design", false}, {2, "backend", true}} {
		s := kernel.NewSemanticSession(context.Background(), t.TempDir(), nil)
		s.GovernanceVersion = tc.policy
		err := progressionCurrentConsumers(s, ProgressionTarget{Consumers: []ProgressionConsumer{{Profile: tc.profile}}})
		if (err == nil) != tc.ok {
			t.Fatalf("policy=%d profile=%s: %v", tc.policy, tc.profile, err)
		}
	}
}
