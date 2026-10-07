package project

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func upgradeAsset(p *Plan, ref string) AssetResult {
	for _, a := range p.Assets {
		if a.Path == ref {
			return a
		}
	}
	return AssetResult{}
}
func copyUpgradeBundle(t *testing.T) *bundle.Bundle {
	t.Helper()
	b, e := bundle.Load("spec")
	if e != nil {
		t.Fatal(e)
	}
	b.SchemaVersion = 3
	b.Upgrade = bundle.DefaultUpgradePolicy()
	return b
}

func TestUpgradeFixedRulesPlanDeletionRenameAndMissingBase(t *testing.T) {
	root := lockNativeFixture(t)
	id, e := Detect(root, "spec", false)
	if e != nil {
		t.Fatal(e)
	}
	// Retire a previously distributed fixed configuration file. The archive
	// contains its exact baseline, so no network/source inference is needed.
	for _, action := range []string{"delete", "rename"} {
		b := copyUpgradeBundle(t)
		ref := ".codex/config.toml"
		delete(b.Initial, ref)
		delete(b.Files, ref)
		rule := bundle.MigrationRule{ID: "config-" + action, From: bundle.SourceMatch{Profile: "spec", TemplateCommit: id.Native.TemplateCommit, SnapshotHash: id.Native.SnapshotHash, BundleHash: id.Native.BundleHash}, Action: action, SourcePath: ref}
		if action == "rename" {
			rule.TargetPath = ".codex/yss-config.toml"
			data := []byte("# renamed fixed config\n")
			f := bundle.File{Data: base64.StdEncoding.EncodeToString(data), Digest: safefs.Digest(data), Mode: 0644, Ownership: "managed"}
			b.Files[rule.TargetPath] = f
			b.Initial[rule.TargetPath] = f
		}
		b.Upgrade.Rules = []bundle.MigrationRule{rule}
		b.BundleHash = bundle.Hash(b)
		p, e := BuildWithOptions(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: b})
		if e != nil {
			t.Fatal(e)
		}
		a := upgradeAsset(p, ref)
		if a.Action != action || !a.BaselineAvailable || p.Coverage.Percent != 100 || !p.ReadyToApply {
			t.Fatalf("rule not fully planned: %+v %+v", a, p.Blockers)
		}
		ops := []transaction.Operation{}
		for _, c := range p.Changes {
			if c.Path == ref || c.Path == rule.TargetPath {
				before := c.Before
				ops = append(ops, transaction.Operation{Path: c.Path, Data: mustDecode(c.Data), Mode: c.After.Mode, Delete: c.After.Type == "missing", Before: &before})
			}
		}
		before, _ := os.ReadFile(filepath.Join(root, ref))
		if _, e = transaction.Apply(root, "sync", ops); e != nil {
			t.Fatal(e)
		}
		if _, e = transaction.Rollback(root); e != nil {
			t.Fatal(e)
		}
		after, _ := os.ReadFile(filepath.Join(root, ref))
		if string(after) != string(before) {
			t.Fatal("rule rollback lost exact bytes")
		}
		// Customization or an occupied target requires a bound decision.
		if action == "rename" {
			if e = os.WriteFile(filepath.Join(root, rule.TargetPath), []byte("occupied"), 0644); e != nil {
				t.Fatal(e)
			}
			p, e = BuildWithOptions(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: b})
			if e != nil {
				t.Fatal(e)
			}
			if p.ReadyToApply || upgradeAsset(p, ref).Action != "conflict" || p.Coverage.Percent != 100 {
				t.Fatalf("occupied rename not reported: %+v", p.Public())
			}
			if e = os.Remove(filepath.Join(root, rule.TargetPath)); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e = os.RemoveAll(filepath.Join(root, ".yss")); e != nil {
		t.Fatal(e)
	}
	b := copyUpgradeBundle(t)
	delete(b.Initial, ".codex/config.toml")
	delete(b.Files, ".codex/config.toml")
	b.Upgrade.Rules = []bundle.MigrationRule{{ID: "without-base", From: bundle.SourceMatch{Profile: "spec", TemplateCommit: id.Native.TemplateCommit, SnapshotHash: id.Native.SnapshotHash}, Action: "delete", SourcePath: ".codex/config.toml"}}
	p, e := BuildWithOptions(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: b})
	if e != nil {
		t.Fatal(e)
	}
	if p.ReadyToApply || upgradeAsset(p, ".codex/config.toml").BaselineAvailable {
		t.Fatal("missing historical material authorized automatic deletion")
	}
}

func TestUpgradeEntryUpdateAndThreeWayCandidates(t *testing.T) {
	root := lockNativeFixture(t)
	b := copyUpgradeBundle(t)
	f := b.Initial["AGENTS.md"]
	original := mustDecode(f.Data)
	target := append([]byte("模板上游新增规则。\n"), original...)
	f.Data = base64.StdEncoding.EncodeToString(target)
	f.Digest = safefs.Digest(target)
	b.Initial["AGENTS.md"] = f
	b.Files["AGENTS.md"] = f
	p, e := BuildWithOptions(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: b})
	if e != nil {
		t.Fatal(e)
	}
	if a := upgradeAsset(p, "AGENTS.md"); a.Action != "update" {
		t.Fatalf("pristine customizable entry not updated: %+v", a)
	}
	local, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	local = append(local, []byte("\n本地尾部规则。\n")...)
	if e = os.WriteFile(filepath.Join(root, "AGENTS.md"), local, 0644); e != nil {
		t.Fatal(e)
	}
	p, e = BuildWithOptions(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: b})
	if e != nil {
		t.Fatal(e)
	}
	a := upgradeAsset(p, "AGENTS.md")
	if a.Candidate == nil || !a.Candidate.Clean || p.ReadyToApply {
		t.Fatalf("clean candidate must await decision: %+v", a)
	}
	// Distinct edits to the same line yield a deterministic overlapping candidate.
	c, e := mergeCandidate([]byte("local\n"), []byte("base\n"), []byte("template\n"))
	if e != nil || c == nil || c.Clean {
		t.Fatalf("overlap absent: %+v %v", c, e)
	}
	c2, _ := mergeCandidate([]byte("local\n"), []byte("base\n"), []byte("template\n"))
	if c.Digest != c2.Digest {
		t.Fatal("candidate depends on temporary paths")
	}
	if e = os.RemoveAll(filepath.Join(root, ".yss")); e != nil {
		t.Fatal(e)
	}
	p, e = BuildWithOptions(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: b})
	if e != nil {
		t.Fatal(e)
	}
	a = upgradeAsset(p, "AGENTS.md")
	if a.BaselineAvailable || a.Candidate != nil {
		t.Fatal("missing archive guessed a three-way baseline")
	}
}

func TestUpgradeOldSavedPlansDiagnosticOnly(t *testing.T) {
	p := &Plan{SchemaVersion: 1, ProtocolVersion: 1, Command: "sync", Root: filepath.Join(t.TempDir(), "project"), Profile: "spec"}
	p.Digest = planDigest(p)
	file := filepath.Join(t.TempDir(), "old.json")
	if e := SavePlan(p, file); e != nil {
		t.Fatal(e)
	}
	read, e := ReadPlan(file)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := Apply(read); lockCode(e) != "PLAN_VERSION" {
		t.Fatalf("old plan accepted: %v", e)
	}
}

func TestUpgradeSkillExtraAndRetiredFilesHavePlanningConclusions(t *testing.T) {
	root := lockNativeFixture(t)
	extra := ".agents/skills/yss-product-lifecycle/local-extra.md"
	if e := os.WriteFile(filepath.Join(root, extra), []byte("local note"), 0600); e != nil {
		t.Fatal(e)
	}
	p, e := Build(root, "spec", "sync", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if a := upgradeAsset(p, extra); a.Action != "conflict" || p.ReadyToApply || p.Coverage.Percent != 100 || len(a.Options) != 0 {
		t.Fatal("unregistered Skill file omitted or deletable")
	}
	if e := os.Remove(filepath.Join(root, extra)); e != nil {
		t.Fatal(e)
	}
	cache := ".agents/skills/yss-product-lifecycle/.DS_Store"
	if e := os.WriteFile(filepath.Join(root, cache), []byte("cache"), 0600); e != nil {
		t.Fatal(e)
	}
	p, e = Build(root, "spec", "sync", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if a := upgradeAsset(p, cache); a.Action != "preserve" || !p.ReadyToApply || p.Coverage.Percent != 100 {
		t.Fatal("source-excluded cache caused false conflict")
	}
	if _, e := Apply(p); e != nil {
		t.Fatal(e)
	}
	b := copyUpgradeBundle(t)
	retired := ".agents/skills/yss-product-lifecycle/references/user-decisions.md"
	for _, runtime := range []string{".agents", ".codex", ".cursor", ".pi"} {
		ref := runtime + "/skills/yss-product-lifecycle/references/user-decisions.md"
		delete(b.Initial, ref)
		delete(b.Files, ref)
	}
	p, e = BuildWithOptions(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: b})
	if e != nil {
		t.Fatal(e)
	}
	if a := upgradeAsset(p, retired); a.Action != "conflict" || p.ReadyToApply || p.Coverage.Percent != 100 {
		t.Fatal("retained fixed tree drift postponed until application")
	}
	for _, runtime := range []string{".agents", ".codex", ".cursor", ".pi"} {
		ref := runtime + "/skills/yss-product-lifecycle/references/user-decisions.md"
		if _, e := os.Stat(filepath.Join(root, ref)); e == nil {
			if e = os.Remove(filepath.Join(root, ref)); e != nil {
				t.Fatal(e)
			}
		}
	}
	p, e = BuildWithOptions(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: b})
	if e != nil {
		t.Fatal(e)
	}
	if a := upgradeAsset(p, retired); a.Action != "retired-preserved" || !p.ReadyToApply || p.Coverage.Percent != 100 {
		t.Fatal("already missing retirement caused a fixed tree conflict")
	}
}

func TestUpgradeMissingRetirementCannotKeepLocal(t *testing.T) {
	root := lockNativeFixture(t)
	id, e := Detect(root, "spec", false)
	if e != nil {
		t.Fatal(e)
	}
	b := copyUpgradeBundle(t)
	delete(b.Files, "AGENTS.md")
	delete(b.Initial, "AGENTS.md")
	b.Upgrade.Rules = []bundle.MigrationRule{{ID: "retire-entry", From: bundle.SourceMatch{Profile: "spec", TemplateCommit: id.Native.TemplateCommit, SnapshotHash: id.Native.SnapshotHash}, Action: "delete", SourcePath: "AGENTS.md"}}
	if e = os.Remove(filepath.Join(root, "AGENTS.md")); e != nil {
		t.Fatal(e)
	}
	p, e := BuildWithOptions(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: b})
	if e != nil {
		t.Fatal(e)
	}
	a := upgradeAsset(p, "AGENTS.md")
	if a.Action != "conflict" || len(a.Options) != 1 || a.Options[0] != "use-template" {
		t.Fatalf("missing retirement offered keep-local: %+v", a)
	}
}

func TestUpgradeRejectsOverlappingRetirementRules(t *testing.T) {
	b := copyUpgradeBundle(t)
	from := bundle.SourceMatch{Profile: "spec", TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash}
	b.Upgrade.Rules = []bundle.MigrationRule{{ID: "first-delete", From: from, Action: "delete", SourcePath: "scripts/old-file"}, {ID: "second-delete", From: from, Action: "delete", SourcePath: "scripts/old-file"}}
	if e := b.Upgrade.Validate(b); lockCode(e) != "BUNDLE_RULE" {
		t.Fatal("overlapping rule sources accepted", e)
	}
	b.Upgrade.Rules[1].SourcePath = "scripts/another-old-file"
	for i := range b.Upgrade.Rules {
		b.Upgrade.Rules[i].Action = "rename"
		b.Upgrade.Rules[i].TargetPath = ".codex/config.toml"
	}
	if e := b.Upgrade.Validate(b); lockCode(e) != "BUNDLE_RULE" {
		t.Fatal("overlapping rule destinations accepted", e)
	}
}

func TestUpgradeOfflineBundleRestoresInitialBaseline(t *testing.T) {
	root := lockNativeFixture(t)
	file := filepath.Join(root, "AGENTS.md")
	local, _ := os.ReadFile(file)
	if e := os.WriteFile(file, append(local, []byte("\nlocal custom entry\n")...), 0644); e != nil {
		t.Fatal(e)
	}
	if e := os.RemoveAll(filepath.Join(root, ".yss")); e != nil {
		t.Fatal(e)
	}
	parent, _ := filepath.EvalSymlinks(t.TempDir())
	base := filepath.Join(parent, "offline-bundle")
	if _, e := bundle.Export(context.Background(), "spec", base); e != nil {
		t.Fatal(e)
	}
	p, e := BuildWithOptions(root, "spec", "sync", nil, nil, nil, PlanningOptions{BaseBundlePath: base})
	if e != nil {
		t.Fatal(e)
	}
	a := upgradeAsset(p, "AGENTS.md")
	if !a.BaselineAvailable || a.BaselineSource != "base-bundle" || a.Candidate == nil || !a.Candidate.Clean {
		t.Fatal("export did not restore exact initial variant")
	}
	if _, e := BuildWithOptions(root, "spec", "sync", nil, nil, nil, PlanningOptions{BaseBundlePath: filepath.Join(base, bundle.SnapshotFile)}); e != nil {
		t.Fatal("explicit snapshot file rejected", e)
	}
}
