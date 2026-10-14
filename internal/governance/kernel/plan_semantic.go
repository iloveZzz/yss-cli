package kernel

import (
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"strings"
)

const NativePlanProtocol = "bounded-plan-review-v1"

func (s *SemanticSession) planPolicy() (map[string]any, error) {
	contract, ref, err := s.Orchestration()
	if err != nil {
		return nil, err
	}
	p := SemMap(SemMap(contract["planning"])["review_control"])
	if p == nil || p["enabled"] != true || p["protocol"] != NativePlanProtocol {
		return nil, s.Unavailable("PLAN_REVIEW_PROTOCOL_REQUIRED", "本地缺少当前 Plan 审查协议")
	}
	ids := SemStrings(p["professional_check_ids"])
	if len(ids) == 0 || !apUnique(p["professional_check_ids"]) {
		return nil, s.Unavailable("PLAN_REVIEW_POLICY_INVALID", "专业检查范围非法")
	}
	boundaries := []string{"check.domain-strategy-approved", "check.stage-decision-package-approved", "gate.plan-approved"}
	for _, id := range ids {
		if !SemHas(boundaries, id) || id == p["aggregate_gate"] {
			return nil, s.Unavailable("PLAN_REVIEW_POLICY_INVALID", "专业检查边界非法")
		}
	}
	if !SemHas(boundaries, Text(p["aggregate_gate"])) || !ApEqual(p["phase_order"], []any{"initial", "rereview", "exception"}) {
		return nil, s.Unavailable("PLAN_REVIEW_POLICY_INVALID", "聚合边界或审查顺序非法")
	}
	limits := SemMap(p["limits"])
	r, rok := Integer(limits["regular"])
	e, eok := Integer(limits["exception"])
	t, tok := Integer(limits["total"])
	if !rok || !eok || !tok || r <= 0 || e <= 0 || t <= 0 || r+e != t {
		return nil, s.Unavailable("PLAN_REVIEW_POLICY_INVALID", "本地审查预算非法")
	}
	for _, kind := range []string{"violation", "missing-evidence", "suggestion"} {
		if !SemHas(SemStrings(p["finding_kinds"]), kind) {
			return nil, s.Unavailable("PLAN_REVIEW_POLICY_INVALID", "问题类型缺失")
		}
	}
	if _, ok := p["later_finding_origins"].([]any); !ok {
		return nil, s.Unavailable("PLAN_REVIEW_POLICY_INVALID", "缺少后续问题来源规则")
	}
	digest, err := SemanticDocumentDigest(p)
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

func (s *SemanticSession) PlanControl(cp map[string]any) (map[string]any, map[string]any, error) {
	p, err := s.planPolicy()
	if err != nil {
		return nil, nil, err
	}
	c := SemMap(cp["plan_review_control"])
	if c == nil || c["protocol_version"] != p["protocol"] {
		return nil, nil, s.Reject("PLAN_REVIEW_CONTROL_REQUIRED", "需受控初始化或接入审查历史")
	}
	if err = s.ValidateSchema(".template-spec/process/schemas/plan-review-control.schema.json", c); err != nil {
		return nil, nil, err
	}
	if c["feature_id"] != cp["feature_id"] || c["policy_ref"] != p["policy_ref"] || c["policy_digest"] != p["policy_digest"] {
		return nil, nil, s.Reject("PLAN_REVIEW_POLICY_DRIFT", "功能或当前策略摘要不一致")
	}
	attempts := SemList(c["attempts"])
	ids := map[string]bool{}
	requests := map[string]bool{}
	history, known := Integer(c["history_count"])
	for i, row := range attempts {
		a := SemMap(row)
		id, key := Text(a["attempt_id"]), Text(a["request_key"])
		if ids[id] || requests[key] || !IsSHA256(key) || !known || id != fmt.Sprintf("%s.attempt-%d", Text(c["cycle_id"]), history+int64(i)+1) || i < len(attempts)-1 && a["status"] != "completed" {
			return nil, nil, s.Reject("PLAN_REVIEW_CONTROL_INVALID", "审查身份、序号或请求重复/重排")
		}
		ids[id] = true
		requests[key] = true
		checkIDs := SemStrings(a["check_ids"])
		if !apUnique(a["check_ids"]) {
			return nil, nil, s.Reject("PLAN_REVIEW_CONTROL_INVALID", "专业检查范围为空或重复")
		}
		for _, id := range checkIDs {
			if !SemHas(SemStrings(p["professional_check_ids"]), id) {
				return nil, nil, s.Reject("PLAN_REVIEW_CONTROL_INVALID", "审查范围越界")
			}
		}
		if a["status"] == "completed" {
			results := SemList(a["check_results"])
			seen := map[string]bool{}
			if len(results) != len(checkIDs) || len(checkIDs) == 0 {
				return nil, nil, s.Reject("PLAN_REVIEW_CONTROL_INVALID", "完成尝试缺少逐项结果")
			}
			for _, row := range results {
				v := SemMap(row)
				id := Text(v["check_id"])
				if seen[id] || !SemHas(checkIDs, id) || !SemHas([]string{"passed", "blocked"}, Text(v["status"])) || a["outcome"] == "passed" && v["status"] != "passed" {
					return nil, nil, s.Reject("PLAN_REVIEW_CONTROL_INVALID", "逐项结果重复、越界或伪称通过")
				}
				seen[id] = true
			}
		}
	}
	seen := map[string]bool{}
	for _, row := range SemList(c["findings"]) {
		f := SemMap(row)
		id := Text(f["id"])
		if id == "" || seen[id] || !SemHas(SemStrings(p["finding_kinds"]), Text(f["kind"])) || f["kind"] == "suggestion" && f["status"] != "backlog" || f["kind"] != "suggestion" && f["status"] == "backlog" {
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
	rows := SemList(c["attempts"])
	for i := len(rows) - 1; i >= 0; i-- {
		a := SemMap(rows[i])
		if SemHas(SemStrings(a["check_ids"]), id) {
			return a
		}
	}
	return nil
}

func PlanExpected(s *SemanticSession, id string, check map[string]any, basis []any, control map[string]any) (map[string]any, error) {
	current := map[string]any{}
	for k, v := range check {
		current[k] = v
	}
	filtered := []any{}
	refs := SemStrings(check["evidence_refs"])
	for _, row := range basis {
		if SemHas(refs, Text(SemMap(row)["ref"])) {
			filtered = append(filtered, row)
		}
	}
	current["basis"] = filtered
	// The registered attempt is the consumer's independent task expectation.
	// A candidate approval/bundle cannot supply its own expected task identity.
	if attempt := PlanLatest(control, id); attempt != nil {
		ref, digest := Text(attempt["task_ref"]), Text(attempt["task_digest"])
		if ref == "" || digest == "" {
			return nil, s.Reject("PLAN_REVIEW_TASK_DRIFT", "当前审查尝试缺少任务绑定")
		}
		if err := s.Basis([]any{map[string]any{"ref": ref, "digest": digest}}); err != nil {
			return nil, err
		}
		if declared := SemMap(check["review_context"]); len(declared) > 0 && (declared["review_task_ref"] != ref || declared["review_task_digest"] != digest) {
			return nil, s.Reject("PLAN_REVIEW_TASK_DRIFT", "检查消费上下文与登记任务不一致")
		}
		if err := s.Verify("plan-review-task", ref, map[string]string{"checkpoint": Text(attempt["checkpoint_ref"]), "require-completed": "true", "boundary": id}); err != nil {
			return nil, err
		}
		task, err := s.Doc(ref)
		if err != nil {
			return nil, err
		}
		reviewContext := ApCopy(SemMap(task["review_context"]))
		reviewContext["review_task_ref"], reviewContext["review_task_digest"] = ref, digest
		current["review_context"] = reviewContext
	}
	return ApprovalExpectationFromState(s, id, current)
}

func (s *SemanticSession) PlanEntryControl(cp, c, p map[string]any, ref string, review map[string]any, basis []any, applicable []string) error {
	history, known := Integer(c["history_count"])
	attempts := SemList(c["attempts"])
	open := false
	for _, row := range SemList(c["findings"]) {
		m := SemMap(row)
		if m["kind"] != "suggestion" && m["status"] == "open" {
			open = true
		}
	}
	if len(attempts) > 0 {
		last := SemMap(attempts[len(attempts)-1])
		actual, err := s.Doc(Text(last["checkpoint_ref"]))
		if err != nil {
			return err
		}
		if actual["feature_id"] != c["feature_id"] || !ApEqual(actual["plan_review_control"], c) {
			return s.Reject("PLAN_REVIEW_CHECKPOINT_DRIFT", "阶段入口使用了旧控制状态")
		}
	}
	if len(applicable) == 0 {
		if !known || open || SemHas([]string{"blocked", "history-unknown", "diagnosis-required"}, Text(c["status"])) {
			return s.Reject("PLAN_REVIEW_ENTRY_BLOCKED", "无专业影响仍需已知历史且无阻断")
		}
		for _, row := range attempts {
			if SemMap(row)["status"] != "completed" {
				return s.Reject("PLAN_REVIEW_ENTRY_BLOCKED", "尚有未完成审查")
			}
		}
		return s.Basis(basis)
	}
	limits := SemMap(p["limits"])
	regular, _ := Integer(limits["regular"])
	total, _ := Integer(limits["total"])
	exceptions, _ := Integer(limits["exception"])
	if SemMap(c["provenance"])["kind"] == "adopted" && len(attempts) == 0 && history > 0 {
		if c["status"] != "active" || open {
			return s.Reject("PLAN_REVIEW_ENTRY_BLOCKED", "旧结论需当前独立核验")
		}
		if history >= regular {
			diagnosed := false
			for _, row := range SemList(c["diagnoses"]) {
				if ApEqual(SemMap(row)["finding_ids"], []any{"history:" + Text(c["cycle_id"])}) {
					diagnosed = true
				}
			}
			if !diagnosed {
				return s.Reject("PLAN_REVIEW_ENTRY_BLOCKED", "历史超额须主控诊断")
			}
		}
		for _, id := range applicable {
			var conclusion map[string]any
			for _, row := range SemList(c["history_conclusions"]) {
				m := SemMap(row)
				if m["check_id"] == id {
					conclusion = m
				}
			}
			if conclusion == nil {
				return s.Reject("PLAN_REVIEW_ENTRY_BLOCKED", "缺少适用历史逐项原结论")
			}
			proof := append([]any{map[string]any{"ref": conclusion["approval_ref"], "digest": conclusion["approval_digest"]}, map[string]any{"ref": conclusion["subject_ref"], "digest": conclusion["subject_digest"]}}, SemList(conclusion["basis"])...)
			if err := s.Basis(proof); err != nil {
				return err
			}
			check := SemMap(SemMap(review["internal_checks"])[id])
			expected, err := PlanExpected(s, id, check, basis, c)
			if err != nil {
				return err
			}
			record, err := s.Doc(Text(conclusion["approval_ref"]))
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
		for _, row := range SemList(c["diagnoses"]) {
			d := SemMap(row)
			proof := append([]any{map[string]any{"ref": d["ref"], "digest": d["digest"]}}, SemList(d["resolution_evidence"])...)
			if err := s.Basis(proof); err != nil {
				return err
			}
		}
		return nil
	}
	if !known || c["status"] != "converged" || open || len(attempts) == 0 {
		return s.Reject("PLAN_REVIEW_ENTRY_BLOCKED", "当前 Plan 周期未收敛")
	}
	last := SemMap(attempts[len(attempts)-1])
	if last["status"] != "completed" || last["outcome"] != "passed" {
		return s.Reject("PLAN_REVIEW_ENTRY_BLOCKED", "缺少当前专业审查完成证据")
	}
	exceptionCount := 0
	for _, row := range attempts {
		if SemMap(row)["phase"] == "exception" {
			exceptionCount++
		}
	}
	if history+int64(len(attempts)) > total || history+int64(len(attempts)-exceptionCount) > regular || int64(exceptionCount) > exceptions {
		return s.Reject("PLAN_REVIEW_BUDGET_EXHAUSTED", "审查预算超额")
	}
	proof := append([]any{map[string]any{"ref": last["task_ref"], "digest": last["task_digest"]}, map[string]any{"ref": last["result_ref"], "digest": last["result_digest"]}}, SemList(last["basis"])...)
	if err := s.Basis(proof); err != nil {
		return err
	}
	result, err := s.Doc(Text(last["result_ref"]))
	if err != nil {
		return err
	}
	if !ApEqual(result["plan_review_binding"], PlanBinding(c, last)) || result["outcome"] != "passed" || !ApEqual(result["check_results"], last["check_results"]) {
		return s.Reject("PLAN_REVIEW_RESULT_MISMATCH", "完成结论与原始结果不一致")
	}
	task, err := s.Doc(Text(last["task_ref"]))
	if err != nil {
		return err
	}
	subject, err := s.Doc(Text(SemMap(task["review_context"])["candidate_ref"]))
	if err != nil {
		return err
	}
	if task["checkpoint_ref"] != last["checkpoint_ref"] || SemMap(subject["source_checkpoint"])["ref"] != last["checkpoint_ref"] || Text(SemMap(subject["source_checkpoint"])["digest"]) == "" || last["candidate_digest"] != SemMap(subject["source_checkpoint"])["digest"] {
		return s.Reject("PLAN_REVIEW_CANDIDATE_REQUIRED", "任务缺少当前 checkpoint 来源绑定")
	}
	if err = s.Verify("plan-review-task", Text(last["task_ref"]), map[string]string{"checkpoint": ref, "require-completed": "true"}); err != nil {
		return err
	}
	for _, id := range applicable {
		a := PlanLatest(c, id)
		if a == nil || a["status"] != "completed" {
			return s.Reject("PLAN_REVIEW_ENTRY_BLOCKED", "缺少当前专业逐项结论")
		}
		passed := false
		for _, row := range SemList(a["check_results"]) {
			m := SemMap(row)
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
		r, err := s.Doc(Text(a["result_ref"]))
		if err != nil {
			return err
		}
		if !ApEqual(r["plan_review_binding"], PlanBinding(c, a)) || !ApEqual(r["check_results"], a["check_results"]) {
			return s.Reject("PLAN_REVIEW_RESULT_MISMATCH", "复用检查缺少结果绑定")
		}
		if err = s.Verify("plan-review-task", Text(a["task_ref"]), map[string]string{"checkpoint": ref, "boundary": id, "require-completed": "true"}); err != nil {
			return err
		}
	}
	return nil
}

func (s *SemanticSession) PlanAggregateRecord(cp, review map[string]any, basis []any, bundles map[string]map[string]any) error {
	approvalRef := First(Text(cp["plan_approval_ref"]), Text(SemMap(SemMap(cp["gates"])["gate.plan-approved"])["approval_ref"]))
	if approvalRef == "" {
		return s.Reject("PLAN_APPROVAL_REQUIRED", "缺少独立 Plan 聚合批准")
	}
	for _, ref := range []string{approvalRef, Text(cp["plan_review_ref"])} {
		if err := RejectProgressionEvidence(s, ref); err != nil {
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
	bytes, err := s.Bytes(Text(cp["plan_review_ref"]))
	if err != nil {
		return err
	}
	drafter := First(Text(review["drafter_principal_ref"]), Text(SemMap(SemMap(cp["gates"])["gate.plan-approved"])["drafter_principal_ref"]))
	if record["subject_ref"] != cp["plan_review_ref"] || strings.TrimPrefix(Text(record["subject_digest"]), "sha256:") != safefs.Digest(bytes) || !ApSetEqual(record["approval_scope"], []any{cp["feature_id"]}) || drafter == "" || record["drafter_principal_ref"] != drafter || record["principal_ref"] == drafter {
		return s.Reject("PLAN_APPROVAL_CONTEXT", "聚合批准未绑定当前主体、范围或独立作者")
	}
	if err = s.Basis(record["basis"]); err != nil {
		return err
	}
	for _, row := range basis {
		m := SemMap(row)
		covered := false
		for _, r := range SemList(record["basis"]) {
			b := SemMap(r)
			if b["ref"] == m["ref"] && strings.TrimPrefix(Text(b["digest"]), "sha256:") == strings.TrimPrefix(Text(m["digest"]), "sha256:") {
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
	c := SemMap(cp["plan_review_control"])
	attempts := SemList(c["attempts"])
	var reused map[string]any
	bundleRef := ""
	if len(attempts) > 0 {
		last := SemMap(attempts[len(attempts)-1])
		for ref, b := range bundles {
			if SemMap(b["plan_review_binding"])["attempt_id"] == last["attempt_id"] {
				reused = b
				bundleRef = ref
			}
		}
		if reused == nil || record["review_bundle_ref"] != bundleRef {
			return s.Reject("PLAN_APPROVAL_REUSE", "未复用最终内部检查会话")
		}
	} else {
		hasApproved := false
		for _, row := range SemMap(review["internal_checks"]) {
			if SemMap(row)["status"] == "approved" {
				hasApproved = true
			}
		}
		if !hasApproved {
			if record["review_bundle_ref"] != nil || record["review_task_ref"] != nil || record["plan_review_binding"] != nil {
				return s.Reject("PLAN_APPROVAL_REUSE", "全部不适用时不得制造空专业任务")
			}
			return nil
		}
		if SemMap(c["provenance"])["kind"] != "adopted" {
			return s.Reject("PLAN_APPROVAL_REUSE", "历史结论未受控接入")
		}
		if len(bundles) > 0 {
			reused = bundles[Text(record["review_bundle_ref"])]
			if reused == nil {
				return s.Reject("PLAN_APPROVAL_REUSE", "未复用原内部审查会话")
			}
		} else {
			for id, row := range SemMap(review["internal_checks"]) {
				check := SemMap(row)
				if check["status"] != "approved" {
					continue
				}
				doc, err := s.Doc(Text(check["approval_ref"]))
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
	version, _ := Integer(record["schema_version"])
	rv, _ := Integer(reused["schema_version"])
	if len(attempts) > 0 && version == 2 {
		if record["review_task_ref"] != reused["review_task_ref"] || record["review_task_digest"] != reused["review_task_digest"] || !ApSetEqual(record["capability_ids"], reused["capability_ids"]) || !ApEqual(record["plan_review_binding"], reused["plan_review_binding"]) {
			return s.Reject("PLAN_APPROVAL_REUSE", "任务、能力或周期绑定未复用")
		}
	} else if rv == 2 || record["review_task_ref"] != nil {
		if record["review_task_ref"] != reused["review_task_ref"] || record["review_task_digest"] != reused["review_task_digest"] {
			return s.Reject("PLAN_APPROVAL_REUSE", "原专业任务未复用")
		}
	}
	return nil
}
