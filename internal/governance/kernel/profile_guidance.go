package kernel

import "errors"

import "github.com/iloveZzz/yss-cli/internal/schema"
import "github.com/iloveZzz/yss-cli/internal/domain"

func GuidanceContractRef(profile string) string { return schema.OrchestrationRef }

func orchestrationReference(exists func(string) (bool, error), doc func(string) (map[string]any, error)) (string, error) {
	owner := "yss-product-lifecycle"
	if present, err := exists(".template-spec/process/harness-profile.yaml"); err != nil {
		return "", err
	} else if present {
		profile, err := doc(".template-spec/process/harness-profile.yaml")
		if err != nil {
			return "", err
		}
		owner = map[string]string{"harness.spec-template": "yss-product-lifecycle", "harness.backend-delivery": "harness-orchestrator"}[Text(profile["profile_id"])]
		if owner == "" {
			return "", domain.Fail("IDENTITY", "未知合同 Profile")
		}
	}
	if present, err := exists(schema.OrchestrationRef); err != nil {
		return "", err
	} else if present {
		return schema.OrchestrationRef, nil
	}
	return ".agents/skills/" + owner + "/references/orchestration-contract.yaml", nil
}

func (s *SemanticSession) OrchestrationReference() (string, error) {
	return orchestrationReference(s.Exists, s.Doc)
}

func (s *SemanticSession) OrchestrationDocument(ref string) (map[string]any, []string, string, error) {
	if ref == schema.OrchestrationRef {
		var err error
		ref, err = s.OrchestrationReference()
		if err != nil {
			return nil, nil, "", err
		}
	}
	value, refs, digest, err := schema.LoadOrchestration(s.Bytes, ref)
	if err != nil {
		var failure *domain.Error
		if errors.As(err, &failure) {
			return nil, nil, "", err
		}
		return nil, nil, "", s.Unavailable("INPUT", err.Error())
	}
	return value, refs, "sha256:" + digest, nil
}
