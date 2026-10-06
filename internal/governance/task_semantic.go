package governance

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

var reviewContextFields = []string{"check_ids", "capability_ids", "candidate_ref", "candidate_digest", "policy_ref", "policy_digest", "reviewer_principal_ref", "drafter_principal_ref", "approval_scope", "basis", "current_approvals", "plan_review_binding"}

type reviewCompilation struct{ capabilities, skills, stages []string }

func init() {
	registerSemanticValidator("task", verifyTaskSemantic)
	registerSemanticValidator("backend-completed-producer-task", verifyBackendCompletedProducerTaskSemantic)
}

func taskRole(s *semanticSession, id string) map[string]any {
	if actor := apMap(s.roles["orchestrator"]); apText(actor["id"]) == id {
		return actor
	}
	return apFind(s.roles["roles"], "id", id)
}
func reviewRule(s *semanticSession, boundary string) map[string]any {
	p := apMap(s.roles["gate_policy"])
	for _, bucket := range []string{"check_reviews", "digital_human_review", "dual_digital_human"} {
		if rule := apFind(p[bucket], "gate", boundary); rule != nil {
			return rule
		}
	}
	return nil
}
func validateReviewCapabilityPolicySemantic(s *semanticSession) error {
	p := apMap(s.roles["gate_policy"])
	if apText(p["default_if_unlisted"]) != "reject-unlisted" || len(apArray(s.roles["review_capabilities"])) == 0 {
		return apFail(s, "GATE_POLICY_REQUIRED", "当前能力政策必须完整并拒绝未分类边界")
	}
	skillRegistry, e := s.doc(approvalSkillsRef)
	if e != nil {
		return e
	}
	skills := map[string]bool{}
	for _, bucket := range []string{"skills", "platform_skills", "external_skills"} {
		for _, skill := range apRows(skillRegistry[bucket]) {
			skills[apText(skill["id"])] = true
		}
	}
	capabilities := map[string]bool{}
	for _, capability := range apRows(s.roles["review_capabilities"]) {
		id := apText(capability["id"])
		suffix := strings.TrimPrefix(id, "capability.")
		valid := strings.HasPrefix(id, "capability.") && len(suffix) > 0 && (suffix[0] >= 'a' && suffix[0] <= 'z' || suffix[0] >= '0' && suffix[0] <= '9')
		for _, c := range strings.TrimPrefix(id, "capability.") {
			valid = valid && (c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-')
		}
		if !valid || capabilities[id] || strings.TrimSpace(apText(capability["name"])) == "" || !apUnique(capability["eligible_roles"]) || !apUnique(capability["review_skills"]) {
			return apFail(s, "GATE_POLICY_REQUIRED", "能力身份、名称、角色或技能无效: "+id)
		}
		capabilities[id] = true
		for _, skill := range apStrings(capability["review_skills"]) {
			if !skills[skill] {
				return apFail(s, "GATE_POLICY_REQUIRED", "未登记审查技能: "+skill)
			}
		}
		for _, roleID := range apStrings(capability["eligible_roles"]) {
			role := apFind(s.roles["roles"], "id", roleID)
			if role == nil {
				return apFail(s, "GATE_POLICY_REQUIRED", "能力引用未知角色: "+roleID)
			}
			for _, skill := range apStrings(capability["review_skills"]) {
				if apContains(role["forbidden_skills"], skill) {
					return apFail(s, "REVIEW_CAPABILITY_FORBIDDEN", "禁止技能不能成为审查能力: "+roleID+"/"+skill)
				}
			}
		}
	}
	for _, bucket := range []string{"check_reviews", "digital_human_review", "dual_digital_human", "digital_human_review_work_units"} {
		for _, rule := range apRows(p[bucket]) {
			if !apUnique(rule["capability_ids"]) {
				return apFail(s, "GATE_POLICY_REQUIRED", "专业审查缺少完整能力分类")
			}
			for _, id := range apStrings(rule["capability_ids"]) {
				if !capabilities[id] {
					return apFail(s, "GATE_POLICY_REQUIRED", "专业审查能力未知: "+id)
				}
			}
		}
	}
	for _, kind := range []string{"gates", "checks"} {
		known := map[string]bool{}
		for _, item := range apRows(s.registry[kind]) {
			known[apText(item["id"])] = true
		}
		classified := []string{}
		if kind == "gates" {
			for _, bucket := range []string{"evidence_only", "orchestrator", "product_digital_human_with_biological_veto", "biological_human"} {
				classified = append(classified, apStrings(p[bucket])...)
			}
			for _, bucket := range []string{"digital_human_review", "dual_digital_human"} {
				for _, row := range apRows(p[bucket]) {
					classified = append(classified, apText(row["gate"]))
				}
			}
		} else {
			classified = append(classified, apStrings(p["automatic_checks"])...)
			for _, row := range apRows(p["check_reviews"]) {
				classified = append(classified, apText(row["gate"]))
			}
		}
		seen := map[string]bool{}
		for _, id := range classified {
			if !known[id] || seen[id] {
				return apFail(s, "GATE_POLICY_REQUIRED", kind+" 分类未知或重复: "+id)
			}
			seen[id] = true
		}
		if len(seen) != len(known) {
			return apFail(s, "GATE_POLICY_REQUIRED", kind+" 分类不完整")
		}
	}
	return nil
}

func compileReviewCapabilitiesSemantic(s *semanticSession, checkIDs any, roleID, state string) (reviewCompilation, error) {
	out := reviewCompilation{}
	if e := validateReviewCapabilityPolicySemantic(s); e != nil {
		return out, e
	}
	if state != "Reviewer" && state != "Verifier" {
		return out, apFail(s, "REVIEW_CAPABILITY_STATE", "审查能力只允许 Reviewer / Verifier")
	}
	if !apUnique(checkIDs) {
		return out, apFail(s, "GATE_POLICY_REQUIRED", "check_ids 必须非空唯一")
	}
	role := taskRole(s, roleID)
	if role == nil {
		return out, apFail(s, "REVIEW_CAPABILITY_MISSING", "未知审查角色")
	}
	capIDs, skills, stages := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, id := range apStrings(checkIDs) {
		_, row, known := findRegistry(s.registry, id)
		rule := reviewRule(s, id)
		if !known || rule == nil || !apUnique(rule["capability_ids"]) {
			return out, apFail(s, "GATE_POLICY_REQUIRED", "未知当前审查边界或能力政策: "+id)
		}
		stages[apText(row["stage"])] = true
		for _, capID := range apStrings(rule["capability_ids"]) {
			capIDs[capID] = true
		}
	}
	for id := range capIDs {
		capability := apFind(s.roles["review_capabilities"], "id", id)
		if !apContains(capability["eligible_roles"], roleID) {
			return out, apFail(s, "REVIEW_CAPABILITY_MISSING", roleID+" 缺少 "+id)
		}
		for _, skill := range apStrings(capability["review_skills"]) {
			if apContains(role["forbidden_skills"], skill) {
				return out, apFail(s, "REVIEW_CAPABILITY_FORBIDDEN", roleID+" 禁止 "+skill)
			}
			skills[skill] = true
		}
	}
	for id := range capIDs {
		out.capabilities = append(out.capabilities, id)
	}
	for skill := range skills {
		out.skills = append(out.skills, skill)
	}
	for stage := range stages {
		out.stages = append(out.stages, stage)
	}
	sort.Strings(out.capabilities)
	sort.Strings(out.skills)
	sort.Strings(out.stages)
	return out, nil
}
func compileWorkUnitReviewSemantic(s *semanticSession, unitID, roleID, state string) (reviewCompilation, error) {
	out := reviewCompilation{}
	if state != "Reviewer" && state != "Verifier" {
		return out, apFail(s, "REVIEW_CAPABILITY_STATE", "工作单元审查只允许 Reviewer / Verifier")
	}
	if e := validateReviewCapabilityPolicySemantic(s); e != nil {
		return out, e
	}
	unit := apFind(s.registry["work_units"], "id", unitID)
	role := taskRole(s, roleID)
	rule := apFind(apMap(s.roles["gate_policy"])["digital_human_review_work_units"], "work_unit", unitID)
	if unit == nil || role == nil || rule == nil {
		return out, apFail(s, "GATE_POLICY_REQUIRED", "工作单元没有已登记专业审查政策")
	}
	skills := map[string]bool{}
	for _, id := range apStrings(rule["capability_ids"]) {
		capability := apFind(s.roles["review_capabilities"], "id", id)
		if !apContains(capability["eligible_roles"], roleID) {
			return out, apFail(s, "REVIEW_CAPABILITY_MISSING", roleID+" 缺少 "+id)
		}
		out.capabilities = append(out.capabilities, id)
		for _, skill := range apStrings(capability["review_skills"]) {
			if apContains(role["forbidden_skills"], skill) {
				return out, apFail(s, "REVIEW_CAPABILITY_FORBIDDEN", "接收角色禁止审查技能")
			}
			skills[skill] = true
		}
	}
	for skill := range skills {
		out.skills = append(out.skills, skill)
	}
	out.stages = []string{apText(unit["stage"])}
	sort.Strings(out.capabilities)
	sort.Strings(out.skills)
	return out, nil
}

func currentApprovalsFromCheckpointSemantic(s *semanticSession, ref string, boundaries any) ([]map[string]any, error) {
	checkpoint, e := s.doc(ref)
	if e != nil {
		return nil, e
	}
	if !apUnique(boundaries) {
		return nil, apFail(s, "APPROVAL_CONTEXT_REQUIRED", "缺少当前检查范围")
	}
	out := []map[string]any{}
	for _, boundary := range apStrings(boundaries) {
		bucket := "gates"
		if strings.HasPrefix(boundary, "check.") {
			bucket = "checks"
		}
		row := apMap(apMap(checkpoint[bucket])[boundary])
		if apText(row["subject_ref"]) == "" || !apUnique(row["approval_scope"]) || strings.TrimSpace(apText(row["drafter_principal_ref"])) == "" || len(apArray(row["basis"])) == 0 {
			return nil, apFail(s, "APPROVAL_CONTEXT_REQUIRED", "当前主体、范围、basis或作者来源缺失: "+boundary)
		}
		binding, e := s.bind(apText(row["subject_ref"]))
		if e != nil {
			return nil, e
		}
		digest := strings.TrimPrefix(binding.Digest, "sha256:")
		if row["subject_digest"] != nil && row["subject_digest"] != digest {
			return nil, apFail(s, "REVIEW_BINDING_STALE", "当前主体摘要漂移: "+boundary)
		}
		allBasis, e := apBasis(s, row["basis"], "当前检查", "REVIEW_BINDING_STALE")
		if e != nil {
			return nil, e
		}
		for _, evidence := range apStrings(row["evidence_refs"]) {
			if apFind(allBasis, "ref", evidence) == nil {
				return nil, apFail(s, "APPROVAL_CONTEXT_REQUIRED", "证据必须包含当前basis: "+evidence)
			}
		}
		basis := []map[string]any{}
		for _, asset := range allBasis {
			if asset["ref"] != row["subject_ref"] && asset["ref"] != row["approval_ref"] {
				basis = append(basis, asset)
			}
		}
		if len(basis) == 0 {
			return nil, apFail(s, "APPROVAL_CONTEXT_REQUIRED", "缺少独立审查依据: "+boundary)
		}
		out = append(out, map[string]any{"boundary": boundary, "subject_ref": row["subject_ref"], "subject_digest": digest, "approval_scope": row["approval_scope"], "basis": basis, "drafter_principal_ref": row["drafter_principal_ref"]})
	}
	return out, nil
}

func currentReviewContextSemantic(s *semanticSession, context map[string]any, boundary string) error {
	for _, field := range reviewContextFields {
		if field != "plan_review_binding" && context[field] == nil {
			return apFail(s, "APPROVAL_CONTEXT_REQUIRED", "缺少完整当前 review_context: "+field)
		}
	}
	for _, field := range []string{"check_ids", "capability_ids", "approval_scope"} {
		if !apUnique(context[field]) {
			return apFail(s, "APPROVAL_CONTEXT_REQUIRED", field+" 无效")
		}
	}
	if strings.TrimSpace(apText(context["reviewer_principal_ref"])) == "" || strings.TrimSpace(apText(context["drafter_principal_ref"])) == "" || context["reviewer_principal_ref"] == context["drafter_principal_ref"] {
		return apFail(s, "REVIEW_NOT_INDEPENDENT", "审查实例必须不同于起草实例")
	}
	for _, pair := range [][2]string{{"candidate_ref", "candidate_digest"}, {"policy_ref", "policy_digest"}} {
		ref := apText(context[pair[0]])
		binding, e := s.bind(ref)
		if e != nil {
			return e
		}
		if !apHashValid(context[pair[1]], false) || strings.TrimPrefix(binding.Digest, "sha256:") != context[pair[1]] {
			return apFail(s, "REVIEW_BINDING_STALE", "当前候选或政策摘要过期: "+ref)
		}
	}
	if context["policy_ref"] != approvalRolesRef {
		return apFail(s, "GATE_POLICY_REQUIRED", "政策引用必须指向唯一角色事实源")
	}
	if len(apArray(context["basis"])) == 0 {
		return apFail(s, "APPROVAL_CONTEXT_REQUIRED", "缺少唯一当前basis")
	}
	basisSeen := map[string]bool{}
	for _, asset := range apRows(context["basis"]) {
		ref := apText(asset["ref"])
		if ref == "" || basisSeen[ref] || !apHashValid(asset["digest"], false) {
			return apFail(s, "REVIEW_BINDING_STALE", "当前依据重复或摘要非法")
		}
		basisSeen[ref] = true
	}
	rows := apRows(context["current_approvals"])
	rowBoundaries := []string{}
	for _, row := range rows {
		rowBoundaries = append(rowBoundaries, apText(row["boundary"]))
	}
	if !apSetEqual(rowBoundaries, context["check_ids"]) {
		return apFail(s, "APPROVAL_CONTEXT_REQUIRED", "current_approvals 必须完整唯一覆盖check_ids")
	}
	selected := rows
	narrow := context["plan_review_binding"] != nil && boundary != ""
	if narrow {
		selected = nil
		for _, row := range rows {
			if apText(row["boundary"]) == boundary {
				selected = append(selected, row)
			}
		}
	}
	if len(selected) == 0 {
		return apFail(s, "APPROVAL_CONTEXT_REQUIRED", "消费边界不属于当前审查任务")
	}
	selectedRefs := map[string]bool{}
	for _, row := range selected {
		for _, asset := range apRows(row["basis"]) {
			selectedRefs[apText(asset["ref"])] = true
		}
	}
	for _, asset := range apRows(context["basis"]) {
		ref := apText(asset["ref"])
		if !narrow || selectedRefs[ref] || ref == approvalRegistryRef || ref == approvalSkillsRef {
			binding, e := s.bind(ref)
			if e != nil {
				return e
			}
			if strings.TrimPrefix(binding.Digest, "sha256:") != asset["digest"] {
				return apFail(s, "REVIEW_BINDING_STALE", "当前依据摘要过期: "+ref)
			}
		}
	}
	for _, row := range rows {
		if !apHashValid(row["subject_digest"], false) {
			return apFail(s, "REVIEW_BINDING_STALE", "逐项主体摘要非法")
		}
		if !narrow || apText(row["boundary"]) == boundary {
			binding, e := s.bind(apText(row["subject_ref"]))
			if e != nil {
				return e
			}
			if strings.TrimPrefix(binding.Digest, "sha256:") != row["subject_digest"] {
				return apFail(s, "REVIEW_BINDING_STALE", "当前逐项主体已变化")
			}
		}
		if !apUnique(row["approval_scope"]) || strings.TrimSpace(apText(row["drafter_principal_ref"])) == "" || row["drafter_principal_ref"] == context["reviewer_principal_ref"] {
			return apFail(s, "REVIEW_NOT_INDEPENDENT", "逐项范围或独立作者来源无效")
		}
		for _, scope := range apStrings(row["approval_scope"]) {
			if !apContains(context["approval_scope"], scope) {
				return apFail(s, "REVIEW_NOT_INDEPENDENT", "逐项范围超出任务范围")
			}
		}
		if len(apArray(row["basis"])) == 0 {
			return apFail(s, "APPROVAL_CONTEXT_REQUIRED", "逐项basis缺失")
		}
		seen := map[string]bool{}
		for _, asset := range apRows(row["basis"]) {
			ref := apText(asset["ref"])
			if ref == "" || seen[ref] {
				return apFail(s, "APPROVAL_CONTEXT_REQUIRED", "逐项basis重复或空引用")
			}
			seen[ref] = true
			found := false
			for _, bound := range apRows(context["basis"]) {
				found = found || apEqual(bound, asset)
			}
			if !found {
				return apFail(s, "APPROVAL_CONTEXT_REQUIRED", "逐项basis不属于当前任务")
			}
		}
	}
	candidate, e := s.doc(apText(context["candidate_ref"]))
	if e != nil {
		return e
	}
	source := apMap(candidate["source_checkpoint"])
	if apNumber(candidate["schema_version"]) != 1 || apText(candidate["kind"]) != "review-subject" || !apEqual(candidate["current_approvals"], rows) || apText(source["ref"]) == "" || !apHashValid(source["digest"], false) {
		return apFail(s, "REVIEW_BINDING_STALE", "审阅包未绑定逐项当前上下文与checkpoint来源")
	}
	boundaries := context["check_ids"]
	if narrow {
		boundaries = []string{boundary}
	}
	current, e := currentApprovalsFromCheckpointSemantic(s, apText(source["ref"]), boundaries)
	if e != nil {
		return e
	}
	var checkpointDigest string
	if apText(source["binding_kind"]) == "current-approvals-v1" {
		checkpointDigest, _ = semanticDocumentDigest(current)
	} else {
		binding, e := s.bind(apText(source["ref"]))
		if e != nil {
			return e
		}
		checkpointDigest = strings.TrimPrefix(binding.Digest, "sha256:")
	}
	if !narrow && checkpointDigest != source["digest"] || !apEqual(current, selected) {
		return apFail(s, "REVIEW_BINDING_STALE", "逐项预期或checkpoint批准输入已变化")
	}
	return nil
}

func validateReviewTaskSemantic(s *semanticSession, ref string, task, expected map[string]any, boundary string, requireCompleted bool) (map[string]any, error) {
	if e := s.validateSchema(approvalTaskSchemaRef, task); e != nil {
		return nil, e
	}
	if apNumber(task["schema_version"]) != 1 || !apContains([]string{"Reviewer", "Verifier"}, apText(task["execution_state"])) || !apContains([]string{"active", "resolved"}, apText(task["workflow_status"])) || apText(apMap(task["contract"])["status"]) != "issued" {
		return nil, apFail(s, "REVIEW_CAPABILITY_STATE", "只接受issued正式v1 Reviewer/Verifier任务")
	}
	context := apMap(task["review_context"])
	if e := currentReviewContextSemantic(s, context, boundary); e != nil {
		return nil, e
	}
	policy, e := s.doc(apText(context["policy_ref"]))
	if e != nil {
		return nil, e
	}
	if !apEqual(policy, s.roles) {
		return nil, apFail(s, "REVIEW_BINDING_STALE", "角色政策与当前事实源不一致")
	}
	role := apFind(s.roles["roles"], "id", apText(task["role_id"]))
	if role == nil || apFind(s.roles["runtimes"], "id", apText(task["runtime_id"])) == nil {
		return nil, apFail(s, "REVIEW_CAPABILITY_MISSING", "未登记审查角色或运行时")
	}
	if e = validateTaskSkillSourceSemantic(s, task, false); e != nil {
		return nil, e
	}
	compiled, e := compileReviewCapabilitiesSemantic(s, context["check_ids"], apText(task["role_id"]), apText(task["execution_state"]))
	if e != nil {
		return nil, e
	}
	unit := apFind(s.registry["work_units"], "id", apText(task["work_unit_id"]))
	candidate, e := s.doc(apText(context["candidate_ref"]))
	if e != nil {
		return nil, e
	}
	checkpointRef := apText(apMap(candidate["source_checkpoint"])["ref"])
	if unit == nil || apText(unit["scope"]) == "template-source" || apText(task["work_unit_id"]) == "work-unit.slice-implementation" || apText(task["stage_id"]) == "" || apFind(s.registry["stages"], "id", apText(task["stage_id"])) == nil || apText(unit["stage"]) != "" && task["stage_id"] != unit["stage"] {
		return nil, apFail(s, "REVIEW_TASK_INVALID", "正式生命周期审查任务工作单元或阶段不匹配")
	}
	contract := apMap(task["contract"])
	convergence := apMap(task["convergence"])
	if apText(contract["kind"]) != "lifecycle-work-unit" || apTruthy(contract["slice_contract_ref"]) || apTruthy(contract["maintenance_ref"]) || contract["contract_ref"] != context["candidate_ref"] || apText(contract["lifecycle_ref"]) != checkpointRef || apText(task["checkpoint_ref"]) != checkpointRef || convergence["parent_work_unit"] != task["work_unit_id"] || apText(convergence["convergence_ref"]) != checkpointRef {
		return nil, apFail(s, "REVIEW_TASK_INVALID", "正式审查合同、checkpoint与汇合来源不一致")
	}
	inputs := []string{checkpointRef, apText(context["policy_ref"]), apText(context["candidate_ref"])}
	for _, asset := range apRows(context["basis"]) {
		inputs = append(inputs, apText(asset["ref"]))
	}
	for _, row := range apRows(context["current_approvals"]) {
		inputs = append(inputs, apText(row["subject_ref"]))
	}
	for _, input := range inputs {
		if !apContains(task["inputs"], input) {
			return nil, apFail(s, "REVIEW_TASK_INVALID", "正式审查输入遗漏来源: "+input)
		}
	}
	if !apSetEqual(context["capability_ids"], compiled.capabilities) || !apArraysEqual(apMap(task["skill_source"])["review_skills"], compiled.skills) || !apContains(compiled.stages, apText(task["stage_id"])) {
		return nil, apFail(s, "REVIEW_CAPABILITY_MISSING", "能力/补充技能/审查阶段必须由当前政策编译")
	}
	if context["implementation_actor_id"] == task["actor_id"] {
		return nil, apFail(s, "REVIEW_NOT_INDEPENDENT", "审查actor不得等于实现actor")
	}
	protected := append(inputs, apText(context["candidate_ref"]))
	for _, allowed := range apStrings(task["allowed_write_paths"]) {
		for _, input := range protected {
			if taskWithinPath(input, allowed) {
				return nil, apFail(s, "REVIEW_CAPABILITY_WRITE_SCOPE", "审查写范围覆盖候选、政策或依据")
			}
		}
	}
	if expected != nil {
		for _, field := range reviewContextFields {
			same := apEqual(context[field], expected[field])
			if field == "check_ids" || field == "capability_ids" || field == "approval_scope" {
				same = apSetEqual(context[field], expected[field])
			}
			if !same {
				return nil, apFail(s, "REVIEW_BINDING_STALE", "任务未绑定当前消费者 "+field)
			}
		}
	}
	if e = s.verify("plan-review-task", ref, map[string]string{"boundary": boundary, "require-completed": fmt.Sprint(requireCompleted)}); e != nil {
		return nil, e
	}
	out := apCopy(context)
	out["review_skills"] = compiled.skills
	out["review_stages"] = compiled.stages
	return out, nil
}

func approvalExpectedFromTaskSemantic(s *semanticSession, ref string, task map[string]any, boundary, digest string, expected map[string]any) (map[string]any, error) {
	context, e := validateReviewTaskSemantic(s, ref, task, expected, boundary, false)
	if e != nil {
		return nil, e
	}
	row := apFind(context["current_approvals"], "boundary", boundary)
	if row == nil || !apContains(context["check_ids"], boundary) {
		return nil, apFail(s, "APPROVAL_CONTEXT_REQUIRED", "任务缺少边界独立当前来源")
	}
	binding, e := s.bind(ref)
	if e != nil {
		return nil, e
	}
	if !apHashValid(digest, false) || strings.TrimPrefix(binding.Digest, "sha256:") != digest {
		return nil, apFail(s, "APPROVAL_CONTEXT_REQUIRED", "缺少当前任务文件及摘要")
	}
	actual, e := s.doc(ref)
	if e != nil {
		return nil, e
	}
	if !apEqual(task, actual) {
		return nil, apFail(s, "REVIEW_BINDING_STALE", "传入任务不是当前文件字节")
	}
	out := apCopy(row)
	review := apCopy(apMap(task["review_context"]))
	review["review_task_ref"] = ref
	review["review_task_digest"] = digest
	out["review_context"] = review
	return out, nil
}

func assertReviewCapabilityBindingSemantic(s *semanticSession, record, expected map[string]any) error {
	ref := apText(expected["review_task_ref"])
	digest := apText(expected["review_task_digest"])
	if ref == "" || !apHashValid(digest, false) {
		return apFail(s, "APPROVAL_CONTEXT_REQUIRED", "缺少消费者当前review task来源")
	}
	if apText(record["review_task_ref"]) != ref || apText(record["review_task_digest"]) != digest {
		return apFail(s, "REVIEW_BINDING_STALE", "会签未绑定当前审查任务")
	}
	binding, e := s.bind(ref)
	if e != nil {
		return e
	}
	if strings.TrimPrefix(binding.Digest, "sha256:") != digest {
		return apFail(s, "REVIEW_BINDING_STALE", "审查任务字节已变化")
	}
	task, e := s.doc(ref)
	if e != nil {
		return e
	}
	context, e := validateReviewTaskSemantic(s, ref, task, expected, apText(record["gate_id"]), true)
	if e != nil {
		return e
	}
	if !apContains(context["check_ids"], apText(record["gate_id"])) || !apSetEqual(record["capability_ids"], context["capability_ids"]) {
		return apFail(s, "REVIEW_CAPABILITY_MISSING", "会签边界或能力不属于当前任务")
	}
	row := apFind(context["current_approvals"], "boundary", apText(record["gate_id"]))
	if record["role_id"] != task["role_id"] || record["runtime_id"] != task["runtime_id"] || record["principal_ref"] != context["reviewer_principal_ref"] || record["drafter_principal_ref"] != row["drafter_principal_ref"] {
		return apFail(s, "REVIEW_NOT_INDEPENDENT", "会签与任务角色、运行时或独立身份不匹配")
	}
	if context["plan_review_binding"] != nil && !apEqual(record["plan_review_binding"], context["plan_review_binding"]) {
		return apFail(s, "PLAN_REVIEW_RESULT_MISMATCH", "Plan结论未绑定已完成当前专业审查尝试")
	}
	return nil
}

func validateTaskSkillSourceSemantic(s *semanticSession, task map[string]any, compile bool) error {
	role := taskRole(s, apText(task["role_id"]))
	source := apMap(task["skill_source"])
	if role == nil || source["registry_ref"] != approvalRolesRef || apText(source["defaults_ref"]) != "taskPackageDefaults("+apText(task["role_id"])+")" || !apArraysEqual(source["core_skills"], role["core_skills"]) || !apArraysEqual(source["forbidden_skills"], role["forbidden_skills"]) {
		return apFail(s, "REVIEW_CAPABILITY_FORBIDDEN", "角色core/forbidden技能不得改变")
	}
	if compile && (source["review_skills"] != nil || apMap(task["review_context"])["capability_ids"] != nil) {
		var result reviewCompilation
		var e error
		if apMap(task["review_context"])["capability_ids"] != nil {
			result, e = compileReviewCapabilitiesSemantic(s, apMap(task["review_context"])["check_ids"], apText(task["role_id"]), apText(task["execution_state"]))
		} else {
			result, e = compileWorkUnitReviewSemantic(s, apText(task["work_unit_id"]), apText(task["role_id"]), apText(task["execution_state"]))
		}
		if e != nil {
			return e
		}
		if !apArraysEqual(source["review_skills"], result.skills) {
			return apFail(s, "REVIEW_CAPABILITY_MISSING", "review_skills必须由权威政策编译")
		}
	}
	return nil
}
func taskWithinPath(child, parent string) bool {
	a, b := path.Clean(child), path.Clean(parent)
	return a == b || strings.HasPrefix(a, b+"/")
}

// The full task contract and v2 intake rules are applied below; review capability
// validation above is also reused by approval consumers without dispatching work.
func verifyTaskSemantic(s *semanticSession, ref string, opts map[string]string) error {
	return verifyTaskPackageSemantic(s, ref, opts)
}

// Only an evidence consumer may select this internal read-only audit seam.
// The public task validator retains its terminal recovery prohibition. The
// consumer supplies its checkpoint independently of both task and report.
func verifyBackendCompletedProducerTaskSemantic(s *semanticSession, ref string, opts map[string]string) error {
	checkpointRef := opts["checkpoint"]
	if checkpointRef == "" || opts["history"] == "true" {
		return apFail(s, "TASK_PRODUCER_AUDIT", "完成态后端证据审计需要独立当前 checkpoint")
	}
	task, err := s.doc(ref)
	if err != nil {
		return err
	}
	result := apMap(task["result"])
	contract := apMap(task["contract"])
	next, hasNext := result["next_route"]
	if apNumber(task["schema_version"]) != 1 || task["work_unit_id"] != "work-unit.backend-delivery" || apFind(s.registry["work_units"], "id", "work-unit.backend-delivery") == nil || contract["kind"] != "lifecycle-work-unit" || contract["status"] != "issued" || task["workflow_status"] != "resolved" || result["result"] != "completed" || result["work_unit"] != task["work_unit_id"] || !hasNext || next != nil {
		return apFail(s, "TASK_PRODUCER_AUDIT", "仅可只读审计已完成且没有后续路由的正式后端交付任务")
	}
	if task["checkpoint_ref"] != checkpointRef || result["checkpoint_ref"] != checkpointRef {
		return apFail(s, "TASK_PRODUCER_AUDIT", "任务与结果必须精确绑定消费者当前 checkpoint")
	}
	if err = s.verify("checkpoint", checkpointRef, nil); err != nil {
		return err
	}
	checkpoint, err := s.doc(checkpointRef)
	if err != nil {
		return err
	}
	if checkpoint["stage"] != task["stage_id"] || apMap(checkpoint["stage_trace"])["completed_work_unit"] != "work-unit.backend-delivery" || checkpoint["next_work_unit"] != nil || checkpoint["mode"] != "audit" {
		return apFail(s, "TASK_PRODUCER_AUDIT", "当前 checkpoint 未独立声明只读后端终点审计范围")
	}
	if err = s.verify("backend-terminal", ".yss-backend-delivery.json", nil); err != nil {
		return err
	}
	terminal, err := s.doc(".yss-backend-delivery.json")
	if err != nil {
		return err
	}
	for _, key := range []string{"delivery", "review_state"} {
		binding := apMap(terminal[key])
		if err = taskReadable(s, apText(binding["ref"]), false, apText(binding["digest"])); err != nil {
			return err
		}
		if !apContains(task["expected_evidence_files"], apText(binding["ref"])) || !apContains(result["evidence_refs"], apText(binding["ref"])) {
			return apFail(s, "TASK_PRODUCER_AUDIT", "完成任务没有声明当前终点的独立 "+key+" 来源证据")
		}
	}
	if !apContains(task["expected_evidence_files"], ".yss-backend-delivery.json") || !apContains(result["evidence_refs"], ".yss-backend-delivery.json") {
		return apFail(s, "TASK_PRODUCER_AUDIT", "完成任务缺少固定后端终点证据")
	}
	if err = verifyTaskPackageSemanticMode(s, ref, opts, true); err != nil {
		return err
	}
	s.report.Applicability = append(s.report.Applicability, map[string]any{"id": "backend-completed-producer-task", "status": "read-only-audit", "recovery_authorized": false, "implementation_authorized": false, "source": ref, "checkpoint": checkpointRef})
	return nil
}

// The caller selects the collection independently. This validates each current
// file before enforcing the same-Slice identity and Worker/Reviewer separation.
// Full CI groups unrelated Slice contracts before calling this legacy set seam.
func validateTaskPackageSetSemantic(s *semanticSession, refs []string) error {
	if len(refs) == 0 {
		return apFail(s, "TASK_SET", "任务包集合不能为空")
	}
	packages := make([]map[string]any, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref == "" || seen[ref] {
			return apFail(s, "TASK_SET", "任务引用为空或重复")
		}
		seen[ref] = true
		if err := s.verify("task", ref, nil); err != nil {
			return err
		}
		task, err := s.doc(ref)
		if err != nil {
			return err
		}
		packages = append(packages, task)
	}
	return validateTaskActorSetSemantic(s, packages)
}

// contract verification may use this coupling check after its independent
// dispatch binding checks, avoiding task → Slice → task verification recursion.
func validateTaskActorSetSemantic(s *semanticSession, packages []map[string]any) error {
	if len(packages) == 0 {
		return apFail(s, "TASK_SET", "任务包集合不能为空")
	}
	var identity []byte
	workers := map[string]bool{}
	for _, task := range packages {
		contract := apMap(task["contract"])
		if apText(contract["kind"]) != "slice-implementation" {
			continue
		}
		current := apCanonical([]any{contract["contract_id"], contract["contract_version"]})
		if identity == nil {
			identity = current
		} else if string(identity) != string(current) {
			return apFail(s, "TASK_SET_CONTRACT", "同一切片任务集合必须消费同一合同ID和版本")
		}
		if apText(task["execution_state"]) == "Worker" {
			workers[apText(task["actor_id"])] = true
		}
	}
	for _, task := range packages {
		if apText(apMap(task["contract"])["kind"]) == "slice-implementation" && apText(task["execution_state"]) == "Reviewer" && workers[apText(task["actor_id"])] {
			return apFail(s, "REVIEW_NOT_INDEPENDENT", "切片Reviewer不得与Worker使用同一actor_id")
		}
	}
	return nil
}

func taskReadable(s *semanticSession, ref string, maintenance bool, digest string) error {
	if strings.TrimSpace(ref) == "" {
		return apFail(s, "TASK_REFERENCE", "任务引用缺失")
	}
	if strings.HasPrefix(ref, "maintenance:") && !maintenance {
		return apFail(s, "TASK_REFERENCE", "maintenance引用仅适用于模板维护")
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return nil
	}
	b, e := s.bytes(ref)
	if e != nil {
		return e
	}
	if digest != "" && apDigest(b) != digest && strings.TrimPrefix(apDigest(b), "sha256:") != digest {
		return apFail(s, "TASK_EVIDENCE_DRIFT", "证据摘要不一致: "+ref)
	}
	return nil
}
func taskSafePath(s *semanticSession, ref string, maintenance bool) error {
	if strings.HasPrefix(ref, "maintenance:") {
		if !maintenance {
			return apFail(s, "TASK_SCOPE", "maintenance写范围仅适用于模板维护")
		}
		_, _, e := s.referenceView(ref)
		return e
	}
	if _, e := safefs.Path(s.root, ref); e != nil {
		return apFail(s, "TASK_SCOPE", e.Error())
	}
	return nil
}

func taskDeliveryProfile(s *semanticSession) (map[string]any, error) {
	metadata := []string{}
	var native map[string]any
	if present, e := s.exists(".yss.json"); e != nil {
		return nil, e
	} else if present {
		native, e = s.doc(".yss.json")
		if e != nil {
			return nil, e
		}
	}
	for _, side := range []string{"backend", "frontend"} {
		ok, e := s.exists(".yss-harness-" + side + ".json")
		if e != nil {
			return nil, e
		}
		if ok || native["profile"] == side {
			metadata = append(metadata, side)
		}
	}
	if len(metadata) > 1 {
		return nil, apFail(s, "TASK_SCOPE", "专职Harness metadata冲突")
	}
	exists, e := s.exists(".template-spec/process/harness-profile.yaml")
	if e != nil {
		return nil, e
	}
	if !exists {
		if len(metadata) > 0 {
			return nil, apFail(s, "TASK_SCOPE", "专职Harness缺少profile")
		}
		return nil, nil
	}
	profile, e := s.doc(".template-spec/process/harness-profile.yaml")
	if e != nil {
		return nil, e
	}
	if len(metadata) == 1 {
		side := metadata[0]
		record := native
		if native == nil {
			var e error
			record, e = s.doc(".yss-harness-" + side + ".json")
			if e != nil {
				return nil, e
			}
		}
		expected := "harness." + side + "-delivery"
		newIdentity := apNumber(record["metadataSchemaVersion"]) == 2 && record["schema_version"] == nil && record["profile_id"] == nil && record["profileId"] == expected && record["templateSource"] == "github:iloveZzz/yss-harness-"+side+"-agent" && apMap(profile["instantiation"])["cli_package"] == "create-yss-harness-"+side
		oldIdentity := apNumber(record["schema_version"]) == 1 && record["metadataSchemaVersion"] == nil && record["profile_id"] == expected
		nativeIdentity := native != nil && native["profileId"] == expected && apMap(profile["instantiation"])["cli_package"] == "yss" && apMap(profile["instantiation"])["metadata_file"] == ".yss.json"
		if (!nativeIdentity && !newIdentity && !oldIdentity) || profile["profile_id"] != expected {
			return nil, apFail(s, "TASK_SCOPE", "专职Harness metadata与profile不一致")
		}
	}
	return profile, nil
}

func enforceHarnessTaskSemantic(s *semanticSession, task map[string]any) error {
	return enforceHarnessTaskSemanticMode(s, task, false)
}

func enforceHarnessTaskSemanticMode(s *semanticSession, task map[string]any, completedProducerAudit bool) error {
	identity, e := s.doc("yss-project.yaml")
	if e != nil {
		return e
	}
	hasScope, e := s.exists(".yss-execution-scope.yaml")
	if e != nil {
		return e
	}
	plugin, e := s.backendPluginBinding()
	if e != nil {
		return e
	}
	if !hasScope {
		if apContains([]string{"yss-plan-to-backend", "yss-backend-delivery"}, apText(plugin["plugin"])) {
			return apFail(s, "TASK_SCOPE", "插件项目缺少职责范围")
		}
		if apText(task["work_unit_id"]) == "work-unit.backend-delivery" {
			return apFail(s, "TASK_SCOPE", "后端终点需要显式职责范围")
		}
	} else {
		scope, e := s.doc(".yss-execution-scope.yaml")
		if e != nil {
			return e
		}
		if apNumber(identity["schema_version"]) != 1 || apText(identity["repository_mode"]) != "project-instance" || apNumber(scope["schema_version"]) != 1 || apText(scope["scope_id"]) != "plan-to-backend" || len(scope) != 2 {
			return apFail(s, "TASK_SCOPE", "未知或被扩大职责配置")
		}
		if plugin != nil && (!apContains([]string{"yss-plan-to-backend", "yss-backend-delivery"}, apText(plugin["plugin"])) || plugin["execution_scope"] != scope["scope_id"]) {
			return apFail(s, "TASK_SCOPE", "插件与职责绑定不一致")
		}
		contract, e := s.doc(".agents/skills/yss-product-lifecycle/references/orchestration-contract.yaml")
		if e != nil {
			return e
		}
		policy := apMap(apMap(contract["execution_scopes"])["plan-to-backend"])
		unit := apText(task["work_unit_id"])
		if apText(apMap(task["contract"])["kind"]) == "slice-implementation" {
			unit = "work-unit.slice-implementation"
		}
		if apText(policy["terminal_work_unit"]) != "work-unit.backend-delivery" || !apContains(policy["allowed_work_units"], unit) {
			return apFail(s, "TASK_SCOPE", "职责范围不允许当前工作单元")
		}
		terminal, e := s.exists(".yss-backend-delivery.json")
		if e != nil {
			return e
		}
		if terminal {
			if e = s.verify("backend-terminal", ".yss-backend-delivery.json", nil); e != nil {
				return e
			}
			if apText(apMap(task["contract"])["kind"]) != "read-only-intake" && !completedProducerAudit {
				return apFail(s, "TASK_SCOPE", "后端交付已完成，不能恢复实现")
			}
		}
		if apText(task["role_id"]) == "role.frontend-engineer" {
			return apFail(s, "TASK_SCOPE", "后端职责不能派发前端实现")
		}
		for _, ref := range apStrings(task["allowed_write_paths"]) {
			if ref == "apps/frontend" || strings.HasPrefix(ref, "apps/frontend/") {
				return apFail(s, "TASK_SCOPE", "后端职责禁止前端写入")
			}
		}
	}
	profile, e := taskDeliveryProfile(s)
	if e != nil {
		return e
	}
	id := apText(profile["profile_id"])
	if apText(identity["repository_mode"]) != "project-instance" || (id != "harness.backend-delivery" && id != "harness.frontend-delivery") {
		return nil
	}
	if apText(apMap(task["contract"])["kind"]) == "template-maintenance" {
		return apFail(s, "TASK_SCOPE", "专职产品项目不得派发模板维护")
	}
	crossReview := apContains([]string{"Explorer", "Reviewer"}, apText(task["execution_state"])) && len(apArray(task["allowed_write_paths"])) == 0
	audience := apMap(profile["audience"])
	if !crossReview && !apContains(audience["target_user_roles"], apText(task["role_id"])) && !apContains(audience["control_plane_roles"], apText(task["role_id"])) {
		return apFail(s, "TASK_SCOPE", "专职Harness不能派发另一端实现/起草")
	}
	opposite := "frontend"
	if id == "harness.frontend-delivery" {
		opposite = "backend"
	}
	for _, ref := range apStrings(task["allowed_write_paths"]) {
		if ref == "apps/"+opposite || strings.HasPrefix(ref, "apps/"+opposite+"/") {
			return apFail(s, "TASK_SCOPE", "专职Harness不能写入另一端工程")
		}
	}
	return nil
}

func taskFrontendDeliverySemantic(s *semanticSession, task map[string]any) error {
	profile, e := taskDeliveryProfile(s)
	if e != nil {
		return e
	}
	identity, e := s.doc("yss-project.yaml")
	if e != nil {
		return e
	}
	dedicated := apText(identity["repository_mode"]) == "project-instance" && apContains([]string{"harness.frontend-delivery", "yss-harness-frontend"}, apText(profile["profile_id"]))
	contractRef := apText(apMap(task["contract"])["slice_contract_ref"])
	var contract map[string]any
	if contractRef != "" {
		loaded, e := loadNativeSlice(s, contractRef)
		if e != nil {
			return e
		}
		contract = loaded.Normalized
	}
	binding := apMap(task["frontend_delivery"])
	direct := apMap(apMap(contract["frontend"])["delivery"])
	resolved := apMap(apMap(contract["resolution"])["frontend_delivery"])
	if direct != nil && resolved != nil && !apEqual(direct, resolved) {
		return apFail(s, "FRONTEND_DELIVERY", "Slice前端交付绑定冲突")
	}
	if direct != nil {
		binding = direct
	} else if resolved != nil {
		binding = resolved
	}
	status := apText(apMap(contract["frontend"])["status"])
	required := status != "" && status != "not-applicable" && status != "disabled"
	if !dedicated && binding == nil && !required {
		return nil
	}
	if dedicated && apText(apMap(task["contract"])["kind"]) == "template-maintenance" {
		return apFail(s, "FRONTEND_DELIVERY", "前端产品项目不得用维护任务绕过输入")
	}
	ref := apText(binding["acceptance_ref"])
	slice := apText(contract["slice_id"])
	if slice == "" {
		slice = apText(task["slice_id"])
	}
	if ref == "" || slice == "" {
		return apFail(s, "FRONTEND_DELIVERY", "缺少明确Slice和战略/后端联合接收")
	}
	if apText(task["slice_id"]) != "" && apText(contract["slice_id"]) != "" && task["slice_id"] != contract["slice_id"] {
		return apFail(s, "FRONTEND_DELIVERY", "任务与Slice不一致")
	}
	phase := "inputs"
	if apText(apMap(task["contract"])["kind"]) == "slice-implementation" {
		phase = "implementation"
		if apText(contract["status"]) != "approved" || binding["digest"] == nil || direct == nil && resolved == nil {
			return apFail(s, "FRONTEND_DELIVERY", "实现缺少approved持久合同和冻结接收摘要")
		}
	}
	return s.verify("frontend-delivery", ref, map[string]string{"slice": slice, "expected-digest": apText(binding["digest"]), "phase": phase, "contract": contractRef})
}

func verifyTaskPackageSemantic(s *semanticSession, ref string, opts map[string]string) error {
	return verifyTaskPackageSemanticMode(s, ref, opts, false)
}

func verifyTaskPackageSemanticMode(s *semanticSession, ref string, opts map[string]string, completedProducerAudit bool) error {
	task, e := s.doc(ref)
	if e != nil {
		return e
	}
	if e = s.validateSchema(approvalTaskSchemaRef, task); e != nil {
		return e
	}
	if e = enforceHarnessTaskSemanticMode(s, task, completedProducerAudit); e != nil {
		return e
	}
	if e = validateTaskSkillSourceSemantic(s, task, true); e != nil {
		return e
	}
	if apNumber(task["schema_version"]) == 2 {
		return validateReadOnlyIntakeSemantic(s, ref, task)
	}
	history := opts["history"] == "true"
	contract := apMap(task["contract"])
	if history {
		identity, e := s.doc("yss-project.yaml")
		if e != nil {
			return e
		}
		if apText(identity["repository_mode"]) != "template-source" || apText(contract["kind"]) != "template-maintenance" {
			return apFail(s, "TASK_HISTORY", "历史兼容仅适用于模板维护schema v1")
		}
	}
	intake := apText(task["execution_state"]) == "Explorer" && len(apArray(task["allowed_write_paths"])) == 0 && apContains([]string{"work-unit.entry-triage", "work-unit.harness-entry"}, apText(task["work_unit_id"]))
	if !intake {
		if e = taskFrontendDeliverySemantic(s, task); e != nil {
			return e
		}
	}
	unit := apFind(s.registry["work_units"], "id", apText(task["work_unit_id"]))
	if unit == nil && apText(contract["kind"]) != "slice-implementation" {
		return apFail(s, "TASK_WORK_UNIT", "未知work_unit_id")
	}
	if apFind(s.roles["runtimes"], "id", apText(task["runtime_id"])) == nil {
		return apFail(s, "TASK_RUNTIME", "未知runtime_id")
	}
	if !apContains([]string{"Explorer", "Drafter", "Worker", "Reviewer", "Verifier"}, apText(task["execution_state"])) || !apContains([]string{"not-started", "active", "paused", "resolved", "failed"}, apText(task["workflow_status"])) {
		return apFail(s, "TASK_STATE", "执行态或workflow_status非法")
	}
	if apText(task["stage_id"]) != "" {
		stages := taskRole(s, apText(task["role_id"]))["stages"]
		if apMap(task["review_context"])["capability_ids"] != nil {
			compiled, e := compileReviewCapabilitiesSemantic(s, apMap(task["review_context"])["check_ids"], apText(task["role_id"]), apText(task["execution_state"]))
			if e != nil {
				return e
			}
			stages = compiled.stages
		}
		if apFind(s.registry["stages"], "id", apText(task["stage_id"])) == nil || !apContains(stages, apText(task["stage_id"])) {
			return apFail(s, "TASK_STAGE", "角色未覆盖当前阶段")
		}
	}
	if apMap(task["review_context"])["capability_ids"] != nil {
		if _, e = validateReviewTaskSemantic(s, ref, task, nil, "", false); e != nil {
			return e
		}
	}
	maintenance := apText(contract["kind"]) == "template-maintenance"
	for _, allowed := range apStrings(task["allowed_write_paths"]) {
		if e = taskSafePath(s, allowed, maintenance); e != nil {
			return e
		}
	}
	if apText(task["execution_state"]) == "Reviewer" && apMap(task["review_context"])["implementation_actor_id"] == task["actor_id"] {
		return apFail(s, "REVIEW_NOT_INDEPENDENT", "Reviewer与实现者actor相同")
	}
	if len(apArray(task["allowed_write_paths"])) > 0 && apText(contract["kind"]) == "lifecycle-work-unit" {
		if e = s.verify("tracking-entry", ref, map[string]string{"checkpoint": apText(task["checkpoint_ref"])}); e != nil {
			return e
		}
	}
	if apText(task["work_unit_id"]) == "work-unit.spec-synthesis" {
		if e = s.verify("plan-spec-entry", ref, map[string]string{"checkpoint": apText(task["checkpoint_ref"])}); e != nil {
			return e
		}
	}
	for _, row := range apRows(task["verification_results"]) {
		if !apContains(task["verification_commands"], apText(row["command"])) {
			return apFail(s, "TASK_VERIFICATION", "验证结果命令未声明")
		}
		if strings.HasPrefix(apText(row["evidence_ref"]), "maintenance:") && row["evidence_digest"] == nil {
			return apFail(s, "TASK_EVIDENCE", "仓外维护证据须绑定摘要")
		}
		if e = taskReadable(s, apText(row["evidence_ref"]), maintenance, apText(row["evidence_digest"])); e != nil {
			return e
		}
	}
	result := apMap(task["result"])
	completed := apText(task["workflow_status"]) == "resolved" || apText(result["result"]) == "completed"
	if completed {
		if apText(task["workflow_status"]) != "resolved" || apText(result["result"]) != "completed" {
			return apFail(s, "TASK_RESULT", "完成状态与result不一致")
		}
		for _, field := range []string{"result_schema", "work_unit", "workflow_reference", "skill", "changed_files", "context_reconciliation", "evidence_refs", "deferred_seams", "drift", "violation", "new_impacts", "stale_candidates", "next_route", "blocking_signals"} {
			if _, ok := result[field]; !ok {
				return apFail(s, "TASK_RESULT", "完成结果缺少: "+field)
			}
		}
		if apText(result["result_schema"]) != "workflow-execution-result-v1" || result["work_unit"] != task["work_unit_id"] {
			return apFail(s, "TASK_RESULT", "结果schema或work-unit不一致")
		}
		reconciliation := apMap(result["context_reconciliation"])
		want := "reconciled"
		if maintenance {
			want = "not-applicable"
		}
		if apText(reconciliation["status"]) != want || maintenance && strings.TrimSpace(apText(reconciliation["reason"])) == "" {
			return apFail(s, "TASK_CONTEXT", "完成结果缺少当前context reconciliation")
		}
		if e = taskReadable(s, apText(reconciliation["ref"]), maintenance, ""); e != nil {
			return e
		}
		if !apContains(result["evidence_refs"], apText(reconciliation["ref"])) {
			return apFail(s, "TASK_CONTEXT", "context证据未包含result.evidence_refs")
		}
		next := apText(result["next_route"])
		if e = s.verify("next-route", ref, map[string]string{"current": apText(task["work_unit_id"]), "next": next, "checkpoint": apText(task["checkpoint_ref"])}); e != nil {
			return e
		}
		for _, expected := range apStrings(task["expected_evidence_files"]) {
			if e = taskReadable(s, expected, maintenance, ""); e != nil {
				return e
			}
		}
		if len(apArray(task["verification_results"])) == 0 {
			return apFail(s, "TASK_VERIFICATION", "完成任务缺少实际验证")
		}
		covered := map[string]bool{}
		for _, row := range apRows(task["verification_results"]) {
			if apNumber(row["exit_code"]) != 0 {
				return apFail(s, "TASK_VERIFICATION", "完成任务有失败验证")
			}
			covered[apText(row["command"])] = true
		}
		for _, command := range apStrings(task["verification_commands"]) {
			if !covered[command] {
				return apFail(s, "TASK_VERIFICATION", "未覆盖全部verification_commands")
			}
		}
		for _, field := range []string{"new_impacts", "stale_candidates", "blocking_signals", "drift", "violation"} {
			if len(apArray(result[field])) > 0 {
				return apFail(s, "TASK_RESULT", "completed仍含阻断: "+field)
			}
		}
		if len(apArray(result["evidence_refs"])) == 0 {
			return apFail(s, "TASK_EVIDENCE", "completed evidence_refs为空")
		}
		for _, evidence := range apStrings(result["evidence_refs"]) {
			if e = taskReadable(s, evidence, maintenance, ""); e != nil {
				return e
			}
		}
	}
	for _, changed := range apStrings(result["changed_files"]) {
		if e = taskSafePath(s, changed, maintenance); e != nil {
			return e
		}
		covered := false
		for _, allowed := range apStrings(task["allowed_write_paths"]) {
			covered = covered || taskWithinPath(changed, allowed)
		}
		if !covered {
			return apFail(s, "TASK_SCOPE", "changed_files超出allowed_write_paths: "+changed)
		}
	}
	return validateTaskContractSemantic(s, ref, task, unit, history)
}

func validateTaskContractSemantic(s *semanticSession, ref string, task, unit map[string]any, history bool) error {
	contract := apMap(task["contract"])
	kind := apText(contract["kind"])
	if !apContains([]string{"lifecycle-work-unit", "slice-implementation", "template-maintenance", "read-only-intake"}, kind) || !apContains([]string{"issued", "stale", "blocked"}, apText(contract["status"])) || apText(contract["status"]) == "stale" && apText(task["workflow_status"]) != "paused" {
		return apFail(s, "TASK_CONTRACT", "未知、过期或未暂停合同")
	}
	if e := taskReadable(s, apText(contract["contract_ref"]), kind == "template-maintenance", ""); e != nil {
		return e
	}
	if kind == "lifecycle-work-unit" {
		if apText(task["work_unit_id"]) == "work-unit.slice-implementation" || apText(apMap(task["convergence"])["parent_work_unit"]) == "work-unit.slice-implementation" || apText(unit["scope"]) == "template-source" || apTruthy(contract["slice_contract_ref"]) || apTruthy(contract["maintenance_ref"]) {
			return apFail(s, "TASK_CONTRACT", "lifecycle合同不得冒充切片或模板维护")
		}
		return taskReadable(s, apText(contract["lifecycle_ref"]), false, "")
	}
	if kind == "template-maintenance" {
		identity, e := s.doc("yss-project.yaml")
		if e != nil {
			return e
		}
		if apText(identity["repository_mode"]) != "template-source" || apText(unit["scope"]) != "template-source" || apTruthy(contract["slice_contract_ref"]) || apTruthy(contract["lifecycle_ref"]) {
			return apFail(s, "TASK_CONTRACT", "模板维护仓库、unit或合同引用不一致")
		}
		checkpointRef := apText(contract["maintenance_ref"])
		checkpoint, e := s.doc(checkpointRef)
		if e != nil {
			return e
		}
		if e = s.verify("maintenance-checkpoint", checkpointRef, map[string]string{"history": fmt.Sprint(history), "allow-pending-review": fmt.Sprint(apText(task["execution_state"]) == "Reviewer" && apText(task["workflow_status"]) != "resolved")}); e != nil {
			return e
		}
		if apNumber(checkpoint["schema_version"]) == 2 && apText(task["execution_state"]) == "Reviewer" {
			if apText(checkpoint["current_state"]) != "review-ready" {
				return apFail(s, "TASK_CONTRACT", "Reviewer仅能绑定review-ready")
			}
			for _, field := range []string{"candidate_kind", "candidate_requirement", "candidate_digest", "review_axis", "review_round", "applicable_rule_refs"} {
				if contract[field] == nil {
					return apFail(s, "TASK_CONTRACT", "Reviewer缺少 "+field)
				}
			}
			if !apEqual(contract["candidate_digest"], checkpoint["candidate_digest"]) || !apEqual(contract["review_round"], checkpoint["review_round"]) || len(apArray(task["allowed_read_paths"])) == 0 || !apContains(task["allowed_read_paths"], checkpointRef) {
				return apFail(s, "TASK_CONTRACT", "Reviewer候选摘要、轮次或读取范围不匹配")
			}
			for _, path := range apStrings(task["allowed_read_paths"]) {
				if e = taskReadable(s, path, true, ""); e != nil {
					return e
				}
			}
			for _, rule := range apStrings(contract["applicable_rule_refs"]) {
				if e = taskReadable(s, rule, false, ""); e != nil {
					return e
				}
			}
		}
		return nil
	}
	if kind != "slice-implementation" {
		return apFail(s, "TASK_CONTRACT", "v1 read-only-intake非法")
	}
	if apTruthy(contract["lifecycle_ref"]) || apTruthy(contract["maintenance_ref"]) || apText(task["stage_id"]) != "stage.vertical-slice-implementation" || apText(apMap(task["convergence"])["parent_work_unit"]) != "work-unit.slice-implementation" {
		return apFail(s, "TASK_CONTRACT", "切片合同阶段或汇合不匹配")
	}
	sliceRef := apText(contract["slice_contract_ref"])
	c, e := loadNativeSlice(s, sliceRef)
	if e != nil {
		return e
	}
	slice := c.Normalized
	if e = s.verify("slice", sliceRef, map[string]string{"checkpoint": apText(task["checkpoint_ref"])}); e != nil {
		return e
	}
	if !apEqual(slice["contract_id"], contract["contract_id"]) || !apEqual(slice["contract_version"], contract["contract_version"]) || apText(slice["status"]) != "approved" {
		return apFail(s, "TASK_CONTRACT", "Slice必须为当前approved版本")
	}
	sliceUnit := apFind(slice["work_units"], "id", apText(task["work_unit_id"]))
	if sliceUnit == nil {
		return apFail(s, "TASK_CONTRACT", "Slice缺少work-unit")
	}
	if apNumber(c.Raw["schema_version"]) == 3 || apNumber(apMap(c.Raw["slice_contract"])["schema_version"]) == 3 {
		if e = validateSliceV3TaskSemantic(s, task, slice, sliceUnit); e != nil {
			return e
		}
	} else {
		for _, field := range []string{"role_id", "runtime_id", "task_package_ref", "contract_id", "contract_version", "allowed_write_paths"} {
			if sliceUnit[field] == nil {
				return apFail(s, "TASK_CONTRACT", "Slice工作单元缺少 "+field)
			}
		}
		for _, field := range []string{"role_id", "runtime_id"} {
			if task[field] != sliceUnit[field] {
				return apFail(s, "TASK_CONTRACT", "Slice任务身份不匹配")
			}
		}
		if !apEqual(sliceUnit["contract_id"], contract["contract_id"]) || !apEqual(sliceUnit["contract_version"], contract["contract_version"]) {
			return apFail(s, "TASK_CONTRACT", "Slice工作单元合同不匹配")
		}
		for _, allowed := range apStrings(task["allowed_write_paths"]) {
			if !apContains(sliceUnit["allowed_write_paths"], allowed) {
				return apFail(s, "TASK_SCOPE", "写范围未获Slice授权")
			}
		}
		dispatchRef := apText(sliceUnit["task_package_ref"])
		if dispatchRef != apText(task["work_unit_id"]) {
			dispatch, e := s.doc(dispatchRef)
			if e != nil {
				return e
			}
			for _, field := range []string{"task_id", "work_unit_id", "actor_id", "role_id", "runtime_id", "contract", "allowed_write_paths"} {
				if !apEqual(dispatch[field], task[field]) {
					return apFail(s, "TASK_CONTRACT", "派发文件身份、合同或写范围不一致")
				}
			}
		}
	}
	if apText(task["execution_state"]) == "Worker" {
		return assertImplementationDecisionSemantic(s, map[string]any{"slice_contract_ref": sliceRef, "vertical_slice_ticket_ref": apMap(slice["lifecycle_refs"])["ticket"], "user_decisions": task["user_decisions"]})
	}
	return nil
}

func validateSliceV3TaskSemantic(s *semanticSession, task, slice, unit map[string]any) error {
	role := taskRole(s, apText(task["role_id"]))
	if role == nil || unit["role_id"] != task["role_id"] || !apContains(role["stages"], apText(task["stage_id"])) {
		return apFail(s, "TASK_CONTRACT", "Slice v3接收角色不支持当前任务")
	}
	for _, skill := range append([]string{apText(unit["primary_skill"])}, apStrings(unit["supporting_skills"])...) {
		if apContains(role["forbidden_skills"], skill) {
			return apFail(s, "TASK_SKILL", "Slice工作单元使用禁用Skill")
		}
	}
	refs := apStrings(apMap(task["contract"])["gate_refs"])
	if len(refs) != 1 {
		return apFail(s, "TASK_CONTRACT", "Slice v3派发须有唯一批准checkpoint")
	}
	if e := s.verify("slice", apText(apMap(task["contract"])["slice_contract_ref"]), map[string]string{"checkpoint": refs[0]}); e != nil {
		return e
	}
	for _, allowed := range apStrings(task["allowed_write_paths"]) {
		covered := false
		for _, parent := range apStrings(unit["allowed_write_paths"]) {
			covered = covered || taskWithinPath(allowed, parent)
		}
		if !covered {
			return apFail(s, "TASK_SCOPE", "任务写范围超出Slice v3")
		}
	}
	work := apMap(unit["work_unit"])
	for _, pair := range [][2]string{{"verification_commands", "verification_commands"}, {"expected_evidence_files", "expected_evidence"}} {
		if !apArraysEqual(task[pair[0]], work[pair[1]]) {
			return apFail(s, "TASK_CONTRACT", "任务遗漏或替换Slice v3 "+pair[0])
		}
	}
	for _, pattern := range apStrings(work["forbidden_patterns"]) {
		if !apContains(task["forbidden_actions"], pattern) {
			return apFail(s, "TASK_CONTRACT", "任务遗漏全局约束")
		}
	}
	return nil
}

func assertImplementationDecisionSemantic(s *semanticSession, state map[string]any) error {
	requirement := apFind(state["user_decisions"], "boundary", "implementation-scope")
	ticket := apText(state["vertical_slice_ticket_ref"])
	if requirement == nil || ticket == "" || !apContains(requirement["scope"], ticket) {
		return apFail(s, "user-decision-scope-mismatch", "实施范围必须包含当前切片")
	}
	if _, e := assertUserDecisionRequirementSemantic(s, requirement); e != nil {
		return e
	}
	manifest, e := s.doc(apText(requirement["subject_ref"]))
	if e != nil {
		return e
	}
	matches := []map[string]any{}
	for _, slice := range apRows(manifest["slices"]) {
		if apText(slice["ticket_ref"]) == ticket {
			matches = append(matches, slice)
		}
	}
	if apText(manifest["kind"]) != "implementation-scope" || len(matches) != 1 {
		return apFail(s, "user-decision-subject-mismatch", "实施范围清单未唯一绑定切片")
	}
	slice := matches[0]
	asset := apMap(slice["contract"])
	if apText(state["slice_contract_ref"]) == "" || state["slice_contract_ref"] != asset["ref"] {
		return apFail(s, "user-decision-subject-mismatch", "当前合同不在批准清单")
	}
	if e = decisionCurrentAsset(s, asset, "user-decision-stale"); e != nil {
		return e
	}
	c, e := loadNativeSlice(s, apText(asset["ref"]))
	if e != nil {
		return e
	}
	contract := c.Normalized
	if apText(apMap(contract["lifecycle_refs"])["ticket"]) != ticket {
		return apFail(s, "user-decision-subject-mismatch", "持久合同切片引用不匹配")
	}
	for _, pair := range [][2]string{{"repositories", "project_roots"}, {"allowed_write_paths", "allowed_write_paths"}} {
		if !apSetEqual(slice[pair[0]], apMap(contract["common"])[pair[1]]) {
			return apFail(s, "user-decision-scope-mismatch", pair[0])
		}
	}
	baselineRef := apText(apMap(contract["lifecycle_refs"])["engineering_baseline"])
	if len(apArray(slice["baselines"])) == 0 || apFind(slice["baselines"], "ref", baselineRef) == nil {
		return apFail(s, "user-decision-scope-mismatch", "工程基线缺失")
	}
	for _, baseline := range apRows(slice["baselines"]) {
		if e = decisionCurrentAsset(s, baseline, "user-decision-stale"); e != nil {
			return e
		}
	}
	return nil
}

func validateReadOnlyIntakeSemantic(s *semanticSession, ref string, task map[string]any) error {
	contract := apMap(task["contract"])
	if apText(contract["kind"]) != "read-only-intake" || apText(task["execution_state"]) != "Explorer" || len(apArray(task["allowed_write_paths"])) != 0 {
		return apFail(s, "READ_ONLY_INTAKE", "只读合同不授予写权限")
	}
	if !apContains([]string{"work-unit.entry-triage", "work-unit.harness-entry"}, apText(task["work_unit_id"])) || apFind(s.registry["work_units"], "id", apText(task["work_unit_id"])) == nil || apFind(s.roles["runtimes"], "id", apText(task["runtime_id"])) == nil {
		return apFail(s, "READ_ONLY_INTAKE", "只允许已登记分诊入口和runtime")
	}
	if apText(contract["status"]) != "issued" && apText(task["workflow_status"]) != "paused" {
		return apFail(s, "READ_ONLY_INTAKE", "过期合同必须暂停")
	}
	refs := append([]string{apText(contract["contract_ref"])}, apStrings(task["inputs"])...)
	refs = append(refs, apStrings(task["expected_evidence_files"])...)
	for _, input := range refs {
		if strings.HasPrefix(input, "maintenance:") || strings.HasPrefix(input, "http") {
			return apFail(s, "READ_ONLY_INTAKE", "分诊证据须本地或显式run引用")
		}
		if _, e := s.bytes(input); e != nil {
			return e
		}
	}
	result := apMap(task["result"])
	if result != nil {
		if result["work_unit"] != task["work_unit_id"] || result["workflow_reference"] != contract["contract_ref"] || len(apArray(result["changed_files"])) > 0 || len(apArray(result["changed_artifacts"])) > 0 || result["next_route"] != nil || apTruthy(result["checkpoint_ref"]) {
			return apFail(s, "READ_ONLY_INTAKE", "只读结果替换来源、包含变更或试图流转")
		}
		for _, evidence := range apStrings(result["evidence_refs"]) {
			if _, e := s.bytes(evidence); e != nil {
				return e
			}
		}
	}
	results := apRows(task["verification_results"])
	if (apText(task["verification_status"]) == "not-executed") != (len(results) == 0) {
		return apFail(s, "READ_ONLY_INTAKE", "执行状态与实际结果不一致")
	}
	for _, row := range results {
		evidenceRef := apText(row["evidence_ref"])
		if !apContains(task["verification_commands"], apText(row["command"])) || !strings.HasPrefix(evidenceRef, "run:") {
			return apFail(s, "READ_ONLY_INTAKE", "运行结果须绑定声明命令及真实运行日志")
		}
		b, e := s.bytes(evidenceRef)
		if e != nil {
			return e
		}
		if apDigest(b) != row["evidence_digest"] {
			return apFail(s, "READ_ONLY_INTAKE", "运行证据摘要不一致")
		}
		record, e := s.doc(evidenceRef)
		if e != nil {
			return e
		}
		for _, key := range []string{"command", "exit_code", "duration_ms", "executed_at"} {
			if !apEqual(record[key], row[key]) {
				return apFail(s, "READ_ONLY_INTAKE", "运行记录不一致: "+key)
			}
		}
		for _, stream := range []string{"stdout", "stderr"} {
			streamRef := apText(row[stream+"_ref"])
			if !strings.HasPrefix(streamRef, "run:") {
				return apFail(s, "READ_ONLY_INTAKE", "运行日志缺少run来源")
			}
			b, e := s.bytes(streamRef)
			if e != nil {
				return e
			}
			if apDigest(b) != record[stream+"_digest"] {
				return apFail(s, "READ_ONLY_INTAKE", "运行日志摘要不一致")
			}
		}
	}
	completed := apText(task["workflow_status"]) == "resolved" || apText(result["result"]) == "completed"
	if !completed {
		return nil
	}
	if apText(task["workflow_status"]) != "resolved" || apText(result["result"]) != "completed" || len(apArray(result["evidence_refs"])) == 0 || apText(apMap(result["context_reconciliation"])["status"]) != "not-applicable" || strings.TrimSpace(apText(apMap(result["context_reconciliation"])["reason"])) == "" {
		return apFail(s, "READ_ONLY_INTAKE", "完成状态、证据或不流转说明缺失")
	}
	for _, key := range []string{"drift", "violation", "new_impacts", "stale_candidates", "blocking_signals", "deferred_seams"} {
		if result[key] == nil || len(apArray(result[key])) > 0 {
			return apFail(s, "READ_ONLY_INTAKE", "只读完成结果必须空数组: "+key)
		}
	}
	covered := map[string]bool{}
	for _, row := range results {
		if apNumber(row["exit_code"]) != 0 {
			return apFail(s, "READ_ONLY_INTAKE", "实际验证失败")
		}
		covered[apText(row["command"])] = true
	}
	for _, command := range apStrings(task["verification_commands"]) {
		if !covered[command] {
			return apFail(s, "READ_ONLY_INTAKE", "实际验证缺失")
		}
	}
	observationRef := apText(task["observation_ref"])
	if !strings.HasPrefix(observationRef, "run:") {
		return apFail(s, "READ_ONLY_INTAKE", "完成结果缺少执行器仓库观测")
	}
	observation, e := s.doc(observationRef)
	if e != nil {
		return e
	}
	b, e := s.bytes(apText(observation["task_ref"]))
	if e != nil {
		return e
	}
	if apDigest(b) != observation["task_digest"] {
		return apFail(s, "READ_ONLY_INTAKE", "原派发任务摘要不一致")
	}
	dispatchedValue, e := schema.Parse(b)
	if e != nil {
		return s.unavailable("INPUT", e.Error())
	}
	dispatched := apMap(dispatchedValue)
	for _, key := range []string{"task_id", "work_unit_id", "actor_id", "role_id", "runtime_id", "contract", "inputs", "allowed_write_paths", "objective", "forbidden_actions", "expected_outputs", "expected_evidence_files", "skill_source", "downstream_consumers", "convergence"} {
		if !apEqual(dispatched[key], task[key]) {
			return apFail(s, "READ_ONLY_INTAKE", "结果替换原任务: "+key)
		}
	}
	if len(apArray(dispatched["verification_commands"])) > 0 && !apEqual(dispatched["verification_commands"], task["verification_commands"]) {
		return apFail(s, "READ_ONLY_INTAKE", "结果替换验证要求")
	}
	resolved, e := filepath.EvalSymlinks(s.root)
	if e != nil {
		return s.unavailable("INPUT", e.Error())
	}
	if apText(observation["kind"]) != "read-only-intake-observation" || observation["task_id"] != task["task_id"] || apText(observation["root"]) != resolved || !apEqual(observation["before"], observation["after"]) {
		return apFail(s, "READ_ONLY_INTAKE", "观测身份或before/after不一致")
	}
	return s.verify("intake-observation", ref, map[string]string{"observation": observationRef})
}
