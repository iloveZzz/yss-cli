package governance

import (
	"bytes"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

func init() { registerSemanticValidator("verification", verifyVerificationSemantic) }

// This validates persisted execution evidence. It never runs a command from it.
func verifyVerificationSemantic(s *semanticSession, ref string, opts map[string]string) error {
	doc, e := s.doc(ref)
	if e != nil {
		return e
	}
	if result, ok := object(doc["execution_result"]); ok {
		doc = result
	}
	kind := text(doc["kind"])
	if doc["consumed_contract"] != nil || doc["work_unit_id"] != nil {
		return contractExecutionResult(s, doc, opts)
	}
	switch kind {
	case "verification":
		return contractFrontendImplementation(s, ref, opts)
	case "template-verification-report":
		return contractTemplateVerification(s, doc, opts)
	case "frontend-scaffold-verification":
		return contractFrontendScaffoldVerification(s, doc, opts)
	case "backend-contract", "backend-deployment":
		if e = s.validateSchema(".template-spec/process/schemas/backend-delivery-verification.schema.json", doc); e != nil {
			return e
		}
		basis := opts["subject-digest"]
		if basis == "" {
			basis, e = contractPublicVerificationBasis(s, ref, kind, opts)
			if e != nil {
				return e
			}
		}
		if text(doc["subject_digest"]) != basis {
			return s.reject("VERIFICATION_CURRENT_REQUIRED", "交付验证缺少独立当前 basis")
		}
		if e = contractRecordedResults(s, doc["results"], true); e != nil {
			return e
		}
		for _, v := range semList(doc["results"]) {
			r := semMap(v)
			executed, _ := time.Parse(time.RFC3339Nano, text(r["executed_at"]))
			if executed.After(time.Now().Add(time.Minute)) {
				return s.reject("VERIFICATION_FAILED", "验证时间超出当前时点")
			}
		}
		return nil
	case "strategic-delivery-verification-v1":
		if e = s.validateSchema(".template-spec/process/schemas/strategic-handoff-delivery-verification.schema.json", doc); e != nil {
			return e
		}
		basis := opts["bundle-digest"]
		if basis == "" {
			basis, e = contractPublicVerificationBasis(s, ref, kind, opts)
			if e != nil {
				return e
			}
		}
		if text(doc["result"]) != "verified" || contractN(doc["exit_code"]) != 0 || text(doc["bundle_digest"]) != basis {
			return s.reject("VERIFICATION_CURRENT_REQUIRED", "交付验证缺少当前包绑定")
		}
		executed, err := time.Parse(time.RFC3339Nano, text(doc["executed_at"]))
		if err != nil || executed.After(time.Now().Add(time.Minute)) {
			return s.reject("VERIFICATION_FAILED", "战略交付验证时间无效或超过当前时点")
		}
		return nil
	default:
		return s.unavailable("CAPABILITY", "未知验证报告语义: "+kind)
	}
}

// Public reports obtain their subject from an independently selected consumer,
// never from the candidate report's digest, package path, or kind. Published
// task results carry output paths rather than an invented artifacts property.
func contractPublicVerificationBasis(s *semanticSession, reportRef, kind string, opts map[string]string) (string, error) {
	cpRef := opts["checkpoint"]
	if cpRef == "" {
		cpRef = s.checkpointRef
	}
	taskRef := opts["task"]
	if taskRef == "" {
		taskRef = opts["task-ref"]
	}
	refs := []string{}
	if cpRef != "" {
		if e := s.verify("checkpoint", cpRef, map[string]string{}); e != nil {
			return "", e
		}
		cp, e := s.doc(cpRef)
		if e != nil {
			return "", e
		}
		if kind == "strategic-delivery-verification-v1" {
			basis, selected, err := contractExplicitDesignVerificationBasis(s, cpRef, cp, reportRef, taskRef)
			if selected || err != nil {
				return basis, err
			}
		}
		trace := semMap(cp["stage_trace"])
		if kind != "strategic-delivery-verification-v1" && cp["mode"] == "audit" && trace["completed_work_unit"] == "work-unit.backend-delivery" {
			unit := apFind(s.registry["work_units"], "id", "work-unit.backend-delivery")
			if unit == nil || unit["scope"] != "project-instance" || cp["status"] != "routing" || cp["next_work_unit"] != nil || cp["stage"] != "stage.verification-release-retrospective" || trace["stage"] != cp["stage"] {
				return "", s.reject("VERIFICATION_CONSUMER_ROUTE", "当前 Checkpoint 未处于已登记后端终点的只读审计状态")
			}
			source, e := contractPublicBackendVerificationSource(s, cpRef)
			if e != nil {
				return "", e
			}
			authorization, e := progressionBackendAuthorization(s, cpRef, "", true)
			if e != nil {
				return "", e
			}
			for _, required := range []string{source, authorization.TerminalRef, reportRef} {
				found := false
				for _, observed := range semStrings(trace["artifact_refs"]) {
					found = found || contractVerificationSameRef(s, required, observed)
				}
				if !found {
					return "", s.reject("VERIFICATION_CONSUMER_ROUTE", "当前后端审计未独立登记交付、终点和待验报告")
				}
			}
			refs = append(refs, source)
		}
		if binding, present := object(semMap(cp["artifacts"])["artifact.fresh-verification"]); present && kind != "strategic-delivery-verification-v1" {
			if binding["status"] != "approved" || !contractSHA(binding["digest"]) || !contractVerificationSameRef(s, reportRef, text(binding["ref"])) {
				return "", s.reject("VERIFICATION_CURRENT_REQUIRED", "当前 Fresh Verification 资产未登记该报告及其字节摘要")
			}
			if _, e = contractBoundDoc(s, binding); e != nil {
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
		if binding, present := object(semMap(cp["artifacts"])["artifact.strategic-design-handoff"]); present && kind == "strategic-delivery-verification-v1" {
			if binding["status"] != "approved" || !contractSHA(binding["digest"]) {
				return "", s.reject("VERIFICATION_CURRENT_REQUIRED", "当前 Checkpoint 交付资产未批准或缺少字节绑定")
			}
			handoff, err := contractBoundDoc(s, binding)
			if err != nil {
				return "", err
			}
			if e = s.validateSchema(".template-spec/process/schemas/strategic-design-handoff-v5.schema.json", handoff); e != nil {
				return "", e
			}
			deliveryRef := path.Join("docs/deliveries/strategic", text(handoff["handoff_id"]), text(handoff["handoff_version"]), "delivery-record.json")
			_, record, e := contractOpenStrategicDelivery(s, deliveryRef)
			if e != nil {
				return "", e
			}
			source := semMap(record["handoff"])
			if source["ref"] != binding["ref"] || source["sha256"] != binding["digest"] || source["id"] != handoff["handoff_id"] || source["version"] != handoff["handoff_version"] {
				return "", s.reject("VERIFICATION_CURRENT_REQUIRED", "战略交付未绑定 Checkpoint 批准的当前 Handoff")
			}
			refs = append(refs, deliveryRef)
		}
	}
	if taskRef != "" {
		task, e := s.doc(taskRef)
		if e != nil {
			return "", e
		}
		validator := "task"
		if kind != "strategic-delivery-verification-v1" && task["work_unit_id"] == "work-unit.backend-delivery" {
			validator = "backend-completed-producer-task"
		}
		if e := s.verify(validator, taskRef, map[string]string{"checkpoint": cpRef}); e != nil {
			return "", e
		}
		result := semMap(task["result"])
		if task["workflow_status"] != "resolved" || result["result"] != "completed" || result["work_unit"] != task["work_unit_id"] || cpRef != "" && task["checkpoint_ref"] != cpRef {
			return "", s.reject("VERIFICATION_CURRENT_REQUIRED", "报告消费者须为当前已完整核验的正式完成任务")
		}
		producer := "work-unit.backend-delivery"
		if kind == "strategic-delivery-verification-v1" {
			producer = "work-unit.strategic-design-handoff"
		}
		unit := apFind(s.registry["work_units"], "id", producer)
		if task["work_unit_id"] != producer || unit == nil || unit["scope"] != "project-instance" {
			return "", s.reject("VERIFICATION_CONSUMER_ROUTE", "正式任务未绑定已登记的专项交付生产工作单元")
		}
		backendSource := ""
		if kind != "strategic-delivery-verification-v1" {
			backendSource, e = contractPublicBackendVerificationSource(s, cpRef)
			if e != nil {
				return "", e
			}
		}
		for _, ref := range semStrings(task["expected_evidence_files"]) {
			if !semHas(result["evidence_refs"], ref) || !semHas([]string{".json", ".yaml", ".yml"}, path.Ext(ref)) {
				continue
			}
			output, e := s.doc(ref)
			if e != nil {
				return "", e
			}
			if kind == "strategic-delivery-verification-v1" && output["kind"] == "strategic-handoff-delivery-v1" || kind != "strategic-delivery-verification-v1" && output["delivery_id"] != nil && output["verification"] != nil && output["environment"] != nil {
				if backendSource != "" && !contractVerificationSameRef(s, ref, backendSource) {
					return "", s.reject("VERIFICATION_CONSUMER_ROUTE", "任务产物未绑定当前持久化后端交付终点")
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
		return "", s.reject("VERIFICATION_CURRENT_REQUIRED", "交付报告缺少唯一独立消费者登记")
	}
	if e := s.currentAsset(unique[0]); e != nil {
		return "", e
	}
	if kind == "strategic-delivery-verification-v1" {
		bundle, record, e := contractOpenStrategicDelivery(s, unique[0])
		if e != nil {
			return "", e
		}
		expectedRef := path.Join(path.Dir(unique[0]), text(record["verification_ref"]))
		if !contractVerificationSameRef(s, reportRef, expectedRef) {
			return "", s.reject("VERIFICATION_CURRENT_REQUIRED", "待验报告不是消费者登记的战略交付验证")
		}
		return text(bundle.Manifest["bundle_digest"]), nil
	}
	delivery, e := backendInspectDelivery(s, unique[0], nil)
	if e != nil {
		return "", e
	}
	binding := semMap(semMap(delivery["verification"])[strings.TrimPrefix(kind, "backend-")])
	if !contractVerificationSameRef(s, reportRef, text(binding["ref"])) {
		return "", s.reject("VERIFICATION_CURRENT_REQUIRED", "待验报告不是消费者登记的后端交付验证")
	}
	if _, e = contractBoundDoc(s, binding); e != nil {
		return "", e
	}
	body := map[string]any{}
	for _, key := range []string{"delivery_id", "version", "strategic_bundle_digest", "strategic_route_id", "scope", "openapi", "slice_contract", "build", "environment"} {
		if value, present := delivery[key]; present {
			body[key] = value
		}
	}
	return contractDigest(body), nil
}

// Intent only selects a consumer. Its current original Receipt, signing
// authority and full delivery determine the verification subject independently
// of the candidate report. This reader is shared by public verification and
// milestone evaluation and never invokes the business evaluator.
func contractExplicitDesignVerificationBasis(s *semanticSession, cpRef string, cp map[string]any, reportRef, taskRef string) (string, bool, error) {
	if s.report.Profile != "spec" || !strings.HasPrefix(text(cp["feature_id"]), "feature.") {
		return "", false, nil
	}
	contractRef := guidanceContractRef("spec")
	present, err := s.exists(contractRef)
	if err != nil || !present {
		return "", false, err // Legacy reports may not install this policy asset.
	}
	contract, err := s.doc(contractRef)
	if err != nil {
		return "", false, err
	}
	if _, declared := contract["progression_target"]; !declared {
		return "", false, nil // Legacy reports retain their existing ownership.
	}
	if _, _, err = progressionPolicy(s); err != nil {
		return "", false, err
	}
	configRef, _, err := progressionLocation(s, cpRef)
	if err != nil {
		return "", false, err
	}
	present, err = s.exists(configRef)
	if err != nil || !present {
		return "", false, err
	}
	raw, err := s.bytes(configRef)
	if err != nil {
		return "", false, err
	}
	intent, err := parseProgressionTarget(raw)
	if err != nil {
		return "", false, err
	}
	if intent.FeatureID != text(cp["feature_id"]) || intent.CheckpointRef != cpRef {
		return "", false, s.reject("PROGRESSION_BINDING", "验证报告的显式消费者意图未绑定当前功能 checkpoint")
	}
	if err = progressionScope(s, intent.Target); err != nil {
		return "", false, err
	}
	for _, consumer := range intent.Consumers {
		if consumer.Profile != "design" {
			continue
		}
		if taskRef != "" || guidanceArtifact(cp, "artifact.strategic-design-handoff", "strategic_handoff_ref", "handoff_ref") != "" {
			return "", true, s.reject("VERIFICATION_CONSUMER_ROUTE", "战略验证同时登记本端与外部 Design 生产来源")
		}
		// A backend or frontend is irrelevant to this report's source selection.
		// Their terminal qualification remains mandatory for whole business.
		intent.Consumers = []ProgressionConsumer{consumer}
		rows, sessions := progressionConsumers(s, "spec", cpRef, cp, intent)
		row, design := semMap(rows["design"]), sessions["design"]
		if design == nil || row["input_status"] != "verified" || row["completion_status"] != "reached" {
			return "", true, s.reject("VERIFICATION_CURRENT_REQUIRED", "外部 Design 的当前来源接收或本端交付未核验："+first(text(row["reason"]), text(row["completion_reason"])))
		}
		current, err := design.doc(consumer.CheckpointRef)
		if err != nil {
			return "", true, err
		}
		ref, present, err := guidanceStrategicDeliveryRef(design, current)
		if err != nil {
			return "", true, err
		}
		if !present {
			return "", true, s.reject("VERIFICATION_CURRENT_REQUIRED", "外部 Design 缺少当前批准 Handoff 的完整交付登记")
		}
		bundle, record, err := contractOpenStrategicDelivery(design, ref)
		if err != nil {
			return "", true, err
		}
		asset := semMap(semMap(current["artifacts"])["artifact.strategic-design-handoff"])
		if asset["status"] != "approved" || !contractSHA(asset["digest"]) {
			return "", true, s.reject("VERIFICATION_CURRENT_REQUIRED", "外部 Design 的当前 Handoff 资产缺少批准或原始字节摘要")
		}
		handoff, err := contractBoundDoc(design, asset)
		if err != nil {
			return "", true, err
		}
		published := semMap(record["handoff"])
		if published["ref"] != asset["ref"] || published["sha256"] != asset["digest"] || !contractSame(handoff, bundle.Handoff) {
			return "", true, s.reject("VERIFICATION_CURRENT_REQUIRED", "外部 Design 当前 Handoff 与完整交付包的原始批准资产不一致")
		}
		expectedRef := path.Join(path.Dir(ref), text(record["verification_ref"]))
		if err = contractVerificationOriginalCopy(s, reportRef, design, expectedRef); err != nil {
			return "", true, err
		}
		return text(bundle.Manifest["bundle_digest"]), true, nil
	}
	return "", false, nil
}

func contractVerificationOriginalCopy(s *semanticSession, reportRef string, source *semanticSession, sourceRef string) error {
	actual, err := s.bytes(reportRef)
	if err != nil {
		return err
	}
	original, err := source.bytes(sourceRef)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, original) {
		return s.reject("VERIFICATION_CURRENT_REQUIRED", "待验冻结报告原始字节与当前外部 Design 交付验证不同")
	}
	return nil
}

// The fixed terminal producer owns the delivery reference. A report or a task
// cannot choose another valid delivery as its current verification subject.
func contractPublicBackendVerificationSource(s *semanticSession, cpRef string) (string, error) {
	authorization, e := progressionBackendAuthorization(s, cpRef, "", true)
	if e != nil {
		return "", e
	}
	ref := authorization.TerminalRef
	exists, e := s.exists(ref)
	if e != nil {
		return "", e
	}
	if !exists {
		return "", s.reject("VERIFICATION_CURRENT_REQUIRED", "缺少当前已发布后端交付终点")
	}
	if e = s.verify("backend-terminal", ref, map[string]string{"checkpoint": cpRef}); e != nil {
		return "", e
	}
	record, e := s.doc(ref)
	if e != nil {
		return "", e
	}
	binding := semMap(record["delivery"])
	if _, e = backendBound(s, binding); e != nil {
		return "", e
	}
	return text(binding["ref"]), nil
}

func contractVerificationSameRef(s *semanticSession, a, b string) bool {
	x, e := contractVerificationRef(s, a)
	if e != nil {
		return false
	}
	y, e := contractVerificationRef(s, b)
	return e == nil && x == y
}
func contractVerificationRef(s *semanticSession, ref string) (string, error) {
	if filepath.IsAbs(ref) {
		local, e := filepath.Rel(s.root, ref)
		if e != nil || local == ".." || strings.HasPrefix(local, ".."+string(filepath.Separator)) {
			if filepath.Clean(ref) != ref {
				return "", s.reject("VERIFICATION_PATH", "验证路径必须规范")
			}
			for root := range s.externalViews {
				rel, err := filepath.Rel(root, ref)
				if err == nil && contractPath(filepath.ToSlash(rel)) {
					return ref, nil
				}
			}
			return "", s.reject("VERIFICATION_PATH", "验证路径未登记在当前项目或批准工程")
		}
		ref = filepath.ToSlash(local)
	}
	if !contractPath(ref) {
		return "", s.reject("VERIFICATION_PATH", "验证引用路径非法")
	}
	return ref, nil
}
func contractVerificationBytes(s *semanticSession, ref string) ([]byte, error) {
	ref, e := contractVerificationRef(s, ref)
	if e != nil {
		return nil, e
	}
	if !filepath.IsAbs(ref) {
		return s.bytes(ref)
	}
	selected := ""
	for root := range s.externalViews {
		rel, err := filepath.Rel(root, ref)
		if err == nil && contractPath(filepath.ToSlash(rel)) && len(root) > len(selected) {
			selected = root
		}
	}
	if selected == "" {
		return nil, s.reject("VERIFICATION_PATH", "验证路径没有批准工程登记")
	}
	rel, e := filepath.Rel(selected, ref)
	if e != nil {
		return nil, e
	}
	return s.externalBytes(selected, filepath.ToSlash(rel))
}
func contractVerificationDoc(s *semanticSession, ref string) (map[string]any, error) {
	b, e := contractVerificationBytes(s, ref)
	if e != nil {
		return nil, e
	}
	v, e := schema.Parse(b)
	if e != nil {
		return nil, s.reject("VERIFICATION_FORMAT", e.Error())
	}
	m, ok := object(v)
	if !ok {
		return nil, s.reject("VERIFICATION_FORMAT", "验证资产必须为对象")
	}
	return m, nil
}
func contractFrontendScaffoldVerification(s *semanticSession, doc map[string]any, opts map[string]string) error {
	if contractN(doc["schema_version"]) != 1 || text(doc["status"]) != "passed" {
		return s.reject("VERIFICATION_FAILED", "前端脚手架验证未通过")
	}
	projectRoot := text(doc["project_root"])
	if !filepath.IsAbs(projectRoot) || filepath.Clean(projectRoot) != projectRoot {
		return s.reject("VERIFICATION_REQUIRED", "报告缺少规范项目根")
	}
	if expected := opts["project-root"]; expected != "" && expected != projectRoot {
		return s.reject("VERIFICATION_CURRENT_REQUIRED", "报告项目根与消费者不一致")
	}
	if owner := opts["contract"]; owner != "" {
		if e := s.verify("scaffold", owner, opts); e != nil {
			return e
		}
		contract, e := s.doc(owner)
		if e != nil {
			return e
		}
		if text(contract["delivery_role"]) != "frontend" || text(contract["target_output_dir"]) != projectRoot {
			return s.reject("VERIFICATION_CURRENT_REQUIRED", "显式批准合同目标与报告工程不一致")
		}
		if e = s.registerExternalRoot(projectRoot, owner); e != nil {
			return e
		}
	}
	manifestRef, e := contractVerificationRef(s, text(doc["scaffold_manifest_ref"]))
	if e != nil {
		return e
	}
	manifest, e := contractVerificationDoc(s, manifestRef)
	if e != nil {
		return e
	}
	if contractN(manifest["schema_version"]) != 4 || text(manifest["kind"]) != "frontend-scaffold" || text(manifest["generation_mode"]) != "controlled-generation" || !contractUnique(manifest["generated_files"], true) || !semHas(manifest["generated_files"], "pnpm-lock.yaml") {
		return s.reject("VERIFICATION_MANIFEST", "前端 Manifest 类型、生成方式或锁文件清单无效")
	}
	manifestFile := manifestRef
	if !filepath.IsAbs(manifestFile) {
		manifestFile = filepath.Join(s.root, filepath.FromSlash(manifestFile))
	}
	if text(manifest["target_output_dir"]) != projectRoot || manifestFile != filepath.Join(projectRoot, ".yss", "scaffold-generation.json") {
		return s.reject("VERIFICATION_MANIFEST", "Manifest 与报告项目根不一致")
	}
	contractRef, e := contractVerificationRef(s, text(manifest["contract_file_ref"]))
	if e != nil {
		return e
	}
	raw, e := s.bytes(contractRef)
	if e != nil {
		return e
	}
	if text(manifest["contract_digest"]) != "sha256:"+safefs.Digest(raw) {
		return s.reject("VERIFICATION_MANIFEST", "原始生成合同摘要变化")
	}
	contract, e := s.doc(contractRef)
	if e != nil {
		return e
	}
	if contract["contract_id"] != manifest["contract_id"] || contract["contract_version"] != manifest["contract_version"] || text(contract["delivery_role"]) != "frontend" || text(contract["status"]) != "approved" || contract["current_version"] != true || text(contract["persisted_ref"]) == "" || !contractSame(contract["verification_commands"], manifest["verification_commands"]) {
		return s.reject("VERIFICATION_MANIFEST", "当前合同身份、状态或固定验证命令变化")
	}
	if persisted, exists := manifest["persisted_ref"]; exists && persisted != contract["persisted_ref"] {
		return s.reject("VERIFICATION_MANIFEST", "Manifest持久化合同身份变化")
	}
	if e = s.verify("scaffold", contractRef, opts); e != nil {
		return e
	}
	if !contractSame(contractWithout(semMap(manifest["template"]), "checkout"), semMap(semMap(contract["frontend"])["template"])) {
		return s.reject("VERIFICATION_MANIFEST", "Manifest模板与当前批准合同不一致")
	}
	commands := semStrings(manifest["verification_commands"])
	allowed := []string{"pnpm install --frozen-lockfile", "pnpm lint", "pnpm lint:check", "pnpm type-check", "pnpm build", "pnpm build:standalone"}
	required := []string{"pnpm install --frozen-lockfile", "pnpm lint", "pnpm type-check", "pnpm build"}
	if text(semMap(manifest["template"])["kind"]) == "bundled" {
		required[1] = "pnpm lint:check"
		required = append(required, "pnpm build:standalone")
	}
	if !contractUnique(manifest["verification_commands"], true) {
		return s.reject("VERIFICATION_REQUIRED", "固定命令缺失或重复")
	}
	for _, command := range commands {
		if !semHas(allowed, command) {
			return s.reject("VERIFICATION_FAILED", "不支持的验证命令")
		}
	}
	for _, command := range required {
		if !semHas(commands, command) {
			return s.reject("VERIFICATION_REQUIRED", "缺少固定验证命令: "+command)
		}
	}
	rows := semList(doc["commands"])
	if len(rows) != len(commands) {
		return s.reject("VERIFICATION_FAILED", "实际结果未覆盖固定命令集合")
	}
	for i, v := range rows {
		row := semMap(v)
		at, e := time.Parse(time.RFC3339Nano, text(row["executed_at"]))
		exit, ok := integer(row["exit_code"])
		if row["command"] != commands[i] || !ok || exit != 0 || e != nil || at.After(time.Now().Add(time.Minute)) || row["termination"] != nil {
			return s.reject("VERIFICATION_FAILED", "实际命令、退出码、时间或取消状态不允许通过")
		}
		for _, key := range []string{"stdout_ref", "stderr_ref"} {
			ref, e := contractVerificationRef(s, text(row[key]))
			if e != nil {
				return e
			}
			if _, e = s.bytes(ref); e != nil {
				return e
			}
		}
	}
	for _, ref := range semStrings(manifest["generated_files"]) {
		if !contractPath(ref) {
			return s.reject("VERIFICATION_MANIFEST", "生成文件路径非法")
		}
		local, e := contractVerificationRef(s, filepath.Join(projectRoot, filepath.FromSlash(ref)))
		if e != nil {
			return e
		}
		if _, e = contractVerificationBytes(s, local); e != nil {
			return e
		}
	}
	return nil
}
func contractTemplateVerification(s *semanticSession, doc map[string]any, opts map[string]string) error {
	if contractN(doc["schema_version"]) != 1 || text(doc["status"]) != "passed" || len(semList(doc["unexecuted"])) > 0 || doc["input_drift"] == true || doc["source_state"] == "working-tree" {
		return s.reject("VERIFICATION_FAILED", "模板验证未完整通过或输入漂移")
	}
	if !contractDate(doc["started_at"]) || !contractDate(doc["finished_at"]) {
		return s.reject("VERIFICATION_REQUIRED", "模板报告缺少实际起止时间")
	}
	if opts["input-digest"] == "" || text(doc["input_sha256"]) != opts["input-digest"] {
		return s.reject("VERIFICATION_CURRENT_REQUIRED", "模板验证需要消费者独立当前输入摘要")
	}
	plan := semList(doc["plan"])
	results := semList(doc["results"])
	if len(plan) == 0 || len(results) != len(plan) {
		return s.reject("VERIFICATION_FAILED", "验证计划有未执行项")
	}
	seen := map[int]bool{}
	for _, v := range results {
		r := semMap(v)
		n, ok := integer(r["index"])
		index := int(n)
		if !ok || index < 0 || index >= len(plan) || seen[index] {
			return s.reject("VERIFICATION_FAILED", "验证结果索引缺失、重复或越界")
		}
		seen[index] = true
		entry := semMap(plan[index])
		exit, ok := integer(r["exit_code"])
		if !ok || exit != 0 || text(r["command"]) == "" || !contractSame(r["command"], entry["command"]) {
			return s.reject("VERIFICATION_FAILED", "验证命令或真实退出码不一致")
		}
		for _, key := range []string{"stdout_ref", "stderr_ref"} {
			if text(r[key]) != "" {
				if _, e := s.bytes(text(r[key])); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
func contractExecutionResult(s *semanticSession, result map[string]any, opts map[string]string) error {
	if contractN(result["schema_version"]) != 2 {
		return s.reject("EXECUTION_RESULT_SCHEMA", "执行报告须为 schema v2")
	}
	status := text(result["status"])
	if !semHas([]string{"implemented", "seam-deferred", "drift", "violation", "not-applicable"}, status) {
		return s.reject("EXECUTION_RESULT_STATUS", "未知执行结果状态")
	}
	consumed := semMap(result["consumed_contract"])
	contractRef := first(opts["contract"], text(consumed["contract_ref"]))
	if contractRef == "" {
		return s.reject("EXECUTION_RESULT_BINDING", "缺少当前批准合同引用")
	}
	c, e := loadNativeSlice(s, contractRef)
	if e != nil {
		return e
	}
	unitID := text(result["work_unit_id"])
	boundOpts := semanticOptions(opts)
	boundOpts["unit"] = unitID
	approval := first(opts["approval-ref"], text(consumed["approval_ref"]))
	if approval == "" {
		return s.reject("SLICE_APPROVAL_REQUIRED", "执行证据须提供当前批准合同")
	}
	boundOpts["approval-ref"] = approval
	if e = contractSliceFresh(s, c, boundOpts); e != nil {
		return e
	}
	raw, n := c.Raw, c.Normalized
	if consumed["contract_id"] != raw["contract_id"] || consumed["contract_version"] != raw["contract_version"] || result["slice_id"] != nil && text(result["slice_id"]) != text(raw["slice_id"]) {
		return s.reject("EXECUTION_RESULT_BINDING", "执行报告合同或切片身份不匹配")
	}
	resolution := semMap(n["resolution"])
	unit := map[string]any{}
	for _, v := range semList(n["work_units"]) {
		m := semMap(v)
		if text(m["id"]) == unitID {
			unit = m
			break
		}
	}
	if len(unit) == 0 {
		return s.reject("WORK_UNIT", "执行报告工作单元未知")
	}
	if repository := c.Repositories[text(unit["project_root"])]; repository != nil {
		resolution = semMap(repository["resolution"])
	}
	if len(semList(resolution["readiness_blockers"])) > 0 {
		return s.reject("EXECUTION_RESULT_BLOCKED", "冻结执行闭包仍有阻断项")
	}
	for _, key := range []string{"registry_digest", "compiler_contract_digest"} {
		if !contractSame(consumed[key], resolution[key]) {
			return s.reject("EXECUTION_RESULT_STALE", "执行报告冻结闭包变化")
		}
	}
	if resolution["architecture_identity"] != nil && strings.TrimPrefix(contractDigest(result["architecture_identity"]), "sha256:") != text(resolution["architecture_identity_digest"]) {
		return s.reject("EXECUTION_RESULT_STALE", "执行结果架构身份与冻结摘要不一致")
	}
	if resolution["component_bindings_digest"] != nil && !contractSame(consumed["component_bindings_digest"], resolution["component_bindings_digest"]) {
		return s.reject("EXECUTION_RESULT_STALE", "执行结果组件冻结摘要不一致")
	}
	if e = contractRecordedResults(s, result["verification_results"], false); e != nil {
		return e
	}
	work := semMap(unit["work_unit"])
	if len(work) == 0 {
		work = unit
	}
	work = contractCopy(work)
	work["project_root"] = unit["project_root"]
	if unit["allowed_write_paths"] != nil {
		work["allowed_write_paths"] = unit["allowed_write_paths"]
	}
	allowed := semStrings(work["allowed_write_paths"])
	if len(allowed) == 0 {
		return s.reject("PATH", "工作单元无写范围")
	}
	if contractN(raw["schema_version"]) == 3 {
		if _, ok := result["changed_files"].([]any); !ok {
			return s.reject("EXECUTION_SOURCE_REQUIRED", "执行结果缺少changed_files数组")
		}
	}
	for _, v := range semList(result["changed_files"]) {
		ref := text(v)
		root := ""
		if m, ok := object(v); ok {
			ref, root = text(m["path"]), text(m["project_root"])
		}
		if len(c.Repositories) > 0 && root != text(work["project_root"]) {
			return s.reject("EXECUTION_SOURCE_REPOSITORY", "改动不属于工作单元工程")
		}
		within := false
		for _, scope := range allowed {
			within = within || contractWithin(ref, scope)
		}
		if !within {
			return s.reject("PATH", "实际改动超出工作单元范围: "+ref)
		}
	}
	expected := semStrings(work["expected_evidence"])
	if len(expected) == 0 {
		expected = semStrings(semMap(n["common"])["expected_evidence_files"])
	}
	evidence := semList(result["evidence_files"])
	for _, ref := range expected {
		found := false
		for _, v := range evidence {
			m := semMap(v)
			found = found || (text(v) == ref || text(m["path"]) == ref) && (len(c.Repositories) == 0 || text(m["project_root"]) == text(work["project_root"]))
		}
		if !found {
			return s.reject("EXECUTION_EVIDENCE_REQUIRED", "工作单元所需证据未记录: "+ref)
		}
	}
	if contractN(raw["schema_version"]) == 3 {
		if e = contractExecutionEvidence(s, result, c, work); e != nil {
			return e
		}
	} else {
		for _, v := range evidence {
			ref := text(v)
			if m, ok := object(v); ok {
				ref = text(m["path"])
			}
			if _, e = s.bytes(ref); e != nil {
				return e
			}
		}
	}
	if status == "drift" || status == "violation" || len(semList(result["new_impacts"])) > 0 {
		return s.reject("EXECUTION_RESULT_BLOCKED", "结果存在漂移、违反或新影响")
	}
	if status == "not-applicable" && text(result["not_applicable_reason"]) == "" {
		return s.reject("EXECUTION_RESULT_INVALID", "不适用缺少理由")
	}
	if status == "seam-deferred" {
		if len(semList(result["seam_deferred"])) == 0 {
			return s.reject("EXECUTION_RESULT_INVALID", "seam延期记录缺失")
		}
		for _, v := range semList(result["seam_deferred"]) {
			if e = contractRequired(s, semMap(v), "risk", "owner", "follow_up_ticket", "verification_plan", "target_version_or_release_date"); e != nil {
				return e
			}
		}
	}
	return nil
}
func contractExecutionEvidence(s *semanticSession, result map[string]any, c *nativeSlice, work map[string]any) error {
	if contractN(result["evidence_binding_version"]) != 1 {
		return s.reject("EXECUTION_EVIDENCE_LEGACY", "缺少原始证据绑定协议")
	}
	bound, e := s.bind(c.Ref)
	if e != nil {
		return e
	}
	if text(semMap(result["consumed_contract"])["contract_digest"]) != bound.Digest {
		return s.reject("EXECUTION_EVIDENCE_STALE", "执行证据合同原字节摘要不一致")
	}
	unitRoot := text(work["project_root"])
	cross := len(c.Repositories) > 0
	read := func(root, ref string) ([]byte, error) {
		if cross {
			return s.externalBytes(root, ref)
		}
		return s.bytes(ref)
	}
	sourceRead := func(root, ref string) ([]byte, error) {
		if root == s.root {
			return s.bytes(ref)
		}
		return s.externalBytes(root, ref)
	}
	files := semList(result["evidence_files"])
	checks := semList(work["verification"])
	allowedRoots := map[string]bool{unitRoot: true}
	for _, v := range checks {
		check := semMap(v)
		for _, root := range semStrings(check["dependency_roots"]) {
			allowedRoots[root] = true
		}
		rows := []map[string]any{}
		for _, v := range semList(result["verification_results"]) {
			r := semMap(v)
			if r["verification_id"] == check["id"] {
				rows = append(rows, r)
			}
		}
		if len(rows) != 1 {
			return s.reject("EXECUTION_EVIDENCE_INVALID", "验证 ID 缺失或重复")
		}
		row := rows[0]
		exit, ok := integer(row["exit_code"])
		if !ok || exit != 0 || row["command"] != check["command"] || row["cwd"] != check["cwd"] || !contractDate(row["executed_at"]) || !semSameSet(row["acceptance_refs"], check["acceptance_refs"]) {
			return s.reject("EXECUTION_EVIDENCE_INVALID", "验证命令/cwd/退出码/验收绑定冲突")
		}
		if check["dependency_roots"] != nil && !contractSame(row["dependency_roots"], check["dependency_roots"]) {
			return s.reject("EXECUTION_EVIDENCE_INVALID", "验证依赖工程集合与冻结命令不一致")
		}
		refs := semStrings(row["evidence_refs"])
		for _, ref := range semStrings(check["expected_evidence"]) {
			if !semHas(refs, ref) {
				return s.reject("EXECUTION_EVIDENCE_REQUIRED", "验证未关联所需证据")
			}
		}
		covered := map[string]bool{}
		for _, ref := range refs {
			matched := false
			for _, v := range files {
				file := semMap(v)
				if text(file["path"]) != ref || cross && text(file["project_root"]) != unitRoot {
					continue
				}
				matched = true
				behavior := text(file["behavior_ref"])
				if !semHas(check["acceptance_refs"], behavior) {
					return s.reject("EXECUTION_EVIDENCE_INVALID", "证据关联无关行为")
				}
				bytes, e := read(unitRoot, ref)
				if e != nil {
					return e
				}
				if "sha256:"+safefs.Digest(bytes) != text(file["digest"]) {
					return s.reject("EXECUTION_EVIDENCE_STALE", "证据原字节变化")
				}
				covered[behavior] = true
			}
			if !matched {
				return s.reject("EXECUTION_EVIDENCE_REQUIRED", "未绑定验证证据")
			}
		}
		for _, behavior := range semStrings(check["acceptance_refs"]) {
			if !covered[behavior] {
				return s.reject("EXECUTION_EVIDENCE_REQUIRED", "验收缺少行为证据")
			}
		}
	}
	sourceBindings := semList(result["source_bindings"])
	if len(sourceBindings) == 0 {
		return s.reject("EXECUTION_SOURCE_REQUIRED", "执行报告缺少固定输入")
	}
	for _, v := range sourceBindings {
		entry := semMap(v)
		root := first(text(entry["project_root"]), unitRoot)
		ref := text(entry["path"])
		if !allowedRoots[root] || cross && c.Repositories[root] == nil {
			return s.reject("EXECUTION_SOURCE_REPOSITORY", "执行输入仓库未批准")
		}
		if entry["deleted"] == true {
			if value, present := entry["digest"]; !present || value != nil {
				return s.reject("EXECUTION_SOURCE_STALE", "删除输入必须显式声明digest:null")
			}
			var exists bool
			if root != s.root {
				view := s.externalViews[root]
				if view == nil {
					return s.unavailable("CAPABILITY", "执行输入工程没有已验证登记")
				}
				d, e := view.watch(ref)
				if e != nil {
					return e
				}
				exists = d.Type != "missing"
			} else {
				var e error
				exists, e = s.exists(ref)
				if e != nil {
					return e
				}
			}
			if exists || entry["digest"] != nil {
				return s.reject("EXECUTION_SOURCE_STALE", "已删除源仍存在或声明摘要")
			}
		} else {
			bytes, e := sourceRead(root, ref)
			if e != nil {
				return e
			}
			if "sha256:"+safefs.Digest(bytes) != text(entry["digest"]) {
				return s.reject("EXECUTION_SOURCE_STALE", "执行输入已变化")
			}
		}
	}
	for _, v := range semList(result["changed_files"]) {
		ref, root := text(v), unitRoot
		if m, ok := object(v); ok {
			ref = text(m["path"])
			root = first(text(m["project_root"]), unitRoot)
		}
		found := false
		for _, v := range sourceBindings {
			b := semMap(v)
			found = found || text(b["path"]) == ref && first(text(b["project_root"]), unitRoot) == root
		}
		if !found {
			return s.reject("EXECUTION_SOURCE_REQUIRED", "改动未绑定当前源字节")
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
