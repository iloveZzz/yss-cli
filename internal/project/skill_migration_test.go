package project

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

// This acceptance seam uses a real historical CLI and its committed Bundle,
// rather than removing current policy from a synthetic new instance.
func TestNativeExplicitSyncRetiresUpgradeSkillAndPreservesBusiness(t *testing.T) {
	oldBinary := os.Getenv("YSS_OLD_NATIVE_BINARY")
	if oldBinary == "" {
		t.Skip("requires the fixed historical native CLI; a skip is not migration acceptance")
	}
	root := freshRoot(t)
	if out, err := exec.Command(oldBinary, "init", "--profile", "spec", "--root", root, "--project-name", "旧实例显式同步", "--json").CombinedOutput(); err != nil {
		t.Fatalf("historical native init: %v %s", err, out)
	}
	oldRef := ".agents/skills/yss-harness-upgrade/SKILL.md"
	oldBytes, err := os.ReadFile(filepath.Join(root, oldRef))
	if err != nil {
		t.Fatal("historical Bundle did not install its original entry", err)
	}
	oldMetadata, err := os.ReadFile(filepath.Join(root, MetadataFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{".work/legacy/checkpoint.yaml", ".work/legacy/gates/approval.yaml", ".work/legacy/package/manifest.json", ".work/legacy/receipt.json"} {
		file := filepath.Join(root, ref)
		if err = os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(file, []byte("immutable historical protocol fixture: "+ref+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before := lockTree(t, root, false)
	readonlyBefore := lockTree(t, root, true)
	p, err := Build(root, "spec", "sync", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !p.ReadyToApply || upgradeAsset(p, oldRef).Action != "delete" {
		t.Fatalf("documented pristine retirement was not planned: %+v", p.Public())
	}
	if !reflect.DeepEqual(readonlyBefore, lockTree(t, root, true)) {
		t.Fatal("saved sync planning wrote files or transaction evidence")
	}
	if _, err = Apply(p); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, oldRef)); !os.IsNotExist(err) {
		t.Fatal("retired skill remains discoverable", err)
	}
	if _, err = os.Stat(filepath.Join(root, ".agents/skills/setup-yss-harness/SKILL.md")); err != nil {
		t.Fatal("replacement was not installed", err)
	}
	id, err := Detect(root, "spec", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(stringsOf(id.Native.Distribution["installedSkills"]), ","), "yss-harness-upgrade") {
		t.Fatal("old entry remained in current selection")
	}
	after := lockTree(t, root, false)
	for ref, descriptor := range before {
		if strings.HasPrefix(ref, ".work/") && after[ref] != descriptor {
			t.Fatal("sync changed historical business evidence", ref)
		}
	}
	repeat, err := Build(root, "spec", "sync", nil, nil)
	if err != nil || !repeat.ReadyToApply || len(repeat.Changes) != 0 {
		t.Fatal("repeat sync was not stable", err)
	}
	if _, err = transaction.Rollback(root); err != nil {
		t.Fatal(err)
	}
	if got := lockTree(t, root, false); !reflect.DeepEqual(before, got) {
		t.Fatal("rollback did not restore original files and modes")
	}
	if raw, err := os.ReadFile(filepath.Join(root, MetadataFile)); err != nil || string(raw) != string(oldMetadata) {
		t.Fatal("rollback did not restore original native metadata bytes", err)
	}

	t.Run("dirty-retired-file", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(root, oldRef), append(append([]byte{}, oldBytes...), []byte("\nlocal customization\n")...), 0644); err != nil {
			t.Fatal(err)
		}
		dirty := lockTree(t, root, true)
		plan, err := Build(root, "spec", "sync", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		asset := upgradeAsset(plan, oldRef)
		if plan.ReadyToApply || asset.Action != "conflict" {
			t.Fatal("customized retired skill was automatically deleted")
		}
		if _, err = Apply(plan); lockCode(err) != "CONFLICT" {
			t.Fatal("unresolved retirement did not block apply", err)
		}
		if !reflect.DeepEqual(dirty, lockTree(t, root, true)) {
			t.Fatal("retirement refusal changed bytes or transaction evidence")
		}
		if err := os.WriteFile(filepath.Join(root, oldRef), oldBytes, 0644); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unknown-installed-skill", func(t *testing.T) {
		var metadata Metadata
		if err := json.Unmarshal(oldMetadata, &metadata); err != nil {
			t.Fatal(err)
		}
		metadata.Distribution["installedSkills"] = append(stringsOf(metadata.Distribution["installedSkills"]), "unknown-removed-skill")
		writeMigrationMetadata(t, root, metadata)
		unknown := lockTree(t, root, true)
		if _, err := Build(root, "spec", "sync", nil, nil); lockCode(err) != "ASSET" {
			t.Fatal("unknown removed skill was silently dropped", err)
		}
		if !reflect.DeepEqual(unknown, lockTree(t, root, true)) {
			t.Fatal("unknown-skill refusal wrote project files")
		}
		if err := os.WriteFile(filepath.Join(root, MetadataFile), oldMetadata, 0644); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unavailable-old-basis", func(t *testing.T) {
		var metadata Metadata
		if err := json.Unmarshal(oldMetadata, &metadata); err != nil {
			t.Fatal(err)
		}
		old := metadata.Managed[oldRef]
		old.Baseline.Digest = safefs.Digest([]byte("unavailable historical baseline"))
		old.Source = &identitymeta.BaselineSource{Kind: "unavailable"}
		metadata.Managed[oldRef] = old
		writeMigrationMetadata(t, root, metadata)
		unknown := lockTree(t, root, true)
		plan, err := Build(root, "spec", "sync", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if plan.ReadyToApply || upgradeAsset(plan, oldRef).Action != "conflict" || upgradeAsset(plan, oldRef).BaselineAvailable {
			t.Fatal("unavailable old basis authorized deletion")
		}
		if !reflect.DeepEqual(unknown, lockTree(t, root, true)) {
			t.Fatal("unknown-basis planning wrote project files")
		}
		if err := os.WriteFile(filepath.Join(root, MetadataFile), oldMetadata, 0644); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("authority-absent", func(t *testing.T) {
		target, err := bundle.Load("spec")
		if err != nil {
			t.Fatal(err)
		}
		ref := ".template-spec/agents/skill-migrations.md"
		file := target.Files[ref]
		raw := []byte("# 历史迁移说明\n\n本夹具没有该技能更名授权。\n")
		file.Data, file.Digest = base64.StdEncoding.EncodeToString(raw), safefs.Digest(raw)
		target.Files[ref] = file
		if _, included := target.Initial[ref]; included {
			target.Initial[ref] = file
		}
		if _, err := BuildWithOptions(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: target}); lockCode(err) != "ASSET" {
			t.Fatal("absent fixed-source authority authorized a replacement", err)
		}
	})

	t.Run("replacement-absent", func(t *testing.T) {
		target, err := bundle.Load("spec")
		if err != nil {
			t.Fatal(err)
		}
		ref := ".agents/skills/setup-yss-harness/SKILL.md"
		file := target.Files[ref]
		raw := []byte("---\nname: different-entry\n---\n# Not the declared replacement\n")
		file.Data, file.Digest = base64.StdEncoding.EncodeToString(raw), safefs.Digest(raw)
		target.Files[ref] = file
		if _, included := target.Initial[ref]; included {
			target.Initial[ref] = file
		}
		if _, err := BuildWithOptions(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: target}); lockCode(err) != "ASSET" {
			t.Fatal("missing declared replacement authorized a migration", err)
		}
	})

	t.Run("replacement-occupied", func(t *testing.T) {
		ref := ".agents/skills/setup-yss-harness/SKILL.md"
		file := filepath.Join(root, ref)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("local replacement target bytes"), 0600); err != nil {
			t.Fatal(err)
		}
		occupied := lockTree(t, root, true)
		plan, err := Build(root, "spec", "sync", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if plan.ReadyToApply || upgradeAsset(plan, ref).Action != "conflict" {
			t.Fatal("occupied replacement target was overwritten")
		}
		if _, err = Apply(plan); lockCode(err) != "CONFLICT" || !reflect.DeepEqual(occupied, lockTree(t, root, true)) {
			t.Fatal("occupied target refusal changed project files", err)
		}
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unregistered-retired-file", func(t *testing.T) {
		ref := ".agents/skills/yss-harness-upgrade/local-script.mjs"
		file := filepath.Join(root, ref)
		if err := os.WriteFile(file, []byte("// local script\n"), 0600); err != nil {
			t.Fatal(err)
		}
		before := lockTree(t, root, true)
		plan, err := Build(root, "spec", "sync", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if plan.ReadyToApply || upgradeAsset(plan, ref).Action != "conflict" || len(upgradeAsset(plan, ref).Options) != 0 {
			t.Fatal("undeclared historical Skill content was silently retired")
		}
		if !reflect.DeepEqual(before, lockTree(t, root, true)) {
			t.Fatal("unregistered-file planning changed project files")
		}
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("bound-custom-retirement-exception", func(t *testing.T) {
		custom := append(append([]byte{}, oldBytes...), []byte("\nexplicitly preserved local customization\n")...)
		if err := os.WriteFile(filepath.Join(root, oldRef), custom, 0644); err != nil {
			t.Fatal(err)
		}
		before := lockTree(t, root, false)
		plan, err := Build(root, "spec", "sync", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		asset := upgradeAsset(plan, oldRef)
		resolution := Resolution{Path: oldRef, Choice: "keep-local", Before: asset.Before, Target: asset.Target, RuleID: asset.RuleID}
		resolved, err := BuildWithOptions(root, "spec", "sync", nil, nil, nil, PlanningOptions{ResolvedFrom: plan.Digest, Resolutions: []Resolution{resolution}})
		if err != nil || !resolved.ReadyToApply {
			t.Fatal("bound historical retirement exception was rejected", err)
		}
		if _, err = Apply(resolved); err != nil {
			t.Fatal(err)
		}
		if raw, err := os.ReadFile(filepath.Join(root, oldRef)); err != nil || string(raw) != string(custom) {
			t.Fatal("explicit retirement exception changed customized bytes", err)
		}
		repeat, err := Build(root, "spec", "sync", nil, nil)
		if err != nil || !repeat.ReadyToApply || len(repeat.Changes) != 0 || upgradeAsset(repeat, oldRef).Action != "retired-preserved" {
			t.Fatal("recorded retirement exception was not stable", err)
		}
		if _, err = transaction.Rollback(root); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, lockTree(t, root, false)) {
			t.Fatal("exception rollback did not restore old files and selection")
		}
	})
}

func writeMigrationMetadata(t *testing.T, root string, metadata Metadata) {
	t.Helper()
	raw, err := json.Marshal(metadata.Managed)
	if err != nil {
		t.Fatal(err)
	}
	metadata.BaselineDigest = safefs.Digest(raw)
	raw, err = jsonBytes(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, domain.MetadataFile), raw, 0644); err != nil {
		t.Fatal(err)
	}
}

func legacyMaintenanceSelectionFixture(t *testing.T) (string, *Identity) {
	t.Helper()
	root, _ := lockLegacyFixture(t)
	ref := ".agents/skills/" + oldMaintenanceSkill + "/SKILL.md"
	raw := []byte("Historical managed maintenance entry; synthetic legacy bytes for migration tests.\n")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, ref)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ref), raw, 0644); err != nil {
		t.Fatal(err)
	}
	value, err := load(root, domain.Profiles["spec"].Metadata)
	if err != nil {
		t.Fatal(err)
	}
	metadata, ok := object(value)
	if !ok {
		t.Fatal("legacy fixture metadata is not an object")
	}
	managed, _ := object(metadata["managedFiles"])
	managed[ref] = map[string]any{"contentHash": safefs.Digest(raw)}
	distribution, _ := object(metadata["distribution"])
	distribution["installedSkills"] = union(stringsOf(distribution["installedSkills"]), []string{oldMaintenanceSkill})
	encoded, err := jsonBytes(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, domain.Profiles["spec"].Metadata), encoded, 0644); err != nil {
		t.Fatal(err)
	}
	id, err := Detect(root, "spec", false)
	if err != nil {
		t.Fatal(err)
	}
	return root, id
}

func TestLegacyMaintenanceSelectionRequiresFixedSource(t *testing.T) {
	_, id := legacyMaintenanceSelectionFixture(t)
	target, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	if !maintenanceSkillMigration(id, target, "migrate") {
		t.Fatal("real committed Bundle did not authorize its documented legacy selection migration")
	}
	for _, variant := range []string{"wrong-command", "native-sync-command", "wrong-profile", "wrong-target-profile", "working-tree-target", "missing-legacy", "native-migrate", "legacy-all", "unregistered-canonical", "projection-only", "invalid-old-hash", "missing-tombstone", "missing-replacement", "partial-replacement", "unsupported-replacement"} {
		t.Run(variant, func(t *testing.T) {
			_, candidate := legacyMaintenanceSelectionFixture(t)
			actual, err := bundle.Load("spec")
			if err != nil {
				t.Fatal(err)
			}
			command := "migrate"
			ref := ".agents/skills/" + oldMaintenanceSkill + "/SKILL.md"
			switch variant {
			case "wrong-command":
				command = "ensure"
			case "native-sync-command":
				command = "sync"
			case "wrong-profile":
				candidate.Profile = domain.Profiles["backend"]
				actual, err = bundle.Load("backend")
				if err != nil {
					t.Fatal(err)
				}
			case "wrong-target-profile":
				actual.Profile = "design"
			case "working-tree-target":
				actual.SourceState = "working-tree"
			case "missing-legacy":
				candidate.Legacy = nil
			case "native-migrate":
				candidate.Native = &Metadata{}
			case "legacy-all":
				d, _ := object(candidate.Legacy["distribution"])
				d["mode"] = "legacy-all"
			case "unregistered-canonical", "projection-only", "invalid-old-hash":
				managed, _ := object(candidate.Legacy["managedFiles"])
				old := managed[ref]
				delete(managed, ref)
				if variant == "projection-only" {
					managed[".codex/skills/"+oldMaintenanceSkill+"/SKILL.md"] = old
				} else if variant == "invalid-old-hash" {
					managed[ref] = map[string]any{"contentHash": "not-a-sha256"}
				}
			case "missing-tombstone":
				delete(actual.Files, maintenanceMigrationRef)
			case "missing-replacement":
				delete(actual.Files, ".agents/skills/"+currentMaintenanceSkill+"/SKILL.md")
			case "partial-replacement":
				missing := ""
				for _, path := range actual.SkillRequirements[currentMaintenanceSkill].Paths {
					if path != ".agents/skills/"+currentMaintenanceSkill+"/SKILL.md" {
						missing = path
						break
					}
				}
				if missing == "" {
					t.Fatal("real replacement closure has no dependency for this counterexample")
				}
				delete(actual.Files, missing)
			case "unsupported-replacement":
				requirement := actual.SkillRequirements[currentMaintenanceSkill]
				requirement.UnsupportedReason = "test-only unavailable dependency"
				actual.SkillRequirements[currentMaintenanceSkill] = requirement
			}
			if maintenanceSkillMigration(candidate, actual, command) {
				t.Fatal("invalid legacy selection authorized the known replacement")
			}
		})
	}
}

func TestLegacyMaintenanceSelectionPreservesOldFiles(t *testing.T) {
	root, id := legacyMaintenanceSelectionFixture(t)
	oldRef := ".agents/skills/" + oldMaintenanceSkill + "/SKILL.md"
	target, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	u := &upgradePlanner{id: id, bundle: target, plan: &Plan{Command: "migrate"}, old: baseline(id)}
	if len(u.maintenanceRetirements()) != 0 {
		t.Fatal("legacy selection treated metadata claims as trusted deletion material")
	}
	for _, variant := range []string{"current", "customized-old-file", "unknown-other-skill"} {
		t.Run(variant, func(t *testing.T) {
			if variant == "customized-old-file" {
				f, err := os.OpenFile(filepath.Join(root, oldRef), os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, writeErr := f.WriteString("local customization must stay byte-for-byte\n")
				closeErr := f.Close()
				if writeErr != nil || closeErr != nil {
					t.Fatal(writeErr, closeErr)
				}
			}
			if variant == "unknown-other-skill" {
				value, err := load(root, domain.Profiles["spec"].Metadata)
				if err != nil {
					t.Fatal(err)
				}
				metadata, _ := object(value)
				d, _ := object(metadata["distribution"])
				d["installedSkills"] = union(stringsOf(d["installedSkills"]), []string{"unknown-removed-skill"})
				raw, err := jsonBytes(metadata)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, domain.Profiles["spec"].Metadata), raw, 0644); err != nil {
					t.Fatal(err)
				}
			}
			before := lockTree(t, root, true)
			plan, err := Build(root, "spec", "migrate", nil, nil)
			if !reflect.DeepEqual(before, lockTree(t, root, true)) {
				t.Fatal("legacy selection planning wrote files or transaction material")
			}
			if variant == "unknown-other-skill" {
				if lockCode(err) != "ASSET" {
					t.Fatal("unknown missing Skill did not fail closed", err)
				}
				return
			}
			if err != nil || !plan.ReadyToApply {
				t.Fatalf("documented legacy selection could not be planned: %v %+v", err, plan)
			}
			if asset := upgradeAsset(plan, oldRef); asset.Action != "retired-preserved" || asset.RuleID != "" {
				t.Fatal("legacy old bytes were scheduled for retirement instead of preservation", asset)
			}
			selected := false
			for _, change := range plan.Changes {
				if historicalMaintenanceFile(change.Path) {
					t.Fatal("legacy selection planned a write to the old Skill", change.Path)
				}
				if change.Path == MetadataFile {
					var metadata Metadata
					if err := json.Unmarshal(mustDecode(change.Data), &metadata); err != nil {
						t.Fatal(err)
					}
					for _, name := range stringsOf(metadata.Distribution["installedSkills"]) {
						if name == oldMaintenanceSkill {
							t.Fatal("old entry remained an active selection")
						}
						selected = selected || name == currentMaintenanceSkill
					}
				}
			}
			if !selected {
				t.Fatal("current setup entry was not selected")
			}
		})
	}
}

func TestLegacyMaintenanceMigrationApplyDoctorAndRollback(t *testing.T) {
	root, original := legacyMaintenanceSelectionFixture(t)
	oldRef := ".agents/skills/" + oldMaintenanceSkill + "/SKILL.md"
	before := lockTree(t, root, false)
	legacyMetadata, err := os.ReadFile(filepath.Join(root, original.LegacyFile))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Build(root, "spec", "migrate", nil, nil)
	if err != nil || !plan.ReadyToApply {
		t.Fatal("documented legacy migration could not be planned", err)
	}
	if _, err = Apply(plan); err != nil {
		t.Fatal(err)
	}
	id, err := Detect(root, "spec", false)
	if err != nil || id.Native == nil {
		t.Fatal("migration did not establish the native identity", err)
	}
	selected := stringsOf(id.Native.Distribution["installedSkills"])
	if !reflect.DeepEqual(selected, migratedMaintenanceSkills(selected)) || !strings.Contains(strings.Join(selected, ","), currentMaintenanceSkill) {
		t.Fatal("migration did not select the replacement alone", selected)
	}
	if got := lockTree(t, root, false)[oldRef]; got != before[oldRef] {
		t.Fatal("migration changed preserved historical Skill bytes or mode")
	}
	if raw, err := os.ReadFile(filepath.Join(root, original.LegacyFile)); err != nil || string(raw) != string(legacyMetadata) {
		t.Fatal("migration changed the protected legacy identity", err)
	}
	nativeMetadata, err := os.ReadFile(filepath.Join(root, MetadataFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"doctor", "diff"} {
		t.Run(command, func(t *testing.T) {
			readonly := lockTree(t, root, true)
			inspection, err := Build(root, "spec", command, nil, nil)
			if err != nil || !inspection.ReadyToApply || len(inspection.Changes) != 0 {
				t.Fatal("preserved inactive old Skill prevented stable inspection", err)
			}
			if asset := upgradeAsset(inspection, oldRef); asset.Action != "retired-preserved" {
				t.Fatal("read-only inspection reactivated the preserved Skill", asset)
			}
			if !reflect.DeepEqual(readonly, lockTree(t, root, true)) {
				t.Fatal("post-migration inspection wrote files or transaction evidence")
			}
		})
	}
	for _, variant := range []string{"replacement-not-selected", "old-still-selected", "unknown-managed-skill"} {
		t.Run(variant, func(t *testing.T) {
			var metadata Metadata
			if err := json.Unmarshal(nativeMetadata, &metadata); err != nil {
				t.Fatal(err)
			}
			skills := stringsOf(metadata.Distribution["installedSkills"])
			switch variant {
			case "replacement-not-selected":
				filtered := make([]string, 0, len(skills))
				for _, skill := range skills {
					if skill != currentMaintenanceSkill {
						filtered = append(filtered, skill)
					}
				}
				metadata.Distribution["installedSkills"] = filtered
			case "old-still-selected":
				metadata.Distribution["installedSkills"] = union(skills, []string{oldMaintenanceSkill})
			case "unknown-managed-skill":
				metadata.Managed[".agents/skills/unknown-removed-skill/SKILL.md"] = metadata.Managed[oldRef]
			}
			writeMigrationMetadata(t, root, metadata)
			defer func() {
				if err := os.WriteFile(filepath.Join(root, MetadataFile), nativeMetadata, 0644); err != nil {
					t.Fatal(err)
				}
			}()
			readonly := lockTree(t, root, true)
			if _, err := Build(root, "spec", "doctor", nil, nil); lockCode(err) != "ASSET" {
				t.Fatal("unselected replacement or unknown old Skill bypassed the selection guard", err)
			}
			if !reflect.DeepEqual(readonly, lockTree(t, root, true)) {
				t.Fatal("invalid post-migration selection refusal wrote project files")
			}
		})
	}
	if _, err = transaction.Rollback(root); err != nil {
		t.Fatal(err)
	}
	if got := lockTree(t, root, false); !reflect.DeepEqual(before, got) {
		t.Fatal("rollback did not restore original legacy identity, files and modes")
	}
	restored, err := Detect(root, "spec", false)
	if err != nil || restored.Native != nil || restored.Legacy == nil {
		t.Fatal("rollback did not restore the original legacy identity", err)
	}
}

// Dedicated native instances use their complete Profile Bundle rather than
// Spec's installedSkills selection. The same exact source-authorized retirement
// must retain the original baseline and transaction rollback behavior.
func TestNativeFullProfileExplicitSyncRetiresUpgradeSkill(t *testing.T) {
	oldBinary := os.Getenv("YSS_OLD_NATIVE_BINARY")
	if oldBinary == "" {
		t.Skip("requires the fixed historical native CLI; a skip is not migration acceptance")
	}
	for _, profile := range []string{"design", "backend", "frontend"} {
		t.Run(profile, func(t *testing.T) {
			root := freshRoot(t)
			if out, err := exec.Command(oldBinary, "init", "--profile", profile, "--root", root, "--project-name", "旧专职实例显式同步", "--full", "--json").CombinedOutput(); err != nil {
				t.Fatalf("historical native full init: %v %s", err, out)
			}
			oldRef := ".agents/skills/yss-harness-upgrade/SKILL.md"
			if _, err := os.ReadFile(filepath.Join(root, oldRef)); err != nil {
				t.Fatal("historical Bundle did not install its original entry", err)
			}
			businessRef := ".work/legacy/approval.json"
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, businessRef)), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, businessRef), []byte("immutable historical protocol fixture\n"), 0600); err != nil {
				t.Fatal(err)
			}
			before := lockTree(t, root, false)
			readonly := lockTree(t, root, true)
			plan, err := Build(root, profile, "sync", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(readonly, lockTree(t, root, true)) {
				t.Fatal("dedicated sync planning wrote files or transaction evidence")
			}
			if !plan.ReadyToApply || upgradeAsset(plan, oldRef).Action != "delete" {
				t.Fatal("fixed-source dedicated retirement not authorized", upgradeAsset(plan, oldRef))
			}
			if _, err = Apply(plan); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(root, oldRef)); !os.IsNotExist(err) {
				t.Fatal("old entry remains discoverable", err)
			}
			if _, err = os.Stat(filepath.Join(root, ".agents/skills/setup-yss-harness/SKILL.md")); err != nil {
				t.Fatal("replacement entry was not installed", err)
			}
			if after := lockTree(t, root, false); after[businessRef] != before[businessRef] {
				t.Fatal("dedicated sync changed historical business bytes or modes")
			}
			repeat, err := Build(root, profile, "sync", nil, nil)
			if err != nil || !repeat.ReadyToApply || len(repeat.Changes) != 0 {
				t.Fatal("dedicated repeat sync was not stable", err)
			}
			if _, err = transaction.Rollback(root); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, lockTree(t, root, false)) {
				t.Fatal("dedicated rollback did not restore original bytes and modes")
			}
			target, err := bundle.Load(profile)
			if err != nil {
				t.Fatal(err)
			}
			id, err := Detect(root, profile, false)
			if err != nil || !maintenanceSkillMigration(id, target, "sync") {
				t.Fatal("actual complete Profile source does not authorize the exact migration", err)
			}
			t.Run("unregistered-source", func(t *testing.T) {
				copyID, metadata := *id, *id.Native
				metadata.Managed = map[string]Managed{}
				copyID.Native = &metadata
				if maintenanceSkillMigration(&copyID, target, "sync") {
					t.Fatal("unregistered old source authorized retirement")
				}
			})
			t.Run("partial-replacement-tree", func(t *testing.T) {
				partial := *target
				partial.Files = map[string]bundle.File{}
				missing := ".agents/skills/setup-yss-harness/references/operation-contract.md"
				if _, exists := target.Files[missing]; !exists {
					t.Fatal("actual replacement reference absent before counterexample")
				}
				for ref, f := range target.Files {
					if ref != missing {
						partial.Files[ref] = f
					}
				}
				if maintenanceSkillMigration(id, &partial, "sync") {
					t.Fatal("partial replacement tree authorized retirement")
				}
			})
			t.Run("wrong-source-profile-or-command", func(t *testing.T) {
				wrong := *target
				wrong.Profile = "spec"
				if maintenanceSkillMigration(id, &wrong, "sync") || maintenanceSkillMigration(id, target, "ensure") {
					t.Fatal("wrong Profile or new-use command authorized retirement")
				}
			})
			t.Run("dirty-retired-file", func(t *testing.T) {
				file := filepath.Join(root, oldRef)
				raw, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(file, append(append([]byte{}, raw...), []byte("\nlocal customization\n")...), 0644); err != nil {
					t.Fatal(err)
				}
				dirty := lockTree(t, root, true)
				plan, err := Build(root, profile, "sync", nil, nil)
				if err != nil || plan.ReadyToApply || upgradeAsset(plan, oldRef).Action != "conflict" {
					t.Fatal("dedicated dirty old source was not preserved as a conflict", err)
				}
				if _, err = Apply(plan); lockCode(err) != "CONFLICT" || !reflect.DeepEqual(dirty, lockTree(t, root, true)) {
					t.Fatal("dedicated dirty retirement refusal wrote bytes or journals", err)
				}
			})
		})
	}
}
