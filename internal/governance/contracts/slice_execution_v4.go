package contracts

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"strings"
)

func init() {
	kernel.RegisterRoleIndependentValidator("slice-execution-result", verifySlice4ExecutionResult)
}

func verifySlice4ExecutionResult(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	taskRef := opts["task"]
	if taskRef == "" {
		return s.Reject("TASK_RESULT", "Slice4执行结果需要明确的完成task3来源")
	}
	if err := s.Verify("task", taskRef, nil); err != nil {
		return err
	}
	verified := kernel.ApMap(s.Report.Coverage["task_execution"])
	task, err := s.Doc(taskRef)
	if err != nil {
		return err
	}
	contract := kernel.ApMap(task["contract"])
	if verified["task_ref"] != taskRef || verified["completed"] != true || task["execution_state"] != "Worker" || contract["kind"] != "slice-implementation" {
		return s.Reject("TASK_RESULT", "来源必须是已验证完成的实际Slice实施task3")
	}
	if !kernel.ApContains(kernel.ApMap(task["result"])["evidence_refs"], ref) {
		return s.Reject("TASK_RESULT", "执行结果未登记在实际完成任务中")
	}
	c, err := LoadNativeSlice(s, kernel.Text(contract["slice_contract_ref"]))
	if err != nil {
		return err
	}
	result, err := s.Doc(ref)
	if err != nil {
		return err
	}
	if wrapped := kernel.ApMap(result["execution_result"]); len(wrapped) > 0 {
		result = wrapped
	}
	if err = validateSlice4ExecutionResult(s, result, c, task, kernel.ApMap(verified["candidate"])); err != nil {
		return err
	}
	s.Report.Coverage["slice_execution_result"] = map[string]any{"status": "passed", "result_status": "accepted", "blockers": []any{}, "task_ref": taskRef, "candidate": verified["candidate"], "execution_allowed": false}
	return nil
}

func validateSlice4ExecutionResult(s *kernel.SemanticSession, result map[string]any, c *NativeSlice, task, candidate map[string]any) error {
	reject := func(message string) error { return s.Reject("SLICE_RESULT", message) }
	if kernel.ContractN(c.Raw["schema_version"]) != 4 || kernel.ContractN(result["schema_version"]) != 2 || kernel.ContractN(result["evidence_binding_version"]) != 1 || result["work_unit_id"] != task["work_unit_id"] {
		return reject("结果版本或实际工作单元不一致")
	}
	if !kernel.ApContains([]string{"implemented", "seam-deferred", "not-applicable"}, kernel.Text(result["status"])) {
		return reject("结果存在drift/violation或未知状态")
	}
	for _, field := range []string{"changed_files", "evidence_files", "verification_results", "constraint_results", "seam_deferred", "deviations", "new_impacts", "source_bindings"} {
		if !kernel.ApIsArray(result[field]) {
			return reject("结果缺少数组: " + field)
		}
	}
	if len(kernel.ApArray(result["new_impacts"])) > 0 || len(kernel.ApArray(result["deviations"])) > 0 {
		return reject("新影响或尚未消费批准的偏离阻断完成")
	}
	if result["status"] == "not-applicable" && strings.TrimSpace(kernel.Text(result["not_applicable_reason"])) == "" {
		return reject("缺少不适用理由")
	}
	if result["status"] == "seam-deferred" && len(kernel.ApRows(result["seam_deferred"])) == 0 {
		return reject("缺少延期接缝")
	}
	for _, seam := range kernel.ApRows(result["seam_deferred"]) {
		for _, field := range []string{"risk", "owner", "follow_up_ticket", "verification_plan", "target_version_or_release_date"} {
			if strings.TrimSpace(kernel.Text(seam[field])) == "" {
				return reject("延期接缝不完整")
			}
		}
	}
	for _, constraint := range kernel.ApRows(result["constraint_results"]) {
		if constraint["status"] != "passed" && (constraint["status"] != "not-applicable" || strings.TrimSpace(kernel.Text(constraint["reason"])) == "") {
			return reject("约束未通过或缺少不适用理由")
		}
	}
	unit := kernel.ApFind(c.Normalized["work_units"], "id", kernel.Text(task["work_unit_id"]))
	if unit == nil {
		return reject("结果工作单元不属于当前Slice")
	}
	_, _, resolution, err := contractSliceSelect(s, c, map[string]string{"unit": kernel.Text(task["work_unit_id"])})
	if err != nil {
		return err
	}
	consumed := kernel.ApMap(result["consumed_contract"])
	if result["slice_id"] != nil && result["slice_id"] != c.Raw["slice_id"] || consumed["contract_ref"] != nil && consumed["contract_ref"] != c.Ref {
		return reject("结果声明了其他Slice来源")
	}
	if approvalRef := kernel.Text(consumed["approval_ref"]); approvalRef != "" && !kernel.ApContains(kernel.ApMap(task["contract"])["gate_refs"], approvalRef) {
		return reject("结果声明了其他批准来源")
	}

	binding, err := s.Bind(c.Ref)
	if err != nil {
		return err
	}
	if consumed["contract_id"] != c.Raw["contract_id"] || consumed["contract_version"] != c.Raw["contract_version"] || strings.TrimPrefix(kernel.Text(consumed["contract_digest"]), "sha256:") != strings.TrimPrefix(binding.Digest, "sha256:") {
		return reject("结果未绑定当前合同字节和版本")
	}
	for _, field := range []string{"registry_digest", "compiler_contract_digest", "component_bindings_digest"} {
		if !kernel.ApEqual(consumed[field], resolution[field]) {
			return reject("结果编译来源过期: " + field)
		}
	}
	if resolution["architecture_identity"] != nil && !kernel.ApEqual(result["architecture_identity"], resolution["architecture_identity"]) {
		return reject("结果架构身份不一致")
	}
	root := kernel.Text(unit["project_root"])
	changes := []string{}
	for _, item := range kernel.ApArray(result["changed_files"]) {
		ref := kernel.Text(item)
		if obj, ok := kernel.Object(item); ok {
			if obj["project_root"] != root {
				return reject("变化属于其他实现仓")
			}
			ref = kernel.Text(obj["path"])
		} else if len(c.Repositories) > 0 {
			return reject("多仓变化必须绑定实现仓")
		}
		changes = append(changes, ref)
	}
	if !kernel.ApSetEqual(changes, kernel.ApMap(task["result"])["changed_files"]) {
		return reject("结果变化不等于任务实际候选变化")
	}
	work := ContractCopy(kernel.ApMap(unit["work_unit"]))
	work["project_root"] = unit["project_root"]
	if err = ContractExecutionEvidence(s, result, c, work); err != nil {
		return err
	}
	checks := kernel.ApRows(work["verification"])
	if len(kernel.ApRows(result["verification_results"])) != len(checks) {
		return reject("结果包含未声明验证")
	}
	for _, row := range kernel.ApRows(result["verification_results"]) {
		if row["status"] != nil && row["status"] != "passed" {
			return reject("结果验证状态未通过")
		}
		actual := kernel.ApFind(task["verification_results"], "command", kernel.Text(row["command"]))
		if actual == nil {
			return reject("结果命令不属于完成任务")
		}
		for _, field := range []string{"command", "exit_code", "executed_at", "candidate_digest"} {
			if !kernel.ApEqual(row[field], actual[field]) {
				return reject("结果与实际执行不一致: " + field)
			}
		}
	}
	for _, ref := range kernel.ApStrings(work["expected_evidence"]) {
		if kernel.ApFind(result["evidence_files"], "path", ref) == nil {
			return reject("预期证据未完整登记")
		}
	}

	return s.ValidateVerificationResultsDigest(task["verification_results"], kernel.ApStrings(task["verification_commands"]), kernel.Text(candidate["digest"]))
}
