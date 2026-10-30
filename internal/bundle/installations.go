package bundle

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"maps"
	"regexp"
	"sort"
	"strings"
)

var referenceGroupName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type Installation struct {
	Initial              map[string]File     `json:"initial"`
	Distribution         map[string]any      `json:"distribution"`
	SkillReferences      map[string][]string `json:"skillReferences,omitempty"`
	ReferenceGroups      map[string][]string `json:"referenceGroups,omitempty"`
	SkillReferenceGroups map[string][]string `json:"skillReferenceGroups,omitempty"`
}
type InstallationPolicy struct {
	SpecWorkflow         string              `json:"specWorkflow,omitempty"`
	Entries              map[string]string   `json:"entries"`
	Skills               []string            `json:"skills"`
	Files                []string            `json:"files"`
	SkillReferences      map[string][]string `json:"skillReferences,omitempty"`
	ReferenceGroups      map[string][]string `json:"referenceGroups,omitempty"`
	SkillReferenceGroups map[string][]string `json:"skillReferenceGroups,omitempty"`
}

// ReferencesForSkill expands only groups declared for this skill, preserving
// deterministic order and rejecting a stale or unknown group declaration.
func (i Installation) ReferencesForSkill(skill string) ([]string, error) {
	refs := append([]string(nil), i.SkillReferences[skill]...)
	for _, group := range i.SkillReferenceGroups[skill] {
		members, ok := i.ReferenceGroups[group]
		if !ok {
			return nil, fmt.Errorf("unknown skill reference group %s for %s", group, skill)
		}
		refs = append(refs, members...)
	}
	seen := make(map[string]bool, len(refs))
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out, nil
}

func (i Installation) ReferenceOwners() []string {
	owners := map[string]bool{}
	for skill := range i.SkillReferences {
		owners[skill] = true
	}
	for skill := range i.SkillReferenceGroups {
		owners[skill] = true
	}
	out := make([]string, 0, len(owners))
	for skill := range owners {
		out = append(out, skill)
	}
	sort.Strings(out)
	return out
}

func (i Installation) validateReferenceGroups() error {
	used := map[string]bool{}
	for skill, groups := range i.SkillReferenceGroups {
		for _, group := range groups {
			if _, ok := i.ReferenceGroups[group]; !ok {
				return fmt.Errorf("unknown skill reference group %s for %s", group, skill)
			}
			used[group] = true
		}
	}
	for group, refs := range i.ReferenceGroups {
		if !referenceGroupName.MatchString(group) || !used[group] || len(refs) == 0 {
			return fmt.Errorf("invalid or unused skill reference group %s", group)
		}
	}
	return nil
}

// ForInstallation returns a private selection view; it never reclassifies a legacy bundle.
func (b *Bundle) ForInstallation(kind string) (*Bundle, error) {
	if kind != "skills" && kind != "governed" {
		return nil, domain.Fail("INSTALLATION", "未知安装方式: "+kind)
	}
	view := *b
	if b.SchemaVersion < 5 {
		if kind != "governed" {
			return nil, domain.Fail("INSTALLATION", "当前 Bundle 不支持独立技能安装")
		}
		view.Initial = maps.Clone(b.Initial)
		view.Distribution = cloneDistribution(b.Distribution)
		return &view, nil
	}
	item, ok := b.Installations[kind]
	if ok && kind == "governed" && item.Initial == nil {
		item.Initial = b.Initial
	}
	if !ok || item.Initial == nil || item.Distribution == nil {
		return nil, domain.Fail("BUNDLE", "缺少安装集合: "+kind)
	}
	if kind == "skills" {
		if err := item.validateReferenceGroups(); err != nil {
			return nil, domain.Fail("BUNDLE", err.Error())
		}
		op := newValidationOp()
		for _, skill := range item.ReferenceOwners() {
			refs, err := item.ReferencesForSkill(skill)
			if err != nil {
				return nil, domain.Fail("BUNDLE", err.Error())
			}
			if err := safefs.ValidateRef(skill); err != nil || strings.Contains(skill, "/") {
				return nil, domain.Fail("BUNDLE", "独立技能引用所有者不合法: "+skill)
			}
			if _, ok := b.Files[".agents/skills/"+skill+"/SKILL.md"]; !ok {
				return nil, domain.Fail("BUNDLE", "独立技能引用所有者缺失: "+skill)
			}
			for _, ref := range refs {
				if err := safefs.ValidateRef(ref); err != nil || !(strings.HasPrefix(ref, ".template-spec/") || strings.HasPrefix(ref, ".template-source/") || strings.HasPrefix(ref, "scripts/")) {
					return nil, domain.Fail("BUNDLE", "独立技能引用不合法: "+ref)
				}
				f, ok := b.Files[ref]
				if !ok {
					return nil, domain.Fail("BUNDLE", "独立技能引用缺失: "+ref)
				}
				if _, err := op.content(f); err != nil || (f.Mode != 0644 && f.Mode != 0755) {
					return nil, domain.Fail("BUNDLE", "独立技能引用内容或权限不合法: "+ref)
				}
				if initial, ok := item.Initial[ref]; ok && initial != f {
					return nil, domain.Fail("BUNDLE", "独立技能初始引用与来源不一致: "+ref)
				}
			}
		}
	}
	for ref, f := range item.Initial {
		if err := safefs.ValidateRef(ref); err != nil {
			return nil, err
		}
		if _, err := newValidationOp().content(f); err != nil {
			return nil, err
		}
	}
	view.Initial = maps.Clone(item.Initial)
	if kind == "governed" {
		// Source references are deliberately packaged for the skills installation;
		// they must not become part of a governed initial or full installation.
		view.Files = maps.Clone(b.Files)
		for ref := range view.Files {
			if strings.HasPrefix(ref, ".template-source/") {
				delete(view.Files, ref)
			}
		}
		for ref := range view.Initial {
			if strings.HasPrefix(ref, ".template-source/") {
				delete(view.Initial, ref)
			}
		}
	}
	view.Distribution = cloneDistribution(item.Distribution)
	if kind == "governed" && item.Distribution["specWorkflow"] == "matt-spec-v1" {
		view.Files = specGovernedCatalog(b, item)
		view.StageRequirements = map[string]Requirement{}
		view.SkillRequirements = map[string]Requirement{}
		for ref := range view.Files {
			if strings.HasPrefix(ref, ".agents/skills/") && strings.HasSuffix(ref, "/SKILL.md") && strings.Count(ref, "/") == 3 {
				skill := strings.Split(ref, "/")[2]
				refs, err := item.ReferencesForSkill(skill)
				if err != nil {
					return nil, err
				}
				view.SkillRequirements[skill] = Requirement{Paths: refs}
			}
		}
	}
	return &view, nil
}
func cloneDistribution(d map[string]any) map[string]any {
	raw, _ := json.Marshal(d)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func buildInstallations(ctx context.Context, b *Bundle, p Policy, s Source, root, object string) error {
	if len(p.Installations) == 0 {
		return nil
	}
	cfg, ok := p.Installations["skills"]
	if !ok || len(p.Installations) != 2 {
		return fmt.Errorf("installations must declare skills and governed")
	}
	if _, ok = p.Installations["governed"]; !ok {
		return fmt.Errorf("missing governed installation")
	}
	b.SchemaVersion = 5
	governed := Installation{Distribution: cloneDistribution(b.Distribution)}
	governed.Distribution["installation"] = "governed"
	initial := map[string]File{}
	sourceAllows := stringSet(stringsFrom(p.Manifest["allowFiles"]))
	declaredSource := map[string]bool{}
	declared := Installation{SkillReferences: cfg.SkillReferences, ReferenceGroups: cfg.ReferenceGroups, SkillReferenceGroups: cfg.SkillReferenceGroups}
	if err := declared.validateReferenceGroups(); err != nil {
		return err
	}
	for _, skill := range declared.ReferenceOwners() {
		refs, err := declared.ReferencesForSkill(skill)
		if err != nil {
			return err
		}
		if err := safefs.ValidateRef(skill); err != nil || strings.Contains(skill, "/") {
			return fmt.Errorf("invalid skill reference owner %s", skill)
		}
		if _, ok := b.Files[".agents/skills/"+skill+"/SKILL.md"]; !ok {
			return fmt.Errorf("skill reference owner missing: %s", skill)
		}
		for _, ref := range refs {
			if err := safefs.ValidateRef(ref); err != nil || !(strings.HasPrefix(ref, ".template-source/") || strings.HasPrefix(ref, ".template-spec/") || strings.HasPrefix(ref, "scripts/")) {
				return fmt.Errorf("invalid skill reference: %s", ref)
			}
			if strings.HasPrefix(ref, ".template-source/") && !sourceAllows[ref] {
				return fmt.Errorf("skill source reference not individually allowed: %s", ref)
			}
			if strings.HasPrefix(ref, ".template-source/") {
				declaredSource[ref] = true
			}
			file, ok := b.Files[ref]
			if !ok {
				return fmt.Errorf("skill reference missing: %s", ref)
			}
			if strings.HasPrefix(ref, ".template-source/") && file.Ownership != "managed" {
				return fmt.Errorf("skill source reference must be managed: %s", ref)
			}
		}
	}
	for ref := range sourceAllows {
		if (ref == ".template-source" || strings.HasPrefix(ref, ".template-source/")) && !declaredSource[ref] {
			return fmt.Errorf("source allowFiles entry has no skill reference: %s", ref)
		}
	}
	for _, ref := range cfg.Files {
		f, ok := b.Files[ref]
		if !ok {
			return fmt.Errorf("installation resource missing: %s", ref)
		}
		initial[ref] = f
	}
	runtimes := stringsFrom(b.Distribution["runtimes"])
	for _, skill := range cfg.Skills {
		if err := safefs.ValidateRef(skill); err != nil || strings.Contains(skill, "/") {
			return fmt.Errorf("invalid skill %s", skill)
		}
		if _, ok := b.Files[".agents/skills/"+skill+"/SKILL.md"]; !ok {
			return fmt.Errorf("installation skill missing: %s", skill)
		}
		refs, err := declared.ReferencesForSkill(skill)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			initial[ref] = b.Files[ref]
		}
		roots := []string{".agents/skills/"}
		for _, runtime := range runtimes {
			roots = append(roots, "."+runtime+"/skills/")
		}
		for ref, f := range b.Files {
			for _, prefix := range roots {
				if strings.HasPrefix(ref, prefix+skill+"/") {
					initial[ref] = f
					break
				}
			}
		}
	}
	for target, source := range cfg.Entries {
		if err := safefs.ValidateRef(target); err != nil {
			return err
		}
		if err := safefs.ValidateRef(source); err != nil {
			return err
		}
		// Entry templates are source-only assets, read from the same immutable tree as the policy.
		data, err := committedSkillFile(ctx, root, s.Commit, source)
		if s.State == "working-tree" {
			data, err = git(ctx, root, "show", object+":"+source)
		}
		if err != nil {
			return err
		}
		initial[target] = encodedFile(data, 0644, "managed-customizable")
	}
	f, ok := b.Files["skills-lock.json"]
	if !ok {
		return fmt.Errorf("installation lock missing")
	}
	raw, err := f.Render(nil)
	if err != nil {
		return err
	}
	raw, err = SelectedSourceLock(raw, cfg.Skills, runtimes)
	if err != nil {
		return err
	}
	initial["skills-lock.json"] = encodedFile(raw, 0644, "generated")
	if err = bindBuiltSkillLock(initial); err != nil {
		return err
	}
	d := map[string]any{"installation": "skills", "mode": "skills", "installedSkills": cfg.Skills, "installedStages": []string{}, "runtimes": runtimes}
	if gc := p.Installations["governed"]; gc.SpecWorkflow != "" {
		governed, err = selectSpecGoverned(b, gc)
		if err != nil {
			return err
		}
		for target, source := range gc.Entries {
			if err := safefs.ValidateRef(target); err != nil {
				return err
			}
			if err := safefs.ValidateRef(source); err != nil {
				return err
			}
			data, err := committedSkillFile(ctx, root, s.Commit, source)
			if s.State == "working-tree" {
				data, err = git(ctx, root, "show", object+":"+source)
			}
			if err != nil {
				return err
			}
			governed.Initial[target] = encodedFile(data, 0644, "managed-customizable")
		}
	}
	b.Installations = map[string]Installation{"governed": governed, "skills": {Initial: initial, Distribution: d, SkillReferences: cfg.SkillReferences, ReferenceGroups: cfg.ReferenceGroups, SkillReferenceGroups: cfg.SkillReferenceGroups}}
	return nil
}
