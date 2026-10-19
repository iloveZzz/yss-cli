package identitymeta

import (
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"strings"
	"testing"
)

func TestNativeMetadataVersionPairs(t *testing.T) {
	for _, tc := range []struct {
		meta, bundle int
		profile      string
		ok           bool
	}{{3, 3, "design", false}, {4, 4, "spec", true}, {4, 4, "backend", true}, {4, 3, "spec", false}, {3, 4, "spec", false}, {4, 4, "design", false}, {5, 4, "spec", false}} {
		t.Run(tc.profile+string(rune('0'+tc.meta))+string(rune('0'+tc.bundle)), func(t *testing.T) {
			profiles := map[string]string{"spec": "harness.spec-template", "backend": "harness.backend-delivery", "design": "harness.business-ddd-strategy-handoff"}
			managed := map[string]Managed{}
			raw, _ := json.Marshal(managed)
			m := Metadata{SchemaVersion: tc.meta, BundleSchemaVersion: tc.bundle, ProtocolVersion: 1, Profile: tc.profile, ProfileID: profiles[tc.profile], CLIVersion: "2.4.0", TemplateVersion: "4.5.0", LegacyCLIVersion: "3.5.10", TemplateCommit: strings.Repeat("a", 40), TemplateSourceState: "committed", CLISourceState: "unknown", SnapshotHash: strings.Repeat("b", 64), ManifestHash: strings.Repeat("c", 64), BundleHash: strings.Repeat("d", 64), Managed: managed, BaselineDigest: safefs.Digest(raw), Variables: map[string]string{}, Distribution: map[string]any{}}
			data, _ := json.Marshal(m)
			_, err := ValidateNative(t.TempDir(), data, "")
			if (err == nil) != tc.ok {
				t.Fatalf("metadata %d bundle %d %s: %v", tc.meta, tc.bundle, tc.profile, err)
			}
		})
	}
}
