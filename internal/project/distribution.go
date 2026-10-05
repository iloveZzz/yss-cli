package project

import (
	"encoding/base64"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/bundle"
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
func selectedLock(b *bundle.Bundle, d map[string]any) (bundle.File, error) {
	f := b.Files["skills-lock.json"]
	if text(d["mode"]) != "selected" {
		return f, nil
	}
	raw, e := base64.StdEncoding.DecodeString(f.Data)
	if e != nil {
		return f, e
	}
	var lock map[string]any
	if e = json.Unmarshal(raw, &lock); e != nil {
		return f, e
	}
	installed := map[string]bool{}
	for _, s := range stringsOf(d["installedSkills"]) {
		installed[s] = true
	}
	runtimeRoots := map[string]string{"codex": ".codex/skills", "cursor": ".cursor/skills", "pi": ".pi/skills"}
	roots := []string{}
	for _, runtime := range stringsOf(d["runtimes"]) {
		if r, ok := runtimeRoots[runtime]; ok {
			roots = append(roots, r)
		}
	}
	lock["projectionRoots"] = roots
	skills, _ := object(lock["skills"])
	shared, _ := object(skills["shared"])
	filtered := map[string]any{}
	for name, item := range shared {
		if installed[name] {
			m, _ := object(item)
			m["targets"] = append([]string{".agents/skills"}, roots...)
			filtered[name] = m
		}
	}
	skills["shared"] = filtered
	platform, _ := object(skills["platform"])
	targetPlatform := map[string]any{}
	for _, root := range roots {
		entries, _ := object(platform[root])
		p := map[string]any{}
		for name, v := range entries {
			if installed[name] {
				p[name] = v
			}
		}
		if len(p) > 0 {
			targetPlatform[root] = p
		}
	}
	skills["platform"] = targetPlatform
	raw, e = jsonBytes(lock)
	if e != nil {
		return f, e
	}
	f.Data = base64.StdEncoding.EncodeToString(raw)
	f.Digest = safefs.Digest(raw)
	return f, nil
}
