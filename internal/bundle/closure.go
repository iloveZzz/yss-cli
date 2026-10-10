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

var imports = []*regexp.Regexp{regexp.MustCompile(`\b(?:import|export)\s+(?:[^'";]*?\s+from\s*)?['"]([^'"]+)['"]`), regexp.MustCompile(`\b(?:require|import)\s*\(\s*['"\x60]([^'"\x60]+)['"\x60]\s*\)`)}
var fixedResources = regexp.MustCompile(`['"]((?:scripts/[\p{L}\p{N}_.\/-]+\.py|\.template-spec/[\p{L}\p{N}_.\/-]+\.(?:json|yaml)))['"]`)
var urlResources = regexp.MustCompile(`\bnew\s+URL\(\s*['"\x60](\.[^'"\x60]+\.(?:json|yaml|py))['"\x60]\s*,\s*import\.meta\.url\s*\)`)
var subprocessEntries = regexp.MustCompile(`\b(?:spawnSync|spawn|execFileSync|execFile)\(\s*(?:(?:process\.execPath|['"](?:node|python3)['"])\s*,\s*\[\s*)?(?:path\.)?(?:join|resolve)\(\s*(?:ROOT|TEMPLATE_ROOT)\s*,\s*['"](scripts/[\p{L}\p{N}_.\/-]+)['"]\s*\)`)
var localScriptDirectory = regexp.MustCompile(`\bconst\s+SCRIPT_DIR\s*=\s*path\.dirname\(\s*fileURLToPath\(\s*import\.meta\.url\s*\)\s*\)`)
var localSubprocessBinding = regexp.MustCompile(`\bconst\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*path\.(?:join|resolve)\(\s*SCRIPT_DIR\s*,\s*['"]([\p{L}\p{N}_.\/-]+)['"]\s*\)`)
var boundSubprocessEntries = regexp.MustCompile(`\b(?:spawnSync|spawn|execFileSync|execFile)\(\s*(?:process\.execPath|['"](?:node|python3)['"])\s*,\s*\[\s*([A-Za-z_$][A-Za-z0-9_$]*)\b`)
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

// Mark syntax positions for the existing static-reference patterns. Comments
// and literal contents cannot start imports, while template expressions remain
// code. Keep the original bytes so quoted module/resource arguments survive.
func moduleSyntax(text string) (string, []bool) {
	active := make([]bool, len(text))
	masked := []byte(text)
	comment := func(start, end int) {
		for i := start; i < end && i < len(masked); i++ {
			if masked[i] != '\n' && masked[i] != '\r' {
				masked[i] = ' '
			}
		}
	}
	word := func(b byte) bool {
		return b == '_' || b == '$' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b >= 128
	}
	var code func(int, bool) int
	var template func(int) int
	quoted := func(i int, quote byte) int {
		active[i] = true
		for i++; i < len(text); i++ {
			if text[i] == '\\' {
				i++
			} else if text[i] == quote {
				return i + 1
			}
		}
		return len(text)
	}
	template = func(i int) int {
		active[i] = true
		for i++; i < len(text); {
			if text[i] == '\\' {
				i += 2
			} else if text[i] == '`' {
				return i + 1
			} else if text[i] == '$' && i+1 < len(text) && text[i+1] == '{' {
				i = code(i+2, true)
			} else {
				i++
			}
		}
		return len(text)
	}
	code = func(i int, expression bool) int {
		depth, operand, property := 1, true, false
		for i < len(text) {
			b := text[i]
			if b == '/' && i+1 < len(text) && text[i+1] == '/' {
				start := i
				for i < len(text) && text[i] != '\n' {
					i++
				}
				comment(start, i)
				continue
			}
			if b == '/' && i+1 < len(text) && text[i+1] == '*' {
				start := i
				i += 2
				for i+1 < len(text) && (text[i] != '*' || text[i+1] != '/') {
					i++
				}
				i += 2
				comment(start, i)
				continue
			}
			if b == '\'' || b == '"' {
				i = quoted(i, b)
				operand = false
				continue
			}
			if b == '`' {
				i = template(i)
				operand = false
				continue
			}
			if b == '/' && operand {
				active[i] = true
				i++
				inClass := false
				for i < len(text) {
					if text[i] == '\\' {
						i += 2
						continue
					}
					if text[i] == '[' {
						inClass = true
					}
					if text[i] == ']' {
						inClass = false
					}
					if text[i] == '/' && !inClass {
						i++
						break
					}
					i++
				}
				for i < len(text) && word(text[i]) {
					i++
				}
				operand = false
				continue
			}
			active[i] = true
			if word(b) {
				start := i
				for i < len(text) && word(text[i]) {
					active[i] = true
					i++
				}
				switch text[start:i] {
				case "return", "throw", "case", "delete", "typeof", "void", "new", "in", "instanceof", "yield", "await", "else", "do":
					operand = !property
				default:
					operand = false
				}
				property = false
				continue
			}
			if (b == '+' || b == '-') && i+1 < len(text) && text[i+1] == b {
				// Prefix ++/-- still expects an operand; postfix ++/-- does not.
				active[i+1] = true
				i += 2
				continue
			}
			if expression && b == '{' {
				depth++
			}
			if expression && b == '}' {
				depth--
				if depth == 0 {
					return i + 1
				}
			}
			if !strings.ContainsRune(" \t\r\n", rune(b)) {
				operand = !strings.ContainsRune(")]}.", rune(b))
				property = b == '.'
			}
			i++
		}
		return i
	}
	code(0, false)
	return string(masked), active
}

func syntaxMatches(pattern *regexp.Regexp, text string, active []bool) [][]string {
	var result [][]string
	for _, indices := range pattern.FindAllStringSubmatchIndex(text, -1) {
		if !active[indices[0]] {
			continue
		}
		match := make([]string, len(indices)/2)
		for i := range match {
			if indices[2*i] >= 0 {
				match[i] = text[indices[2*i]:indices[2*i+1]]
			}
		}
		result = append(result, match)
	}
	return result
}

// A selected canonical Skill may consume static resources inside its own
// namespace. This does not authorize another Skill or an arbitrary root path.
func (c *closure) ownSkillResource(source, target string) bool {
	parts := strings.Split(source, "/")
	if len(parts) < 4 || parts[0] != ".agents" || parts[1] != "skills" || !c.skills[parts[2]] {
		return false
	}
	prefix := ".agents/skills/" + parts[2] + "/"
	_, canonical := c.raw[prefix+"SKILL.md"]
	return canonical && strings.HasPrefix(target, prefix)
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
						if !strings.HasPrefix(target, ".template-spec/") && !strings.HasPrefix(target, "scripts/") && !c.ownSkillResource(ref, target) {
							return fmt.Errorf("阶段 Schema 依赖越界: %s -> %s", ref, target)
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
	text, active := moduleSyntax(string(c.raw[ref].data))
	for _, re := range imports {
		for _, m := range syntaxMatches(re, text, active) {
			if strings.Contains(m[1], "${") && strings.Contains(m[0], "`") {
				continue
			}
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
				if e := c.module(candidate); e != nil {
					return e
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
	for _, m := range syntaxMatches(fixedResources, text, active) {
		target := m[1]
		if _, ok := c.raw[target]; !ok && !strings.HasSuffix(target, ".py") && !strings.HasSuffix(target, ".schema.json") {
			continue
		}
		if e := c.add(target); e != nil {
			return e
		}
	}
	for _, m := range syntaxMatches(urlResources, text, active) {
		if strings.Contains(m[1], "${") && strings.Contains(m[0], "`") {
			continue
		}
		target := path.Clean(path.Join(path.Dir(ref), m[1]))
		if !strings.HasPrefix(target, "scripts/") && !strings.HasPrefix(target, ".template-spec/") && !c.ownSkillResource(ref, target) {
			return fmt.Errorf("阶段资源越界: %s -> %s", ref, target)
		}
		if e := c.add(target); e != nil {
			return e
		}
	}
	for _, m := range syntaxMatches(subprocessEntries, text, active) {
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
	if len(syntaxMatches(localScriptDirectory, text, active)) > 0 {
		bindings := map[string]string{}
		for _, m := range syntaxMatches(localSubprocessBinding, text, active) {
			bindings[m[1]] = path.Clean(path.Join(path.Dir(ref), m[2]))
		}
		for _, m := range syntaxMatches(boundSubprocessEntries, text, active) {
			target, bound := bindings[m[1]]
			if !bound {
				continue
			}
			if !strings.HasPrefix(target, "scripts/") && !c.ownSkillResource(ref, target) {
				return fmt.Errorf("阶段子进程入口越界: %s -> %s", ref, target)
			}
			if e := c.module(target); e != nil {
				return e
			}
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
			// Skill commands are relative to their canonical Skill root. Resolve
			// documented own-script entries before falling back to shared scripts;
			// their static imports use the same guarded module closure as stages.
			own := prefix + ref
			if strings.HasPrefix(ref, "scripts/") {
				if _, ok := c.raw[own]; ok {
					if e := c.module(own); e != nil {
						return e
					}
					continue
				}
			}
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
		data, e := prepareSource("spec", ref, f.data)
		if e != nil {
			return e
		}
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
