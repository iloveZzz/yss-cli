package contracts

import "github.com/iloveZzz/yss-cli/internal/governance/approval"

import (
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"path/filepath"
	"strings"
)

func init() {
	kernel.RegisterSemanticValidator("task", verifyTaskSemantic)
	kernel.RegisterSemanticValidator("backend-completed-producer-task", verifyBackendCompletedProducerTaskSemantic)
}

// The full task contract and v2 intake rules are applied below; review capability
// validation above is also reused by approval consumers without dispatching work.
func verifyTaskSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	return verifyTaskPackageSemantic(s, ref, opts)
}

// Only an evidence consumer may select this internal read-only audit seam.
// The public task validator retains its terminal recovery prohibition. The
// consumer supplies its checkpoint independently of both task and report.
func verifyBackendCompletedProducerTaskSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	checkpointRef := opts["checkpoint"]
	if checkpointRef == "" || opts["history"] == "true" {
		return kernel.ApFail(s, "TASK_PRODUCER_AUDIT", "完成态后端证据审计需要独立当前 checkpoint")
	}
	task, err := s.Doc(ref)
	if err != nil {
		return err
	}
	result := kernel.ApMap(task["result"])
	contract := kernel.ApMap(task["contract"])
	next, hasNext := result["next_route"]
	if kernel.ApNumber(task["schema_version"]) != 1 || task["work_unit_id"] != "work-unit.backend-delivery" || kernel.ApFind(s.Registry["work_units"], "id", "work-unit.backend-delivery") == nil || contract["kind"] != "lifecycle-work-unit" || contract["status"] != "issued" || task["workflow_status"] != "resolved" || result["result"] != "completed" || result["work_unit"] != task["work_unit_id"] || !hasNext || next != nil {
		return kernel.ApFail(s, "TASK_PRODUCER_AUDIT", "仅可只读审计已完成且没有后续路由的正式后端交付任务")
	}
	if task["checkpoint_ref"] != checkpointRef || result["checkpoint_ref"] != checkpointRef {
		return kernel.ApFail(s, "TASK_PRODUCER_AUDIT", "任务与结果必须精确绑定消费者当前 checkpoint")
	}
	if err = s.Verify("checkpoint", checkpointRef, nil); err != nil {
		return err
	}
	checkpoint, err := s.Doc(checkpointRef)
	if err != nil {
		return err
	}
	if checkpoint["stage"] != task["stage_id"] || kernel.ApMap(checkpoint["stage_trace"])["completed_work_unit"] != "work-unit.backend-delivery" || checkpoint["next_work_unit"] != nil || checkpoint["mode"] != "audit" {
		return kernel.ApFail(s, "TASK_PRODUCER_AUDIT", "当前 checkpoint 未独立声明只读后端终点审计范围")
	}
	authorization, err := dependencies.ProgressionBackendAuthorization(s, checkpointRef, "", true)
	if err != nil {
		return err
	}
	terminalRef := authorization.TerminalRef
	if err = s.Verify("backend-terminal", terminalRef, map[string]string{"checkpoint": checkpointRef}); err != nil {
		return err
	}
	terminal, err := s.Doc(terminalRef)
	if err != nil {
		return err
	}
	for _, key := range []string{"delivery", "review_state"} {
		binding := kernel.ApMap(terminal[key])
		if err = taskReadable(s, kernel.ApText(binding["ref"]), false, kernel.ApText(binding["digest"])); err != nil {
			return err
		}
		if !kernel.ApContains(task["expected_evidence_files"], kernel.ApText(binding["ref"])) || !kernel.ApContains(result["evidence_refs"], kernel.ApText(binding["ref"])) {
			return kernel.ApFail(s, "TASK_PRODUCER_AUDIT", "完成任务没有声明当前终点的独立 "+key+" 来源证据")
		}
	}
	if !kernel.ApContains(task["expected_evidence_files"], terminalRef) || !kernel.ApContains(result["evidence_refs"], terminalRef) {
		return kernel.ApFail(s, "TASK_PRODUCER_AUDIT", "完成任务缺少固定后端终点证据")
	}
	if err = verifyTaskPackageSemanticMode(s, ref, opts, true); err != nil {
		return err
	}
	s.Report.Applicability = append(s.Report.Applicability, map[string]any{"id": "backend-completed-producer-task", "status": "read-only-audit", "recovery_authorized": false, "implementation_authorized": false, "source": ref, "checkpoint": checkpointRef})
	return nil
}

// The caller selects the collection independently. This validates each current
// file before enforcing the same-Slice identity and Worker/Reviewer separation.
// Full CI groups unrelated Slice contracts before calling this legacy set seam.
func validateTaskPackageSetSemantic(s *kernel.SemanticSession, refs []string) error {
	if len(refs) == 0 {
		return kernel.ApFail(s, "TASK_SET", "任务包集合不能为空")
	}
	packages := make([]map[string]any, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref == "" || seen[ref] {
			return kernel.ApFail(s, "TASK_SET", "任务引用为空或重复")
		}
		seen[ref] = true
		if err := s.Verify("task", ref, nil); err != nil {
			return err
		}
		task, err := s.Doc(ref)
		if err != nil {
			return err
		}
		packages = append(packages, task)
	}
	return ValidateTaskActorSetSemantic(s, packages)
}

// contract verification may use this coupling check after its independent
// dispatch binding checks, avoiding task → Slice → task verification recursion.
func ValidateTaskActorSetSemantic(s *kernel.SemanticSession, packages []map[string]any) error {
	if len(packages) == 0 {
		return kernel.ApFail(s, "TASK_SET", "任务包集合不能为空")
	}
	var identity []byte
	workers := map[string]bool{}
	for _, task := range packages {
		contract := kernel.ApMap(task["contract"])
		if kernel.ApText(contract["kind"]) != "slice-implementation" {
			continue
		}
		current := kernel.ApCanonical([]any{contract["contract_id"], contract["contract_version"]})
		if identity == nil {
			identity = current
		} else if string(identity) != string(current) {
			return kernel.ApFail(s, "TASK_SET_CONTRACT", "同一切片任务集合必须消费同一合同ID和版本")
		}
		if kernel.ApText(task["execution_state"]) == "Worker" {
			workers[kernel.ApText(task["actor_id"])] = true
		}
	}
	for _, task := range packages {
		if kernel.ApText(kernel.ApMap(task["contract"])["kind"]) == "slice-implementation" && kernel.ApText(task["execution_state"]) == "Reviewer" && workers[kernel.ApText(task["actor_id"])] {
			return kernel.ApFail(s, "REVIEW_NOT_INDEPENDENT", "切片Reviewer不得与Worker使用同一actor_id")
		}
	}
	return nil
}

func taskReadable(s *kernel.SemanticSession, ref string, maintenance bool, digest string) error {
	if strings.TrimSpace(ref) == "" {
		return kernel.ApFail(s, "TASK_REFERENCE", "任务引用缺失")
	}
	if strings.HasPrefix(ref, "maintenance:") && !maintenance {
		return kernel.ApFail(s, "TASK_REFERENCE", "maintenance引用仅适用于模板维护")
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return nil
	}
	b, e := s.Bytes(ref)
	if e != nil {
		return e
	}
	if digest != "" && kernel.ApDigest(b) != digest && strings.TrimPrefix(kernel.ApDigest(b), "sha256:") != digest {
		return kernel.ApFail(s, "TASK_EVIDENCE_DRIFT", "证据摘要不一致: "+ref)
	}
	return nil
}

func taskSafePath(s *kernel.SemanticSession, ref string, maintenance bool) error {
	if strings.HasPrefix(ref, "maintenance:") {
		if !maintenance {
			return kernel.ApFail(s, "TASK_SCOPE", "maintenance写范围仅适用于模板维护")
		}
		_, _, e := s.ReferenceView(ref)
		return e
	}
	// 写范围条目允许以单个「/」结尾表示目录（模板生成的任务包就是这样写的）；其余仍按可移植项目相对路径校验。
	checked := ref
	if len(checked) > 1 && strings.HasSuffix(checked, "/") && !strings.HasSuffix(checked, "//") {
		checked = strings.TrimSuffix(checked, "/")
	}
	if _, e := safefs.Path(s.Root, checked); e != nil {
		return kernel.ApFail(s, "TASK_SCOPE", e.Error())
	}
	return nil
}

func taskDeliveryProfile(s *kernel.SemanticSession) (map[string]any, error) {
	metadata := []string{}
	var native map[string]any
	if present, e := s.Exists(".yss.json"); e != nil {
		return nil, e
	} else if present {
		native, e = s.Doc(".yss.json")
		if e != nil {
			return nil, e
		}
	}
	for _, side := range []string{"backend"} {
		ok, e := s.Exists(".yss-harness-" + side + ".json")
		if e != nil {
			return nil, e
		}
		if ok || native["profile"] == side {
			metadata = append(metadata, side)
		}
	}
	if len(metadata) > 1 {
		return nil, kernel.ApFail(s, "TASK_SCOPE", "专职Harness metadata冲突")
	}
	exists, e := s.Exists(".template-spec/process/harness-profile.yaml")
	if e != nil {
		return nil, e
	}
	if !exists {
		if len(metadata) > 0 {
			return nil, kernel.ApFail(s, "TASK_SCOPE", "专职Harness缺少profile")
		}
		return nil, nil
	}
	profile, e := s.Doc(".template-spec/process/harness-profile.yaml")
	if e != nil {
		return nil, e
	}
	if len(metadata) == 1 {
		side := metadata[0]
		record := native
		if native == nil {
			var e error
			record, e = s.Doc(".yss-harness-" + side + ".json")
			if e != nil {
				return nil, e
			}
		}
		expected := "harness." + side + "-delivery"
		newIdentity := kernel.ApNumber(record["metadataSchemaVersion"]) == 2 && record["schema_version"] == nil && record["profile_id"] == nil && record["profileId"] == expected && record["templateSource"] == "github:iloveZzz/yss-harness-"+side+"-agent" && kernel.ApMap(profile["instantiation"])["cli_package"] == "create-yss-harness-"+side
		oldIdentity := kernel.ApNumber(record["schema_version"]) == 1 && record["metadataSchemaVersion"] == nil && record["profile_id"] == expected
		nativeIdentity := native != nil && native["profileId"] == expected && kernel.ApMap(profile["instantiation"])["cli_package"] == "yss" && kernel.ApMap(profile["instantiation"])["metadata_file"] == ".yss.json"
		if (!nativeIdentity && !newIdentity && !oldIdentity) || profile["profile_id"] != expected {
			return nil, kernel.ApFail(s, "TASK_SCOPE", "专职Harness metadata与profile不一致")
		}
	}
	return profile, nil
}

func enforceHarnessTaskSemantic(s *kernel.SemanticSession, task map[string]any) error {
	return enforceHarnessTaskSemanticMode(s, task, false)
}

func enforceHarnessTaskSemanticMode(s *kernel.SemanticSession, task map[string]any, completedProducerAudit bool) error {
	identity, e := s.Doc("yss-project.yaml")
	if e != nil {
		return e
	}
	hasScope, e := s.Exists(".yss-execution-scope.yaml")
	if e != nil {
		return e
	}
	plugin, e := s.BackendPluginBinding()
	if e != nil {
		return e
	}
	if !hasScope {
		if kernel.ApContains([]string{"yss-plan-to-backend", "yss-backend-delivery"}, kernel.ApText(plugin["plugin"])) {
			return kernel.ApFail(s, "TASK_SCOPE", "插件项目缺少职责范围")
		}
		if kernel.ApText(task["work_unit_id"]) == "work-unit.backend-delivery" {
			if _, e := dependencies.ProgressionBackendAuthorization(s, kernel.ApText(task["checkpoint_ref"]), "", false); e != nil {
				return e
			}
		}
	} else {
		scope, e := s.Doc(".yss-execution-scope.yaml")
		if e != nil {
			return e
		}
		if kernel.ApNumber(identity["schema_version"]) != 1 || kernel.ApText(identity["repository_mode"]) != "project-instance" || kernel.ApNumber(scope["schema_version"]) != 1 || kernel.ApText(scope["scope_id"]) != "plan-to-backend" || len(scope) != 2 {
			return kernel.ApFail(s, "TASK_SCOPE", "未知或被扩大职责配置")
		}
		if plugin != nil && (!kernel.ApContains([]string{"yss-plan-to-backend", "yss-backend-delivery"}, kernel.ApText(plugin["plugin"])) || plugin["execution_scope"] != scope["scope_id"]) {
			return kernel.ApFail(s, "TASK_SCOPE", "插件与职责绑定不一致")
		}
		contract, e := s.Doc(".template-spec/process/orchestration-contract.yaml")
		if e != nil {
			return e
		}
		policy := kernel.ApMap(kernel.ApMap(contract["execution_scopes"])["plan-to-backend"])
		unit := kernel.ApText(task["work_unit_id"])
		if kernel.ApText(kernel.ApMap(task["contract"])["kind"]) == "slice-implementation" {
			unit = "work-unit.slice-implementation"
		}
		if kernel.ApText(policy["terminal_work_unit"]) != "work-unit.backend-delivery" || !kernel.ApContains(policy["allowed_work_units"], unit) {
			return kernel.ApFail(s, "TASK_SCOPE", "职责范围不允许当前工作单元")
		}
		terminal, e := s.Exists(".yss-backend-delivery.json")
		if e != nil {
			return e
		}
		if terminal {
			if e = s.Verify("backend-terminal", ".yss-backend-delivery.json", nil); e != nil {
				return e
			}
			if kernel.ApText(kernel.ApMap(task["contract"])["kind"]) != "read-only-intake" && !completedProducerAudit {
				return kernel.ApFail(s, "TASK_SCOPE", "后端交付已完成，不能恢复实现")
			}
		}
		if kernel.ApText(task["role_id"]) == "role.frontend-engineer" {
			return kernel.ApFail(s, "TASK_SCOPE", "后端职责不能派发前端实现")
		}
		for _, ref := range kernel.ApStrings(task["allowed_write_paths"]) {
			if ref == "apps/frontend" || strings.HasPrefix(ref, "apps/frontend/") {
				return kernel.ApFail(s, "TASK_SCOPE", "后端职责禁止前端写入")
			}
		}
	}
	profile, e := taskDeliveryProfile(s)
	if e != nil {
		return e
	}
	id := kernel.ApText(profile["profile_id"])
	if kernel.ApText(identity["repository_mode"]) != "project-instance" || id != "harness.backend-delivery" {
		return nil
	}
	if kernel.ApText(kernel.ApMap(task["contract"])["kind"]) == "template-maintenance" {
		return kernel.ApFail(s, "TASK_SCOPE", "专职产品项目不得派发模板维护")
	}

	if kernel.ApContains([]string{"role.requirements-manager", "role.product-manager"}, kernel.ApText(task["role_id"])) {
		analysisUnits := []string{"work-unit.plan-opportunity", "work-unit.plan-requirements", "work-unit.domain-strategy-design", "work-unit.stage-decision", "work-unit.spec-synthesis"}
		if kernel.ApText(task["execution_state"]) == "Worker" || !kernel.ApContains(analysisUnits, kernel.ApText(task["work_unit_id"])) {
			return kernel.ApFail(s, "TASK_SCOPE", "业务分析角色仅能起草/审查本端分析与产品设计，不能实现生产代码")
		}
	}
	crossReview := kernel.ApContains([]string{"Explorer", "Reviewer"}, kernel.ApText(task["execution_state"])) && len(kernel.ApArray(task["allowed_write_paths"])) == 0
	audience := kernel.ApMap(profile["audience"])
	if !crossReview && !kernel.ApContains(audience["target_user_roles"], kernel.ApText(task["role_id"])) && !kernel.ApContains(audience["control_plane_roles"], kernel.ApText(task["role_id"])) {
		return kernel.ApFail(s, "TASK_SCOPE", "专职Harness不能派发另一端实现/起草")
	}
	for _, ref := range kernel.ApStrings(task["allowed_write_paths"]) {
		if ref == "apps/frontend" || strings.HasPrefix(ref, "apps/frontend/") {
			return kernel.ApFail(s, "TASK_SCOPE", "专职Harness不能写入另一端工程")
		}
	}
	return nil
}

func verifySpecTaskPlanEntry(s *kernel.SemanticSession, task map[string]any) error {
	checkpointRef := kernel.First(kernel.ApText(task["checkpoint_ref"]), kernel.ApText(kernel.ApMap(task["contract"])["lifecycle_ref"]))
	checkpoint, err := s.Doc(checkpointRef)
	if err != nil {
		return err
	}
	for _, key := range []string{"feature_id", "plan_review_ref", "plan_approval_ref", "plan_user_decision_ref", "plan_continuation_ref"} {
		if !kernel.ApEqual(task[key], checkpoint[key]) {
			return kernel.ApFail(s, "TASK_PLAN_CONTEXT", "Spec 任务的 "+key+" 与当前 checkpoint 不一致")
		}
	}
	return s.Verify("plan-spec-entry", checkpointRef, nil)
}

func verifyTaskPackageSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	return verifyTaskPackageSemanticMode(s, ref, opts, false)
}

func verifyTaskPackageSemanticMode(s *kernel.SemanticSession, ref string, opts map[string]string, completedProducerAudit bool) error {
	task, e := s.Doc(ref)
	if e != nil {
		return e
	}
	if e = s.ValidateSchema(kernel.ApprovalTaskSchemaRef, task); e != nil {
		return e
	}
	if e = enforceHarnessTaskSemanticMode(s, task, completedProducerAudit); e != nil {
		return e
	}
	if e = kernel.ValidateTaskSkillSourceSemantic(s, task, true); e != nil {
		return e
	}
	if kernel.ApNumber(task["schema_version"]) == 2 {
		return validateReadOnlyIntakeSemantic(s, ref, task)
	}
	history := opts["history"] == "true"
	contract := kernel.ApMap(task["contract"])
	if history {
		identity, e := s.Doc("yss-project.yaml")
		if e != nil {
			return e
		}
		if kernel.ApText(identity["repository_mode"]) != "template-source" || kernel.ApText(contract["kind"]) != "template-maintenance" {
			return kernel.ApFail(s, "TASK_HISTORY", "历史兼容仅适用于模板维护schema v1")
		}
	}
	unit := kernel.ApFind(s.Registry["work_units"], "id", kernel.ApText(task["work_unit_id"]))
	if unit == nil && kernel.ApText(contract["kind"]) != "slice-implementation" {
		return kernel.ApFail(s, "TASK_WORK_UNIT", "未知work_unit_id")
	}
	if kernel.ApFind(s.Roles["runtimes"], "id", kernel.ApText(task["runtime_id"])) == nil {
		return kernel.ApFail(s, "TASK_RUNTIME", "未知runtime_id")
	}
	if !kernel.ApContains([]string{"Explorer", "Drafter", "Worker", "Reviewer", "Verifier"}, kernel.ApText(task["execution_state"])) || !kernel.ApContains([]string{"not-started", "active", "paused", "resolved", "failed"}, kernel.ApText(task["workflow_status"])) {
		return kernel.ApFail(s, "TASK_STATE", "执行态或workflow_status非法")
	}
	if kernel.ApText(task["stage_id"]) != "" {
		stages := kernel.TaskRole(s, kernel.ApText(task["role_id"]))["stages"]
		if kernel.ApMap(task["review_context"])["capability_ids"] != nil {
			compiled, e := kernel.CompileReviewCapabilitiesSemantic(s, kernel.ApMap(task["review_context"])["check_ids"], kernel.ApText(task["role_id"]), kernel.ApText(task["execution_state"]))
			if e != nil {
				return e
			}
			stages = compiled.Stages
		}
		if kernel.ApFind(s.Registry["stages"], "id", kernel.ApText(task["stage_id"])) == nil || !kernel.ApContains(stages, kernel.ApText(task["stage_id"])) {
			return kernel.ApFail(s, "TASK_STAGE", "角色未覆盖当前阶段")
		}
	}
	if kernel.ApMap(task["review_context"])["capability_ids"] != nil {
		if _, e = kernel.ValidateReviewTaskSemantic(s, ref, task, nil, "", false); e != nil {
			return e
		}
	}
	maintenance := kernel.ApText(contract["kind"]) == "template-maintenance"
	for _, allowed := range kernel.ApStrings(task["allowed_write_paths"]) {
		if e = taskSafePath(s, allowed, maintenance); e != nil {
			return e
		}
	}
	if kernel.ApText(task["execution_state"]) == "Reviewer" && kernel.ApMap(task["review_context"])["implementation_actor_id"] == task["actor_id"] {
		return kernel.ApFail(s, "REVIEW_NOT_INDEPENDENT", "Reviewer与实现者actor相同")
	}
	if e = dependencies.ProgressionTaskEntry(s, task); e != nil {
		return e
	}
	if len(kernel.ApArray(task["allowed_write_paths"])) > 0 && kernel.ApText(contract["kind"]) == "lifecycle-work-unit" {
		if e = s.Verify("tracking-entry", ref, map[string]string{"checkpoint": kernel.ApText(task["checkpoint_ref"])}); e != nil {
			return e
		}
	}
	if kernel.ApText(task["work_unit_id"]) == "work-unit.spec-synthesis" {
		if e = verifySpecTaskPlanEntry(s, task); e != nil {
			return e
		}
	}
	for _, row := range kernel.ApRows(task["verification_results"]) {
		if !kernel.ApContains(task["verification_commands"], kernel.ApText(row["command"])) {
			return kernel.ApFail(s, "TASK_VERIFICATION", "验证结果命令未声明")
		}
		if strings.HasPrefix(kernel.ApText(row["evidence_ref"]), "maintenance:") && row["evidence_digest"] == nil {
			return kernel.ApFail(s, "TASK_EVIDENCE", "仓外维护证据须绑定摘要")
		}
		if e = taskReadable(s, kernel.ApText(row["evidence_ref"]), maintenance, kernel.ApText(row["evidence_digest"])); e != nil {
			return e
		}
	}
	result := kernel.ApMap(task["result"])
	completed := kernel.ApText(task["workflow_status"]) == "resolved" || kernel.ApText(result["result"]) == "completed"
	if completed {
		if kernel.ApText(task["workflow_status"]) != "resolved" || kernel.ApText(result["result"]) != "completed" {
			return kernel.ApFail(s, "TASK_RESULT", "完成状态与result不一致")
		}
		for _, field := range []string{"result_schema", "work_unit", "workflow_reference", "skill", "changed_files", "context_reconciliation", "evidence_refs", "deferred_seams", "drift", "violation", "new_impacts", "stale_candidates", "next_route", "blocking_signals"} {
			if _, ok := result[field]; !ok {
				return kernel.ApFail(s, "TASK_RESULT", "完成结果缺少: "+field)
			}
		}
		if kernel.ApText(result["result_schema"]) != "workflow-execution-result-v1" || result["work_unit"] != task["work_unit_id"] {
			return kernel.ApFail(s, "TASK_RESULT", "结果schema或work-unit不一致")
		}
		reconciliation := kernel.ApMap(result["context_reconciliation"])
		want := "reconciled"
		if maintenance {
			want = "not-applicable"
		}
		if kernel.ApText(reconciliation["status"]) != want || maintenance && strings.TrimSpace(kernel.ApText(reconciliation["reason"])) == "" {
			return kernel.ApFail(s, "TASK_CONTEXT", "完成结果缺少当前context reconciliation")
		}
		if e = taskReadable(s, kernel.ApText(reconciliation["ref"]), maintenance, ""); e != nil {
			return e
		}
		if !kernel.ApContains(result["evidence_refs"], kernel.ApText(reconciliation["ref"])) {
			return kernel.ApFail(s, "TASK_CONTEXT", "context证据未包含result.evidence_refs")
		}
		next := kernel.ApText(result["next_route"])
		if e = s.Verify("next-route", ref, map[string]string{"current": kernel.ApText(task["work_unit_id"]), "next": next, "checkpoint": kernel.ApText(task["checkpoint_ref"])}); e != nil {
			return e
		}
		for _, expected := range kernel.ApStrings(task["expected_evidence_files"]) {
			if e = taskReadable(s, expected, maintenance, ""); e != nil {
				return e
			}
		}
		if len(kernel.ApArray(task["verification_results"])) == 0 {
			return kernel.ApFail(s, "TASK_VERIFICATION", "完成任务缺少实际验证")
		}
		covered := map[string]bool{}
		for _, row := range kernel.ApRows(task["verification_results"]) {
			if kernel.ApNumber(row["exit_code"]) != 0 {
				return kernel.ApFail(s, "TASK_VERIFICATION", "完成任务有失败验证")
			}
			covered[kernel.ApText(row["command"])] = true
		}
		for _, command := range kernel.ApStrings(task["verification_commands"]) {
			if !covered[command] {
				return kernel.ApFail(s, "TASK_VERIFICATION", "未覆盖全部verification_commands")
			}
		}
		for _, field := range []string{"new_impacts", "stale_candidates", "blocking_signals", "drift", "violation"} {
			if len(kernel.ApArray(result[field])) > 0 {
				return kernel.ApFail(s, "TASK_RESULT", "completed仍含阻断: "+field)
			}
		}
		if len(kernel.ApArray(result["evidence_refs"])) == 0 {
			return kernel.ApFail(s, "TASK_EVIDENCE", "completed evidence_refs为空")
		}
		for _, evidence := range kernel.ApStrings(result["evidence_refs"]) {
			if e = taskReadable(s, evidence, maintenance, ""); e != nil {
				return e
			}
		}
	}
	for _, changed := range kernel.ApStrings(result["changed_files"]) {
		if e = taskSafePath(s, changed, maintenance); e != nil {
			return e
		}
		covered := false
		for _, allowed := range kernel.ApStrings(task["allowed_write_paths"]) {
			covered = covered || kernel.TaskWithinPath(changed, allowed)
		}
		if !covered {
			return kernel.ApFail(s, "TASK_SCOPE", "changed_files超出allowed_write_paths: "+changed)
		}
	}
	return validateTaskContractSemantic(s, ref, task, unit, history)
}

func validateTaskContractSemantic(s *kernel.SemanticSession, ref string, task, unit map[string]any, history bool) error {
	contract := kernel.ApMap(task["contract"])
	kind := kernel.ApText(contract["kind"])
	if !kernel.ApContains([]string{"lifecycle-work-unit", "slice-implementation", "template-maintenance", "read-only-intake"}, kind) || !kernel.ApContains([]string{"issued", "stale", "blocked"}, kernel.ApText(contract["status"])) || kernel.ApText(contract["status"]) == "stale" && kernel.ApText(task["workflow_status"]) != "paused" {
		return kernel.ApFail(s, "TASK_CONTRACT", "未知、过期或未暂停合同")
	}
	if e := taskReadable(s, kernel.ApText(contract["contract_ref"]), kind == "template-maintenance", ""); e != nil {
		return e
	}
	if kind == "lifecycle-work-unit" {
		if kernel.ApText(task["work_unit_id"]) == "work-unit.slice-implementation" || kernel.ApText(kernel.ApMap(task["convergence"])["parent_work_unit"]) == "work-unit.slice-implementation" || kernel.ApText(unit["scope"]) == "template-source" || kernel.ApTruthy(contract["slice_contract_ref"]) || kernel.ApTruthy(contract["maintenance_ref"]) {
			return kernel.ApFail(s, "TASK_CONTRACT", "lifecycle合同不得冒充切片或模板维护")
		}
		return taskReadable(s, kernel.ApText(contract["lifecycle_ref"]), false, "")
	}
	if kind == "template-maintenance" {
		identity, e := s.Doc("yss-project.yaml")
		if e != nil {
			return e
		}
		if kernel.ApText(identity["repository_mode"]) != "template-source" || kernel.ApText(unit["scope"]) != "template-source" || kernel.ApTruthy(contract["slice_contract_ref"]) || kernel.ApTruthy(contract["lifecycle_ref"]) {
			return kernel.ApFail(s, "TASK_CONTRACT", "模板维护仓库、unit或合同引用不一致")
		}
		checkpointRef := kernel.ApText(contract["maintenance_ref"])
		checkpoint, e := s.Doc(checkpointRef)
		if e != nil {
			return e
		}
		if e = s.Verify("maintenance-checkpoint", checkpointRef, map[string]string{"history": fmt.Sprint(history), "allow-pending-review": fmt.Sprint(kernel.ApText(task["execution_state"]) == "Reviewer" && kernel.ApText(task["workflow_status"]) != "resolved")}); e != nil {
			return e
		}
		if kernel.ApNumber(checkpoint["schema_version"]) == 2 && kernel.ApText(task["execution_state"]) == "Reviewer" {
			if kernel.ApText(checkpoint["current_state"]) != "review-ready" {
				return kernel.ApFail(s, "TASK_CONTRACT", "Reviewer仅能绑定review-ready")
			}
			for _, field := range []string{"candidate_kind", "candidate_requirement", "candidate_digest", "review_axis", "review_round", "applicable_rule_refs"} {
				if contract[field] == nil {
					return kernel.ApFail(s, "TASK_CONTRACT", "Reviewer缺少 "+field)
				}
			}
			if !kernel.ApEqual(contract["candidate_digest"], checkpoint["candidate_digest"]) || !kernel.ApEqual(contract["review_round"], checkpoint["review_round"]) || len(kernel.ApArray(task["allowed_read_paths"])) == 0 || !kernel.ApContains(task["allowed_read_paths"], checkpointRef) {
				return kernel.ApFail(s, "TASK_CONTRACT", "Reviewer候选摘要、轮次或读取范围不匹配")
			}
			for _, path := range kernel.ApStrings(task["allowed_read_paths"]) {
				if e = taskReadable(s, path, true, ""); e != nil {
					return e
				}
			}
			for _, rule := range kernel.ApStrings(contract["applicable_rule_refs"]) {
				if e = taskReadable(s, rule, false, ""); e != nil {
					return e
				}
			}
		}
		return nil
	}
	if kind != "slice-implementation" {
		return kernel.ApFail(s, "TASK_CONTRACT", "v1 read-only-intake非法")
	}
	stages := []string{}
	for _, stage := range kernel.ApRows(s.Registry["stages"]) {
		if kernel.ApContains([]string{"stage.vertical-slice-implementation", "stage.slice-implementation"}, kernel.ApText(stage["id"])) {
			stages = append(stages, kernel.ApText(stage["id"]))
		}
	}
	if len(stages) != 1 {
		return kernel.ApFail(s, "TASK_CONTRACT", "接收端注册表缺少唯一 Slice 实现阶段")
	}
	if kernel.ApTruthy(contract["lifecycle_ref"]) || kernel.ApTruthy(contract["maintenance_ref"]) || kernel.ApText(task["stage_id"]) != stages[0] || kernel.ApText(kernel.ApMap(task["convergence"])["parent_work_unit"]) != "work-unit.slice-implementation" {
		return kernel.ApFail(s, "TASK_CONTRACT", "切片合同阶段或汇合不匹配")
	}
	sliceRef := kernel.ApText(contract["slice_contract_ref"])
	c, e := LoadNativeSlice(s, sliceRef)
	if e != nil {
		return e
	}
	slice := c.Normalized
	options := map[string]string{"checkpoint": kernel.ApText(task["checkpoint_ref"])}
	if kernel.ContractN(c.Raw["schema_version"]) == 3 {
		options, e = sliceV3TaskVerificationOptions(s, task, sliceRef)
		if e != nil {
			return e
		}
	}
	if e = s.Verify("slice", sliceRef, options); e != nil {
		return e
	}
	if !kernel.ApEqual(slice["contract_id"], contract["contract_id"]) || !kernel.ApEqual(slice["contract_version"], contract["contract_version"]) || kernel.ApText(slice["status"]) != "approved" {
		return kernel.ApFail(s, "TASK_CONTRACT", "Slice必须为当前approved版本")
	}
	sliceUnit := kernel.ApFind(slice["work_units"], "id", kernel.ApText(task["work_unit_id"]))
	if sliceUnit == nil {
		return kernel.ApFail(s, "TASK_CONTRACT", "Slice缺少work-unit")
	}
	if kernel.ApNumber(c.Raw["schema_version"]) == 3 || kernel.ApNumber(kernel.ApMap(c.Raw["slice_contract"])["schema_version"]) == 3 {
		if e = validateSliceV3TaskSemantic(s, task, slice, sliceUnit); e != nil {
			return e
		}
	} else {
		for _, field := range []string{"role_id", "runtime_id", "task_package_ref", "contract_id", "contract_version", "allowed_write_paths"} {
			if sliceUnit[field] == nil {
				return kernel.ApFail(s, "TASK_CONTRACT", "Slice工作单元缺少 "+field)
			}
		}
		for _, field := range []string{"role_id", "runtime_id"} {
			if task[field] != sliceUnit[field] {
				return kernel.ApFail(s, "TASK_CONTRACT", "Slice任务身份不匹配")
			}
		}
		if !kernel.ApEqual(sliceUnit["contract_id"], contract["contract_id"]) || !kernel.ApEqual(sliceUnit["contract_version"], contract["contract_version"]) {
			return kernel.ApFail(s, "TASK_CONTRACT", "Slice工作单元合同不匹配")
		}
		for _, allowed := range kernel.ApStrings(task["allowed_write_paths"]) {
			if !kernel.ApContains(sliceUnit["allowed_write_paths"], allowed) {
				return kernel.ApFail(s, "TASK_SCOPE", "写范围未获Slice授权")
			}
		}
		dispatchRef := kernel.ApText(sliceUnit["task_package_ref"])
		if dispatchRef != kernel.ApText(task["work_unit_id"]) {
			dispatch, e := s.Doc(dispatchRef)
			if e != nil {
				return e
			}
			for _, field := range []string{"task_id", "work_unit_id", "actor_id", "role_id", "runtime_id", "contract", "allowed_write_paths"} {
				if !kernel.ApEqual(dispatch[field], task[field]) {
					return kernel.ApFail(s, "TASK_CONTRACT", "派发文件身份、合同或写范围不一致")
				}
			}
		}
	}
	if kernel.ApText(task["execution_state"]) == "Worker" {
		return AssertImplementationDecisionSemantic(s, map[string]any{"slice_contract_ref": sliceRef, "vertical_slice_ticket_ref": kernel.ApMap(slice["lifecycle_refs"])["ticket"], "user_decisions": task["user_decisions"]})
	}
	return nil
}

func validateSliceV3TaskSemantic(s *kernel.SemanticSession, task, slice, unit map[string]any) error {
	role := kernel.TaskRole(s, kernel.ApText(task["role_id"]))
	if role == nil || unit["role_id"] != task["role_id"] || !kernel.ApContains(role["stages"], kernel.ApText(task["stage_id"])) {
		return kernel.ApFail(s, "TASK_CONTRACT", "Slice v3接收角色不支持当前任务")
	}
	for _, skill := range append([]string{kernel.ApText(unit["primary_skill"])}, kernel.ApStrings(unit["supporting_skills"])...) {
		if kernel.ApContains(role["forbidden_skills"], skill) {
			return kernel.ApFail(s, "TASK_SKILL", "Slice工作单元使用禁用Skill")
		}
	}
	options, e := sliceV3TaskVerificationOptions(s, task, kernel.ApText(kernel.ApMap(task["contract"])["slice_contract_ref"]))
	if e != nil {
		return e
	}
	if e := s.Verify("slice", kernel.ApText(kernel.ApMap(task["contract"])["slice_contract_ref"]), options); e != nil {
		return e
	}
	for _, allowed := range kernel.ApStrings(task["allowed_write_paths"]) {
		covered := false
		for _, parent := range kernel.ApStrings(unit["allowed_write_paths"]) {
			covered = covered || kernel.TaskWithinPath(allowed, parent)
		}
		if !covered {
			return kernel.ApFail(s, "TASK_SCOPE", "任务写范围超出Slice v3")
		}
	}
	work := kernel.ApMap(unit["work_unit"])
	for _, pair := range [][2]string{{"verification_commands", "verification_commands"}, {"expected_evidence_files", "expected_evidence"}} {
		if !kernel.ApArraysEqual(task[pair[0]], work[pair[1]]) {
			return kernel.ApFail(s, "TASK_CONTRACT", "任务遗漏或替换Slice v3 "+pair[0])
		}
	}
	for _, pattern := range kernel.ApStrings(work["forbidden_patterns"]) {
		if !kernel.ApContains(task["forbidden_actions"], pattern) {
			return kernel.ApFail(s, "TASK_CONTRACT", "任务遗漏全局约束")
		}
	}
	return nil
}

// A Slice's original approval checkpoint and its current feature checkpoint
// are different identities. Public v3 tasks bind the former in gate_refs.
func sliceV3TaskVerificationOptions(s *kernel.SemanticSession, task map[string]any, sliceRef string) (map[string]string, error) {
	refs := kernel.ApStrings(kernel.ApMap(task["contract"])["gate_refs"])
	if len(refs) != 1 {
		return nil, kernel.ApFail(s, "TASK_CONTRACT", "Slice v3派发须有唯一原批准checkpoint")
	}
	cpRef := kernel.ApText(task["checkpoint_ref"])
	if s.CheckpointRef != "" {
		if cpRef != "" && cpRef != s.CheckpointRef {
			return nil, kernel.ApFail(s, "TASK_CONTRACT", "任务与显式checkpoint冲突")
		}
		cpRef = s.CheckpointRef
	}
	local, err := dependencies.HasLocalImplementationInputs(s)
	if err != nil {
		return nil, err
	}
	if local {
		current, err := dependencies.ProgressionTaskAssetCheckpoint(s, sliceRef)
		if err != nil {
			return nil, err
		}
		if current == "" {
			return nil, kernel.ApFail(s, "PROGRESSION_BINDING", "当前Slice缺少唯一活动功能checkpoint登记")
		}
		if cpRef != "" && cpRef != current {
			return nil, kernel.ApFail(s, "TASK_CONTRACT", "显式checkpoint不是当前Slice唯一登记的功能checkpoint")
		}
		cpRef = current
	} else if cpRef == "" {
		cpRef = refs[0]
	}
	return map[string]string{"checkpoint": cpRef, "approval-ref": refs[0], "unit": kernel.ApText(task["work_unit_id"])}, nil
}

func AssertImplementationDecisionSemantic(s *kernel.SemanticSession, state map[string]any) error {
	return implementationDecisionWithContract(s, state, func(ref string) (map[string]any, error) {
		contract, err := LoadNativeSlice(s, ref)
		if err != nil {
			return nil, err
		}
		return contract.Normalized, nil
	})
}

// The independent scope receipt is not execution qualification. Historical v2
// source approval checks consume the digest-bound scope fields; execution callers
// above continue to require the complete current Slice contract. v3 is unchanged.
func AssertImplementationScopeSemantic(s *kernel.SemanticSession, state map[string]any) error {
	return implementationDecisionWithContract(s, state, func(ref string) (map[string]any, error) {
		doc, err := s.Doc(ref)
		if err != nil {
			return nil, err
		}
		raw := doc
		if nested, ok := kernel.Object(doc["slice_contract"]); ok {
			raw = nested
		}
		if kernel.ContractN(raw["schema_version"]) == 2 {
			return raw, nil
		}
		contract, err := LoadNativeSlice(s, ref)
		if err != nil {
			return nil, err
		}
		return contract.Normalized, nil
	})
}

func implementationDecisionWithContract(s *kernel.SemanticSession, state map[string]any, contractView func(string) (map[string]any, error)) error {
	requirement := kernel.ApFind(state["user_decisions"], "boundary", "implementation-scope")
	ticket := kernel.ApText(state["vertical_slice_ticket_ref"])
	if requirement == nil || ticket == "" || !kernel.ApContains(requirement["scope"], ticket) {
		return kernel.ApFail(s, "user-decision-scope-mismatch", "实施范围必须包含当前切片")
	}
	if _, e := approval.AssertUserDecisionRequirementSemantic(s, requirement); e != nil {
		return e
	}
	manifest, e := s.Doc(kernel.ApText(requirement["subject_ref"]))
	if e != nil {
		return e
	}
	matches := []map[string]any{}
	for _, slice := range kernel.ApRows(manifest["slices"]) {
		if kernel.ApText(slice["ticket_ref"]) == ticket {
			matches = append(matches, slice)
		}
	}
	if kernel.ApText(manifest["kind"]) != "implementation-scope" || len(matches) != 1 {
		return kernel.ApFail(s, "user-decision-subject-mismatch", "实施范围清单未唯一绑定切片")
	}
	slice := matches[0]
	asset := kernel.ApMap(slice["contract"])
	if kernel.ApText(state["slice_contract_ref"]) == "" || state["slice_contract_ref"] != asset["ref"] {
		return kernel.ApFail(s, "user-decision-subject-mismatch", "当前合同不在批准清单")
	}
	if e = approval.DecisionCurrentAsset(s, asset, "user-decision-stale"); e != nil {
		return e
	}
	contract, e := contractView(kernel.ApText(asset["ref"]))
	if e != nil {
		return e
	}
	if kernel.ApText(kernel.ApMap(contract["lifecycle_refs"])["ticket"]) != ticket {
		return kernel.ApFail(s, "user-decision-subject-mismatch", "持久合同切片引用不匹配")
	}
	for _, pair := range [][2]string{{"repositories", "project_roots"}, {"allowed_write_paths", "allowed_write_paths"}} {
		if !kernel.ApSetEqual(slice[pair[0]], kernel.ApMap(contract["common"])[pair[1]]) {
			return kernel.ApFail(s, "user-decision-scope-mismatch", pair[0])
		}
	}
	baselineRef := kernel.ApText(kernel.ApMap(contract["lifecycle_refs"])["engineering_baseline"])
	if len(kernel.ApArray(slice["baselines"])) == 0 || kernel.ApFind(slice["baselines"], "ref", baselineRef) == nil {
		return kernel.ApFail(s, "user-decision-scope-mismatch", "工程基线缺失")
	}
	for _, baseline := range kernel.ApRows(slice["baselines"]) {
		if e = approval.DecisionCurrentAsset(s, baseline, "user-decision-stale"); e != nil {
			return e
		}
	}
	return nil
}

func validateReadOnlyIntakeSemantic(s *kernel.SemanticSession, ref string, task map[string]any) error {
	contract := kernel.ApMap(task["contract"])
	if kernel.ApText(contract["kind"]) != "read-only-intake" || kernel.ApText(task["execution_state"]) != "Explorer" || len(kernel.ApArray(task["allowed_write_paths"])) != 0 {
		return kernel.ApFail(s, "READ_ONLY_INTAKE", "只读合同不授予写权限")
	}
	if !kernel.ApContains([]string{"work-unit.entry-triage", "work-unit.harness-entry"}, kernel.ApText(task["work_unit_id"])) || kernel.ApFind(s.Registry["work_units"], "id", kernel.ApText(task["work_unit_id"])) == nil || kernel.ApFind(s.Roles["runtimes"], "id", kernel.ApText(task["runtime_id"])) == nil {
		return kernel.ApFail(s, "READ_ONLY_INTAKE", "只允许已登记分诊入口和runtime")
	}
	if kernel.ApText(contract["status"]) != "issued" && kernel.ApText(task["workflow_status"]) != "paused" {
		return kernel.ApFail(s, "READ_ONLY_INTAKE", "过期合同必须暂停")
	}
	refs := append([]string{kernel.ApText(contract["contract_ref"])}, kernel.ApStrings(task["inputs"])...)
	refs = append(refs, kernel.ApStrings(task["expected_evidence_files"])...)
	for _, input := range refs {
		if strings.HasPrefix(input, "maintenance:") || strings.HasPrefix(input, "http") {
			return kernel.ApFail(s, "READ_ONLY_INTAKE", "分诊证据须本地或显式run引用")
		}
		if _, e := s.Bytes(input); e != nil {
			return e
		}
	}
	result := kernel.ApMap(task["result"])
	if result != nil {
		if result["work_unit"] != task["work_unit_id"] || result["workflow_reference"] != contract["contract_ref"] || len(kernel.ApArray(result["changed_files"])) > 0 || len(kernel.ApArray(result["changed_artifacts"])) > 0 || result["next_route"] != nil || kernel.ApTruthy(result["checkpoint_ref"]) {
			return kernel.ApFail(s, "READ_ONLY_INTAKE", "只读结果替换来源、包含变更或试图流转")
		}
		for _, evidence := range kernel.ApStrings(result["evidence_refs"]) {
			if _, e := s.Bytes(evidence); e != nil {
				return e
			}
		}
	}
	results := kernel.ApRows(task["verification_results"])
	if (kernel.ApText(task["verification_status"]) == "not-executed") != (len(results) == 0) {
		return kernel.ApFail(s, "READ_ONLY_INTAKE", "执行状态与实际结果不一致")
	}
	for _, row := range results {
		evidenceRef := kernel.ApText(row["evidence_ref"])
		if !kernel.ApContains(task["verification_commands"], kernel.ApText(row["command"])) || !strings.HasPrefix(evidenceRef, "run:") {
			return kernel.ApFail(s, "READ_ONLY_INTAKE", "运行结果须绑定声明命令及真实运行日志")
		}
		b, e := s.Bytes(evidenceRef)
		if e != nil {
			return e
		}
		if kernel.ApDigest(b) != row["evidence_digest"] {
			return kernel.ApFail(s, "READ_ONLY_INTAKE", "运行证据摘要不一致")
		}
		record, e := s.Doc(evidenceRef)
		if e != nil {
			return e
		}
		for _, key := range []string{"command", "exit_code", "duration_ms", "executed_at"} {
			if !kernel.ApEqual(record[key], row[key]) {
				return kernel.ApFail(s, "READ_ONLY_INTAKE", "运行记录不一致: "+key)
			}
		}
		for _, stream := range []string{"stdout", "stderr"} {
			streamRef := kernel.ApText(row[stream+"_ref"])
			if !strings.HasPrefix(streamRef, "run:") {
				return kernel.ApFail(s, "READ_ONLY_INTAKE", "运行日志缺少run来源")
			}
			b, e := s.Bytes(streamRef)
			if e != nil {
				return e
			}
			if kernel.ApDigest(b) != record[stream+"_digest"] {
				return kernel.ApFail(s, "READ_ONLY_INTAKE", "运行日志摘要不一致")
			}
		}
	}
	completed := kernel.ApText(task["workflow_status"]) == "resolved" || kernel.ApText(result["result"]) == "completed"
	if !completed {
		return nil
	}
	if kernel.ApText(task["workflow_status"]) != "resolved" || kernel.ApText(result["result"]) != "completed" || len(kernel.ApArray(result["evidence_refs"])) == 0 || kernel.ApText(kernel.ApMap(result["context_reconciliation"])["status"]) != "not-applicable" || strings.TrimSpace(kernel.ApText(kernel.ApMap(result["context_reconciliation"])["reason"])) == "" {
		return kernel.ApFail(s, "READ_ONLY_INTAKE", "完成状态、证据或不流转说明缺失")
	}
	for _, key := range []string{"drift", "violation", "new_impacts", "stale_candidates", "blocking_signals", "deferred_seams"} {
		if result[key] == nil || len(kernel.ApArray(result[key])) > 0 {
			return kernel.ApFail(s, "READ_ONLY_INTAKE", "只读完成结果必须空数组: "+key)
		}
	}
	covered := map[string]bool{}
	for _, row := range results {
		if kernel.ApNumber(row["exit_code"]) != 0 {
			return kernel.ApFail(s, "READ_ONLY_INTAKE", "实际验证失败")
		}
		covered[kernel.ApText(row["command"])] = true
	}
	for _, command := range kernel.ApStrings(task["verification_commands"]) {
		if !covered[command] {
			return kernel.ApFail(s, "READ_ONLY_INTAKE", "实际验证缺失")
		}
	}
	observationRef := kernel.ApText(task["observation_ref"])
	if !strings.HasPrefix(observationRef, "run:") {
		return kernel.ApFail(s, "READ_ONLY_INTAKE", "完成结果缺少执行器仓库观测")
	}
	observation, e := s.Doc(observationRef)
	if e != nil {
		return e
	}
	b, e := s.Bytes(kernel.ApText(observation["task_ref"]))
	if e != nil {
		return e
	}
	if kernel.ApDigest(b) != observation["task_digest"] {
		return kernel.ApFail(s, "READ_ONLY_INTAKE", "原派发任务摘要不一致")
	}
	dispatchedValue, e := schema.Parse(b)
	if e != nil {
		return s.Unavailable("INPUT", e.Error())
	}
	dispatched := kernel.ApMap(dispatchedValue)
	for _, key := range []string{"task_id", "work_unit_id", "actor_id", "role_id", "runtime_id", "contract", "inputs", "allowed_write_paths", "objective", "forbidden_actions", "expected_outputs", "expected_evidence_files", "skill_source", "downstream_consumers", "convergence"} {
		if !kernel.ApEqual(dispatched[key], task[key]) {
			return kernel.ApFail(s, "READ_ONLY_INTAKE", "结果替换原任务: "+key)
		}
	}
	if len(kernel.ApArray(dispatched["verification_commands"])) > 0 && !kernel.ApEqual(dispatched["verification_commands"], task["verification_commands"]) {
		return kernel.ApFail(s, "READ_ONLY_INTAKE", "结果替换验证要求")
	}
	resolved, e := filepath.EvalSymlinks(s.Root)
	if e != nil {
		return s.Unavailable("INPUT", e.Error())
	}
	if kernel.ApText(observation["kind"]) != "read-only-intake-observation" || observation["task_id"] != task["task_id"] || kernel.ApText(observation["root"]) != resolved || !kernel.ApEqual(observation["before"], observation["after"]) {
		return kernel.ApFail(s, "READ_ONLY_INTAKE", "观测身份或before/after不一致")
	}
	return s.Verify("intake-observation", ref, map[string]string{"observation": observationRef})
}
