package bundle

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

type UpgradePolicy struct {
	SchemaVersion int             `json:"schemaVersion"`
	Assets        []AssetPolicy   `json:"assets"`
	Rules         []MigrationRule `json:"rules"`
}
type AssetPolicy struct {
	Paths     []string `json:"paths,omitempty"`
	Prefixes  []string `json:"prefixes,omitempty"`
	Kind      string   `json:"kind"`
	Generator string   `json:"generator,omitempty"`
}
type SourceMatch struct {
	Profile        string `json:"profile"`
	TemplateCommit string `json:"templateCommit"`
	SnapshotHash   string `json:"sourceSnapshotHash"`
	BundleHash     string `json:"bundleHash,omitempty"`
}
type MigrationRule struct {
	ID         string      `json:"id"`
	From       SourceMatch `json:"from"`
	Action     string      `json:"action"`
	SourcePath string      `json:"sourcePath"`
	TargetPath string      `json:"targetPath,omitempty"`
}

// Legacy bundles have no retirement authority. Policy defaults only classify
// assets already present in the fixed source; they never invent migration rules.
func DefaultUpgradePolicy() *UpgradePolicy {
	return &UpgradePolicy{SchemaVersion: 1, Rules: []MigrationRule{}, Assets: []AssetPolicy{
		{Prefixes: []string{".work", "docs/.scratch"}, Kind: "preserve"},
		{Paths: []string{"CONTEXT.md", "yss-project.yaml", ".template-spec/agents/issue-tracker.md", "README.md"}, Kind: "preserve"},
		{Paths: []string{"AGENTS.md", ".gitignore", "DESIGN.md"}, Kind: "customizable"},
		{Paths: []string{"skills-lock.json", ".template-spec/process/harness-profile.yaml", ".agents/skills/.public-skills.json", ".agents/skills/.skills-manifest.json"}, Kind: "generated", Generator: "native-bundle"},
	}}
}
func (b *Bundle) AssetKind(ref string, f File) string {
	policy := b.Upgrade
	if policy == nil {
		policy = DefaultUpgradePolicy()
	}
	for _, entry := range policy.Assets {
		for _, p := range entry.Paths {
			if ref == p {
				return entry.Kind
			}
		}
		for _, p := range entry.Prefixes {
			if strings.HasPrefix(ref, strings.TrimSuffix(p, "/")+"/") {
				return entry.Kind
			}
		}
	}
	switch f.Ownership {
	case "user-owned", "protected":
		return "preserve"
	case "managed-customizable":
		return "customizable"
	case "generated":
		return "preserve" // unregistered generators cannot overwrite project settings
	default:
		return "fixed"
	}
}
func protectedUpgradePath(ref string) bool {
	for _, profile := range domain.Profiles {
		if strings.EqualFold(ref, profile.Metadata) {
			return true
		}
	}
	return ref == ".yss.json" || strings.EqualFold(strings.Split(ref, "/")[0], ".yss") || strings.EqualFold(strings.Split(ref, "/")[0], ".git") || ref == "CONTEXT.md" || ref == "yss-project.yaml" || strings.HasPrefix(ref, ".yss-") || strings.HasPrefix(ref, "docs/plan/") || strings.HasPrefix(ref, "docs/spec/")
}
func (p *UpgradePolicy) Validate(b *Bundle) error {
	if p.SchemaVersion != 1 {
		return domain.Fail("BUNDLE_RULE", "未知升级政策版本")
	}
	var paths safefs.PathSet
	seen := map[string]bool{}
	for _, a := range p.Assets {
		if a.Kind != "preserve" && a.Kind != "customizable" && a.Kind != "fixed" && a.Kind != "generated" {
			return domain.Fail("BUNDLE_RULE", "未知资产政策")
		}
		if a.Kind == "generated" && a.Generator != "native-bundle" {
			return domain.Fail("BUNDLE_RULE", "未登记生成器")
		}
		for _, ref := range append(append([]string{}, a.Paths...), a.Prefixes...) {
			if e := paths.Add(ref); e != nil {
				return e
			}
		}
	}
	for index, r := range p.Rules {
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9._-]+$`).MatchString(r.ID) || seen[r.ID] || r.From.Profile != b.Profile || !fullCommit.MatchString(r.From.TemplateCommit) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(r.From.SnapshotHash) || r.From.BundleHash != "" && !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(r.From.BundleHash) {
			return domain.Fail("BUNDLE_RULE", "迁移规则来源或 ID 不合法")
		}
		seen[r.ID] = true
		for _, previous := range p.Rules[:index] {
			if previous.From.Profile != r.From.Profile || previous.From.TemplateCommit != r.From.TemplateCommit || previous.From.SnapshotHash != r.From.SnapshotHash || previous.From.BundleHash != "" && r.From.BundleHash != "" && previous.From.BundleHash != r.From.BundleHash {
				continue
			}
			if strings.EqualFold(previous.SourcePath, r.SourcePath) || previous.TargetPath != "" && r.TargetPath != "" && strings.EqualFold(previous.TargetPath, r.TargetPath) {
				return domain.Fail("BUNDLE_RULE", "适用来源重叠的迁移规则具有重复源或目标")
			}
		}
		if e := safefs.ValidateRef(r.SourcePath); e != nil {
			return e
		}
		if protectedUpgradePath(r.SourcePath) {
			return domain.Fail("BUNDLE_RULE", "迁移规则触及受保护资产")
		}
		if _, exists := b.Files[r.SourcePath]; exists {
			return domain.Fail("BUNDLE_RULE", "退役源仍属于目标 Bundle")
		}
		if r.Action == "rename" {
			if e := safefs.ValidateRef(r.TargetPath); e != nil {
				return e
			}
			f, exists := b.Files[r.TargetPath]
			if !exists || protectedUpgradePath(r.TargetPath) || b.AssetKind(r.TargetPath, f) == "preserve" || strings.EqualFold(r.SourcePath, r.TargetPath) {
				return domain.Fail("BUNDLE_RULE", "改名目标无效或存在路径别名")
			}
		} else if r.Action != "delete" || r.TargetPath != "" {
			return domain.Fail("BUNDLE_RULE", "未知迁移动作")
		}
	}
	return nil
}

const SnapshotFile = ".yss-bundle.snapshot.json"

func Hash(b *Bundle) string { return contentHash(b) }

// ReadSnapshot consumes complete, explicitly supplied offline material, including
// initial variants. An export from an older producer without this material is
// diagnosed as unavailable instead of synthesizing an initial variant.
func ReadSnapshot(file, profile string) (*Bundle, string, string, error) {
	info, e := os.Lstat(file)
	if e != nil {
		return nil, "", "", e
	}
	if info.IsDir() {
		file = filepath.Join(file, SnapshotFile)
	}
	abs, e := filepath.Abs(file)
	if e != nil {
		return nil, "", "", e
	}
	canonical, e := filepath.EvalSymlinks(abs)
	if e != nil {
		return nil, "", "", domain.Fail("BASE_BUNDLE", "旧 Bundle 缺少完整初始/完整变体材料")
	}
	if canonical != abs {
		return nil, "", "", domain.Fail("BASE_BUNDLE", "旧 Bundle 不接受链接路径")
	}
	info, e = os.Lstat(abs)
	if e != nil || !info.Mode().IsRegular() || info.Size() > 128*1024*1024 {
		return nil, "", "", domain.Fail("BASE_BUNDLE", "旧 Bundle 必须是最多 128 MiB 的普通 JSON 文件")
	}
	raw, e := os.ReadFile(abs)
	if e != nil {
		return nil, "", "", e
	}
	if _, e = schema.Parse(raw); e != nil {
		return nil, "", "", e
	}
	var b Bundle
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e = d.Decode(&b); e != nil {
		return nil, "", "", domain.Wrap("BASE_BUNDLE", e)
	}
	if e = validate(&b, profile); e != nil {
		return nil, "", "", e
	}
	if b.SchemaVersion >= 2 && b.BundleHash != contentHash(&b) {
		return nil, "", "", domain.Fail("BASE_BUNDLE", "旧 Bundle 摘要不匹配")
	}
	return &b, abs, safefs.Digest(raw), nil
}
