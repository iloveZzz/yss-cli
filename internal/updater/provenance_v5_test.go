package updater

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
)

func TestV5ArchiveProvenanceKeepsStrictProfileBinding(t *testing.T) {
	for _, scenario := range []string{"current", "bundle5", "committed-source", "profile-working-tree", "unknown-source-state", "source-tree", "future-inspection", "extra-design", "missing-backend", "old-lock", "old-inspection", "working-tree", "missing-backend-source"} {
		hash, commit := strings.Repeat("a", 64), strings.Repeat("b", 40)
		producer := bundle.Provenance{Version: "bundle-producer-v4", Commit: commit, SourceState: "committed"}
		lock := bundle.SourceLock{SchemaVersion: 5, Producer: producer, Profiles: map[string]bundle.Source{}}
		m := Manifest{CLICommit: commit, BinarySHA256: hash, Files: map[string]domain.Descriptor{fileName(): {Digest: hash}}, Bundles: map[string]*bundle.Inspection{}}
		for _, profile := range []string{"spec", "backend"} {
			s := bundle.Source{Profile: profile, SourcePath: ".", Commit: commit, TemplateVersion: "4.6.1", TemplateTag: "v4.6.1", PolicyPath: ".template-source/distribution/bundle-profile.json", PolicyHash: hash}
			if profile == "backend" {
				s.OverlayPaths = []string{"profiles/backend"}
			}
			lock.Profiles[profile] = s
			m.Bundles[profile] = &bundle.Inspection{SchemaVersion: 4, Profile: profile, TemplateVersion: "4.6.1", TemplateCommit: commit, SourceState: "committed", Producer: producer, SourcePolicy: bundle.PolicyProvenance{Kind: "committed", Path: s.PolicyPath, Digest: hash}, SnapshotHash: hash, ManifestHash: hash, BundleHash: hash}
		}
		switch scenario {
		case "committed-source", "profile-working-tree", "unknown-source-state", "source-tree":
			s := lock.Profiles["spec"]
			s.State = "committed"
			if scenario == "profile-working-tree" {
				s.State = "working-tree"
			}
			if scenario == "unknown-source-state" {
				s.State = "unknown"
			}
			if scenario == "source-tree" {
				s.Tree = hash
			}
			lock.Profiles["spec"] = s
		case "bundle5":
			for _, inspection := range m.Bundles {
				inspection.SchemaVersion = 5
			}
		case "future-inspection":
			m.Bundles["spec"].SchemaVersion = 6
		case "extra-design":
			lock.Profiles["design"] = bundle.Source{Profile: "design"}
		case "missing-backend":
			delete(m.Bundles, "backend")
		case "old-lock":
			lock.SchemaVersion = 3
		case "old-inspection":
			m.Bundles["spec"].SchemaVersion = 3
		case "missing-backend-source":
			s := lock.Profiles["backend"]
			s.OverlayPaths = nil
			lock.Profiles["backend"] = s
		case "working-tree":
			lock.Producer.SourceState = "working-tree"
		}
		raw, _ := json.Marshal(lock)
		err := validateProvenance(m, raw)
		if (err == nil) != (scenario == "current" || scenario == "bundle5" || scenario == "committed-source") {
			t.Errorf("%s: %v", scenario, err)
		}
	}
}
