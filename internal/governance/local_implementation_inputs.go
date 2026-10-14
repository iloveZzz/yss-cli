package governance

import (
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

// This selects a policy path only. The current feature and approval facts are
// revalidated at each actual consumption seam. Only the Spec family can carry
// local implementation inputs; the removed frontend line never reaches here.
func hasLocalImplementationInputs(s *kernel.SemanticSession) (bool, error) {
	identity, err := s.Doc("yss-project.yaml")
	if err != nil {
		return false, err
	}
	if identity["repository_mode"] != "project-instance" {
		return false, nil
	}
	present, err := s.Exists(".yss.json")
	if err != nil || !present {
		return false, err
	}
	metadata, err := s.Doc(".yss.json")
	if err != nil {
		return false, err
	}
	if kernel.Text(metadata["profile"]) != "spec" {
		return false, nil
	}
	profile, err := s.Doc(".template-spec/process/harness-profile.yaml")
	if err != nil {
		return false, err
	}
	if metadata["profileId"] != domain.Profiles["spec"].ID || profile["profile_id"] != domain.Profiles["spec"].ID {
		return false, s.Reject("IDENTITY", "本地业务输入 Profile 身份不一致")
	}
	if err = kernel.ProjectIdentity(s.V); err != nil {
		return false, err
	}
	contract, err := s.Doc(kernel.GuidanceContractRef("spec"))
	if err != nil {
		return false, err
	}
	policy := kernel.SemMap(contract["progression_target"])
	if policy["local_implementation_inputs"] == nil {
		return false, nil
	}
	if kernel.ContractN(contract["schema_version"]) != 1 || kernel.ContractN(policy["schema_version"]) != 1 || policy["config_file"] != kernel.ProgressionFile || !kernel.SameStringSet(policy["required_capabilities"], []any{"lifecycle-target-v1"}) || !kernel.SemHas(policy["writer_profiles"], "spec") {
		return false, s.Unavailable("CAPABILITY", "已声明本地实现输入，但政策版本或能力不受支持")
	}
	if policy["local_implementation_inputs"] != "native-spec-current-feature-approved-assets" {
		return false, s.Unavailable("CAPABILITY", "未知本地实现输入政策")
	}
	return true, nil
}
