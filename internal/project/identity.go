package project

import (
	"bytes"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"os"
	"path/filepath"
	"regexp"
)

const MetadataFile = domain.MetadataFile

type Managed = identitymeta.Managed
type Metadata = identitymeta.Metadata
type Identity struct {
	Root       string         `json:"root"`
	Profile    domain.Profile `json:"profile"`
	Mode       string         `json:"repositoryMode"`
	Native     *Metadata      `json:"native,omitempty"`
	Legacy     map[string]any `json:"legacy,omitempty"`
	LegacyFile string         `json:"legacyFile,omitempty"`
}

var digestPattern = regexp.MustCompile("^[a-f0-9]{64}$")

func exists(root, ref string) (bool, error) {
	p, err := safefs.Path(root, ref)
	if err != nil {
		return false, err
	}
	_, err = os.Lstat(p)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err == nil {
		info, e := os.Lstat(p)
		if e != nil {
			return false, e
		}
		if !info.Mode().IsRegular() {
			return false, domain.Fail("PATH", "身份或权威资产不是普通文件: "+ref)
		}
	}
	return err == nil, err
}
func load(root, ref string) (any, error) {
	p, err := safefs.Path(root, ref)
	if err != nil {
		return nil, err
	}
	return schema.LoadFile(p)
}
func object(v any) (map[string]any, bool) { m, ok := v.(map[string]any); return m, ok }
func number(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}
func text(v any) string { s, _ := v.(string); return s }
func Detect(root, explicit string, allowAbsent bool) (*Identity, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if _, err = safefs.Path(root, "yss-project.yaml"); err != nil {
		return nil, err
	}
	out := &Identity{Root: root}
	found := ""
	for name, p := range domain.Profiles {
		ok, err := exists(root, p.Metadata)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if found != "" {
			return nil, domain.Fail("IDENTITY", "检测到多个家族 metadata，拒绝混用")
		}
		found = name
		v, err := load(root, p.Metadata)
		if err != nil {
			return nil, err
		}
		m, ok := object(v)
		if !ok {
			return nil, domain.Fail("IDENTITY", "metadata 必须是对象")
		}
		ver := number(m["metadataSchemaVersion"])
		if (name == "spec" && ver != 3) || (name != "spec" && ver != 2) {
			return nil, domain.Fail("LEGACY", "旧 metadata schema 尚未迁移；请使用原 CLI 检查和恢复")
		}
		if text(m["templateSource"]) != p.TemplateSource || (name != "spec" && text(m["profileId"]) != p.ID) {
			return nil, domain.Fail("IDENTITY", "metadata 身份矛盾")
		}
		if !regexp.MustCompile("^[a-f0-9]{40}$").MatchString(text(m["templateCommit"])) {
			return nil, domain.Fail("BASELINE", "metadata 模板来源缺失")
		}
		if _, ok := object(m["managedFiles"]); !ok {
			return nil, domain.Fail("BASELINE", "metadata 缺少受管基线")
		}
		out.Legacy = m
		out.LegacyFile = p.Metadata
		if name != "spec" {
			b, e := os.ReadFile(filepath.Join(root, p.Metadata))
			if e != nil {
				return nil, e
			}
			var raw map[string]json.RawMessage
			if e = json.Unmarshal(b, &raw); e != nil {
				return nil, e
			}
			var compact bytes.Buffer
			if e = json.Compact(&compact, raw["managedFiles"]); e != nil {
				return nil, e
			}
			if safefs.Digest(compact.Bytes()) != text(m["baselineDigest"]) {
				return nil, domain.Fail("BASELINE", "旧 metadata 基线摘要损坏")
			}
		}
	}
	nativePresent, err := exists(root, MetadataFile)
	if err != nil {
		return nil, err
	}
	if nativePresent {
		if _, e := load(root, MetadataFile); e != nil {
			return nil, e
		}
		b, e := os.ReadFile(filepath.Join(root, MetadataFile))
		if e != nil {
			return nil, e
		}
		meta, e := identitymeta.ValidateNative(root, b, found)
		if e != nil {
			return nil, e
		}
		found = meta.Profile
		out.Native = meta
	}
	if explicit != "" {
		if _, err = domain.GetProfile(explicit); err != nil {
			return nil, err
		}
		if found != "" && found != explicit {
			return nil, domain.Fail("IDENTITY", "显式 Profile 与项目身份不匹配")
		}
		found = explicit
	}
	if found == "" {
		if allowAbsent {
			return out, nil
		}
		return nil, domain.Fail("IDENTITY", "缺少可识别的 Profile metadata")
	}
	out.Profile = domain.Profiles[found]
	hasIdentity, err := exists(root, "yss-project.yaml")
	if err != nil {
		return nil, err
	}
	if hasIdentity {
		v, e := load(root, "yss-project.yaml")
		if e != nil {
			return nil, e
		}
		m, ok := object(v)
		if !ok || number(m["schema_version"]) != 1 || text(m["repository_mode"]) != "project-instance" {
			return nil, domain.Fail("IDENTITY", "根身份缺失、未知 schema 或不是 project-instance")
		}
		out.Mode = "project-instance"
	} else if out.Native != nil || out.Legacy != nil || !allowAbsent {
		return nil, domain.Fail("IDENTITY", "缺少根 yss-project.yaml")
	}
	profileRef := ".template-spec/process/harness-profile.yaml"
	if ok, e := exists(root, profileRef); e != nil {
		return nil, e
	} else if ok {
		v, e := load(root, profileRef)
		if e != nil {
			return nil, e
		}
		m, ok := object(v)
		if !ok || !((number(m["schema_version"]) == 1) || (number(m["schema_version"]) == 2)) || text(m["profile_id"]) != out.Profile.ID {
			return nil, domain.Fail("IDENTITY", "Profile 合同未知或矛盾")
		}
		if inst, ok := object(m["instantiation"]); ok {
			if text(inst["cli_package"]) != out.Profile.LegacyCommand || text(inst["metadata_file"]) != out.Profile.Metadata || text(inst["template_source"]) != out.Profile.TemplateSource {
				return nil, domain.Fail("IDENTITY", "Profile 安装合同矛盾")
			}
		}
	} else if out.Native != nil || (out.Legacy != nil && out.Profile.Name != "spec") {
		return nil, domain.Fail("IDENTITY", "缺少项目 Profile 合同")
	}
	return out, nil
}
