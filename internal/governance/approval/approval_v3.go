package approval

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"strings"
)

func init() {
	kernel.RegisterRoleIndependentValidator("approval", func(s *kernel.SemanticSession, ref string, opts map[string]string) error {
		record, err := s.Doc(ref)
		if err != nil {
			return err
		}
		if kernel.ApNumber(record["schema_version"]) != 3 {
			return s.Reject("APPROVAL_VERSION", "新政策只接受approval3/bundle3；历史记录须在原政策只读核验")
		}
		return verifyApprovalSemantic(s, ref, opts)
	})
}

func approvalRule3(s *kernel.SemanticSession, record map[string]any) (map[string]any, error) {
	if s.GovernanceVersion != 2 || kernel.ApNumber(record["schema_version"]) != 3 {
		return nil, s.Reject("APPROVAL_VERSION", "批准与治理政策代际不匹配")
	}
	if err := s.ValidateGovernancePolicyBinding(kernel.ApMap(record["policy"])); err != nil {
		return nil, err
	}
	gate := kernel.Text(record["gate_id"])
	policy := s.GatePolicy()
	var rule map[string]any
	for _, bucket := range []string{"check_reviews", "technical_reviews"} {
		if row := kernel.ApFind(policy[bucket], "gate", gate); row != nil {
			rule = kernel.ApCopy(row)
			rule["bucket"] = bucket
		}
	}
	for _, bucket := range []string{"user_decision_gates", "contract_approval_gates"} {
		if kernel.ApContains(policy[bucket], gate) {
			rule = map[string]any{"bucket": bucket}
		}
	}
	if rule == nil {
		return nil, s.Reject("GATE_POLICY_REQUIRED", "当前Profile未登记批准边界: "+gate)
	}
	if record["decision"] != "approved" {
		return nil, s.Reject("APPROVAL_CURRENT_INVALID", "当前批准尚未通过或已撤回")
	}
	if record["actor_kind"] != "agent" && record["actor_kind"] != "human" {
		return nil, s.Reject("APPROVAL_CURRENT_INVALID", "未知批准来源类型")
	}
	return rule, nil
}

func assertCurrentApproval3(s *kernel.SemanticSession, record, expected map[string]any) error {
	if _, err := approvalRule3(s, record); err != nil {
		return err
	}
	if err := CurrentApprovalReferencesSemantic(s, record); err != nil {
		return err
	}
	if expected["boundary"] != record["gate_id"] || expected["subject_ref"] != record["subject_ref"] || !kernel.ApUnique(expected["approval_scope"]) || !kernel.ApSetEqual(record["approval_scope"], expected["approval_scope"]) || len(kernel.ApRows(expected["basis"])) == 0 {
		return s.Reject("APPROVAL_CURRENT_INVALID", "当前消费主体、范围或依据不匹配")
	}
	candidate := map[string]any{"ref": record["subject_ref"], "digest": record["subject_digest"]}
	if _, err := s.EvidenceAsset(candidate); err != nil {
		return err
	}
	if expected["subject_digest"] != nil && strings.TrimPrefix(kernel.Text(expected["subject_digest"]), "sha256:") != candidate["digest"] {
		return s.Reject("APPROVAL_CURRENT_INVALID", "消费者冻结主体摘要已变化")
	}
	taskRef := kernel.Text(record["review_task_ref"])
	if _, err := s.EvidenceAsset(map[string]any{"ref": taskRef, "digest": record["review_task_digest"]}); err != nil {
		return err
	}
	if consumer, ok := kernel.Object(expected["review_context"]); ok {
		if consumer["review_task_ref"] != record["review_task_ref"] || consumer["review_task_digest"] != record["review_task_digest"] {
			return s.Reject("REVIEW_BINDING_STALE", "批准未绑定消费者当前审查任务")
		}
	} else if expected["review_context"] != nil {
		return s.Reject("APPROVAL_CONTEXT_REQUIRED", "当前审查上下文必须是对象")
	}
	if err := s.Verify("task", taskRef, nil); err != nil {
		return err
	}
	task, err := s.Doc(taskRef)
	if err != nil {
		return err
	}
	verified := kernel.ApMap(s.Report.Coverage["task_execution"])
	review := kernel.ApMap(task["review_context"])
	if task["schema_version"] == nil || kernel.ApNumber(task["schema_version"]) != 3 || verified["task_ref"] != taskRef || verified["completed"] != true || !kernel.ApContains([]string{"Reviewer", "Verifier"}, kernel.Text(task["execution_state"])) {
		return s.Reject("APPROVAL_REVIEW", "需要实际完成且当前的审查任务")
	}
	if !kernel.ApEqual(candidate, review["candidate"]) || !kernel.ApEqual(record["review"], review["review"]) || !kernel.ApSetEqual(record["approval_scope"], review["approval_scope"]) || !kernel.ApSetEqual(record["check_ids"], review["check_ids"]) || !reviewCoversBoundary(s, record["check_ids"], kernel.Text(record["gate_id"])) {
		return s.Reject("APPROVAL_REVIEW", "批准未绑定实际审查的候选、检查和范围")
	}
	if err = approvalBasis3(s, record["basis"], expected["basis"]); err != nil {
		return err
	}
	if err = approvalBasis3(s, review["basis"], record["basis"]); err != nil {
		return err
	}
	if !kernel.ApUnique(record["evidence_refs"]) {
		return s.Reject("APPROVAL_REVIEW", "批准缺少实际审查证据")
	}
	for _, ref := range kernel.ApStrings(record["evidence_refs"]) {
		if ref != kernel.ApMap(task["provenance"])["execution_ref"] && !kernel.ApContains(kernel.ApMap(task["result"])["evidence_refs"], ref) {
			return s.Reject("APPROVAL_REVIEW", "证据不是当前审查执行产出")
		}
		if _, err = s.Bytes(ref); err != nil {
			return err
		}
	}
	if record["actor_kind"] == "human" || kernel.ApContains(s.UserDecisionPolicy()["gates"], kernel.Text(record["gate_id"])) || record["user_decision_ref"] != nil || record["continuation_ref"] != nil {
		requirement := map[string]any{"boundary": record["gate_id"], "subject_ref": record["subject_ref"], "scope": record["approval_scope"], "user_decision_ref": record["user_decision_ref"], "continuation_ref": record["continuation_ref"]}
		if _, err = AssertUserDecisionRequirementSemantic(s, requirement); err != nil {
			return err
		}
	}
	return nil
}

func approvalBasis3(s *kernel.SemanticSession, basis, required any) error {
	if len(kernel.ApRows(basis)) == 0 || len(kernel.ApRows(required)) == 0 {
		return s.Reject("APPROVAL_CURRENT_INVALID", "批准依据不能为空")
	}
	seen := map[string]string{}
	for _, asset := range kernel.ApRows(basis) {
		ref := kernel.Text(asset["ref"])
		if _, ok := seen[ref]; ok {
			return s.Reject("APPROVAL_CURRENT_INVALID", "批准依据重复")
		}
		if _, err := s.EvidenceAsset(asset); err != nil {
			return err
		}
		seen[ref] = kernel.Text(asset["digest"])
	}
	for _, asset := range kernel.ApRows(required) {
		if _, err := s.EvidenceAsset(asset); err != nil {
			return err
		}
		if seen[kernel.Text(asset["ref"])] != asset["digest"] {
			return s.Reject("APPROVAL_CURRENT_INVALID", "批准遗漏当前必需依据")
		}
	}
	return nil
}

func reviewBundleRows3(s *kernel.SemanticSession, bundle map[string]any) ([]map[string]any, error) {
	rows := kernel.ApRows(bundle["reviews"])
	if len(rows) == 0 || s.GovernanceVersion != 2 {
		return nil, s.Reject("APPROVAL_BUNDLE_INVALID", "组合审查为空或政策代际不匹配")
	}
	seen := map[string]bool{}
	for _, row := range rows {
		gate := kernel.Text(row["gate_id"])
		if gate == "" || seen[gate] {
			return nil, s.Reject("APPROVAL_BUNDLE_INVALID", "组合审查边界缺失或重复")
		}
		seen[gate] = true
		for _, key := range []string{"schema_version", "review_task_ref", "review_task_digest", "policy", "review"} {
			if !kernel.ApEqual(row[key], bundle[key]) {
				return nil, s.Reject("APPROVAL_BUNDLE_INVALID", "组合与逐项实际来源不一致: "+key)
			}
		}
		if !kernel.ApSetEqual(row["check_ids"], bundle["check_ids"]) {
			return nil, s.Reject("APPROVAL_BUNDLE_INVALID", "组合检查范围不一致")
		}
		for _, asset := range kernel.ApRows(row["basis"]) {
			found := false
			for _, bound := range kernel.ApRows(bundle["basis"]) {
				found = found || kernel.ApEqual(asset, bound)
			}
			if !found {
				return nil, s.Reject("APPROVAL_BUNDLE_INVALID", "组合未覆盖逐项依据")
			}
		}
	}
	return rows, nil
}

func reviewCoversBoundary(s *kernel.SemanticSession, checks any, boundary string) bool {
	if kernel.ApContains(checks, boundary) {
		return true
	}
	groups := map[string][]string{}
	for _, bucket := range []string{"check_reviews", "technical_reviews", "work_unit_reviews"} {
		for _, row := range kernel.ApRows(s.GatePolicy()[bucket]) {
			id := kernel.Text(row["gate"])
			if bucket == "work_unit_reviews" {
				id = kernel.Text(row["work_unit"])
			}
			groups[id] = kernel.ApStrings(row["capability_ids"])
		}
	}
	required := groups[boundary]
	if len(required) == 0 {
		return false
	}
	selected := map[string]bool{}
	for _, check := range kernel.ApStrings(checks) {
		for _, group := range groups[check] {
			selected[group] = true
		}
	}
	for _, group := range required {
		if !selected[group] {
			return false
		}
	}
	return true
}
