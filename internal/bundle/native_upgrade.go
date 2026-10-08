package bundle

import (
	"embed"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

// These compatibility-generator inputs are reviewed CLI source. The producer
// keeps the named template Git snapshot intact and records both original bytes
// and generated output in the Bundle. It never reads dirty template files.
//
//go:embed native/*
var nativeUpgradeAssets embed.FS

type NativeTransform struct {
	Path         string `json:"path"`
	Generator    string `json:"generator"`
	Source       File   `json:"source"`
	OutputDigest string `json:"outputDigest"`
	SourceAbsent bool   `json:"sourceAbsent,omitempty"`
}

func nativeUpgradeSource(raw map[string]sourceFile) ([]NativeTransform, error) {
	inputs := map[string]string{
		"scripts/lib/instance-metadata.mjs":                                   "instance-metadata.mjs",
		".agents/skills/yss-harness-upgrade/SKILL.md":                         "upgrade-SKILL.md",
		".agents/skills/yss-harness-upgrade/references/project-operations.md": "project-operations.md",
		".template-spec/process/harness-upgrade.md":                           "harness-upgrade.md",
	}
	for _, root := range []string{".codex", ".cursor", ".pi"} {
		inputs[root+"/skills/yss-harness-upgrade/SKILL.md"] = "upgrade-SKILL.md"
		inputs[root+"/skills/yss-harness-upgrade/references/project-operations.md"] = "project-operations.md"
	}
	// The old paths above are fixed historical inputs, not discovery aliases.
	for _, root := range []string{".agents", ".codex", ".cursor", ".pi"} {
		inputs[root+"/skills/setup-yss-harness/SKILL.md"] = "setup-SKILL.md"
		inputs[root+"/skills/setup-yss-harness/references/project-operations.md"] = "setup-project-operations.md"
		inputs[root+"/skills/setup-yss-harness/references/operation-contract.md"] = "setup-operation-contract.md"
	}
	if _, exists := raw[".agents/skills/setup-yss-harness/SKILL.md"]; exists {
		inputs[".template-spec/process/harness-upgrade.md"] = "setup-contract.md"
	}
	transforms := []NativeTransform{}
	for _, ref := range sortedKeys(inputs) {
		original, exists := raw[ref]
		if !exists {
			continue // a producer cannot install an unselected or absent source asset
		}
		generated, e := nativeUpgradeAssets.ReadFile("native/" + inputs[ref])
		if e != nil {
			return nil, e
		}
		if safefs.Digest(original.data) == safefs.Digest(generated) {
			continue
		}
		transforms = append(transforms, NativeTransform{Path: ref, Generator: "native-upgrade-v3", Source: encodedFile(original.data, original.mode, "managed")})
		raw[ref] = sourceFile{data: generated, mode: original.mode}
	}
	return transforms, nil
}

func validateNativeTransforms(b *Bundle) error {
	var paths safefs.PathSet
	for _, t := range b.NativeTransforms {
		if e := paths.Add(t.Path); e != nil {
			return e
		}
		output, exists := b.Files[t.Path]
		if (t.Generator != "native-upgrade-v3" && t.Generator != "native-work-layout-v1") || !exists || t.OutputDigest != output.Digest || strings.HasPrefix(t.Path, ".yss/") {
			return domain.Fail("BUNDLE", "原生兼容生成记录无效")
		}
		if t.SourceAbsent && (t.Generator != "native-work-layout-v1" || t.Path != "scripts/lib/work-layout.mjs" && t.Path != ".template-spec/process/work-layout.md" || t.Source.Digest != safefs.Digest(nil)) {
			return domain.Fail("BUNDLE", "新增兼容资产来源无效")
		}
		if _, e := t.Source.Render(nil); e != nil {
			return e
		}
	}
	return nil
}
