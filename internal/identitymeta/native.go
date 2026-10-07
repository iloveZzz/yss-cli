// Package identitymeta validates the shared native metadata contract without
// opening assets. Callers provide bytes from their own observed reading view.
package identitymeta

import (
	"bytes"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"regexp"
)

type Managed struct {
	Baseline    domain.Descriptor `json:"baseline"`
	Applied     domain.Descriptor `json:"lastApplied"`
	Ownership   string            `json:"ownership"`
	Source      *BaselineSource   `json:"baselineSource,omitempty"`
	Disposition *Disposition      `json:"disposition,omitempty"`
}

// BaselineSource distinguishes canonical template material from bytes accepted
// in a project. Unknown historical material is explicitly unavailable.
type BaselineSource struct {
	Kind           string `json:"kind"`
	TemplateCommit string `json:"templateCommit,omitempty"`
	SnapshotHash   string `json:"snapshotHash,omitempty"`
	BundleHash     string `json:"bundleHash,omitempty"`
	Path           string `json:"path,omitempty"`
	Variant        string `json:"variant,omitempty"`
	TemplateDigest string `json:"templateDigest,omitempty"`
}
type Disposition struct {
	Choice  string            `json:"choice"`
	Target  domain.Descriptor `json:"target"`
	Applied domain.Descriptor `json:"applied"`
	RuleID  string            `json:"ruleId,omitempty"`
}
type Metadata struct {
	BundleSchemaVersion int                `json:"bundleSchemaVersion,omitempty"`
	BundleHash          string             `json:"bundleHash,omitempty"`
	CLICommit           string             `json:"cliCommit,omitempty"`
	CLISourceState      string             `json:"cliSourceState,omitempty"`
	TemplateSourceState string             `json:"templateSourceState"`
	SchemaVersion       int                `json:"schemaVersion"`
	Profile             string             `json:"profile"`
	ProfileID           string             `json:"profileId"`
	ProtocolVersion     int                `json:"protocolVersion"`
	CLIVersion          string             `json:"cliVersion"`
	TemplateVersion     string             `json:"templateVersion"`
	LegacyCLIVersion    string             `json:"legacyCliVersion"`
	TemplateCommit      string             `json:"templateCommit"`
	SnapshotHash        string             `json:"snapshotHash"`
	ManifestHash        string             `json:"manifestHash"`
	Managed             map[string]Managed `json:"managedFiles"`
	BaselineDigest      string             `json:"baselineDigest"`
	Variables           map[string]string  `json:"variables"`
	Distribution        map[string]any     `json:"distribution"`
}

var digestPattern = regexp.MustCompile("^[a-f0-9]{64}$")

func ValidateNative(root string, b []byte, found string) (*Metadata, error) {
	var meta Metadata
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&meta); e != nil {
		return nil, e
	}
	if (meta.SchemaVersion < 1 || meta.SchemaVersion > 3) || meta.ProtocolVersion != domain.ProtocolVersion {
		return nil, domain.Explain(domain.Fail("IDENTITY", "未知统一 CLI metadata 或协议版本"), "METADATA_SCHEMA_UNSUPPORTED", "当前执行器不支持该实例的元数据或协议版本。", map[string]any{"root": root, "metadataSchema": meta.SchemaVersion, "instanceProtocol": meta.ProtocolVersion, "instanceCLI": meta.CLIVersion, "templateCommit": meta.TemplateCommit})
	}
	p, e := domain.GetProfile(meta.Profile)
	if e != nil {
		return nil, e
	}
	if p.ID != meta.ProfileID || (found != "" && found != meta.Profile) {
		return nil, domain.Explain(domain.Fail("IDENTITY", "统一 metadata 与旧家族身份矛盾"), "PROFILE_IDENTITY_CONFLICT", "统一元数据与 Profile 合同或旧家族身份不一致。", map[string]any{"root": root, "profile": meta.Profile, "legacyProfile": found})
	}
	if !regexp.MustCompile("^[a-f0-9]{40}$").MatchString(meta.TemplateCommit) || !digestPattern.MatchString(meta.SnapshotHash) || !digestPattern.MatchString(meta.ManifestHash) || meta.TemplateSourceState != "committed" {
		return nil, domain.Fail("BASELINE", "统一 metadata 模板来源、快照或清单摘要缺失或不合法")
	}
	semver := regexp.MustCompile("^v?[0-9]+\\.[0-9]+\\.[0-9]+(?:[-+][0-9A-Za-z.+-]+)?$")
	if !semver.MatchString(meta.CLIVersion) || !(semver.MatchString(meta.TemplateVersion) || meta.SchemaVersion >= 2 && meta.TemplateVersion == "git:"+meta.TemplateCommit) || !semver.MatchString(meta.LegacyCLIVersion) || meta.Variables == nil || meta.Distribution == nil {
		return nil, domain.Fail("BASELINE", "统一 metadata 版本、变量或分发合同缺失")
	}
	if meta.SchemaVersion >= 2 {
		if (meta.BundleSchemaVersion != 2 && !(meta.SchemaVersion == 3 && meta.BundleSchemaVersion == 3)) || !digestPattern.MatchString(meta.BundleHash) || (meta.CLICommit != "" && !regexp.MustCompile("^[a-f0-9]{40}$").MatchString(meta.CLICommit)) || (meta.CLISourceState != "committed" && meta.CLISourceState != "working-tree" && meta.CLISourceState != "unknown") || meta.CLISourceState == "committed" && meta.CLICommit == "" {
			return nil, domain.Fail("BASELINE", "native v2 Bundle/CLI provenance invalid")
		}
	}
	if meta.Managed == nil || !digestPattern.MatchString(meta.BaselineDigest) {
		return nil, domain.Fail("BASELINE", "统一 CLI 基线缺失")
	}
	b, e = json.Marshal(meta.Managed)
	if e != nil || safefs.Digest(b) != meta.BaselineDigest {
		return nil, domain.Fail("BASELINE", "统一 CLI 基线摘要不一致")
	}
	for ref, m := range meta.Managed {
		if _, e := safefs.Path(root, ref); e != nil {
			return nil, e
		}
		modeValid := m.Applied.Mode == 0644 || m.Applied.Mode == 0755 || meta.SchemaVersion == 3 && m.Applied.Mode <= 0777
		if m.Applied.Type != "file" || !digestPattern.MatchString(m.Applied.Digest) || m.Baseline.Type != "file" || !digestPattern.MatchString(m.Baseline.Digest) || !modeValid {
			return nil, domain.Fail("BASELINE", "未知文件基线: "+ref)
		}
		if meta.SchemaVersion == 3 {
			if m.Source == nil || (m.Source.Kind != "bundle" && m.Source.Kind != "unavailable") {
				return nil, domain.Fail("BASELINE", "缺少基线来源: "+ref)
			}
			if m.Source.Kind == "bundle" && (!regexp.MustCompile("^[a-f0-9]{40}$").MatchString(m.Source.TemplateCommit) || !digestPattern.MatchString(m.Source.SnapshotHash) || !digestPattern.MatchString(m.Source.BundleHash) || !digestPattern.MatchString(m.Source.TemplateDigest) || m.Source.Path != ref || (m.Source.Variant != "initial" && m.Source.Variant != "full" && m.Source.Variant != "generated")) {
				return nil, domain.Fail("BASELINE", "基线来源不合法: "+ref)
			}
			if m.Disposition != nil && (m.Disposition.Choice != "keep-local" && m.Disposition.Choice != "use-merged" || m.Disposition.Applied != m.Applied || m.Disposition.Target != m.Baseline) {
				return nil, domain.Fail("BASELINE", "保留决定与基线矛盾: "+ref)
			}
		}
	}
	return &meta, nil
}
