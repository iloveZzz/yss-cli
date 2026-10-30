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
	Source   string   `yaml:"source"`
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
		for _, root := range []string{".agents/skills", ".claude/skills", ".codex/skills", ".cursor/skills", ".pi/skills", ".workbuddy/skills"} {
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

// Compatibility reader for fixed v1 sources; v2 never parses JavaScript.
func legacyRetiredSkillIDs(b *bundle.Bundle) (map[string]bool, error) {
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

func runtimeSkillRoot(runtime string) (string, bool) {
	return domain.SkillRuntimeRoot(runtime)
}

func runtimeDeclarationIssue(id *Identity, installed skillLock) string {
	declared := stringsOf(id.Native.Distribution["runtimes"])
	roots := []string{}
	for _, runtime := range declared {
		root, ok := domain.SkillRuntimeRoot(runtime)
		if !ok {
			return "实例声明未知运行时"
		}
		roots = append(roots, root)
	}
	if len(union(declared)) != len(declared) || len(union(installed.ProjectionRoots)) != len(installed.ProjectionRoots) || !reflect.DeepEqual(union(roots), union(installed.ProjectionRoots)) {
		return "运行时声明与来源锁投影范围不一致"
	}
	return ""
}

type SkillReadiness struct {
	Status   string       `json:"status"`
	Scope    string       `json:"scope"`
	Checked  []string     `json:"checked"`
	Missing  []string     `json:"missing"`
	Issues   []SkillIssue `json:"issues"`
	Runtimes []string     `json:"runtimes"`
}

func CheckInstalledSkills(id *Identity, b *bundle.Bundle) (*SkillReadiness, error) {
	out := &SkillReadiness{Status: "not-evaluated", Scope: "installed-skills", Checked: []string{}, Missing: []string{}, Issues: []SkillIssue{}, Runtimes: []string{}}
	if id.Native == nil || id.Profile.Name != "spec" || id.Native.SchemaVersion != 5 {
		return out, nil
	}
	for _, field := range []string{"installedSkills", "runtimes"} {
		switch value := id.Native.Distribution[field].(type) {
		case nil, []string:
		case []any:
			if len(stringsOf(value)) != len(value) {
				return nil, domain.Fail("SKILL", "安装声明必须为字符串数组: "+field)
			}
		default:
			return nil, domain.Fail("SKILL", "安装声明必须为字符串数组: "+field)
		}
	}
	out.Checked = union(stringsOf(id.Native.Distribution["installedSkills"]))
	if len(out.Checked) == 0 {
		return out, nil
	}
	out.Runtimes = union(stringsOf(id.Native.Distribution["runtimes"]))
	out.Status = "ready"
	seen := map[SkillIssue]bool{}
	if installed, err := installedSkillLock(id); err == nil {
		if reason := runtimeDeclarationIssue(id, installed); reason != "" {
			issue := SkillIssue{Code: "runtime-unavailable", Reason: reason}
			out.Status = "blocked"
			out.Issues = append(out.Issues, issue)
			seen[issue] = true
		}
	}
	for _, runtime := range append([]string{""}, out.Runtimes...) {
		if _, known := domain.SkillRuntimeRoot(runtime); runtime != "" && !known {
			continue
		}
		result, err := ResolveSkills(id, b, out.Checked, runtime, "")
		if err != nil {
			return nil, err
		}
		if result.Status == "blocked" || result.Status == "missing" && out.Status == "ready" {
			out.Status = result.Status
		}
		out.Missing = union(out.Missing, result.Missing)
		for _, issue := range result.Issues {
			if !seen[issue] {
				seen[issue] = true
				out.Issues = append(out.Issues, issue)
			}
		}
	}
	sort.Slice(out.Issues, func(i, j int) bool {
		a, b := out.Issues[i], out.Issues[j]
		if a.Skill != b.Skill {
			return a.Skill < b.Skill
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Reason < b.Reason
	})
	return out, nil
}

func ResolveSkills(id *Identity, b *bundle.Bundle, requested []string, runtime, when string) (*SkillResolution, error) {
	runtimeRoot, _ := runtimeSkillRoot(runtime)
	if len(requested) == 0 || (runtime != "" && runtimeRoot == "") {
		return nil, domain.Fail("ARGUMENT", "需要 skills resolve <标识...>；投影校验可加 --agent-runtime claude|codex|cursor|pi|workbuddy")
	}
	r, source, err := querySkillRegistry(b)
	if err != nil {
		return nil, err
	}
	selected, err := resolveSkillSelection(b, requested, when)
	if err != nil {
		return nil, err
	}
	registered := map[string]skillRegistration{}
	for _, item := range r.Skills {
		registered[item.ID] = item
	}
	out := &SkillResolution{ReadOnly: true, Status: "ready", AgentRuntime: runtime, Requested: append([]string{}, requested...), CanonicalIDs: selected.CanonicalIDs, When: selected.When, Dependencies: selected.Dependencies, Missing: []string{}, Issues: []SkillIssue{}, Skills: []ResolvedSkill{}}
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
		if ref == skillRegistryRef && skillsIdentity(id) {
			continue
		}
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
	if runtime == "claude" || runtime == "workbuddy" {
		if id.Native == nil || !containsString(stringsOf(id.Native.Distribution["runtimes"]), runtime) {
			issue("", "runtime-unavailable", "所选运行时未安装；请先核对 sync 计划")
		}
		if id.Native != nil {
			if reason := runtimeDeclarationIssue(id, installed); reason != "" {
				issue("", "runtime-unavailable", reason)
			}
		}
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
		prefixes := []string{".agents/skills/" + name}
		if runtimeRoot != "" {
			prefixes = append(prefixes, runtimeRoot+"/"+name)
		}
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
			if skillsIdentity(id) || IsMattSpec(id) {
				installation := "skills"
				if IsMattSpec(id) {
					installation = "governed"
				}
				references, refErr := b.Installations[installation].ReferencesForSkill(name)
				if refErr != nil {
					issue(name, "skill-reference-invalid", refErr.Error())
				}
				for _, ref := range references {
					file, ok := b.Files[ref]
					if !ok {
						issue(name, "skill-reference-missing", "固定 Bundle 缺少技能引用: "+ref)
						continue
					}
					if ref == ".template-spec/process/harness-profile.yaml" {
						file, err = nativeProfileFile(file, id.Profile)
						if err != nil {
							issue(name, "skill-reference-missing-or-drift", "技能引用转换失败: "+ref)
							continue
						}
					}
					observed, e := safefs.Describe(id.Root, ref)
					previous, tracked := managed[ref]
					source := previous.Source
					if e != nil || !tracked || observed.Type != "file" || observed != previous.Applied || previous.Applied != previous.Baseline || source == nil || source.Kind != "bundle" || source.Path != ref || source.TemplateCommit != b.TemplateCommit || source.SnapshotHash != b.SnapshotHash || (b.BundleHash != "" && source.BundleHash != b.BundleHash) || source.TemplateDigest != file.Digest {
						issue(name, "skill-reference-missing-or-drift", "技能引用缺失或漂移: "+ref)
					}
				}
			}
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
			if runtimeRoot != "" && (!containsString(installed.ProjectionRoots, runtimeRoot) || (!containsString(stringsOf(entry["targets"]), runtimeRoot) || !containsString(stringsOf(entry["targets"]), ".agents/skills"))) {
				issue(name, "runtime-unavailable", "所选运行时投影未登记")
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
		if strings.HasPrefix(ref, ".workbuddy/skills/") {
			canonicalRef := ".agents/skills/" + strings.TrimPrefix(ref, ".workbuddy/skills/")
			canonical, known := b.Files[canonicalRef]
			canonicalRaw, e := canonical.Render(vars)
			if !known || e != nil || !bytes.Equal(raw, canonicalRaw) || uint32(actual.Mode) != canonical.Mode {
				return fmt.Errorf("WorkBuddy 投影原字节或权限与 canonical 不一致: %s", ref)
			}
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
