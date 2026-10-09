package project

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"go.yaml.in/yaml/v3"
)

const skillRegistryRef = ".template-spec/agents/yss-skill-registry.yaml"

type skillRegistration struct {
	ID       string   `yaml:"id"`
	Layer    string   `yaml:"layer"`
	Maturity string   `yaml:"maturity"`
	Aliases  []string `yaml:"aliases"`
}
type SkillDependency struct {
	From  string `json:"from,omitempty"`
	Skill string `yaml:"skill" json:"skill"`
	Type  string `yaml:"type" json:"type"`
	When  string `yaml:"when" json:"when,omitempty"`
}
type skillRegistry struct {
	Version       int                          `yaml:"schema_version"`
	Status        string                       `yaml:"status"`
	CanonicalRoot string                       `yaml:"canonical_content_root"`
	Skills        []skillRegistration          `yaml:"skills"`
	Platform      []skillRegistration          `yaml:"platform_skills"`
	External      []skillRegistration          `yaml:"external_skills"`
	Dependencies  map[string][]SkillDependency `yaml:"skill_dependencies"`
	Invocation    struct {
		Version   int                       `yaml:"schema_version"`
		Default   map[string]any            `yaml:"default"`
		Layers    map[string]map[string]any `yaml:"layer_defaults"`
		Overrides map[string]map[string]any `yaml:"overrides"`
	} `yaml:"invocation_contract"`
}
type skillLock struct {
	Version         int      `json:"version"`
	ProjectionRoots []string `json:"projectionRoots"`
	Skills          struct {
		Shared map[string]map[string]any `json:"shared"`
	} `json:"skills"`
}
type SkillDetail struct {
	ID           string         `json:"id"`
	Aliases      []string       `json:"aliases"`
	Description  string         `json:"description"`
	Invocation   map[string]any `json:"invocation"`
	Source       map[string]any `json:"source"`
	Installation string         `json:"installation"`
}
type SkillIssue struct {
	Skill  string `json:"skill,omitempty"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}
type ResolvedSkill struct {
	SkillDetail
	Status        string `json:"status"`
	EntryPath     string `json:"entryPath,omitempty"`
	ContentDigest string `json:"contentDigest,omitempty"`
	EffectiveHash string `json:"effectiveHash,omitempty"`
}
type SkillResolution struct {
	ReadOnly     bool              `json:"readOnly"`
	Status       string            `json:"status"`
	AgentRuntime string            `json:"agentRuntime"`
	Requested    []string          `json:"requested"`
	CanonicalIDs []string          `json:"canonicalIds"`
	When         []string          `json:"when"`
	Dependencies []SkillDependency `json:"dependencies"`
	Missing      []string          `json:"missing"`
	Issues       []SkillIssue      `json:"issues"`
	Skills       []ResolvedSkill   `json:"skills"`
}

func querySkillRegistry(b *bundle.Bundle) (*skillRegistry, skillLock, error) {
	var r skillRegistry
	var lock skillLock
	raw, err := b.Files[skillRegistryRef].Render(nil)
	if err == nil {
		err = yaml.Unmarshal(raw, &r)
	}
	if err != nil || r.Version != 3 || r.Status != "active" || r.CanonicalRoot != ".agents/skills" || r.Invocation.Version != 2 {
		return nil, lock, domain.Fail("SKILL", "内置技能注册表不受支持")
	}
	raw, err = b.Files["skills-lock.json"].Render(nil)
	if err == nil {
		err = json.Unmarshal(raw, &lock)
	}
	if err != nil || lock.Version != 3 || lock.Skills.Shared == nil {
		return nil, lock, domain.Fail("SKILL", "内置技能来源锁不受支持")
	}
	return &r, lock, nil
}

func skillInvocation(r *skillRegistry, s skillRegistration) map[string]any {
	out := map[string]any{}
	for _, values := range []map[string]any{r.Invocation.Default, r.Invocation.Layers[s.Layer], r.Invocation.Overrides[s.ID]} {
		for k, v := range values {
			out[k] = v
		}
	}
	return out
}

func skillDeclared(id *Identity, name string, locked bool) bool {
	if locked {
		return true
	}
	if id.Native != nil {
		for _, s := range stringsOf(id.Native.Distribution["installedSkills"]) {
			if s == name {
				return true
			}
		}
	}
	for ref := range baseline(id) {
		for _, root := range []string{".agents/skills", ".codex/skills", ".cursor/skills", ".pi/skills"} {
			if strings.HasPrefix(ref, root+"/"+name+"/") {
				return true
			}
		}
	}
	return false
}

// Details observes declarations and entry presence, never hashes Skill trees.
func skillInstallation(id *Identity, name string, locked bool) string {
	p, err := safefs.Path(id.Root, ".agents/skills/"+name+"/SKILL.md")
	if err != nil {
		return "occupied"
	}
	st, err := os.Lstat(p)
	if err == nil {
		if !st.Mode().IsRegular() || !skillDeclared(id, name, locked) {
			return "occupied"
		}
		return "installed"
	}
	if !os.IsNotExist(err) {
		return "occupied"
	}
	if skillDeclared(id, name, locked) {
		return "managed-missing"
	}
	return "missing"
}

func skillDetail(id *Identity, b *bundle.Bundle, r *skillRegistry, s skillRegistration, source map[string]any, locked bool) (SkillDetail, error) {
	raw, err := b.Files[".agents/skills/"+s.ID+"/SKILL.md"].Render(Variables(id, nil))
	if err != nil {
		return SkillDetail{}, err
	}
	parts := bytes.SplitN(raw, []byte("\n---\n"), 2)
	var front struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if !bytes.HasPrefix(raw, []byte("---\n")) || len(parts) != 2 {
		return SkillDetail{}, domain.Fail("SKILL", "内置技能入口缺少 frontmatter: "+s.ID)
	}
	if err = yaml.Unmarshal(parts[0][4:], &front); err != nil || front.Name != s.ID || front.Description == "" {
		return SkillDetail{}, domain.Fail("SKILL", "内置技能入口身份无效: "+s.ID)
	}
	return SkillDetail{ID: s.ID, Aliases: append([]string{}, s.Aliases...), Description: front.Description, Invocation: skillInvocation(r, s), Source: map[string]any{"kind": "builtin", "templateCommit": b.TemplateCommit, "lock": source}, Installation: skillInstallation(id, s.ID, locked)}, nil
}

func installedSkillLock(id *Identity) (skillLock, error) {
	var lock skillLock
	p, err := safefs.Path(id.Root, "skills-lock.json")
	if err != nil {
		return lock, err
	}
	raw, err := os.ReadFile(p)
	if err == nil {
		err = json.Unmarshal(raw, &lock)
	}
	if err == nil && (lock.Version != 3 || lock.Skills.Shared == nil) {
		err = fmt.Errorf("实例 skills-lock.json 不受支持")
	}
	return lock, err
}

func SkillsDetails(id *Identity, b *bundle.Bundle) (any, error) {
	r, source, err := querySkillRegistry(b)
	if err != nil {
		return nil, err
	}
	installed, err := installedSkillLock(id)
	if err != nil {
		return nil, err
	}
	details := []SkillDetail{}
	for _, s := range r.Skills {
		if _, present := b.Files[".agents/skills/"+s.ID+"/SKILL.md"]; !present {
			continue
		}
		_, locked := installed.Skills.Shared[s.ID]
		d, err := skillDetail(id, b, r, s, source.Skills.Shared[s.ID], locked)
		if err != nil {
			return nil, err
		}
		details = append(details, d)
	}
	sort.Slice(details, func(i, j int) bool { return details[i].ID < details[j].ID })
	return map[string]any{"readOnly": true, "verification": "metadata-only", "skills": details}, nil
}

// Retired IDs come from the fixed registry's declared source, not a CLI copy.
// shortcut: accepts fixed JSON literal declarations; extend parsing if the source contract changes.
func retiredSkillIDs(b *bundle.Bundle) (map[string]bool, error) {
	raw, err := b.Files["scripts/lib/skill-supply-chain.mjs"].Render(nil)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	arrays := regexp.MustCompile(`(?s)(?:OBSOLETE = new Set\(|for \(const id of )(\[[^\]]*\])`).FindAllSubmatch(raw, -1)
	if len(arrays) == 0 {
		return nil, domain.Fail("SKILL", "固定来源缺少退役技能合同")
	}
	concat := regexp.MustCompile(`"([^"\\]*)"\s*\+\s*"([^"\\]*)"`)
	for _, a := range arrays {
		var names []string
		if err := json.Unmarshal(concat.ReplaceAll(a[1], []byte(`"$1$2"`)), &names); err != nil {
			return nil, err
		}
		for _, name := range names {
			out[name] = true
		}
	}
	for _, match := range regexp.MustCompile(`OBSOLETE\.add\("([^"\\]+)"\)`).FindAllSubmatch(raw, -1) {
		out[string(match[1])] = true
	}
	return out, nil
}

func ResolveSkills(id *Identity, b *bundle.Bundle, requested []string, runtime, when string) (*SkillResolution, error) {
	if len(requested) == 0 || runtime != "codex" {
		return nil, domain.Fail("ARGUMENT", "需要 skills resolve <标识...> --agent-runtime codex")
	}
	r, source, err := querySkillRegistry(b)
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
	out := &SkillResolution{ReadOnly: true, Status: "ready", AgentRuntime: runtime, Requested: append([]string{}, requested...), CanonicalIDs: []string{}, When: []string{}, Dependencies: []SkillDependency{}, Missing: []string{}, Issues: []SkillIssue{}, Skills: []ResolvedSkill{}}
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
	issue := func(skill, code, reason string) {
		out.Issues = append(out.Issues, SkillIssue{Skill: skill, Code: code, Reason: reason})
		out.Status = "blocked"
	}
	vars, managed := Variables(id, nil), baseline(id)
	if id.Native == nil {
		issue("", "native-metadata-required", "当前就绪核验需要可验证的原生受管状态；不会自动迁移")
	} else if id.Native.TemplateCommit != b.TemplateCommit || id.Native.SnapshotHash != b.SnapshotHash || id.Native.ManifestHash != b.ManifestHash || id.Native.BundleHash != "" && id.Native.BundleHash != b.BundleHash {
		issue("", "cli-snapshot-mismatch", "当前实例与固定 Bundle 来源不匹配")
	}
	for _, ref := range []string{skillRegistryRef, "skills-lock.json"} {
		d, err := safefs.Describe(id.Root, ref)
		m, ok := managed[ref]
		if err != nil || !ok || d != m.Applied {
			issue("", "managed-state-drift", ref+" 受管状态缺失或漂移")
		}
		if ref == skillRegistryRef {
			raw, e := b.Files[ref].Render(vars)
			if e != nil || d.Digest != safefs.Digest(raw) {
				issue("", "registry-source-mismatch", "实例注册表与固定 Bundle 不匹配")
			}
		}
	}
	installed, err := installedSkillLock(id)
	if err != nil {
		issue("", "skill-lock-invalid", err.Error())
	}
	commonBlocked := out.Status == "blocked"
	for _, name := range out.CanonicalIDs {
		_, locked := installed.Skills.Shared[name]
		d, err := skillDetail(id, b, r, registered[name], source.Skills.Shared[name], locked)
		if err != nil {
			return nil, err
		}
		item := ResolvedSkill{SkillDetail: d, Status: "blocked"}
		start := len(out.Issues)
		prefixes := []string{".agents/skills/" + name, ".codex/skills/" + name}
		present := false
		for _, prefix := range prefixes {
			p, e := safefs.Path(id.Root, prefix)
			if e != nil {
				issue(name, "path-conflict", e.Error())
				continue
			}
			st, e := os.Lstat(p)
			if e == nil {
				present = true
				if !st.IsDir() {
					issue(name, "path-conflict", "技能路径不是普通目录: "+prefix)
				}
			} else if !os.IsNotExist(e) {
				issue(name, "path-conflict", e.Error())
			}
		}
		if !present && !skillDeclared(id, name, locked) && start == len(out.Issues) {
			item.Status = "missing"
			out.Missing = append(out.Missing, name)
			if out.Status == "ready" {
				out.Status = "missing"
			}
		} else {
			entry := installed.Skills.Shared[name]
			if !locked {
				issue(name, "skill-source-unlocked", "技能未登记在实例来源锁")
			}
			keys := []string{}
			for key := range source.Skills.Shared[name] {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				want := source.Skills.Shared[name][key]
				if key != "targets" && key != "effectiveHash" && !reflect.DeepEqual(entry[key], want) {
					issue(name, "skill-source-mismatch", "技能来源锁字段不匹配: "+key)
				}
			}
			if !containsString(installed.ProjectionRoots, ".codex/skills") || !containsString(stringsOf(entry["targets"]), ".codex/skills") {
				issue(name, "runtime-unavailable", "Codex 投影未登记")
			}
			expected, err := bundle.SkillTreeHash(b.Files, prefixes[0], vars)
			if err != nil {
				return nil, err
			}
			if text(entry["effectiveHash"]) != expected {
				issue(name, "skill-source-mismatch", "技能锁摘要与固定 Bundle 不匹配")
			}
			entryDigest := ""
			for _, prefix := range prefixes {
				files, e := observedSkillTree(id.Root, prefix, b, vars, managed)
				if e != nil {
					issue(name, "skill-drift", e.Error())
					continue
				}
				hash, e := bundle.SkillTreeHash(files, prefix, nil)
				if e != nil || hash != expected {
					issue(name, "skill-drift", "技能资源或投影摘要不匹配: "+prefix)
				}
				if prefix == prefixes[0] {
					entryDigest = files[prefix+"/SKILL.md"].Digest
				}
			}
			if start == len(out.Issues) && !commonBlocked {
				item.Status = "ready"
				item.EntryPath = filepath.Join(id.Root, filepath.FromSlash(prefixes[0]), "SKILL.md")
				item.EffectiveHash = expected
				item.ContentDigest = entryDigest
			}
		}
		out.Skills = append(out.Skills, item)
	}
	return out, nil
}

func containsString(values []string, name string) bool {
	for _, v := range values {
		if v == name {
			return true
		}
	}
	return false
}

func observedSkillTree(root, prefix string, b *bundle.Bundle, vars map[string]string, managed map[string]Managed) (map[string]bundle.File, error) {
	p, err := safefs.Path(root, prefix)
	if err != nil {
		return nil, err
	}
	files := map[string]bundle.File{}
	err = filepath.WalkDir(p, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		ref := filepath.ToSlash(rel)
		if bundle.IgnoredSkillFile(strings.TrimPrefix(ref, prefix+"/")) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if _, e = safefs.Path(root, ref); e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		expected, known := b.Files[ref]
		if !known {
			return fmt.Errorf("技能路径被未登记资源占用: %s", ref)
		}
		actual, e := safefs.Describe(root, ref)
		if e != nil {
			return e
		}
		m, known := managed[ref]
		if !known || m.Applied != actual || m.Ownership != expected.Ownership {
			return fmt.Errorf("受管文件缺失或字节/权限漂移: %s", ref)
		}
		raw, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		want, e := expected.Render(vars)
		if e != nil {
			return e
		}
		if !bytes.Equal(raw, want) || uint32(actual.Mode) != expected.Mode {
			return fmt.Errorf("文件与固定来源不匹配: %s", ref)
		}
		files[ref] = bundle.File{Data: base64.StdEncoding.EncodeToString(raw), Digest: safefs.Digest(raw), Mode: uint32(actual.Mode)}
		return nil
	})
	if err != nil {
		return nil, err
	}
	refs := []string{}
	for ref := range b.Files {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	for _, ref := range refs {
		if strings.HasPrefix(ref, prefix+"/") && !bundle.IgnoredSkillFile(strings.TrimPrefix(ref, prefix+"/")) {
			if _, ok := files[ref]; !ok {
				return nil, fmt.Errorf("受管文件缺失: %s", ref)
			}
		}
	}
	return files, nil
}
