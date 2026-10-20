package bundle

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"strings"
)

type File struct {
	Data      string `json:"data"`
	Digest    string `json:"digest"`
	Mode      uint32 `json:"mode"`
	Ownership string `json:"ownership"`
}
type Bundle struct {
	NativeTransforms  []NativeTransform      `json:"nativeTransforms,omitempty"`
	Upgrade           *UpgradePolicy         `json:"upgradePolicy,omitempty"`
	Legacy            LegacyLineage          `json:"legacy,omitempty"`
	SchemaVersion     int                    `json:"schemaVersion"`
	TemplateVersion   string                 `json:"templateVersion,omitempty"`
	LegacyCLICommit   string                 `json:"legacyCLICommit,omitempty"`
	BundleHash        string                 `json:"bundleHash,omitempty"`
	Producer          Provenance             `json:"producer,omitempty"`
	SourcePolicy      PolicyProvenance       `json:"sourcePolicy,omitempty"`
	Profile           string                 `json:"profile"`
	LegacyVersion     string                 `json:"legacyVersion"`
	CLICommit         string                 `json:"cliCommit,omitempty"`
	TemplateCommit    string                 `json:"templateCommit"`
	SourceState       string                 `json:"sourceState"`
	SnapshotHash      string                 `json:"sourceSnapshotHash"`
	ManifestHash      string                 `json:"manifestHash"`
	Distribution      map[string]any         `json:"distribution"`
	Manifest          map[string]any         `json:"manifest"`
	Initial           map[string]File        `json:"initial"`
	Files             map[string]File        `json:"files"`
	StageRequirements map[string]Requirement `json:"stageRequirements"`
	SkillRequirements map[string]Requirement `json:"skillRequirements"`
}

func Load(profile string) (*Bundle, error) {
	b, _, err := loadForOperation(profile)
	return b, err
}

func loadForOperation(profile string) (*Bundle, *validationOp, error) {
	if _, err := domain.GetProfile(profile); err != nil {
		return nil, nil, err
	}
	op := newValidationOp()
	b, err := embeddedBundles.load(profile, op)
	return b, op, err
}
func (f File) Render(vars map[string]string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(f.Data)
	if err != nil {
		return nil, err
	}
	return renderDecoded(b, vars), nil
}

func renderDecoded(b []byte, vars map[string]string) []byte {
	if bytes.IndexByte(b, 0) >= 0 {
		return b
	}
	text := string(b)
	for k, token := range map[string]string{"projectName": "__YSS_PROJECT_NAME__", "businessDomain": "__YSS_BUSINESS_DOMAIN__", "teamSize": "__YSS_TEAM_SIZE__"} {
		text = strings.ReplaceAll(text, token, vars[k])
	}
	return []byte(text)
}

func validate(out *Bundle, profile string) error {
	return validateWith(out, profile, newValidationOp())
}

func validateWith(out *Bundle, profile string, op *validationOp) error {
	if (out.SchemaVersion < 1 || out.SchemaVersion > 4) || out.Profile != profile || (out.SourceState != "committed" && !(out.SourceState == "working-tree" && out.Producer.SourceState == "working-tree" && out.SourcePolicy.Kind == "working-tree" && strings.HasPrefix(out.TemplateVersion, "worktree:") && fullCommit.MatchString(strings.TrimPrefix(out.TemplateVersion, "worktree:")))) {
		return domain.Fail("BUNDLE", "快照身份或来源不合法")
	}
	if out.SchemaVersion == 4 && profile != "spec" && profile != "backend" {
		return domain.Fail("BUNDLE", "Bundle4 仅支持 Spec/Backend")
	}
	if out.Initial == nil {
		out.Initial = out.Files
	}
	var paths safefs.PathSet
	for _, files := range []map[string]File{out.Files, out.Initial} {
		for ref, f := range files {
			if err := paths.Add(ref); err != nil {
				return err
			}
			_, err := op.content(f)
			if err != nil {
				return domain.Fail("BUNDLE", "快照摘要不一致: "+ref)
			}
			if !stringSet([]string{"managed", "managed-customizable", "generated", "user-owned", "protected"})[f.Ownership] {
				return domain.Fail("BUNDLE", "快照ownership不合法: "+ref)
			}
			if f.Mode != 0644 && f.Mode != 0755 {
				return domain.Fail("BUNDLE", "快照权限不合法: "+ref)
			}
		}
	}
	for ref := range out.Initial {
		if _, ok := out.Files[ref]; !ok {
			return domain.Fail("BUNDLE", "初始化不属于完整快照: "+ref)
		}
	}
	if out.SchemaVersion == 4 {
		for _, files := range []map[string]File{out.Files, out.Initial} {
			if err := validateGovernance4(files, profile); err != nil {
				return err
			}
		}
	}
	if out.SchemaVersion >= 2 {
		if out.TemplateVersion == "" || !fullCommit.MatchString(out.TemplateCommit) || out.SourcePolicy.Digest == "" {
			return domain.Fail("BUNDLE", "v2来源身份缺失")
		}
	}
	if out.SchemaVersion >= 3 && out.Upgrade == nil {
		return domain.Fail("BUNDLE", "Bundle v3 缺少升级政策")
	}
	if out.Upgrade != nil {
		if err := out.Upgrade.Validate(out); err != nil {
			return err
		}
	}
	if err := validateNativeTransforms(out); err != nil {
		return err
	}
	for _, req := range out.StageRequirements {
		for _, p := range req.Paths {
			if _, ok := out.Files[p]; !ok {
				return domain.Fail("BUNDLE", "阶段快照缺文件: "+p)
			}
		}
	}
	for _, req := range out.SkillRequirements {
		for _, p := range req.Paths {
			if _, ok := out.Files[p]; !ok {
				return domain.Fail("BUNDLE", "Skill快照缺文件: "+p)
			}
		}
	}
	return nil
}

func validateGovernance4(files map[string]File, profile string) error {
	read := func(ref string) ([]byte, error) {
		f, ok := files[ref]
		if !ok {
			return nil, domain.Fail("BUNDLE", "Bundle4 缺少身份或政策: "+ref)
		}
		return base64.StdEncoding.DecodeString(f.Data)
	}
	doc := func(ref string) (map[string]any, error) {
		raw, err := read(ref)
		if err != nil {
			return nil, err
		}
		value, err := schema.Parse(raw)
		if err != nil {
			return nil, err
		}
		m, ok := value.(map[string]any)
		if !ok {
			return nil, domain.Fail("BUNDLE", "Bundle4 身份必须为对象")
		}
		return m, nil
	}
	identity, err := doc("yss-project.yaml")
	if err != nil {
		return err
	}
	harness, err := doc(".template-spec/process/harness-profile.yaml")
	if err != nil {
		return err
	}
	p, err := domain.GetProfile(profile)
	if err != nil {
		return err
	}
	capabilities, _ := harness["required_capabilities"].([]any)
	if len(identity) != 2 || identity["schema_version"] != json.Number("2") || identity["repository_mode"] != "project-instance" || harness["schema_version"] != json.Number("3") || harness["profile_id"] != p.ID || harness["status"] != "active" {
		return domain.Fail("BUNDLE", "Bundle4 必须使用当前 identity2/Profile3 精确组合")
	}
	policy, _, _, err := schema.LoadOrchestration(read, schema.OrchestrationRef)
	if err != nil {
		return err
	}
	stage, stageEnabled := policy["stage_gates"].(map[string]any)
	expected := map[string]bool{"role-independent-governance-v1": true}
	if stageEnabled {
		if profile != "spec" || stage["capability"] != "stage-gates-v1" || stage["schema_version"] != json.Number("1") {
			return domain.Fail("BUNDLE", "四阶段政策只能显式用于 Spec")
		}
		expected["stage-gates-v1"] = true
	}
	if len(capabilities) != len(expected) {
		return domain.Fail("BUNDLE", "Bundle4 Profile 能力与当前政策不匹配")
	}
	for _, value := range capabilities {
		cap, ok := value.(string)
		if !ok || !expected[cap] {
			return domain.Fail("BUNDLE", "Bundle4 包含未知或重复能力")
		}
		delete(expected, cap)
	}
	versions, _ := policy["governance_versions"].(map[string]any)
	if policy["schema_version"] != json.Number("2") || versions["required_capability"] != "role-independent-governance-v1" {
		return domain.Fail("BUNDLE", "Bundle4 必须提供明确的新治理政策2")
	}
	return nil
}

type Requirement struct {
	Paths             []string `json:"paths"`
	Skills            []string `json:"skills"`
	UnsupportedReason string   `json:"unsupportedReason,omitempty"`
}
