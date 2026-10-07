package bundle

import (
	"encoding/base64"
	"strings"
	"testing"
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
