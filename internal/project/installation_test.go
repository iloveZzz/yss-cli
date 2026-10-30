package project

import (
	"context"
	"encoding/base64"
	"errors"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallationPlanBindingIsExplicit(t *testing.T) {
	for _, bound := range []bool{false, true} {
		b := installationFixture()
		root := filepath.Join(t.TempDir(), "not-created")
		p, err := buildCore(root, "spec", "init", nil, nil, nil, PlanningOptions{target: b, BindCLI: bound})
		if err != nil {
			t.Fatal(err)
		}
		wantSchema := 4
		if bound {
			wantSchema = 5
			if p.Kind != "installation-plan" || p.CLI == nil || p.CLI.ExecutableSHA256 == "" || p.BundleHash != bundle.Hash(b) {
				t.Fatalf("missing source binding: %+v", p)
			}
		} else if p.Kind != "" || p.CLI != nil || p.BundleHash != "" {
			t.Fatal("ordinary plan acquired a new binding requirement")
		}
		if p.SchemaVersion != wantSchema || planningOptionsForReplay(p).BindCLI != bound {
			t.Fatalf("schema/replay changed: %d %+v", p.SchemaVersion, planningOptionsForReplay(p))
		}
		file := filepath.Join(t.TempDir(), "plan.json")
		if err := SavePlan(p, file); err != nil {
			t.Fatal(err)
		}
		read, err := ReadPlan(file)
		if err != nil || read.Digest != p.Digest || read.SchemaVersion != wantSchema {
			t.Fatalf("saved plan round trip: %v %v", read, err)
		}
		if read.Installation != p.Installation || planningOptionsForReplay(read).Installation != read.Installation {
			t.Fatal("saved plan lost installation choice")
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatalf("planning wrote target: %v", err)
		}
	}
}

func TestBoundInstallationRejectsUnconfirmedOrDifferentSource(t *testing.T) {
	b := installationFixture()
	root := filepath.Join(t.TempDir(), "not-created")
	p, err := buildCore(root, "spec", "init", nil, nil, nil, PlanningOptions{target: b, BindCLI: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ digest, code string }{{"", "PLAN_REQUIRED"}, {"different", "INPUT_DRIFT"}} {
		_, err := ApplyConfirmedContext(context.Background(), p, tc.digest)
		var rejection *domain.Error
		if !errors.As(err, &rejection) || rejection.Code != tc.code {
			t.Fatalf("confirmation %q: %v", tc.digest, err)
		}
	}
	if err := verifyInstallationPlanSource(p, b); err != nil {
		t.Fatalf("actual source rejected: %v", err)
	}
	for _, field := range []string{"executable", "bundle", "snapshot"} {
		changed := *p
		cli := *p.CLI
		changed.CLI = &cli
		switch field {
		case "executable":
			changed.CLI.ExecutableSHA256 = strings.Repeat("0", 64)
		case "bundle":
			changed.BundleHash = strings.Repeat("0", 64)
		case "snapshot":
			changed.SnapshotHash = strings.Repeat("0", 64)
		}
		changed.Digest = planDigest(&changed)
		var rejection *domain.Error
		if err := verifyInstallationPlanSource(&changed, b); !errors.As(err, &rejection) || rejection.Code != "INPUT_DRIFT" {
			t.Fatalf("changed %s accepted: %v", field, err)
		}
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("rejection wrote target: %v", err)
	}
}

func TestResolveInstallation(t *testing.T) {
	for _, tc := range []struct {
		command, requested, saved, want string
		old, fail                       bool
	}{
		{"init", "", "", "governed", false, false},
		{"attach", "", "", "governed", false, false},
		{"init", "skills", "", "skills", false, false},
		{"attach", "skills", "", "skills", false, false},
		{"init", "governed", "", "governed", false, false},
		{"attach", "governed", "", "governed", false, false},
		{"doctor", "", "", "skills", false, false},
		{"diff", "", "", "skills", false, false},
		{"migrate", "", "", "skills", false, false},
		{"sync", "", "", "governed", true, false},
		{"sync", "", "skills", "skills", true, false},
		{"doctor", "", "governed", "governed", true, false},
		{"diff", "", "skills", "skills", true, false},
		{"ensure", "", "skills", "skills", true, false},
		{"init", "", "skills", "skills", true, false},
		{"migrate", "", "", "governed", true, false},
		{"migrate", "governed", "skills", "governed", true, false},
		{"attach", "governed", "skills", "", true, true},
		{"sync", "skills", "governed", "", true, true},
		{"init", "unknown", "", "", false, true},
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
	return &bundle.Bundle{SchemaVersion: 5, Profile: "spec", TemplateCommit: strings.Repeat("a", 40), SnapshotHash: strings.Repeat("b", 64), ManifestHash: strings.Repeat("c", 64), BundleHash: strings.Repeat("d", 64), TemplateVersion: "4.7.1", LegacyVersion: "3.5.10", SourceState: "committed", Upgrade: bundle.DefaultUpgradePolicy(), Files: map[string]bundle.File{"AGENTS.md": file("formal"), ".agents/skills/implement/SKILL.md": file("---\nname: implement\ndescription: Implementation entry\n---\n"), skillRegistryRef: file("schema_version: 3\nstatus: active\ncanonical_content_root: .agents/skills\ninvocation_contract:\n  schema_version: 2\nskills:\n  - {id: implement, layer: core, maturity: supported}\n"), "scripts/lib/skill-supply-chain.mjs": file("export const OBSOLETE = new Set([]);\n"), "skills-lock.json": file(`{"version":3,"canonicalRoot":".agents/skills","projectionRoots":[],"skills":{"shared":{"implement":{"skillPath":".agents/skills/implement","targets":[".agents/skills"]}},"platform":{}}}`)}, Installations: map[string]bundle.Installation{
		"skills":   {Initial: map[string]bundle.File{"AGENTS.md": file("engineering"), ".agents/skills/implement/SKILL.md": file("---\nname: implement\ndescription: Implementation entry\n---\n")}, Distribution: map[string]any{"installation": "skills", "mode": "skills", "installedSkills": []string{"implement"}}},
		"governed": {Initial: map[string]bundle.File{"AGENTS.md": file("formal")}, Distribution: map[string]any{"installation": "governed", "mode": "selected"}},
	}}
}
func TestInitInstallationAssets(t *testing.T) {
	for _, profile := range []string{"spec", "backend"} {
		for _, command := range []string{"init", "attach"} {
			for _, requested := range []InstallationKind{"", InstallationSkills} {
				t.Run(profile+"/"+command+"/"+string(requested), func(t *testing.T) {
					b := installationFixture()
					b.Profile = profile
					p, err := buildCore(t.TempDir(), profile, command, nil, nil, nil, PlanningOptions{target: b, Installation: requested})
					if err != nil {
						t.Fatal(err)
					}
					want, entry := InstallationGoverned, "formal"
					if requested == InstallationSkills {
						want, entry = InstallationSkills, "engineering"
					}
					if p.SchemaVersion != 4 || p.Installation != want || p.Profile != domain.Profiles[profile].Name {
						t.Fatalf("wrong plan: %d %s %s", p.SchemaVersion, p.Installation, p.Profile)
					}
					found := false
					for _, c := range p.Changes {
						if c.Path == "AGENTS.md" {
							raw, err := base64.StdEncoding.DecodeString(c.Data)
							if err != nil || string(raw) != entry {
								t.Fatalf("wrong entry: %q %v", raw, err)
							}
							found = true
						}
					}
					if !found {
						t.Fatal("missing entry")
					}
				})
			}
		}
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

func TestRetiredGovernancePlanCannotApply(t *testing.T) {
	p := &Plan{SchemaVersion: 4, Command: "migrate", ReadyToApply: true, GovernanceMigration: &GovernanceMigration{}}
	p.Digest = planDigest(p)
	if err := PreflightApplyContext(context.Background(), p); err == nil || !strings.Contains(err.Error(), "旧治理迁移计划已停止") {
		t.Fatalf("已退役迁移计划必须在写入前拒绝: %v", err)
	}
}
