package bundle

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/base64"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"io"
	"strings"
)

//go:embed assets/*.json.gz
var assets embed.FS

type File struct {
	Data      string `json:"data"`
	Digest    string `json:"digest"`
	Mode      uint32 `json:"mode"`
	Ownership string `json:"ownership"`
}
type Bundle struct {
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
	if _, err := domain.GetProfile(profile); err != nil {
		return nil, err
	}
	b, err := assets.ReadFile("assets/" + profile + ".json.gz")
	if err != nil {
		return nil, domain.Wrap("BUNDLE", err)
	}
	z, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer z.Close()
	raw, err := io.ReadAll(io.LimitReader(z, 512*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 512*1024*1024 {
		return nil, domain.Fail("BUNDLE", "快照超过限制")
	}
	var out Bundle
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if err = validate(&out, profile); err != nil {
		return nil, err
	}
	if out.SchemaVersion == 2 && out.BundleHash != contentHash(&out) {
		return nil, domain.Fail("BUNDLE", "Bundle 摘要不一致")
	}
	return &out, nil
}
func (f File) Render(vars map[string]string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(f.Data)
	if err != nil {
		return nil, err
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return b, nil
	}
	text := string(b)
	for k, token := range map[string]string{"projectName": "__YSS_PROJECT_NAME__", "businessDomain": "__YSS_BUSINESS_DOMAIN__", "teamSize": "__YSS_TEAM_SIZE__"} {
		text = strings.ReplaceAll(text, token, vars[k])
	}
	return []byte(text), nil
}

func validate(out *Bundle, profile string) error {
	if (out.SchemaVersion != 1 && out.SchemaVersion != 2) || out.Profile != profile || out.SourceState != "committed" {
		return domain.Fail("BUNDLE", "快照身份或来源不合法")
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
			data, err := base64.StdEncoding.DecodeString(f.Data)
			if err != nil || safefs.Digest(data) != f.Digest {
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
	if out.SchemaVersion == 2 {
		if out.TemplateVersion == "" || !fullCommit.MatchString(out.TemplateCommit) || out.SourcePolicy.Digest == "" {
			return domain.Fail("BUNDLE", "v2来源身份缺失")
		}
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

type Requirement struct {
	Paths             []string `json:"paths"`
	Skills            []string `json:"skills"`
	UnsupportedReason string   `json:"unsupportedReason,omitempty"`
}
