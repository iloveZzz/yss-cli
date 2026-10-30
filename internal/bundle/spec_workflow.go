package bundle

import (
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"strings"
)

// selectSpecGoverned selects resources independently of the legacy stage model.
func selectSpecGoverned(b *Bundle, cfg InstallationPolicy) (Installation, error) {
	if b.Profile != "spec" || cfg.SpecWorkflow != identitymeta.MattSpecWorkflow {
		return Installation{}, fmt.Errorf("unsupported Spec workflow policy")
	}
	out := Installation{Initial: map[string]File{}, SkillReferences: cfg.SkillReferences, ReferenceGroups: cfg.ReferenceGroups, SkillReferenceGroups: cfg.SkillReferenceGroups}
	if err := out.validateReferenceGroups(); err != nil {
		return out, err
	}
	add := func(ref string) error {
		if err := safefs.ValidateRef(ref); err != nil {
			return err
		}
		f, ok := b.Files[ref]
		if !ok {
			return fmt.Errorf("Spec workflow resource missing: %s", ref)
		}
		out.Initial[ref] = f
		return nil
	}
	for _, ref := range cfg.Files {
		if err := add(ref); err != nil {
			return out, err
		}
	}
	runtimes := stringsFrom(b.Distribution["runtimes"])
	for _, skill := range cfg.Skills {
		if err := safefs.ValidateRef(skill); err != nil || strings.Contains(skill, "/") {
			return out, fmt.Errorf("invalid workflow skill: %s", skill)
		}
		if err := add(".agents/skills/" + skill + "/SKILL.md"); err != nil {
			return out, err
		}
		prefixes := []string{".agents/skills/" + skill + "/"}
		for _, runtime := range runtimes {
			prefixes = append(prefixes, "."+runtime+"/skills/"+skill+"/")
		}
		for ref, f := range b.Files {
			for _, prefix := range prefixes {
				if strings.HasPrefix(ref, prefix) {
					out.Initial[ref] = f
					break
				}
			}
		}
		refs, err := out.ReferencesForSkill(skill)
		if err != nil {
			return out, err
		}
		for _, ref := range refs {
			if err := add(ref); err != nil {
				return out, err
			}
		}
	}
	if f, ok := b.Files["skills-lock.json"]; ok {
		raw, err := f.Render(nil)
		if err != nil {
			return out, err
		}
		raw, err = SelectedSourceLock(raw, cfg.Skills, runtimes)
		if err != nil {
			return out, err
		}
		out.Initial["skills-lock.json"] = encodedFile(raw, 0644, "generated")
		if err := bindBuiltSkillLock(out.Initial); err != nil {
			return out, err
		}
	}
	out.Distribution = map[string]any{"installation": "governed", "specWorkflow": cfg.SpecWorkflow, "mode": "selected", "installedSkills": cfg.Skills, "installedStages": []string{}, "runtimes": runtimes}
	return out, nil
}

func specGovernedCatalog(b *Bundle, item Installation) map[string]File {
	out := map[string]File{}
	for ref := range item.Initial {
		if f, ok := b.Files[ref]; ok {
			out[ref] = f
		}
	}
	// Keep distribution metadata used by readiness resolution, not product stages.
	for _, ref := range []string{"skills-lock.json", ".template-spec/agents/yss-skill-registry.yaml", "scripts/lib/skill-supply-chain.mjs"} {
		if f, ok := b.Files[ref]; ok {
			out[ref] = f
		}
	}
	owners := map[string]bool{}
	for _, skill := range stringsFrom(item.Distribution["installedSkills"]) {
		owners[skill] = true
	}
	for _, skill := range item.ReferenceOwners() {
		owners[skill] = true
	}
	for skill := range owners {
		refs, _ := item.ReferencesForSkill(skill)
		for _, ref := range refs {
			if f, ok := b.Files[ref]; ok {
				out[ref] = f
			}
		}
		for ref, f := range b.Files {
			for _, prefix := range []string{".agents/skills/", ".claude/skills/", ".codex/skills/", ".cursor/skills/", ".pi/skills/", ".workbuddy/skills/"} {
				if strings.HasPrefix(ref, prefix+skill+"/") {
					out[ref] = f
					break
				}
			}
		}
	}
	// Entry variants must remain effective for --full as well as initial install.
	for _, ref := range []string{"AGENTS.md", "CLAUDE.md"} {
		if f, ok := item.Initial[ref]; ok {
			out[ref] = f
		}
	}
	return out
}
