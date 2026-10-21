package project

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

// The historical source is input to retirement only, never a valid current
// runtime or a fixture passed through the current Bundle producer.
func workBuddyRootMigrationProject(t *testing.T) (*Identity, *bundle.Bundle, *bundle.Bundle) {
	t.Helper()
	id, target := workBuddyProject(t)
	target.SourceState = "committed"
	id.Native.TemplateCommit = strings.Repeat("a", 40)
	id.Native.SnapshotHash = strings.Repeat("b", 64)
	id.Native.BundleHash = strings.Repeat("c", 64)
	old := &bundle.Bundle{Files: map[string]bundle.File{}}
	for ref, file := range target.Files {
		oldRef := strings.Replace(ref, ".workbuddy/skills/", ".codebuddy/skills/", 1)
		if ref == "skills-lock.json" {
			raw := []byte(strings.ReplaceAll(string(mustDecode(file.Data)), ".workbuddy/skills", ".codebuddy/skills"))
			file.Data, file.Digest = base64.StdEncoding.EncodeToString(raw), safefs.Digest(raw)
		}
		old.Files[oldRef] = file
		if oldRef != ref {
			if err := os.Remove(filepath.Join(id.Root, ref)); err != nil {
				t.Fatal(err)
			}
			delete(id.Native.Managed, ref)
		}
		p := filepath.Join(id.Root, oldRef)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, mustDecode(file.Data), os.FileMode(file.Mode)); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, os.FileMode(file.Mode)); err != nil {
			t.Fatal(err)
		}
		d, err := safefs.Describe(id.Root, oldRef)
		if err != nil {
			t.Fatal(err)
		}
		id.Native.Managed[oldRef] = Managed{Applied: d, Baseline: d, Ownership: file.Ownership, Source: &identitymeta.BaselineSource{Kind: "bundle", TemplateCommit: id.Native.TemplateCommit, SnapshotHash: id.Native.SnapshotHash, BundleHash: id.Native.BundleHash, Path: oldRef, Variant: "full", TemplateDigest: file.Digest}}
	}
	if err := os.RemoveAll(filepath.Join(id.Root, ".workbuddy")); err != nil {
		t.Fatal(err)
	}
	return id, old, target
}

func workBuddyMigrationPlan(t *testing.T, id *Identity, old, target *bundle.Bundle, command string) (*Plan, map[string]Managed) {
	t.Helper()
	p := &Plan{Root: id.Root, Command: command, Inputs: map[string]domain.Descriptor{}, Variables: map[string]string{}}
	u, err := newUpgradePlanner(id, target, p, id.Native.Managed, PlanningOptions{})
	if err != nil {
		t.Fatal(err)
	}
	u.base = old // Exact old material; production binds --base-bundle before use.
	managed, err := u.planAssets(target.Files, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = finalizePlan(p); err != nil {
		t.Fatal(err)
	}
	return p, managed
}

func TestWorkBuddyRootMigrationRequiresSync(t *testing.T) {
	id, _, target := workBuddyRootMigrationProject(t)
	for _, command := range []string{"skills", "assets"} {
		if err := requireWorkBuddyRootSync(id, target, command); err == nil {
			t.Fatalf("%s allowed implicit root migration", command)
		}
	}
	for _, command := range []string{"sync", "diff", "doctor"} {
		if err := requireWorkBuddyRootSync(id, target, command); err != nil {
			t.Fatalf("%s: %v", command, err)
		}
	}
	if err := os.WriteFile(filepath.Join(id.Root, "skills-lock.json"), []byte("changed lock"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := requireWorkBuddyRootSync(id, target, "skills"); err == nil {
		t.Fatal("lock drift bypassed explicit sync")
	}
	if err := requireWorkBuddyRootSync(id, target, "sync"); err == nil {
		t.Fatal("drifted lock allowed sync to abandon old projection")
	}
	fresh, current := workBuddyProject(t)
	current.SourceState = "committed"
	if err := requireWorkBuddyRootSync(fresh, current, "skills"); err != nil {
		t.Fatalf("current-root ensure blocked: %v", err)
	}
}

func TestWorkBuddyRootMigrationRejectsUntrustedLockResolution(t *testing.T) {
	for _, kind := range []string{"bytes", "mode", "source", "ownership", "unlocked-tree", "file-source", "file-unavailable"} {
		t.Run(kind, func(t *testing.T) {
			id, old, target := workBuddyRootMigrationProject(t)
			switch kind {
			case "bytes":
				if err := os.WriteFile(filepath.Join(id.Root, "skills-lock.json"), []byte("changed lock"), 0644); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(filepath.Join(id.Root, "skills-lock.json"), 0600); err != nil {
					t.Fatal(err)
				}
			case "source":
				id.Native.Managed["skills-lock.json"].Source.BundleHash = strings.Repeat("d", 64)
			case "ownership":
				m := id.Native.Managed["skills-lock.json"]
				m.Ownership = "protected"
				id.Native.Managed["skills-lock.json"] = m
			case "unlocked-tree":
				id.Native.Managed[".codebuddy/skills/unknown/SKILL.md"] = id.Native.Managed[".codebuddy/skills/probe/SKILL.md"]
			case "file-source":
				id.Native.Managed[".codebuddy/skills/probe/payload.txt"].Source.BundleHash = strings.Repeat("d", 64)
			case "file-unavailable":
				id.Native.Managed[".codebuddy/skills/probe/payload.txt"].Source.Kind = "unavailable"
			}
			if err := requireWorkBuddyRootSync(id, target, "sync"); err == nil || !strings.Contains(err.Error(), "单文件 use-template") {
				t.Fatalf("missing non-resolvable migration blocker for %s: %v", kind, err)
			}
			// A single lock resolution would otherwise overwrite the lock and
			// permanently move metadata past the old cleanup authority.
			if kind == "bytes" {
				p, _ := workBuddyMigrationPlan(t, id, old, target, "sync")
				if !containsString(p.Conflicts, "skills-lock.json") {
					t.Fatal("fixture lacks lock use-template conflict")
				}
			}
		})
	}
}

func TestWorkBuddyRootSyncRetiresOnlyManagedSkills(t *testing.T) {
	for _, profile := range []string{"spec", "backend"} {
		t.Run(profile, func(t *testing.T) {
			id, old, target := workBuddyRootMigrationProject(t)
			id.Profile, _ = domain.GetProfile(profile)
			target.Profile = profile
			if profile == "backend" {
				id.Native.Distribution["mode"] = "profile-full"
			}
			personal := map[string]string{".codebuddy/settings.json": "private settings", ".codebuddy/skills/personal/SKILL.md": "my own skill"}
			for ref, text := range personal {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(id.Root, ref)), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(id.Root, ref), []byte(text), 0644); err != nil {
					t.Fatal(err)
				}
			}
			p, managed := workBuddyMigrationPlan(t, id, old, target, "sync")
			if !p.ReadyToApply {
				t.Fatalf("migration blocked: %+v", p.Blockers)
			}
			deleted := 0
			for _, asset := range p.Assets {
				if strings.HasPrefix(asset.Path, ".codebuddy/skills/probe/") {
					if asset.Action != "delete" {
						t.Fatalf("old asset not retired: %+v", asset)
					}
					deleted++
				}
			}
			if deleted != 2 {
				t.Fatalf("retired %d files, want 2", deleted)
			}
			for ref := range managed {
				if strings.HasPrefix(ref, ".codebuddy/") {
					t.Fatalf("old active record retained: %s", ref)
				}
			}
			for ref, want := range personal {
				if raw, err := os.ReadFile(filepath.Join(id.Root, ref)); err != nil || string(raw) != want {
					t.Fatalf("user file changed: %s", ref)
				}
				for _, change := range p.Changes {
					if change.Path == ref {
						t.Fatalf("user path in migration: %s", ref)
					}
				}
			}
		})
	}
}

func TestWorkBuddyRootMigrationConflicts(t *testing.T) {
	for _, kind := range []string{"bytes", "mode", "missing-baseline", "target", "foreign"} {
		t.Run(kind, func(t *testing.T) {
			id, old, target := workBuddyRootMigrationProject(t)
			ref := ".codebuddy/skills/probe/payload.txt"
			switch kind {
			case "bytes":
				if err := os.WriteFile(filepath.Join(id.Root, ref), []byte("local change"), 0644); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(filepath.Join(id.Root, ref), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-baseline":
				old = nil
			case "target":
				ref = ".workbuddy/skills/probe/payload.txt"
				if err := os.MkdirAll(filepath.Dir(filepath.Join(id.Root, ref)), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(id.Root, ref), []byte("occupied"), 0644); err != nil {
					t.Fatal(err)
				}
			case "foreign":
				ref = ".codebuddy/skills/probe/personal.txt"
				if err := os.WriteFile(filepath.Join(id.Root, ref), []byte("personal"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			before, err := safefs.Describe(id.Root, ref)
			if err != nil {
				t.Fatal(err)
			}
			p, _ := workBuddyMigrationPlan(t, id, old, target, "sync")
			if p.ReadyToApply || !containsString(p.Conflicts, ref) {
				t.Fatalf("%s not blocked: %+v", kind, p.Assets)
			}
			after, err := safefs.Describe(id.Root, ref)
			if err != nil || after != before {
				t.Fatalf("planning changed %s", ref)
			}
		})
	}
}

func TestWorkBuddyRootMigrationRollback(t *testing.T) {
	id, old, target := workBuddyRootMigrationProject(t)
	settings := filepath.Join(id.Root, ".codebuddy/settings.json")
	if err := os.WriteFile(settings, []byte("personal"), 0600); err != nil {
		t.Fatal(err)
	}
	metadata := []byte("old metadata\n")
	if err := os.WriteFile(filepath.Join(id.Root, MetadataFile), metadata, 0644); err != nil {
		t.Fatal(err)
	}
	p, _ := workBuddyMigrationPlan(t, id, old, target, "sync")
	before, _ := safefs.Describe(id.Root, MetadataFile)
	changed := []byte("new metadata\n")
	p.Changes = append(p.Changes, Change{Path: MetadataFile, Before: before, After: domain.Descriptor{Type: "file", Mode: 0644, Digest: safefs.Digest(changed)}, Data: base64.StdEncoding.EncodeToString(changed)})
	ops, err := nativePlanOperations(p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = transaction.ApplyContextWithValidation(context.Background(), id.Root, "sync", ops, p.Inputs, nil, func() error { return errors.New("verification failed") })
	if err == nil {
		t.Fatal("failed validation accepted")
	}
	for ref, file := range old.Files {
		actual, err := safefs.Describe(id.Root, ref)
		if err != nil || actual.Digest != file.Digest || actual.Mode != file.Mode {
			t.Fatalf("rollback lost %s", ref)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(id.Root, MetadataFile)); string(raw) != string(metadata) {
		t.Fatal("metadata not restored")
	}
	if raw, _ := os.ReadFile(settings); string(raw) != "personal" {
		t.Fatal("user settings changed")
	}
	if _, err := os.Lstat(filepath.Join(id.Root, ".workbuddy")); !os.IsNotExist(err) {
		t.Fatal("new projection directory survived rollback")
	}
}

func TestWorkBuddyRootMigrationKeepsExplicitLocalException(t *testing.T) {
	id, old, target := workBuddyRootMigrationProject(t)
	ref := ".codebuddy/skills/probe/payload.txt"
	local := []byte("custom resource\n")
	if err := os.WriteFile(filepath.Join(id.Root, ref), local, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(id.Root, ref), 0600); err != nil {
		t.Fatal(err)
	}
	p, _ := workBuddyMigrationPlan(t, id, old, target, "sync")
	var decision Resolution
	for _, asset := range p.Assets {
		if asset.Path == ref {
			decision = Resolution{Path: ref, Choice: "keep-local", Before: asset.Before, Target: asset.Target, RuleID: asset.RuleID}
		}
	}
	if decision.Path == "" || decision.RuleID == "" {
		t.Fatal("retirement conflict missing")
	}
	p = &Plan{Root: id.Root, Command: "sync", Inputs: map[string]domain.Descriptor{}, Variables: map[string]string{}}
	u, err := newUpgradePlanner(id, target, p, id.Native.Managed, PlanningOptions{Resolutions: []Resolution{decision}})
	if err != nil {
		t.Fatal(err)
	}
	u.base = old
	managed, err := u.planAssets(target.Files, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := finalizePlan(p); err != nil {
		t.Fatal(err)
	}
	if !p.ReadyToApply {
		t.Fatalf("keep-local not resolved: %+v", p.Blockers)
	}
	ops, err := nativePlanOperations(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = transaction.ApplyContextWithValidation(context.Background(), id.Root, "sync", ops, p.Inputs, nil, func() error {
		return verifySkillTrees(id.Root, &Metadata{Managed: managed, Distribution: id.Native.Distribution})
	}); err != nil {
		t.Fatal(err)
	}
	if actual, err := safefs.Describe(id.Root, ref); err != nil || actual.Digest != safefs.Digest(local) || actual.Mode != 0600 {
		t.Fatal("customized retired file changed")
	}
	if m := managed[ref]; m.Disposition == nil || m.Disposition.Choice != "keep-local" {
		t.Fatal("retirement exception not recorded")
	}
	id.Native.Managed = managed
	p, _ = workBuddyMigrationPlan(t, id, target, target, "sync")
	if !p.ReadyToApply || len(p.Changes) != 0 {
		t.Fatalf("repeat sync changed migrated tree: %+v", p.Assets)
	}
	id.Native.TemplateCommit, id.Native.SnapshotHash, id.Native.BundleHash = target.TemplateCommit, target.SnapshotHash, target.BundleHash
	if err := requireWorkBuddyRootSync(id, target, "skills"); err != nil {
		t.Fatalf("retired keep-local exception blocked current ensure: %v", err)
	}
}
