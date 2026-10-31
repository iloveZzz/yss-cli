//go:build !yss_nobundle

package trustguard

import (
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestPolicyMustMatchTrustedBundleAndObservedBytes(t *testing.T) {
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	const ref = ".template-spec/process/spec-workflow.json"
	f, ok := b.Files[ref]
	if !ok {
		t.Fatal("baseline missing workflow")
	}
	raw, err := f.Render(nil)
	if err != nil {
		t.Fatal(err)
	}
	expected := domain.Descriptor{Type: "file", Digest: safefs.Digest(raw), Mode: domain.FileMode(f.Mode)}
	m := identitymeta.Managed{Baseline: expected, Applied: expected, Ownership: f.Ownership, Source: &identitymeta.BaselineSource{Kind: "bundle", TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, BundleHash: b.BundleHash, Path: ref, Variant: "initial", TemplateDigest: f.Digest}}
	meta := &identitymeta.Metadata{Profile: b.Profile, TemplateVersion: b.TemplateVersion, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash, BundleHash: b.BundleHash, TemplateSourceState: b.SourceState, Managed: map[string]identitymeta.Managed{ref: m}}
	g, err := Open(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.VerifyPolicy(ref, expected); err != nil {
		t.Fatal(err)
	}
	bad := expected
	bad.Digest = safefs.Digest([]byte("tampered"))
	if err := g.VerifyPolicy(ref, bad); err == nil {
		t.Fatal("tampered policy accepted")
	}
	if err := g.VerifyPolicy(".template-spec/process/not-registered.yaml", expected); err == nil {
		t.Fatal("unregistered policy accepted")
	}
	m.Source.TemplateDigest = bad.Digest
	meta.Managed[ref] = m
	g, err = Open(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.VerifyPolicy(ref, expected); err == nil {
		t.Fatal("forged metadata source accepted")
	}
}
