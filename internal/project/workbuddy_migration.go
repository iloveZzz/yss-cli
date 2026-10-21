package project

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

// This is retirement input only, not a supported runtime or discovery alias.
const retiredWorkBuddyRoot = ".codebuddy/skills"

func workBuddyManagedOwnership(ownership string) bool {
	return ownership == "managed" || ownership == "managed-customizable" || ownership == "generated"
}

func currentWorkBuddyManaged(id *Identity, managed Managed) bool {
	return workBuddyManagedOwnership(managed.Ownership) && managed.Source != nil && managed.Source.Kind == "bundle" && managed.Source.TemplateCommit == id.Native.TemplateCommit && managed.Source.SnapshotHash == id.Native.SnapshotHash && managed.Source.BundleHash == id.Native.BundleHash
}

func requireWorkBuddyRootSync(id *Identity, b *bundle.Bundle, command string) error {
	if (command != "skills" && command != "assets" && command != "sync") || !workBuddyRootMigrationTarget(id, b) {
		return nil
	}
	var skills map[string]bool
	if command == "sync" {
		skills = workBuddyRootMigrationSkills(id, b)
	}
	for ref, managed := range id.Native.Managed {
		// A drifted lock cannot bypass the explicit sync boundary. Explicit
		// retirement exceptions remain protected, outside the active runtime.
		if managed.Disposition != nil && managed.Disposition.Choice == "keep-local" && strings.HasPrefix(managed.Disposition.RuleID, "workbuddy-root-retirement.") {
			continue
		}
		if strings.HasPrefix(ref, retiredWorkBuddyRoot+"/") && workBuddyManagedOwnership(managed.Ownership) {
			if command != "sync" {
				return domain.Fail("SYNC_REQUIRED", "WorkBuddy 项目目录变更须先显式执行 yss sync --plan / --apply")
			}
			parts := strings.Split(ref, "/")
			if !currentWorkBuddyManaged(id, managed) || len(parts) < 4 || !skills[parts[2]] {
				return domain.Fail("WORKBUDDY_MIGRATION_BLOCKED", "旧 WorkBuddy 投影缺少可信来源锁授权；请先恢复精确旧 skills-lock.json 及其受管来源，再重新生成 sync 计划；单文件 use-template 决议不能替代迁移授权")
			}
		}
	}
	return nil
}

func workBuddyRootMigrationTarget(id *Identity, b *bundle.Bundle) bool {
	if id.Native == nil || b.SourceState != "committed" || b.Profile != id.Profile.Name || !containsString(stringsOf(id.Native.Distribution["runtimes"]), "workbuddy") {
		return false
	}
	current, _ := domain.SkillRuntimeRoot("workbuddy")
	file, ok := b.Files["skills-lock.json"]
	if !ok {
		return false
	}
	raw, err := file.Render(nil)
	if err != nil || safefs.Digest(raw) != file.Digest {
		return false
	}
	var target skillLock
	if json.Unmarshal(raw, &target) != nil || target.Version != 3 || !containsString(target.ProjectionRoots, current) || containsString(target.ProjectionRoots, retiredWorkBuddyRoot) {
		return false
	}
	_, err = bundle.RenderedSkillLock(raw, b.Files, Variables(id, nil))
	return err == nil
}

// A current committed target and the unchanged registered old lock authorize
// only its formerly installed shared Skill trees. The planner separately proves
// each old file's original bytes and mode before proposing deletion.
func workBuddyRootMigrationSkills(id *Identity, b *bundle.Bundle) map[string]bool {
	if !workBuddyRootMigrationTarget(id, b) {
		return nil
	}
	previous, registered := id.Native.Managed["skills-lock.json"]
	if !registered || !workBuddyManagedOwnership(previous.Ownership) || previous.Source == nil || previous.Source.Kind != "bundle" || previous.Source.TemplateCommit != id.Native.TemplateCommit || previous.Source.SnapshotHash != id.Native.SnapshotHash || previous.Source.BundleHash != id.Native.BundleHash {
		return nil
	}
	actual, err := safefs.Describe(id.Root, "skills-lock.json")
	if err != nil || actual != previous.Applied || actual != previous.Baseline {
		return nil
	}
	lockPath, err := safefs.Path(id.Root, "skills-lock.json")
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(lockPath)
	if err != nil || safefs.Digest(raw) != actual.Digest {
		return nil
	}
	var old struct {
		Version         int      `json:"version"`
		CanonicalRoot   string   `json:"canonicalRoot"`
		ProjectionRoots []string `json:"projectionRoots"`
		Skills          struct {
			Shared map[string]struct {
				Targets []string `json:"targets"`
			} `json:"shared"`
		} `json:"skills"`
	}
	current, _ := domain.SkillRuntimeRoot("workbuddy")
	if json.Unmarshal(raw, &old) != nil || old.Version != 3 || old.CanonicalRoot != ".agents/skills" || !containsString(old.ProjectionRoots, retiredWorkBuddyRoot) || containsString(old.ProjectionRoots, current) || len(union(old.ProjectionRoots)) != len(old.ProjectionRoots) {
		return nil
	}
	skills := map[string]bool{}
	for name, entry := range old.Skills.Shared {
		if name == "" || strings.HasPrefix(name, ".") || strings.ContainsAny(name, "/\\") || !containsString(entry.Targets, retiredWorkBuddyRoot) || !containsString(entry.Targets, ".agents/skills") {
			continue
		}
		if _, managed := id.Native.Managed[retiredWorkBuddyRoot+"/"+name+"/SKILL.md"]; managed {
			skills[name] = true
		}
	}
	return skills
}

func (u *upgradePlanner) workBuddyRootRetirements() map[string]bundle.MigrationRule {
	rules := map[string]bundle.MigrationRule{}
	if u.plan.Command != "sync" {
		return rules
	}
	skills := workBuddyRootMigrationSkills(u.id, u.bundle)
	commit, snapshot, hash := u.previousSource()
	for ref, managed := range u.old {
		parts := strings.Split(ref, "/")
		if len(parts) < 4 || strings.Join(parts[:2], "/") != retiredWorkBuddyRoot || !skills[parts[2]] || !currentWorkBuddyManaged(u.id, managed) {
			continue
		}
		rules[ref] = bundle.MigrationRule{
			ID:     "workbuddy-root-retirement." + safefs.Digest([]byte(ref)),
			From:   bundle.SourceMatch{Profile: u.id.Profile.Name, TemplateCommit: commit, SnapshotHash: snapshot, BundleHash: hash},
			Action: "delete", SourcePath: ref,
		}
	}
	return rules
}
