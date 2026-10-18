package kernel

import (
	"github.com/iloveZzz/yss-cli/internal/domain"
	"strings"
)

func SemanticDefinition(registry map[string]any, bucket, id string) map[string]any {
	for _, row := range SemList(registry[bucket]) {
		m := SemMap(row)
		if Text(m["id"]) == id {
			return m
		}
	}
	return nil
}

func (s *SemanticSession) Orchestration() (map[string]any, string, error) {
	if s.GovernanceVersion == 2 {
		if s.GovernancePolicy == nil {
			return nil, "", s.Unavailable("GOVERNANCE_POLICY", "缺少已核验的当前编排政策")
		}
		return s.GovernancePolicy, ".template-spec/process/orchestration-contract.yaml", nil
	}
	ref := GuidanceContractRef("")
	profileName := "spec"
	if present, err := s.Exists(".template-spec/process/harness-profile.yaml"); err != nil {
		return nil, "", err
	} else if present {
		profile, err := s.Doc(".template-spec/process/harness-profile.yaml")
		if err != nil {
			return nil, "", err
		}
		profileName = ""
		for name, known := range domain.Profiles {
			if profile["profile_id"] == known.ID {
				profileName = name
			}
		}
		if profileName == "" {
			return nil, "", s.Unavailable("IDENTITY", "无法识别当前 Profile 的主控合同")
		}
	}
	present, err := s.Exists(ref)
	if err != nil {
		return nil, ref, err
	}
	if !present {
		owner := map[string]string{"spec": "yss-product-lifecycle", "backend": "harness-orchestrator", "design": "yss-strategic-design"}[profileName]
		ref = ".agents/skills/" + owner + "/references/orchestration-contract.yaml"
		present, err = s.Exists(ref)
		if err != nil {
			return nil, ref, err
		}
		if !present {
			ref = ".template-spec/process/checkpoint-boundary.yaml"
		}
	}
	doc, err := s.Doc(ref)
	if err == nil {
		version, valid := Integer(doc["schema_version"])
		if !valid || version != 1 && !(version == 2 && profileName == "design" && strings.HasSuffix(ref, "/orchestration-contract.yaml")) {
			return nil, ref, s.Unavailable("CAPABILITY", "未知本地主控合同版本")
		}
	}
	return doc, ref, err
}

// Draft preparation is a route classification, never an approval or write grant.
func (s *SemanticSession) DraftPreparation(cp map[string]any) bool {
	if s.GovernanceVersion != 2 || cp["status"] != "routing" || !SemHas([]string{"resume", "orchestrate"}, Text(cp["mode"])) {
		return false
	}
	if !SemHas(SemMap(s.GovernancePolicy["preparation_policy"])["work_units"], Text(cp["next_work_unit"])) {
		return false
	}
	for _, id := range []string{"gate.plan-approved", "gate.spec-baseline-approved"} {
		if SemMap(SemMap(cp["gates"])[id])["status"] == "approved" {
			return false
		}
	}
	return cp["plan_approval_ref"] == nil && cp["plan_review_control"] == nil
}
