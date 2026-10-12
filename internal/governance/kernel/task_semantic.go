package kernel

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

var reviewContextFields = []string{"check_ids", "capability_ids", "candidate_ref", "candidate_digest", "policy_ref", "policy_digest", "reviewer_principal_ref", "drafter_principal_ref", "approval_scope", "basis", "current_approvals", "plan_review_binding"}

type reviewCompilation struct {
	capabilities, skills, Stages []string `json:"-" yaml:"-"`
}

func TaskRole(s *SemanticSession, id string) map[string]any {
	if actor := ApMap(s.Roles["orchestrator"]); ApText(actor["id"]) == id {
		return actor
	}
	return ApFind(s.Roles["roles"], "id", id)
}

func reviewRule(s *SemanticSession, boundary string) map[string]any {
	p := ApMap(s.Roles["gate_policy"])
	for _, bucket := range []string{"check_reviews", "digital_human_review", "dual_digital_human"} {
		if rule := ApFind(p[bucket], "gate", boundary); rule != nil {
			return rule
		}
	}
	return nil
}

func validateReviewCapabilityPolicySemantic(s *SemanticSession) error {
	p := ApMap(s.Roles["gate_policy"])
	if ApText(p["default_if_unlisted"]) != "reject-unlisted" || len(ApArray(s.Roles["review_capabilities"])) == 0 {
		return ApFail(s, "GATE_POLICY_REQUIRED", "当前能力政策必须完整并拒绝未分类边界")
	}
	skillRegistry, e := s.Doc(ApprovalSkillsRef)
	if e != nil {
		return e
	}
	skills := map[string]bool{}
	for _, bucket := range []string{"skills", "platform_skills", "external_skills"} {
		for _, skill := range ApRows(skillRegistry[bucket]) {
			skills[ApText(skill["id"])] = true
		}
	}
	capabilities := map[string]bool{}
	for _, capability := range ApRows(s.Roles["review_capabilities"]) {
		id := ApText(capability["id"])
		suffix := strings.TrimPrefix(id, "capability.")
		valid := strings.HasPrefix(id, "capability.") && len(suffix) > 0 && (suffix[0] >= 'a' && suffix[0] <= 'z' || suffix[0] >= '0' && suffix[0] <= '9')
		for _, c := range strings.TrimPrefix(id, "capability.") {
			valid = valid && (c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-')
		}
		if !valid || capabilities[id] || strings.TrimSpace(ApText(capability["name"])) == "" || !apUnique(capability["eligible_roles"]) || !apUnique(capability["review_skills"]) {
			return ApFail(s, "GATE_POLICY_REQUIRED", "能力身份、名称、角色或技能无效: "+id)
		}
		capabilities[id] = true
		for _, skill := range ApStrings(capability["review_skills"]) {
			if !skills[skill] {
				return ApFail(s, "GATE_POLICY_REQUIRED", "未登记审查技能: "+skill)
			}
		}
		for _, roleID := range ApStrings(capability["eligible_roles"]) {
			role := ApFind(s.Roles["roles"], "id", roleID)
			if role == nil {
				return ApFail(s, "GATE_POLICY_REQUIRED", "能力引用未知角色: "+roleID)
			}
			for _, skill := range ApStrings(capability["review_skills"]) {
				if ApContains(role["forbidden_skills"], skill) {
					return ApFail(s, "REVIEW_CAPABILITY_FORBIDDEN", "禁止技能不能成为审查能力: "+roleID+"/"+skill)
				}
			}
		}
	}
	for _, bucket := range []string{"check_reviews", "digital_human_review", "dual_digital_human", "digital_human_review_work_units"} {
		for _, rule := range ApRows(p[bucket]) {
			if !apUnique(rule["capability_ids"]) {
				return ApFail(s, "GATE_POLICY_REQUIRED", "专业审查缺少完整能力分类")
			}
			for _, id := range ApStrings(rule["capability_ids"]) {
				if !capabilities[id] {
					return ApFail(s, "GATE_POLICY_REQUIRED", "专业审查能力未知: "+id)
				}
			}
		}
	}
	for _, kind := range []string{"gates", "checks"} {
		known := map[string]bool{}
		for _, item := range ApRows(s.Registry[kind]) {
			known[ApText(item["id"])] = true
		}
		classified := []string{}
		if kind == "gates" {
			for _, bucket := range []string{"evidence_only", "orchestrator", "product_digital_human_with_biological_veto", "biological_human"} {
				classified = append(classified, ApStrings(p[bucket])...)
			}
			for _, bucket := range []string{"digital_human_review", "dual_digital_human"} {
				for _, row := range ApRows(p[bucket]) {
					classified = append(classified, ApText(row["gate"]))
				}
			}
		} else {
			classified = append(classified, ApStrings(p["automatic_checks"])...)
			for _, row := range ApRows(p["check_reviews"]) {
				classified = append(classified, ApText(row["gate"]))
			}
		}
		seen := map[string]bool{}
		for _, id := range classified {
			if !known[id] || seen[id] {
				return ApFail(s, "GATE_POLICY_REQUIRED", kind+" 分类未知或重复: "+id)
			}
			seen[id] = true
		}
		if len(seen) != len(known) {
			return ApFail(s, "GATE_POLICY_REQUIRED", kind+" 分类不完整")
		}
	}
	return nil
}

func CompileReviewCapabilitiesSemantic(s *SemanticSession, checkIDs any, roleID, state string) (reviewCompilation, error) {
	out := reviewCompilation{}
	if e := validateReviewCapabilityPolicySemantic(s); e != nil {
		return out, e
	}
	if state != "Reviewer" && state != "Verifier" {
		return out, ApFail(s, "REVIEW_CAPABILITY_STATE", "审查能力只允许 Reviewer / Verifier")
	}
	if !apUnique(checkIDs) {
		return out, ApFail(s, "GATE_POLICY_REQUIRED", "check_ids 必须非空唯一")
	}
	role := TaskRole(s, roleID)
	if role == nil {
		return out, ApFail(s, "REVIEW_CAPABILITY_MISSING", "未知审查角色")
	}
	capIDs, skills, stages := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, id := range ApStrings(checkIDs) {
		_, row, known := FindRegistry(s.Registry, id)
		rule := reviewRule(s, id)
		if !known || rule == nil || !apUnique(rule["capability_ids"]) {
			return out, ApFail(s, "GATE_POLICY_REQUIRED", "未知当前审查边界或能力政策: "+id)
		}
		stages[ApText(row["stage"])] = true
		for _, capID := range ApStrings(rule["capability_ids"]) {
			capIDs[capID] = true
		}
	}
	for id := range capIDs {
		capability := ApFind(s.Roles["review_capabilities"], "id", id)
		if !ApContains(capability["eligible_roles"], roleID) {
			return out, ApFail(s, "REVIEW_CAPABILITY_MISSING", roleID+" 缺少 "+id)
		}
		for _, skill := range ApStrings(capability["review_skills"]) {
			if ApContains(role["forbidden_skills"], skill) {
				return out, ApFail(s, "REVIEW_CAPABILITY_FORBIDDEN", roleID+" 禁止 "+skill)
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
		out.Stages = append(out.Stages, stage)
	}
	sort.Strings(out.capabilities)
	sort.Strings(out.skills)
	sort.Strings(out.Stages)
	return out, nil
}

func compileWorkUnitReviewSemantic(s *SemanticSession, unitID, roleID, state string) (reviewCompilation, error) {
	out := reviewCompilation{}
	if state != "Reviewer" && state != "Verifier" {
		return out, ApFail(s, "REVIEW_CAPABILITY_STATE", "工作单元审查只允许 Reviewer / Verifier")
	}
	if e := validateReviewCapabilityPolicySemantic(s); e != nil {
		return out, e
	}
	unit := ApFind(s.Registry["work_units"], "id", unitID)
	role := TaskRole(s, roleID)
	rule := ApFind(ApMap(s.Roles["gate_policy"])["digital_human_review_work_units"], "work_unit", unitID)
	if unit == nil || role == nil || rule == nil {
		return out, ApFail(s, "GATE_POLICY_REQUIRED", "工作单元没有已登记专业审查政策")
	}
	skills := map[string]bool{}
	for _, id := range ApStrings(rule["capability_ids"]) {
		capability := ApFind(s.Roles["review_capabilities"], "id", id)
		if !ApContains(capability["eligible_roles"], roleID) {
			return out, ApFail(s, "REVIEW_CAPABILITY_MISSING", roleID+" 缺少 "+id)
		}
		out.capabilities = append(out.capabilities, id)
		for _, skill := range ApStrings(capability["review_skills"]) {
			if ApContains(role["forbidden_skills"], skill) {
				return out, ApFail(s, "REVIEW_CAPABILITY_FORBIDDEN", "接收角色禁止审查技能")
			}
			skills[skill] = true
		}
	}
	for skill := range skills {
		out.skills = append(out.skills, skill)
	}
	out.Stages = []string{ApText(unit["stage"])}
	sort.Strings(out.capabilities)
	sort.Strings(out.skills)
	return out, nil
}

func currentApprovalsFromCheckpointSemantic(s *SemanticSession, ref string, boundaries any) ([]map[string]any, error) {
	checkpoint, e := s.Doc(ref)
	if e != nil {
		return nil, e
	}
	if !apUnique(boundaries) {
		return nil, ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "缺少当前检查范围")
	}
	out := []map[string]any{}
	for _, boundary := range ApStrings(boundaries) {
		bucket := "gates"
		if strings.HasPrefix(boundary, "check.") {
			bucket = "checks"
		}
		row := ApMap(ApMap(checkpoint[bucket])[boundary])
		if ApText(row["subject_ref"]) == "" || !apUnique(row["approval_scope"]) || strings.TrimSpace(ApText(row["drafter_principal_ref"])) == "" || len(ApArray(row["basis"])) == 0 {
			return nil, ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "当前主体、范围、basis或作者来源缺失: "+boundary)
		}
		binding, e := s.Bind(ApText(row["subject_ref"]))
		if e != nil {
			return nil, e
		}
		digest := strings.TrimPrefix(binding.Digest, "sha256:")
		if row["subject_digest"] != nil && row["subject_digest"] != digest {
			return nil, ApFail(s, "REVIEW_BINDING_STALE", "当前主体摘要漂移: "+boundary)
		}
		allBasis, e := apBasis(s, row["basis"], "当前检查", "REVIEW_BINDING_STALE")
		if e != nil {
			return nil, e
		}
		for _, evidence := range ApStrings(row["evidence_refs"]) {
			if ApFind(allBasis, "ref", evidence) == nil {
				return nil, ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "证据必须包含当前basis: "+evidence)
			}
		}
		basis := []map[string]any{}
		for _, asset := range allBasis {
			if asset["ref"] != row["subject_ref"] && asset["ref"] != row["approval_ref"] {
				basis = append(basis, asset)
			}
		}
		if len(basis) == 0 {
			return nil, ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "缺少独立审查依据: "+boundary)
		}
		out = append(out, map[string]any{"boundary": boundary, "subject_ref": row["subject_ref"], "subject_digest": digest, "approval_scope": row["approval_scope"], "basis": basis, "drafter_principal_ref": row["drafter_principal_ref"]})
	}
	return out, nil
}

func currentReviewContextSemantic(s *SemanticSession, context map[string]any, boundary string) error {
	for _, field := range reviewContextFields {
		if field != "plan_review_binding" && context[field] == nil {
			return ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "缺少完整当前 review_context: "+field)
		}
	}
	for _, field := range []string{"check_ids", "capability_ids", "approval_scope"} {
		if !apUnique(context[field]) {
			return ApFail(s, "APPROVAL_CONTEXT_REQUIRED", field+" 无效")
		}
	}
	if strings.TrimSpace(ApText(context["reviewer_principal_ref"])) == "" || strings.TrimSpace(ApText(context["drafter_principal_ref"])) == "" || context["reviewer_principal_ref"] == context["drafter_principal_ref"] {
		return ApFail(s, "REVIEW_NOT_INDEPENDENT", "审查实例必须不同于起草实例")
	}
	for _, pair := range [][2]string{{"candidate_ref", "candidate_digest"}, {"policy_ref", "policy_digest"}} {
		ref := ApText(context[pair[0]])
		binding, e := s.Bind(ref)
		if e != nil {
			return e
		}
		if !apHashValid(context[pair[1]], false) || strings.TrimPrefix(binding.Digest, "sha256:") != context[pair[1]] {
			return ApFail(s, "REVIEW_BINDING_STALE", "当前候选或政策摘要过期: "+ref)
		}
	}
	if context["policy_ref"] != ApprovalRolesRef {
		return ApFail(s, "GATE_POLICY_REQUIRED", "政策引用必须指向唯一角色事实源")
	}
	if len(ApArray(context["basis"])) == 0 {
		return ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "缺少唯一当前basis")
	}
	basisSeen := map[string]bool{}
	for _, asset := range ApRows(context["basis"]) {
		ref := ApText(asset["ref"])
		if ref == "" || basisSeen[ref] || !apHashValid(asset["digest"], false) {
			return ApFail(s, "REVIEW_BINDING_STALE", "当前依据重复或摘要非法")
		}
		basisSeen[ref] = true
	}
	rows := ApRows(context["current_approvals"])
	rowBoundaries := []string{}
	for _, row := range rows {
		rowBoundaries = append(rowBoundaries, ApText(row["boundary"]))
	}
	if !ApSetEqual(rowBoundaries, context["check_ids"]) {
		return ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "current_approvals 必须完整唯一覆盖check_ids")
	}
	selected := rows
	narrow := context["plan_review_binding"] != nil && boundary != ""
	if narrow {
		selected = nil
		for _, row := range rows {
			if ApText(row["boundary"]) == boundary {
				selected = append(selected, row)
			}
		}
	}
	if len(selected) == 0 {
		return ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "消费边界不属于当前审查任务")
	}
	selectedRefs := map[string]bool{}
	for _, row := range selected {
		for _, asset := range ApRows(row["basis"]) {
			selectedRefs[ApText(asset["ref"])] = true
		}
	}
	for _, asset := range ApRows(context["basis"]) {
		ref := ApText(asset["ref"])
		if !narrow || selectedRefs[ref] || ref == ApprovalRegistryRef || ref == ApprovalSkillsRef {
			binding, e := s.Bind(ref)
			if e != nil {
				return e
			}
			if strings.TrimPrefix(binding.Digest, "sha256:") != asset["digest"] {
				return ApFail(s, "REVIEW_BINDING_STALE", "当前依据摘要过期: "+ref)
			}
		}
	}
	for _, row := range rows {
		if !apHashValid(row["subject_digest"], false) {
			return ApFail(s, "REVIEW_BINDING_STALE", "逐项主体摘要非法")
		}
		if !narrow || ApText(row["boundary"]) == boundary {
			binding, e := s.Bind(ApText(row["subject_ref"]))
			if e != nil {
				return e
			}
			if strings.TrimPrefix(binding.Digest, "sha256:") != row["subject_digest"] {
				return ApFail(s, "REVIEW_BINDING_STALE", "当前逐项主体已变化")
			}
		}
		if !apUnique(row["approval_scope"]) || strings.TrimSpace(ApText(row["drafter_principal_ref"])) == "" || row["drafter_principal_ref"] == context["reviewer_principal_ref"] {
			return ApFail(s, "REVIEW_NOT_INDEPENDENT", "逐项范围或独立作者来源无效")
		}
		for _, scope := range ApStrings(row["approval_scope"]) {
			if !ApContains(context["approval_scope"], scope) {
				return ApFail(s, "REVIEW_NOT_INDEPENDENT", "逐项范围超出任务范围")
			}
		}
		if len(ApArray(row["basis"])) == 0 {
			return ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "逐项basis缺失")
		}
		seen := map[string]bool{}
		for _, asset := range ApRows(row["basis"]) {
			ref := ApText(asset["ref"])
			if ref == "" || seen[ref] {
				return ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "逐项basis重复或空引用")
			}
			seen[ref] = true
			found := false
			for _, bound := range ApRows(context["basis"]) {
				found = found || ApEqual(bound, asset)
			}
			if !found {
				return ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "逐项basis不属于当前任务")
			}
		}
	}
	candidate, e := s.Doc(ApText(context["candidate_ref"]))
	if e != nil {
		return e
	}
	source := ApMap(candidate["source_checkpoint"])
	if ApNumber(candidate["schema_version"]) != 1 || ApText(candidate["kind"]) != "review-subject" || !ApEqual(candidate["current_approvals"], rows) || ApText(source["ref"]) == "" || !apHashValid(source["digest"], false) {
		return ApFail(s, "REVIEW_BINDING_STALE", "审阅包未绑定逐项当前上下文与checkpoint来源")
	}
	boundaries := context["check_ids"]
	if narrow {
		boundaries = []string{boundary}
	}
	current, e := currentApprovalsFromCheckpointSemantic(s, ApText(source["ref"]), boundaries)
	if e != nil {
		return e
	}
	var checkpointDigest string
	if ApText(source["binding_kind"]) == "current-approvals-v1" {
		checkpointDigest, _ = SemanticDocumentDigest(current)
	} else {
		binding, e := s.Bind(ApText(source["ref"]))
		if e != nil {
			return e
		}
		checkpointDigest = strings.TrimPrefix(binding.Digest, "sha256:")
	}
	if !narrow && checkpointDigest != source["digest"] || !ApEqual(current, selected) {
		return ApFail(s, "REVIEW_BINDING_STALE", "逐项预期或checkpoint批准输入已变化")
	}
	return nil
}

func ValidateReviewTaskSemantic(s *SemanticSession, ref string, task, expected map[string]any, boundary string, requireCompleted bool) (map[string]any, error) {
	if e := s.ValidateSchema(ApprovalTaskSchemaRef, task); e != nil {
		return nil, e
	}
	if ApNumber(task["schema_version"]) != 1 || !ApContains([]string{"Reviewer", "Verifier"}, ApText(task["execution_state"])) || !ApContains([]string{"active", "resolved"}, ApText(task["workflow_status"])) || ApText(ApMap(task["contract"])["status"]) != "issued" {
		return nil, ApFail(s, "REVIEW_CAPABILITY_STATE", "只接受issued正式v1 Reviewer/Verifier任务")
	}
	context := ApMap(task["review_context"])
	if e := currentReviewContextSemantic(s, context, boundary); e != nil {
		return nil, e
	}
	policy, e := s.Doc(ApText(context["policy_ref"]))
	if e != nil {
		return nil, e
	}
	if !ApEqual(policy, s.Roles) {
		return nil, ApFail(s, "REVIEW_BINDING_STALE", "角色政策与当前事实源不一致")
	}
	role := ApFind(s.Roles["roles"], "id", ApText(task["role_id"]))
	if role == nil || ApFind(s.Roles["runtimes"], "id", ApText(task["runtime_id"])) == nil {
		return nil, ApFail(s, "REVIEW_CAPABILITY_MISSING", "未登记审查角色或运行时")
	}
	if e = ValidateTaskSkillSourceSemantic(s, task, false); e != nil {
		return nil, e
	}
	compiled, e := CompileReviewCapabilitiesSemantic(s, context["check_ids"], ApText(task["role_id"]), ApText(task["execution_state"]))
	if e != nil {
		return nil, e
	}
	unit := ApFind(s.Registry["work_units"], "id", ApText(task["work_unit_id"]))
	candidate, e := s.Doc(ApText(context["candidate_ref"]))
	if e != nil {
		return nil, e
	}
	checkpointRef := ApText(ApMap(candidate["source_checkpoint"])["ref"])
	if unit == nil || ApText(unit["scope"]) == "template-source" || ApText(task["work_unit_id"]) == "work-unit.slice-implementation" || ApText(task["stage_id"]) == "" || ApFind(s.Registry["stages"], "id", ApText(task["stage_id"])) == nil || ApText(unit["stage"]) != "" && task["stage_id"] != unit["stage"] {
		return nil, ApFail(s, "REVIEW_TASK_INVALID", "正式生命周期审查任务工作单元或阶段不匹配")
	}
	contract := ApMap(task["contract"])
	convergence := ApMap(task["convergence"])
	if ApText(contract["kind"]) != "lifecycle-work-unit" || ApTruthy(contract["slice_contract_ref"]) || ApTruthy(contract["maintenance_ref"]) || contract["contract_ref"] != context["candidate_ref"] || ApText(contract["lifecycle_ref"]) != checkpointRef || ApText(task["checkpoint_ref"]) != checkpointRef || convergence["parent_work_unit"] != task["work_unit_id"] || ApText(convergence["convergence_ref"]) != checkpointRef {
		return nil, ApFail(s, "REVIEW_TASK_INVALID", "正式审查合同、checkpoint与汇合来源不一致")
	}
	inputs := []string{checkpointRef, ApText(context["policy_ref"]), ApText(context["candidate_ref"])}
	for _, asset := range ApRows(context["basis"]) {
		inputs = append(inputs, ApText(asset["ref"]))
	}
	for _, row := range ApRows(context["current_approvals"]) {
		inputs = append(inputs, ApText(row["subject_ref"]))
	}
	for _, input := range inputs {
		if !ApContains(task["inputs"], input) {
			return nil, ApFail(s, "REVIEW_TASK_INVALID", "正式审查输入遗漏来源: "+input)
		}
	}
	if !ApSetEqual(context["capability_ids"], compiled.capabilities) || !ApArraysEqual(ApMap(task["skill_source"])["review_skills"], compiled.skills) || !ApContains(compiled.Stages, ApText(task["stage_id"])) {
		return nil, ApFail(s, "REVIEW_CAPABILITY_MISSING", "能力/补充技能/审查阶段必须由当前政策编译")
	}
	if context["implementation_actor_id"] == task["actor_id"] {
		return nil, ApFail(s, "REVIEW_NOT_INDEPENDENT", "审查actor不得等于实现actor")
	}
	protected := append(inputs, ApText(context["candidate_ref"]))
	for _, allowed := range ApStrings(task["allowed_write_paths"]) {
		for _, input := range protected {
			if TaskWithinPath(input, allowed) {
				return nil, ApFail(s, "REVIEW_CAPABILITY_WRITE_SCOPE", "审查写范围覆盖候选、政策或依据")
			}
		}
	}
	if expected != nil {
		for _, field := range reviewContextFields {
			same := ApEqual(context[field], expected[field])
			if field == "check_ids" || field == "capability_ids" || field == "approval_scope" {
				same = ApSetEqual(context[field], expected[field])
			}
			if !same {
				return nil, ApFail(s, "REVIEW_BINDING_STALE", "任务未绑定当前消费者 "+field)
			}
		}
	}
	if e = s.Verify("plan-review-task", ref, map[string]string{"boundary": boundary, "require-completed": fmt.Sprint(requireCompleted)}); e != nil {
		return nil, e
	}
	out := ApCopy(context)
	out["review_skills"] = compiled.skills
	out["review_stages"] = compiled.Stages
	return out, nil
}

func ApprovalExpectedFromTaskSemantic(s *SemanticSession, ref string, task map[string]any, boundary, digest string, expected map[string]any) (map[string]any, error) {
	context, e := ValidateReviewTaskSemantic(s, ref, task, expected, boundary, false)
	if e != nil {
		return nil, e
	}
	row := ApFind(context["current_approvals"], "boundary", boundary)
	if row == nil || !ApContains(context["check_ids"], boundary) {
		return nil, ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "任务缺少边界独立当前来源")
	}
	binding, e := s.Bind(ref)
	if e != nil {
		return nil, e
	}
	if !apHashValid(digest, false) || strings.TrimPrefix(binding.Digest, "sha256:") != digest {
		return nil, ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "缺少当前任务文件及摘要")
	}
	actual, e := s.Doc(ref)
	if e != nil {
		return nil, e
	}
	if !ApEqual(task, actual) {
		return nil, ApFail(s, "REVIEW_BINDING_STALE", "传入任务不是当前文件字节")
	}
	out := ApCopy(row)
	review := ApCopy(ApMap(task["review_context"]))
	review["review_task_ref"] = ref
	review["review_task_digest"] = digest
	out["review_context"] = review
	return out, nil
}

func assertReviewCapabilityBindingSemantic(s *SemanticSession, record, expected map[string]any) error {
	ref := ApText(expected["review_task_ref"])
	digest := ApText(expected["review_task_digest"])
	if ref == "" || !apHashValid(digest, false) {
		return ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "缺少消费者当前review task来源")
	}
	if ApText(record["review_task_ref"]) != ref || ApText(record["review_task_digest"]) != digest {
		return ApFail(s, "REVIEW_BINDING_STALE", "会签未绑定当前审查任务")
	}
	binding, e := s.Bind(ref)
	if e != nil {
		return e
	}
	if strings.TrimPrefix(binding.Digest, "sha256:") != digest {
		return ApFail(s, "REVIEW_BINDING_STALE", "审查任务字节已变化")
	}
	task, e := s.Doc(ref)
	if e != nil {
		return e
	}
	context, e := ValidateReviewTaskSemantic(s, ref, task, expected, ApText(record["gate_id"]), true)
	if e != nil {
		return e
	}
	if !ApContains(context["check_ids"], ApText(record["gate_id"])) || !ApSetEqual(record["capability_ids"], context["capability_ids"]) {
		return ApFail(s, "REVIEW_CAPABILITY_MISSING", "会签边界或能力不属于当前任务")
	}
	row := ApFind(context["current_approvals"], "boundary", ApText(record["gate_id"]))
	if record["role_id"] != task["role_id"] || record["runtime_id"] != task["runtime_id"] || record["principal_ref"] != context["reviewer_principal_ref"] || record["drafter_principal_ref"] != row["drafter_principal_ref"] {
		return ApFail(s, "REVIEW_NOT_INDEPENDENT", "会签与任务角色、运行时或独立身份不匹配")
	}
	if context["plan_review_binding"] != nil && !ApEqual(record["plan_review_binding"], context["plan_review_binding"]) {
		return ApFail(s, "PLAN_REVIEW_RESULT_MISMATCH", "Plan结论未绑定已完成当前专业审查尝试")
	}
	return nil
}

func ValidateTaskSkillSourceSemantic(s *SemanticSession, task map[string]any, compile bool) error {
	role := TaskRole(s, ApText(task["role_id"]))
	source := ApMap(task["skill_source"])
	if role == nil || source["registry_ref"] != ApprovalRolesRef || ApText(source["defaults_ref"]) != "taskPackageDefaults("+ApText(task["role_id"])+")" || !ApArraysEqual(source["core_skills"], role["core_skills"]) || !ApArraysEqual(source["forbidden_skills"], role["forbidden_skills"]) {
		return ApFail(s, "REVIEW_CAPABILITY_FORBIDDEN", "角色core/forbidden技能不得改变")
	}
	if compile && (source["review_skills"] != nil || ApMap(task["review_context"])["capability_ids"] != nil) {
		var result reviewCompilation
		var e error
		if ApMap(task["review_context"])["capability_ids"] != nil {
			result, e = CompileReviewCapabilitiesSemantic(s, ApMap(task["review_context"])["check_ids"], ApText(task["role_id"]), ApText(task["execution_state"]))
		} else {
			result, e = compileWorkUnitReviewSemantic(s, ApText(task["work_unit_id"]), ApText(task["role_id"]), ApText(task["execution_state"]))
		}
		if e != nil {
			return e
		}
		if !ApArraysEqual(source["review_skills"], result.skills) {
			return ApFail(s, "REVIEW_CAPABILITY_MISSING", "review_skills必须由权威政策编译")
		}
	}
	return nil
}

func TaskWithinPath(child, parent string) bool {
	a, b := path.Clean(child), path.Clean(parent)
	return a == b || strings.HasPrefix(a, b+"/")
}
