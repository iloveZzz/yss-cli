package release

import "testing"

func TestScopedUpstreamCommandAllowsIndependentPinnedCheckout(t *testing.T) {
	for _, row := range []struct {
		name string
		args []string
		want bool
	}{
		{"bundle-checkout", []string{"scripts/verify-upstream-skill-source", "--source=iloveZzz/yss-harness-design-agent", "--source-root=/fixed/source/submodules/yss-harness-design-agent"}, true},
		{"imported-skill-pin", []string{"scripts/verify-upstream-skill-source", "--source=iloveZzz/yss-harness-design-agent", "--source-root=/fixed/imported-skill-pin"}, true},
		{"empty", []string{"scripts/verify-upstream-skill-source", "--source=iloveZzz/yss-harness-design-agent", "--source-root="}, false},
		{"relative", []string{"scripts/verify-upstream-skill-source", "--source=iloveZzz/yss-harness-design-agent", "--source-root=../skill-pin"}, false},
		{"noncanonical", []string{"scripts/verify-upstream-skill-source", "--source=iloveZzz/yss-harness-design-agent", "--source-root=/fixed/../skill-pin"}, false},
		{"wrong-option", []string{"scripts/verify-upstream-skill-source", "--source=iloveZzz/yss-harness-design-agent", "--root=/fixed/skill-pin"}, false},
		{"wrong-source", []string{"scripts/verify-upstream-skill-source", "--source=other/source", "--source-root=/fixed/skill-pin"}, false},
		{"extra-argument", []string{"scripts/verify-upstream-skill-source", "--source=iloveZzz/yss-harness-design-agent", "--source-root=/fixed/skill-pin", "--skip-hashes"}, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			if got := scopedCommand(8, row.args, "/fixed/source"); got != row.want {
				t.Fatalf("got %v, want %v", got, row.want)
			}
		})
	}
}
