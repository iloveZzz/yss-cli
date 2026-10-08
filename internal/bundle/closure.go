package bundle

import (
	"encoding/json"
	"fmt"
	"go.yaml.in/yaml/v3"
	"path"
	"regexp"
	"sort"
	"strings"
)

var imports = []*regexp.Regexp{regexp.MustCompile(`\b(?:import|export)\s+(?:[^'"\n]*?\s+from\s*)?['"]([^'"]+)['"]`), regexp.MustCompile(`\b(?:require|import)\s*\(\s*['"]([^'"]+)['"]\s*\)`)}
var fixedResources = regexp.MustCompile(`['"]((?:scripts/[\p{L}\p{N}_.\/-]+\.py|\.template-spec/[\p{L}\p{N}_.\/-]+\.(?:json|yaml)))['"]`)
var urlResources = regexp.MustCompile(`\bnew\s+URL\(\s*['"](\.[^'"]+\.(?:json|yaml|py))['"]\s*,\s*import\.meta\.url\s*\)`)
var subprocessEntries = regexp.MustCompile(`\b(?:spawnSync|spawn|execFileSync|execFile)\(\s*(?:(?:process\.execPath|['"](?:node|python3)['"])\s*,\s*\[\s*)?(?:path\.)?(?:join|resolve)\(\s*(?:ROOT|TEMPLATE_ROOT)\s*,\s*['"](scripts/[\p{L}\p{N}_.\/-]+)['"]\s*\)`)
var skillRefs = regexp.MustCompile(`(?:\.template-spec|scripts)/[\p{L}\p{N}_.\/-]+`)

type requiredSkill struct{ skill, from string }

func (e requiredSkill) Error() string {
	return "阶段脚本依赖未安装 Skill: " + e.from + " -> " + e.skill
}

type closure struct {
	raw                             map[string]sourceFile
	paths, modules, schemas, skills map[string]bool
	ids                             map[string]string
}

func (c *closure) add(ref string) error {
	if _, ok := c.raw[ref]; !ok {
		return fmt.Errorf("CLI 快照缺少阶段资产: %s", ref)
	}
	c.paths[ref] = true
	if !strings.HasSuffix(ref, ".schema.json") || c.schemas[ref] {
		return nil
	}
	c.schemas[ref] = true
	var schema any
	if e := json.Unmarshal(c.raw[ref].data, &schema); e != nil {
		return e
	}
	if obj, ok := schema.(map[string]any); ok {
		if id, ok := obj["$id"].(string); ok {
			if old, exists := c.ids[id]; exists && old != ref {
				return fmt.Errorf("阶段 Schema ID 冲突: %s", id)
			}
			c.ids[id] = ref
		}
	}
	var visit func(any) error
	visit = func(v any) error {
		switch x := v.(type) {
		case []any:
			for _, a := range x {
				if e := visit(a); e != nil {
					return e
				}
			}
		case map[string]any:
			for _, k := range sortedKeys(x) {
				a := x[k]
				if k == "$ref" || k == "$dynamicRef" {
					if dependency, ok := a.(string); ok && !strings.HasPrefix(dependency, "#") {
						file := strings.Split(dependency, "#")[0]
						if _, ok := c.ids[file]; ok {
							continue
						}
						if strings.Contains(file, ":") || strings.HasPrefix(file, "/") || strings.Contains(file, "\\") {
							return fmt.Errorf("阶段 Schema 依赖必须本地相对: %s -> %s", ref, dependency)
						}
						target := path.Clean(path.Join(path.Dir(ref), file))
						if !strings.HasPrefix(target, ".template-spec/") && !strings.HasPrefix(target, "scripts/") {
							return fmt.Errorf("阶段 Schema 依赖越界: %s", target)
						}
						if e := c.add(target); e != nil {
							return e
						}
						continue
					}
				}
				if e := visit(a); e != nil {
					return e
				}
			}
		}
		return nil
	}
	return visit(schema)
}
func (c *closure) prefix(prefix string) error {
	found := false
	for _, ref := range sortedKeys(c.raw) {
		if strings.HasPrefix(ref, prefix) {
			found = true
			if e := c.add(ref); e != nil {
				return e
			}
		}
	}
	if !found {
		return fmt.Errorf("CLI 快照缺少阶段目录: %s", prefix)
	}
	return nil
}
func (c *closure) module(ref string) error {
	if e := c.add(ref); e != nil {
		return e
	}
	if c.modules[ref] {
		return nil
	}
	c.modules[ref] = true
	text := string(c.raw[ref].data)
	for _, re := range imports {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			if !strings.HasPrefix(m[1], ".") {
				continue
			}
			target := path.Clean(path.Join(path.Dir(ref), m[1]))
			candidate := ""
			for _, p := range []string{target, target + ".mjs", target + ".js"} {
				if _, ok := c.raw[p]; ok {
					candidate = p
					break
				}
			}
			if candidate == "" && target == "scripts/lib/maintenance-intensity.mjs" {
				continue
			}
			if strings.HasPrefix(candidate, ".agents/skills/") {
				skill := strings.Split(candidate, "/")[2]
				if !c.skills[skill] {
					return requiredSkill{skill, ref}
				}
				continue
			}
			if candidate == "" || !strings.HasPrefix(candidate, "scripts/") {
				return fmt.Errorf("阶段脚本依赖未分发: %s -> %s", ref, m[1])
			}
			if e := c.module(candidate); e != nil {
				return e
			}
		}
	}
	for _, m := range fixedResources.FindAllStringSubmatch(text, -1) {
		target := m[1]
		if _, ok := c.raw[target]; !ok && !strings.HasSuffix(target, ".py") && !strings.HasSuffix(target, ".schema.json") {
			continue
		}
		if e := c.add(target); e != nil {
			return e
		}
	}
	for _, m := range urlResources.FindAllStringSubmatch(text, -1) {
		target := path.Clean(path.Join(path.Dir(ref), m[1]))
		if !strings.HasPrefix(target, "scripts/") && !strings.HasPrefix(target, ".template-spec/") {
			return fmt.Errorf("阶段资源越界")
		}
		if e := c.add(target); e != nil {
			return e
		}
	}
	for _, m := range subprocessEntries.FindAllStringSubmatch(text, -1) {
		target := m[1]
		var e error
		if strings.HasSuffix(target, ".py") || strings.HasSuffix(target, ".json") || strings.HasSuffix(target, ".yaml") {
			e = c.add(target)
		} else {
			e = c.module(target)
		}
		if e != nil {
			return e
		}
	}
	return nil
}
func (c *closure) skill(skill string) error {
	prefix := ".agents/skills/" + skill + "/"
	found := false
	for _, p := range sortedKeys(c.raw) {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		found = true
		if !strings.HasSuffix(p, ".md") && !strings.HasSuffix(p, ".yaml") && !strings.HasSuffix(p, ".json") {
			continue
		}
		for _, ref := range skillRefs.FindAllString(string(c.raw[p].data), -1) {
			ref = strings.TrimRight(ref, ".,;")
			if _, ok := c.raw[ref]; !ok {
				continue
			}
			var e error
			if strings.HasPrefix(ref, "scripts/") {
				e = c.module(ref)
			} else {
				e = c.add(ref)
			}
			if e != nil {
				return e
			}
		}
	}
	if !found {
		return fmt.Errorf("CLI 快照缺少 Skill: %s", skill)
	}
	if skill == "yss-design-system" {
		if e := c.prefix(".template-spec/design/tokens/"); e != nil {
			return e
		}
		return c.module(".agents/skills/yss-design-system/scripts/design-md.mjs")
	}
	return nil
}
func assetClosure(raw map[string]sourceFile, p Policy, stages, skills, resources []string) (Requirement, error) {
	selected := stringSet(skills)
	for attempts := 0; attempts < 100; attempts++ {
		c := &closure{raw: raw, paths: map[string]bool{}, modules: map[string]bool{}, schemas: map[string]bool{}, ids: map[string]string{}, skills: selected}
		var e error
		for _, ref := range p.Common.Files {
			if ref == ".template-spec/process/lifecycle-registry-baseline-v1.json" {
				if _, ok := raw[ref]; !ok {
					var baseline map[string]any
					json.Unmarshal(raw[".template-spec/process/lifecycle-registry-baseline.json"].data, &baseline)
					if baseline["schema_version"] == float64(1) {
						continue
					}
				}
			}
			if e = c.add(ref); e != nil {
				break
			}
		}
		if e == nil {
			for _, ref := range p.Common.Scripts {
				if e = c.module(ref); e != nil {
					break
				}
			}
		}
		if e == nil {
			for _, stage := range stages {
				entry, ok := p.Stages[stage]
				if !ok {
					return Requirement{}, fmt.Errorf("未知生命周期阶段: %s", stage)
				}
				for _, pre := range entry.Prefixes {
					if e = c.prefix(pre); e != nil {
						break
					}
				}
				if e != nil {
					break
				}
				for _, ref := range entry.Files {
					if e = c.add(ref); e != nil {
						break
					}
				}
				if e != nil {
					break
				}
				for _, ref := range entry.Scripts {
					if e = c.module(ref); e != nil {
						break
					}
				}
				if e != nil {
					break
				}
			}
		}
		if e == nil {
			rs := stringSet(resources)
			// Fixed historical snapshots retain their original skill identity.
			for _, skill := range []string{"setup-yss-harness", "yss-harness-upgrade"} {
				if selected[skill] {
					rs[skill] = true
				}
			}
			for _, skill := range sortedKeys(rs) {
				if e = c.skill(skill); e != nil {
					break
				}
			}
		}
		if missing, ok := e.(requiredSkill); ok && !selected[missing.skill] {
			selected[missing.skill] = true
			continue
		}
		if e != nil {
			return Requirement{Paths: []string{}, Skills: sortedKeys(selected)}, e
		}
		return Requirement{Paths: sortedKeys(c.paths), Skills: sortedKeys(selected)}, nil
	}
	return Requirement{}, fmt.Errorf("Skill 闭包超过限制")
}
func buildSelective(b *Bundle, raw map[string]sourceFile, p Policy, renderSet map[string]bool) error {
	var registry struct {
		InstanceDistribution struct {
			InitialSkills []string `yaml:"initial_skills"`
		} `yaml:"instance_distribution"`
	}
	if e := yaml.Unmarshal(raw[".template-spec/agents/yss-skill-registry.yaml"].data, &registry); e != nil {
		return e
	}
	skills := registry.InstanceDistribution.InitialSkills
	if len(skills) == 0 {
		return fmt.Errorf("Skill 注册表缺少 initial_skills")
	}
	// A stage policy change must explicitly account for every authoritative stage.
	re := regexp.MustCompile(`(?m)^  - id: (stage\.[a-z0-9-]+)\s*$`)
	ids := []string{}
	for _, m := range re.FindAllStringSubmatch(string(raw[".template-spec/process/lifecycle-registry.yaml"].data), -1) {
		ids = append(ids, m[1])
	}
	sort.Strings(ids)
	if strings.Join(ids, ",") != strings.Join(sortedKeys(p.Stages), ",") {
		return fmt.Errorf("阶段政策与生命周期注册表不一致")
	}
	stages := []string{"stage.entry-triage", "stage.plan"}
	initial, e := assetClosure(raw, p, stages, skills, nil)
	if e != nil {
		return e
	}
	paths := stringSet(initial.Paths)
	selected := stringSet(initial.Skills)
	b.Distribution = map[string]any{"mode": "selected", "runtimes": []string{"codex"}, "installedSkills": skills, "assetProfile": "stage-selective", "installedStages": stages, "resourceSkills": []string{}}
	for ref, f := range raw {
		if !includedByManifest(ref, p.Manifest, true) || ref == ".cursorrules" {
			continue
		}
		skip := false
		for _, omit := range p.OmitPaths {
			skip = skip || beneath(ref, omit)
		}
		if skip {
			continue
		}
		if strings.HasPrefix(ref, ".template-spec/") || strings.HasPrefix(ref, "scripts/") || strings.HasPrefix(ref, ".vscode/") {
			if !paths[ref] {
				continue
			}
		}
		if strings.HasPrefix(ref, ".cursor/") || strings.HasPrefix(ref, ".pi/") {
			continue
		}
		if strings.HasPrefix(ref, ".agents/skills/") || strings.HasPrefix(ref, ".codex/skills/") {
			name := strings.Split(ref, "/")[2]
			if !strings.HasPrefix(name, ".") && !selected[name] {
				continue
			}
		}
		data := prepareSource("spec", ref, f.data)
		if renderSet[ref] {
			data, e = renderSource("spec", ref, data, true, skills)
			if e != nil {
				return e
			}
		}
		b.Initial[ref] = encodedFile(nativeGuidance("spec", ref, data), f.mode, ownership("spec", ref, p.Manifest))
	}
	b.StageRequirements = map[string]Requirement{}
	for _, stage := range sortedKeys(p.Stages) {
		combined := append([]string{}, skills...)
		combined = append(combined, p.Stages[stage].Skills...)
		req, e := assetClosure(raw, p, append(append([]string{}, stages...), stage), combined, nil)
		if e != nil {
			return fmt.Errorf("stage %s: %w", stage, e)
		}
		req.Skills = additionalSkills(req.Skills, skills)
		b.StageRequirements[stage] = req
	}
	b.SkillRequirements = map[string]Requirement{}
	for _, ref := range sortedKeys(raw) {
		if !strings.HasPrefix(ref, ".agents/skills/") || !strings.HasSuffix(ref, "/SKILL.md") {
			continue
		}
		skill := strings.Split(ref, "/")[2]
		req, e := assetClosure(raw, p, stages, append(append([]string{}, skills...), skill), []string{skill})
		if e != nil {
			req = Requirement{Paths: []string{}, Skills: req.Skills, UnsupportedReason: e.Error()}
		}
		req.Skills = additionalSkills(req.Skills, skills)
		b.SkillRequirements[skill] = req
	}
	return nil
}

func additionalSkills(all, initial []string) []string {
	base := stringSet(initial)
	out := []string{}
	for _, s := range all {
		if !base[s] {
			out = append(out, s)
		}
	}
	return out
}
