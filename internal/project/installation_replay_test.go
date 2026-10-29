package project

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

func TestAttachResolutionPreservesExplicitInstallation(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "AGENTS.md")
	const local = "Local project instructions.\n"
	if err := os.WriteFile(entry, []byte(local), 0644); err != nil {
		t.Fatal(err)
	}
	b := installationFixture()
	original, err := BuildWithOptions(root, "spec", "attach", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationSkills})
	if err != nil {
		t.Fatal(err)
	}
	var decision Resolution
	for _, a := range original.Assets {
		if a.Path == "AGENTS.md" {
			decision = Resolution{Path: a.Path, Choice: "keep-local", Before: a.Before, Target: a.Target}
		}
	}
	if len(original.Conflicts) == 0 || decision.Path == "" {
		t.Fatal("custom entry conflict not observed")
	}
	planFile := filepath.Join(t.TempDir(), "plan.json")
	if err := SavePlan(original, planFile); err != nil {
		t.Fatal(err)
	}
	original, err = ReadPlan(planFile)
	if err != nil {
		t.Fatal(err)
	}
	resolutionFile := filepath.Join(t.TempDir(), "resolution.json")
	for _, stale := range []bool{false, true} {
		digest := original.Digest
		if stale {
			digest = strings.Repeat("0", 64)
		}
		raw, err := json.Marshal(ResolutionFile{SchemaVersion: 1, PlanDigest: digest, Decisions: []Resolution{decision}})
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(resolutionFile, raw, 0644); err != nil {
			t.Fatal(err)
		}
		replanned, err := BuildWithOptions(root, "spec", "attach", nil, nil, nil, PlanningOptions{target: b, Original: original, ResolutionFile: resolutionFile})
		if stale {
			var rejection *domain.Error
			if !errors.As(err, &rejection) || rejection.Code != "RESOLUTION_STALE" {
				t.Fatalf("stale resolution accepted: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if replanned.Installation != InstallationSkills || replanned.ResolvedFrom != original.Digest || len(replanned.Conflicts) != 0 {
			t.Fatalf("installation or original binding changed: %+v", replanned)
		}
		for _, c := range replanned.Changes {
			if c.Path == "yss-project.yaml" || c.Path == "CONTEXT.md" || c.Path == "AGENTS.md" {
				t.Fatalf("unexpected change: %s", c.Path)
			}
		}
	}
	if raw, err := os.ReadFile(entry); err != nil || string(raw) != local {
		t.Fatalf("planning changed user entry: %q %v", raw, err)
	}
}
