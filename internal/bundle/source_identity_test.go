//go:build !yss_nobundle

package bundle

import "testing"

func TestEmbeddedBundleHasSupportedTemplateSource(t *testing.T) {
	for _, profile := range []string{"spec", "backend"} {
		b, err := Load(profile)
		if err != nil {
			t.Fatal(err)
		}
		if b.Profile != profile || !supportedProduct(b) {
			t.Fatalf("不受支持的当前来源: %+v", b)
		}
	}
}

func TestRetiredSameVersionTemplateSourceIsRejected(t *testing.T) {
	old := &Bundle{Profile: "spec", TemplateVersion: "5.0.0", TemplateCommit: retiredTemplateCommit, SourceState: "committed"}
	if supportedProduct(old) {
		t.Fatal("重发前同名模板提交不得取得受信身份")
	}
}

func TestSourceIdentityRequiresExactBundleSource(t *testing.T) {
	for _, profile := range []string{"spec", "backend"} {
		t.Run(profile, func(t *testing.T) {
			b, err := Load(profile)
			if err != nil {
				t.Fatal(err)
			}
			source := SourceIdentity{Profile: b.Profile, Version: b.TemplateVersion, Commit: b.TemplateCommit, Snapshot: b.SnapshotHash, Manifest: b.ManifestHash, Bundle: b.BundleHash, State: b.SourceState}
			if !MatchesSource(b, source) {
				t.Fatal("实际 Bundle 来源应匹配")
			}
			for name, mutate := range map[string]func(*SourceIdentity){
				"profile":  func(v *SourceIdentity) { v.Profile = "other" },
				"version":  func(v *SourceIdentity) { v.Version += "-other" },
				"commit":   func(v *SourceIdentity) { v.Commit = "other" },
				"snapshot": func(v *SourceIdentity) { v.Snapshot = "other" },
				"manifest": func(v *SourceIdentity) { v.Manifest = "other" },
				"bundle":   func(v *SourceIdentity) { v.Bundle = "other" },
				"state":    func(v *SourceIdentity) { v.State = "unknown" },
			} {
				changed := source
				mutate(&changed)
				if MatchesSource(b, changed) {
					t.Fatalf("来源字段 %s 不匹配时不得通过", name)
				}
			}
		})
	}
	if MatchesSource(nil, SourceIdentity{}) {
		t.Fatal("缺少 Bundle 不得匹配")
	}
}
