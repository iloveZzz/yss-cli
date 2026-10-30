package bundle

import (
	"sync"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

// LoadBaseline reads the separately fixed, independently validated 5.0 archive.
var baselineOnce sync.Once
var baselineRaw []byte
var baselineErr error

func LoadBaseline(profile string) (*Bundle, error) {
	baselineOnce.Do(func() { baselineRaw, baselineErr = reconstructBaseline(packedData, baselineDelta, baselineRawDigest) })
	raw, err := baselineRaw, baselineErr
	if err != nil {
		return nil, err
	}
	return loadPacked(raw, profile)
}

// SourceIdentity is the installed source tuple to compare with a validated Bundle.
type SourceIdentity struct {
	Profile, Version, Commit, Snapshot, Manifest, Bundle, State string
}

// LoadTrustedSource selects a fixed Bundle; callers must still verify installed assets.
func LoadTrustedSource(source SourceIdentity) (*Bundle, error) {
	current, err := Load(source.Profile)
	if err != nil {
		return nil, err
	}
	if supportedProduct(current) && MatchesSource(current, source) {
		return current, nil
	}
	baseline, err := LoadBaseline(source.Profile)
	if err != nil {
		return nil, err
	}
	return selectTrustedSource(current, baseline, source)
}

func selectTrustedSource(current, baseline *Bundle, source SourceIdentity) (*Bundle, error) {
	for _, b := range []*Bundle{current, baseline} {
		if supportedProduct(b) && MatchesSource(b, source) {
			return b, nil
		}
	}
	return nil, domain.Fail("BASELINE", "实例来源与当前 Bundle 或受信 5.0 基线不匹配")
}

func supportedProduct(b *Bundle) bool {
	return b != nil && b.SourceState == "committed" && (VersionRange{Min: "5.0.0"}).Contains(b.TemplateVersion)
}

// MatchesSource only compares identities; the caller must independently trust and validate b.
func MatchesSource(b *Bundle, source SourceIdentity) bool {
	return b != nil && b.Profile == source.Profile && b.TemplateVersion == source.Version &&
		b.TemplateCommit == source.Commit && b.SnapshotHash == source.Snapshot &&
		b.ManifestHash == source.Manifest && b.BundleHash == source.Bundle && b.SourceState == source.State
}
