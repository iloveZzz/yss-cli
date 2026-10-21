package bundle

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

// FullSkillDistribution describes the shipped lock without turning a full Profile
// into a selected installation. Missing historical locks retain their old mode.
func FullSkillDistribution(files map[string]File) (map[string]any, error) {
	out := map[string]any{"mode": "profile-full"}
	file, present := files["skills-lock.json"]
	if !present {
		return out, nil
	}
	raw, err := file.Render(nil)
	if err != nil {
		return nil, err
	}
	var lock struct {
		Version         int      `json:"version"`
		CanonicalRoot   string   `json:"canonicalRoot"`
		ProjectionRoots []string `json:"projectionRoots"`
		Skills          struct {
			Shared   map[string]json.RawMessage            `json:"shared"`
			Platform map[string]map[string]json.RawMessage `json:"platform"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(raw, &lock); err != nil {
		return nil, err
	}
	if lock.Version != 3 || lock.CanonicalRoot != ".agents/skills" || lock.Skills.Shared == nil {
		return nil, fmt.Errorf("invalid full Profile Skill lock")
	}
	runtimes := []string{}
	roots := map[string]bool{}
	for _, root := range lock.ProjectionRoots {
		runtime, ok := domain.SkillRuntimeForRoot(root)
		if !ok || roots[root] {
			return nil, fmt.Errorf("invalid or duplicate full Profile projection: %s", root)
		}
		roots[root] = true
		runtimes = append(runtimes, runtime)
	}
	names := map[string]bool{}
	for name := range lock.Skills.Shared {
		if name == "." || path.Base(name) != name {
			return nil, fmt.Errorf("invalid Skill name: %s", name)
		}
		names[name] = true
		canonical := ".agents/skills/" + name
		if _, ok := files[canonical+"/SKILL.md"]; !ok {
			return nil, fmt.Errorf("full Profile canonical Skill missing: %s", name)
		}
		for root := range roots {
			projection := root + "/" + name
			for ref, entry := range files {
				if strings.HasPrefix(ref, canonical+"/") {
					mirror, ok := files[projection+strings.TrimPrefix(ref, canonical)]
					if !ok || mirror.Data != entry.Data || mirror.Mode != entry.Mode || mirror.Digest != entry.Digest {
						return nil, fmt.Errorf("full Profile projection disagrees: %s", projection)
					}
				}
				if strings.HasPrefix(ref, projection+"/") {
					if _, ok := files[canonical+strings.TrimPrefix(ref, projection)]; !ok {
						return nil, fmt.Errorf("unexpected projection resource: %s", ref)
					}
				}
			}
		}
	}
	for root, group := range lock.Skills.Platform {
		if !roots[root] {
			return nil, fmt.Errorf("platform Skill outside full Profile roots: %s", root)
		}
		for name := range group {
			if _, err := SkillTreeHash(files, root+"/"+name, nil); err != nil {
				return nil, err
			}
			names[name] = true
		}
	}
	installed := make([]string, 0, len(names))
	for name := range names {
		installed = append(installed, name)
	}
	sort.Strings(installed)
	out["runtimes"], out["installedSkills"] = runtimes, installed
	return out, nil
}
