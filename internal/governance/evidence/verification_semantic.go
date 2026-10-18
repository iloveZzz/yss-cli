package evidence

import "github.com/iloveZzz/yss-cli/internal/governance/lifecycle"

import (
	"bytes"
	"github.com/iloveZzz/yss-cli/internal/governance/contracts"
	handoffdomain "github.com/iloveZzz/yss-cli/internal/governance/handoff"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func init() {
	kernel.RegisterSemanticValidator("verification", verifyVerificationSemantic)
	kernel.RegisterRoleIndependentValidator("verification", verifyVerificationSemantic)
}

// This validates persisted execution evidence. It never runs a command from it.
func verifyVerificationSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	doc, e := s.Doc(ref)
	if e != nil {
		return e
	}
	if result, ok := kernel.Object(doc["execution_result"]); ok {
		doc = result
	}
	kind := kernel.Text(doc["kind"])
	if doc["consumed_contract"] != nil || doc["work_unit_id"] != nil {
		boundOpts := kernel.SemanticOptions(opts)
		boundOpts["result-ref"] = ref
		return ContractExecutionResult(s, doc, boundOpts)
	}
	switch kind {
	case "verification":
		return s.Unavailable("CAPABILITY", "前端实现验证已从生命周期移除")
	case "template-verification-report":
		return contractTemplateVerification(s, doc, opts)
	case "frontend-scaffold-verification":
		return s.Unavailable("CAPABILITY", "前端脚手架验证已从生命周期移除")
	case "backend-contract", "backend-deployment":
		if e = s.ValidateSchema(".template-spec/process/schemas/backend-delivery-verification.schema.json", doc); e != nil {
			return e
		}
		basis := opts["subject-digest"]
		if basis == "" {
			basis, e = contractPublicVerificationBasis(s, ref, kind, opts)
			if e != nil {
				return e
			}
		}
		if kernel.Text(doc["subject_digest"]) != basis {
			return s.Reject("VERIFICATION_CURRENT_REQUIRED", "交付验证缺少独立当前 basis")
		}
		if e = contracts.ContractRecordedResults(s, doc["results"], true); e != nil {
			return e
		}
		for _, v := range kernel.SemList(doc["results"]) {
			r := kernel.SemMap(v)
			executed, _ := time.Parse(time.RFC3339Nano, kernel.Text(r["executed_at"]))
			if executed.After(time.Now().Add(time.Minute)) {
				return s.Reject("VERIFICATION_FAILED", "验证时间超出当前时点")
			}
		}
		return nil
	case "strategic-delivery-verification-v1":
		if e = s.ValidateSchema(".template-spec/process/schemas/strategic-handoff-delivery-verification.schema.json", doc); e != nil {
			return e
		}
		basis := opts["bundle-digest"]
		if basis == "" {
			basis, e = contractPublicVerificationBasis(s, ref, kind, opts)
			if e != nil {
				return e
			}
		}
		if kernel.Text(doc["result"]) != "verified" || kernel.ContractN(doc["exit_code"]) != 0 || kernel.Text(doc["bundle_digest"]) != basis {
			return s.Reject("VERIFICATION_CURRENT_REQUIRED", "交付验证缺少当前包绑定")
		}
		executed, err := time.Parse(time.RFC3339Nano, kernel.Text(doc["executed_at"]))
		if err != nil || executed.After(time.Now().Add(time.Minute)) {
			return s.Reject("VERIFICATION_FAILED", "战略交付验证时间无效或超过当前时点")
		}
		return nil
	default:
		return s.Unavailable("CAPABILITY", "未知验证报告语义: "+kind)
	}
}

// Public reports obtain their subject from an independently selected consumer,
// never from the candidate report's digest, package path, or kind. Published
// task results carry output paths rather than an invented artifacts property.
func contractPublicVerificationBasis(s *kernel.SemanticSession, reportRef, kind string, opts map[string]string) (string, error) {
	cpRef := opts["checkpoint"]
	if cpRef == "" {
		cpRef = s.CheckpointRef
	}
	taskRef := opts["task"]
	if taskRef == "" {
		taskRef = opts["task-ref"]
	}
	refs := []string{}
	if cpRef != "" {
		if e := s.Verify("checkpoint", cpRef, map[string]string{}); e != nil {
			return "", e
		}
		cp, e := s.Doc(cpRef)
		if e != nil {
			return "", e
		}
		if kind == "strategic-delivery-verification-v1" {
			basis, selected, err := contractExplicitDesignVerificationBasis(s, cpRef, cp, reportRef, taskRef)
			if selected || err != nil {
				return basis, err
			}
		}
		trace := kernel.SemMap(cp["stage_trace"])
		if kind != "strategic-delivery-verification-v1" && cp["mode"] == "audit" && trace["completed_work_unit"] == "work-unit.backend-delivery" {
			unit := kernel.ApFind(s.Registry["work_units"], "id", "work-unit.backend-delivery")
			if unit == nil || unit["scope"] != "project-instance" || cp["status"] != "routing" || cp["next_work_unit"] != nil || cp["stage"] != "stage.verification-release-retrospective" || trace["stage"] != cp["stage"] {
				return "", s.Reject("VERIFICATION_CONSUMER_ROUTE", "当前 Checkpoint 未处于已登记后端终点的只读审计状态")
			}
			source, e := contractPublicBackendVerificationSource(s, cpRef)
			if e != nil {
				return "", e
			}
			authorization, e := dependencies.ProgressionBackendAuthorization(s, cpRef, "", true)
			if e != nil {
				return "", e
			}
			for _, required := range []string{source, authorization.TerminalRef, reportRef} {
				found := false
				for _, observed := range kernel.SemStrings(trace["artifact_refs"]) {
					found = found || contractVerificationSameRef(s, required, observed)
				}
				if !found {
					return "", s.Reject("VERIFICATION_CONSUMER_ROUTE", "当前后端审计未独立登记交付、终点和待验报告")
				}
			}
			refs = append(refs, source)
		}
		if binding, present := kernel.Object(kernel.SemMap(cp["artifacts"])["artifact.fresh-verification"]); present && kind != "strategic-delivery-verification-v1" {
			if binding["status"] != "approved" || !contracts.ContractSHA(binding["digest"]) || !contractVerificationSameRef(s, reportRef, kernel.Text(binding["ref"])) {
				return "", s.Reject("VERIFICATION_CURRENT_REQUIRED", "当前 Fresh Verification 资产未登记该报告及其字节摘要")
			}
			if _, e = contracts.ContractBoundDoc(s, binding); e != nil {
				return "", e
			}
			source, e := contractPublicBackendVerificationSource(s, cpRef)
			if e != nil {
				return "", e
			}
			refs = append(refs, source)
		}
		// Design publishes the strategic source artifact ID. The finalized
		// delivery path follows its independently approved identity/version.
		if binding, present := kernel.Object(kernel.SemMap(cp["artifacts"])["artifact.strategic-design-handoff"]); present && kind == "strategic-delivery-verification-v1" {
			if binding["status"] != "approved" || !contracts.ContractSHA(binding["digest"]) {
				return "", s.Reject("VERIFICATION_CURRENT_REQUIRED", "当前 Checkpoint 交付资产未批准或缺少字节绑定")
			}
			handoff, err := contracts.ContractBoundDoc(s, binding)
			if err != nil {
				return "", err
			}
			if e = s.ValidateSchema(".template-spec/process/schemas/strategic-design-handoff-v5.schema.json", handoff); e != nil {
				return "", e
			}
			deliveryRef := path.Join("docs/deliveries/strategic", kernel.Text(handoff["handoff_id"]), kernel.Text(handoff["handoff_version"]), "delivery-record.json")
			_, record, e := handoffdomain.ContractOpenStrategicDelivery(s, deliveryRef)
			if e != nil {
				return "", e
			}
			source := kernel.SemMap(record["handoff"])
			if source["ref"] != binding["ref"] || source["sha256"] != binding["digest"] || source["id"] != handoff["handoff_id"] || source["version"] != handoff["handoff_version"] {
				return "", s.Reject("VERIFICATION_CURRENT_REQUIRED", "战略交付未绑定 Checkpoint 批准的当前 Handoff")
			}
			refs = append(refs, deliveryRef)
		}
	}
	if taskRef != "" {
		task, e := s.Doc(taskRef)
		if e != nil {
			return "", e
		}
		validator := "task"
		if kind != "strategic-delivery-verification-v1" && task["work_unit_id"] == "work-unit.backend-delivery" {
			validator = "backend-completed-producer-task"
		}
		if e := s.Verify(validator, taskRef, map[string]string{"checkpoint": cpRef}); e != nil {
			return "", e
		}
		result := kernel.SemMap(task["result"])
		if task["workflow_status"] != "resolved" || result["result"] != "completed" || result["work_unit"] != task["work_unit_id"] || cpRef != "" && task["checkpoint_ref"] != cpRef {
			return "", s.Reject("VERIFICATION_CURRENT_REQUIRED", "报告消费者须为当前已完整核验的正式完成任务")
		}
		producer := "work-unit.backend-delivery"
		if kind == "strategic-delivery-verification-v1" {
			producer = "work-unit.strategic-design-handoff"
		}
		unit := kernel.ApFind(s.Registry["work_units"], "id", producer)
		if task["work_unit_id"] != producer || unit == nil || unit["scope"] != "project-instance" {
			return "", s.Reject("VERIFICATION_CONSUMER_ROUTE", "正式任务未绑定已登记的专项交付生产工作单元")
		}
		backendSource := ""
		if kind != "strategic-delivery-verification-v1" {
			backendSource, e = contractPublicBackendVerificationSource(s, cpRef)
			if e != nil {
				return "", e
			}
		}
		for _, ref := range kernel.SemStrings(task["expected_evidence_files"]) {
			if !kernel.SemHas(result["evidence_refs"], ref) || !kernel.SemHas([]string{".json", ".yaml", ".yml"}, path.Ext(ref)) {
				continue
			}
			output, e := s.Doc(ref)
			if e != nil {
				return "", e
			}
			if kind == "strategic-delivery-verification-v1" && output["kind"] == "strategic-handoff-delivery-v1" || kind != "strategic-delivery-verification-v1" && output["delivery_id"] != nil && output["verification"] != nil && output["environment"] != nil {
				if backendSource != "" && !contractVerificationSameRef(s, ref, backendSource) {
					return "", s.Reject("VERIFICATION_CONSUMER_ROUTE", "任务产物未绑定当前持久化后端交付终点")
				}
				refs = append(refs, ref)
			}
		}
	}
	sort.Strings(refs)
	unique := []string{}
	for _, ref := range refs {
		if len(unique) == 0 || unique[len(unique)-1] != ref {
			unique = append(unique, ref)
		}
	}
	if len(unique) != 1 {
		return "", s.Reject("VERIFICATION_CURRENT_REQUIRED", "交付报告缺少唯一独立消费者登记")
	}
	if e := s.CurrentAsset(unique[0]); e != nil {
		return "", e
	}
	if kind == "strategic-delivery-verification-v1" {
		bundle, record, e := handoffdomain.ContractOpenStrategicDelivery(s, unique[0])
		if e != nil {
			return "", e
		}
		expectedRef := path.Join(path.Dir(unique[0]), kernel.Text(record["verification_ref"]))
		if !contractVerificationSameRef(s, reportRef, expectedRef) {
			return "", s.Reject("VERIFICATION_CURRENT_REQUIRED", "待验报告不是消费者登记的战略交付验证")
		}
		return kernel.Text(bundle.Manifest["bundle_digest"]), nil
	}
	delivery, e := dependencies.BackendInspectDelivery(s, unique[0], nil)
	if e != nil {
		return "", e
	}
	binding := kernel.SemMap(kernel.SemMap(delivery["verification"])[strings.TrimPrefix(kind, "backend-")])
	if !contractVerificationSameRef(s, reportRef, kernel.Text(binding["ref"])) {
		return "", s.Reject("VERIFICATION_CURRENT_REQUIRED", "待验报告不是消费者登记的后端交付验证")
	}
	if _, e = contracts.ContractBoundDoc(s, binding); e != nil {
		return "", e
	}
	body := map[string]any{}
	for _, key := range []string{"delivery_id", "version", "strategic_bundle_digest", "strategic_route_id", "scope", "openapi", "slice_contract", "build", "environment"} {
		if value, present := delivery[key]; present {
			body[key] = value
		}
	}
	return contracts.ContractDigest(body), nil
}

// Intent only selects a consumer. Its current original Receipt, signing
// authority and full delivery determine the verification subject independently
// of the candidate report. This reader is shared by public verification and
// milestone evaluation and never invokes the business evaluator.
func contractExplicitDesignVerificationBasis(s *kernel.SemanticSession, cpRef string, cp map[string]any, reportRef, taskRef string) (string, bool, error) {
	if s.Report.Profile != "spec" || !strings.HasPrefix(kernel.Text(cp["feature_id"]), "feature.") {
		return "", false, nil
	}
	contractRef, err := s.OrchestrationReference()
	if err != nil {
		return "", false, err
	}
	present, err := s.Exists(contractRef)
	if err != nil || !present {
		return "", false, err // Legacy reports may not install this policy asset.
	}
	contract, err := s.Doc(contractRef)
	if err != nil {
		return "", false, err
	}
	if _, declared := contract["progression_target"]; !declared {
		return "", false, nil // Legacy reports retain their existing ownership.
	}
	if _, _, err = kernel.ProgressionPolicy(s); err != nil {
		return "", false, err
	}
	configRef, _, err := kernel.ProgressionLocation(s, cpRef)
	if err != nil {
		return "", false, err
	}
	present, err = s.Exists(configRef)
	if err != nil || !present {
		return "", false, err
	}
	raw, err := s.Bytes(configRef)
	if err != nil {
		return "", false, err
	}
	intent, err := dependencies.ParseProgressionTarget(raw)
	if err != nil {
		return "", false, err
	}
	if intent.FeatureID != kernel.Text(cp["feature_id"]) || intent.CheckpointRef != cpRef {
		return "", false, s.Reject("PROGRESSION_BINDING", "验证报告的显式消费者意图未绑定当前功能 checkpoint")
	}
	if err = dependencies.ProgressionScope(s, intent.Target); err != nil {
		return "", false, err
	}
	for _, consumer := range intent.Consumers {
		if consumer.Profile != "design" {
			continue
		}
		if taskRef != "" || dependencies.GuidanceArtifact(cp, "artifact.strategic-design-handoff", "strategic_handoff_ref", "handoff_ref") != "" {
			return "", true, s.Reject("VERIFICATION_CONSUMER_ROUTE", "战略验证同时登记本端与外部 Design 生产来源")
		}
		// A backend or frontend is irrelevant to this report's source selection.
		// Their terminal qualification remains mandatory for whole business.
		intent.Consumers = []lifecycle.ProgressionConsumer{consumer}
		rows, sessions := dependencies.ProgressionConsumers(s, "spec", cpRef, cp, intent)
		row, design := kernel.SemMap(rows["design"]), sessions["design"]
		if design == nil || row["input_status"] != "verified" || row["completion_status"] != "reached" {
			return "", true, s.Reject("VERIFICATION_CURRENT_REQUIRED", "外部 Design 的当前来源接收或本端交付未核验："+kernel.First(kernel.Text(row["reason"]), kernel.Text(row["completion_reason"])))
		}
		current, err := design.Doc(consumer.CheckpointRef)
		if err != nil {
			return "", true, err
		}
		ref, present, err := dependencies.GuidanceStrategicDeliveryRef(design, current)
		if err != nil {
			return "", true, err
		}
		if !present {
			return "", true, s.Reject("VERIFICATION_CURRENT_REQUIRED", "外部 Design 缺少当前批准 Handoff 的完整交付登记")
		}
		bundle, record, err := handoffdomain.ContractOpenStrategicDelivery(design, ref)
		if err != nil {
			return "", true, err
		}
		asset := kernel.SemMap(kernel.SemMap(current["artifacts"])["artifact.strategic-design-handoff"])
		if asset["status"] != "approved" || !contracts.ContractSHA(asset["digest"]) {
			return "", true, s.Reject("VERIFICATION_CURRENT_REQUIRED", "外部 Design 的当前 Handoff 资产缺少批准或原始字节摘要")
		}
		handoff, err := contracts.ContractBoundDoc(design, asset)
		if err != nil {
			return "", true, err
		}
		published := kernel.SemMap(record["handoff"])
		if published["ref"] != asset["ref"] || published["sha256"] != asset["digest"] || !contracts.ContractSame(handoff, bundle.Handoff) {
			return "", true, s.Reject("VERIFICATION_CURRENT_REQUIRED", "外部 Design 当前 Handoff 与完整交付包的原始批准资产不一致")
		}
		expectedRef := path.Join(path.Dir(ref), kernel.Text(record["verification_ref"]))
		if err = contractVerificationOriginalCopy(s, reportRef, design, expectedRef); err != nil {
			return "", true, err
		}
		return kernel.Text(bundle.Manifest["bundle_digest"]), true, nil
	}
	return "", false, nil
}

func contractVerificationOriginalCopy(s *kernel.SemanticSession, reportRef string, source *kernel.SemanticSession, sourceRef string) error {
	actual, err := s.Bytes(reportRef)
	if err != nil {
		return err
	}
	original, err := source.Bytes(sourceRef)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, original) {
		return s.Reject("VERIFICATION_CURRENT_REQUIRED", "待验冻结报告原始字节与当前外部 Design 交付验证不同")
	}
	return nil
}

// The fixed terminal producer owns the delivery reference. A report or a task
// cannot choose another valid delivery as its current verification subject.
func contractPublicBackendVerificationSource(s *kernel.SemanticSession, cpRef string) (string, error) {
	authorization, e := dependencies.ProgressionBackendAuthorization(s, cpRef, "", true)
	if e != nil {
		return "", e
	}
	ref := authorization.TerminalRef
	exists, e := s.Exists(ref)
	if e != nil {
		return "", e
	}
	if !exists {
		return "", s.Reject("VERIFICATION_CURRENT_REQUIRED", "缺少当前已发布后端交付终点")
	}
	if e = s.Verify("backend-terminal", ref, map[string]string{"checkpoint": cpRef}); e != nil {
		return "", e
	}
	record, e := s.Doc(ref)
	if e != nil {
		return "", e
	}
	binding := kernel.SemMap(record["delivery"])
	if _, e = dependencies.BackendBound(s, binding); e != nil {
		return "", e
	}
	return kernel.Text(binding["ref"]), nil
}

func contractVerificationSameRef(s *kernel.SemanticSession, a, b string) bool {
	x, e := ContractVerificationRef(s, a)
	if e != nil {
		return false
	}
	y, e := ContractVerificationRef(s, b)
	return e == nil && x == y
}

func ContractVerificationRef(s *kernel.SemanticSession, ref string) (string, error) {
	if filepath.IsAbs(ref) {
		local, e := filepath.Rel(s.Root, ref)
		if e != nil || local == ".." || strings.HasPrefix(local, ".."+string(filepath.Separator)) {
			if filepath.Clean(ref) != ref {
				return "", s.Reject("VERIFICATION_PATH", "验证路径必须规范")
			}
			for root := range s.ExternalViews {
				rel, err := filepath.Rel(root, ref)
				if err == nil && kernel.ContractPath(filepath.ToSlash(rel)) {
					return ref, nil
				}
			}
			return "", s.Reject("VERIFICATION_PATH", "验证路径未登记在当前项目或批准工程")
		}
		ref = filepath.ToSlash(local)
	}
	if !kernel.ContractPath(ref) {
		return "", s.Reject("VERIFICATION_PATH", "验证引用路径非法")
	}
	return ref, nil
}

func contractVerificationBytes(s *kernel.SemanticSession, ref string) ([]byte, error) {
	ref, e := ContractVerificationRef(s, ref)
	if e != nil {
		return nil, e
	}
	if !filepath.IsAbs(ref) {
		return s.Bytes(ref)
	}
	selected := ""
	for root := range s.ExternalViews {
		rel, err := filepath.Rel(root, ref)
		if err == nil && kernel.ContractPath(filepath.ToSlash(rel)) && len(root) > len(selected) {
			selected = root
		}
	}
	if selected == "" {
		return nil, s.Reject("VERIFICATION_PATH", "验证路径没有批准工程登记")
	}
	rel, e := filepath.Rel(selected, ref)
	if e != nil {
		return nil, e
	}
	return s.ExternalBytes(selected, filepath.ToSlash(rel))
}

func contractVerificationDoc(s *kernel.SemanticSession, ref string) (map[string]any, error) {
	b, e := contractVerificationBytes(s, ref)
	if e != nil {
		return nil, e
	}
	v, e := schema.Parse(b)
	if e != nil {
		return nil, s.Reject("VERIFICATION_FORMAT", e.Error())
	}
	m, ok := kernel.Object(v)
	if !ok {
		return nil, s.Reject("VERIFICATION_FORMAT", "验证资产必须为对象")
	}
	return m, nil
}

func contractTemplateVerification(s *kernel.SemanticSession, doc map[string]any, opts map[string]string) error {
	if kernel.ContractN(doc["schema_version"]) != 1 || kernel.Text(doc["status"]) != "passed" || len(kernel.SemList(doc["unexecuted"])) > 0 || doc["input_drift"] == true || doc["source_state"] == "working-tree" {
		return s.Reject("VERIFICATION_FAILED", "模板验证未完整通过或输入漂移")
	}
	if !contracts.ContractDate(doc["started_at"]) || !contracts.ContractDate(doc["finished_at"]) {
		return s.Reject("VERIFICATION_REQUIRED", "模板报告缺少实际起止时间")
	}
	if opts["input-digest"] == "" || kernel.Text(doc["input_sha256"]) != opts["input-digest"] {
		return s.Reject("VERIFICATION_CURRENT_REQUIRED", "模板验证需要消费者独立当前输入摘要")
	}
	plan := kernel.SemList(doc["plan"])
	results := kernel.SemList(doc["results"])
	if len(plan) == 0 || len(results) != len(plan) {
		return s.Reject("VERIFICATION_FAILED", "验证计划有未执行项")
	}
	seen := map[int]bool{}
	for _, v := range results {
		r := kernel.SemMap(v)
		n, ok := kernel.Integer(r["index"])
		index := int(n)
		if !ok || index < 0 || index >= len(plan) || seen[index] {
			return s.Reject("VERIFICATION_FAILED", "验证结果索引缺失、重复或越界")
		}
		seen[index] = true
		entry := kernel.SemMap(plan[index])
		exit, ok := kernel.Integer(r["exit_code"])
		if !ok || exit != 0 || kernel.Text(r["command"]) == "" || !contracts.ContractSame(r["command"], entry["command"]) {
			return s.Reject("VERIFICATION_FAILED", "验证命令或真实退出码不一致")
		}
		for _, key := range []string{"stdout_ref", "stderr_ref"} {
			if kernel.Text(r[key]) != "" {
				if _, e := s.Bytes(kernel.Text(r[key])); e != nil {
					return e
				}
			}
		}
	}
	return nil
}

func ContractExecutionResult(s *kernel.SemanticSession, result map[string]any, opts map[string]string) error {
	if kernel.ContractN(result["schema_version"]) != 2 {
		return s.Reject("EXECUTION_RESULT_SCHEMA", "执行报告须为 schema v2")
	}
	status := kernel.Text(result["status"])
	if !kernel.SemHas([]string{"implemented", "seam-deferred", "drift", "violation", "not-applicable"}, status) {
		return s.Reject("EXECUTION_RESULT_STATUS", "未知执行结果状态")
	}
	consumed := kernel.SemMap(result["consumed_contract"])
	contractRef := kernel.First(opts["contract"], kernel.Text(consumed["contract_ref"]))
	if contractRef == "" {
		return s.Reject("EXECUTION_RESULT_BINDING", "缺少当前批准合同引用")
	}
	c, e := contracts.LoadNativeSlice(s, contractRef)
	if e != nil {
		return e
	}
	if kernel.ContractN(c.Raw["schema_version"]) == 4 {
		ref := opts["result-ref"]
		if ref == "" || opts["task"] == "" {
			return s.Reject("TASK_RESULT", "Slice4消费需要实际结果引用与完成task3，不接受历史弱绑定")
		}
		saved, err := s.Doc(ref)
		if err != nil {
			return err
		}
		if nested, ok := kernel.Object(saved["execution_result"]); ok {
			saved = nested
		}
		if !kernel.ApEqual(saved, result) {
			return s.Reject("TASK_RESULT", "结果参数与实际原字节不符")
		}
		task, err := s.Doc(opts["task"])
		if err != nil {
			return err
		}
		if kernel.ApMap(task["contract"])["slice_contract_ref"] != contractRef {
			return s.Reject("TASK_RESULT", "完成任务属于另一Slice")
		}
		return s.Verify("slice-execution-result", ref, map[string]string{"task": opts["task"]})
	}
	unitID := kernel.Text(result["work_unit_id"])
	boundOpts := kernel.SemanticOptions(opts)
	boundOpts["unit"] = unitID
	approval := kernel.First(opts["approval-ref"], kernel.Text(consumed["approval_ref"]))
	if approval == "" {
		return s.Reject("SLICE_APPROVAL_REQUIRED", "执行证据须提供当前批准合同")
	}
	boundOpts["approval-ref"] = approval
	if e = contracts.ContractSliceFresh(s, c, boundOpts); e != nil {
		return e
	}
	raw, n := c.Raw, c.Normalized
	if consumed["contract_id"] != raw["contract_id"] || consumed["contract_version"] != raw["contract_version"] || result["slice_id"] != nil && kernel.Text(result["slice_id"]) != kernel.Text(raw["slice_id"]) {
		return s.Reject("EXECUTION_RESULT_BINDING", "执行报告合同或切片身份不匹配")
	}
	resolution := kernel.SemMap(n["resolution"])
	unit := map[string]any{}
	for _, v := range kernel.SemList(n["work_units"]) {
		m := kernel.SemMap(v)
		if kernel.Text(m["id"]) == unitID {
			unit = m
			break
		}
	}
	if len(unit) == 0 {
		return s.Reject("WORK_UNIT", "执行报告工作单元未知")
	}
	if repository := c.Repositories[kernel.Text(unit["project_root"])]; repository != nil {
		resolution = kernel.SemMap(repository["resolution"])
	}
	if len(kernel.SemList(resolution["readiness_blockers"])) > 0 {
		return s.Reject("EXECUTION_RESULT_BLOCKED", "冻结执行闭包仍有阻断项")
	}
	for _, key := range []string{"registry_digest", "compiler_contract_digest"} {
		if !contracts.ContractSame(consumed[key], resolution[key]) {
			return s.Reject("EXECUTION_RESULT_STALE", "执行报告冻结闭包变化")
		}
	}
	if resolution["architecture_identity"] != nil && strings.TrimPrefix(contracts.ContractDigest(result["architecture_identity"]), "sha256:") != kernel.Text(resolution["architecture_identity_digest"]) {
		return s.Reject("EXECUTION_RESULT_STALE", "执行结果架构身份与冻结摘要不一致")
	}
	if resolution["component_bindings_digest"] != nil && !contracts.ContractSame(consumed["component_bindings_digest"], resolution["component_bindings_digest"]) {
		return s.Reject("EXECUTION_RESULT_STALE", "执行结果组件冻结摘要不一致")
	}
	if e = contracts.ContractRecordedResults(s, result["verification_results"], false); e != nil {
		return e
	}
	work := kernel.SemMap(unit["work_unit"])
	if len(work) == 0 {
		work = unit
	}
	work = contracts.ContractCopy(work)
	work["project_root"] = unit["project_root"]
	if unit["allowed_write_paths"] != nil {
		work["allowed_write_paths"] = unit["allowed_write_paths"]
	}
	allowed := kernel.SemStrings(work["allowed_write_paths"])
	if len(allowed) == 0 {
		return s.Reject("PATH", "工作单元无写范围")
	}
	if kernel.ContractN(raw["schema_version"]) == 3 {
		if _, ok := result["changed_files"].([]any); !ok {
			return s.Reject("EXECUTION_SOURCE_REQUIRED", "执行结果缺少changed_files数组")
		}
	}
	for _, v := range kernel.SemList(result["changed_files"]) {
		ref := kernel.Text(v)
		root := ""
		if m, ok := kernel.Object(v); ok {
			ref, root = kernel.Text(m["path"]), kernel.Text(m["project_root"])
		}
		if len(c.Repositories) > 0 && root != kernel.Text(work["project_root"]) {
			return s.Reject("EXECUTION_SOURCE_REPOSITORY", "改动不属于工作单元工程")
		}
		within := false
		for _, scope := range allowed {
			within = within || contracts.ContractWithin(ref, scope)
		}
		if !within {
			return s.Reject("PATH", "实际改动超出工作单元范围: "+ref)
		}
	}
	expected := kernel.SemStrings(work["expected_evidence"])
	if len(expected) == 0 {
		expected = kernel.SemStrings(kernel.SemMap(n["common"])["expected_evidence_files"])
	}
	evidence := kernel.SemList(result["evidence_files"])
	for _, ref := range expected {
		found := false
		for _, v := range evidence {
			m := kernel.SemMap(v)
			found = found || (kernel.Text(v) == ref || kernel.Text(m["path"]) == ref) && (len(c.Repositories) == 0 || kernel.Text(m["project_root"]) == kernel.Text(work["project_root"]))
		}
		if !found {
			return s.Reject("EXECUTION_EVIDENCE_REQUIRED", "工作单元所需证据未记录: "+ref)
		}
	}
	if kernel.ContractN(raw["schema_version"]) == 3 {
		if e = contracts.ContractExecutionEvidence(s, result, c, work); e != nil {
			return e
		}
	} else {
		for _, v := range evidence {
			ref := kernel.Text(v)
			if m, ok := kernel.Object(v); ok {
				ref = kernel.Text(m["path"])
			}
			if _, e = s.Bytes(ref); e != nil {
				return e
			}
		}
	}
	if status == "drift" || status == "violation" || len(kernel.SemList(result["new_impacts"])) > 0 {
		return s.Reject("EXECUTION_RESULT_BLOCKED", "结果存在漂移、违反或新影响")
	}
	if status == "not-applicable" && kernel.Text(result["not_applicable_reason"]) == "" {
		return s.Reject("EXECUTION_RESULT_INVALID", "不适用缺少理由")
	}
	if status == "seam-deferred" {
		if len(kernel.SemList(result["seam_deferred"])) == 0 {
			return s.Reject("EXECUTION_RESULT_INVALID", "seam延期记录缺失")
		}
		for _, v := range kernel.SemList(result["seam_deferred"]) {
			if e = contracts.ContractRequired(s, kernel.SemMap(v), "risk", "owner", "follow_up_ticket", "verification_plan", "target_version_or_release_date"); e != nil {
				return e
			}
		}
	}
	return nil
}

// References remain anchored to the consuming project; command text is data.
func contractEvidenceRef(base, ref string) string {
	if strings.HasPrefix(ref, "./") {
		return path.Join(base, ref)
	}
	return ref
}
