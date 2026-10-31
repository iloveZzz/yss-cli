package bundle

const retiredTemplateCommit = "1e49127842c358caf552af8ebec58f438a35c5a6"

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
	return loadTrustedArchive(trustedSources, packedData, source)
}

func supportedProduct(b *Bundle) bool {
	return b != nil && b.SourceState == "committed" && b.TemplateCommit != retiredTemplateCommit && trustedVersionPattern.MatchString(b.TemplateVersion) && (VersionRange{Min: "5.0.0"}).Contains(b.TemplateVersion)
}

// MatchesSource only compares identities; the caller must independently trust and validate b.
func MatchesSource(b *Bundle, source SourceIdentity) bool {
	return b != nil && b.Profile == source.Profile && b.TemplateVersion == source.Version &&
		b.TemplateCommit == source.Commit && b.SnapshotHash == source.Snapshot &&
		b.ManifestHash == source.Manifest && b.BundleHash == source.Bundle && b.SourceState == source.State
}
