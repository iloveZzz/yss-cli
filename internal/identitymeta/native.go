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
	Baseline  domain.Descriptor `json:"baseline"`
	Applied   domain.Descriptor `json:"lastApplied"`
	Ownership string            `json:"ownership"`
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
	if (meta.SchemaVersion != 1 && meta.SchemaVersion != 2) || meta.ProtocolVersion != domain.ProtocolVersion {
		return nil, domain.Fail("IDENTITY", "未知统一 CLI metadata 或协议版本")
	}
	p, e := domain.GetProfile(meta.Profile)
	if e != nil {
		return nil, e
	}
	if p.ID != meta.ProfileID || (found != "" && found != meta.Profile) {
		return nil, domain.Fail("IDENTITY", "统一 metadata 与旧家族身份矛盾")
	}
	if !regexp.MustCompile("^[a-f0-9]{40}$").MatchString(meta.TemplateCommit) || !digestPattern.MatchString(meta.SnapshotHash) || !digestPattern.MatchString(meta.ManifestHash) || meta.TemplateSourceState != "committed" {
		return nil, domain.Fail("BASELINE", "统一 metadata 模板来源、快照或清单摘要缺失或不合法")
	}
	semver := regexp.MustCompile("^v?[0-9]+\\.[0-9]+\\.[0-9]+(?:[-+][0-9A-Za-z.+-]+)?$")
	if !semver.MatchString(meta.CLIVersion) || !(semver.MatchString(meta.TemplateVersion) || meta.SchemaVersion == 2 && meta.TemplateVersion == "git:"+meta.TemplateCommit) || !semver.MatchString(meta.LegacyCLIVersion) || meta.Variables == nil || meta.Distribution == nil {
		return nil, domain.Fail("BASELINE", "统一 metadata 版本、变量或分发合同缺失")
	}
	if meta.SchemaVersion == 2 {
		if meta.BundleSchemaVersion != 2 || !digestPattern.MatchString(meta.BundleHash) || (meta.CLICommit != "" && !regexp.MustCompile("^[a-f0-9]{40}$").MatchString(meta.CLICommit)) || (meta.CLISourceState != "committed" && meta.CLISourceState != "working-tree" && meta.CLISourceState != "unknown") || meta.CLISourceState == "committed" && meta.CLICommit == "" {
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
		if m.Applied.Type != "file" || !digestPattern.MatchString(m.Applied.Digest) || m.Baseline.Type != "file" || !digestPattern.MatchString(m.Baseline.Digest) || (m.Applied.Mode != 0644 && m.Applied.Mode != 0755) {
			return nil, domain.Fail("BASELINE", "未知文件基线: "+ref)
		}
	}
	return &meta, nil
}
