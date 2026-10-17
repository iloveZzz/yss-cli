package lifecycle

import (
	"github.com/iloveZzz/yss-cli/internal/governance/approval"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

// standard 是 daily 与 governed 之间的交付路线：沿用 daily 的单一任务记录与全部完成条件，
// 另需一页 Spec-lite 与一次绑定其摘要的生物人决定。它不是生命周期阶段，不创建 checkpoint、
// 正式 Slice 或多级批准；只能由 daily 升级而来或直接声明，不能降级。

// standardKnownImpacts 是 CLI 认识的 standard 级影响；策略只能从中选择，未知影响仍走 governed。
var standardKnownImpacts = []string{"behavior-change", "new-capability"}

const standardDecisionID = "standard-change-approved"

// readStandardPolicy 读取可选的 delivery_path.standard；缺失或不认识时视为未启用，不影响 daily。
func (d *dailySession) readStandardPolicy() {
	p, ok := kernel.Object(d.policy["standard"])
	if !ok {
		return
	}
	version := int64(1)
	if d.s.GovernanceVersion == 2 {
		version = 2
	}
	if v, valid := kernel.Integer(p["version"]); !valid || v != version {
		return
	}
	impacts, valid := dailyStrings(p["impacts"])
	if !valid || len(impacts) == 0 {
		return
	}
	excluded, _ := dailyStrings(d.policy["excluded_impacts"])
	for _, impact := range impacts {
		if !kernel.SemHas(standardKnownImpacts, impact) || kernel.SemHas(excluded, impact) {
			return
		}
	}
	decision, ok := kernel.Object(p["decision"])
	if !ok || kernel.Text(decision["id"]) != standardDecisionID || kernel.Text(decision["actor_kind"]) != "human" {
		return
	}
	if version == 2 && (decision["requester_may_confirm"] != true || decision["agent_may_generate_user_response"] != false) {
		return
	}
	d.standard = p
}

func (d *dailySession) standardImpact(impact string) bool {
	if d.standard == nil {
		return false
	}
	impacts, _ := dailyStrings(d.standard["impacts"])
	return kernel.SemHas(impacts, impact)
}

// requireStandard：daily 记录命中 standard 级影响时升级，不直接放行也不跳到 governed。
func (d *dailySession) requireStandard(action, impact string) error {
	d.report.DeliveryPath = "standard"
	d.report.StandardRequired = true
	message := "影响 " + impact + " 需要 standard 路线：一页 Spec-lite 与一次生物人决定"
	d.report.Reasons = append(d.report.Reasons, message)
	d.report.Diagnostics = append(d.report.Diagnostics, kernel.SemanticDiagnostic{Code: "STANDARD_REQUIRED", Message: message, SourceRef: d.taskRef, Recovery: "按 Spec-lite 模板补齐需求，取得 " + standardDecisionID + " 决定后把 delivery_path 改为 standard；保留已有编辑与证据"})
	if action == "verify-daily" {
		return d.s.Reject("STANDARD_REQUIRED", message)
	}
	return nil
}

// standardEvidence 核验 Spec-lite 与决定的绑定；不从记录反推决定人，也不接受数字人代答。
func (d *dailySession) standardEvidence() error {
	spec, ok := kernel.Object(d.record["spec_lite"])
	if !ok || kernel.Text(spec["ref"]) == "" || kernel.Text(spec["digest"]) == "" {
		return d.info("standard 需要 spec_lite 的 ref 与原字节 digest")
	}
	if _, err := d.basisRef(kernel.Text(spec["ref"]), kernel.Text(spec["digest"])); err != nil {
		return err
	}
	decision, ok := kernel.Object(d.record["standard_decision"])
	if !ok {
		return d.info("standard 需要 standard_decision")
	}
	if kernel.Text(decision["id"]) != standardDecisionID {
		return d.rejected("standard_decision.id 必须是 " + standardDecisionID)
	}
	if kernel.Text(decision["decision"]) != "approved" {
		return d.rejected("standard 决定尚未批准")
	}
	if kernel.Text(decision["actor_kind"]) != "human" || kernel.Text(decision["decided_by"]) == "" {
		return d.rejected("standard 决定必须由登记的生物人作出")
	}
	if kernel.Text(decision["spec_lite_digest"]) != kernel.Text(spec["digest"]) {
		return d.rejected("standard 决定没有绑定当前 Spec-lite 摘要")
	}
	if kernel.Text(decision["response_ref"]) == "" || kernel.Text(decision["response_digest"]) == "" {
		return d.info("standard 决定需要原始回复的 response_ref 与 response_digest")
	}
	if _, err := d.basisRef(kernel.Text(decision["response_ref"]), kernel.Text(decision["response_digest"])); err != nil {
		return err
	}
	if d.s.GovernanceVersion == 2 {
		record, err := d.s.Doc(kernel.Text(decision["response_ref"]))
		if err != nil {
			return err
		}
		result, err := approval.ValidateUserDecisionSemantic(d.s, record, []map[string]any{{"boundary": standardDecisionID, "subject_ref": spec["ref"], "scope": kernel.ApMap(d.record["scope"])["paths"]}})
		if err != nil {
			return err
		}
		rows := result.Rows()
		if len(rows) != 1 || rows[0]["principal_ref"] != decision["decided_by"] {
			return d.rejected("standard 决定人与真实来源不一致")
		}
	}
	d.report.Checks = append(d.report.Checks, kernel.SemanticCheck{ID: "standard.spec-lite-decision", SourceRef: d.taskRef, Status: "passed"})
	return nil
}

// Legacy policy1 requires separate actors; policy2 already validated the actual human source.
func (d *dailySession) standardIndependence(implementer string) error {
	if d.s.GovernanceVersion == 2 {
		return nil
	}
	decision, _ := kernel.Object(d.record["standard_decision"])
	review, _ := kernel.Object(d.record["review"])
	decider := kernel.Text(decision["decided_by"])
	if decider == implementer || decider == kernel.Text(review["reviewer_id"]) {
		return d.rejected("standard 决定人不能是实现者或审查者")
	}
	return nil
}
