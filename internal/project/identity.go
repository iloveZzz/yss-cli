package project

import (
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
	ProfileRef string         `json:"profileContract,omitempty"`
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
	if err := rejectRemovedDesign(root); err != nil {
		return nil, err
	}
	out := &Identity{Root: root}
	found := ""
	legacyFile := ""
	// 前端 Profile 已移除：旧前端实例（旧版 metadata）明确拒绝，不识别、不升级、不迁移，文件原样保留。
	if legacy, e := exists(root, ".yss-harness-frontend.json"); e != nil {
		return nil, e
	} else if legacy {
		_, removed := domain.GetProfile("frontend")
		return nil, removed
	}
	// 以 2.3.1 为基线：旧家族 metadata 不再识别或迁移。仅作为与统一 metadata 并存时的身份一致性参照。
	for name, p := range domain.Profiles {
		ok, err := exists(root, p.Metadata)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if found != "" {
			return nil, domain.Explain(domain.Fail("IDENTITY", "检测到多个家族 metadata，拒绝混用"), "PROFILE_IDENTITY_CONFLICT", "同一目录包含多个 Profile 的身份元数据。", map[string]any{"root": root, "profiles": []string{found, name}})
		}
		found = name
		legacyFile = p.Metadata
	}
	nativePresent, err := exists(root, MetadataFile)
	if err != nil {
		return nil, err
	}
	if !nativePresent && legacyFile != "" {
		return nil, domain.Explain(domain.Fail("LEGACY", "旧版实例不再受支持；本 CLI 以 2.3.1 为基线"), "LEGACY_INSTANCE_UNSUPPORTED", "该目录只有旧家族 metadata，没有统一 CLI metadata；不再提供迁移。", map[string]any{"root": root, "metadataFile": legacyFile})
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
		if e = validateHistoryProfile(root, meta); e != nil {
			return nil, e
		}
		out.Native = meta
	}
	if explicit != "" {
		if _, err = domain.GetProfile(explicit); err != nil {
			return nil, err
		}
		if found != "" && found != explicit {
			return nil, domain.Explain(domain.Fail("IDENTITY", "显式 Profile 与项目身份不匹配"), "PROFILE_IDENTITY_CONFLICT", "显式选择的 Profile 与实际项目身份不同。", map[string]any{"root": root, "expectedProfile": found, "requestedProfile": explicit})
		}
		found = explicit
	}
	if found == "" {
		if allowAbsent {
			return out, nil
		}
		id, cause := "PROJECT_METADATA_MISSING", "项目目录未包含可识别的 Profile 元数据。"
		if _, e := os.Lstat(root); os.IsNotExist(e) {
			id, cause = "PROJECT_ROOT_NOT_FOUND", "指定的项目目录不存在。"
		}
		return nil, domain.Explain(domain.Fail("IDENTITY", "缺少可识别的 Profile metadata"), id, cause, map[string]any{"root": root})
	}
	out.Profile = domain.Profiles[found]
	if skillsIdentity(out) {
		out.Mode = "project-instance"
		return out, nil
	}
	identityVersion := 0
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
		identityVersion = number(m["schema_version"])
		if !ok || (identityVersion != 1 && identityVersion != 2) || !exactVersion(m["schema_version"], identityVersion) || text(m["repository_mode"]) != "project-instance" {
			return nil, domain.Fail("IDENTITY", "根身份缺失、未知 schema 或不是 project-instance")
		}
		if out.Native != nil && ((identityVersion == 2 && out.Native.SchemaVersion < 4) || (identityVersion == 1 && out.Native.SchemaVersion > 3)) {
			return nil, domain.Fail("IDENTITY", "根身份与统一metadata混代；需要显式治理迁移")
		}
		out.Mode = "project-instance"
	} else if out.Native != nil || !allowAbsent {
		return nil, domain.Fail("IDENTITY", "缺少根 yss-project.yaml")
	}
	profileRef := ".template-spec/process/harness-profile.yaml"
	out.ProfileRef = profileRef
	if ok, e := exists(root, profileRef); e != nil {
		return nil, e
	} else if ok {
		v, e := load(root, profileRef)
		if e != nil {
			return nil, e
		}
		m, ok := object(v)
		profileVersion := number(m["schema_version"])
		validTuple := (identityVersion != 2 && (profileVersion == 1 || profileVersion == 2)) || (identityVersion == 2 && profileVersion == 3 && (found == "spec" || found == "backend"))
		if !ok || !validTuple || !exactVersion(m["schema_version"], profileVersion) || text(m["profile_id"]) != out.Profile.ID {
			return nil, domain.Fail("IDENTITY", "Profile 合同未知或矛盾")
		}
		if inst, ok := object(m["instantiation"]); ok {
			nativeContract := out.Native != nil && text(inst["cli_package"]) == "yss" && text(inst["metadata_file"]) == MetadataFile && text(inst["native_profile"]) == out.Profile.Name
			if !nativeContract || text(inst["template_source"]) != out.Profile.TemplateSource {
				return nil, domain.Fail("IDENTITY", "Profile 安装合同矛盾")
			}
		}
	} else if out.Native != nil || identityVersion == 2 {
		return nil, domain.Fail("IDENTITY", "缺少项目 Profile 合同")
	}
	if IsMattSpec(out) {
		if err := validateInstalledSpecWorkflow(out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func exactVersion(value any, want int) bool {
	switch n := value.(type) {
	case int:
		return n == want
	case int64:
		return n == int64(want)
	case float64:
		return n == float64(want)
	case json.Number:
		v, err := n.Int64()
		return err == nil && v == int64(want)
	}
	return false
}

func rejectRemovedDesign(root string) error {
	for _, ref := range []string{".yss-harness-design.json", ".yss-product-design-plugin.json"} {
		present, err := exists(root, ref)
		if err != nil {
			return err
		}
		if present {
			_, err = domain.GetProfile("design")
			return err
		}
	}
	return nil
}
func validateHistoryProfile(root string, meta *Metadata) error {
	if meta.GovernanceHistory == nil {
		return nil
	}
	ref := meta.GovernanceHistory.Ref
	file, err := safefs.Path(root, ref)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	return identitymeta.ValidateHistoryProfile(meta.GovernanceHistory, raw)
}
