package governance

// These adapters only consume persisted evidence. They never build an application,
// contact a service, sign an approval, or create a delivery terminal.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

func init() {
	registerSemanticValidator("backend-terminal", verifyBackendTerminalSemantic)
	registerSemanticValidator("backend-review", verifyBackendReviewSemantic)
	registerSemanticValidator("backend-delivery", func(s *semanticSession, ref string, opts map[string]string) error {
		_, e := backendInspectDelivery(s, ref, opts)
		return e
	})
}

func backendReject(s *semanticSession, message string) error {
	return s.reject("BACKEND_DELIVERY", message)
}
func backendHash(b []byte) string { return "sha256:" + safefs.Digest(b) }

// Formal Backend inputs retain the complete published delivery wrapper for v5.
// Historical v4 packages remain readable without synthesizing that wrapper.
func backendOpenStrategicInput(s *semanticSession, ref string) (*nativeHandoff, error) {
	if !strings.EqualFold(path.Ext(ref), ".zip") {
		recordRef := path.Join(ref, "delivery-record.json")
		present, err := s.exists(recordRef)
		if err != nil {
			return nil, err
		}
		if present {
			bundle, _, err := contractOpenStrategicDelivery(s, recordRef)
			return bundle, err
		}
	}
	bundle, err := contractOpenHandoff(s, ref)
	if err != nil {
		return nil, err
	}
	if contractN(bundle.Handoff["schema_version"]) == 5 {
		return nil, backendReject(s, "Handoff v5 后端战略输入必须保留正式 delivery wrapper")
	}
	return bundle, nil
}
func backendBound(s *semanticSession, v any) ([]byte, error) {
	b := apMap(v)
	if apText(b["ref"]) == "" || apText(b["digest"]) == "" {
		return nil, backendReject(s, "缺少当前原始字节绑定")
	}
	raw, e := s.bytes(apText(b["ref"]))
	if e != nil {
		return nil, e
	}
	if backendHash(raw) != apText(b["digest"]) {
		return nil, backendReject(s, "原始字节摘要变化: "+apText(b["ref"]))
	}
	return raw, nil
}
func backendProject(s *semanticSession) error {
	d, e := s.doc("yss-project.yaml")
	if e != nil {
		return e
	}
	if apNumber(d["schema_version"]) != 1 || d["repository_mode"] != "project-instance" {
		return backendReject(s, "后端交付仅适用于项目实例")
	}
	if _, e = s.contextContract(); e != nil {
		return e
	}
	return nil
}

func verifyBackendTerminalSemantic(s *semanticSession, ref string, opts map[string]string) error {
	if e := backendProject(s); e != nil {
		return e
	}
	a, e := progressionBackendAuthorization(s, opts["checkpoint"], ref, true)
	if e != nil {
		return e
	}
	if ref != a.TerminalRef {
		return backendReject(s, "终点必须消费固定持久引用")
	}
	record, e := s.doc(ref)
	if e != nil {
		return e
	}
	if apNumber(record["schema_version"]) != 1 || record["kind"] != "backend-delivery-terminal" || record["business_completed"] != false || record["release_authorized"] != false {
		return backendReject(s, "后端终点不是业务完成或发布批准")
	}
	if record["delivery_mode"] == "local-evidence" {
		return backendVerifyLocalTerminal(s, ref, record, a, opts)
	}
	for _, k := range []string{"delivery", "review_state"} {
		if _, e = backendBound(s, record[k]); e != nil {
			return e
		}
	}
	state, e := s.doc(apText(apMap(record["review_state"])["ref"]))
	if e != nil {
		return e
	}
	delivery, e := backendInspectDelivery(s, apText(apMap(record["delivery"])["ref"]), opts)
	if e != nil {
		return e
	}
	if e = backendTerminalSliceScope(s, delivery); e != nil {
		return e
	}
	if e = backendCurrentCheckpointSlice(s, a.CheckpointRef, record, delivery); e != nil {
		return e
	}
	if a.Mode == "backend-profile" {
		if e = backendProfileFresh(s, a.CheckpointRef, delivery); e != nil {
			return e
		}
	}
	input := apMap(state["review_input"])
	if input["scope_kind"] != "change" || input["slice_contract_ref"] != apMap(delivery["slice_contract"])["ref"] {
		return backendReject(s, "独立审查未绑定交付的 Slice")
	}
	result, e := backendReview(s, state, opts)
	if e != nil {
		return e
	}
	if result["status"] != "passed" {
		return backendReject(s, "后端交付需要当前独立审查")
	}
	root, e := backendProjectRoot(s, input)
	if e != nil {
		return e
	}
	buildSource, e := candidateBuildSource(s, root, input)
	if e != nil {
		return e
	}
	if input["review_mode"] != "committed" || apMap(delivery["build"])["source_commit"] != buildSource {
		return backendReject(s, "终点需要当前已提交审查候选和真实构建源码提交")
	}
	downstream := apMap(record["downstream"])
	for _, k := range []string{"owner", "ticket_ref", "verification_plan", "target_version"} {
		if strings.TrimSpace(apText(downstream[k])) == "" {
			return backendReject(s, "终点缺少下游接收责任和待办")
		}
	}
	manifest, source, e := backendOpenDelivery(s, apText(record["bundle_ref"]), opts)
	if e != nil {
		return e
	}
	if record["bundle_digest"] != manifest["bundle_digest"] || manifest["delivery_ref"] != apMap(record["delivery"])["ref"] {
		return backendReject(s, "终点绑定交付包不一致")
	}
	for _, file := range apRows(manifest["files"]) {
		if original := apText(file["original_ref"]); original != "" {
			b, e := s.bytes(original)
			if e != nil {
				return e
			}
			if backendHash(b) != file["sha256"] {
				return backendReject(s, "包与当前源不同: "+original)
			}
		}
	}
	_ = source
	coverage := map[string]any{"result": "backend-delivered", "delivery_id": delivery["delivery_id"], "version": delivery["version"], "bundle_digest": manifest["bundle_digest"], "next_work_unit": nil, "business_completed": false, "release_authorized": false, "live_service_checked": false, "downstream": downstream, "terminal_ref": ref}
	if a.CheckpointRef != "" {
		cp, err := s.doc(a.CheckpointRef)
		if err != nil {
			return err
		}
		coverage["checkpoint_ref"], coverage["next_work_unit"] = a.CheckpointRef, cp["next_work_unit"]
	}
	s.report.Coverage = coverage
	return nil
}

func backendProfileFresh(s *semanticSession, cpRef string, delivery map[string]any) error {
	cp, err := s.doc(cpRef)
	if err != nil {
		return err
	}
	slice, err := loadNativeSlice(s, text(semMap(delivery["slice_contract"])["ref"]))
	if err != nil {
		return err
	}
	if contractN(slice.Raw["schema_version"]) == 3 {
		if err = s.gateChecks(cp, cpRef, "gate.slice-contract-approved"); err != nil {
			return err
		}
	}
	if err = s.gateChecks(cp, cpRef, "gate.fresh-verification-passed"); err != nil {
		return err
	}
	refs := semStrings(semMap(semMap(semMap(cp["gates"])["gate.fresh-verification-passed"])["evidence"])["evidence.fresh-verification"])
	if len(refs) == 0 {
		return backendReject(s, "后端专职终点缺少当前实际Fresh Verification报告")
	}
	for _, ref := range refs {
		report, err := s.doc(ref)
		if err != nil {
			return err
		}
		if nested, ok := object(report["execution_result"]); ok {
			report = nested
		}
		switch report["kind"] {
		case "backend-contract", "backend-deployment":
			name := map[string]string{"backend-contract": "contract", "backend-deployment": "deployment"}[text(report["kind"])]
			if semMap(semMap(delivery["verification"])[name])["ref"] != ref {
				return backendReject(s, "Fresh Verification未绑定当前实际契约或部署报告")
			}
		default:
			if len(semMap(report["consumed_contract"])) == 0 || report["work_unit_id"] == nil || report["status"] != "implemented" {
				return backendReject(s, "未知或未实现的Fresh Verification报告")
			}
			binding := semMap(delivery["slice_contract"])
			if err = contractExecutionResult(s, report, map[string]string{"contract": text(binding["ref"]), "approval-ref": text(binding["approval_ref"])}); err != nil {
				return err
			}
			for _, row := range semList(report["verification_results"]) {
				executed, err := time.Parse(time.RFC3339Nano, text(semMap(row)["executed_at"]))
				if err != nil || executed.After(time.Now().Add(time.Minute)) {
					return backendReject(s, "Fresh Verification执行时间无效")
				}
			}
		}
	}
	return nil
}

func backendSliceApproved(s *semanticSession, binding map[string]any, opts map[string]string) (*nativeSlice, error) {
	if _, e := backendBound(s, binding); e != nil {
		return nil, e
	}
	c, e := loadNativeSlice(s, apText(binding["ref"]))
	if e != nil {
		return nil, e
	}
	if c.Normalized["status"] != "approved" || c.Normalized["contract_id"] != binding["id"] || c.Normalized["contract_version"] != binding["version"] {
		return nil, backendReject(s, "Slice 身份/版本/批准状态不匹配")
	}
	o := apCopyStrings(opts)
	o["approval-ref"] = apText(binding["approval_ref"])
	// The shared source-approval kernel validates signing policy, genuine scope
	// decisions and v3 independent professional review, without current Git/build.
	if e = contractSliceApprovalBinding(s, c, o, binding); e != nil {
		return nil, e
	}
	return c, nil
}
func apCopyStrings(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Package validation consumes captured source bindings. A receiving checkpoint,
// task or Profile is never a selector for the source's approval expectation.
func backendSourceOptions(opts map[string]string) map[string]string {
	out := map[string]string{}
	for _, key := range []string{"tool-root", "home", "run-dir"} {
		if value, present := opts[key]; present {
			out[key] = value
		}
	}
	return out
}
func backendUnit(c *nativeSlice, requested string) string {
	if requested != "" {
		return requested
	}
	u := apRows(c.Normalized["work_units"])
	if len(u) == 1 {
		return apText(u[0]["id"])
	}
	return ""
}
func backendSelected(s *semanticSession, c *nativeSlice, unit string) (map[string]any, error) {
	n := apCopy(c.Normalized)
	if len(c.Repositories) == 0 {
		selected, err := contractSelectLocalUnit(s, c, unit)
		if err != nil {
			return nil, err
		}
		return apCopy(selected.Normalized), nil
	}
	u := apFind(n["work_units"], "id", unit)
	if u == nil {
		return nil, backendReject(s, "必须显式选择唯一工作单元")
	}
	repo := c.Repositories[apText(u["project_root"])]
	if repo == nil {
		return nil, backendReject(s, "工作单元工程未登记")
	}
	resolution := apCopy(apMap(repo["resolution"]))
	resolution["freshness"] = apMap(n["resolution"])["freshness"]
	n["resolution"] = resolution
	common := apCopy(apMap(n["common"]))
	common["project_roots"] = []any{u["project_root"]}
	common["allowed_write_paths"] = u["allowed_write_paths"]
	n["common"] = common
	role := apText(apMap(repo["project"])["delivery_role"])
	if role != "backend" {
		n["backend"] = map[string]any{"status": "not-applicable"}
	}
	if role != "frontend" {
		n["frontend"] = map[string]any{"status": "not-applicable"}
	} else if delivery := resolution["frontend_delivery"]; delivery != nil {
		front := apCopy(apMap(n["frontend"]))
		front["delivery"] = delivery
		n["frontend"] = front
	}
	refs := apCopy(apMap(n["lifecycle_refs"]))
	for key, value := range apMap(repo["basis"]) {
		refs[key] = apMap(value)["ref"]
	}
	n["lifecycle_refs"] = refs
	return n, nil
}

// Source v1 artifact approval publishes its own signing policy. This mirrors
// sourceApproval's sourceGateIds registry; it never closes a local lifecycle
// gate or supplies a consumer expectation from the approval record.
func backendPublishedRegistry(roles map[string]any) map[string]any {
	policy := apMap(roles["gate_policy"])
	ids := map[string]bool{}
	for _, bucket := range []string{"biological_human", "product_digital_human_with_biological_veto"} {
		for _, id := range apStrings(policy[bucket]) {
			ids[id] = true
		}
	}
	for _, bucket := range []string{"dual_digital_human", "digital_human_review", "check_reviews"} {
		for _, row := range apRows(policy[bucket]) {
			ids[apText(row["gate"])] = true
		}
	}
	gates := []any{}
	for _, id := range backendSortedKeys(ids) {
		gates = append(gates, map[string]any{"id": id})
	}
	return map[string]any{"gates": gates, "id_policy": map[string]any{"deprecated_ids": []any{}}}
}

func backendSourcePolicy(s *semanticSession) error {
	version, valid := integer(s.roles["schema_version"])
	gatePolicy, gatesPresent := object(s.roles["gate_policy"])
	decisionPolicy, decisionsPresent := object(s.roles["user_decision_policy"])
	decisionVersion, decisionVersionValid := integer(decisionPolicy["schema_version"])
	_, decisionGatesValid := decisionPolicy["gates"].([]any)
	_, decisionUnitsValid := object(decisionPolicy["work_units"])
	if !valid || version != 1 || s.roles["status"] != "active" || !gatesPresent || gatePolicy["default_if_unlisted"] != "reject-unlisted" || !decisionsPresent || !decisionVersionValid || decisionVersion != 1 || !decisionGatesValid || !decisionUnitsValid {
		return s.unavailable("CAPABILITY", "源批准/用户决定规则缺失、未知版本或结构不完整；不能降为无需真实决定")
	}
	for _, bucket := range []string{"biological_human", "product_digital_human_with_biological_veto", "dual_digital_human", "digital_human_review", "check_reviews"} {
		if value, present := gatePolicy[bucket]; present {
			if _, valid := value.([]any); !valid {
				return s.unavailable("CAPABILITY", "源签署策略分类必须是数组: "+bucket)
			}
		}
	}
	return nil
}

func backendArtifactApproval(s *semanticSession, gate string, binding, record map[string]any, opts map[string]string) error {
	if apNumber(record["schema_version"]) != 1 {
		// The v2 source task must freeze both actual source registries. Merely
		// declaring them as inputs, or hashing a consumer-updated task, cannot
		// substitute for their explicit original byte bindings in task basis.
		source := *s
		var e error
		source.roles, e = source.doc(approvalRolesRef)
		if e != nil {
			return e
		}
		if e = backendSourcePolicy(&source); e != nil {
			return e
		}
		task, e := source.doc(apText(record["review_task_ref"]))
		if e != nil {
			return e
		}
		for _, ref := range []string{approvalRegistryRef, approvalSkillsRef} {
			authority, e := source.doc(ref)
			if e != nil {
				return e
			}
			wantVersion := int64(1)
			if ref == approvalSkillsRef {
				wantVersion = 3
			} else {
				source.registry = authority
			}
			version, supported := integer(authority["schema_version"])
			if !supported || version != wantVersion || authority["status"] != "active" {
				return s.unavailable("CAPABILITY", "源实际注册表版本未知或非 active: "+ref)
			}
			raw, e := source.bytes(ref)
			if e != nil {
				return e
			}
			found := false
			for _, row := range apRows(apMap(task["review_context"])["basis"]) {
				found = found || row["ref"] == ref && row["digest"] == safefs.Digest(raw)
			}
			if !found {
				return s.reject("REVIEW_BINDING_STALE", "源正式审查任务未冻结实际注册表原始字节: "+ref)
			}
		}
		return contractApproval(&source, gate, binding, apText(binding["approval_ref"]), opts)
	}
	if e := backendSourcePolicy(s); e != nil {
		return e
	}
	// Only registry publication differs. All filesystem views, input guards,
	// source policies and the independent binding remain the current session's.
	source := *s
	source.registry = backendPublishedRegistry(s.roles)
	return contractApproval(&source, gate, binding, apText(binding["approval_ref"]), opts)
}

func backendInspectDelivery(s *semanticSession, ref string, opts map[string]string) (map[string]any, error) {
	return backendInspectDeliveryMode(s, ref, opts, false, "")
}

func backendInspectDeliveryMode(s *semanticSession, ref string, opts map[string]string, local bool, cpRef string) (map[string]any, error) {
	if e := backendProject(s); e != nil {
		return nil, e
	}
	d, e := s.doc(ref)
	if e != nil {
		return nil, e
	}
	if e = s.validateSchema(".template-spec/process/schemas/backend-delivery.schema.json", d); e != nil {
		return nil, e
	}
	if (d["delivery_mode"] == "local-evidence") != local {
		return nil, backendReject(s, "本地交付证据不能替代对外后端交付包")
	}
	if local {
		for _, key := range []string{"strategic_bundle_ref", "strategic_bundle_digest", "strategic_route_id"} {
			if d[key] != nil {
				return nil, backendReject(s, "本地交付不得伪造战略包字段")
			}
		}
	}
	data, e := backendBound(s, apMap(d["environment"])["test_data"])
	if e != nil {
		return nil, e
	}
	if len(data) == 0 {
		return nil, backendReject(s, "测试数据准备说明为空")
	}
	c, e := backendSliceApproved(s, apMap(d["slice_contract"]), opts)
	if e != nil {
		return nil, e
	}
	if c.Normalized["slice_id"] != apMap(d["scope"])["slice_id"] {
		return nil, backendReject(s, "Slice 切片不匹配")
	}
	if local && semMap(c.Normalized["backend"])["status"] != "required" {
		return nil, backendReject(s, "本地后端证据缺少当前批准后端实现范围")
	}
	apiBinding := apMap(d["openapi"])
	operations := []string{}
	if local && apiBinding["mode"] == "not-applicable" {
		if semMap(semMap(c.Raw["applicability"])["api"])["status"] != "not-applicable" || text(apiBinding["reason"]) == "" || len(apStrings(apMap(d["scope"])["operation_ids"])) != 0 {
			return nil, backendReject(s, "无API交付必须与批准Slice适用性一致")
		}
		if _, e = backendBound(s, apiBinding); e != nil {
			return nil, e
		}
		na, err := s.doc(text(apiBinding["ref"]))
		if err != nil {
			return nil, err
		}
		if na["status"] != "not-applicable" || na["slice_id"] != c.Normalized["slice_id"] || c.Basis["no_api_impact_record"]["ref"] != apiBinding["ref"] || c.Basis["no_api_impact_record"]["digest"] != apiBinding["digest"] {
			return nil, backendReject(s, "无API记录未绑定当前Slice依据")
		}
	} else {
		if local {
			api := c.Basis["openapi_freeze"]
			if api["ref"] != apiBinding["ref"] || api["digest"] != apiBinding["digest"] {
				return nil, backendReject(s, "本地后端证据未消费当前Slice冻结OpenAPI")
			}
		}
		if _, e = backendBound(s, apiBinding); e != nil {
			return nil, e
		}
		ar, e := s.doc(apText(apiBinding["approval_ref"]))
		if e != nil {
			return nil, e
		}
		gate := apText(ar["gate_id"])
		if !apContains([]string{"gate.engineering-contract-approved", "gate.openapi-frozen", "gate.openapi-freeze-confirmed"}, gate) {
			return nil, backendReject(s, "OpenAPI 批准门禁不匹配")
		}
		if e = backendArtifactApproval(s, gate, apiBinding, ar, opts); e != nil {
			return nil, e
		}
		api, e := s.doc(apText(apiBinding["ref"]))
		if e != nil {
			return nil, e
		}
		if !strings.HasPrefix(apText(api["openapi"]), "3.1.") {
			return nil, backendReject(s, "必须使用 OpenAPI 3.1")
		}
		for _, item := range apMap(api["paths"]) {
			for method, op := range apMap(item) {
				if apContains([]string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}, method) {
					operations = append(operations, apText(apMap(op)["operationId"]))
				}
			}
		}
		for _, id := range apStrings(apMap(d["scope"])["operation_ids"]) {
			if !apContains(operations, id) {
				return nil, backendReject(s, "交付接口不在冻结 OpenAPI 中")
			}
		}
	}
	body := map[string]any{}
	for _, k := range []string{"delivery_id", "version", "delivery_mode", "strategic_bundle_digest", "strategic_route_id", "scope", "openapi", "slice_contract", "build", "environment"} {
		if v, ok := d[k]; ok {
			body[k] = v
		}
	}
	basis := apDigest(body)
	tests, e := backendVerification(s, apMap(d["verification"])["contract"], basis, "backend-contract")
	if e != nil {
		return nil, e
	}
	if _, e = backendVerification(s, apMap(d["verification"])["deployment"], basis, "backend-deployment"); e != nil {
		return nil, e
	}
	if local {
		if e = backendLocalSpecCoverage(s, cpRef, c, d, tests); e != nil {
			return nil, e
		}
		return d, nil
	}
	b, e := backendOpenStrategicInput(s, apText(d["strategic_bundle_ref"]))
	if e != nil {
		return nil, e
	}
	if b.Manifest["bundle_digest"] != d["strategic_bundle_digest"] {
		return nil, backendReject(s, "交付与战略版本不一致")
	}
	if apNumber(b.Handoff["schema_version"]) >= 4 {
		route := apFind(b.Handoff["consumer_routes"], "capability", "backend-technical-design")
		if route == nil || route["activation"] == "not-applicable" || route["route_id"] != d["strategic_route_id"] {
			return nil, backendReject(s, "交付未绑定当前后端技术 route")
		}
	}
	ids := apStrings(apMap(d["scope"])["source_ids"])
	known := []string{}
	for _, r := range apRows(b.Rules) {
		known = append(known, apText(r["rule_id"]))
	}
	for _, r := range apRows(b.Scenarios) {
		known = append(known, apText(r["scenario_id"]))
	}
	for _, id := range ids {
		if !apContains(known, id) {
			return nil, backendReject(s, "未知交付规则/场景")
		}
	}
	count := 0
	for _, scenario := range apRows(b.Scenarios) {
		if !apContains(ids, apText(scenario["scenario_id"])) {
			continue
		}
		count++
		for _, rule := range apStrings(scenario["rule_refs"]) {
			if !apContains(ids, rule) {
				return nil, backendReject(s, "交付场景缺少关联规则")
			}
		}
		for _, outcome := range []string{"success", "failure"} {
			found := false
			for _, row := range apRows(tests["coverage"]) {
				found = found || row["source_id"] == scenario["scenario_id"] && row["outcome"] == outcome
			}
			if !found {
				return nil, backendReject(s, "交付缺少成功/失败场景验证")
			}
		}
	}
	if count == 0 {
		return nil, backendReject(s, "交付缺少业务场景")
	}
	for _, id := range apStrings(apMap(d["scope"])["operation_ids"]) {
		if !apContains(tests["operation_ids"], id) {
			return nil, backendReject(s, "契约验证未覆盖接口")
		}
	}
	return d, nil
}
func backendVerification(s *semanticSession, binding any, basis, kind string) (map[string]any, error) {
	if _, e := backendBound(s, binding); e != nil {
		return nil, e
	}
	d, e := s.doc(apText(apMap(binding)["ref"]))
	if e != nil {
		return nil, e
	}
	if e = s.validateSchema(".template-spec/process/schemas/backend-delivery-verification.schema.json", d); e != nil {
		return nil, e
	}
	if d["kind"] != kind || d["subject_digest"] != basis {
		return nil, backendReject(s, "验证未绑定当前交付")
	}
	for _, row := range apRows(d["results"]) {
		t, valid := decisionTime(apText(row["executed_at"]))
		if apNumber(row["exit_code"]) != 0 || !valid || t.After(time.Now().Add(time.Minute)) {
			return nil, backendReject(s, "验证失败或执行时间无效")
		}
		for _, f := range apArray(row["evidence"]) {
			if _, e = backendBound(s, f); e != nil {
				return nil, e
			}
		}
	}
	return d, nil
}

func backendOpenDelivery(s *semanticSession, prefix string, opts map[string]string) (map[string]any, *semanticSession, error) {
	if strings.HasSuffix(strings.ToLower(prefix), ".zip") {
		archive, bundle, e := s.zipSourceSession(prefix)
		if e != nil {
			return nil, nil, e
		}
		return backendOpenDelivery(archive, bundle, opts)
	}
	if prefix == "" {
		return nil, nil, backendReject(s, "交付包引用为空")
	}
	manifest, e := s.doc(path.Join(prefix, "manifest.json"))
	if e != nil {
		return nil, nil, e
	}
	if apNumber(manifest["schema_version"]) != 1 || manifest["kind"] != "backend-delivery" || manifest["bundle_digest"] != apDigest(contractWithout(manifest, "bundle_digest")) {
		return nil, nil, backendReject(s, "后端包类型/清单摘要不一致")
	}
	list, ok := manifest["files"].([]any)
	if !ok || len(list) > 20000 {
		return nil, nil, backendReject(s, "后端包文件清单无效")
	}
	names := []string{"manifest.json"}
	paths := &safefs.PathSet{}
	aliases := map[string]string{}
	originals := &safefs.PathSet{}
	total := 0
	for _, v := range list {
		f := apMap(v)
		ref := apText(f["path"])
		for _, candidate := range []string{ref, apText(f["original_ref"])} {
			if e = rejectProgressionEvidence(s, candidate); e != nil {
				return nil, nil, e
			}
		}
		if e = paths.Add(ref); e != nil {
			return nil, nil, backendReject(s, "后端包路径非法/冲突")
		}
		names = append(names, ref)
		raw, e := s.bytes(path.Join(prefix, ref))
		if e != nil {
			return nil, nil, e
		}
		if len(raw) != apNumber(f["size_bytes"]) || backendHash(raw) != f["sha256"] {
			return nil, nil, backendReject(s, "后端包文件摘要/大小不一致")
		}
		total += len(raw)
		if original := apText(f["original_ref"]); original != "" {
			if e = originals.Add(original); e != nil {
				return nil, nil, backendReject(s, "后端包源路径非法/冲突")
			}
			aliases[original] = ref
		}
	}
	if total > 512<<20 {
		return nil, nil, backendReject(s, "后端包大小超限")
	}
	files, e := s.scan(prefix)
	if e != nil {
		return nil, nil, e
	}
	actual := []string{}
	for _, f := range files {
		actual = append(actual, strings.TrimPrefix(f, prefix+"/"))
	}
	if !apSetEqual(names, actual) {
		return nil, nil, backendReject(s, "后端包存在额外/缺失文件")
	}
	// Derive directory aliases only when every captured child has one coherent
	// stored location. The immutable manifest, never live asset fields, owns them.
	directories := map[string]string{}
	for original, stored := range aliases {
		for o, p := path.Dir(original), path.Dir(stored); o != "."; o, p = path.Dir(o), path.Dir(p) {
			if old, ok := directories[o]; ok && old != p {
				return nil, nil, backendReject(s, "后端源目录布局不唯一")
			}
			directories[o] = p
		}
	}
	for original, stored := range directories {
		aliases[original] = stored
	}
	source, e := s.sourceSnapshotSession(prefix, aliases)
	if e != nil {
		return nil, nil, e
	}
	source.roles, e = source.doc(approvalRolesRef)
	if e != nil {
		return nil, nil, e
	}
	if e = backendSourcePolicy(source); e != nil {
		return nil, nil, e
	}
	source.registry = backendPublishedRegistry(source.roles)
	if _, captured := aliases[approvalRegistryRef]; captured {
		source.registry, e = source.doc(approvalRegistryRef)
		if e != nil {
			return nil, nil, e
		}
	}
	d, e := backendInspectDelivery(source, apText(manifest["delivery_ref"]), backendSourceOptions(opts))
	if e != nil {
		return nil, nil, e
	}
	if d["delivery_id"] != manifest["delivery_id"] || d["version"] != manifest["version"] {
		return nil, nil, backendReject(s, "后端包身份/版本不一致")
	}
	return manifest, source, nil
}

func verifyBackendReviewSemantic(s *semanticSession, ref string, opts map[string]string) error {
	state, e := s.doc(ref)
	if e != nil {
		return e
	}
	result, e := backendReview(s, state, opts)
	if e == nil {
		s.report.Coverage = result
	}
	return e
}
func backendProjectRoot(s *semanticSession, input map[string]any) (string, error) {
	ref := apText(input["project_root"])
	if ref == "" {
		ref = "."
	}
	p := ref
	if !filepath.IsAbs(p) {
		p = filepath.Join(s.root, filepath.FromSlash(ref))
	}
	p, e := filepath.Abs(p)
	if e != nil {
		return "", e
	}
	if _, e = safefs.Path(p, "yss-project.yaml"); e != nil {
		return "", s.unavailable("PATH", e.Error())
	}
	return p, nil
}
func backendReview(s *semanticSession, state map[string]any, opts map[string]string) (map[string]any, error) {
	input := apMap(state["review_input"])
	kind := apText(input["scope_kind"])
	if !apContains([]string{"baseline", "change"}, kind) {
		return nil, backendReject(s, "历史记录需明确 baseline/change")
	}
	root, e := backendProjectRoot(s, input)
	if e != nil {
		return nil, e
	}
	var c *nativeSlice
	var contract map[string]any
	registerRef := apText(apMap(input["baseline_binding"])["ref"])
	if kind == "change" {
		if _, ok := input["actual_skill_impacts"].([]any); !ok {
			return nil, backendReject(s, "实际 Skill 影响必须显式数组")
		}
		c, e = loadNativeSlice(s, apText(input["slice_contract_ref"]))
		if e != nil {
			return nil, e
		}
		o := apCopyStrings(opts)
		o["approval-ref"] = apText(input["approval_ref"])
		o["unit"] = backendUnit(c, apText(input["work_unit_id"]))
		if contractN(c.Raw["schema_version"]) == 3 && semMap(c.Normalized["frontend"])["status"] == "required" {
			unit := apFind(c.Normalized["work_units"], "id", apText(input["work_unit_id"]))
			if unit == nil || unit["role_id"] != "role.backend-engineer" {
				return nil, backendReject(s, "混合Slice后端审查须显式选择当前批准后端工作单元")
			}
			unitRoot := text(unit["project_root"])
			if !filepath.IsAbs(unitRoot) {
				unitRoot = filepath.Join(s.root, unitRoot)
			}
			if filepath.Clean(unitRoot) != root {
				return nil, backendReject(s, "后端审查工程与当前工作单元冲突")
			}
		}
		if e = contractSliceFresh(s, c, o); e != nil {
			return nil, e
		}
		contract, e = backendSelected(s, c, o["unit"])
		if e != nil {
			return nil, e
		}
		if apMap(contract["backend"])["status"] != "required" {
			return map[string]any{"status": "not-applicable", "reason": "contract has no backend impact"}, nil
		}
		allowed := false
		for _, ref := range apStrings(apMap(contract["common"])["project_roots"]) {
			p := ref
			if !filepath.IsAbs(p) {
				p = filepath.Join(s.root, ref)
			}
			allowed = allowed || filepath.Clean(p) == root
		}
		if !allowed {
			return nil, backendReject(s, "候选工程超出批准合同")
		}
		registerRef = c.Ref
		if b := c.Basis["repository_registration"]; b != nil {
			registerRef = apText(b["ref"])
		}
	}
	if root != s.root {
		if registerRef == "" {
			return nil, backendReject(s, "外部工程缺少绑定的登记/基线")
		}
		if e = s.registerExternalRoot(root, registerRef); e != nil {
			return nil, e
		}
	}
	result, e := s.doc(apText(state["review_result_ref"]))
	if e != nil {
		return nil, e
	}
	if result["skill"] != "code-review" || result["result"] != "completed" {
		return nil, backendReject(s, "需要已完成独立 code-review")
	}
	for _, who := range []string{"reviewer", "implementer"} {
		for _, k := range []string{"actor_id", "runtime_id", "instance_id"} {
			if strings.TrimSpace(apText(apMap(result[who])[k])) == "" {
				return nil, backendReject(s, "独立审查身份缺失")
			}
		}
	}
	reviewer, implementer := apMap(result["reviewer"]), apMap(result["implementer"])
	if reviewer["actor_id"] == implementer["actor_id"] || reviewer["instance_id"] == implementer["instance_id"] || implementer["actor_id"] != input["implementation_actor_id"] || implementer["instance_id"] != input["implementation_instance_id"] {
		return nil, backendReject(s, "审查者/实现者不独立或身份不匹配")
	}
	comparison := ""
	if kind == "change" {
		b, e := s.bind(c.Ref)
		if e != nil {
			return nil, e
		}
		if result["contract_digest"] != b.Digest || result["candidate_digest"] != input["candidate_digest"] {
			return nil, backendReject(s, "审查合同/候选摘要变化")
		}
		if input["review_mode"] == "worktree" {
			comparison, e = backendWorktreeCurrent(s, root, input)
			if e != nil {
				return nil, e
			}
		} else {
			if input["review_mode"] != "committed" {
				return nil, backendReject(s, "未知审查候选模式")
			}
			comparison = apText(input["review_base_ref"])
			candidate := apText(input["implementation_candidate_ref"])
			if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(candidate) {
				return nil, backendReject(s, "审查候选须固定提交")
			}
			if _, e = candidateCommittedCurrent(s, root, candidate, apText(input["candidate_digest"]), c.Ref, "BACKEND_DELIVERY"); e != nil {
				return nil, e
			}
		}
	}
	coverage, e := backendCurrentCoverage(s, input, root, contract, comparison)
	if e != nil {
		return nil, e
	}
	if e = backendCoverageRows(s, root, coverage, result["constraint_results"]); e != nil {
		return nil, e
	}
	axes := apMap(result["axes"])
	if axes["Standards"] != "passed" {
		return nil, backendReject(s, "Standards 必须通过")
	}
	if kind == "change" && axes["Spec"] != "passed" {
		return nil, backendReject(s, "Spec 必须单独通过")
	}
	if kind == "baseline" {
		if !apContains([]string{"passed", "missing_evidence"}, apText(axes["Spec"])) {
			return nil, backendReject(s, "基线 Spec 结论须明确")
		}
		candidate := backendCoverageDigest(coverage["inventory"])
		if result["candidate_digest"] != candidate || input["candidate_digest"] != candidate {
			return nil, backendReject(s, "基线候选变化")
		}
		if axes["Spec"] == "passed" {
			binding := apMap(input["spec_binding"])
			if _, e = backendBoundFlexible(s, binding); e != nil {
				return nil, e
			}
			if e = contractApproval(s, "gate.spec-baseline-approved", binding, apText(binding["approval_ref"]), opts); e != nil {
				return nil, e
			}
			count := 0
			for _, row := range apRows(result["constraint_results"]) {
				if row["axis"] != "Spec" {
					continue
				}
				count++
				if row["status"] != "passed" || apText(row["constraint"]) == "" || apText(row["code_ref"]) == "" {
					return nil, backendReject(s, "Spec 验收证据未通过")
				}
				if _, e = backendCodeBytes(s, root, strings.Split(apText(row["code_ref"]), ":")[0]); e != nil {
					return nil, e
				}
				if _, e = backendBoundFlexible(s, map[string]any{"ref": row["evidence_ref"], "digest": row["evidence_digest"]}); e != nil {
					return nil, e
				}
			}
			if count == 0 {
				return nil, backendReject(s, "缺少 Spec 验收证据")
			}
		}
	}
	findings, ok := result["findings"].([]any)
	if !ok {
		return nil, backendReject(s, "findings 必须数组")
	}
	for _, v := range findings {
		f := apMap(v)
		disp := apText(f["disposition"])
		resolved := f["status"] == "resolved"
		if kind == "baseline" {
			if disp != "suggestion" && !resolved && !(disp == "missing_evidence" && f["axis"] == "Spec" && axes["Spec"] == "missing_evidence") {
				return nil, backendReject(s, "基线仍有阻断项")
			}
		} else {
			if apContains([]string{"violation", "drift", "new_impacts", "missing_evidence"}, disp) && !resolved {
				return nil, backendReject(s, "独立审查仍有阻断项")
			}
			if disp == "suggestion" && !resolved && apText(f["follow_up_ref"]) == "" {
				return nil, backendReject(s, "未解决建议缺少待办")
			}
		}
	}
	checks, ok := result["verification_results"].([]any)
	if !ok || len(checks) == 0 {
		return nil, backendReject(s, "缺少实际机器验证")
	}
	for _, v := range checks {
		r := apMap(v)
		checkedAt, valid := decisionTime(apText(r["executed_at"]))
		if apText(r["command"]) == "" || apNumber(r["exit_code"]) != 0 || !valid || checkedAt.After(time.Now().Add(time.Minute)) || r["candidate_digest"] != result["candidate_digest"] {
			return nil, backendReject(s, "机器验证失败/缺失/过期")
		}
		if _, e = backendBoundFlexible(s, map[string]any{"ref": r["evidence_ref"], "digest": r["evidence_digest"]}); e != nil {
			return nil, e
		}
	}
	skills := []any{}
	for _, r := range apRows(coverage["skills"]) {
		skills = append(skills, r["skill"])
	}
	status := "passed"
	if kind == "baseline" {
		status = "audited"
	}
	return map[string]any{"status": status, "axes": axes, "scope_kind": kind, "candidate_digest": result["candidate_digest"], "reviewed_skills": skills, "execution_allowed": false}, nil
}
func backendBoundFlexible(s *semanticSession, v any) ([]byte, error) {
	b := apMap(v)
	raw, e := s.bytes(apText(b["ref"]))
	if e != nil {
		return nil, e
	}
	if apText(b["digest"]) == "" || strings.TrimPrefix(apText(b["digest"]), "sha256:") != safefs.Digest(raw) {
		return nil, backendReject(s, "证据字节漂移")
	}
	return raw, nil
}

func backendWorktreeCurrent(s *semanticSession, root string, input map[string]any) (string, error) {
	ps := s
	if root != s.root {
		ps = newSemanticSession(s.ctx, root, s.args)
		ps.ruleSession = s
		s.children = append(s.children, ps)
	}
	snapshot, e := ps.worktreeCandidate(apText(input["candidate_snapshot_ref"]))
	if e != nil {
		return "", e
	}
	if snapshot.Manifest["candidate_digest"] != input["candidate_digest"] {
		return "", backendReject(s, "工作树 snapshot 摘要不匹配")
	}
	base := apText(snapshot.Manifest["merge_base"])
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(base) {
		return "", backendReject(s, "工作树 merge_base 须固定提交")
	}
	diff, e := s.git(root, "diff", "--binary", "--full-index", base)
	if e != nil {
		return "", e
	}
	allowed, e := candidateIntentAllowlist(s, root, apText(input["slice_contract_ref"]))
	if e != nil {
		return "", e
	}
	if !bytes.Equal(diff, snapshot.TrackedDiff) {
		if e = candidateTrackedIntentDifference(s, root, base, snapshot.TrackedDiff, diff, allowed); e != nil {
			return "", e
		}
	}
	exclusions := apStrings(snapshot.Manifest["excluded_paths"])
	for _, ref := range exclusions {
		if !strings.HasPrefix(ref, ".template-source/evidence/maintenance/") || safefs.ValidateRef(ref) != nil {
			return "", backendReject(s, "候选只能排除既有维护证据路径")
		}
	}
	inventoryRoot, prefix := root, ""
	if len(allowed) > 0 {
		inventoryRoot = s.root
		rel, err := filepath.Rel(s.root, root)
		if err != nil {
			return "", err
		}
		if rel != "." {
			prefix = filepath.ToSlash(rel) + "/"
		}
	}
	current, e := s.git(inventoryRoot, "ls-files", "-z", "--others", "--exclude-standard")
	if e != nil {
		return "", e
	}
	actual := []string{}
	for _, ref := range strings.Split(string(current), "\x00") {
		if ref == "" {
			continue
		}
		exclude := false
		for _, parent := range exclusions {
			exclude = exclude || ref == prefix+parent || strings.HasPrefix(ref, prefix+parent+"/")
		}
		if !exclude && !allowed[ref] {
			actual = append(actual, ref)
		}
	}
	expected := []string{}
	for _, file := range snapshot.Files {
		if !allowed[prefix+string(file.RawPath)] {
			expected = append(expected, prefix+string(file.RawPath))
		}
	}
	sort.Strings(actual)
	sort.Strings(expected)
	if !equalStrings(actual, expected) {
		return "", backendReject(s, "工作树 untracked inventory 变化："+strings.Join(actual, "|")+" != "+strings.Join(expected, "|"))
	}
	observation, e := s.intakeSnapshot(root, 0)
	if e != nil {
		return "", e
	}
	s.intakeInputs[root] = observation
	for _, file := range snapshot.Files {
		ref := string(file.RawPath)
		if allowed[prefix+ref] {
			continue
		} // Original packed bytes were already validated; only verified current intent may differ.
		if e = safefs.ValidateRef(ref); e != nil {
			return "", e
		}
		parent := path.Dir(ref)
		if parent != "." {
			if _, e = safefs.Path(root, parent); e != nil {
				return "", e
			}
		}
		p := filepath.Join(root, filepath.FromSlash(ref))
		info, e := os.Lstat(p)
		if e != nil {
			return "", e
		}
		mode := uint32(info.Mode().Perm())
		var raw []byte
		if file.Kind == 0x52 && info.Mode().IsRegular() {
			mode |= 0100000
			raw, e = backendCodeBytes(s, root, ref)
		} else if file.Kind == 0x4c && info.Mode()&os.ModeSymlink != 0 {
			mode |= 0120000
			target, err := os.Readlink(p)
			e = err
			raw = []byte(target)
		} else {
			return "", backendReject(s, "工作树文件类型变化")
		}
		if e != nil {
			return "", e
		}
		if info.Mode()&os.ModeSetuid != 0 {
			mode |= 04000
		}
		if info.Mode()&os.ModeSetgid != 0 {
			mode |= 02000
		}
		if info.Mode()&os.ModeSticky != 0 {
			mode |= 01000
		}
		if mode != file.Mode || !bytes.Equal(raw, file.Content) {
			return "", backendReject(s, "工作树文件原始字节/模式变化")
		}
	}
	return base, nil
}
func backendCoverageDigest(v any) string {
	if rows, ok := v.([]any); ok {
		inventory := make([]struct {
			Path   string `json:"path"`
			Mode   any    `json:"mode"`
			Digest string `json:"digest"`
		}, len(rows))
		valid := len(rows) > 0
		for i, row := range rows {
			m := apMap(row)
			if len(m) != 3 || apText(m["path"]) == "" || m["mode"] == nil || apText(m["digest"]) == "" {
				valid = false
				break
			}
			inventory[i].Path = apText(m["path"])
			inventory[i].Mode = m["mode"]
			inventory[i].Digest = apText(m["digest"])
		}
		if valid {
			b, _ := json.Marshal(inventory)
			return safefs.Digest(b)
		}
	}
	b, _ := json.Marshal(v)
	return safefs.Digest(b)
}
func backendCodeBytes(s *semanticSession, root, ref string) ([]byte, error) {
	if root == s.root {
		return s.bytes(ref)
	}
	return s.externalBytes(root, ref)
}

type backendJava struct {
	ref, code, pkg                 string
	declared, annotations, parents []string
	tokens, roles                  map[string]bool
	http                           bool
	uncertainty                    map[string]any
}

var backendLiterals = regexp.MustCompile(`(?s)""".*?"""|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|/\*.*?\*/|//[^\n]*`)
var backendPackage = regexp.MustCompile(`\bpackage\s+([\w.]+)\s*;`)
var backendDeclared = regexp.MustCompile(`(?:@\s*interface|\bclass|\binterface|\benum|\brecord)\s+(\w+)`)
var backendAnnotations = regexp.MustCompile(`@([\w.]+)`)
var backendParent = regexp.MustCompile(`\b(?:extends|implements)\s+([\w.,<>\s]+)`)
var backendTokens = regexp.MustCompile(`\b[A-Z]\w*`)

func backendJavaType(ref string, b []byte) *backendJava {
	code := backendLiterals.ReplaceAllStringFunc(string(b), func(v string) string { return regexp.MustCompile(`[^\n]`).ReplaceAllString(v, " ") })
	t := &backendJava{ref: ref, code: code, tokens: map[string]bool{}, roles: map[string]bool{}}
	if m := backendPackage.FindStringSubmatch(code); len(m) > 1 {
		t.pkg = m[1]
	}
	for _, m := range backendDeclared.FindAllStringSubmatch(code, -1) {
		t.declared = append(t.declared, m[1])
	}
	for _, m := range backendAnnotations.FindAllStringSubmatch(code, -1) {
		parts := strings.Split(m[1], ".")
		t.annotations = append(t.annotations, parts[len(parts)-1])
	}
	for _, m := range backendParent.FindAllStringSubmatch(code, -1) {
		t.parents = append(t.parents, backendTokens.FindAllString(m[1], -1)...)
	}
	for _, name := range backendTokens.FindAllString(code, -1) {
		t.tokens[name] = true
	}
	return t
}
func backendUses(from, to *backendJava) bool {
	for _, name := range to.declared {
		if from.tokens[name] && (from.pkg == to.pkg || strings.Contains(from.code, to.pkg+"."+name) || strings.Contains(from.code, "import "+to.pkg+".*")) {
			return true
		}
	}
	return false
}

var backendRoleSkills = map[string]string{"web": "yss-web-controller", "server": "yss-web-controller", "domain": "yss-domain", "application": "yss-application", "service": "yss-application", "core": "yss-application", "infrastructure": "yss-repository", "persistence": "yss-repository", "repository": "yss-repository"}
var backendCoreSkills = []string{"yss-web-controller", "yss-dto", "yss-domain", "yss-application", "yss-repository", "yss-mybatis"}

func backendHasRole(t *backendJava, roles ...string) bool {
	for _, r := range roles {
		if t.roles[r] {
			return true
		}
	}
	return false
}
func backendSortedKeys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Excluded build/Git trees are not source. Git's full cached/other inventory and
// bytes are observed separately for final rechecking, including ignored files.
func backendSourceFiles(s *semanticSession, root string) ([]string, error) {
	snapshot, e := s.intakeSnapshot(root, 0)
	if e != nil {
		return nil, e
	}
	s.intakeInputs[root] = snapshot
	files := []string{}
	e = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if e = s.guard(); e != nil {
			return e
		}
		ref, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		ref = filepath.ToSlash(ref)
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "target" || d.Name() == "node_modules" || ref == ".template-source/evidence" {
				return filepath.SkipDir
			}
			return nil
		}
		if _, e = safefs.Path(root, ref); e != nil {
			return e
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return backendReject(s, "未解析源 symlink/文件类型: "+ref)
		}
		files = append(files, ref)
		if len(files) > 20000 {
			return s.unavailable("CAPABILITY", "后端 source inventory 超过20000项")
		}
		return nil
	})
	if e != nil {
		return nil, s.unavailable("PATH", e.Error())
	}
	order := collate.New(language.English)
	sort.Slice(files, func(i, j int) bool { return order.CompareString(files[i], files[j]) < 0 })
	return files, nil
}
func backendCurrentCoverage(s *semanticSession, input map[string]any, root string, contract map[string]any, comparison string) (map[string]any, error) {
	ref := apText(input["standards_coverage_ref"])
	b, e := backendBoundFlexible(s, map[string]any{"ref": ref, "digest": input["standards_coverage_digest"]})
	if e != nil {
		return nil, e
	}
	v, e := schema.Parse(b)
	if e != nil {
		return nil, e
	}
	recorded := apMap(v)
	intent, e := candidateCoverageIntent(s, root, input, recorded)
	if e != nil {
		return nil, e
	}
	current, e := backendCompileCoverageIntent(s, root, contract, apText(input["scope_kind"]), apMap(input["baseline_binding"]), comparison, apStrings(input["actual_skill_impacts"]), apRows(input["responsibility_evidence"]), intent)
	if e != nil {
		return nil, e
	}
	if intent != nil {
		candidateRestoreCoverageMaterials(current, recorded, intent)
	}
	if !apEqual(current, recorded) {
		return nil, backendReject(s, "规范覆盖过期或不完整")
	}
	return current, nil
}
func backendCompileCoverage(s *semanticSession, root string, contract map[string]any, kind string, baselineBinding map[string]any, comparison string, actual []string, responsibilities []map[string]any) (map[string]any, error) {
	return backendCompileCoverageIntent(s, root, contract, kind, baselineBinding, comparison, actual, responsibilities, nil)
}
func backendCompileCoverageIntent(s *semanticSession, root string, contract map[string]any, kind string, baselineBinding map[string]any, comparison string, actual []string, responsibilities []map[string]any, intent *candidateCoverageContext) (map[string]any, error) {
	if !apContains([]string{"baseline", "change"}, kind) {
		return nil, backendReject(s, "未知覆盖范围")
	}
	resolution := apMap(contract["resolution"])
	basis := apMap(apMap(resolution["architecture_evidence"])["engineering_baseline"])
	if len(basis) == 0 {
		basis = baselineBinding
	}
	var baseline map[string]any
	if len(basis) > 0 {
		b, e := backendBoundFlexible(s, basis)
		if e != nil {
			return nil, e
		}
		v, e := schema.Parse(b)
		if e != nil {
			return nil, e
		}
		baseline = apMap(v)
	}
	identity := apMap(resolution["architecture_identity"])
	if len(identity) == 0 {
		identity = apMap(baseline["architecture_identity"])
	}
	family := apText(identity["architecture_family"])
	issues := []any{}
	issue := func(id, reason string) { issues = append(issues, map[string]any{"id": id, "reason": reason}) }
	if len(baseline) > 0 && (baseline["kind"] != "existing-engineering-baseline" || baseline["status"] != "current") && identity["source_kind"] == "existing-registration" {
		issue("baseline-not-current", "既有工程基线未标记为当前观测，须先核对登记")
	}
	if !apContains([]string{"domain-driven", "layered-mvc"}, family) {
		issue("architecture-unknown", "需要登记架构；只读盘点不能猜测 DDD/MVC")
	}
	all, e := backendSourceFiles(s, root)
	if e != nil {
		return nil, e
	}
	roots := apStrings(apMap(baseline["source"])["roots"])
	if apMap(baseline["source"])["roots"] == nil {
		roots = []string{"."}
	}
	for _, r := range roots {
		if r != "." && safefs.ValidateRef(r) != nil {
			return nil, backendReject(s, "登记源目录非法")
		}
	}
	selected := []string{}
	outside := []string{}
	for _, ref := range all {
		if intent != nil && intent.Local[ref] {
			continue
		}
		inside := false
		for _, r := range roots {
			inside = inside || r == "." || ref == r || strings.HasPrefix(ref, r+"/")
		}
		if inside {
			selected = append(selected, ref)
		} else if strings.HasSuffix(ref, ".java") {
			outside = append(outside, ref)
		}
	}
	if len(outside) > 0 {
		issue("unregistered-source", strings.Join(outside, ", "))
	}
	inventory := []any{}
	types := []*backendJava{}
	for _, ref := range selected {
		b, e := backendCodeBytes(s, root, ref)
		if e != nil {
			return nil, e
		}
		p, e := safefs.Path(root, ref)
		if e != nil {
			return nil, e
		}
		info, e := os.Lstat(p)
		if e != nil {
			return nil, e
		}
		mode := int64(info.Mode().Perm()) | 0100000
		if info.Mode()&os.ModeSetuid != 0 {
			mode |= 04000
		}
		if info.Mode()&os.ModeSetgid != 0 {
			mode |= 02000
		}
		if info.Mode()&os.ModeSticky != 0 {
			mode |= 01000
		}
		inventory = append(inventory, map[string]any{"path": ref, "mode": mode, "digest": safefs.Digest(b)})
		if strings.HasSuffix(ref, ".java") {
			t := backendJavaType(ref, b)
			types = append(types, t)
			if len(t.declared) == 0 || regexp.MustCompile(`\\u[0-9a-fA-F]{4}`).MatchString(t.code) {
				issue("unparsed:"+ref, "源码类型解析不足，须人工核实或编译态证据")
			}
		}
	}
	business := false
	http := map[string]bool{}
	for _, name := range []string{"RestController", "Controller", "RequestMapping", "GetMapping", "PostMapping", "PutMapping", "DeleteMapping", "PatchMapping"} {
		http[name] = true
	}
	for _, t := range types {
		business = business || !strings.Contains(t.ref, "/test/")
	}
	if !business {
		issue("zero-business-types", "未发现业务类型，不能证明存量工程合规")
	}
	for changing := true; changing; {
		changing = false
		for _, t := range types {
			hit := false
			for _, name := range append(append([]string{}, t.annotations...), t.parents...) {
				hit = hit || http[name]
			}
			if !t.http && hit {
				t.http = true
				for _, name := range t.declared {
					http[name] = true
				}
				changing = true
			}
		}
	}
	local, familiar := map[string]bool{}, map[string]bool{}
	for _, t := range types {
		for _, name := range t.declared {
			local[name] = true
		}
	}
	for name := range http {
		familiar[name] = true
	}
	for _, name := range strings.Split("interface Override Deprecated SuppressWarnings FunctionalInterface Serializable Comparable Exception RuntimeException Service Repository Mapper Entity TableName Id TableId Autowired Component Configuration Bean Valid Validated NotNull NotBlank Size Min Max Pattern RequestBody RequestParam PathVariable RequestHeader CookieValue ResponseBody ResponseStatus ExceptionHandler ControllerAdvice RestControllerAdvice Transactional Data Getter Setter Builder NoArgsConstructor AllArgsConstructor RequiredArgsConstructor Slf4j EqualsAndHashCode ToString JsonProperty JsonIgnore JsonCreator Test", " ") {
		familiar[name] = true
	}
	resolutions := []map[string]any{}
	for _, b := range responsibilities {
		raw, e := backendBoundFlexible(s, b)
		if e != nil {
			return nil, e
		}
		v, e := schema.Parse(raw)
		if e != nil {
			return nil, e
		}
		resolutions = append(resolutions, apMap(v))
	}
	for _, t := range types {
		for _, u := range apRows(baseline["build_units"]) {
			for role, paths := range apMap(u["role_paths"]) {
				for _, ref := range apStrings(paths) {
					if contractWithin(t.ref, ref) {
						t.roles[role] = true
					}
				}
			}
		}
		for _, a := range apRows(apMap(apMap(contract["backend"])["first_slice"])["artifacts"]) {
			if a["path"] == t.ref {
				t.roles[apText(a["role"])] = true
			}
		}
		if t.http {
			t.roles["web"] = true
		}
		if apContains(t.annotations, "Service") {
			t.roles["application"] = true
		}
		if apContains(t.annotations, "Repository") || apContains(t.annotations, "Entity") || apContains(t.annotations, "TableName") || apContains(t.annotations, "Mapper") && !strings.Contains(t.code, "org.mapstruct") || regexp.MustCompile(`\b(?:JdbcTemplate|SqlSession|EntityManager|BaseMapper)\b`).MatchString(t.code) {
			t.roles["persistence"] = true
		}
		unresolved := []string{}
		for _, name := range append(append([]string{}, t.annotations...), t.parents...) {
			if !local[name] && !familiar[name] {
				unresolved = append(unresolved, name)
			}
		}
		if (len(unresolved) > 0 || len(t.roles) == 0 && !regexp.MustCompile(`@\s*interface\b`).MatchString(t.code)) && !strings.Contains(t.ref, "/test/") {
			r := apFind(resolutions, "source_ref", t.ref)
			if r == nil {
				reason := strings.Join(unresolved, ", ")
				if reason == "" {
					reason = "type has no registered or observed responsibility"
				}
				t.uncertainty = map[string]any{"id": "unresolved-responsibility:" + t.ref, "reason": reason}
			} else {
				b, e := backendCodeBytes(s, root, t.ref)
				if e != nil {
					return nil, e
				}
				roles := apStrings(r["roles"])
				if r["source_digest"] != safefs.Digest(b) || strings.TrimSpace(apText(r["reason"])) == "" || len(roles) == 0 {
					return nil, backendReject(s, "职责证据失效")
				}
				for _, role := range roles {
					if backendRoleSkills[role] == "" && role != "data" {
						return nil, backendReject(s, "职责证据角色非法")
					}
					t.roles[role] = true
				}
				if _, e = backendBoundFlexible(s, map[string]any{"ref": r["evidence_ref"], "digest": r["evidence_digest"]}); e != nil {
					return nil, e
				}
				t.http = t.http || backendHasRole(t, "web", "server")
			}
		}
	}
	active := types
	changedPaths := []string{}
	if kind == "change" {
		if contract == nil || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(comparison) {
			return nil, backendReject(s, "变更覆盖需要批准合同和固定 comparison_ref")
		}
		var diff []byte
		var e error
		if intent != nil {
			diff, e = candidateCoverageTrackedPaths(s, root, comparison, intent)
		} else {
			diff, e = s.git(root, "diff", "--name-only", comparison)
		}
		if e != nil {
			return nil, e
		}
		other, e := s.git(root, "ls-files", "--others", "--exclude-standard")
		if e != nil {
			return nil, e
		}
		changed := map[string]bool{}
		for _, ref := range strings.Split(strings.TrimSpace(string(diff)+"\n"+string(other)), "\n") {
			if intent != nil && (intent.Allowed[ref] || intent.Local[ref]) {
				continue
			}
			if ref != "" {
				changed[ref] = true
			}
		}
		changedPaths = backendSortedKeys(changed)
		affected := map[*backendJava]bool{}
		for _, t := range types {
			if changed[t.ref] {
				affected[t] = true
			}
		}
		for expanding := true; expanding; {
			expanding = false
			for _, t := range types {
				if affected[t] {
					continue
				}
				for a := range affected {
					if backendUses(t, a) || backendUses(a, t) {
						affected[t] = true
						expanding = true
						break
					}
				}
			}
		}
		active = []*backendJava{}
		for _, t := range types {
			if affected[t] {
				active = append(active, t)
			}
		}
		for _, p := range selected {
			if changed[p] && !strings.HasSuffix(p, ".java") && !strings.HasPrefix(p, ".template-source/") {
				active = types
				break
			}
		}
	}
	for _, t := range active {
		if t.uncertainty != nil {
			issues = append(issues, t.uncertainty)
		}
	}
	tags := map[string]bool{"always": true}
	if kind == "change" {
		tags["change"] = true
	}
	reasons := map[string]map[string]bool{}
	add := func(skill, reason string) {
		if reasons[skill] == nil {
			reasons[skill] = map[string]bool{}
		}
		reasons[skill][reason] = true
	}
	declared := append(append(append([]string{}, apStrings(resolution["required_skills"])...), apStrings(apMap(contract["common"])["required_skills"])...), apStrings(apMap(contract["backend"])["required_skills"])...)
	for _, skill := range append(append([]string{}, declared...), actual...) {
		add(skill, "contract-or-declared-impact")
	}
	add("alibaba-java-code-style", "backend-scope")
	for _, t := range active {
		if strings.Contains(t.ref, "/test/") {
			continue
		}
		for role := range t.roles {
			if skill := backendRoleSkills[role]; skill != "" {
				add(skill, t.ref)
			}
		}
		if t.http {
			tags["web"], tags["wire"] = true, true
			add("yss-dto", t.ref)
		}
		if regexp.MustCompile(`\b(?:PageResult|PageQuery|CommandDTO|QueryDTO|SingleResult|MultiResult)\b`).MatchString(t.code) {
			tags["wire"] = true
			add("yss-dto", t.ref)
		}
		tags["domain"] = tags["domain"] || t.roles["domain"] && family == "domain-driven"
		tags["application"] = tags["application"] || backendHasRole(t, "application", "service", "core")
		tags["persistence"] = tags["persistence"] || backendHasRole(t, "repository", "persistence", "infrastructure")
		if regexp.MustCompile(`\b(?:PageQuery|PageRequest|PageResult|IPage|PageHelper|pageIndex|pageSize)\b`).MatchString(t.code) {
			tags["pagination"] = true
		}
		if regexp.MustCompile(`\b(?:insertBatch|saveBatch|updateBatch|executeBatch)\b`).MatchString(t.code) {
			tags["batch"] = true
		}
		for pattern, skill := range map[string]string{`\b(?:Valid|Validated|NotNull|NotBlank|Size)\b`: "yss-validation", `\b(?:ExceptionHandler|ControllerAdvice|RestControllerAdvice)\b`: "yss-exception", `\borg\.mapstruct\b`: "mapstruct", `\blombok\b`: "lombok"} {
			if regexp.MustCompile(pattern).MatchString(t.code) {
				add(skill, t.ref)
			}
		}
	}
	for _, layer := range apStrings(apMap(contract["backend"])["affected_layers"]) {
		if skill := backendRoleSkills[layer]; skill != "" {
			add(skill, "affected-layer:"+layer)
		}
	}
	textParts := []string{}
	for _, t := range active {
		textParts = append(textParts, t.code)
	}
	for _, p := range selected {
		if kind != "baseline" && !apContains(changedPaths, p) {
			continue
		}
		if regexp.MustCompile(`\.(?:xml|ya?ml|properties|sql|json)$`).MatchString(p) {
			b, e := backendCodeBytes(s, root, p)
			if e != nil {
				return nil, e
			}
			textParts = append(textParts, string(b))
		}
	}
	runtimeText := strings.Join(textParts, "\n")
	discovery := []any{}
	registryRef := approvalSkillsRef
	present, e := s.exists(registryRef)
	if e != nil {
		return nil, e
	}
	if present {
		raw, e := s.bytes(registryRef)
		if e != nil {
			return nil, e
		}
		discovery = append(discovery, map[string]any{"ref": registryRef, "digest": safefs.Digest(raw)})
		registry, e := s.doc(registryRef)
		if e != nil {
			return nil, e
		}
		line := apText(apMap(identity["platform_configuration"])["component_platform_line"])
		if !apContains([]string{"boot2-java8", "boot3-java17"}, line) {
			issue("component-discovery-platform-unknown", "组件发现需要已登记的平台源码索引，不跨平台借证据")
		} else {
			components := map[string]bool{}
			for _, c := range apRows(registry["capabilities"]) {
				if apMap(c["provider"])["kind"] == "yss-component" {
					components[apText(c["primary_skill"])] = true
				}
			}
			for _, skill := range backendSortedKeys(components) {
				if !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(skill) {
					return nil, backendReject(s, "组件 Skill ID 非法")
				}
				ref := ".agents/skills/" + skill + "/references/source-index." + line + ".md"
				present, e := s.exists(ref)
				if e != nil {
					return nil, e
				}
				if !present {
					issue("component-index-missing:"+skill, ref)
					continue
				}
				raw, e := s.bytes(ref)
				if e != nil {
					return nil, e
				}
				discovery = append(discovery, map[string]any{"ref": ref, "digest": safefs.Digest(raw)})
				hit := false
				for _, m := range regexp.MustCompile(`src/main/java/([\w/$]+)\.java`).FindAllStringSubmatch(string(raw), -1) {
					symbol := strings.ReplaceAll(m[1], "/", ".")
					i := strings.LastIndex(symbol, ".")
					hit = hit || strings.Contains(runtimeText, symbol) || i >= 0 && strings.Contains(runtimeText, "import "+symbol[:i]+".*")
				}
				for _, m := range regexp.MustCompile("GAV `([^:`]+):([^:`]+):[^`]+`").FindAllStringSubmatch(string(raw), -1) {
					hit = hit || strings.Contains(runtimeText, "<groupId>"+m[1]+"</groupId>") && strings.Contains(runtimeText, "<artifactId>"+m[2]+"</artifactId>")
				}
				if hit {
					add(skill, "registered-component-index:"+ref)
				}
			}
		}
	}
	if regexp.MustCompile(`(?i)org\.apache\.ibatis|com\.baomidou|mybatis|<mapper\b`).MatchString(runtimeText) {
		tags["mybatis"], tags["persistence"] = true, true
		add("yss-mybatis", "source-or-build-binding")
		add("yss-repository", "source-or-build-binding")
	}
	if regexp.MustCompile(`\b(?:JdbcTemplate|EntityManager)\b|spring-boot-starter-data-jpa|<mapper\b`).MatchString(runtimeText) {
		tags["persistence"] = true
		add("yss-repository", "source-or-build-binding")
	}
	for skill, tag := range map[string]string{"yss-web-controller": "web", "yss-dto": "wire", "yss-domain": "domain", "yss-application": "application", "yss-repository": "persistence", "yss-mybatis": "mybatis"} {
		if reasons[skill] != nil {
			tags[tag] = true
		}
	}
	if family == "layered-mvc" {
		delete(tags, "domain")
		if reasons["yss-domain"] != nil {
			issue("mvc-domain-conflict", "MVC 不应加载 DDD 专属技能，须回合同核对")
		}
		delete(reasons, "yss-domain")
	}
	observed := []string{}
	for skill := range reasons {
		observed = append(observed, skill)
	}
	sort.Strings(observed)
	if kind == "change" {
		for _, skill := range observed {
			if skill != "alibaba-java-code-style" && !apContains(declared, skill) {
				issue("undeclared-skill:"+skill, "actual impact exceeds contract; recompile before execution")
			}
		}
	}
	var lock map[string]any
	var lockDigest any
	present, e = s.exists("skills-lock.json")
	if e != nil {
		return nil, e
	}
	if present {
		raw, e := s.bytes("skills-lock.json")
		if e != nil {
			return nil, e
		}
		lockDigest = safefs.Digest(raw)
		lock, e = s.doc("skills-lock.json")
		if e != nil {
			return nil, e
		}
	} else {
		issue("skill-lock-missing", "缺少锁定的 canonical Skill 来源；只读盘点不能声明规范已锁定")
	}
	trees := map[string]string{}
	constraints, ruleSources := []any{}, []any{}
	constraintIDs := map[string]bool{}
	for _, skill := range observed {
		tree, e := backendSkillTree(s, skill)
		if e != nil {
			return nil, e
		}
		trees[skill] = tree
		if apMap(apMap(apMap(lock["skills"])["shared"])[skill])["effectiveHash"] != tree {
			issue("skill-lock-drift:"+skill, "技能正文、引用或源码索引与锁不一致")
		}
		rules, sources, e := backendReadRules(s, skill)
		if e != nil {
			return nil, e
		}
		ruleSources = append(ruleSources, sources...)
		for _, r := range rules {
			if constraintIDs[apText(r["id"])] {
				return nil, backendReject(s, "canonical constraint ID 重复")
			}
			constraintIDs[apText(r["id"])] = true
			when := apText(r["when"])
			applicability := "not-applicable"
			if tags[when] {
				applicability = "required"
			} else if when == "pagination" || when == "batch" {
				applicability = "conditional"
			}
			r["constraint_id"], r["applicability"] = r["id"], applicability
			if applicability == "required" {
				r["applicability_basis"] = backendSortedKeys(reasons[skill])
			} else {
				r["applicability_basis"] = []string{"no-observed-" + when + "; reviewer must reconcile actual behavior"}
			}
			constraints = append(constraints, r)
		}
	}
	ruleOrder := collate.New(language.English)
	sort.Slice(constraints, func(i, j int) bool {
		return ruleOrder.CompareString(apText(apMap(constraints[i])["constraint_id"]), apText(apMap(constraints[j])["constraint_id"])) < 0
	})
	sort.Slice(ruleSources, func(i, j int) bool {
		return ruleOrder.CompareString(apText(apMap(ruleSources[i])["ref"]), apText(apMap(ruleSources[j])["ref"])) < 0
	})
	platform := apMap(identity["platform_configuration"])
	catalogRef := ".template-spec/engineering/backend-platforms.json"
	var catalogDigest any
	present, e = s.exists(catalogRef)
	if e != nil {
		return nil, e
	}
	if apText(platform["profile_id"]) != "" && present {
		raw, e := s.bytes(catalogRef)
		if e != nil {
			return nil, e
		}
		catalogDigest = safefs.Digest(raw)
		catalog, e := s.doc(catalogRef)
		if e != nil {
			return nil, e
		}
		profile := apFind(catalog["profiles"], "id", apText(platform["profile_id"]))
		if profile == nil || !apEqual(profile["java_version"], platform["java_version"]) || profile["spring_boot_version"] != platform["spring_boot_version"] || profile["component_platform_line"] != platform["component_platform_line"] {
			issue("platform-identity-mismatch", "登记精确平台与事实源不一致")
		} else {
			other := "javax"
			if profile["validation_namespace"] == "javax" {
				other = "jakarta"
			}
			for _, t := range types {
				if regexp.MustCompile(`\bimport\s+` + other + `\.(?:validation|servlet)\.`).MatchString(t.code) {
					issue("platform-namespace-mismatch", "源码使用了目标平台之外的 Validation/Servlet 命名空间")
					break
				}
			}
		}
	}
	findings := []any{}
	for _, t := range active {
		webdep, domainDep := false, false
		for _, other := range types {
			if other == t || !backendUses(t, other) {
				continue
			}
			webdep = webdep || backendHasRole(other, "web", "server")
			domainDep = domainDep || backendHasRole(other, "domain", "repository", "infrastructure", "persistence")
		}
		addFinding := func(id, reason string) {
			findings = append(findings, map[string]any{"constraint_id": id, "code_ref": t.ref, "disposition": "violation", "reason": reason})
		}
		if backendHasRole(t, "infrastructure", "repository", "persistence") && webdep {
			addFinding("repository.ownership", "持久化层引用 Web 类型")
		}
		if t.http && !apContains(t.annotations, "ControllerAdvice") && !apContains(t.annotations, "RestControllerAdvice") && (domainDep || regexp.MustCompile(`\b(?:[A-Z]\w*(?:Gateway|Repository|Mapper)|JdbcTemplate|EntityManager)\b`).MatchString(t.code)) {
			addFinding("web.use-case", "HTTP 入口直接引用领域/持久化能力")
		}
		if t.roles["domain"] && family == "domain-driven" && regexp.MustCompile(`\bimport\s+(?:org\.springframework|org\.apache\.ibatis|com\.baomidou|jakarta\.persistence|javax\.persistence)\.`).MatchString(t.code) {
			addFinding("domain.dependencies", "Domain 技术依赖泄漏")
		}
	}
	head, e := s.git(root, "rev-parse", "HEAD")
	if e != nil {
		return nil, e
	}
	if intent != nil {
		head = []byte(intent.SourceHead)
	}
	reviewed := []string{}
	for _, t := range active {
		reviewed = append(reviewed, t.ref)
	}
	sort.Strings(reviewed)
	skills, assessments := []any{}, []any{}
	for _, skill := range observed {
		skills = append(skills, map[string]any{"skill": skill, "tree_digest": trees[skill], "reasons": backendSortedKeys(reasons[skill])})
	}
	for _, skill := range backendCoreSkills {
		status, reason := "not-applicable", "no matching registered responsibility or source signal"
		if reasons[skill] != nil {
			status, reason = "applicable", "observed-or-declared"
		}
		assessments = append(assessments, map[string]any{"skill": skill, "status": status, "reason": reason})
	}
	var identityValue, basisValue, comparisonValue any
	if len(identity) > 0 {
		identityValue = identity
	}
	if len(basis) > 0 {
		basisValue = basis
	}
	if comparison != "" {
		comparisonValue = comparison
	}
	resp := []any{}
	for _, r := range responsibilities {
		resp = append(resp, r)
	}
	return map[string]any{"schema_version": 1, "scope_kind": kind, "architecture_identity": identityValue, "basis": basisValue, "comparison_ref": comparisonValue, "source_roots": roots, "source_head": strings.TrimSpace(string(head)), "changed_paths": changedPaths, "inventory": inventory, "reviewed_paths": reviewed, "skills": skills, "skill_assessments": assessments, "constraints": constraints, "responsibility_evidence": resp, "discovery_sources": discovery, "platform_catalog_digest": catalogDigest, "rule_sources": ruleSources, "skill_lock_digest": lockDigest, "issues": issues, "findings": findings, "limitations": []string{"源码检查用于发现职责与明确反例，不替代编译态 ArchUnit、HTTP fixture 或 Reviewer 全文语义审查。"}}, nil
}

func backendSkillTree(s *semanticSession, skill string) (string, error) {
	if !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(skill) {
		return "", backendReject(s, "Skill ID 非法")
	}
	prefix := ".agents/skills/" + skill
	files, e := s.scan(prefix)
	if e != nil {
		return "", e
	}
	h := sha256.New()
	order := collate.New(language.English)
	sort.Slice(files, func(i, j int) bool {
		return order.CompareString(strings.TrimPrefix(files[i], prefix+"/"), strings.TrimPrefix(files[j], prefix+"/")) < 0
	})
	count := 0
	for _, ref := range files {
		name := strings.TrimPrefix(ref, prefix+"/")
		if path.Base(name) == ".DS_Store" || apContains(strings.Split(name, "/"), "__pycache__") || regexp.MustCompile(`\.(?:iml|pyc|pyo)$`).MatchString(name) {
			continue
		}
		b, e := s.bytes(ref)
		if e != nil {
			return "", e
		}
		if !bytes.ContainsRune(b, 0) {
			b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
		count++
	}
	if count == 0 {
		return "", backendReject(s, "适用 Skill 缺失: "+skill)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func backendReadRules(s *semanticSession, skill string) ([]map[string]any, []any, error) {
	prefix := ".agents/skills/" + skill
	main := prefix + "/SKILL.md"
	raw, e := s.bytes(main)
	if e != nil {
		return nil, nil, e
	}
	files, e := s.scan(prefix)
	if e != nil {
		return nil, nil, e
	}
	rules := []map[string]any{}
	sources := []any{}
	declaration := regexp.MustCompile(`(?m)^<!-- yss-rule (\{[^\n]+\}) -->$`)
	for _, ref := range files {
		if !strings.HasSuffix(ref, ".md") || strings.Contains(ref, "source-index") {
			continue
		}
		b, e := s.bytes(ref)
		if e != nil {
			return nil, nil, e
		}
		sources = append(sources, map[string]any{"ref": ref, "digest": safefs.Digest(b)})
		for _, m := range declaration.FindAllSubmatch(b, -1) {
			var r map[string]any
			if e = json.Unmarshal(m[1], &r); e != nil {
				return nil, nil, backendReject(s, "规则 JSON 无效")
			}
			if !regexp.MustCompile(`^[a-z][a-z0-9.-]+$`).MatchString(apText(r["id"])) || r["level"] != "mandatory" || !apContains([]string{"always", "change", "web", "wire", "domain", "application", "persistence", "mybatis", "pagination", "batch"}, apText(r["when"])) || !bytes.Contains(b, []byte(`<a id="`+apText(r["id"])+`"></a>`)) {
				return nil, nil, backendReject(s, "mandatory 规则/锚点无效")
			}
			r["skill"], r["rule_ref"], r["rule_digest"] = skill, ref+"#"+apText(r["id"]), safefs.Digest(b)
			rules = append(rules, r)
		}
	}
	rules = append(rules, map[string]any{"id": skill + ".full-text", "when": "always", "level": "mandatory", "evidence": "semantic-review", "skill": skill, "rule_ref": main, "rule_digest": safefs.Digest(raw)})
	return rules, sources, nil
}
func backendCoverageRows(s *semanticSession, root string, coverage map[string]any, value any) error {
	if len(apArray(coverage["issues"])) > 0 || len(apArray(coverage["findings"])) > 0 {
		return backendReject(s, "规范覆盖仍有 unresolved/violation")
	}
	rows, ok := value.([]any)
	if !ok {
		return backendReject(s, "constraint_results 必须数组")
	}
	standards := map[string]map[string]any{}
	for _, v := range rows {
		r := apMap(v)
		if r["axis"] != "Standards" {
			continue
		}
		id := apText(r["constraint_id"])
		if standards[id] != nil {
			return backendReject(s, "约束审查结果重复")
		}
		standards[id] = r
	}
	for _, expected := range apRows(coverage["constraints"]) {
		id := apText(expected["constraint_id"])
		r := standards[id]
		if r == nil {
			return backendReject(s, "缺少约束审查: "+id)
		}
		delete(standards, id)
		if r["skill"] != expected["skill"] || r["rule_ref"] != expected["rule_ref"] || r["rule_digest"] != expected["rule_digest"] || strings.TrimSpace(apText(r["constraint"])) == "" || !apEqual(r["applicability_basis"], expected["applicability_basis"]) {
			return backendReject(s, "约束归属/规则/适用依据不匹配")
		}
		if !apContains([]string{"passed", "not-applicable"}, apText(r["status"])) || r["status"] == "not-applicable" && (expected["applicability"] == "required" || strings.TrimSpace(apText(r["reason"])) == "") {
			return backendReject(s, "适用规则不能豁免")
		}
		if apText(r["code_ref"]) == "" {
			return backendReject(s, "缺少源码绑定")
		}
		if _, e := backendCodeBytes(s, root, strings.Split(apText(r["code_ref"]), ":")[0]); e != nil {
			return e
		}
		if _, e := backendBoundFlexible(s, map[string]any{"ref": r["evidence_ref"], "digest": r["evidence_digest"]}); e != nil {
			return e
		}
		if expected["evidence"] == "semantic-review" && len([]rune(strings.TrimSpace(apText(r["review_notes"])))) < 20 {
			return backendReject(s, "全文语义审查缺少具体理由")
		}
	}
	if len(standards) > 0 {
		return backendReject(s, "存在未登记约束审查")
	}
	return nil
}
