package project

import (
	"encoding/base64"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"sort"
	"strings"
)

func stringsOf(v any) []string {
	out := []string{}
	switch list := v.(type) {
	case []string:
		return append(out, list...)
	case []any:
		for _, v := range list {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
func union(values ...[]string) []string {
	m := map[string]bool{}
	for _, list := range values {
		for _, s := range list {
			m[s] = true
		}
	}
	out := []string{}
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
func distributionFor(id *Identity, b *bundle.Bundle, selection []string) (map[string]any, error) {
	source := b.Distribution
	if id.Native != nil {
		source = id.Native.Distribution
	} else if id.Legacy != nil {
		if d, ok := object(id.Legacy["distribution"]); ok {
			source = d
		}
	}
	data, _ := json.Marshal(source)
	var d map[string]any
	_ = json.Unmarshal(data, &d)
	if id.Profile.Name != "spec" || text(d["mode"]) != "selected" {
		return d, nil
	}
	added := []string{}
	set := map[string]bool{}
	for _, ref := range selection {
		set[ref] = true
		if strings.HasPrefix(ref, ".agents/skills/") {
			parts := strings.Split(ref, "/")
			if len(parts) > 3 {
				added = append(added, parts[2])
			}
		}
	}
	platformSkills, err := selectedPlatformSkills(id, b, d, set)
	if err != nil {
		return nil, err
	}
	added = append(added, platformSkills...)
	d["installedSkills"] = union(stringsOf(d["installedSkills"]), added)
	if len(selection) > 0 {
		stages := stringsOf(d["installedStages"])
		for stage, r := range b.StageRequirements {
			matches := true
			for _, ref := range r.Paths {
				if !set[ref] {
					matches = false
					break
				}
			}
			if matches {
				stages = append(stages, stage)
			}
		}
		d["installedStages"] = union(stages)
	}
	return d, nil
}

func selectedPlatformSkills(id *Identity, b *bundle.Bundle, d map[string]any, selected map[string]bool) ([]string, error) {
	roots := map[string]string{"codex": ".codex/skills", "cursor": ".cursor/skills", "pi": ".pi/skills"}
	projectionSelection := false
	for _, runtime := range stringsOf(d["runtimes"]) {
		root, known := roots[runtime]
		if !known {
			return nil, domain.Fail("BUNDLE", "平台 Skill 运行时来源非法")
		}
		for ref := range selected {
			projectionSelection = projectionSelection || strings.HasPrefix(ref, root+"/")
		}
	}
	if !projectionSelection {
		return nil, nil
	}
	f, exists := b.Files["skills-lock.json"]
	if !exists {
		return nil, domain.Fail("BUNDLE", "平台 Skill 分发缺少固定源锁")
	}
	raw, err := base64.StdEncoding.DecodeString(f.Data)
	if err != nil {
		return nil, err
	}
	var lock struct {
		Skills struct {
			Platform map[string]map[string]json.RawMessage `json:"platform"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(raw, &lock); err != nil {
		return nil, err
	}
	added := []string{}
	for _, runtime := range stringsOf(d["runtimes"]) {
		root, known := roots[runtime]
		if !known {
			return nil, domain.Fail("BUNDLE", "平台 Skill 运行时来源非法")
		}
		for name := range lock.Skills.Platform[root] {
			prefix := root + "/" + name + "/"
			if err := safefs.ValidateRef(strings.TrimSuffix(prefix, "/")); err != nil || strings.Contains(name, "/") {
				return nil, domain.Fail("BUNDLE", "平台 Skill 来源名称非法")
			}
			hit := false
			for ref := range selected {
				if strings.HasPrefix(ref, prefix) {
					if _, exists := b.Files[ref]; !exists {
						return nil, domain.Fail("ASSET", "平台 Skill 选择不在固定 Bundle 中: "+ref)
					}
					hit = true
				}
			}
			if !hit {
				continue
			}
			for ref := range b.Files {
				if strings.HasPrefix(ref, prefix) && !selected[ref] {
					if id.Native == nil {
						return nil, domain.Fail("ASSET", "平台 Skill 选择缺少完整固定目录: "+ref)
					}
					if _, managed := id.Native.Managed[ref]; !managed {
						return nil, domain.Fail("ASSET", "平台 Skill 选择缺少受管资产: "+ref)
					}
				}
			}
			added = append(added, name)
		}
	}
	return added, nil
}
func selectedLock(b *bundle.Bundle, d map[string]any) (bundle.File, error) {
	f := b.Files["skills-lock.json"]
	if text(d["mode"]) != "selected" {
		return f, nil
	}
	raw, e := base64.StdEncoding.DecodeString(f.Data)
	if e != nil {
		return f, e
	}
	raw, e = bundle.SelectedSourceLock(raw, stringsOf(d["installedSkills"]), stringsOf(d["runtimes"]))
	if e != nil {
		return f, e
	}
	f.Data = base64.StdEncoding.EncodeToString(raw)
	f.Digest = safefs.Digest(raw)
	return f, nil
}
