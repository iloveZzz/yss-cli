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
	SchemaVersion     int             `json:"schemaVersion"`
	Profile           string          `json:"profile"`
	LegacyVersion     string          `json:"legacyVersion"`
	CLICommit         string          `json:"cliCommit"`
	TemplateCommit    string          `json:"templateCommit"`
	SourceState       string          `json:"sourceState"`
	SnapshotHash      string          `json:"sourceSnapshotHash"`
	ManifestHash      string          `json:"manifestHash"`
	Distribution      map[string]any  `json:"distribution"`
	Manifest          map[string]any  `json:"manifest"`
	Initial           map[string]File `json:"initial"`
	Files             map[string]File `json:"files"`
	StageRequirements map[string]struct {
		Paths  []string `json:"paths"`
		Skills []string `json:"skills"`
	} `json:"stageRequirements"`
	SkillRequirements map[string]struct {
		Paths             []string `json:"paths"`
		Skills            []string `json:"skills"`
		UnsupportedReason string   `json:"unsupportedReason,omitempty"`
	} `json:"skillRequirements"`
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
	if out.SchemaVersion != 1 || out.Profile != profile || out.SourceState != "committed" {
		return nil, domain.Fail("BUNDLE", "快照身份或来源不合法")
	}
	for ref, f := range out.Files {
		if _, err := safefs.Path("/__yss_check__", ref); err != nil {
			return nil, err
		}
		data, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil || safefs.Digest(data) != f.Digest {
			return nil, domain.Fail("BUNDLE", "快照摘要不一致: "+ref)
		}
	}
	for ref, f := range out.Initial {
		data, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil || safefs.Digest(data) != f.Digest {
			return nil, domain.Fail("BUNDLE", "初始化快照摘要不一致: "+ref)
		}
	}
	if out.Initial == nil {
		out.Initial = out.Files
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
