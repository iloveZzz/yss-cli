package kernel

import "github.com/iloveZzz/yss-cli/internal/schema"

// Only bound inputs of already validated implementation/review consumers supply requirements.
func (s *SemanticSession) RequiredReviewMode(candidate, implementation map[string]any, scope []string, bindings ...any) (string, error) {
	mode := "self-check"
	seen := map[string]string{}
	for _, basis := range bindings {
		for _, asset := range ApRows(basis) {
			ref, digest := Text(asset["ref"]), Text(asset["digest"])
			if old, ok := seen[ref]; ok {
				if old != digest {
					return "", s.Reject("EVIDENCE_BINDING", "复核要求输入摘要冲突")
				}
				continue
			}
			seen[ref] = digest
			raw, err := s.EvidenceAsset(asset)
			if err != nil {
				return "", err
			}
			value, err := schema.Parse(raw)
			if err != nil {
				continue
			}
			record, ok := Object(value)
			if !ok {
				continue
			}
			if rules, exists := Object(record["external_rules"]); exists {
				if err = s.ValidateExternalRulesDigest(rules, candidate, scope, implementation); err != nil {
					return "", err
				}
			}
			if record["kind"] != "review-requirement" {
				continue
			}
			if !ApUnique(record["scope"]) {
				return "", s.Reject("EVIDENCE_REVIEW_REQUIREMENT", "已知复核范围不可判定")
			}
			applies := false
			for _, requested := range ApStrings(record["scope"]) {
				applies = applies || SemHas(scope, requested)
			}
			if !applies {
				continue
			}
			if err = s.Verify("review-requirement", ref, nil); err != nil {
				return "", err
			}
			mode = "independent-review"
		}
	}
	return mode, nil
}
