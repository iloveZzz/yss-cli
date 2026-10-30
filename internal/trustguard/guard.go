package trustguard

import (
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/nativeprofile"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"strings"
)

// IsPolicy identifies fixed governance policies, not user-authored work files.
func IsPolicy(ref string) bool {
	return ref == ".template-spec/process/harness-profile.yaml" || ref == ".template-spec/process/spec-workflow.json" || ref == ".template-spec/process/orchestration-contract.yaml" || ref == ".template-spec/process/lifecycle-registry.yaml" || ref == ".agents/skills/yss-implementation-contract-compiler/references/compiler-contract.yaml" || ref == ".template-spec/agents/yss-skill-registry.yaml" || strings.HasPrefix(ref, ".template-spec/process/orchestration/") || strings.HasPrefix(ref, ".template-spec/process/schemas/")
}

// Guard checks explicitly identified policy reads against a trusted Bundle.
type Guard struct {
	meta   *identitymeta.Metadata
	bundle *bundle.Bundle
}

func Open(meta *identitymeta.Metadata) (*Guard, error) {
	if meta == nil {
		return nil, domain.Fail("IDENTITY", "缺少实例来源")
	}
	b, err := bundle.LoadTrustedSource(bundle.SourceIdentity{Profile: meta.Profile, Version: meta.TemplateVersion, Commit: meta.TemplateCommit, Snapshot: meta.SnapshotHash, Manifest: meta.ManifestHash, Bundle: meta.BundleHash, State: meta.TemplateSourceState})
	if err != nil {
		return nil, err
	}
	return &Guard{meta: meta, bundle: b}, nil
}

// VerifyPolicy must be invoked for each policy actually consumed by an operation.
func (g *Guard) VerifyPolicy(ref string, actual domain.Descriptor) error {
	if err := safefs.ValidateRef(ref); err != nil {
		return err
	}
	m, ok := g.meta.Managed[ref]
	s := m.Source
	if !ok || s == nil || s.Kind != "bundle" || s.Path != ref || s.TemplateCommit != g.bundle.TemplateCommit || s.SnapshotHash != g.bundle.SnapshotHash || s.BundleHash != g.bundle.BundleHash {
		return domain.Fail("BASELINE", "政策缺少受信 Bundle 来源: "+ref)
	}
	candidates := []map[string]bundle.File{g.bundle.Initial, g.bundle.Files}
	for _, v := range g.bundle.Installations {
		candidates = append(candidates, v.Initial)
	}
	for _, files := range candidates {
		f, exists := files[ref]
		if exists && ref == ".template-spec/process/harness-profile.yaml" {
			profile, err := domain.GetProfile(g.meta.Profile)
			if err != nil {
				return err
			}
			f, err = nativeprofile.Generate(f, profile)
			if err != nil {
				return err
			}
		}
		if !exists || f.Digest != s.TemplateDigest {
			continue
		}
		raw, err := f.Render(g.meta.Variables)
		if err != nil {
			return err
		}
		expected := domain.Descriptor{Type: "file", Digest: safefs.Digest(raw), Mode: domain.FileMode(f.Mode)}
		if m.Baseline == expected && m.Applied == expected && actual == expected {
			return nil
		}
	}
	return domain.Fail("BASELINE", "政策与受信 Bundle 或实际字节不一致: "+ref)
}
