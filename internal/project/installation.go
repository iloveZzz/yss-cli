package project

import (
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/domain"
)

type InstallationCLI struct {
	domain.Provenance
	ExecutableSHA256 string `json:"executableSha256"`
}

func installationCLI() (*InstallationCLI, error) {
	digest, err := domain.ExecutableDigest()
	if err != nil {
		return nil, err
	}
	return &InstallationCLI{Provenance: domain.BuildProvenance(), ExecutableSHA256: digest}, nil
}

type InstallationKind string

const (
	InstallationSkills   InstallationKind = "skills"
	InstallationGoverned InstallationKind = "governed"
)

func ResolveInstallation(id *Identity, command string, requested InstallationKind) (InstallationKind, error) {
	if requested != "" && requested != InstallationSkills && requested != InstallationGoverned {
		return "", domain.Fail("ARGUMENT", "未知安装方式")
	}
	if requested != "" && command != "init" && command != "attach" && command != "migrate" {
		return "", domain.Fail("ARGUMENT", "此命令不能更改安装方式")
	}
	saved := InstallationGoverned
	if id.Native != nil && id.Native.Distribution["installation"] == "skills" {
		saved = InstallationSkills
	}
	if id.Native != nil {
		if requested != "" && requested != saved && command != "migrate" {
			return "", domain.Fail("INSTALLATION_MIGRATION_REQUIRED", "安装方式变化需要显式迁移")
		}
		if requested != "" {
			return requested, nil
		}
		return saved, nil
	}
	if requested != "" {
		return requested, nil
	}
	return InstallationSkills, nil
}
func skillsIdentity(id *Identity) bool {
	return id.Native != nil && id.Native.SchemaVersion == 5 && id.Native.Distribution["installation"] == "skills"
}

func cloneInstallationDistribution(d map[string]any) map[string]any {
	data, _ := json.Marshal(d)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return out
}

func planningOptionsForReplay(p *Plan) PlanningOptions {
	option := InstallationKind("")
	if p.Command == "init" || p.Command == "attach" || p.InstallationMigration != nil {
		option = p.Installation
	}
	return PlanningOptions{Installation: option, BindCLI: p.SchemaVersion == 5, BaseBundlePath: p.BaseBundlePath, ResolvedFrom: p.ResolvedFrom, Resolutions: p.Resolutions}
}
