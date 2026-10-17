package approval

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"strings"
)

func init() { kernel.RegisterRoleIndependentValidator("review-requirement", verifyReviewRequirement) }
func verifyReviewRequirement(s *kernel.SemanticSession, ref string, _ map[string]string) error {
	requirement, err := s.Doc(ref)
	if err != nil {
		return err
	}
	if len(requirement) != 4 || requirement["kind"] != "review-requirement" || requirement["mode"] != "independent-review" || !kernel.ApUnique(requirement["scope"]) {
		return s.Reject("EVIDENCE_REVIEW_REQUIREMENT", "已知独立复核要求字段无效")
	}
	message, err := decisionMessage(s, kernel.ApMap(requirement["source"]))
	if err != nil {
		return err
	}
	text := strings.ToLower(kernel.Text(message["text"]))
	if message["actor_kind"] != "biological-human" || (!strings.Contains(text, "独立") && !strings.Contains(text, "independent")) || strings.Contains(text, "不要独立") || strings.Contains(text, "无需独立") || strings.Contains(text, "not independent") || strings.Contains(text, "no independent") {
		return s.Reject("EVIDENCE_REVIEW_REQUIREMENT", "来源不是实际用户的明确独立复核要求")
	}
	for _, scope := range kernel.ApStrings(requirement["scope"]) {
		if !strings.Contains(kernel.Text(message["text"]), scope) {
			return s.Reject("EVIDENCE_REVIEW_REQUIREMENT", "用户原始要求未明确覆盖登记范围")
		}
	}
	return nil
}
