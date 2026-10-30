//go:build !yss_nobundle

package bundle

import "testing"

func TestLoadBaselineHasReleasedTemplateSources(t *testing.T) {
	for _, profile := range []string{"spec", "backend"} {
		b, err := LoadBaseline(profile)
		if err != nil {
			t.Fatal(err)
		}
		if b.Profile != profile || b.TemplateVersion != "5.0.0" || b.TemplateCommit != "1e49127842c358caf552af8ebec58f438a35c5a6" || b.SourceState != "committed" {
			t.Fatalf("意外的基线来源: %+v", b)
		}
	}
}

func TestTrustedSourceAllowsPriorBaselineForSync(t *testing.T) {
	baseline, err := LoadBaseline("spec")
	if err != nil {
		t.Fatal(err)
	}
	current := cloneBundle(baseline)
	current.TemplateVersion = "5.1.0"
	current.TemplateCommit = "different"
	source := SourceIdentity{Profile: baseline.Profile, Version: baseline.TemplateVersion, Commit: baseline.TemplateCommit, Snapshot: baseline.SnapshotHash, Manifest: baseline.ManifestHash, Bundle: baseline.BundleHash, State: baseline.SourceState}
	selected, err := selectTrustedSource(current, baseline, source)
	if err != nil || selected != baseline {
		t.Fatalf("当前 Bundle 更新后仍应接受受信 5.0 基线: %v", err)
	}
	source.Bundle = "untrusted"
	if _, err := selectTrustedSource(current, baseline, source); err == nil {
		t.Fatal("自报未知 Bundle 不得取得受信来源")
	}
	old := cloneBundle(baseline)
	old.TemplateVersion = "4.9.0"
	oldSource := SourceIdentity{Profile: old.Profile, Version: old.TemplateVersion, Commit: old.TemplateCommit, Snapshot: old.SnapshotHash, Manifest: old.ManifestHash, Bundle: old.BundleHash, State: old.SourceState}
	if _, err := selectTrustedSource(old, baseline, oldSource); err == nil {
		t.Fatal("即使当前归档声明旧模板，也不得将其当作受信产品")
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
				"version":  func(v *SourceIdentity) { v.Version = "5.0.1" },
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
