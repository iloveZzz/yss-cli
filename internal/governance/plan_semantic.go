package governance

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

const nativePlanProtocol = "bounded-plan-review-v1"

func init() {
	registerSemanticValidator("plan-checkpoint", verifyPlanCheckpointSemantic)
	registerSemanticValidator("plan-review-task", verifyPlanReviewTaskSemantic)
	registerSemanticValidator("plan-spec-entry", verifyPlanEntrySemantic)
	registerSemanticValidator("plan-aggregate", verifyPlanAggregateSemantic)
	registerSemanticValidator("source-plan-approval", verifySourcePlanApprovalSemantic)
}

func verifySourcePlanApprovalSemantic(s *semanticSession, ref string, opts map[string]string) error {
	checkpoint, err := sourcePlanApprovalCheckpoint(s, ref, opts)
	if err != nil {
		return err
	}
	cp, err := s.doc(checkpoint)
	if err != nil {
		return err
	}
	if opts["asset-ref"] != "" && cp["plan_review_ref"] != opts["asset-ref"] {
		return s.reject("HANDOFF_PLAN_OWNER_REQUIRED", "来源 checkpoint 的 Plan 主体与待消费资产矛盾")
	}
	if err = s.validateSchema(".template-spec/process/schemas/lifecycle-checkpoint.schema.json", cp); err != nil {
		return err
	}
	if first(text(cp["plan_approval_ref"]), text(semMap(semMap(cp["gates"])["gate.plan-approved"])["approval_ref"])) != ref {
		return s.reject("HANDOFF_PLAN_OWNER_REQUIRED", "来源 checkpoint 未持有该 Plan 聚合批准")
	}
	return s.verify("plan-aggregate", checkpoint, map[string]string{"approval": ref})
}

func sourcePlanApprovalCheckpoint(s *semanticSession, ref string, opts map[string]string) (string, error) {
	checkpoint := ""
	if opts["task-ref"] != "" {
		task, err := s.doc(opts["task-ref"])
		if err != nil {
			return "", err
		}
		checkpoint = text(task["checkpoint_ref"])
	}
	if checkpoint == "" {
		candidates := []string{}
		for original := range s.aliases {
			if !semHas([]string{".json", ".yaml", ".yml"}, filepath.Ext(original)) {
				continue
			}
			value, err := s.doc(original)
			if err != nil {
				continue
			}
			if value["gate_id"] != nil || value["gates"] == nil {
				continue
			}
			if text(semMap(semMap(value["gates"])["gate.plan-approved"])["approval_ref"]) == ref {
				candidates = append(candidates, original)
			}
		}
		if len(candidates) != 1 {
			return "", s.reject("HANDOFF_PLAN_OWNER_REQUIRED", "来源 Plan 批准缺少唯一已导出的当前 checkpoint")
		}
		checkpoint = candidates[0]
	}
	return checkpoint, nil
}
func (s *semanticSession) planPolicy() (map[string]any, error) {
	contract, ref, err := s.orchestration()
	if err != nil {
		return nil, err
	}
	p := semMap(semMap(contract["planning"])["review_control"])
	if p == nil || p["enabled"] != true || p["protocol"] != nativePlanProtocol {
		return nil, s.unavailable("PLAN_REVIEW_PROTOCOL_REQUIRED", "本地缺少当前 Plan 审查协议")
	}
	ids := semStrings(p["professional_check_ids"])
	if len(ids) == 0 || !apUnique(p["professional_check_ids"]) {
		return nil, s.unavailable("PLAN_REVIEW_POLICY_INVALID", "专业检查范围非法")
	}
	boundaries := []string{"check.domain-strategy-approved", "check.stage-decision-package-approved", "gate.plan-approved"}
	for _, id := range ids {
		if !semHas(boundaries, id) || id == p["aggregate_gate"] {
			return nil, s.unavailable("PLAN_REVIEW_POLICY_INVALID", "专业检查边界非法")
		}
	}
	if !semHas(boundaries, text(p["aggregate_gate"])) || !apEqual(p["phase_order"], []any{"initial", "rereview", "exception"}) {
		return nil, s.unavailable("PLAN_REVIEW_POLICY_INVALID", "聚合边界或审查顺序非法")
	}
	limits := semMap(p["limits"])
	r, rok := integer(limits["regular"])
	e, eok := integer(limits["exception"])
	t, tok := integer(limits["total"])
	if !rok || !eok || !tok || r <= 0 || e <= 0 || t <= 0 || r+e != t {
		return nil, s.unavailable("PLAN_REVIEW_POLICY_INVALID", "本地审查预算非法")
	}
	for _, kind := range []string{"violation", "missing-evidence", "suggestion"} {
		if !semHas(semStrings(p["finding_kinds"]), kind) {
			return nil, s.unavailable("PLAN_REVIEW_POLICY_INVALID", "问题类型缺失")
		}
	}
	if _, ok := p["later_finding_origins"].([]any); !ok {
		return nil, s.unavailable("PLAN_REVIEW_POLICY_INVALID", "缺少后续问题来源规则")
	}
	digest, err := semanticDocumentDigest(p)
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
func (s *semanticSession) planControl(cp map[string]any) (map[string]any, map[string]any, error) {
	p, err := s.planPolicy()
	if err != nil {
		return nil, nil, err
	}
	c := semMap(cp["plan_review_control"])
	if c == nil || c["protocol_version"] != p["protocol"] {
		return nil, nil, s.reject("PLAN_REVIEW_CONTROL_REQUIRED", "需受控初始化或接入审查历史")
	}
	if err = s.validateSchema(".template-spec/process/schemas/plan-review-control.schema.json", c); err != nil {
		return nil, nil, err
	}
	if c["feature_id"] != cp["feature_id"] || c["policy_ref"] != p["policy_ref"] || c["policy_digest"] != p["policy_digest"] {
		return nil, nil, s.reject("PLAN_REVIEW_POLICY_DRIFT", "功能或当前策略摘要不一致")
	}
	attempts := semList(c["attempts"])
	ids := map[string]bool{}
	requests := map[string]bool{}
	history, known := integer(c["history_count"])
	for i, row := range attempts {
		a := semMap(row)
		id, key := text(a["attempt_id"]), text(a["request_key"])
		if ids[id] || requests[key] || !isSHA256(key) || !known || id != fmt.Sprintf("%s.attempt-%d", text(c["cycle_id"]), history+int64(i)+1) || i < len(attempts)-1 && a["status"] != "completed" {
			return nil, nil, s.reject("PLAN_REVIEW_CONTROL_INVALID", "审查身份、序号或请求重复/重排")
		}
		ids[id] = true
		requests[key] = true
		checkIDs := semStrings(a["check_ids"])
		if !apUnique(a["check_ids"]) {
			return nil, nil, s.reject("PLAN_REVIEW_CONTROL_INVALID", "专业检查范围为空或重复")
		}
		for _, id := range checkIDs {
			if !semHas(semStrings(p["professional_check_ids"]), id) {
				return nil, nil, s.reject("PLAN_REVIEW_CONTROL_INVALID", "审查范围越界")
			}
		}
		if a["status"] == "completed" {
			results := semList(a["check_results"])
			seen := map[string]bool{}
			if len(results) != len(checkIDs) || len(checkIDs) == 0 {
				return nil, nil, s.reject("PLAN_REVIEW_CONTROL_INVALID", "完成尝试缺少逐项结果")
			}
			for _, row := range results {
				v := semMap(row)
				id := text(v["check_id"])
				if seen[id] || !semHas(checkIDs, id) || !semHas([]string{"passed", "blocked"}, text(v["status"])) || a["outcome"] == "passed" && v["status"] != "passed" {
					return nil, nil, s.reject("PLAN_REVIEW_CONTROL_INVALID", "逐项结果重复、越界或伪称通过")
				}
				seen[id] = true
			}
		}
	}
	seen := map[string]bool{}
	for _, row := range semList(c["findings"]) {
		f := semMap(row)
		id := text(f["id"])
		if id == "" || seen[id] || !semHas(semStrings(p["finding_kinds"]), text(f["kind"])) || f["kind"] == "suggestion" && f["status"] != "backlog" || f["kind"] != "suggestion" && f["status"] == "backlog" {
			return nil, nil, s.reject("PLAN_REVIEW_CONTROL_INVALID", "问题身份或分类非法")
		}
		seen[id] = true
	}
	return c, p, nil
}
func planBinding(c, a map[string]any) map[string]any {
	return map[string]any{"protocol_version": c["protocol_version"], "checkpoint_ref": a["checkpoint_ref"], "cycle_id": c["cycle_id"], "attempt_id": a["attempt_id"], "phase": a["phase"], "scope_digest": c["scope_digest"], "request_key": a["request_key"], "check_ids": a["check_ids"], "target_finding_ids": a["target_finding_ids"]}
}
func planLatest(c map[string]any, id string) map[string]any {
	rows := semList(c["attempts"])
	for i := len(rows) - 1; i >= 0; i-- {
		a := semMap(rows[i])
		if semHas(semStrings(a["check_ids"]), id) {
			return a
		}
	}
	return nil
}
func planNormalizedBasis(value any) []any {
	out := []any{}
	for _, r := range semList(value) {
		m := semMap(r)
		out = append(out, map[string]any{"ref": m["ref"], "digest": strings.TrimPrefix(text(m["digest"]), "sha256:")})
	}
	return out
}
func verifyPlanReviewTaskSemantic(s *semanticSession, ref string, opts map[string]string) error {
	task, err := s.doc(ref)
	if err != nil {
		return err
	}
	rc := semMap(task["review_context"])
	checkIDs := semStrings(rc["check_ids"])
	applicable := false
	for _, id := range checkIDs {
		if semHas([]string{"check.domain-strategy-approved", "check.stage-decision-package-approved", "gate.plan-approved"}, id) {
			applicable = true
		}
	}
	if !applicable {
		return nil
	}
	binding := semMap(rc["plan_review_binding"])
	if binding == nil {
		return s.reject("PLAN_REVIEW_ATTEMPT_UNREGISTERED", "Plan 专业任务未绑定登记尝试")
	}
	cpRef := text(binding["checkpoint_ref"])
	if opts["checkpoint"] != "" && opts["checkpoint"] != cpRef {
		return s.reject("PLAN_REVIEW_CHECKPOINT_DRIFT", "消费者 checkpoint 与任务声明不一致")
	}
	cp, err := s.doc(cpRef)
	if err != nil {
		return err
	}
	c, _, err := s.planControl(cp)
	if err != nil {
		return err
	}
	var a map[string]any
	for _, row := range semList(c["attempts"]) {
		m := semMap(row)
		if m["attempt_id"] == binding["attempt_id"] {
			a = m
		}
	}
	if a == nil || !apEqual(planBinding(c, a), binding) {
		return s.reject("PLAN_REVIEW_ATTEMPT_UNREGISTERED", "任务不属于当前登记周期")
	}
	if text(a["task_digest"]) == "" || !semHas([]string{"reserved", "dispatched", "completed"}, text(a["status"])) || opts["require-completed"] == "true" && a["status"] != "completed" {
		return s.reject("PLAN_REVIEW_NOT_COMPLETED", "任务字节未绑定或审查尚未完成")
	}
	if !apEqual(rc["check_ids"], a["check_ids"]) || rc["candidate_ref"] != a["candidate_ref"] || !apEqual(planNormalizedBasis(rc["basis"]), a["basis"]) {
		return s.reject("PLAN_REVIEW_TASK_DRIFT", "任务候选、范围或依据变化")
	}
	bytes, err := s.bytes(text(a["task_ref"]))
	if err != nil {
		return err
	}
	actual, err := s.doc(text(a["task_ref"]))
	if err != nil {
		return err
	}
	if safefs.Digest(bytes) != text(a["task_digest"]) || !apEqual(actual, task) {
		return s.reject("PLAN_REVIEW_TASK_DRIFT", "登记任务实际字节变化")
	}
	subjectBytes, err := s.bytes(text(a["candidate_ref"]))
	if err != nil {
		return err
	}
	subject, err := s.doc(text(a["candidate_ref"]))
	if err != nil {
		return err
	}
	digest, err := semanticDocumentDigest(subject["current_approvals"])
	if err != nil {
		return err
	}
	if safefs.Digest(subjectBytes) != strings.TrimPrefix(text(rc["candidate_digest"]), "sha256:") || !apEqual(subject["plan_review_binding"], binding) || semMap(subject["source_checkpoint"])["ref"] != binding["checkpoint_ref"] || semMap(subject["source_checkpoint"])["digest"] != a["candidate_digest"] || digest != text(a["candidate_digest"]) || !apEqual(subject["current_approvals"], rc["current_approvals"]) {
		return s.reject("PLAN_REVIEW_TASK_DRIFT", "审阅主体与登记候选摘要不一致")
	}
	all := semList(subject["current_approvals"])
	rows := []any{}
	for _, row := range all {
		m := semMap(row)
		if opts["boundary"] == "" || m["boundary"] == opts["boundary"] {
			rows = append(rows, row)
		}
	}
	if opts["boundary"] != "" && len(rows) != 1 {
		return s.reject("PLAN_REVIEW_COVERAGE_REQUIRED", "任务未唯一覆盖消费检查")
	}
	basis := []any{}
	for _, item := range semList(a["basis"]) {
		b := semMap(item)
		inSelected, inAll := false, false
		for _, r := range rows {
			for _, p := range semList(semMap(r)["basis"]) {
				if semMap(p)["ref"] == b["ref"] {
					inSelected = true
				}
			}
		}
		for _, r := range all {
			for _, p := range semList(semMap(r)["basis"]) {
				if semMap(p)["ref"] == b["ref"] {
					inAll = true
				}
			}
		}
		if opts["boundary"] == "" || inSelected || !inAll {
			basis = append(basis, item)
		}
	}
	if err = s.basis(basis); err != nil {
		return err
	}
	for _, row := range rows {
		r := semMap(row)
		proof := append([]any{map[string]any{"ref": r["subject_ref"], "digest": r["subject_digest"]}}, semList(r["basis"])...)
		if err = s.basis(proof); err != nil {
			return err
		}
		bucket := "gates"
		if strings.HasPrefix(text(r["boundary"]), "check.") {
			bucket = "checks"
		}
		current := semMap(semMap(cp[bucket])[text(r["boundary"])])
		if current != nil && (current["subject_ref"] != r["subject_ref"] || current["subject_digest"] != nil && strings.TrimPrefix(text(current["subject_digest"]), "sha256:") != text(r["subject_digest"]) || !apEqual(current["approval_scope"], r["approval_scope"]) || current["drafter_principal_ref"] != r["drafter_principal_ref"]) {
			return s.reject("PLAN_REVIEW_CANDIDATE_DRIFT", "当前检查主体、范围或作者变化")
		}
	}
	return nil
}
func verifyPlanCheckpointSemantic(s *semanticSession, ref string, opts map[string]string) error {
	cp, err := s.doc(ref)
	if err != nil {
		return err
	}
	if cp["repository_mode"] != "project-instance" {
		return nil
	}
	if cp["upstream_spec_baseline"] != nil && (semMap(semMap(cp["gates"])["gate.plan-approved"])["status"] != "approved" || semMap(semMap(cp["gates"])["gate.spec-baseline-approved"])["status"] != "approved") {
		return verifyInheritedSpecCheckpoint(s, ref, cp)
	}
	if cp["next_work_unit"] == "work-unit.spec-synthesis" || cp["stage"] == "stage.spec-architecture" || semMap(cp["stage_trace"])["completed_work_unit"] == "work-unit.spec-synthesis" {
		return s.verify("plan-spec-entry", ref, opts)
	}
	if cp["stage"] != "stage.plan" || !semHas([]string{"resume", "orchestrate"}, text(cp["mode"])) {
		return nil
	}
	prior := semMap(semMap(cp["gates"])["gate.plan-approved"])["status"] == "approved"
	for _, id := range []string{"check.domain-strategy-approved", "check.stage-decision-package-approved"} {
		r := semMap(semMap(cp["checks"])[id])
		if text(r["approval_ref"]) != "" || semHas([]string{"approved", "passed"}, text(r["status"])) {
			prior = true
		}
	}
	if cp["plan_review_control"] == nil && !prior {
		return nil
	}
	c, p, err := s.planControl(cp)
	if err != nil {
		return err
	}
	h, known := integer(c["history_count"])
	max, _ := integer(semMap(p["limits"])["total"])
	if (!known || h+int64(len(semList(c["attempts"]))) > max) && !semHas([]string{"history-unknown", "diagnosis-required", "blocked"}, text(c["status"])) {
		return s.reject("PLAN_REVIEW_BUDGET_EXHAUSTED", "超限或未知历史只能诊断补证据")
	}
	for _, row := range semList(c["attempts"]) {
		a := semMap(row)
		if a["status"] != "completed" {
			if err = s.verify("plan-review-task", text(a["task_ref"]), map[string]string{"checkpoint": ref}); err != nil {
				return err
			}
		}
	}
	return nil
}
func planCheckPolicy(s *semanticSession) (map[string]any, error) {
	p := semMap(semanticDefinition(s.registry, "stages", "stage.plan")["spec_entry"])
	if p == nil || len(semStrings(p["required_checks"])) == 0 {
		return nil, s.unavailable("CAPABILITY", "缺少本地 Plan 入口策略")
	}
	for _, bucket := range []string{"gates", "checks"} {
		ids := []string{}
		for _, r := range semList(s.registry[bucket]) {
			m := semMap(r)
			if m["stage"] == "stage.plan" {
				ids = append(ids, text(m["id"]))
			}
		}
		key := "gate_impacts"
		if bucket == "checks" {
			key = "check_impacts"
		}
		declared := []string{}
		for id := range semMap(p[key]) {
			declared = append(declared, id)
		}
		sort.Strings(ids)
		sort.Strings(declared)
		if !equalStrings(ids, declared) {
			return nil, s.unavailable("CAPABILITY", "Plan 影响规则覆盖不完整")
		}
	}
	return p, nil
}
func planSelectApproval(doc map[string]any, boundary string) map[string]any {
	if doc["kind"] != "review-bundle" {
		if doc["gate_id"] == boundary {
			return doc
		}
		return nil
	}
	for _, row := range semList(doc["reviews"]) {
		r := semMap(row)
		if r["gate_id"] == boundary {
			out := map[string]any{}
			for k, v := range doc {
				out[k] = v
			}
			for k, v := range r {
				out[k] = v
			}
			delete(out, "reviews")
			delete(out, "kind")
			return out
		}
	}
	return nil
}
func planHistoricalLinked(s *semanticSession, c map[string]any, ref string) (bool, error) {
	if semMap(c["provenance"])["kind"] != "adopted" {
		return false, nil
	}
	b, err := s.bytes(ref)
	if err != nil {
		return false, err
	}
	digest := safefs.Digest(b)
	for _, row := range semList(c["history_evidence"]) {
		m := semMap(row)
		if m["ref"] == ref && strings.TrimPrefix(text(m["digest"]), "sha256:") == digest {
			return true, nil
		}
		doc, err := s.doc(text(m["ref"]))
		if err != nil {
			return false, err
		}
		if doc["kind"] == "plan-review-history-classification" && doc["feature_id"] == c["feature_id"] {
			for _, entry := range semList(doc["entries"]) {
				e := semMap(entry)
				if e["kind"] == "professional" && e["ref"] == ref && strings.TrimPrefix(text(e["digest"]), "sha256:") == digest {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func verifyPlanAggregateSemantic(s *semanticSession, ref string, opts map[string]string) error {
	cp, err := s.doc(ref)
	if err != nil {
		return err
	}
	if semMap(semMap(cp["gates"])["gate.plan-approved"])["status"] != "approved" {
		return s.reject("PLAN_GATE_BLOCKED", "Plan 聚合门禁未批准")
	}
	if opts["approval"] != "" && opts["approval"] != first(text(cp["plan_approval_ref"]), text(semMap(semMap(cp["gates"])["gate.plan-approved"])["approval_ref"])) {
		return s.reject("APPROVAL_CURRENT_INVALID", "待验批准不是当前 Plan 聚合记录")
	}
	return verifyPlanEntrySemantic(s, ref, opts)
}
func planExpected(s *semanticSession, id string, check map[string]any, basis []any, control map[string]any) (map[string]any, error) {
	current := map[string]any{}
	for k, v := range check {
		current[k] = v
	}
	filtered := []any{}
	refs := semStrings(check["evidence_refs"])
	for _, row := range basis {
		if semHas(refs, text(semMap(row)["ref"])) {
			filtered = append(filtered, row)
		}
	}
	current["basis"] = filtered
	// The registered attempt is the consumer's independent task expectation.
	// A candidate approval/bundle cannot supply its own expected task identity.
	if attempt := planLatest(control, id); attempt != nil {
		ref, digest := text(attempt["task_ref"]), text(attempt["task_digest"])
		if ref == "" || digest == "" {
			return nil, s.reject("PLAN_REVIEW_TASK_DRIFT", "当前审查尝试缺少任务绑定")
		}
		if err := s.basis([]any{map[string]any{"ref": ref, "digest": digest}}); err != nil {
			return nil, err
		}
		if declared := semMap(check["review_context"]); len(declared) > 0 && (declared["review_task_ref"] != ref || declared["review_task_digest"] != digest) {
			return nil, s.reject("PLAN_REVIEW_TASK_DRIFT", "检查消费上下文与登记任务不一致")
		}
		if err := s.verify("plan-review-task", ref, map[string]string{"checkpoint": text(attempt["checkpoint_ref"]), "require-completed": "true", "boundary": id}); err != nil {
			return nil, err
		}
		task, err := s.doc(ref)
		if err != nil {
			return nil, err
		}
		reviewContext := apCopy(semMap(task["review_context"]))
		reviewContext["review_task_ref"], reviewContext["review_task_digest"] = ref, digest
		current["review_context"] = reviewContext
	}
	return approvalExpectationFromState(s, id, current)
}
func planEvidence(s *semanticSession, basis []any, refs any) error {
	rows := semStrings(refs)
	if len(rows) == 0 {
		return s.reject("PLAN_EVIDENCE_REQUIRED", "检查项缺少当前依据")
	}
	for _, ref := range rows {
		found := false
		for _, row := range basis {
			if semMap(row)["ref"] == ref {
				found = true
			}
		}
		if !found {
			return s.reject("PLAN_EVIDENCE_REQUIRED", "检查引用未绑定审阅包: "+ref)
		}
	}
	return nil
}
func verifyPlanEntrySemantic(s *semanticSession, ref string, opts map[string]string) error {
	cp, err := s.doc(ref)
	if err != nil {
		return err
	}
	if cp["upstream_spec_baseline"] != nil && semMap(semMap(cp["gates"])["gate.plan-approved"])["status"] != "approved" {
		return verifyInheritedSpecCheckpoint(s, ref, cp)
	}
	reviewRef := text(cp["plan_review_ref"])
	feature := text(cp["feature_id"])
	if reviewRef == "" || feature == "" {
		return s.reject("PLAN_CONTEXT_REQUIRED", "缺少 Plan 审阅包或功能身份")
	}
	review, err := s.doc(reviewRef)
	if err != nil {
		return err
	}
	policy, err := planCheckPolicy(s)
	if err != nil {
		return err
	}
	version, ok := integer(review["schema_version"])
	if !ok || version != 1 || review["kind"] != "plan-entry-review" || review["gate_id"] != "gate.plan-approved" || review["feature_id"] != feature {
		return s.reject("PLAN_REVIEW_INVALID", "审阅包身份或范围不匹配")
	}
	if review["review_protocol"] != nil && !semHas([]string{"bundled-plan-review-v1", nativePlanProtocol}, text(review["review_protocol"])) {
		return s.unavailable("CAPABILITY", "未知 Plan 审查协议")
	}
	basis := semList(review["basis"])
	if err = s.basis(basis); err != nil {
		return err
	}
	for _, r := range []string{text(review["plan_ref"]), "CONTEXT.md", ".template-spec/process/lifecycle-registry.yaml", text(review["context_reconciliation_ref"])} {
		if err = planEvidence(s, basis, []any{r}); err != nil {
			return err
		}
	}
	checks := semMap(review["checks"])
	ids := semStrings(policy["required_checks"])
	if len(checks) != len(ids) {
		return s.reject("PLAN_CHECK_REQUIRED", "检查项缺失或存在未知项")
	}
	for _, id := range ids {
		check := semMap(checks[id])
		if check["status"] != "passed" {
			return s.reject("PLAN_CHECK_BLOCKED", "Plan 检查未通过: "+id)
		}
		if err = planEvidence(s, basis, check["evidence_refs"]); err != nil {
			return err
		}
	}
	openItems, ok := review["open_items"].([]any)
	if !ok {
		return s.reject("PLAN_OPEN_ITEMS", "必须显式列出未决项")
	}
	for _, row := range openItems {
		item := semMap(row)
		_, criticalOK := item["critical"].(bool)
		_, blockerOK := item["runnable_blocker"].(bool)
		if text(item["id"]) == "" || !criticalOK || !blockerOK {
			return s.reject("PLAN_OPEN_ITEMS", "未决项分类不完整")
		}
		if item["status"] != "resolved" {
			if item["status"] != "deferred" || item["critical"] == true || item["runnable_blocker"] == true {
				return s.reject("PLAN_OPEN_ITEMS", "关键问题或可执行阻塞未解决")
			}
			for _, field := range []string{"noncritical_reason", "owner", "resolution_point", "downstream_recipient"} {
				if strings.TrimSpace(text(item[field])) == "" {
					return s.reject("PLAN_OPEN_ITEMS", "延期项缺少 "+field)
				}
			}
		}
		if err = planEvidence(s, basis, item["evidence_refs"]); err != nil {
			return err
		}
	}
	c, p, err := s.planControl(cp)
	if err != nil {
		return err
	}
	internal := semMap(review["internal_checks"])
	applicable := []string{}
	bundles := map[string]map[string]any{}
	for id, impact := range semMap(policy["check_impacts"]) {
		hit, ok := semMap(review["impacts"])[text(impact)].(bool)
		if !ok {
			return s.reject("PLAN_IMPACT_REQUIRED", "未评估影响面: "+text(impact))
		}
		check := semMap(internal[id])
		if err = planEvidence(s, basis, check["evidence_refs"]); err != nil {
			return err
		}
		if !hit {
			if check["status"] != "not-applicable" || strings.TrimSpace(text(check["reason"])) == "" {
				return s.reject("PLAN_APPLICABILITY", "未命中检查须有原因与证据")
			}
			continue
		}
		if check["status"] != "approved" {
			return s.reject("PLAN_CHECK_BLOCKED", "命中检查未批准: "+id)
		}
		if err = planEvidence(s, basis, []any{check["approval_ref"], check["subject_ref"]}); err != nil {
			return err
		}
		approvalRef := text(check["approval_ref"])
		source, err := s.doc(approvalRef)
		if err != nil {
			return err
		}
		record, err := selectApprovalRecordSemantic(s, source, id)
		if err != nil {
			return err
		}
		if record["subject_ref"] != check["subject_ref"] || !semHas(semStrings(check["approval_scope"]), feature) || !apSetEqual(check["approval_scope"], record["approval_scope"]) {
			return s.reject("PLAN_APPROVAL_CONTEXT", "会签主体或范围不匹配")
		}
		if source["kind"] == "review-bundle" {
			if _, err = reviewBundleRowsSemantic(s, source); err != nil {
				return err
			}
			v, _ := integer(source["schema_version"])
			if v != 2 || source["plan_review_binding"] == nil {
				linked, e := planHistoricalLinked(s, c, approvalRef)
				if e != nil {
					return e
				}
				if !linked {
					return s.reject("PLAN_REVIEW_BINDING_REQUIRED", "新 Plan 审查须绑定当前周期")
				}
			} else {
				var declared map[string]any
				for _, row := range semList(semMap(semMap(s.roles["gate_policy"])["review_execution"])["review_bundles"]) {
					m := semMap(row)
					if m["aggregate_gate"] == "gate.plan-approved" {
						declared = m
					}
				}
				binding := semMap(source["plan_review_binding"])
				if declared == nil || source["bundle_id"] != declared["bundle_id"] || source["work_unit_id"] != declared["work_unit"] || binding["protocol_version"] != nativePlanProtocol || binding["cycle_id"] != c["cycle_id"] || binding["scope_digest"] != c["scope_digest"] {
					return s.reject("PLAN_REVIEW_BINDING_REQUIRED", "组合结论与本地策略或周期不一致")
				}
				reviewIDs := []string{}
				for _, row := range semList(source["reviews"]) {
					reviewIDs = append(reviewIDs, text(semMap(row)["gate_id"]))
				}
				if !semSameSet(reviewIDs, semStrings(binding["check_ids"])) {
					return s.reject("PLAN_REVIEW_COVERAGE_REQUIRED", "组合结论未覆盖实际检查范围")
				}
				a := planLatest(c, id)
				if a == nil || a["attempt_id"] != binding["attempt_id"] || a["status"] != "completed" || a["task_ref"] != source["review_task_ref"] || a["task_digest"] != source["review_task_digest"] {
					return s.reject("PLAN_REVIEW_RESULT_REQUIRED", "检查缺少登记且通过的当前结论")
				}
				passed := false
				for _, row := range semList(a["check_results"]) {
					m := semMap(row)
					if m["check_id"] == id && m["status"] == "passed" {
						passed = true
					}
				}
				if !passed {
					return s.reject("PLAN_REVIEW_RESULT_REQUIRED", "检查未通过")
				}
				task, err := s.doc(text(source["review_task_ref"]))
				if err != nil {
					return err
				}
				if !apEqual(semMap(task["review_context"])["plan_review_binding"], binding) {
					return s.reject("PLAN_REVIEW_TASK_DRIFT", "组合结论与任务绑定不一致")
				}
			}
			bundles[approvalRef] = source
		} else {
			linked, e := planHistoricalLinked(s, c, approvalRef)
			if e != nil {
				return e
			}
			if !linked {
				return s.reject("PLAN_REVIEW_BUNDLE_REQUIRED", "专业结论缺少当前组合审查")
			}
		}
		expected, err := planExpected(s, id, check, basis, c)
		if err != nil {
			return err
		}
		if err = assertCurrentApprovalSemantic(s, record, expected); err != nil {
			return err
		}
		applicable = append(applicable, id)
	}
	for _, row := range semList(s.registry["checks"]) {
		definition := semMap(row)
		id := text(definition["id"])
		if definition["stage"] == "stage.plan" && semMap(internal[id])["status"] == "approved" {
			for _, dependency := range semStrings(definition["requires_checks"]) {
				if semMap(internal[dependency])["status"] != "approved" {
					return s.reject("PLAN_DEPENDENCY", "内部检查依赖未批准: "+dependency)
				}
			}
		}
	}
	reconRef := text(review["context_reconciliation_ref"])
	recon, err := s.doc(reconRef)
	if err != nil {
		return err
	}
	if recon["status"] != "reconciled" || recon["repository_mode"] != "project-instance" {
		return s.reject("CONTEXT", "Plan Context 尚未调和")
	}
	if err = s.verify("context-reconciliation", reconRef, opts); err != nil {
		return err
	}
	if err = s.planEntryControl(cp, c, p, ref, review, basis, applicable); err != nil {
		return err
	}
	if _, err = assertUserDecisionRequirementSemantic(s, map[string]any{"boundary": "gate.plan-approved", "subject_ref": reviewRef, "scope": []any{feature}, "user_decision_ref": cp["plan_user_decision_ref"], "continuation_ref": cp["plan_continuation_ref"]}); err != nil {
		return err
	}
	return s.planAggregateRecord(cp, review, basis, bundles)
}
func (s *semanticSession) planEntryControl(cp, c, p map[string]any, ref string, review map[string]any, basis []any, applicable []string) error {
	history, known := integer(c["history_count"])
	attempts := semList(c["attempts"])
	open := false
	for _, row := range semList(c["findings"]) {
		m := semMap(row)
		if m["kind"] != "suggestion" && m["status"] == "open" {
			open = true
		}
	}
	if len(attempts) > 0 {
		last := semMap(attempts[len(attempts)-1])
		actual, err := s.doc(text(last["checkpoint_ref"]))
		if err != nil {
			return err
		}
		if actual["feature_id"] != c["feature_id"] || !apEqual(actual["plan_review_control"], c) {
			return s.reject("PLAN_REVIEW_CHECKPOINT_DRIFT", "阶段入口使用了旧控制状态")
		}
	}
	if len(applicable) == 0 {
		if !known || open || semHas([]string{"blocked", "history-unknown", "diagnosis-required"}, text(c["status"])) {
			return s.reject("PLAN_REVIEW_ENTRY_BLOCKED", "无专业影响仍需已知历史且无阻断")
		}
		for _, row := range attempts {
			if semMap(row)["status"] != "completed" {
				return s.reject("PLAN_REVIEW_ENTRY_BLOCKED", "尚有未完成审查")
			}
		}
		return s.basis(basis)
	}
	limits := semMap(p["limits"])
	regular, _ := integer(limits["regular"])
	total, _ := integer(limits["total"])
	exceptions, _ := integer(limits["exception"])
	if semMap(c["provenance"])["kind"] == "adopted" && len(attempts) == 0 && history > 0 {
		if c["status"] != "active" || open {
			return s.reject("PLAN_REVIEW_ENTRY_BLOCKED", "旧结论需当前独立核验")
		}
		if history >= regular {
			diagnosed := false
			for _, row := range semList(c["diagnoses"]) {
				if apEqual(semMap(row)["finding_ids"], []any{"history:" + text(c["cycle_id"])}) {
					diagnosed = true
				}
			}
			if !diagnosed {
				return s.reject("PLAN_REVIEW_ENTRY_BLOCKED", "历史超额须主控诊断")
			}
		}
		for _, id := range applicable {
			var conclusion map[string]any
			for _, row := range semList(c["history_conclusions"]) {
				m := semMap(row)
				if m["check_id"] == id {
					conclusion = m
				}
			}
			if conclusion == nil {
				return s.reject("PLAN_REVIEW_ENTRY_BLOCKED", "缺少适用历史逐项原结论")
			}
			proof := append([]any{map[string]any{"ref": conclusion["approval_ref"], "digest": conclusion["approval_digest"]}, map[string]any{"ref": conclusion["subject_ref"], "digest": conclusion["subject_digest"]}}, semList(conclusion["basis"])...)
			if err := s.basis(proof); err != nil {
				return err
			}
			check := semMap(semMap(review["internal_checks"])[id])
			expected, err := planExpected(s, id, check, basis, c)
			if err != nil {
				return err
			}
			record, err := s.doc(text(conclusion["approval_ref"]))
			if err != nil {
				return err
			}
			selected, err := selectApprovalRecordSemantic(s, record, id)
			if err != nil {
				return err
			}
			if err = assertCurrentApprovalSemantic(s, selected, expected); err != nil {
				return err
			}
		}
		for _, row := range semList(c["diagnoses"]) {
			d := semMap(row)
			proof := append([]any{map[string]any{"ref": d["ref"], "digest": d["digest"]}}, semList(d["resolution_evidence"])...)
			if err := s.basis(proof); err != nil {
				return err
			}
		}
		return nil
	}
	if !known || c["status"] != "converged" || open || len(attempts) == 0 {
		return s.reject("PLAN_REVIEW_ENTRY_BLOCKED", "当前 Plan 周期未收敛")
	}
	last := semMap(attempts[len(attempts)-1])
	if last["status"] != "completed" || last["outcome"] != "passed" {
		return s.reject("PLAN_REVIEW_ENTRY_BLOCKED", "缺少当前专业审查完成证据")
	}
	exceptionCount := 0
	for _, row := range attempts {
		if semMap(row)["phase"] == "exception" {
			exceptionCount++
		}
	}
	if history+int64(len(attempts)) > total || history+int64(len(attempts)-exceptionCount) > regular || int64(exceptionCount) > exceptions {
		return s.reject("PLAN_REVIEW_BUDGET_EXHAUSTED", "审查预算超额")
	}
	proof := append([]any{map[string]any{"ref": last["task_ref"], "digest": last["task_digest"]}, map[string]any{"ref": last["result_ref"], "digest": last["result_digest"]}}, semList(last["basis"])...)
	if err := s.basis(proof); err != nil {
		return err
	}
	result, err := s.doc(text(last["result_ref"]))
	if err != nil {
		return err
	}
	if !apEqual(result["plan_review_binding"], planBinding(c, last)) || result["outcome"] != "passed" || !apEqual(result["check_results"], last["check_results"]) {
		return s.reject("PLAN_REVIEW_RESULT_MISMATCH", "完成结论与原始结果不一致")
	}
	task, err := s.doc(text(last["task_ref"]))
	if err != nil {
		return err
	}
	subject, err := s.doc(text(semMap(task["review_context"])["candidate_ref"]))
	if err != nil {
		return err
	}
	if task["checkpoint_ref"] != last["checkpoint_ref"] || semMap(subject["source_checkpoint"])["ref"] != last["checkpoint_ref"] || text(semMap(subject["source_checkpoint"])["digest"]) == "" || last["candidate_digest"] != semMap(subject["source_checkpoint"])["digest"] {
		return s.reject("PLAN_REVIEW_CANDIDATE_REQUIRED", "任务缺少当前 checkpoint 来源绑定")
	}
	if err = s.verify("plan-review-task", text(last["task_ref"]), map[string]string{"checkpoint": ref, "require-completed": "true"}); err != nil {
		return err
	}
	for _, id := range applicable {
		a := planLatest(c, id)
		if a == nil || a["status"] != "completed" {
			return s.reject("PLAN_REVIEW_ENTRY_BLOCKED", "缺少当前专业逐项结论")
		}
		passed := false
		for _, row := range semList(a["check_results"]) {
			m := semMap(row)
			if m["check_id"] == id && m["status"] == "passed" {
				passed = true
			}
		}
		if !passed {
			return s.reject("PLAN_REVIEW_ENTRY_BLOCKED", "逐项结论未通过")
		}
		if err = s.basis([]any{map[string]any{"ref": a["task_ref"], "digest": a["task_digest"]}, map[string]any{"ref": a["result_ref"], "digest": a["result_digest"]}}); err != nil {
			return err
		}
		r, err := s.doc(text(a["result_ref"]))
		if err != nil {
			return err
		}
		if !apEqual(r["plan_review_binding"], planBinding(c, a)) || !apEqual(r["check_results"], a["check_results"]) {
			return s.reject("PLAN_REVIEW_RESULT_MISMATCH", "复用检查缺少结果绑定")
		}
		if err = s.verify("plan-review-task", text(a["task_ref"]), map[string]string{"checkpoint": ref, "boundary": id, "require-completed": "true"}); err != nil {
			return err
		}
	}
	return nil
}
func (s *semanticSession) planAggregateRecord(cp, review map[string]any, basis []any, bundles map[string]map[string]any) error {
	approvalRef := first(text(cp["plan_approval_ref"]), text(semMap(semMap(cp["gates"])["gate.plan-approved"])["approval_ref"]))
	if approvalRef == "" {
		return s.reject("PLAN_APPROVAL_REQUIRED", "缺少独立 Plan 聚合批准")
	}
	for _, ref := range []string{approvalRef, text(cp["plan_review_ref"])} {
		if err := rejectProgressionEvidence(s, ref); err != nil {
			return err
		}
	}
	record, err := s.doc(approvalRef)
	if err != nil {
		return err
	}
	if err = currentApprovalReferencesSemantic(s, record); err != nil {
		return err
	}
	if record["kind"] == "review-bundle" || record["gate_id"] != "gate.plan-approved" {
		return s.reject("PLAN_APPROVAL_INVALID", "Plan 聚合须使用独立批准记录")
	}
	if err = s.validateSchema(".template-spec/process/schemas/approval-record.schema.json", record); err != nil {
		return err
	}
	if _, err = assertApprovalSignerSemantic(s, record); err != nil {
		return err
	}
	bytes, err := s.bytes(text(cp["plan_review_ref"]))
	if err != nil {
		return err
	}
	drafter := first(text(review["drafter_principal_ref"]), text(semMap(semMap(cp["gates"])["gate.plan-approved"])["drafter_principal_ref"]))
	if record["subject_ref"] != cp["plan_review_ref"] || strings.TrimPrefix(text(record["subject_digest"]), "sha256:") != safefs.Digest(bytes) || !apSetEqual(record["approval_scope"], []any{cp["feature_id"]}) || drafter == "" || record["drafter_principal_ref"] != drafter || record["principal_ref"] == drafter {
		return s.reject("PLAN_APPROVAL_CONTEXT", "聚合批准未绑定当前主体、范围或独立作者")
	}
	if err = s.basis(record["basis"]); err != nil {
		return err
	}
	for _, row := range basis {
		m := semMap(row)
		covered := false
		for _, r := range semList(record["basis"]) {
			b := semMap(r)
			if b["ref"] == m["ref"] && strings.TrimPrefix(text(b["digest"]), "sha256:") == strings.TrimPrefix(text(m["digest"]), "sha256:") {
				covered = true
			}
		}
		if !covered {
			return s.reject("PLAN_APPROVAL_COVERAGE", "聚合批准未覆盖审阅依据")
		}
	}
	if cp["plan_continuation_ref"] != nil {
		if record["continuation_ref"] != cp["plan_continuation_ref"] {
			return s.reject("PLAN_DECISION_BINDING", "批准未绑定同一延续")
		}
	} else if record["user_decision_ref"] != cp["plan_user_decision_ref"] {
		return s.reject("PLAN_DECISION_BINDING", "批准未绑定同一真实用户决定")
	}
	c := semMap(cp["plan_review_control"])
	attempts := semList(c["attempts"])
	var reused map[string]any
	bundleRef := ""
	if len(attempts) > 0 {
		last := semMap(attempts[len(attempts)-1])
		for ref, b := range bundles {
			if semMap(b["plan_review_binding"])["attempt_id"] == last["attempt_id"] {
				reused = b
				bundleRef = ref
			}
		}
		if reused == nil || record["review_bundle_ref"] != bundleRef {
			return s.reject("PLAN_APPROVAL_REUSE", "未复用最终内部检查会话")
		}
	} else {
		hasApproved := false
		for _, row := range semMap(review["internal_checks"]) {
			if semMap(row)["status"] == "approved" {
				hasApproved = true
			}
		}
		if !hasApproved {
			if record["review_bundle_ref"] != nil || record["review_task_ref"] != nil || record["plan_review_binding"] != nil {
				return s.reject("PLAN_APPROVAL_REUSE", "全部不适用时不得制造空专业任务")
			}
			return nil
		}
		if semMap(c["provenance"])["kind"] != "adopted" {
			return s.reject("PLAN_APPROVAL_REUSE", "历史结论未受控接入")
		}
		if len(bundles) > 0 {
			reused = bundles[text(record["review_bundle_ref"])]
			if reused == nil {
				return s.reject("PLAN_APPROVAL_REUSE", "未复用原内部审查会话")
			}
		} else {
			for id, row := range semMap(review["internal_checks"]) {
				check := semMap(row)
				if check["status"] != "approved" {
					continue
				}
				doc, err := s.doc(text(check["approval_ref"]))
				if err != nil {
					return err
				}
				r, err := selectApprovalRecordSemantic(s, doc, id)
				if err != nil {
					return err
				}
				if r["principal_ref"] == record["principal_ref"] && r["role_id"] == record["role_id"] && r["runtime_id"] == record["runtime_id"] {
					reused = r
					break
				}
			}
			if record["review_bundle_ref"] != nil || reused == nil {
				return s.reject("PLAN_APPROVAL_REUSE", "旧单项结论不得补造组合审查")
			}
		}
	}
	for _, key := range []string{"role_id", "runtime_id", "principal_ref"} {
		if record[key] != reused[key] {
			return s.reject("PLAN_APPROVAL_REUSE", "聚合与内部会签身份不一致")
		}
	}
	if (len(attempts) > 0 || reused["review_session_id"] != nil) && record["review_session_id"] != reused["review_session_id"] {
		return s.reject("PLAN_APPROVAL_REUSE", "会话未复用")
	}
	version, _ := integer(record["schema_version"])
	rv, _ := integer(reused["schema_version"])
	if len(attempts) > 0 && version == 2 {
		if record["review_task_ref"] != reused["review_task_ref"] || record["review_task_digest"] != reused["review_task_digest"] || !apSetEqual(record["capability_ids"], reused["capability_ids"]) || !apEqual(record["plan_review_binding"], reused["plan_review_binding"]) {
			return s.reject("PLAN_APPROVAL_REUSE", "任务、能力或周期绑定未复用")
		}
	} else if rv == 2 || record["review_task_ref"] != nil {
		if record["review_task_ref"] != reused["review_task_ref"] || record["review_task_digest"] != reused["review_task_digest"] {
			return s.reject("PLAN_APPROVAL_REUSE", "原专业任务未复用")
		}
	}
	return nil
}
