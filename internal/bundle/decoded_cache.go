package bundle

import (
	"encoding/json"
	"maps"
	"slices"
	"sync"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

// This cache owns only this executable's immutable embedded archive. There are
// four possible Profiles, so it cannot grow with user inputs or source paths.
// External/producer archives still use loadPacked and are always validated.
var embeddedBundles = decodedBundleCache{profiles: map[string]*Bundle{}}

type decodedBundleCache struct {
	mu       sync.Mutex
	archive  *packedArchive
	profiles map[string]*Bundle
}

func (c *decodedBundleCache) load(profile string, op *validationOp) (*Bundle, error) {
	c.mu.Lock()
	b, err := c.validated(profile, op)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return cloneBundle(b), nil
}

// Both the parsed archive and each logical Profile become visible only after
// successful validation. Failures are never memoized.
func (c *decodedBundleCache) validated(profile string, op *validationOp) (*Bundle, error) {
	if b := c.profiles[profile]; b != nil {
		return b, nil
	}
	a := c.archive
	if a == nil {
		raw, err := snapshots.load("packed-v1", packedData)
		if err != nil {
			return nil, err
		}
		a = &packedArchive{}
		if err := json.Unmarshal(raw, a); err != nil {
			return nil, domain.Wrap("BUNDLE", err)
		}
	}
	b, err := loadPackedProfileWith(a, profile, op)
	if err != nil {
		return nil, err
	}
	c.archive, c.profiles[profile] = a, b
	return b, nil
}

// Strings and File values are immutable; every mutable container is detached.
func cloneBundle(source *Bundle) *Bundle {
	b := *source
	b.Files, b.Initial = maps.Clone(source.Files), maps.Clone(source.Initial)
	b.NativeTransforms = slices.Clone(source.NativeTransforms)
	b.Manifest, b.Distribution = cloneJSONMap(source.Manifest), cloneJSONMap(source.Distribution)
	b.StageRequirements, b.SkillRequirements = cloneRequirements(source.StageRequirements), cloneRequirements(source.SkillRequirements)
	if source.Upgrade != nil {
		p := *source.Upgrade
		p.Rules, p.Assets = slices.Clone(p.Rules), slices.Clone(p.Assets)
		for i := range p.Assets {
			p.Assets[i].Paths = slices.Clone(p.Assets[i].Paths)
			p.Assets[i].Prefixes = slices.Clone(p.Assets[i].Prefixes)
		}
		b.Upgrade = &p
	}
	return &b
}

func cloneRequirements(source map[string]Requirement) map[string]Requirement {
	out := maps.Clone(source)
	for key, req := range out {
		req.Paths, req.Skills = slices.Clone(req.Paths), slices.Clone(req.Skills)
		out[key] = req
	}
	return out
}

func cloneJSONMap(source map[string]any) map[string]any {
	out := maps.Clone(source)
	for key, value := range out {
		out[key] = cloneJSONValue(value)
	}
	return out
}

func cloneJSONValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneJSONMap(v)
	case []any:
		out := slices.Clone(v)
		for i := range out {
			out[i] = cloneJSONValue(out[i])
		}
		return out
	default:
		return value
	}
}
