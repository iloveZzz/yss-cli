//go:build !yss_nobundle

package kernel

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestTrustedPolicyReadRejectsAbsoluteAndAliasBypass(t *testing.T) {
	b, err := bundle.LoadBaseline("backend")
	if err != nil {
		t.Fatal(err)
	}
	const ref = ".template-spec/process/orchestration-contract.yaml"
	f, ok := b.Files[ref]
	if !ok {
		t.Fatal("基线缺少编排政策")
	}
	raw, err := f.Render(nil)
	if err != nil {
		t.Fatal(err)
	}
	expected := domain.Descriptor{Type: "file", Digest: safefs.Digest(raw), Mode: domain.FileMode(f.Mode)}
	meta := &identitymeta.Metadata{Profile: b.Profile, TemplateVersion: b.TemplateVersion, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash, BundleHash: b.BundleHash, TemplateSourceState: b.SourceState, Managed: map[string]identitymeta.Managed{ref: {Baseline: expected, Applied: expected, Source: &identitymeta.BaselineSource{Kind: "bundle", TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, BundleHash: b.BundleHash, Path: ref, Variant: "initial", TemplateDigest: f.Digest}}}}
	root := t.TempDir()
	file := filepath.Join(root, ref)
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0644); err != nil {
		t.Fatal(err)
	}
	s := NewSemanticSession(context.Background(), root, nil)
	if err := s.BindTrustedSource(meta); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Bytes(file); err != nil {
		t.Fatalf("受信绝对路径应通过: %v", err)
	}
	if err := os.WriteFile(file, append(raw, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	for _, alias := range []bool{false, true} {
		s = NewSemanticSession(context.Background(), root, nil)
		if err := s.BindTrustedSource(meta); err != nil {
			t.Fatal(err)
		}
		input := file
		if alias {
			input = "alias.yaml"
			s.Aliases[input] = ref
		}
		if _, err := s.Bytes(input); err == nil {
			t.Fatalf("绝对路径或别名绕过了政策校验: %s", input)
		}
	}
}

func TestReadingPoliciesRequireTrustedBundleBytes(t *testing.T) {
	b, err := bundle.LoadBaseline("backend")
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{".agents/skills/yss-implementation-contract-compiler/references/compiler-contract.yaml"} {
		t.Run(ref, func(t *testing.T) {
			f, ok := b.Files[ref]
			if !ok {
				t.Fatalf("官方 Bundle 缺少政策: %s", ref)
			}
			raw, err := f.Render(nil)
			if err != nil {
				t.Fatal(err)
			}
			d := domain.Descriptor{Type: "file", Digest: safefs.Digest(raw), Mode: domain.FileMode(f.Mode)}
			meta := &identitymeta.Metadata{Profile: b.Profile, TemplateVersion: b.TemplateVersion, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash, BundleHash: b.BundleHash, TemplateSourceState: b.SourceState, Managed: map[string]identitymeta.Managed{ref: {Baseline: d, Applied: d, Source: &identitymeta.BaselineSource{Kind: "bundle", TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, BundleHash: b.BundleHash, Path: ref, Variant: "initial", TemplateDigest: f.Digest}}}}
			root := t.TempDir()
			file := filepath.Join(root, ref)
			if err = os.MkdirAll(filepath.Dir(file), 0755); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(file, raw, os.FileMode(f.Mode)); err != nil {
				t.Fatal(err)
			}
			s := NewSemanticSession(context.Background(), root, nil)
			if err = s.BindTrustedSource(meta); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Bytes(ref); err != nil {
				t.Fatalf("受信政策应可读取: %v", err)
			}
			if err = os.WriteFile(file, append(raw, '\n'), os.FileMode(f.Mode)); err != nil {
				t.Fatal(err)
			}
			s = NewSemanticSession(context.Background(), root, nil)
			if err = s.BindTrustedSource(meta); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Bytes(ref); err == nil {
				t.Fatal("篡改决策政策被读取")
			}
		})
	}
}
