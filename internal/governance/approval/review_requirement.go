package approval

import (
	"strings"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
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
	if message["actor_kind"] != "biological-human" {
		return s.Reject("EVIDENCE_REVIEW_REQUIREMENT", "独立复核要求必须来自实际用户消息")
	}
	// Deliberately finite source grammar, not NLP: match the entire original
	// message to the normalized request and exact captured scope. Never rewrite
	// an unsupported source into a confirming message or infer from keywords.
	text := strings.TrimSpace(kernel.Text(message["text"]))
	scopes := kernel.ApStrings(requirement["scope"])
	for _, scope := range scopes {
		if strings.ContainsAny(scope, " \t\r\n、，,。") {
			return s.Reject("EVIDENCE_REVIEW_REQUIREMENT", "首版明确请求格式无法无歧义表示该范围，保留原始来源缺口")
		}
	}
	chinese := "请独立复核 " + strings.Join(scopes, "、")
	english := "Please independently review " + strings.Join(scopes, ", ")
	if text != chinese && text != chinese+"。" && text != english && text != english+"." {
		return s.Reject("EVIDENCE_REVIEW_REQUIREMENT", "无法从原始消息确认明确独立复核请求及范围；首版仅支持完整‘请独立复核 <范围>’或‘Please independently review <scope>’来源格式，不能改写原话或降级")
	}
	return nil
}
