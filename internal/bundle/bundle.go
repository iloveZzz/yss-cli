package bundle

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"regexp"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

type File struct {
	Data      string `json:"data"`
	Digest    string `json:"digest"`
	Mode      uint32 `json:"mode"`
	Ownership string `json:"ownership"`
}
type RetiredSkill struct {
	Replacements []string `json:"replacements"`
	Reason       string   `json:"reason"`
}
type Requirement struct {
	Paths  []string `json:"paths"`
	Skills []string `json:"skills,omitempty"`
}
type Installation struct {
	Initial           map[string]File        `json:"initial"`
	Distribution      map[string]any         `json:"distribution"`
	SkillReferences   map[string][]string    `json:"skillReferences,omitempty"`
	SkillRequirements map[string]Requirement `json:"skillRequirements,omitempty"`
}
type Provenance struct {
	Version     string `json:"version"`
	Commit      string `json:"commit,omitempty"`
	SourceState string `json:"sourceState"`
}
type PolicyProvenance struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Kind   string `json:"kind"`
}
type Bundle struct {
	RetiredSkills     map[string]RetiredSkill `json:"retiredSkills,omitempty"`
	SchemaVersion     int                     `json:"schemaVersion"`
	Profile           string                  `json:"profile"`
	TemplateVersion   string                  `json:"templateVersion"`
	TemplateCommit    string                  `json:"templateCommit"`
	SourceState       string                  `json:"sourceState"`
	SnapshotHash      string                  `json:"sourceSnapshotHash"`
	ManifestHash      string                  `json:"manifestHash"`
	BundleHash        string                  `json:"bundleHash"`
	Producer          Provenance              `json:"producer"`
	SourcePolicy      PolicyProvenance        `json:"sourcePolicy"`
	Manifest          map[string]any          `json:"manifest"`
	Distribution      map[string]any          `json:"distribution"`
	Files             map[string]File         `json:"files"`
	Initial           map[string]File         `json:"initial"`
	Installations     map[string]Installation `json:"installations"`
	SkillRequirements map[string]Requirement  `json:"skillRequirements"`
}

// Bound archive reads to limit memory use, independently of release/package size policy.
const MaxArchiveBytes = 256 << 20
const maxSnapshotBytes = 256 << 20

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var skillNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

func (f File) Render(vars map[string]string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(f.Data)
	if err != nil {
		return nil, err
	}
	if safefs.Digest(raw) != f.Digest {
		return nil, fmt.Errorf("文件摘要不一致")
	}
	if bytes.IndexByte(raw, 0) >= 0 {
		return raw, nil
	}
	text := string(raw)
	for k, token := range map[string]string{"projectName": "__YSS_PROJECT_NAME__", "businessDomain": "__YSS_BUSINESS_DOMAIN__", "teamSize": "__YSS_TEAM_SIZE__"} {
		if value, ok := vars[k]; ok {
			text = strings.ReplaceAll(text, token, value)
		}
	}
	return []byte(text), nil
}
func Hash(b *Bundle) string {
	copy := *b
	copy.BundleHash = ""
	raw, _ := json.Marshal(copy)
	return safefs.Digest(raw)
}
func validate(b *Bundle) error {
	if b == nil || b.SchemaVersion != 6 || (b.Profile != "spec" && b.Profile != "backend") || !commitPattern.MatchString(b.TemplateCommit) || b.TemplateVersion == "" || (b.SourceState != "committed" && b.SourceState != "working-tree") {
		return fmt.Errorf("Bundle 身份不合法")
	}
	if !digestPattern.MatchString(b.SnapshotHash) || !digestPattern.MatchString(b.ManifestHash) || !digestPattern.MatchString(b.SourcePolicy.Digest) || b.BundleHash != Hash(b) {
		return fmt.Errorf("Bundle 来源或摘要不一致")
	}
	if b.SourcePolicy.Kind != b.SourceState || b.Producer.SourceState != b.SourceState {
		return fmt.Errorf("Bundle 来源状态不一致")
	}
	if b.SourceState == "working-tree" && b.TemplateVersion != "worktree:"+b.SnapshotHash {
		return fmt.Errorf("工作树版本不匹配")
	}
	manifest, _ := json.Marshal(b.Manifest)
	if safefs.Digest(manifest) != b.ManifestHash {
		return fmt.Errorf("清单摘要不一致")
	}
	snapshot, _ := json.Marshal(b.Files)
	if safefs.Digest(snapshot) != b.SnapshotHash {
		return fmt.Errorf("源码快照摘要不一致")
	}
	if len(b.Installations) != 2 {
		return fmt.Errorf("Bundle 必须包含 project/skills 安装")
	}
	for _, kind := range []string{"project", "skills"} {
		i, ok := b.Installations[kind]
		if !ok || i.Initial == nil || i.Distribution["installation"] != kind {
			return fmt.Errorf("缺少安装集合 %s", kind)
		}
	}
	sets := []map[string]File{b.Files, b.Initial}
	for _, i := range b.Installations {
		sets = append(sets, i.Initial)
	}
	for _, files := range sets {
		var paths safefs.PathSet
		for ref, f := range files {
			if err := paths.Add(ref); err != nil {
				return err
			}
			if f.Mode != 0644 && f.Mode != 0755 {
				return fmt.Errorf("不合法权限 %s", ref)
			}
			switch f.Ownership {
			case "managed", "managed-customizable", "generated", "user-owned", "protected":
			default:
				return fmt.Errorf("不合法 ownership %s", ref)
			}
			if _, err := f.Render(nil); err != nil {
				return fmt.Errorf("%s: %w", ref, err)
			}
		}
	}
	for _, i := range b.Installations {
		if i.SkillRequirements != nil {
			if err := validateRequirements(b.Files, i.SkillRequirements); err != nil {
				return err
			}
		}
		for skill, refs := range i.SkillReferences {
			if strings.Contains(skill, "/") {
				return fmt.Errorf("技能名称不合法 %s", skill)
			}
			if _, ok := b.Files[".agents/skills/"+skill+"/SKILL.md"]; !ok {
				return fmt.Errorf("技能不存在 %s", skill)
			}
			for _, ref := range refs {
				if _, ok := b.Files[ref]; !ok {
					return fmt.Errorf("技能引用不存在 %s", ref)
				}
			}
		}
	}
	return validateRetirements(b)
}
func validateRetirements(b *Bundle) error {
	for _, skill := range sortedKeys(b.RetiredSkills) {
		retired := b.RetiredSkills[skill]
		_, current := b.Files[".agents/skills/"+skill+"/SKILL.md"]
		if !skillNamePattern.MatchString(skill) || current || strings.TrimSpace(retired.Reason) == "" {
			return fmt.Errorf("退役技能声明无效 %s", skill)
		}
		for _, replacement := range retired.Replacements {
			if _, ok := b.Files[".agents/skills/"+replacement+"/SKILL.md"]; !ok || !skillNamePattern.MatchString(replacement) {
				return fmt.Errorf("退役技能 %s 的替代技能不存在 %s", skill, replacement)
			}
			if _, retired := b.RetiredSkills[replacement]; retired {
				return fmt.Errorf("退役技能 %s 不能引用退役替代 %s", skill, replacement)
			}
		}
	}
	return nil
}
func Load(profile string) (*Bundle, error) {
	if len(packedData) == 0 {
		return nil, fmt.Errorf("没有内嵌模板，请运行 tools/bundle")
	}
	if len(packedData) > MaxArchiveBytes {
		return nil, fmt.Errorf("Bundle 超过限制")
	}
	z, err := gzip.NewReader(bytes.NewReader(packedData))
	if err != nil {
		return nil, err
	}
	defer z.Close()
	raw, err := io.ReadAll(io.LimitReader(z, maxSnapshotBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxSnapshotBytes {
		return nil, fmt.Errorf("Bundle 解压后超过限制")
	}
	bundles, err := unpack(raw)
	if err != nil {
		return nil, err
	}
	b := bundles[profile]
	if err = validate(b); err != nil {
		return nil, err
	}
	if b.Profile != profile {
		return nil, fmt.Errorf("Bundle profile 不匹配")
	}
	return b, nil
}
func (b *Bundle) ForInstallation(kind string) (*Bundle, error) {
	if kind != "project" && kind != "skills" {
		return nil, fmt.Errorf("未知安装方式 %s", kind)
	}
	i, ok := b.Installations[kind]
	if !ok {
		return nil, fmt.Errorf("缺少安装集合 %s", kind)
	}
	view := *b
	view.Initial = maps.Clone(i.Initial)
	view.Files = maps.Clone(b.Files)
	view.Distribution = maps.Clone(i.Distribution)
	for key, value := range view.Distribution {
		switch a := value.(type) {
		case []string:
			view.Distribution[key] = append([]string(nil), a...)
		case []any:
			view.Distribution[key] = append([]any(nil), a...)
		}
	}
	view.SkillRequirements = copyRequirements(i.SkillRequirements)
	if i.SkillRequirements == nil {
		view.SkillRequirements = copyRequirements(b.SkillRequirements)
		for skill, req := range view.SkillRequirements {
			req.Paths = append([]string(nil), i.SkillReferences[skill]...)
			view.SkillRequirements[skill] = req
		}
		for skill, refs := range i.SkillReferences {
			req := view.SkillRequirements[skill]
			req.Paths = append([]string(nil), refs...)
			view.SkillRequirements[skill] = req
		}
	}
	return &view, nil
}
func copyRequirements(input map[string]Requirement) map[string]Requirement {
	out := make(map[string]Requirement, len(input))
	for skill, req := range input {
		out[skill] = Requirement{Paths: append([]string(nil), req.Paths...), Skills: append([]string(nil), req.Skills...)}
	}
	return out
}
func validateRequirements(files map[string]File, requirements map[string]Requirement) error {
	exists := func(skill string) bool {
		_, ok := files[".agents/skills/"+skill+"/SKILL.md"]
		return skillNamePattern.MatchString(skill) && ok
	}
	for _, skill := range sortedKeys(requirements) {
		if !exists(skill) {
			return fmt.Errorf("技能不存在或名称不合法 %s", skill)
		}
		req := requirements[skill]
		for _, ref := range req.Paths {
			if err := safefs.ValidateRef(ref); err != nil {
				return fmt.Errorf("技能 %s 引用 %s: %w", skill, ref, err)
			}
			if _, ok := files[ref]; !ok {
				return fmt.Errorf("技能 %s 引用不存在 %s", skill, ref)
			}
		}
		for _, dep := range req.Skills {
			if !exists(dep) {
				return fmt.Errorf("技能 %s 依赖不存在或名称不合法 %s", skill, dep)
			}
		}
	}
	state := map[string]int{}
	var visit func(string, []string) error
	visit = func(skill string, chain []string) error {
		if state[skill] == 1 {
			return fmt.Errorf("技能依赖环 %s", strings.Join(append(chain, skill), " -> "))
		}
		if state[skill] == 2 {
			return nil
		}
		state[skill] = 1
		for _, dep := range requirements[skill].Skills {
			if err := visit(dep, append(chain, skill)); err != nil {
				return err
			}
		}
		state[skill] = 2
		return nil
	}
	for _, skill := range sortedKeys(requirements) {
		if err := visit(skill, nil); err != nil {
			return err
		}
	}
	return nil
}
func (i Installation) ReferencesForSkill(skill string) ([]string, error) {
	return append([]string(nil), i.SkillReferences[skill]...), nil
}
func (i Installation) ReferenceOwners() []string { return sortedKeys(i.SkillReferences) }

func skillPath(ref, skill string) bool {
	parts := strings.Split(ref, "/")
	return len(parts) >= 4 && parts[1] == "skills" && parts[2] == skill && strings.HasPrefix(parts[0], ".")
}
func (b *Bundle) SkillFiles(names []string) (map[string]File, error) {
	out := map[string]File{}
	seen := map[string]bool{}
	var add func(string) error
	add = func(name string) error {
		if seen[name] {
			return nil
		}
		seen[name] = true
		if retired, ok := b.RetiredSkills[name]; ok {
			message := "技能 " + name + " 已退役: " + retired.Reason
			if len(retired.Replacements) > 0 {
				message += " 替代技能: " + strings.Join(retired.Replacements, ", ")
			}
			return domain.Fail("SKILL_RETIRED", message)
		}
		if _, ok := b.Files[".agents/skills/"+name+"/SKILL.md"]; !ok {
			return fmt.Errorf("未知技能 %s", name)
		}
		for ref, f := range b.Files {
			if skillPath(ref, name) {
				out[ref] = f
			}
		}
		req := b.SkillRequirements[name]
		for _, ref := range req.Paths {
			f, ok := b.Files[ref]
			if !ok {
				return fmt.Errorf("技能引用缺失 %s", ref)
			}
			out[ref] = f
		}
		for _, dep := range req.Skills {
			if err := add(dep); err != nil {
				return err
			}
		}
		return nil
	}
	for _, name := range names {
		if err := add(name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

type archive struct {
	SchemaVersion int                `json:"schemaVersion"`
	Profiles      map[string]*Bundle `json:"profiles"`
	Objects       map[string]string  `json:"objects"`
}

func mapFiles(b *Bundle, fn func(File) (File, error)) error {
	sets := []map[string]File{b.Files, b.Initial}
	for _, i := range b.Installations {
		sets = append(sets, i.Initial)
	}
	for _, files := range sets {
		for ref, f := range files {
			v, err := fn(f)
			if err != nil {
				return err
			}
			files[ref] = v
		}
	}
	return nil
}
func pack(bundles map[string]*Bundle) ([]byte, error) {
	raw, err := json.Marshal(bundles)
	if err != nil {
		return nil, err
	}
	var copies map[string]*Bundle
	if err = json.Unmarshal(raw, &copies); err != nil {
		return nil, err
	}
	a := archive{SchemaVersion: 6, Profiles: copies, Objects: map[string]string{}}
	for _, b := range copies {
		err = mapFiles(b, func(f File) (File, error) {
			if previous, ok := a.Objects[f.Digest]; ok && previous != f.Data {
				return f, fmt.Errorf("内容摘要冲突")
			}
			a.Objects[f.Digest] = f.Data
			f.Data = ""
			return f, nil
		})
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(a)
}
func unpack(raw []byte) (map[string]*Bundle, error) {
	var a archive
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.SchemaVersion != 6 {
		return nil, fmt.Errorf("归档版本不支持")
	}
	for _, b := range a.Profiles {
		if b == nil {
			return nil, fmt.Errorf("空 Bundle")
		}
		err := mapFiles(b, func(f File) (File, error) {
			data, ok := a.Objects[f.Digest]
			if !ok || f.Data != "" {
				return f, fmt.Errorf("缺少内容对象")
			}
			f.Data = data
			return f, nil
		})
		if err != nil {
			return nil, err
		}
	}
	return a.Profiles, nil
}
