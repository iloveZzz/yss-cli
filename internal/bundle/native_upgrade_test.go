package bundle

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestEmbeddedUpgradeConsumersAndTransformEvidence(t *testing.T) {
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		t.Run(profile, func(t *testing.T) {
			b, e := Load(profile)
			if e != nil {
				t.Fatal(e)
			}
			if b.SchemaVersion != 3 || b.Upgrade == nil {
				t.Fatal("upgrade bundle contract absent")
			}
			reader, exists := b.Files["scripts/lib/instance-metadata.mjs"]
			if !exists {
				t.Fatal("distributed identity consumer absent")
			}
			raw, _ := base64.StdEncoding.DecodeString(reader.Data)
			if !strings.Contains(string(raw), "[1,2,3].includes(native.schemaVersion)") || !strings.Contains(string(raw), "baselineSource") {
				t.Fatal("distributed reader rejects metadata v3")
			}
			for _, ref := range []string{".agents/skills/yss-harness-upgrade/SKILL.md", ".agents/skills/yss-harness-upgrade/references/project-operations.md", ".template-spec/process/harness-upgrade.md"} {
				if f, included := b.Files[ref]; included {
					raw, _ := base64.StdEncoding.DecodeString(f.Data)
					if !strings.Contains(string(raw), "resolution-file") {
						t.Fatal("distributed upgrade guidance stale: " + ref)
					}
				}
			}
			for _, tr := range b.NativeTransforms {
				if tr.Source.Digest == tr.OutputDigest || tr.OutputDigest != b.Files[tr.Path].Digest {
					t.Fatal("raw source/output evidence not distinct")
				}
			}
		})
	}
}

func TestSetupSourceRetainsHistoricalIdentityAndDoesNotInstallAbsentSkills(t *testing.T) {
	for _, name := range []string{"setup-yss-harness", "yss-harness-upgrade"} {
		t.Run(name, func(t *testing.T) {
			ref := ".agents/skills/" + name + "/SKILL.md"
			contract := ".template-spec/process/harness-upgrade.md"
			original := []byte("source snapshot " + contract)
			raw := map[string]sourceFile{ref: {data: original, mode: 0644}, contract: {data: []byte("source contract"), mode: 0644}}
			req, err := assetClosure(raw, Policy{}, nil, []string{name}, nil)
			if err != nil || len(req.Paths) != 1 || req.Paths[0] != contract || len(req.Skills) != 1 || req.Skills[0] != name {
				t.Fatalf("selected skill is not in closure: %+v, %v", req, err)
			}
			transforms, err := nativeUpgradeSource(raw)
			if err != nil || len(transforms) != 2 || transforms[0].Path != ref || transforms[1].Path != contract {
				t.Fatalf("unexpected source transforms: %+v, %v", transforms, err)
			}
			if len(raw) != 2 || !strings.Contains(string(raw[ref].data), "name: "+name) || transforms[0].Source.Digest != safefs.Digest(original) {
				t.Fatal("historical identity, selection or raw source evidence changed")
			}
		})
	}
	for _, root := range []string{".agents", ".codex", ".cursor", ".pi"} {
		ref := root + "/skills/setup-yss-harness/references/operation-contract.md"
		raw := map[string]sourceFile{ref: {data: []byte("source protocol"), mode: 0644}}
		transforms, err := nativeUpgradeSource(raw)
		if err != nil || len(transforms) != 1 || len(raw) != 1 || !strings.Contains(string(raw[ref].data), "整体 setup") {
			t.Fatalf("standalone contract missing or absent source installed: %s, %v", ref, err)
		}
	}
}
