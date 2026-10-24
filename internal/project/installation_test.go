package project

import (
	"encoding/base64"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"strings"
	"testing"
)

func TestResolveInstallation(t *testing.T) {
	for _, tc := range []struct {
		command, requested, saved, want string
		old, fail                       bool
	}{
		{"init", "", "", "skills", false, false}, {"attach", "governed", "", "governed", false, false},
		{"sync", "", "", "governed", true, false}, {"sync", "", "skills", "skills", true, false},
		{"sync", "skills", "governed", "", true, true}, {"init", "unknown", "", "", false, true},
	} {
		id := &Identity{}
		if tc.old {
			id.Native = &Metadata{Distribution: map[string]any{"installation": tc.saved}}
		}
		got, err := ResolveInstallation(id, tc.command, InstallationKind(tc.requested))
		if (err != nil) != tc.fail || err == nil && string(got) != tc.want {
			t.Fatalf("%+v: %s %v", tc, got, err)
		}
	}
}
func installationFixture() *bundle.Bundle {
	file := func(s string) bundle.File {
		return bundle.File{Data: base64.StdEncoding.EncodeToString([]byte(s)), Digest: safefs.Digest([]byte(s)), Mode: 0644, Ownership: "managed"}
	}
	return &bundle.Bundle{SchemaVersion: 5, Profile: "spec", TemplateCommit: strings.Repeat("a", 40), SnapshotHash: strings.Repeat("b", 64), ManifestHash: strings.Repeat("c", 64), BundleHash: strings.Repeat("d", 64), TemplateVersion: "4.7.1", LegacyVersion: "3.5.10", SourceState: "committed", Upgrade: bundle.DefaultUpgradePolicy(), Files: map[string]bundle.File{"AGENTS.md": file("formal"), ".agents/skills/implement/SKILL.md": file("implement"), "skills-lock.json": file(`{"version":3,"skills":{"shared":{"implement":{"skillPath":".agents/skills/implement"}},"platform":{}}}`)}, Installations: map[string]bundle.Installation{
		"skills":   {Initial: map[string]bundle.File{"AGENTS.md": file("engineering"), ".agents/skills/implement/SKILL.md": file("implement")}, Distribution: map[string]any{"installation": "skills", "mode": "skills", "installedSkills": []string{"implement"}}},
		"governed": {Initial: map[string]bundle.File{"AGENTS.md": file("formal")}, Distribution: map[string]any{"installation": "governed", "mode": "selected"}},
	}}
}
func TestInitInstallationAssets(t *testing.T) {
	for _, profile := range []string{"spec", "backend"} {
		t.Run(profile, func(t *testing.T) {
			b := installationFixture()
			b.Profile = profile
			p, err := buildCore(t.TempDir(), profile, "init", nil, nil, nil, PlanningOptions{target: b})
			if err != nil {
				t.Fatal(err)
			}
			if p.SchemaVersion != 4 || p.Installation != InstallationSkills {
				t.Fatalf("wrong plan: %d %s", p.SchemaVersion, p.Installation)
			}
			for _, c := range p.Changes {
				if c.Path == "yss-project.yaml" || strings.HasPrefix(c.Path, ".template-spec/") {
					t.Fatalf("generated governance %s", c.Path)
				}
			}
			if p.Profile != domain.Profiles[profile].Name {
				t.Fatal("profile changed")
			}
		})
	}
}

func TestInstallationBaseMaterialUsesSelectedEntry(t *testing.T) {
	root := t.TempDir()
	b := installationFixture()
	reader, err := transaction.NewMaterialReader(root)
	if err != nil {
		t.Fatal(err)
	}
	u := &upgradePlanner{base: b, archive: reader, plan: &Plan{Variables: map[string]string{}}}
	m := Managed{Baseline: domain.Descriptor{Type: "file", Digest: safefs.Digest([]byte("engineering")), Mode: 0644}}
	raw, _, ok, err := u.baseMaterial("AGENTS.md", m)
	if err != nil || !ok || string(raw) != "engineering" {
		t.Fatalf("ordinary baseline lost: %q %v %v", raw, ok, err)
	}
}

func TestSyncPreservesInstallation(t *testing.T) {
	b, err := installationFixture().ForInstallation("governed")
	if err != nil {
		t.Fatal(err)
	}
	id := &Identity{Profile: domain.Profiles["spec"], Native: &Metadata{SchemaVersion: 4, Distribution: map[string]any{"mode": "selected", "installedSkills": []string{}}}}
	got, err := distributionFor(id, b, nil)
	if err != nil || got["installation"] != "governed" {
		t.Fatalf("old governed installation lost: %v %v", got, err)
	}
}

func TestSkillsDerivedLockHasGeneratedOwnership(t *testing.T) {
	f, err := selectedLock(installationFixture(), map[string]any{"mode": "skills", "installedSkills": []string{"implement"}})
	if err != nil {
		t.Fatal(err)
	}
	if f.Ownership != "generated" {
		t.Fatalf("derived lock ownership %s", f.Ownership)
	}
}
func TestLegacyMigrateReplayDoesNotRequestInstallationChange(t *testing.T) {
	p := &Plan{Command: "migrate", Installation: InstallationGoverned, GovernanceMigration: &GovernanceMigration{}}
	if got := planningOptionsForReplay(p); got.Installation != "" {
		t.Fatal("legacy migrate replay requested installation conversion")
	}
	p.InstallationMigration = &InstallationMigration{Source: InstallationGoverned, Target: InstallationSkills}
	p.Installation = InstallationSkills
	if got := planningOptionsForReplay(p); got.Installation != InstallationSkills {
		t.Fatal("explicit migration lost its target")
	}
}
