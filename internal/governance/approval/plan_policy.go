package approval

import (
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"strings"
)

const NativePlanProtocol = "bounded-plan-review-v1"

func planPolicy(s *kernel.SemanticSession) (map[string]any, error) {
	contract, ref, err := s.Orchestration()
	if err != nil {
		return nil, err
	}
	p := kernel.SemMap(kernel.SemMap(contract["planning"])["review_control"])
	if p == nil || p["enabled"] != true || p["protocol"] != NativePlanProtocol {
		return nil, s.Unavailable("PLAN_REVIEW_PROTOCOL_REQUIRED", "本地缺少当前 Plan 审查协议")
	}
	ids := kernel.SemStrings(p["professional_check_ids"])
	if len(ids) == 0 || !kernel.ApUnique(p["professional_check_ids"]) {
		return nil, s.Unavailable("PLAN_REVIEW_POLICY_INVALID", "专业检查范围非法")
	}
	boundaries := []string{"check.domain-strategy-approved", "check.stage-decision-package-approved", "gate.plan-approved"}
	for _, id := range ids {
		if !kernel.SemHas(boundaries, id) || id == p["aggregate_gate"] {
			return nil, s.Unavailable("PLAN_REVIEW_POLICY_INVALID", "专业检查边界非法")
		}
	}
	if !kernel.SemHas(boundaries, kernel.Text(p["aggregate_gate"])) || !kernel.ApEqual(p["phase_order"], []any{"initial", "rereview", "exception"}) {
		return nil, s.Unavailable("PLAN_REVIEW_POLICY_INVALID", "聚合边界或审查顺序非法")
	}
	limits := kernel.SemMap(p["limits"])
	r, rok := kernel.Integer(limits["regular"])
	e, eok := kernel.Integer(limits["exception"])
	t, tok := kernel.Integer(limits["total"])
	if !rok || !eok || !tok || r <= 0 || e <= 0 || t <= 0 || r+e != t {
		return nil, s.Unavailable("PLAN_REVIEW_POLICY_INVALID", "本地审查预算非法")
	}
	for _, kind := range []string{"violation", "missing-evidence", "suggestion"} {
		if !kernel.SemHas(kernel.SemStrings(p["finding_kinds"]), kind) {
			return nil, s.Unavailable("PLAN_REVIEW_POLICY_INVALID", "问题类型缺失")
		}
	}
	if _, ok := p["later_finding_origins"].([]any); !ok {
		return nil, s.Unavailable("PLAN_REVIEW_POLICY_INVALID", "缺少后续问题来源规则")
	}
	digest, err := kernel.SemanticDocumentDigest(p)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for k, v := range p {
		out[k] = v
	}
	out["policy_ref"] = ref
	out["policy_digest"] = digest
	return out, nil
}

func PlanControl(s *kernel.SemanticSession, cp map[string]any) (map[string]any, map[string]any, error) {
	p, err := planPolicy(s)
	if err != nil {
		return nil, nil, err
	}
	c := kernel.SemMap(cp["plan_review_control"])
	if c == nil || c["protocol_version"] != p["protocol"] {
		return nil, nil, s.Reject("PLAN_REVIEW_CONTROL_REQUIRED", "需受控初始化或接入审查历史")
	}
	if err = s.ValidateSchema(".template-spec/process/schemas/plan-review-control.schema.json", c); err != nil {
		return nil, nil, err
	}
	if c["feature_id"] != cp["feature_id"] || c["policy_ref"] != p["policy_ref"] || c["policy_digest"] != p["policy_digest"] {
		return nil, nil, s.Reject("PLAN_REVIEW_POLICY_DRIFT", "功能或当前策略摘要不一致")
	}
	attempts := kernel.SemList(c["attempts"])
	ids := map[string]bool{}
	requests := map[string]bool{}
	history, known := kernel.Integer(c["history_count"])
	for i, row := range attempts {
		a := kernel.SemMap(row)
		id, key := kernel.Text(a["attempt_id"]), kernel.Text(a["request_key"])
		if ids[id] || requests[key] || !kernel.IsSHA256(key) || !known || id != fmt.Sprintf("%s.attempt-%d", kernel.Text(c["cycle_id"]), history+int64(i)+1) || i < len(attempts)-1 && a["status"] != "completed" {
			return nil, nil, s.Reject("PLAN_REVIEW_CONTROL_INVALID", "审查身份、序号或请求重复/重排")
		}
		ids[id] = true
		requests[key] = true
		checkIDs := kernel.SemStrings(a["check_ids"])
		if !kernel.ApUnique(a["check_ids"]) {
			return nil, nil, s.Reject("PLAN_REVIEW_CONTROL_INVALID", "专业检查范围为空或重复")
		}
		for _, id := range checkIDs {
			if !kernel.SemHas(kernel.SemStrings(p["professional_check_ids"]), id) {
				return nil, nil, s.Reject("PLAN_REVIEW_CONTROL_INVALID", "审查范围越界")
			}
		}
		if a["status"] == "completed" {
			results := kernel.SemList(a["check_results"])
			seen := map[string]bool{}
			if len(results) != len(checkIDs) || len(checkIDs) == 0 {
				return nil, nil, s.Reject("PLAN_REVIEW_CONTROL_INVALID", "完成尝试缺少逐项结果")
			}
			for _, row := range results {
				v := kernel.SemMap(row)
				id := kernel.Text(v["check_id"])
				if seen[id] || !kernel.SemHas(checkIDs, id) || !kernel.SemHas([]string{"passed", "blocked"}, kernel.Text(v["status"])) || a["outcome"] == "passed" && v["status"] != "passed" {
					return nil, nil, s.Reject("PLAN_REVIEW_CONTROL_INVALID", "逐项结果重复、越界或伪称通过")
				}
				seen[id] = true
			}
		}
	}
	seen := map[string]bool{}
	for _, row := range kernel.SemList(c["findings"]) {
		f := kernel.SemMap(row)
		id := kernel.Text(f["id"])
		if id == "" || seen[id] || !kernel.SemHas(kernel.SemStrings(p["finding_kinds"]), kernel.Text(f["kind"])) || f["kind"] == "suggestion" && f["status"] != "backlog" || f["kind"] != "suggestion" && f["status"] == "backlog" {
			return nil, nil, s.Reject("PLAN_REVIEW_CONTROL_INVALID", "问题身份或分类非法")
		}
		seen[id] = true
	}
	return c, p, nil
}

func PlanBinding(c, a map[string]any) map[string]any {
	return map[string]any{"protocol_version": c["protocol_version"], "checkpoint_ref": a["checkpoint_ref"], "cycle_id": c["cycle_id"], "attempt_id": a["attempt_id"], "phase": a["phase"], "scope_digest": c["scope_digest"], "request_key": a["request_key"], "check_ids": a["check_ids"], "target_finding_ids": a["target_finding_ids"]}
}

func PlanLatest(c map[string]any, id string) map[string]any {
	rows := kernel.SemList(c["attempts"])
	for i := len(rows) - 1; i >= 0; i-- {
		a := kernel.SemMap(rows[i])
		if kernel.SemHas(kernel.SemStrings(a["check_ids"]), id) {
			return a
		}
	}
	return nil
}

func PlanExpected(s *kernel.SemanticSession, id string, check map[string]any, basis []any, control map[string]any) (map[string]any, error) {
	current := map[string]any{}
	for k, v := range check {
		current[k] = v
	}
	filtered := []any{}
	refs := kernel.SemStrings(check["evidence_refs"])
	for _, row := range basis {
		if kernel.SemHas(refs, kernel.Text(kernel.SemMap(row)["ref"])) {
			filtered = append(filtered, row)
		}
	}
	current["basis"] = filtered
	// The registered attempt is the consumer's independent task expectation.
	// A candidate approval/bundle cannot supply its own expected task identity.
	if attempt := PlanLatest(control, id); attempt != nil {
		ref, digest := kernel.Text(attempt["task_ref"]), kernel.Text(attempt["task_digest"])
		if ref == "" || digest == "" {
			return nil, s.Reject("PLAN_REVIEW_TASK_DRIFT", "当前审查尝试缺少任务绑定")
		}
		if err := s.Basis([]any{map[string]any{"ref": ref, "digest": digest}}); err != nil {
			return nil, err
		}
		if declared := kernel.SemMap(check["review_context"]); len(declared) > 0 && (declared["review_task_ref"] != ref || declared["review_task_digest"] != digest) {
			return nil, s.Reject("PLAN_REVIEW_TASK_DRIFT", "检查消费上下文与登记任务不一致")
		}
		if err := s.Verify("plan-review-task", ref, map[string]string{"checkpoint": kernel.Text(attempt["checkpoint_ref"]), "require-completed": "true", "boundary": id}); err != nil {
			return nil, err
		}
		task, err := s.Doc(ref)
		if err != nil {
			return nil, err
		}
		reviewContext := kernel.ApCopy(kernel.SemMap(task["review_context"]))
		reviewContext["review_task_ref"], reviewContext["review_task_digest"] = ref, digest
		current["review_context"] = reviewContext
	}
	return ApprovalExpectationFromState(s, id, current)
}

func PlanEntryControl(s *kernel.SemanticSession, cp, c, p map[string]any, ref string, review map[string]any, basis []any, applicable []string) error {
	history, known := kernel.Integer(c["history_count"])
	attempts := kernel.SemList(c["attempts"])
	open := false
	for _, row := range kernel.SemList(c["findings"]) {
		m := kernel.SemMap(row)
		if m["kind"] != "suggestion" && m["status"] == "open" {
			open = true
		}
	}
	if len(attempts) > 0 {
		last := kernel.SemMap(attempts[len(attempts)-1])
		actual, err := s.Doc(kernel.Text(last["checkpoint_ref"]))
		if err != nil {
			return err
		}
		if actual["feature_id"] != c["feature_id"] || !kernel.ApEqual(actual["plan_review_control"], c) {
			return s.Reject("PLAN_REVIEW_CHECKPOINT_DRIFT", "阶段入口使用了旧控制状态")
		}
		// Entry state may use a transport file; tasks stay bound to this observed owner.
		ref = kernel.Text(last["checkpoint_ref"])
	}
	if len(applicable) == 0 {
		if !known || open || kernel.SemHas([]string{"blocked", "history-unknown", "diagnosis-required"}, kernel.Text(c["status"])) {
			return s.Reject("PLAN_REVIEW_ENTRY_BLOCKED", "无专业影响仍需已知历史且无阻断")
		}
		for _, row := range attempts {
			if kernel.SemMap(row)["status"] != "completed" {
				return s.Reject("PLAN_REVIEW_ENTRY_BLOCKED", "尚有未完成审查")
			}
		}
		return s.Basis(basis)
	}
	limits := kernel.SemMap(p["limits"])
	regular, _ := kernel.Integer(limits["regular"])
	total, _ := kernel.Integer(limits["total"])
	exceptions, _ := kernel.Integer(limits["exception"])
	if kernel.SemMap(c["provenance"])["kind"] == "adopted" && len(attempts) == 0 && history > 0 {
		if c["status"] != "active" || open {
			return s.Reject("PLAN_REVIEW_ENTRY_BLOCKED", "旧结论需当前独立核验")
		}
		if history >= regular {
			diagnosed := false
			for _, row := range kernel.SemList(c["diagnoses"]) {
				if kernel.ApEqual(kernel.SemMap(row)["finding_ids"], []any{"history:" + kernel.Text(c["cycle_id"])}) {
					diagnosed = true
				}
			}
			if !diagnosed {
				return s.Reject("PLAN_REVIEW_ENTRY_BLOCKED", "历史超额须主控诊断")
			}
		}
		for _, id := range applicable {
			var conclusion map[string]any
			for _, row := range kernel.SemList(c["history_conclusions"]) {
				m := kernel.SemMap(row)
				if m["check_id"] == id {
					conclusion = m
				}
			}
			if conclusion == nil {
				return s.Reject("PLAN_REVIEW_ENTRY_BLOCKED", "缺少适用历史逐项原结论")
			}
			proof := append([]any{map[string]any{"ref": conclusion["approval_ref"], "digest": conclusion["approval_digest"]}, map[string]any{"ref": conclusion["subject_ref"], "digest": conclusion["subject_digest"]}}, kernel.SemList(conclusion["basis"])...)
			if err := s.Basis(proof); err != nil {
				return err
			}
			check := kernel.SemMap(kernel.SemMap(review["internal_checks"])[id])
			expected, err := PlanExpected(s, id, check, basis, c)
			if err != nil {
				return err
			}
			record, err := s.Doc(kernel.Text(conclusion["approval_ref"]))
			if err != nil {
				return err
			}
			selected, err := SelectApprovalRecordSemantic(s, record, id)
			if err != nil {
				return err
			}
			if err = AssertCurrentApprovalSemantic(s, selected, expected); err != nil {
				return err
			}
		}
		for _, row := range kernel.SemList(c["diagnoses"]) {
			d := kernel.SemMap(row)
			proof := append([]any{map[string]any{"ref": d["ref"], "digest": d["digest"]}}, kernel.SemList(d["resolution_evidence"])...)
			if err := s.Basis(proof); err != nil {
				return err
			}
		}
		return nil
	}
	if !known || c["status"] != "converged" || open || len(attempts) == 0 {
		return s.Reject("PLAN_REVIEW_ENTRY_BLOCKED", "当前 Plan 周期未收敛")
	}
	last := kernel.SemMap(attempts[len(attempts)-1])
	if last["status"] != "completed" || last["outcome"] != "passed" {
		return s.Reject("PLAN_REVIEW_ENTRY_BLOCKED", "缺少当前专业审查完成证据")
	}
	exceptionCount := 0
	for _, row := range attempts {
		if kernel.SemMap(row)["phase"] == "exception" {
			exceptionCount++
		}
	}
	if history+int64(len(attempts)) > total || history+int64(len(attempts)-exceptionCount) > regular || int64(exceptionCount) > exceptions {
		return s.Reject("PLAN_REVIEW_BUDGET_EXHAUSTED", "审查预算超额")
	}
	proof := append([]any{map[string]any{"ref": last["task_ref"], "digest": last["task_digest"]}, map[string]any{"ref": last["result_ref"], "digest": last["result_digest"]}}, kernel.SemList(last["basis"])...)
	if err := s.Basis(proof); err != nil {
		return err
	}
	result, err := s.Doc(kernel.Text(last["result_ref"]))
	if err != nil {
		return err
	}
	if !kernel.ApEqual(result["plan_review_binding"], PlanBinding(c, last)) || result["outcome"] != "passed" || !kernel.ApEqual(result["check_results"], last["check_results"]) {
		return s.Reject("PLAN_REVIEW_RESULT_MISMATCH", "完成结论与原始结果不一致")
	}
	task, err := s.Doc(kernel.Text(last["task_ref"]))
	if err != nil {
		return err
	}
	subject, err := s.Doc(kernel.Text(kernel.SemMap(task["review_context"])["candidate_ref"]))
	if err != nil {
		return err
	}
	if task["checkpoint_ref"] != last["checkpoint_ref"] || kernel.SemMap(subject["source_checkpoint"])["ref"] != last["checkpoint_ref"] || kernel.Text(kernel.SemMap(subject["source_checkpoint"])["digest"]) == "" || last["candidate_digest"] != kernel.SemMap(subject["source_checkpoint"])["digest"] {
		return s.Reject("PLAN_REVIEW_CANDIDATE_REQUIRED", "任务缺少当前 checkpoint 来源绑定")
	}
	if err = s.Verify("plan-review-task", kernel.Text(last["task_ref"]), map[string]string{"checkpoint": ref, "require-completed": "true"}); err != nil {
		return err
	}
	for _, id := range applicable {
		a := PlanLatest(c, id)
		if a == nil || a["status"] != "completed" {
			return s.Reject("PLAN_REVIEW_ENTRY_BLOCKED", "缺少当前专业逐项结论")
		}
		passed := false
		for _, row := range kernel.SemList(a["check_results"]) {
			m := kernel.SemMap(row)
			if m["check_id"] == id && m["status"] == "passed" {
				passed = true
			}
		}
		if !passed {
			return s.Reject("PLAN_REVIEW_ENTRY_BLOCKED", "逐项结论未通过")
		}
		if err = s.Basis([]any{map[string]any{"ref": a["task_ref"], "digest": a["task_digest"]}, map[string]any{"ref": a["result_ref"], "digest": a["result_digest"]}}); err != nil {
			return err
		}
		r, err := s.Doc(kernel.Text(a["result_ref"]))
		if err != nil {
			return err
		}
		if !kernel.ApEqual(r["plan_review_binding"], PlanBinding(c, a)) || !kernel.ApEqual(r["check_results"], a["check_results"]) {
			return s.Reject("PLAN_REVIEW_RESULT_MISMATCH", "复用检查缺少结果绑定")
		}
		if err = s.Verify("plan-review-task", kernel.Text(a["task_ref"]), map[string]string{"checkpoint": ref, "boundary": id, "require-completed": "true"}); err != nil {
			return err
		}
	}
	return nil
}

func PlanAggregateRecord(s *kernel.SemanticSession, cp, review map[string]any, basis []any, bundles map[string]map[string]any) error {
	approvalRef := kernel.First(kernel.Text(cp["plan_approval_ref"]), kernel.Text(kernel.SemMap(kernel.SemMap(cp["gates"])["gate.plan-approved"])["approval_ref"]))
	if approvalRef == "" {
		return s.Reject("PLAN_APPROVAL_REQUIRED", "缺少独立 Plan 聚合批准")
	}
	for _, ref := range []string{approvalRef, kernel.Text(cp["plan_review_ref"])} {
		if err := kernel.RejectProgressionEvidence(s, ref); err != nil {
			return err
		}
	}
	record, err := s.Doc(approvalRef)
	if err != nil {
		return err
	}
	if err = CurrentApprovalReferencesSemantic(s, record); err != nil {
		return err
	}
	if record["kind"] == "review-bundle" || record["gate_id"] != "gate.plan-approved" {
		return s.Reject("PLAN_APPROVAL_INVALID", "Plan 聚合须使用独立批准记录")
	}
	if err = s.ValidateSchema(".template-spec/process/schemas/approval-record.schema.json", record); err != nil {
		return err
	}
	if _, err = assertApprovalSignerSemantic(s, record); err != nil {
		return err
	}
	bytes, err := s.Bytes(kernel.Text(cp["plan_review_ref"]))
	if err != nil {
		return err
	}
	drafter := kernel.First(kernel.Text(review["drafter_principal_ref"]), kernel.Text(kernel.SemMap(kernel.SemMap(cp["gates"])["gate.plan-approved"])["drafter_principal_ref"]))
	if record["subject_ref"] != cp["plan_review_ref"] || strings.TrimPrefix(kernel.Text(record["subject_digest"]), "sha256:") != safefs.Digest(bytes) || !kernel.ApSetEqual(record["approval_scope"], []any{cp["feature_id"]}) || drafter == "" || record["drafter_principal_ref"] != drafter || record["principal_ref"] == drafter {
		return s.Reject("PLAN_APPROVAL_CONTEXT", "聚合批准未绑定当前主体、范围或独立作者")
	}
	if err = s.Basis(record["basis"]); err != nil {
		return err
	}
	for _, row := range basis {
		m := kernel.SemMap(row)
		covered := false
		for _, r := range kernel.SemList(record["basis"]) {
			b := kernel.SemMap(r)
			if b["ref"] == m["ref"] && strings.TrimPrefix(kernel.Text(b["digest"]), "sha256:") == strings.TrimPrefix(kernel.Text(m["digest"]), "sha256:") {
				covered = true
			}
		}
		if !covered {
			return s.Reject("PLAN_APPROVAL_COVERAGE", "聚合批准未覆盖审阅依据")
		}
	}
	if cp["plan_continuation_ref"] != nil {
		if record["continuation_ref"] != cp["plan_continuation_ref"] {
			return s.Reject("PLAN_DECISION_BINDING", "批准未绑定同一延续")
		}
	} else if record["user_decision_ref"] != cp["plan_user_decision_ref"] {
		return s.Reject("PLAN_DECISION_BINDING", "批准未绑定同一真实用户决定")
	}
	c := kernel.SemMap(cp["plan_review_control"])
	attempts := kernel.SemList(c["attempts"])
	var reused map[string]any
	bundleRef := ""
	if len(attempts) > 0 {
		last := kernel.SemMap(attempts[len(attempts)-1])
		for ref, b := range bundles {
			if kernel.SemMap(b["plan_review_binding"])["attempt_id"] == last["attempt_id"] {
				reused = b
				bundleRef = ref
			}
		}
		if reused == nil || record["review_bundle_ref"] != bundleRef {
			return s.Reject("PLAN_APPROVAL_REUSE", "未复用最终内部检查会话")
		}
	} else {
		hasApproved := false
		for _, row := range kernel.SemMap(review["internal_checks"]) {
			if kernel.SemMap(row)["status"] == "approved" {
				hasApproved = true
			}
		}
		if !hasApproved {
			if record["review_bundle_ref"] != nil || record["review_task_ref"] != nil || record["plan_review_binding"] != nil {
				return s.Reject("PLAN_APPROVAL_REUSE", "全部不适用时不得制造空专业任务")
			}
			return nil
		}
		if kernel.SemMap(c["provenance"])["kind"] != "adopted" {
			return s.Reject("PLAN_APPROVAL_REUSE", "历史结论未受控接入")
		}
		if len(bundles) > 0 {
			reused = bundles[kernel.Text(record["review_bundle_ref"])]
			if reused == nil {
				return s.Reject("PLAN_APPROVAL_REUSE", "未复用原内部审查会话")
			}
		} else {
			for id, row := range kernel.SemMap(review["internal_checks"]) {
				check := kernel.SemMap(row)
				if check["status"] != "approved" {
					continue
				}
				doc, err := s.Doc(kernel.Text(check["approval_ref"]))
				if err != nil {
					return err
				}
				r, err := SelectApprovalRecordSemantic(s, doc, id)
				if err != nil {
					return err
				}
				if r["principal_ref"] == record["principal_ref"] && r["role_id"] == record["role_id"] && r["runtime_id"] == record["runtime_id"] {
					reused = r
					break
				}
			}
			if record["review_bundle_ref"] != nil || reused == nil {
				return s.Reject("PLAN_APPROVAL_REUSE", "旧单项结论不得补造组合审查")
			}
		}
	}
	for _, key := range []string{"role_id", "runtime_id", "principal_ref"} {
		if record[key] != reused[key] {
			return s.Reject("PLAN_APPROVAL_REUSE", "聚合与内部会签身份不一致")
		}
	}
	if (len(attempts) > 0 || reused["review_session_id"] != nil) && record["review_session_id"] != reused["review_session_id"] {
		return s.Reject("PLAN_APPROVAL_REUSE", "会话未复用")
	}
	version, _ := kernel.Integer(record["schema_version"])
	rv, _ := kernel.Integer(reused["schema_version"])
	if len(attempts) > 0 && version == 2 {
		if record["review_task_ref"] != reused["review_task_ref"] || record["review_task_digest"] != reused["review_task_digest"] || !kernel.ApSetEqual(record["capability_ids"], reused["capability_ids"]) || !kernel.ApEqual(record["plan_review_binding"], reused["plan_review_binding"]) {
			return s.Reject("PLAN_APPROVAL_REUSE", "任务、能力或周期绑定未复用")
		}
	} else if rv == 2 || record["review_task_ref"] != nil {
		if record["review_task_ref"] != reused["review_task_ref"] || record["review_task_digest"] != reused["review_task_digest"] {
			return s.Reject("PLAN_APPROVAL_REUSE", "原专业任务未复用")
		}
	}
	return nil
}
