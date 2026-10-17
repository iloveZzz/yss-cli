package approval

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"strings"
)

func approvalRule(s *kernel.SemanticSession, boundary string) (map[string]any, error) {
	p := kernel.ApMap(s.Roles["gate_policy"])
	for _, bucket := range []string{"check_reviews", "dual_digital_human", "digital_human_review"} {
		if row := kernel.ApFind(p[bucket], "gate", boundary); row != nil {
			rule := kernel.ApCopy(row)
			rule["bucket"] = bucket
			return rule, nil
		}
	}
	if kernel.ApContains(p["product_digital_human_with_biological_veto"], boundary) {
		return map[string]any{"bucket": "product_digital_human_with_biological_veto", "countersigners": []string{"role.product-manager"}}, nil
	}
	if kernel.ApContains(p["biological_human"], boundary) {
		return map[string]any{"bucket": "biological_human", "countersigners": []string{"role.biological-human"}}, nil
	}
	for _, bucket := range []string{"evidence_only", "orchestrator", "automatic_checks"} {
		if kernel.ApContains(p[bucket], boundary) {
			return nil, nil
		}
	}
	return nil, kernel.ApFail(s, "GATE_POLICY_REQUIRED", "未分类门禁或检查: "+boundary)
}

func assertApprovalSignerSemantic(s *kernel.SemanticSession, record map[string]any) (map[string]any, error) {
	if e := s.ValidateSchema(kernel.ApprovalSchemaRef, record); e != nil {
		return nil, e
	}
	gate := kernel.ApText(record["gate_id"])
	if kernel.ApContains(kernel.ApMap(s.Registry["id_policy"])["deprecated_ids"], gate) {
		return nil, kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "未知或已退役门禁: "+gate)
	}
	if kernel.ApFind(s.Roles["runtimes"], "id", kernel.ApText(record["runtime_id"])) == nil {
		return nil, kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "未知 runtime_id")
	}
	if kernel.ApText(record["actor_kind"]) == "orchestrator" {
		return nil, kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "编排器不能关闭会签门禁")
	}
	rule, e := approvalRule(s, gate)
	if e != nil {
		return nil, e
	}
	if record["continuation_ref"] != nil && kernel.ApText(record["actor_kind"]) == "digital-human" {
		reviewers := kernel.ApMap(kernel.ApMap(s.Roles["gate_policy"])["continuation_reviews"])[gate]
		if reviewers != nil {
			rule = map[string]any{"bucket": "digital_human_review", "countersigners": reviewers}
		}
	}
	// 三种拒绝原因分开说明，措辞与 Node 一致，调用方与反例按原因断言。
	if rule == nil {
		return nil, kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", gate+" 不是会签门禁；evidence_only / orchestrator 门禁不写 approval-record")
	}
	if kernel.ApText(record["decision"]) != "approved" {
		return nil, kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", gate+" 会签 decision 必须为 approved 才能关闭门禁")
	}
	if record["biological_veto"] == true {
		return nil, kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", gate+" 已被生物人否决，不能标为 approved")
	}
	if kernel.ApText(rule["bucket"]) == "biological_human" {
		if kernel.ApText(record["actor_kind"]) != "biological-human" || kernel.ApText(record["role_id"]) != "role.biological-human" {
			return nil, kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "必须由生物人会签")
		}
	} else {
		if kernel.ApText(record["actor_kind"]) != "digital-human" {
			return nil, kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "必须由数字人会签")
		}
		if !(kernel.ApNumber(record["schema_version"]) == 2 && len(kernel.ApArray(rule["capability_ids"])) > 0) && !kernel.ApContains(rule["countersigners"], kernel.ApText(record["role_id"])) {
			return nil, kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "会签角色未获当前门禁政策授权")
		}
		drafter := kernel.ApText(rule["drafter"])
		if kernel.ApNumber(record["schema_version"]) == 1 && drafter != "" && (kernel.ApText(record["role_id"]) == drafter || kernel.ApContains(record["countersigner_role_ids"], drafter)) {
			return nil, kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "起草角色不得会签自己")
		}
		if drafter != "" && kernel.ApText(record["drafter_role_id"]) != "" && kernel.ApText(record["drafter_role_id"]) != drafter {
			return nil, kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "drafter_role_id 与门禁政策不符")
		}
	}
	return rule, nil
}

func ApprovalExpectationFromState(s *kernel.SemanticSession, boundary string, state map[string]any) (map[string]any, error) {
	return ApprovalExpectationFromStateWith(s, boundary, state, nil)
}

// reviewPackage 非空时覆盖状态里的 review_package（对应调用方上下文显式指定）。
func ApprovalExpectationFromStateWith(s *kernel.SemanticSession, boundary string, state map[string]any, reviewPackage any) (map[string]any, error) {
	ref := kernel.ApText(state["subject_ref"])
	for _, ref := range []string{ref, kernel.ApText(state["approval_ref"])} {
		if err := kernel.RejectProgressionEvidence(s, ref); err != nil {
			return nil, err
		}
	}
	if ref == "" || !kernel.ApUnique(state["approval_scope"]) {
		return nil, kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "当前消费边界缺少主体或批准范围")
	}
	b, e := s.Bytes(ref)
	if e != nil {
		return nil, e
	}
	digest := state["subject_digest"]
	if !kernel.ApTruthy(digest) {
		for _, x := range kernel.ApRows(state["basis"]) {
			if kernel.ApText(x["ref"]) == ref {
				digest = x["digest"]
				break
			}
		}
	}
	if !kernel.ApTruthy(digest) {
		digest = safefs.Digest(b)
	}
	basis := []map[string]any{}
	for _, x := range kernel.ApRows(state["basis"]) {
		if kernel.ApText(x["ref"]) != ref && kernel.ApText(x["ref"]) != kernel.ApText(state["approval_ref"]) {
			basis = append(basis, x)
		}
	}
	drafter := state["drafter_principal_ref"]
	if !kernel.ApTruthy(drafter) {
		subject, _ := s.Doc(ref)
		drafter = subject["drafter_principal_ref"]
	}
	rp := any(strings.HasPrefix(boundary, "gate."))
	if state["review_package"] != nil {
		rp = state["review_package"]
	}
	if reviewPackage != nil {
		rp = reviewPackage
	}
	expected := map[string]any{"boundary": boundary, "subject_ref": ref, "subject_digest": digest, "approval_scope": state["approval_scope"], "basis": basis, "drafter_principal_ref": drafter, "review_package": rp, "implementer_principal_ref": state["implementer_principal_ref"], "subject_id": state["subject_id"], "subject_version": state["subject_version"]}
	if context, present := state["review_context"]; present {
		expected["review_context"] = context
	}
	return expected, nil
}

func ReviewBundleRowsSemantic(s *kernel.SemanticSession, bundle map[string]any) ([]map[string]any, error) {
	if kernel.ApNumber(bundle["schema_version"]) == 3 {
		return reviewBundleRows3(s, bundle)
	}
	version := kernel.ApNumber(bundle["schema_version"])
	rows := kernel.ApRows(bundle["reviews"])
	if (version != 1 && version != 2) || kernel.ApText(bundle["kind"]) != "review-bundle" || len(rows) == 0 {
		return nil, kernel.ApFail(s, "APPROVAL_BUNDLE_INVALID", "组合审查身份无效或为空")
	}
	seen := map[string]bool{}
	for _, row := range rows {
		gate := kernel.ApText(row["gate_id"])
		if gate == "" || seen[gate] {
			return nil, kernel.ApFail(s, "APPROVAL_BUNDLE_INVALID", "组合审查 gate_id 缺失或重复")
		}
		seen[gate] = true
		for _, field := range []string{"schema_version", "role_id", "runtime_id", "principal_ref"} {
			if !kernel.ApEqual(row[field], bundle[field]) {
				return nil, kernel.ApFail(s, "APPROVAL_BUNDLE_INVALID", "组合审查身份不同: "+field)
			}
		}
		if row["review_session_id"] != nil && !kernel.ApEqual(row["review_session_id"], bundle["review_session_id"]) {
			return nil, kernel.ApFail(s, "APPROVAL_BUNDLE_INVALID", "review_session_id 不一致")
		}
		if version == 2 {
			for _, field := range []string{"review_task_ref", "review_task_digest"} {
				if !kernel.ApEqual(row[field], bundle[field]) {
					return nil, kernel.ApFail(s, "APPROVAL_BUNDLE_INVALID", field+" 不一致")
				}
			}
			if !kernel.ApSetEqual(row["capability_ids"], bundle["capability_ids"]) {
				return nil, kernel.ApFail(s, "APPROVAL_BUNDLE_INVALID", "capability_ids 不一致")
			}
			for _, asset := range kernel.ApRows(row["basis"]) {
				found := false
				for _, bound := range kernel.ApRows(bundle["basis"]) {
					if kernel.ApEqual(asset, bound) {
						found = true
					}
				}
				if !found {
					return nil, kernel.ApFail(s, "APPROVAL_BUNDLE_INVALID", "组合依据没有覆盖逐项 basis")
				}
			}
		}
	}
	return rows, nil
}

func SelectApprovalRecordSemantic(s *kernel.SemanticSession, record map[string]any, gate string) (map[string]any, error) {
	if kernel.ApText(record["kind"]) != "review-bundle" {
		return record, nil
	}
	rows, e := ReviewBundleRowsSemantic(s, record)
	if e != nil {
		return nil, e
	}
	row := kernel.ApFind(rows, "gate_id", gate)
	if row == nil {
		return nil, kernel.ApFail(s, "APPROVAL_BUNDLE_INVALID", "组合审查缺少明确当前边界")
	}
	out := kernel.ApCopy(row)
	for _, pair := range [][2]string{{"review_bundle_id", "bundle_id"}, {"review_task_id", "task_id"}, {"review_work_unit_id", "work_unit_id"}, {"review_session_id", "review_session_id"}} {
		out[pair[0]] = record[pair[1]]
	}
	if kernel.ApNumber(record["schema_version"]) == 2 {
		out["review_bundle_basis"] = record["basis"]
	}
	if record["plan_review_binding"] != nil {
		out["plan_review_binding"] = record["plan_review_binding"]
	}
	return out, nil
}

// The current consumer excludes intent from its existing reference fields. Pure
// historical schema inspection deliberately does not call this proof boundary.
func CurrentApprovalReferencesSemantic(s *kernel.SemanticSession, record map[string]any) error {
	refs := []string{kernel.ApText(record["subject_ref"]), kernel.ApText(record["review_task_ref"]), kernel.ApText(record["continuation_ref"])}
	refs = append(refs, kernel.SemStrings(record["evidence_refs"])...)
	for _, field := range []string{"basis", "review_bundle_basis"} {
		for _, asset := range kernel.ApRows(record[field]) {
			refs = append(refs, kernel.ApText(asset["ref"]))
		}
	}
	for _, ref := range refs {
		if err := kernel.RejectProgressionEvidence(s, ref); err != nil {
			return err
		}
	}
	return nil
}

func AssertCurrentApprovalSemantic(s *kernel.SemanticSession, record, expected map[string]any) error {
	if kernel.ApNumber(record["schema_version"]) == 3 {
		return assertCurrentApproval3(s, record, expected)
	}
	if err := CurrentApprovalReferencesSemantic(s, record); err != nil {
		return err
	}
	if err := kernel.RejectProgressionEvidence(s, kernel.ApText(expected["subject_ref"])); err != nil {
		return err
	}
	_, e := assertApprovalSignerSemantic(s, record)
	if e != nil {
		return e
	}
	gate := kernel.ApText(expected["boundary"])
	if gate == "" || kernel.ApText(expected["subject_ref"]) == "" || !kernel.ApUnique(expected["approval_scope"]) || len(kernel.ApArray(expected["basis"])) == 0 {
		return kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "当前批准缺少独立边界、范围及依据")
	}
	if kernel.ApNumber(record["schema_version"]) == 2 {
		rawReview, reviewPresent := expected["review_context"]
		consumerReview, reviewObject := kernel.Object(rawReview)
		if reviewPresent && !reviewObject {
			return kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "显式 review_context 必须是当前审查上下文对象")
		}
		taskRef := kernel.ApText(consumerReview["review_task_ref"])
		taskDigest := kernel.ApText(consumerReview["review_task_digest"])
		if !reviewPresent {
			// A legacy state consumer supplies the current subject/scope/basis
			// independently, but not the task locator. The record may locate a
			// task; only its fully validated current issuance supplies context.
			taskRef = kernel.ApText(record["review_task_ref"])
			taskDigest = kernel.ApText(record["review_task_digest"])
		}
		if taskRef == "" || !kernel.ApHashValid(taskDigest, false) {
			return kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "v2 批准须由消费者独立指定当前正式审查任务和摘要")
		}
		if record["review_task_ref"] != taskRef || record["review_task_digest"] != taskDigest {
			return kernel.ApFail(s, "REVIEW_BINDING_STALE", "会签未绑定消费者当前审查任务")
		}
		task, e := s.Doc(taskRef)
		if e != nil {
			return e
		}
		issued, e := kernel.ApprovalExpectedFromTaskSemantic(s, taskRef, task, gate, taskDigest, consumerReview)
		if e != nil {
			return e
		}
		if kernel.ApText(record["review_task_id"]) != "" && record["review_task_id"] != task["task_id"] || kernel.ApText(record["review_work_unit_id"]) != "" && record["review_work_unit_id"] != task["work_unit_id"] {
			return kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "组合包与正式任务身份不匹配")
		}
		if record["review_bundle_basis"] != nil {
			bundle := kernel.ApRows(record["review_bundle_basis"])
			checked := bundle
			if record["plan_review_binding"] != nil {
				checked = nil
				for _, b := range bundle {
					if kernel.ApCovers(kernel.ApRows(issued["basis"]), b) {
						checked = append(checked, b)
					}
				}
			}
			if _, e = kernel.ApBasis(s, checked, "组合包", "APPROVAL_CURRENT_INVALID"); e != nil {
				return e
			}
			taskBasis := kernel.ApRows(kernel.ApMap(task["review_context"])["basis"])
			if len(bundle) != len(taskBasis) {
				return kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "组合包依据数量与正式任务不匹配")
			}
			for _, b := range bundle {
				if !kernel.ApCovers(taskBasis, b) {
					return kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "组合包依据与正式任务不匹配")
				}
			}
		}
		if issued["subject_ref"] != expected["subject_ref"] || !kernel.ApSetEqual(issued["approval_scope"], expected["approval_scope"]) {
			return kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "当前消费上下文与正式任务范围不匹配")
		}
		for _, b := range kernel.ApRows(expected["basis"]) {
			if !kernel.ApCovers(kernel.ApRows(issued["basis"]), b) {
				return kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "消费上下文依据不属于正式任务")
			}
		}
		if kernel.ApText(expected["drafter_principal_ref"]) != "" && expected["drafter_principal_ref"] != issued["drafter_principal_ref"] {
			return kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "正式任务作者与消费上下文不匹配")
		}
		expected = kernel.ApCopy(expected)
		expected["drafter_principal_ref"] = issued["drafter_principal_ref"]
		if !reviewPresent {
			expected["review_context"] = issued["review_context"]
		}
	}
	drafter := kernel.ApText(expected["drafter_principal_ref"])
	if strings.TrimSpace(drafter) == "" {
		return kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "当前消费上下文缺少独立起草者来源")
	}
	if record["gate_id"] != expected["boundary"] || record["subject_ref"] != expected["subject_ref"] || !kernel.ApSetEqual(record["approval_scope"], expected["approval_scope"]) {
		return kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "审查对象或范围不匹配")
	}
	if strings.TrimSpace(kernel.ApText(record["drafter_principal_ref"])) == "" || record["drafter_principal_ref"] == record["principal_ref"] || record["drafter_principal_ref"] != drafter || kernel.ApTruthy(expected["implementer_principal_ref"]) && record["principal_ref"] == expected["implementer_principal_ref"] {
		return kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "独立作者/实施者/审查身份不匹配")
	}
	b, e := s.Bytes(kernel.ApText(expected["subject_ref"]))
	if e != nil {
		return e
	}
	if !kernel.ApHashValid(record["subject_digest"], true) || safefs.Digest(b) != kernel.ApHex(record["subject_digest"]) || kernel.ApTruthy(expected["subject_digest"]) && safefs.Digest(b) != kernel.ApHex(expected["subject_digest"]) {
		return kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "审查主体或消费边界摘要过期")
	}
	subject, _ := s.Doc(kernel.ApText(expected["subject_ref"]))
	for _, pair := range [][3]string{{"subject_id", "id", "contract_id"}, {"subject_version", "version", "contract_version"}} {
		if kernel.ApTruthy(expected[pair[0]]) && expected[pair[0]] != subject[pair[1]] && expected[pair[0]] != subject[pair[2]] {
			return kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "主体ID或版本不匹配")
		}
	}
	reviewPackage := strings.HasPrefix(gate, "gate.")
	if expected["review_package"] != nil {
		reviewPackage = expected["review_package"] == true
	}
	if reviewPackage && (subject["gate_id"] != gate || len(kernel.ApArray(subject["basis"])) == 0) {
		return kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "审阅包身份或依据缺失")
	}
	required, e := kernel.ApBasis(s, expected["basis"], "当前消费上下文", "APPROVAL_CURRENT_INVALID")
	if e != nil {
		return e
	}
	recordBasis := record["basis"]
	if recordBasis == nil {
		recordBasis = subject["basis"]
	}
	bound, e := kernel.ApBasis(s, recordBasis, "批准记录", "APPROVAL_CURRENT_INVALID")
	if e != nil {
		return e
	}
	for _, b := range required {
		// Both sets have already passed current-byte validation. Only this
		// consumer coverage check accepts equivalent spellings in one view;
		// frozen task and signed-record identities keep their exact references.
		view, ref, err := s.ReferenceView(kernel.ApText(b["ref"]))
		if err != nil {
			return err
		}
		covered := false
		for _, candidate := range bound {
			boundView, boundRef, err := s.ReferenceView(kernel.ApText(candidate["ref"]))
			if err != nil {
				return err
			}
			covered = covered || view == boundView && ref == boundRef && kernel.ApHex(b["digest"]) == kernel.ApHex(candidate["digest"])
		}
		if !covered {
			return kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "批准范围未覆盖证据: "+kernel.ApText(b["ref"]))
		}
	}
	if kernel.ApNumber(record["schema_version"]) == 2 {
		if e = kernel.AssertReviewCapabilityBindingSemantic(s, record, kernel.ApMap(expected["review_context"])); e != nil {
			return e
		}
	}
	if kernel.ApContains(kernel.ApMap(s.Roles["user_decision_policy"])["gates"], gate) {
		result, e := assertApprovalUserDecisionSemantic(s, record)
		if e != nil {
			return e
		}
		if kernel.ApText(record["actor_kind"]) == "biological-human" {
			for _, v := range result.validated {
				if v.principal != kernel.ApText(record["principal_ref"]) {
					return kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "user-decision-responder-mismatch: 生物人会签者与原始回复者不一致")
				}
			}
		}
	}
	return nil
}

// 由当前主体资产（结构化审阅包）直接给出期望：范围与依据取自主体本身，对应 Node 的 approvalExpectationFromSubject。
func ApprovalExpectationFromSubject(s *kernel.SemanticSession, boundary, ref string, reviewPackage any) (map[string]any, error) {
	if err := kernel.RejectProgressionEvidence(s, ref); err != nil {
		return nil, err
	}
	b, e := s.Bytes(ref)
	if e != nil {
		return nil, e
	}
	subject, e := s.Doc(ref)
	if e != nil {
		return nil, e
	}
	scope := subject["approval_scope"]
	if scope == nil {
		scope = subject["scope"]
	}
	if !kernel.ApUnique(scope) || len(kernel.ApArray(subject["basis"])) == 0 {
		return nil, kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "当前主体缺少独立范围及依据；请提供 checkpoint/task 消费上下文")
	}
	rp := any(strings.HasPrefix(boundary, "gate."))
	if reviewPackage != nil {
		rp = reviewPackage
	}
	return map[string]any{"boundary": boundary, "subject_ref": ref, "subject_digest": safefs.Digest(b), "approval_scope": scope, "basis": subject["basis"], "drafter_principal_ref": subject["drafter_principal_ref"], "review_package": rp}, nil
}

// 消费方当前绑定选择审查主体，对应 Node 的 approvalExpectationForBoundAsset：
// binding 为资产路径，或 {ref, digest, approval_context}；有独立批准上下文时按状态算，否则按主体本身算。
func ApprovalExpectationForBoundAsset(s *kernel.SemanticSession, boundary string, binding any, scope []string, reviewPackage any) (map[string]any, error) {
	assetRef, digest, context := "", "", map[string]any(nil)
	if text, ok := binding.(string); ok {
		assetRef = text
	} else if m := kernel.ApMap(binding); m != nil {
		assetRef, digest, context = kernel.ApText(m["ref"]), kernel.ApText(m["digest"]), kernel.ApMap(m["approval_context"])
	}
	if assetRef == "" {
		return nil, kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "消费方缺少当前资产引用")
	}
	b, e := s.Bytes(assetRef)
	if e != nil {
		return nil, e
	}
	if digest != "" && kernel.ApHex(digest) != safefs.Digest(b) {
		return nil, kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "消费方当前资产摘要过期")
	}
	asset, _ := s.Doc(assetRef)
	if context == nil && asset != nil {
		context = kernel.ApMap(asset["approval_context"])
	}
	if context == nil && asset == nil {
		return nil, kernel.ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "原始资产缺少消费方独立批准上下文")
	}
	var expected map[string]any
	if context != nil {
		expected, e = ApprovalExpectationFromStateWith(s, boundary, context, reviewPackage)
	} else {
		expected, e = ApprovalExpectationFromSubject(s, boundary, assetRef, reviewPackage)
	}
	if e != nil {
		return nil, e
	}
	if assetRef != kernel.ApText(expected["subject_ref"]) {
		var frozen map[string]any
		for _, item := range kernel.ApRows(expected["basis"]) {
			if kernel.ApText(item["ref"]) == assetRef {
				frozen = item
			}
		}
		if frozen != nil && kernel.ApHex(frozen["digest"]) != safefs.Digest(b) {
			return nil, kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "当前资产批准上下文摘要过期")
		}
		if frozen == nil {
			rows := append([]any{}, kernel.ApArray(expected["basis"])...)
			expected["basis"] = append(rows, map[string]any{"ref": assetRef, "digest": safefs.Digest(b)})
		}
	}
	if scope != nil && !kernel.ApSetEqual(stringsToAny(scope), expected["approval_scope"]) {
		return nil, kernel.ApFail(s, "APPROVAL_CURRENT_INVALID", "当前资产消费范围与审阅包不匹配")
	}
	if reviewPackage != nil {
		expected["review_package"] = reviewPackage
	}
	return expected, nil
}

func stringsToAny(values []string) []any {
	out := make([]any, 0, len(values))
	for _, v := range values {
		out = append(out, v)
	}
	return out
}

// 已通过当前批准校验的记录所适用的会签规则（桶、会签角色等），供调用方了解它按哪类门禁关闭。
func ApprovalRuleSemantic(s *kernel.SemanticSession, record map[string]any) (map[string]any, error) {
	if kernel.ApNumber(record["schema_version"]) == 3 {
		return approvalRule3(s, record)
	}
	return assertApprovalSignerSemantic(s, record)
}
