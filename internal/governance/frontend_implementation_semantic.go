package governance

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The native Spec route consumes the registered implementation evidence,
// without manufacturing an external Receipt or granting execution authority.
func localFrontendImplementationSelected(s *kernel.SemanticSession, cpRef string, cp map[string]any) (bool, error) {
	local, err := hasLocalImplementationInputs(s)
	if err != nil || !local {
		return false, err
	}
	if guidanceArtifact(cp, "artifact.frontend-delivery-acceptance", "frontend_delivery_ref", "acceptance_ref") != "" {
		return false, nil
	}
	configRef, _, err := kernel.ProgressionLocation(s, cpRef)
	if err != nil {
		return false, err
	}
	if present, err := s.Exists(configRef); err != nil {
		return false, err
	} else if present {
		raw, err := s.Bytes(configRef)
		if err != nil {
			return false, err
		}
		intent, err := parseProgressionTarget(raw)
		if err != nil {
			return false, err
		}
		if intent.CheckpointRef != cpRef || intent.FeatureID != kernel.Text(cp["feature_id"]) {
			return false, s.Reject("FRONTEND_BINDING", "前端完成目标未绑定当前功能")
		}
		for _, consumer := range intent.Consumers {
			if consumer.Profile == "frontend" {
				return false, nil
			}
		}
	}
	if ref := progressionSliceRef(cp); ref != "" {
		c, err := loadNativeSlice(s, ref)
		if err != nil {
			return false, err
		}
		if c.Basis["frontend_delivery"] != nil || len(kernel.SemMap(kernel.SemMap(c.Normalized["resolution"])["frontend_delivery"])) != 0 || len(kernel.SemMap(kernel.SemMap(c.Normalized["frontend"])["delivery"])) != 0 {
			return false, nil
		}
	}
	return true, nil
}

func frontendImplementationAsset(s *kernel.SemanticSession, cp map[string]any, id string) (string, map[string]any, error) {
	asset := kernel.SemMap(kernel.SemMap(cp["artifacts"])[id])
	if kernel.Text(asset["ref"]) == "" {
		return "", nil, s.Reject("FRONTEND_IMPLEMENTATION_REQUIRED", "当前前端实施资产未登记："+id)
	}
	if err := kernel.RejectProgressionEvidence(s, kernel.Text(asset["ref"])); err != nil {
		return "", nil, err
	}
	var doc map[string]any
	var err error
	if asset["digest"] != nil {
		doc, err = contractBoundDoc(s, asset)
	} else {
		doc, err = s.Doc(kernel.Text(asset["ref"]))
	}
	return kernel.Text(asset["ref"]), doc, err
}

// This is the existing schema2 plan/verification protocol. Current completion
// validates current raw evidence before an independent check is produced.
// Progression completion separately requires the real check and gate approval.
func contractFrontendImplementation(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	cpRef := kernel.First(opts["checkpoint"], s.CheckpointRef)
	_, cp, err := kernel.ProgressionLocation(s, cpRef)
	if err != nil {
		return err
	}
	local, err := localFrontendImplementationSelected(s, cpRef, cp)
	if err != nil {
		return err
	}
	if !local {
		return s.Unavailable("CAPABILITY", "当前完成须使用既有专职前端接收路线")
	}
	planRef, plan, err := frontendImplementationAsset(s, cp, "artifact.frontend-implementation-plan")
	if err != nil {
		return err
	}
	verificationRef, report, err := frontendImplementationAsset(s, cp, "artifact.frontend-implementation-verification")
	if err != nil {
		return err
	}
	if ref != verificationRef {
		return s.Reject("FRONTEND_BINDING", "前端验证不是当前功能登记的原始验证记录")
	}
	for _, doc := range []map[string]any{plan, report} {
		if doc["frontend_delivery"] != nil {
			return s.Reject("FRONTEND_BINDING", "显式前端接收绑定不得回退本地原始证据核验")
		}
	}
	sliceRef := progressionSliceRef(cp)
	c, err := loadNativeSlice(s, sliceRef)
	if err != nil {
		return err
	}
	if err = contractLocalFrontendInputs(s, c, map[string]string{"checkpoint": cpRef, "slice": kernel.Text(c.Raw["slice_id"]), "phase": "verification"}); err != nil {
		return err
	}
	schema := ".template-spec/process/schemas/frontend-implementation-evidence.schema.json"
	for _, doc := range []map[string]any{plan, report} {
		if err = s.ValidateSchema(schema, doc); err != nil {
			return err
		}
		if kernel.ContractN(doc["schema_version"]) != 2 || doc["template"] != false || doc["status"] != "approved" {
			return s.Reject("FRONTEND_IMPLEMENTATION_REQUIRED", "当前前端完成只消费正式批准的schema2证据")
		}
		serialized, err := contractJSON(doc)
		if err != nil {
			return err
		}
		if err = frontendImplementationPlaceholders(s, serialized); err != nil {
			return err
		}
		if (doc["slice_contract_ref"] != nil && doc["slice_contract_ref"] != c.Ref) || (doc["slice_id"] != nil && doc["slice_id"] != c.Raw["slice_id"]) || doc["spec_ref"] != c.Basis["spec"]["ref"] {
			return s.Reject("FRONTEND_BINDING", "前端原始证据未绑定同功能当前完整Slice与Spec")
		}
		for _, key := range []string{"prototype_ref", "spec_ref"} {
			if err = kernel.RejectProgressionEvidence(s, kernel.Text(doc[key])); err != nil {
				return err
			}
			if _, err = s.Bytes(kernel.Text(doc[key])); err != nil {
				return err
			}
		}
	}
	if plan["kind"] != "plan" || report["kind"] != "verification" {
		return s.Reject("FRONTEND_IMPLEMENTATION_REQUIRED", "实施计划及原始验证类型错误")
	}
	if kernel.SemMap(report["implementation_plan"])["ref"] != planRef {
		return s.Reject("FRONTEND_BINDING", "前端验证未消费当前登记的实施计划")
	}
	if err = kernel.RejectProgressionEvidence(s, planRef); err != nil {
		return err
	}
	if _, err = contractBoundDoc(s, report["implementation_plan"]); err != nil {
		return err
	}
	for _, key := range []string{"visual_baseline", "route_and_page_inventory", "pnpm_commands", "spec_ref", "prototype_ref"} {
		if !contractSame(plan[key], report[key]) {
			return s.Reject("FRONTEND_BINDING", "实施计划与当前验证范围不一致："+key)
		}
	}
	visual := kernel.SemMap(report["visual_baseline"])
	manifestRef := kernel.Text(visual["manifest_ref"])
	if err = kernel.RejectProgressionEvidence(s, manifestRef); err != nil {
		return err
	}
	if c.Basis["visual_baseline"]["ref"] != manifestRef {
		return s.Reject("FRONTEND_BINDING", "前端验证未消费当前Slice冻结的Visual Baseline")
	}
	if _, err = contractBoundDoc(s, c.Basis["visual_baseline"]); err != nil {
		return err
	}
	baseline, err := contractVisualBaseline(s, manifestRef)
	if err != nil {
		return err
	}
	if baseline["baseline_id"] != visual["baseline_id"] || baseline["version"] != visual["version"] || kernel.SemMap(baseline["bundle"])["digest"] != visual["digest"] || baseline["status"] != "approved" {
		return s.Reject("FRONTEND_BINDING", "前端Visual Baseline身份或版本漂移")
	}
	manifestCases := map[string]map[string]any{}
	for _, row := range kernel.SemList(baseline["cases"]) {
		item := kernel.SemMap(row)
		manifestCases[kernel.Text(item["case_id"])] = item
	}
	cases := kernel.SemStrings(visual["case_ids"])
	for _, id := range cases {
		if manifestCases[id] == nil {
			return s.Reject("FRONTEND_BINDING", "前端验证引用不存在的冻结case")
		}
	}
	for _, id := range kernel.SemStrings(kernel.SemMap(kernel.SemMap(c.Raw["extensions"])["frontend"])["visual_baseline_case_ids"]) {
		if !kernel.SemHas(cases, id) {
			return s.Reject("FRONTEND_COVERAGE", "前端验证遗漏当前完整Slice的冻结case")
		}
	}
	for _, row := range kernel.SemList(report["route_and_page_inventory"]) {
		for _, id := range kernel.SemStrings(kernel.SemMap(row)["case_ids"]) {
			if !kernel.SemHas(cases, id) {
				return s.Reject("FRONTEND_COVERAGE", "页面case超出当前选定基线")
			}
		}
	}
	for _, id := range kernel.SemStrings(plan["baseline_case_ids"]) {
		if !kernel.SemHas(cases, id) {
			return s.Reject("FRONTEND_COVERAGE", "实施计划case超出当前基线")
		}
	}
	bound := func(ref, digest string) error {
		if err := kernel.RejectProgressionEvidence(s, ref); err != nil {
			return err
		}
		_, err := contractBinding(s, map[string]any{"ref": ref, "digest": digest})
		return err
	}
	states, coveredCases, pairs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, row := range kernel.SemList(report["interaction_results"]) {
		interaction := kernel.SemMap(row)
		if interaction == nil || interaction["result"] != "pass" {
			return s.Reject("FRONTEND_INTERACTION", "当前交互必须为通过的结构化原始记录")
		}
		state, id := kernel.Text(interaction["state"]), kernel.Text(interaction["case_id"])
		if !kernel.SemHas(plan["state_cases"], state) || !kernel.SemHas(cases, id) {
			return s.Reject("FRONTEND_INTERACTION", "交互超出当前批准计划")
		}
		pair := id + "\x00" + state
		if pairs[pair] {
			return s.Reject("FRONTEND_INTERACTION", "交互记录重复")
		}
		pairs[pair] = true
		if err = bound(kernel.Text(interaction["evidence_ref"]), kernel.Text(interaction["evidence_digest"])); err != nil {
			return err
		}
		states[state], coveredCases[id] = true, true
	}
	for _, state := range kernel.SemStrings(plan["state_cases"]) {
		if !states[state] {
			return s.Reject("FRONTEND_COVERAGE", "前端状态验收缺失")
		}
	}
	for _, id := range cases {
		if !coveredCases[id] {
			return s.Reject("FRONTEND_COVERAGE", "前端case交互验收缺失")
		}
	}
	console := kernel.SemMap(report["console_evidence"])
	if err = bound(kernel.Text(console["ref"]), kernel.Text(console["digest"])); err != nil {
		return err
	}
	if report["console_warning_check"] != "pass" || len(kernel.SemList(report["uncovered_differences"])) != 0 || kernel.SemMap(report["independent_review"])["status"] != "approved" {
		return s.Reject("FRONTEND_IMPLEMENTATION_FAILED", "console、视觉差异或独立还原审查未通过")
	}
	findingsRef := kernel.Text(kernel.SemMap(report["independent_review"])["findings_ref"])
	if err = kernel.RejectProgressionEvidence(s, findingsRef); err != nil {
		return err
	}
	if _, err = s.Bytes(findingsRef); err != nil {
		return err
	}
	actualCases := map[string]bool{}
	for _, row := range kernel.SemList(report["case_results"]) {
		item := kernel.SemMap(row)
		id := kernel.Text(item["case_id"])
		if !kernel.SemHas(cases, id) || actualCases[id] || item["result"] != "pass" || item["uncovered_difference"] != false {
			return s.Reject("FRONTEND_COVERAGE", "视觉case重复、越界或未通过")
		}
		actualCases[id] = true
		for _, key := range []string{"baseline_image_ref", "implementation_image_ref", "diff_ref"} {
			if err = kernel.RejectProgressionEvidence(s, kernel.Text(item[key])); err != nil {
				return err
			}
			if _, err = s.Bytes(kernel.Text(item[key])); err != nil {
				return err
			}
		}
		if item["mask_ref"] != "not-applicable" {
			if err = kernel.RejectProgressionEvidence(s, kernel.Text(item["mask_ref"])); err != nil {
				return err
			}
			if _, err = s.Bytes(kernel.Text(item["mask_ref"])); err != nil {
				return err
			}
		}
		for _, key := range []string{"implementation_image", "diff"} {
			if err = bound(kernel.Text(item[key+"_ref"]), kernel.Text(item[key+"_digest"])); err != nil {
				return err
			}
		}
	}
	if len(actualCases) != len(cases) {
		return s.Reject("FRONTEND_COVERAGE", "视觉case未完整一一覆盖")
	}
	commands := map[string]bool{}
	for _, row := range kernel.SemList(report["actual_verification"]) {
		item := kernel.SemMap(row)
		_, err := time.Parse(time.RFC3339Nano, kernel.Text(item["executed_at"]))
		if err != nil || kernel.ContractN(item["exit_code"]) != 0 {
			return s.Reject("FRONTEND_IMPLEMENTATION_FAILED", "实际pnpm验证失败或时间无效")
		}
		if err = bound(kernel.Text(item["evidence_ref"]), kernel.Text(item["evidence_digest"])); err != nil {
			return err
		}
		commands[kernel.Text(item["command"])] = true
	}
	for _, command := range kernel.SemStrings(plan["pnpm_commands"]) {
		if !commands[command] {
			return s.Reject("FRONTEND_COVERAGE", "当前pnpm验证命令覆盖缺失")
		}
	}
	s.Report.Coverage = map[string]any{"result": "implementation-evidence-verified", "ready_for_agent": false, "delivery_mode": "local-approved-assets", "root": s.Root, "checkpoint_ref": cpRef, "spec_ref": report["spec_ref"], "slice_contract_ref": c.Ref, "slice_id": c.Raw["slice_id"], "verification_ref": ref, "implementation_plan_ref": planRef, "inputs_current": false}
	return nil
}

func frontendImplementationPlaceholders(s *kernel.SemanticSession, serialized string) error {
	if present, err := s.Exists(worklayout.TrackerRef); err != nil {
		return err
	} else if present {
		if _, err = s.Bytes(worklayout.TrackerRef); err != nil {
			return err
		}
	}
	roots, err := worklayout.ReadScanRoots(s.Root)
	if err != nil {
		return err
	}
	placeholder := strings.Contains(serialized, "REPLACE_ME") || regexp.MustCompile(`<[^>]+>`).MatchString(serialized)
	for _, root := range roots {
		placeholder = placeholder || strings.Contains(serialized, root+"/example")
	}
	if placeholder {
		return s.Reject("FRONTEND_IMPLEMENTATION_REQUIRED", "当前前端证据含模板占位值")
	}
	return nil
}

func progressionFrontendImplementationCompletion(s *kernel.SemanticSession, cpRef string, cp map[string]any) error {
	ref := checkpointAsset(cp, "frontend_implementation_verification_ref", "artifact.frontend-implementation-verification")
	if err := contractFrontendImplementation(s, ref, map[string]string{"checkpoint": cpRef}); err != nil {
		return err
	}
	check := kernel.SemMap(kernel.SemMap(cp["checks"])["check.frontend-implementation-verified"])
	if !kernel.SemHas([]string{"passed", "approved"}, kernel.Text(check["status"])) || check["applicable"] != true || !kernel.SemHas(kernel.SemMap(check["evidence"])["evidence.frontend-implementation-verification"], ref) {
		return s.Reject("FRONTEND_REVIEW_REQUIRED", "当前原始前端验证未获已登记独立检查批准")
	}
	if err := s.Basis(check["basis"]); err != nil {
		return err
	}
	refs := map[string]bool{}
	for _, row := range kernel.SemList(check["basis"]) {
		refs[kernel.Text(kernel.SemMap(row)["ref"])] = true
	}
	if !refs[ref] || !refs[kernel.Text(check["subject_ref"])] || !refs[kernel.Text(check["approval_ref"])] {
		return s.Reject("FRONTEND_REVIEW_REQUIRED", "前端独立检查未绑定当前报告、原主体及批准")
	}
	if err := s.Verify("approval", kernel.Text(check["approval_ref"]), map[string]string{"checkpoint": cpRef, "boundary": "check.frontend-implementation-verified"}); err != nil {
		return err
	}
	if err := frontendImplementationCurrent(s, cpRef, cp, ref); err != nil {
		return err
	}
	s.Report.Coverage["result"] = "implementation-verified"
	return nil
}

// Completion reuses the current Slice and original code-review candidate;
// validating raw protocol evidence never manufactures this review context.
func frontendImplementationCurrent(s *kernel.SemanticSession, cpRef string, cp map[string]any, ref string) error {
	c, err := loadNativeSlice(s, progressionSliceRef(cp))
	if err != nil {
		return err
	}
	report, err := s.Doc(ref)
	if err != nil {
		return err
	}
	prototype := c.Basis["prototype_deliverable"]
	if prototype == nil {
		prototype = c.Basis["existing_ui_baseline"]
	}
	if prototype == nil || prototype["ref"] != report["prototype_ref"] {
		return s.Reject("FRONTEND_BINDING", "前端完成未绑定当前Slice批准的原型或既有UI基线")
	}
	if _, err = contractBinding(s, prototype); err != nil {
		return err
	}
	if prototypeRef := checkpointAsset(cp, "prototype_ref", "artifact.high-fidelity-prototype"); prototypeRef != "" && prototypeRef != report["prototype_ref"] {
		return s.Reject("FRONTEND_BINDING", "前端完成与当前功能批准原型不一致")
	}
	check := kernel.SemMap(kernel.SemMap(cp["checks"])["check.frontend-implementation-verified"])
	subject, err := s.Doc(kernel.Text(check["subject_ref"]))
	if err != nil {
		return err
	}
	input := kernel.SemMap(subject["review_input"])
	if input == nil {
		proof, err := s.Doc(kernel.Text(check["approval_ref"]))
		if err != nil {
			return err
		}
		taskRef := kernel.First(kernel.Text(subject["review_task_ref"]), kernel.Text(proof["review_task_ref"]))
		if taskRef == "" {
			return s.Reject("FRONTEND_CANDIDATE", "当前已签署主体缺少原实现候选审查输入")
		}
		if err = kernel.RejectProgressionEvidence(s, taskRef); err != nil {
			return err
		}
		bound := false
		for _, basis := range []any{check["basis"], subject["basis"]} {
			for _, row := range kernel.SemList(basis) {
				if kernel.SemMap(row)["ref"] == taskRef {
					bound = true
				}
			}
		}
		if !bound {
			return s.Reject("FRONTEND_CANDIDATE", "原实现审查任务未在当前签署依据中绑定原始字节")
		}
		if err = s.Basis(subject["basis"]); err != nil {
			return err
		}
		task, err := s.Doc(taskRef)
		if err != nil {
			return err
		}
		input = kernel.SemMap(task["review_input"])
	}
	if input["scope_kind"] != "change" || input["slice_contract_ref"] != c.Ref {
		return s.Reject("FRONTEND_CANDIDATE", "前端完成缺少同功能当前完整Slice的实现审查输入")
	}
	unit := kernel.Text(input["work_unit_id"])
	selected := kernel.SemanticDefinition(c.Normalized, "work_units", unit)
	if selected == nil || !kernel.SemHas([]string{"role.frontend-engineer", "role.frontend-agent"}, kernel.Text(selected["role_id"])) {
		return s.Reject("FRONTEND_CANDIDATE", "前端完成须选择当前批准前端工作单元")
	}
	root, err := backendProjectRoot(s, input)
	if err != nil {
		return err
	}
	for _, row := range kernel.SemList(c.Normalized["work_units"]) {
		item := kernel.SemMap(row)
		if !kernel.SemHas([]string{"role.frontend-engineer", "role.frontend-agent"}, kernel.Text(item["role_id"])) {
			continue
		}
		project := kernel.Text(item["project_root"])
		if !filepath.IsAbs(project) {
			project = filepath.Join(s.Root, project)
		}
		project, err = filepath.EvalSymlinks(project)
		if err != nil || project != root {
			return s.Reject("FRONTEND_CANDIDATE", "完整Slice的前端实现工程未由当前审查候选覆盖")
		}
		if err = contractSliceFresh(s, c, map[string]string{"checkpoint": cpRef, "approval-ref": cpRef, "unit": kernel.Text(item["id"])}); err != nil {
			return err
		}
	}
	if root != s.Root {
		if err = s.RegisterExternalRoot(root, c.Ref); err != nil {
			return err
		}
	}
	digest := kernel.Text(kernel.SemMap(report["independent_review"])["candidate_digest"])
	if input["review_mode"] == "worktree" {
		if strings.TrimPrefix(digest, "sha256:") != strings.TrimPrefix(kernel.Text(input["candidate_digest"]), "sha256:") {
			return s.Reject("FRONTEND_CANDIDATE", "前端还原报告与原工作树审查候选不一致")
		}
		_, err = backendWorktreeCurrent(s, root, input)
		return err
	}
	if input["review_mode"] != "committed" {
		return s.Reject("FRONTEND_CANDIDATE", "未知前端实现审查候选模式")
	}
	candidate := kernel.Text(input["implementation_candidate_ref"])
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(candidate) || digest != input["candidate_digest"] {
		return s.Reject("FRONTEND_CANDIDATE", "前端完成需要原固定提交及同一不可变tree身份")
	}
	_, err = candidateCommittedCurrent(s, root, candidate, digest, c.Ref, "FRONTEND_CANDIDATE")
	return err
}
