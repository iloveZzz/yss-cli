package bundle

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"maps"
	"strings"
)

type Installation struct {
	Initial      map[string]File `json:"initial"`
	Distribution map[string]any  `json:"distribution"`
}
type InstallationPolicy struct {
	Entries map[string]string `json:"entries"`
	Skills  []string          `json:"skills"`
	Files   []string          `json:"files"`
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
	for ref, f := range item.Initial {
		if err := safefs.ValidateRef(ref); err != nil {
			return nil, err
		}
		if _, err := newValidationOp().content(f); err != nil {
			return nil, err
		}
	}
	view.Initial = maps.Clone(item.Initial)
	view.Distribution = cloneDistribution(item.Distribution)
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
	b.Installations = map[string]Installation{"governed": governed, "skills": {Initial: initial, Distribution: d}}
	return nil
}
