package project

import (
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
)

func TestUpgradeSourceVersionBinding(t *testing.T) {
	t.Parallel()
	commit, snapshot := strings.Repeat("a", 40), strings.Repeat("b", 64)
	for _, tc := range []struct {
		name, binding, recorded string
		missingNative, want     bool
	}{
		{name: "matching-semver", binding: "4.0.1", recorded: "4.0.1", want: true},
		{name: "different-semver", binding: "4.0.1", recorded: "4.0.2"},
		{name: "matching-git", binding: "git:" + commit, recorded: "git:" + commit, want: true},
		{name: "git-binding-rejects-semver", binding: "git:" + commit, recorded: "4.0.1"},
		{name: "semver-binding-rejects-git", binding: "4.0.1", recorded: "git:" + commit},
		{name: "missing-version", binding: "4.0.1"},
		{name: "missing-native", binding: "4.0.1", missingNative: true},
		{name: "unbound-semver", recorded: "4.0.1", want: true},
		{name: "unbound-git", recorded: "git:" + commit, want: true},
		{name: "unbound-missing-version", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := &Identity{Native: &Metadata{TemplateCommit: commit, SnapshotHash: snapshot, TemplateVersion: tc.recorded}}
			if tc.missingNative {
				id.Native = nil
			}
			u := &upgradePlanner{id: id}
			got, err := u.matchesSource(bundle.SourceMatch{Profile: "spec", TemplateCommit: commit, SnapshotHash: snapshot, TemplateVersion: tc.binding})
			if err != nil || got != tc.want {
				t.Fatalf("matchesSource(binding=%q, recorded=%q) = %v, %v; want %v", tc.binding, tc.recorded, got, err, tc.want)
			}
		})
	}
}
