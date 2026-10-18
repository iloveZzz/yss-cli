package release

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTwoProfileIdentityRequiresExactSources(t *testing.T) {
	hash, commit := strings.Repeat("a", 64), strings.Repeat("b", 40)
	b := BundleIdentity{TemplateVersion: "4.6.1", TemplateCommit: commit, SourceState: "committed", SourceSnapshotHash: hash, ManifestHash: hash, BundleHash: hash}
	id := Identity{CLIVersion: "2.5.0", ProtocolVersion: 1, CLICommit: commit, SourceState: "committed", SourceLockSHA256: hash, Bundles: map[string]BundleIdentity{"spec": b, "backend": b}}
	if err := validIdentity(id, id); err != nil {
		t.Fatalf("Spec/Backend identity rejected: %v", err)
	}
	id.Bundles = map[string]BundleIdentity{"spec": b, "other": b}
	if err := validIdentity(id, id); err == nil {
		t.Fatal("unknown source substituted for Backend")
	}
}

func TestInspectionSchemaMatchesFixedLock(t *testing.T) {
	hash, commit := strings.Repeat("a", 64), strings.Repeat("b", 40)
	for _, tc := range []struct {
		lock, schema int
		valid        bool
	}{{5, 4, true}, {5, 3, false}, {3, 3, true}, {3, 4, false}, {2, 2, true}, {2, 4, false}} {
		value := map[string]any{"schemaVersion": tc.schema, "profile": "spec", "manifest": map[string]any{"name": "spec"}, "distribution": map[string]any{}, "files": map[string]any{"README.md": map[string]any{"digest": hash, "mode": 420, "size": 1, "ownership": "managed"}}, "initialPaths": []string{"README.md"}, "producer": map[string]any{"sourceState": "committed", "commit": commit, "version": "bundle-producer-v4"}, "sourcePolicy": map[string]any{"kind": "committed", "digest": hash, "path": ".template-source/distribution/bundle-profile.json"}}
		raw, _ := json.Marshal(value)
		err := completeInspection(raw, "spec", tc.lock)
		if (err == nil) != tc.valid {
			t.Errorf("lock%d Bundle%d: %v", tc.lock, tc.schema, err)
		}
	}
}
