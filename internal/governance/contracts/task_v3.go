package contracts

import (
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func init() {
	kernel.RegisterRoleIndependentValidator("task", verifyRoleIndependentTask)
	kernel.RegisterRoleIndependentValidator("task-candidate", func(s *kernel.SemanticSession, ref string, _ map[string]string) error {
		return verifyRoleIndependentTask(s, ref, map[string]string{"candidate-query": "true"})
	})
}

func verifyRoleIndependentTask(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	task, err := s.Doc(ref)
	if err != nil {
		return err
	}
	if kernel.ContractN(task["schema_version"]) != 3 || s.GovernanceVersion != 2 {
		return s.Reject("TASK_VERSION", "当前政策只接受task3；历史任务须在原政策快照只读核验")
	}
	if err = s.ValidateSchema(kernel.ApprovalTaskSchemaRef, task); err != nil {
		return err
	}
	if task["profile"] != s.Report.Profile {
		return s.Reject("TASK_PROFILE", "任务与当前Profile不符")
	}
	if err = s.ValidateGovernancePolicyBinding(kernel.ApMap(task["policy"])); err != nil {
		return err
	}
	query := opts["candidate-query"] == "true"
	actualRef := ref
	if opts["task-ref"] != "" {
		if task["workflow_status"] != "not-started" || !kernel.ContractPath(opts["task-ref"]) {
			return s.Reject("TASK_ORIGIN", "--task-ref仅运输未开始派发的规范目标路径")
		}
		if _, err = safefs.Path(s.Root, opts["task-ref"]); err != nil {
			return err
		}
		actualRef = opts["task-ref"]
	}
	dispatch := task["workflow_status"] == "not-started"
	if dispatch && (task["verification_status"] == "executed" || len(kernel.ApMap(task["result"])) > 0 || len(kernel.ApRows(task["verification_results"])) > 0) {
		return s.Reject("TASK_STATE", "派发不能冒充实际执行结果")
	}
	identity, err := s.Doc("yss-project.yaml")
	if err != nil {
		return err
	}
	if identity["repository_mode"] != "project-instance" {
		return s.Reject("TASK_SCOPE", "task3草稿派发当前仅支持项目实例；模板维护入口尚未启用")
	}
	if kernel.ApFind(s.Registry["stages"], "id", kernel.Text(task["stage_id"])) == nil {
		return s.Reject("TASK_STAGE", "任务阶段不属于当前Profile注册表")
	}
	contract := kernel.ApMap(task["contract"])
	selectionUnit := kernel.Text(task["work_unit_id"])
	if contract["kind"] == "slice-implementation" {
		selectionUnit = "work-unit.slice-implementation"
	}
	unit := kernel.ApFind(s.Registry["work_units"], "id", selectionUnit)
	if task["execution_state"] == "Worker" && contract["kind"] != "slice-implementation" {
		return s.Reject("TASK_CONTRACT", "业务实现必须绑定已批准Slice")
	}
	if contract["kind"] != "lifecycle-work-unit" && contract["kind"] != "slice-implementation" {
		return s.Reject("CAPABILITY", "task3 Slice生产派发及模板维护尚未启用")
	}
	if unit == nil {
		return s.Reject("TASK_WORK_UNIT", "工作单元不属于当前Profile")
	}
	if !kernel.ApContains([]string{"Explorer", "Drafter", "Worker", "Reviewer", "Verifier"}, kernel.Text(task["execution_state"])) || !kernel.ApContains([]string{"not-started", "active", "paused", "resolved", "failed"}, kernel.Text(task["workflow_status"])) {
		return s.Reject("TASK_STATE", "未知执行阶段或状态")
	}
	if contract["status"] != "issued" {
		return s.Reject("TASK_CONTRACT", "任务合同不是当前issued版本")
	}
	for _, key := range []string{"acceptance", "inputs", "allowed_write_paths", "forbidden_actions", "expected_outputs", "expected_evidence_files", "verification_commands"} {
		if !kernel.ApUnique(task[key]) {
			return s.Reject("TASK_REQUIRED", "任务缺少明确且唯一的"+key)
		}
	}
	if contract["kind"] == "slice-implementation" {
		if _, err = LoadNativeSlice(s, kernel.Text(contract["slice_contract_ref"])); err != nil {
			return err
		}
	}
	root := kernel.Text(kernel.ApMap(task["baseline"])["repository_root"])
	// Repository registration must have come from current Slice bindings, never task self-registration.
	if contract["kind"] != "slice-implementation" && root != s.Root && s.ExternalViews[root] == nil {
		return s.Reject("TASK_BASELINE", "实现仓未由当前合同登记")
	}
	for _, key := range []string{"allowed_write_paths", "expected_evidence_files"} {
		for _, p := range kernel.ApStrings(task[key]) {
			if !kernel.ContractPath(strings.TrimSuffix(p, "/")) {
				return s.Reject("TASK_SCOPE", "路径必须为明确相对路径")
			}
			if _, err = safefs.Path(root, strings.TrimSuffix(p, "/")); err != nil {
				return err
			}
		}
	}
	if err = validateTaskOutputRefs(s, actualRef, task); err != nil {
		return err
	}
	if contract["kind"] != "slice-implementation" {
		err = validateTaskBaseline(s, task)
	}
	if err != nil {
		return err
	}
	bindings := kernel.ApRows(task["input_bindings"])
	seen := map[string]bool{}
	impacts := []string{}
	for _, asset := range bindings {
		input := kernel.Text(asset["ref"])
		if seen[input] || !kernel.ApContains(task["inputs"], input) {
			return s.Reject("TASK_INPUT", "输入摘要重复或不属于当前输入")
		}
		seen[input] = true
		if _, err = s.EvidenceAsset(asset); err != nil {
			return err
		}
		doc, err := s.Doc(input)
		if err != nil {
			return err
		}
		for _, location := range []map[string]any{doc, kernel.ApMap(doc["scope"]), kernel.ApMap(doc["impact_assessment"])} {
			v, present := location["impacted_areas"]
			if !present {
				continue
			}
			if !kernel.ApIsArray(v) || (len(kernel.ApArray(v)) > 0 && !kernel.ApUnique(v)) {
				return s.Reject("TASK_IMPACT", "输入影响必须为无重复的非空字符串数组")
			}
			if len(kernel.ApArray(v)) == 0 {
				continue
			}
			values := kernel.ApStrings(v)
			if _, err = kernel.CompileTaskSelection(s, selectionUnit, values); err != nil {
				return err
			}
			impacts = ContractUnion(impacts, values)
		}
	}
	if len(seen) != len(kernel.ApStrings(task["inputs"])) || !seen[kernel.Text(contract["contract_ref"])] {
		return s.Reject("TASK_INPUT", "当前输入或合同缺少原字节绑定")
	}
	if !kernel.ApUnique(impacts) || !kernel.ApSetEqual(impacts, kernel.ApMap(task["skill_source"])["impacts"]) {
		return s.Reject("TASK_IMPACT", "实际绑定输入未完整支持声明的影响")
	}
	selectionTask := ContractCopy(task)
	selectionTask["work_unit_id"] = selectionUnit
	if err = kernel.ValidateTaskSkillSourceSemantic(s, selectionTask, true); err != nil {
		return err
	}
	candidate := map[string]any{}
	for _, asset := range bindings {
		if asset["ref"] == contract["contract_ref"] {
			candidate = asset
		}
	}
	var changed, protectedPaths []string
	if contract["kind"] == "slice-implementation" {
		candidate, changed, protectedPaths, err = sliceTaskCandidate(s, task)
		if err != nil {
			return err
		}
	}
	if (!dispatch || query) && (task["execution_state"] == "Reviewer" || task["execution_state"] == "Verifier") {
		review := kernel.ApMap(task["review_context"])
		if _, _, _, _, err = trustedTaskImplementation(s, review); err != nil {
			return err
		}
		candidate = kernel.ApMap(review["candidate"])
	}
	if (!dispatch || query) && (task["execution_state"] == "Drafter" || task["execution_state"] == "Explorer") {
		candidate, err = draftExecutionCandidate(s, task)
		protectedPaths = kernel.ApStrings(task["expected_outputs"])
		if err != nil {
			return err
		}
	}
	if !dispatch && !query && kernel.ApContains([]string{"Drafter", "Explorer", "Reviewer", "Verifier"}, kernel.Text(task["execution_state"])) {
		baseline := kernel.ApMap(task["baseline"])
		source, e := TaskBaselineSource(s, root, kernel.Text(baseline["commit"]), kernel.ApStrings(task["allowed_write_paths"]))
		if e != nil {
			return e
		}
		_, changed, err = executionCandidate(s, root, source, kernel.ApStrings(task["allowed_write_paths"]), task["input_bindings"])
		if err != nil {
			return err
		}
	}
	if query {
		if s.Report.Coverage == nil {
			s.Report.Coverage = map[string]any{}
		}
		s.Report.Coverage["task_candidate"] = map[string]any{"candidate": candidate, "read_only": true, "execution_allowed": false, "approval_created": false}
	}
	if query {
		err = nil
	} else if dispatch {
		err = validateTask3Origin(s, actualRef, task, candidate)
	} else {
		err = validateTask3Execution(s, ref, task, candidate, changed, protectedPaths)
	}
	if err != nil {
		return err
	}
	if task["execution_state"] == "Reviewer" || task["execution_state"] == "Verifier" {
		if err = validateTask3Review(s, task, candidate); err != nil {
			return err
		}
	}
	if contract["kind"] == "slice-implementation" {
		s.Report.ExecutionAuthorization = "not-evaluated"
		return nil
	}
	if contract["kind"] != "lifecycle-work-unit" || task["work_unit_id"] == "work-unit.slice-implementation" || unit["scope"] == "template-source" || contract["lifecycle_ref"] == nil || contract["slice_contract_ref"] != nil || contract["maintenance_ref"] != nil {
		return s.Reject("TASK_CONTRACT", "task3生命周期合同引用冲突")
	}
	if !seen[kernel.Text(contract["lifecycle_ref"])] || !seen[kernel.Text(kernel.ApMap(task["convergence"])["convergence_ref"])] {
		return s.Reject("TASK_INPUT", "生命周期与汇合引用缺少当前字节绑定")
	}
	if _, err = s.Bytes(kernel.Text(contract["lifecycle_ref"])); err != nil {
		return err
	}
	if kernel.ApMap(task["convergence"])["parent_work_unit"] != task["work_unit_id"] {
		return s.Reject("TASK_CONTRACT", "任务汇合工作单元冲突")
	}
	// Preparation is an explicit route and never authorizes ready-for-agent or implementation.
	if !kernel.ApContains(kernel.ApMap(s.GovernancePolicy["preparation_policy"])["work_units"], kernel.Text(task["work_unit_id"])) && task["execution_state"] != "Reviewer" && task["execution_state"] != "Verifier" {
		return s.Reject("TASK_PREPARATION", "当前工作单元不是草稿准备路径")
	}
	for _, action := range []string{"user-approval", "ready-for-agent"} {
		if !kernel.ApContains(task["forbidden_actions"], action) {
			return s.Reject("TASK_PREPARATION", "准备任务遗漏禁止动作: "+action)
		}
	}
	s.Report.ExecutionAuthorization = "not-evaluated"
	return nil
}

func validateTask3Origin(s *kernel.SemanticSession, ref string, task, candidate map[string]any) error {
	p := kernel.ApMap(task["provenance"])
	if p["task_ref"] != ref || strings.TrimSpace(kernel.Text(p["tool"])) == "" {
		return s.Reject("TASK_ORIGIN", "派发来源必须属于当前任务")
	}
	actual, err := s.EvidenceFact(kernel.Text(p["execution_ref"]))
	if err != nil {
		return err
	}
	identity := kernel.ApMap(p["identity"])
	if identity["status"] == "unavailable" {
		if len(identity) != 2 || strings.TrimSpace(kernel.Text(identity["reason"])) == "" {
			return s.Reject("TASK_ORIGIN", "缺少稳定身份不可用原因")
		}
	} else if identity["status"] != "available" || len(identity) != 3 || kernel.Text(identity["principal_ref"]) == "" || kernel.Text(identity["session_ref"]) == "" {
		return s.Reject("TASK_ORIGIN", "身份来源非法")
	}
	when, e := time.Parse(time.RFC3339Nano, kernel.Text(actual["dispatched_at"]))
	if e != nil || when.After(time.Now().Add(time.Minute)) || actual["phase"] != "dispatch" {
		return s.Reject("TASK_ORIGIN", "派发必须有真实派发时间和阶段")
	}
	for _, key := range []string{"tool", "task_ref", "identity"} {
		if !kernel.ApEqual(actual[key], p[key]) {
			return s.Reject("TASK_ORIGIN", "来源登记与实际派发不符")
		}
	}
	for key, want := range map[string]any{"candidate": candidate, "input_bindings": task["input_bindings"], "baseline": task["baseline"], "scope": task["allowed_write_paths"], "expected_outputs": task["expected_outputs"]} {
		if !kernel.ApEqual(actual[key], want) {
			return s.Reject("TASK_ORIGIN", "派发未绑定当前"+key)
		}
	}
	for _, key := range []string{"command", "argv", "exit_code", "executed_at", "completed_at", "status", "result"} {
		if actual[key] != nil {
			return s.Reject("TASK_ORIGIN", "派发记录不能冒充实际执行结果")
		}
	}
	return nil
}

func validateTask3Review(s *kernel.SemanticSession, task, candidate map[string]any) error {
	review := kernel.ApMap(task["review_context"])
	actual := kernel.ApMap(review["review"])
	if task["workflow_status"] == "not-started" && actual["mode"] != "self-check" {
		return s.Reject("CAPABILITY", "独立审查执行事实尚未启用；草稿仅支持self-check派发")
	}
	if !kernel.ApEqual(actual["provenance"], task["provenance"]) {
		return s.Reject("TASK_REVIEW", "审查派发来源与当前任务不符")
	}

	if !kernel.ApEqual(review["candidate"], candidate) || !kernel.ApSetEqual(review["check_ids"], kernel.ApMap(task["skill_source"])["check_ids"]) {
		return s.Reject("TASK_REVIEW", "审查未绑定当前候选或检查范围")
	}
	if err := s.ValidateGovernancePolicyBinding(kernel.ApMap(review["policy"])); err != nil {
		return err
	}
	for _, asset := range kernel.ApRows(review["basis"]) {
		if !kernel.ApContains(task["inputs"], kernel.Text(asset["ref"])) {
			return s.Reject("TASK_REVIEW", "审查依据必须属于当前绑定输入")
		}
		if _, err := s.EvidenceAsset(asset); err != nil {
			return err
		}
	}
	for _, path := range kernel.ApStrings(task["allowed_write_paths"]) {
		for _, input := range kernel.ApStrings(task["inputs"]) {
			if kernel.TaskWithinPath(input, path) {
				return s.Reject("TASK_REVIEW", "审查写范围覆盖候选或依据")
			}
		}
	}
	if task["workflow_status"] != "not-started" {
		implementation, implementationRef, scope, protection, err := trustedTaskImplementation(s, review)
		if err != nil {
			return err
		}
		if err = validateReviewCandidateWrites(s, task, protection); err != nil {
			return err
		}
		if !kernel.ApSetEqual(review["approval_scope"], scope) {
			return s.Reject("TASK_REVIEW", "审查范围未绑定实际实施范围")
		}
		return s.ValidateReviewEvidenceDigest(actual, candidate, scope, "self-check", implementation, implementationRef)
	}
	return nil
}
