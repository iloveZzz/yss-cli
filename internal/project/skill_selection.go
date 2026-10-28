package project

import (
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"sort"
	"strings"
)

type skillSelection struct {
	CanonicalIDs []string
	When         []string
	Dependencies []SkillDependency
}

func resolveSkillSelection(b *bundle.Bundle, requested []string, when string) (*skillSelection, error) {
	if len(requested) == 0 {
		return nil, domain.Fail("ARGUMENT", "需要技能标识")
	}
	r, _, err := querySkillRegistry(b)
	if err != nil {
		return nil, err
	}
	retired, err := retiredSkillIDs(b)
	if err != nil {
		return nil, err
	}
	registered, aliases := map[string]skillRegistration{}, map[string]string{}
	for _, s := range r.Skills {
		registered[s.ID] = s
		aliases[s.ID] = s.ID
		for _, alias := range s.Aliases {
			aliases[alias] = s.ID
		}
	}
	canonical := func(name string) (string, error) {
		if retired[name] {
			return "", domain.Fail("SKILL", "skill-retired: "+name)
		}
		resolved, ok := aliases[name]
		if !ok {
			return "", domain.Fail("SKILL", "未知或非内置共享技能: "+name)
		}
		if registered[resolved].Maturity == "deprecated" {
			return "", domain.Fail("SKILL", "skill-deprecated: "+resolved)
		}
		if _, ok := b.Files[".agents/skills/"+resolved+"/SKILL.md"]; !ok {
			return "", domain.Fail("SKILL", "当前 Profile 无内置技能: "+resolved)
		}
		return resolved, nil
	}
	out := &skillSelection{CanonicalIDs: []string{}, When: []string{}, Dependencies: []SkillDependency{}}
	conditions, allowed := map[string]bool{}, map[string]bool{}
	for _, deps := range r.Dependencies {
		for _, d := range deps {
			if d.Type == "context-conditional" {
				allowed[d.When] = true
			}
		}
	}
	if when != "" {
		for _, v := range strings.Split(when, ",") {
			v = strings.TrimSpace(v)
			if v == "" || !allowed[v] {
				return nil, domain.Fail("ARGUMENT", "未知条件: "+v)
			}
			conditions[v] = true
		}
		for v := range conditions {
			out.When = append(out.When, v)
		}
		sort.Strings(out.When)
	}
	seen := map[string]bool{}
	var visit func(string) error
	visit = func(name string) error {
		name, err := canonical(name)
		if err != nil {
			return err
		}
		if seen[name] {
			return nil
		}
		seen[name] = true
		for _, d := range r.Dependencies[name] {
			if d.Type != "context-required" && !(d.Type == "context-conditional" && conditions[d.When]) {
				continue
			}
			d.Skill, err = canonical(d.Skill)
			if err != nil {
				return err
			}
			d.From = name
			out.Dependencies = append(out.Dependencies, d)
			if err = visit(d.Skill); err != nil {
				return err
			}
		}
		out.CanonicalIDs = append(out.CanonicalIDs, name)
		return nil
	}
	for _, name := range requested {
		if err = visit(name); err != nil {
			return nil, err
		}
	}

	return out, nil
}

func SelectSkillResources(b *bundle.Bundle, installation InstallationKind, requested, runtimes []string) ([]string, error) {
	if len(requested) == 0 {
		return nil, domain.Fail("ARGUMENT", "需要技能标识")
	}
	paths := map[string]bool{}
	addTree := func(prefix string) bool {
		if _, ok := b.Files[prefix+"SKILL.md"]; !ok {
			return false
		}
		for ref := range b.Files {
			if strings.HasPrefix(ref, prefix) {
				paths[ref] = true
			}
		}
		return true
	}
	switch installation {
	case InstallationSkills:
		if b.SchemaVersion != 5 {
			return nil, domain.Fail("INSTALLATION", "当前 Bundle 不支持独立技能安装")
		}
		selected, err := resolveSkillSelection(b, requested, "")
		if err != nil {
			return nil, err
		}
		roots := []string{".agents/skills"}
		for _, runtime := range runtimes {
			root, ok := domain.SkillRuntimeRoot(runtime)
			if !ok || root == "" {
				return nil, domain.Fail("BUNDLE", "未知技能运行时: "+runtime)
			}
			roots = append(roots, root)
		}
		for _, name := range selected.CanonicalIDs {
			if r, ok := b.SkillRequirements[name]; ok && r.UnsupportedReason != "" {
				return nil, domain.Fail("UNPORTED", r.UnsupportedReason)
			}
			for _, root := range roots {
				if !addTree(root + "/" + name + "/") {
					return nil, domain.Fail("SKILL", "固定来源缺少技能或投影: "+root+"/"+name)
				}
			}
			refs, err := b.Installations["skills"].ReferencesForSkill(name)
			if err != nil {
				return nil, domain.Fail("BUNDLE", err.Error())
			}
			for _, ref := range refs {
				if err := safefs.ValidateRef(ref); err != nil || !(strings.HasPrefix(ref, ".template-spec/") || strings.HasPrefix(ref, ".template-source/") || strings.HasPrefix(ref, "scripts/")) {
					return nil, domain.Fail("BUNDLE", "独立技能引用不合法: "+ref)
				}
				if _, ok := b.Files[ref]; !ok {
					return nil, domain.Fail("BUNDLE", "独立技能引用缺失: "+ref)
				}
				paths[ref] = true
			}
		}
	case InstallationGoverned:
		if b.SchemaVersion == 5 {
			pending, seen := requested, map[string]bool{}
			requested = nil
			for len(pending) > 0 {
				selected, err := resolveSkillSelection(b, pending, "")
				if err != nil {
					return nil, err
				}
				pending = nil
				for _, name := range selected.CanonicalIDs {
					if seen[name] {
						continue
					}
					seen[name] = true
					requested = append(requested, name)
					pending = append(pending, b.SkillRequirements[name].Skills...)
				}
			}
		}
		for _, name := range requested {
			if r, ok := b.SkillRequirements[name]; ok {
				if r.UnsupportedReason != "" {
					return nil, domain.Fail("UNPORTED", r.UnsupportedReason)
				}
				for _, ref := range r.Paths {
					paths[ref] = true
				}
				for _, dep := range r.Skills {
					for ref := range b.Files {
						if strings.HasPrefix(ref, ".agents/skills/"+dep+"/") || strings.HasPrefix(ref, ".claude/skills/"+dep+"/") {
							paths[ref] = true
						}
					}
				}
			}
			canonical := addTree(".agents/skills/" + name + "/")
			projected := addTree(".claude/skills/" + name + "/")
			if !canonical && !projected {
				return nil, domain.Fail("SKILL", "快照未登记 Skill: "+name)
			}
		}
	default:
		return nil, domain.Fail("INSTALLATION", "未知安装方式")
	}
	result := make([]string, 0, len(paths))
	for ref := range paths {
		result = append(result, ref)
	}
	sort.Strings(result)
	return result, nil
}

func validateSkillsSelection(b *bundle.Bundle, selection, runtimes []string) error {
	roots := []string{".agents/skills"}
	for _, runtime := range runtimes {
		root, ok := domain.SkillRuntimeRoot(runtime)
		if !ok {
			return domain.Fail("BUNDLE", "未知技能运行时: "+runtime)
		}
		roots = append(roots, root)
	}
	if len(selection) == 0 {
		return nil
	}
	registry, _, err := querySkillRegistry(b)
	if err != nil {
		return err
	}
	allowedRefs := map[string]bool{}
	item := b.Installations["skills"]
	for _, skill := range item.ReferenceOwners() {
		refs, err := item.ReferencesForSkill(skill)
		if err != nil {
			return domain.Fail("BUNDLE", err.Error())
		}
		for _, ref := range refs {
			if strings.HasPrefix(ref, ".template-spec/") || strings.HasPrefix(ref, ".template-source/") || strings.HasPrefix(ref, "scripts/") {
				_, allowedRefs[ref] = b.Files[ref]
			}
		}
	}
	for _, ref := range selection {
		if err := safefs.ValidateRef(ref); err != nil {
			return err
		}
		if _, ok := b.Initial[ref]; ok {
			continue
		}
		allowed := allowedRefs[ref]
		for _, skill := range registry.Skills {
			if skill.Maturity == "deprecated" {
				continue
			}
			for _, root := range roots {
				prefix := root + "/" + skill.ID + "/"
				if strings.HasPrefix(ref, prefix) {
					_, allowed = b.Files[root+"/"+skill.ID+"/SKILL.md"]
					if allowed {
						break
					}
				}
			}
			if allowed {
				break
			}
		}
		if !allowed {
			return domain.Fail("GOVERNED_REQUIRED", "独立技能安装不能选择治理资源或未登记投影: "+ref)
		}
	}
	return nil
}
