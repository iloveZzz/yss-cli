package project

import (
	"encoding/json"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

const oldMaintenanceSkill = "yss-harness-upgrade"
const currentMaintenanceSkill = "setup-yss-harness"
const maintenanceMigrationRef = ".template-spec/agents/skill-migrations.md"

// This is an instance-selection migration, not a new-use alias. The exact
// documented pair must be present in the immutable target Bundle. Historical
// Bundles without that retirement section keep their original behavior.
func maintenanceSkillMigration(id *Identity, b *bundle.Bundle, command string) bool {
	if command != "sync" || id.Native == nil || b.SourceState != "committed" || b.Profile != id.Profile.Name {
		return false
	}
	fullProfile := false
	switch id.Profile.Name {
	case "spec":
		if text(id.Native.Distribution["mode"]) != "selected" {
			return false
		}
	case "design", "backend", "frontend":
		if text(id.Native.Distribution["mode"]) != "profile-full" || text(b.Distribution["mode"]) != "profile-full" {
			return false
		}
		// Complete Profiles have no installedSkills selection. Only their
		// registered old canonical source can request this exact retirement.
		if _, registered := id.Native.Managed[".agents/skills/"+oldMaintenanceSkill+"/SKILL.md"]; !registered {
			return false
		}
		fullProfile = true
	default:
		return false
	}
	for ref := range b.Files {
		if historicalMaintenanceFile(ref) {
			return false
		}
	}
	entry, exists := b.Files[".agents/skills/"+currentMaintenanceSkill+"/SKILL.md"]
	if !exists {
		return false
	}
	raw := mustDecode(entry.Data)
	if safefs.Digest(raw) != entry.Digest || !strings.Contains(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\nname: "+currentMaintenanceSkill+"\n") {
		return false
	}
	if fullProfile {
		if !maintenanceReplacementTree(b) {
			return false
		}
	} else {
		requirement, exists := b.SkillRequirements[currentMaintenanceSkill]
		if !exists || requirement.UnsupportedReason != "" {
			return false
		}
		for _, ref := range requirement.Paths {
			if _, exists := b.Files[ref]; !exists {
				return false
			}
		}
	}
	tombstone, exists := b.Files[maintenanceMigrationRef]
	if !exists {
		return false
	}
	raw = mustDecode(tombstone.Data)
	if safefs.Digest(raw) != tombstone.Digest {
		return false
	}
	sections := strings.SplitN(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n## `"+oldMaintenanceSkill+"` 更名", 2)
	if len(sections) != 2 {
		return false
	}
	section := strings.SplitN(sections[1], "\n## ", 2)[0]
	return strings.Contains(section, "安装和实例维护入口统一为 `"+currentMaintenanceSkill+"`") &&
		strings.Contains(section, "既有工程按明确同步或迁移计划更新") &&
		strings.Contains(section, "无法安全退役的定制旧文件保留为明确例外")
}

// profile-full distributes the complete validated Bundle, not a selected Skill
// requirement. Its immutable lock binds the complete canonical and declared
// projection trees; a partial replacement cannot authorize old-file deletion.
func maintenanceReplacementTree(b *bundle.Bundle) bool {
	f, exists := b.Files["skills-lock.json"]
	if !exists {
		return false
	}
	raw := mustDecode(f.Data)
	if safefs.Digest(raw) != f.Digest {
		return false
	}
	var lock struct {
		Version       int    `json:"version"`
		CanonicalRoot string `json:"canonicalRoot"`
		Skills        struct {
			Shared map[string]struct {
				SkillPath     string   `json:"skillPath"`
				EffectiveHash string   `json:"effectiveHash"`
				Targets       []string `json:"targets"`
			} `json:"shared"`
		} `json:"skills"`
	}
	if json.Unmarshal(raw, &lock) != nil || lock.Version != 3 || lock.CanonicalRoot != ".agents/skills" {
		return false
	}
	entry, exists := lock.Skills.Shared[currentMaintenanceSkill]
	if !exists || entry.SkillPath != ".agents/skills/"+currentMaintenanceSkill+"/SKILL.md" {
		return false
	}
	canonical := false
	for _, root := range entry.Targets {
		switch root {
		case ".agents/skills", ".codex/skills", ".cursor/skills", ".pi/skills":
		default:
			return false
		}
		canonical = canonical || root == ".agents/skills"
		digest, err := bundle.SkillTreeHash(b.Files, root+"/"+currentMaintenanceSkill, nil)
		if err != nil || digest != entry.EffectiveHash {
			return false
		}
	}
	return canonical
}

func migratedMaintenanceSkills(skills []string) []string {
	result := make([]string, 0, len(skills))
	for _, name := range skills {
		if name == oldMaintenanceSkill {
			name = currentMaintenanceSkill
		}
		result = append(result, name)
	}
	return union(result)
}

func historicalMaintenanceFile(ref string) bool {
	for _, root := range []string{".agents", ".codex", ".cursor", ".pi"} {
		if strings.HasPrefix(ref, root+"/skills/"+oldMaintenanceSkill+"/") {
			return true
		}
	}
	return false
}

func (u *upgradePlanner) maintenanceRetirements() map[string]bundle.MigrationRule {
	rules := map[string]bundle.MigrationRule{}
	if !maintenanceSkillMigration(u.id, u.bundle, u.plan.Command) {
		return rules
	}
	commit, snapshot, hash := u.previousSource()
	for ref := range u.old {
		if historicalMaintenanceFile(ref) {
			rules[ref] = bundle.MigrationRule{
				ID:     "native-maintenance-retirement." + safefs.Digest([]byte(ref)),
				From:   bundle.SourceMatch{Profile: u.id.Profile.Name, TemplateCommit: commit, SnapshotHash: snapshot, BundleHash: hash},
				Action: "delete", SourcePath: ref,
			}
		}
	}
	return rules
}
