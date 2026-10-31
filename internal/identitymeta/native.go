// Package identitymeta reads installation records without imposing a workflow.
package identitymeta

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

type Managed struct {
	Baseline    domain.Descriptor `json:"baseline"`
	Applied     domain.Descriptor `json:"lastApplied"`
	Ownership   string            `json:"ownership"`
	Source      *BaselineSource   `json:"baselineSource,omitempty"`
	Disposition *Disposition      `json:"disposition,omitempty"`
}
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
	SchemaVersion       int                `json:"schemaVersion"`
	BundleSchemaVersion int                `json:"bundleSchemaVersion"`
	Profile             string             `json:"profile"`
	ProfileID           string             `json:"profileId"`
	ProtocolVersion     int                `json:"protocolVersion"`
	CLIVersion          string             `json:"cliVersion"`
	CLICommit           string             `json:"cliCommit,omitempty"`
	CLISourceState      string             `json:"cliSourceState,omitempty"`
	TemplateVersion     string             `json:"templateVersion"`
	TemplateCommit      string             `json:"templateCommit"`
	TemplateSourceState string             `json:"templateSourceState"`
	SnapshotHash        string             `json:"snapshotHash"`
	ManifestHash        string             `json:"manifestHash"`
	BundleHash          string             `json:"bundleHash"`
	Managed             map[string]Managed `json:"managedFiles"`
	BaselineDigest      string             `json:"baselineDigest"`
	Variables           map[string]string  `json:"variables"`
	Distribution        map[string]any     `json:"distribution"`
}

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func ValidateNative(root string, raw []byte, found string) (*Metadata, error) {
	if !json.Valid(raw) {
		return nil, domain.Fail("IDENTITY", "安装记录不是 JSON")
	}
	if _, err := schema.Parse(raw); err != nil {
		return nil, err
	}
	var m Metadata
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m.SchemaVersion < 3 || m.SchemaVersion > 6 || m.ProtocolVersion != domain.ProtocolVersion {
		return nil, domain.Fail("IDENTITY", "不支持的安装记录版本；保留现有文件")
	}
	p, err := domain.GetProfile(m.Profile)
	if err != nil {
		return nil, err
	}
	if m.ProfileID != p.ID || found != "" && found != m.Profile {
		return nil, domain.Fail("IDENTITY", "项目 Profile 与安装记录不一致")
	}
	if m.Managed == nil || m.Variables == nil || m.Distribution == nil {
		return nil, domain.Fail("IDENTITY", "安装记录缺少文件基线或安装范围")
	}
	kind, _ := m.Distribution["installation"].(string)
	if m.SchemaVersion < 6 && (kind == "governed" || kind == "") {
		kind = "project"
	}
	if kind != "project" && kind != "skills" {
		return nil, domain.Fail("IDENTITY", "未知安装范围")
	}
	b, err := json.Marshal(m.Managed)
	if err != nil || !digestPattern.MatchString(m.BaselineDigest) || safefs.Digest(b) != m.BaselineDigest {
		return nil, domain.Fail("BASELINE", "安装文件基线摘要不一致")
	}
	var paths safefs.PathSet
	for ref, f := range m.Managed {
		if err := paths.Add(ref); err != nil {
			return nil, err
		}
		if strings.EqualFold(ref, domain.MetadataFile) || strings.EqualFold(strings.SplitN(ref, "/", 2)[0], ".yss") {
			return nil, domain.Fail("BASELINE", "安装清单不可管理自身状态")
		}
		if _, err := safefs.Path(root, ref); err != nil {
			return nil, err
		}
		for _, d := range []domain.Descriptor{f.Applied, f.Baseline} {
			if d.Type != "file" || !digestPattern.MatchString(d.Digest) || d.Mode > 0777 {
				return nil, domain.Fail("BASELINE", fmt.Sprintf("无效文件基线: %s", ref))
			}
		}
		switch f.Ownership {
		case "managed", "managed-customizable", "generated", "user-owned", "protected":
		default:
			return nil, domain.Fail("BASELINE", "未知文件归属: "+ref)
		}
	}
	m.Distribution["installation"] = kind
	return &m, nil
}
