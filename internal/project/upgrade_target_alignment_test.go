package project

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestUpgradeTargetAlignment(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, kind, local, action string
		mode                      os.FileMode
		selectedLock              bool
	}{
		{name: "fixed-at-target", kind: "fixed", local: "target\n", mode: 0644, action: "unchanged"},
		{name: "generated-at-target", kind: "generated", local: "target\n", mode: 0644, action: "unchanged"},
		{name: "fixed-content-differs", kind: "fixed", local: "custom\n", mode: 0644, action: "conflict"},
		{name: "generated-content-differs", kind: "generated", local: "custom\n", mode: 0644, action: "conflict"},
		{name: "fixed-mode-differs", kind: "fixed", local: "target\n", mode: 0755, action: "conflict"},
		{name: "generated-mode-differs", kind: "generated", local: "target\n", mode: 0755, action: "conflict"},
		{name: "selected-lock-at-target", kind: "generated", local: "target\n", mode: 0644, selectedLock: true, action: "conflict"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, ref := t.TempDir(), "asset.txt"
			if tc.selectedLock {
				ref = "skills-lock.json"
			}
			path := filepath.Join(root, ref)
			if err := os.WriteFile(path, []byte(tc.local), tc.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, tc.mode); err != nil {
				t.Fatal(err)
			}
			ownership := "managed"
			if tc.kind == "generated" {
				ownership = "generated"
			}
			previous := domain.Descriptor{Type: "file", Digest: safefs.Digest([]byte("registered\n")), Mode: 0644}
			old := map[string]Managed{ref: {Baseline: previous, Applied: previous, Ownership: ownership}}
			id := &Identity{Root: root, Profile: domain.Profile{Name: "spec"}, Native: &Metadata{Managed: old}}
			target := bundle.File{Data: base64.StdEncoding.EncodeToString([]byte("target\n")), Digest: safefs.Digest([]byte("target\n")), Mode: 0644, Ownership: ownership}
			b := &bundle.Bundle{
				Profile: "spec", TemplateCommit: strings.Repeat("a", 40), SnapshotHash: strings.Repeat("b", 64), BundleHash: strings.Repeat("c", 64),
				Files:   map[string]bundle.File{ref: target},
				Upgrade: &bundle.UpgradePolicy{SchemaVersion: 1, Assets: []bundle.AssetPolicy{{Paths: []string{ref}, Kind: tc.kind, Generator: "native-bundle"}}},
			}
			p := &Plan{Root: root, Command: "sync", Inputs: map[string]domain.Descriptor{}, Variables: map[string]string{}}
			u, err := newUpgradePlanner(id, b, p, old, PlanningOptions{})
			if err != nil {
				t.Fatal(err)
			}
			managed, err := u.planAssets(b.Files, tc.selectedLock)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Assets) != 1 || p.Assets[0].Action != tc.action {
				t.Fatalf("assets = %+v; want one %s asset", p.Assets, tc.action)
			}
			if len(p.Changes) != 0 {
				t.Fatalf("planning aligned or conflicting assets must not schedule writes: %+v", p.Changes)
			}
			if tc.action == "conflict" {
				if len(p.Conflicts) != 1 || p.Conflicts[0] != ref {
					t.Fatalf("conflicts = %v; want [%s]", p.Conflicts, ref)
				}
				return
			}
			if len(p.Conflicts) != 0 {
				t.Fatalf("aligned asset still conflicts: %v", p.Conflicts)
			}
			m := managed[ref]
			if m.Baseline != p.Assets[0].Target || m.Applied != p.Assets[0].Target || m.Source == nil || m.Source.TemplateCommit != b.TemplateCommit || m.Source.BundleHash != b.BundleHash {
				t.Fatalf("aligned asset did not adopt target registration: %+v", m)
			}
		})
	}
}
