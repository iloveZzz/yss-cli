package approval

import (
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

const StageResultSchema = ".template-spec/process/schemas/stage-result.schema.json"

func init() { kernel.RegisterRoleIndependentValidator("stage-result", VerifyStageResult) }

// VerifyStageResult checks an immutable stage result against current content and
// original authorization. Readiness is never granted by a child approval.
func VerifyStageResult(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	if !s.StageGatesEnabled() {
		return s.Unavailable("CAPABILITY", "当前 Spec 未启用 stage-gates-v1")
	}
	r, err := s.Doc(ref)
	if err != nil {
		return err
	}
	if err = s.ValidateSchema(StageResultSchema, r); err != nil {
		return err
	}
	gate := kernel.Text(r["gate_id"])
	definition := s.StageGate(gate)
	if definition == nil || (opts["gate"] != "" && opts["gate"] != gate) {
		return s.Reject("STAGE_GATE", "阶段出口与当前政策不匹配")
	}
	if r["kind"] != "stage-result" || kernel.ContractN(r["schema_version"]) != 1 || !kernel.ApUnique(r["scope"]) || !kernel.ApContains(r["scope"], kernel.Text(r["feature_id"])) {
		return s.Reject("STAGE_RESULT", "阶段记录格式或范围无效")
	}
	if s.Report.Coverage == nil {
		s.Report.Coverage = map[string]any{}
	}
	coverage := map[string]any{"gate_id": gate, "status": r["outcome"], "affected_checks": []string{}, "blocking_findings": r["blocking_findings"]}
	s.Report.Coverage["stage_result"] = coverage
	assets := kernel.ApRows(r["assets"])
	if len(assets) == 0 {
		return s.Reject("STAGE_ASSETS", "阶段缺少已形成的成果")
	}
	seen := map[string]bool{}
	contentKinds := map[string]bool{}
	reviewBindings := []any{r["assets"]}
	reviewPaths := []string{}
	for _, a := range assets {
		reviewPaths = append(reviewPaths, kernel.Text(a["ref"]))
	}
	for _, a := range assets {
		name := kernel.Text(a["ref"])
		if name == "" || seen[name] || sameStageRef(s, name, ref) || opts["checkpoint"] != "" && sameStageRef(s, name, opts["checkpoint"]) {
			return s.Reject("STAGE_SELF_BINDING", "阶段成果重复或引用自身/可变 checkpoint")
		}
		seen[name] = true
		if doc, e := s.Doc(name); e == nil && (kernel.ContractN(doc["schema_version"]) == 4 || kernel.ContractN(doc["schema_version"]) == 3) {
			kind := ""
			if doc["domain_strategy_id"] != nil {
				kind = "domain-strategy"
			}
			if doc["stage_decision_id"] != nil {
				kind = "stage-decision-package"
			}
			if kind != "" {
				suffix, status := "-v4", "ready"
				if kernel.ContractN(doc["schema_version"]) == 3 {
					suffix, status = "-v3", "approved"
				}
				contentKinds[kind] = true
				if e = s.ValidateSchema(".template-spec/process/schemas/strategic-handoff-"+kind+suffix+".schema.json", doc); e != nil {
					return e
				}
				if doc["status"] != status {
					return s.Reject("STAGE_CONTENT", "阶段只接受 ready 的当前业务内容")
				}
				if _, e = s.ContextSnapshot(kernel.ApMap(doc["context_snapshot"])); e != nil {
					return e
				}
				if kind == "stage-decision-package" {
					bound := kernel.ApMap(doc["domain_strategy_ref"])
					domain, e := s.Doc(kernel.Text(bound["persisted_ref"]))
					if e != nil {
						return e
					}
					if domain["schema_version"] != doc["schema_version"] || domain["status"] != status || domain["domain_strategy_id"] != bound["domain_strategy_id"] || domain["domain_version"] != bound["domain_version"] || stageHex(bound["digest"]) != stageHex(kernel.ApDigest(domain)) {
						return s.Reject("STAGE_CONTENT", "阶段包引用的领域策略已变化")
					}
				}
			}
		}

		// Checkpoint-shaped files cannot be smuggled in when planning without a
		// registered checkpoint argument either.
		if doc, e := s.Doc(name); e == nil && doc["repository_mode"] != nil && doc["gates"] != nil {
			return s.Reject("STAGE_SELF_BINDING", "不能冻结可变 checkpoint")
		}
	}
	if gate == "gate.plan-approved" {
		for id, kind := range map[string]string{"check.domain-strategy-approved": "domain-strategy", "check.stage-decision-package-approved": "stage-decision-package"} {
			if kernel.ApContains(definition["checks"], id) && !contentKinds[kind] {
				return s.Reject("STAGE_CONTENT", "Plan 缺少已核验的当前成果成员: "+kind)
			}
		}
	}
	if err = s.Basis(r["assets"]); err != nil {
		coverage["status"] = "stale"
		return err
	}
	if cpRef := opts["checkpoint"]; cpRef != "" {
		cp, e := s.Doc(cpRef)
		if e != nil {
			return e
		}
		if kernel.ContractN(cp["schema_version"]) != 2 || cp["feature_id"] != r["feature_id"] {
			return s.Reject("STAGE_CHECKPOINT", "阶段记录与当前功能不匹配")
		}
		binding := kernel.ApMap(kernel.ApMap(cp["gates"])[gate])
		b, e := s.Bind(ref)
		if e != nil {
			return e
		}
		if !sameStageRef(s, kernel.Text(binding["record_ref"]), ref) || stageHex(binding["record_digest"]) != stageHex(b.Digest) {
			return s.Reject("STAGE_CHECKPOINT", "当前 checkpoint 未绑定该阶段记录字节")
		}
	}
	checkIDs := map[string]bool{}
	for _, check := range kernel.ApRows(r["checks"]) {
		id := kernel.Text(check["id"])
		if id == "" || checkIDs[id] {
			return s.Reject("STAGE_CHECK", "重复或缺失检查标识")
		}
		checkIDs[id] = true
		for _, key := range []string{"approval_ref", "approver", "role_id", "countersigner_role_ids"} {
			if _, found := check[key]; found {
				return s.Reject("STAGE_CHILD_APPROVAL", "阶段内部检查不能要求独立批准")
			}
		}
		if !kernel.SemHas(kernel.ApStrings(definition["checks"]), id) {
			return s.Reject("STAGE_CHECK", "检查不属于当前阶段: "+id)
		}
		if check["result"] == "failed" {
			coverage["status"] = "blocked"
			return s.Reject("STAGE_CHECK_FAILED", id)
		}
		if !kernel.SemHas([]string{"passed", "not-applicable"}, kernel.Text(check["result"])) {
			return s.Reject("STAGE_CHECK", "检查结果不是质量结论")
		}
		if check["result"] == "not-applicable" && strings.TrimSpace(kernel.Text(check["reason"])) == "" {
			return s.Reject("STAGE_CHECK", "不适用结论缺少原因")
		}
		if check["result"] == "not-applicable" {
			if gate == "gate.plan-approved" || id == "check.artifact-consistency-analyzed" || id == "check.slice-ready-for-agent" {
				return s.Reject("STAGE_APPLICABILITY", "必要内容检查不能声明不适用: "+id)
			}
			if id == "check.prototype-reviewed" || id == "check.prototype-verified" {
				noUI := false
				for _, member := range assets {
					doc, e := s.Doc(kernel.Text(member["ref"]))
					if e == nil && doc["stage_decision_id"] != nil {
						impact := kernel.ApMap(doc["impact_assessment"])
						if impact["ui"] == true {
							return s.Reject("STAGE_APPLICABILITY", "当前影响评估要求 UI 检查")
						}
						noUI = impact["ui"] == false
					}
				}
				if !noUI {
					return s.Reject("STAGE_APPLICABILITY", "不适用原型检查需要已绑定的当前无 UI 影响评估")
				}
			}
		}
		if len(kernel.ApRows(check["inputs"])) == 0 || len(kernel.ApRows(check["evidence"])) == 0 {
			return s.Reject("STAGE_CHECK", "检查缺少当前输入或实际证据")
		}
		covered := false
		for _, input := range kernel.ApRows(check["inputs"]) {
			for _, asset := range assets {
				if sameStageRef(s, kernel.Text(input["ref"]), kernel.Text(asset["ref"])) && stageHex(input["digest"]) == stageHex(asset["digest"]) {
					covered = true
				}
			}
		}
		if !covered {
			return s.Reject("STAGE_CHECK_SCOPE", "检查输入未覆盖当前阶段成果: "+id)
		}
		// Typed Plan checks must consume their corresponding current content.
		requiredKind := map[string]string{"check.domain-strategy-approved": "domain_strategy_id", "check.stage-decision-package-approved": "stage_decision_id"}[id]
		if requiredKind != "" {
			matched := false
			for _, input := range kernel.ApRows(check["inputs"]) {
				for _, asset := range assets {
					if sameStageRef(s, kernel.Text(input["ref"]), kernel.Text(asset["ref"])) {
						d, e := s.Doc(kernel.Text(input["ref"]))
						matched = matched || e == nil && d[requiredKind] != nil
					}
				}
			}
			if !matched {
				return s.Reject("STAGE_CHECK_SCOPE", "检查未绑定对应的业务成果: "+id)
			}
		}
		reviewBindings = append(reviewBindings, check["inputs"])
		if err = s.Basis(check["inputs"]); err != nil {
			coverage["status"] = "stale"
			coverage["affected_checks"] = []string{id}
			return err
		}
		if err = s.Basis(check["evidence"]); err != nil {
			coverage["status"] = "stale"
			coverage["affected_checks"] = []string{id}
			return err
		}
		for _, proof := range kernel.ApRows(check["evidence"]) {
			if err = stageExecution(s, kernel.Text(proof["ref"]), check); err != nil {
				return err
			}
		}
	}
	for _, id := range kernel.ApStrings(definition["checks"]) {
		if !checkIDs[id] {
			return s.Reject("STAGE_CHECK_MISSING", id)
		}
	}
	provenance := kernel.ApMap(r["provenance"])
	if kernel.Text(provenance["tool"]) == "" || kernel.Text(provenance["execution_ref"]) == "" {
		return s.Reject("STAGE_EXECUTION", "缺少实际阶段执行来源")
	}
	execution, e := s.Doc(kernel.Text(provenance["execution_ref"]))
	if e != nil {
		return e
	}
	if err = validStageExecution(s, execution); err != nil {
		return err
	}
	if requirement := kernel.Text(provenance["review_requirement_ref"]); requirement != "" {
		binding, e := s.Bind(requirement)
		if e != nil {
			return e
		}
		reviewBindings = append(reviewBindings, []any{map[string]any{"ref": requirement, "digest": binding.Digest}})
	}
	candidateBinding, e := s.Bind(kernel.Text(assets[0]["ref"]))
	if e != nil {
		return e
	}
	normalizedReviewBindings := []any{}
	for _, group := range reviewBindings {
		rows := []any{}
		for _, row := range kernel.ApRows(group) {
			rows = append(rows, map[string]any{"ref": row["ref"], "digest": stageHex(row["digest"])})
		}
		normalizedReviewBindings = append(normalizedReviewBindings, rows)
	}
	var implementation map[string]any
	var trustedStageReview map[string]any
	reviewCandidate := map[string]any{"ref": assets[0]["ref"], "digest": candidateBinding.Digest}
	reviewScope := reviewPaths
	if kernel.Text(provenance["task_ref"]) != "" {
		if err = s.Verify("stage-review-provenance", ref, nil); err != nil {
			return err
		}
		trusted := kernel.ApMap(s.Report.Coverage["stage_review_provenance"])
		trustedStageReview = trusted
		implementation = kernel.ApMap(trusted["implementation"])
		reviewCandidate = kernel.ApMap(trusted["candidate"])
		reviewScope = kernel.ApStrings(trusted["scope"])
		normalizedReviewBindings = append(normalizedReviewBindings, trusted["required_bindings"])
	}
	mode, e := s.RequiredReviewModeForTask(reviewCandidate, implementation, reviewScope, reviewPaths, normalizedReviewBindings...)
	if e != nil {
		return e
	}
	if mode != "self-check" {
		taskRef := kernel.Text(provenance["task_ref"])
		if taskRef == "" {
			return s.Reject("STAGE_REVIEW", "明确独立复核要求缺少实际审查任务")
		}
		if err = s.Verify("task", taskRef, nil); err != nil {
			return err
		}
		task, e := s.Doc(taskRef)
		if e != nil {
			return e
		}
		context := kernel.ApMap(task["review_context"])
		if task["workflow_status"] != "resolved" || !kernel.SemHas([]string{"Reviewer", "Verifier"}, kernel.Text(task["execution_state"])) || !kernel.SemHas([]string{"independent-review", "external-required-review"}, kernel.Text(kernel.ApMap(context["review"])["mode"])) || task["feature_id"] != r["feature_id"] || !kernel.ApEqual(context["candidate"], trustedStageReview["implementation_candidate"]) {
			return s.Reject("STAGE_REVIEW", "实际任务未完成当前范围要求的独立复核")
		}
		// A validated review task has a native candidate; every stage asset must
		// also be among its immutable bound inputs, never an unrelated review.
		for _, asset := range assets {
			found := false
			for _, bound := range kernel.ApRows(task["input_bindings"]) {
				found = found || sameStageRef(s, kernel.Text(asset["ref"]), kernel.Text(bound["ref"])) && stageHex(asset["digest"]) == stageHex(bound["digest"])
			}
			if !found {
				return s.Reject("STAGE_REVIEW", "独立复核没有覆盖当前阶段成果")
			}
		}
	}
	if gate == "gate.spec-baseline-approved" {
		if err = s.Verify("stage-spec-inputs", ref, nil); err != nil {
			return err
		}
	}

	if r["outcome"] != "approved" || len(kernel.ApArray(r["blocking_findings"])) != 0 {
		return s.Reject("STAGE_PENDING", "阶段尚未批准或存在未解决阻断")
	}
	if err = stageAuthorization(s, ref, r); err != nil {
		return err
	}
	if gate == "gate.delivery-accepted" {
		if err = s.Verify("stage-delivery-inputs", ref, opts); err != nil {
			return err
		}
	}
	if gate == "gate.implementation-admitted" {
		if err = s.Verify("stage-admission-inputs", ref, opts); err != nil {
			return err
		}
	}
	if assetRef := opts["asset-ref"]; assetRef != "" {
		matched := false
		for _, a := range assets {
			matched = matched || sameStageRef(s, kernel.Text(a["ref"]), assetRef)
		}
		if !matched {
			return s.Reject("STAGE_ASSET_UNAPPROVED", "当前成果不属于该阶段: "+assetRef)
		}
	}
	coverage["status"] = "approved"
	s.Report.Coverage["stage_result"] = coverage
	return nil
}

func stageHex(value any) string { return strings.TrimPrefix(kernel.Text(value), "sha256:") }
func sameStageRef(s *kernel.SemanticSession, a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	av, ar, ae := s.ReferenceView(a)
	bv, br, be := s.ReferenceView(b)
	return ae == nil && be == nil && av == bv && ar == br
}
func validStageExecution(s *kernel.SemanticSession, log map[string]any) error {
	if log["phase"] != "execution" || !kernel.ApIsArray(log["argv"]) || len(kernel.ApArray(log["argv"])) == 0 || log["exit_code"] == nil || kernel.ContractN(log["exit_code"]) != 0 || log["result"] != "passed" || len(kernel.ApArray(log["blocking_findings"])) > 0 {
		return s.Reject("STAGE_EXECUTION", "验证未实际成功执行")
	}
	if executed, ok := DecisionTime(log["executed_at"]); !ok || executed.After(time.Now().Add(time.Minute)) {
		return s.Reject("STAGE_EXECUTION", "验证时间无效")
	}
	return nil
}
func stageExecution(s *kernel.SemanticSession, ref string, check map[string]any) error {
	log, err := s.Doc(ref)
	if err != nil {
		return err
	}
	if err = validStageExecution(s, log); err != nil {
		return err
	}
	if !kernel.ApContains(log["check_ids"], kernel.Text(check["id"])) {
		return s.Reject("STAGE_EXECUTION", "实际验证未覆盖该检查")
	}
	if check["result"] == "not-applicable" && !(log["applicable"] == false && log["reason"] == check["reason"]) {
		return s.Reject("STAGE_EXECUTION", "不适用结论缺少实际影响依据")
	}
	for _, input := range kernel.ApRows(check["inputs"]) {
		matched := false
		for _, bound := range kernel.ApRows(log["input_bindings"]) {
			matched = matched || sameStageRef(s, kernel.Text(input["ref"]), kernel.Text(bound["ref"])) && stageHex(input["digest"]) == stageHex(bound["digest"])
		}
		if !matched {
			return s.Reject("STAGE_EXECUTION", "验证没有绑定当前检查输入")
		}
	}
	return nil
}

// VerifyStageQuality validates an internal execution result during preparation.
// It grants no approval or implementation authority.
func VerifyStageQuality(s *kernel.SemanticSession, ref, checkID, subjectRef string) error {
	if !s.StageGatesEnabled() {
		return s.Reject("CAPABILITY", "质量结果需要四阶段政策")
	}
	log, err := s.Doc(ref)
	if err != nil {
		return err
	}
	for _, key := range []string{"gate_id", "approval_ref", "role_id", "decision", "approver"} {
		if _, exists := log[key]; exists {
			return s.Reject("STAGE_CHILD_APPROVAL", "内部质量结果不能携带批准字段")
		}
	}
	bound, err := s.Bind(subjectRef)
	if err != nil {
		return err
	}
	check := map[string]any{"id": checkID, "result": "passed", "inputs": []any{map[string]any{"ref": subjectRef, "digest": bound.Digest}}}
	return stageExecution(s, ref, check)
}

func stageAuthorization(s *kernel.SemanticSession, ref string, r map[string]any) error {
	a := kernel.ApMap(r["authorization"])
	source := kernel.Text(a["ref"])
	if source == "" || sameStageRef(s, source, ref) {
		return s.Reject("STAGE_AUTHORIZATION", "缺少真实授权或引用自身")
	}
	if err := s.Basis([]any{map[string]any{"ref": source, "digest": a["digest"]}}); err != nil {
		return err
	}
	assets := kernel.ApRows(r["assets"])
	subject := kernel.Text(assets[0]["ref"])
	switch a["kind"] {
	case "user-decision":
		decision, err := s.Doc(source)
		if err != nil {
			return err
		}
		if _, err = ValidateUserDecisionSemantic(s, decision, []map[string]any{{"boundary": r["gate_id"], "subject_ref": subject, "scope": r["scope"]}}); err != nil {
			return err
		}
		var item map[string]any
		for _, row := range kernel.ApRows(kernel.ApMap(decision["request"])["items"]) {
			if row["boundary"] == r["gate_id"] && kernel.ApSetEqual(row["scope"], r["scope"]) && sameStageRef(s, kernel.Text(kernel.ApMap(row["subject"])["ref"]), subject) {
				if item != nil {
					return s.Reject("STAGE_AUTHORIZATION", "阶段决定匹配多个事项，无法确定成果依据")
				}
				item = row
			}
		}
		reviewed := append([]map[string]any{kernel.ApMap(item["subject"])}, kernel.ApRows(item["basis"])...)
		for _, asset := range assets {
			found := false
			expectedVersion := ""
			if doc, e := s.Doc(kernel.Text(asset["ref"])); e == nil && kernel.ContractN(doc["schema_version"]) == 4 {
				if doc["domain_strategy_id"] != nil {
					expectedVersion = kernel.Text(doc["domain_version"])
				}
				if doc["stage_decision_id"] != nil {
					expectedVersion = kernel.Text(doc["package_version"])
				}
			}
			for _, bound := range reviewed {
				found = found || sameStageRef(s, kernel.Text(asset["ref"]), kernel.Text(bound["ref"])) && stageHex(asset["digest"]) == stageHex(bound["digest"]) && (expectedVersion == "" || kernel.Text(bound["version"]) == expectedVersion)
			}
			if !found {
				return s.Reject("STAGE_AUTHORIZATION", "阶段成果未在真实用户决定中展示: "+kernel.Text(asset["ref"]))
			}
		}
	case "continuation":
		if _, err := AssertUserDecisionRequirementSemantic(s, map[string]any{"boundary": r["gate_id"], "subject_ref": subject, "scope": r["scope"], "continuation_ref": source}); err != nil {
			return err
		}
		continuation, err := s.Doc(source)
		if err != nil {
			return err
		}
		bound := append([]map[string]any{kernel.ApMap(continuation["subject"])}, kernel.ApRows(continuation["basis"])...)
		for _, asset := range assets {
			covered := false
			for _, member := range bound {
				covered = covered || sameStageRef(s, kernel.Text(asset["ref"]), kernel.Text(member["ref"])) && stageHex(asset["digest"]) == stageHex(member["digest"])
			}
			if !covered {
				return s.Reject("STAGE_AUTHORIZATION", "阶段成果不在当前延续依据中")
			}
		}
		return nil
	case "stage":
		if r["gate_id"] != "gate.implementation-admitted" {
			return s.Reject("STAGE_AUTHORIZATION", "只有实施准入可自动消费基线阶段授权")
		}
		parent, err := s.Doc(source)
		if err != nil {
			return err
		}
		if parent["gate_id"] != "gate.spec-baseline-approved" || parent["feature_id"] != r["feature_id"] || !kernel.ApSetEqual(parent["scope"], r["scope"]) {
			return s.Reject("STAGE_AUTHORIZATION", "基线不能覆盖当前实施范围")
		}
		return s.Verify("stage-result", source, map[string]string{"gate": "gate.spec-baseline-approved"})
	default:
		return s.Reject("STAGE_AUTHORIZATION", "未知授权来源")
	}
	return nil
}

// VerifyCheckpointStage consumes only the checkpoint's bound immutable result.
func VerifyCheckpointStage(s *kernel.SemanticSession, checkpointRef, gate, assetRef string) error {
	cp, err := s.Doc(checkpointRef)
	if err != nil {
		return err
	}
	if !s.StageGatesEnabled() || kernel.ContractN(cp["schema_version"]) != 2 {
		return s.Reject("STAGE_CHECKPOINT", "当前政策需要显式四阶段 checkpoint2")
	}
	binding := kernel.ApMap(kernel.ApMap(cp["gates"])[gate])
	ref := kernel.Text(binding["record_ref"])
	if ref == "" {
		return s.Reject("STAGE_PENDING", "尚无阶段记录: "+gate)
	}
	return s.Verify("stage-result", ref, map[string]string{"checkpoint": checkpointRef, "gate": gate, "asset-ref": assetRef})
}
