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
