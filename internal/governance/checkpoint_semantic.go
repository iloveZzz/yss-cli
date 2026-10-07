package governance

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

func init() {
	registerSemanticValidator("checkpoint", verifyCheckpointSemantic)
	registerSemanticValidator("tracking-entry", verifyTrackingEntrySemantic)
	registerSemanticValidator("checkpoint-boundary", verifyCheckpointBoundarySemantic)
}
func semanticDefinition(registry map[string]any, bucket, id string) map[string]any {
	for _, row := range semList(registry[bucket]) {
		m := semMap(row)
		if text(m["id"]) == id {
			return m
		}
	}
	return nil
}
func (s *semanticSession) orchestration() (map[string]any, string, error) {
	ref := ".agents/skills/yss-product-lifecycle/references/orchestration-contract.yaml"
	if present, err := s.exists(".template-spec/process/harness-profile.yaml"); err != nil {
		return nil, "", err
	} else if present {
		profile, err := s.doc(".template-spec/process/harness-profile.yaml")
		if err != nil {
			return nil, "", err
		}
		preferred := ""
		for name, known := range domain.Profiles {
			if profile["profile_id"] == known.ID {
				preferred = guidanceContractRef(name)
			}
		}
		if preferred == "" {
			return nil, "", s.unavailable("IDENTITY", "无法识别当前 Profile 的主控合同")
		}
		if present, err := s.exists(preferred); err != nil {
			return nil, "", err
		} else if present {
			ref = preferred
		}
	}
	exists, err := s.exists(ref)
	if err != nil {
		return nil, "", err
	}
	if !exists {
		ref = ".template-spec/process/checkpoint-boundary.yaml"
	}
	doc, err := s.doc(ref)
	if err == nil {
		version, valid := integer(doc["schema_version"])
		if !valid || version != 1 && !(version == 2 && ref == guidanceContractRef("design")) {
			return nil, ref, s.unavailable("CAPABILITY", "未知本地主控合同版本")
		}
	}
	return doc, ref, err
}
func verifyCheckpointBoundarySemantic(s *semanticSession, ref string, opts map[string]string) error {
	cp, err := s.doc(ref)
	if err != nil {
		return err
	}
	policy, _, err := s.orchestration()
	if err != nil {
		return err
	}
	if semanticDefinition(s.registry, "stages", text(cp["stage"])) == nil {
		return s.reject("CHECKPOINT_BOUNDARY", "当前 stage 未登记")
	}
	if cp["next_work_unit"] != nil && semanticDefinition(s.registry, "work_units", text(cp["next_work_unit"])) == nil {
		return s.reject("CHECKPOINT_BOUNDARY", "下一工作单元未登记")
	}
	boundary := semMap(cp["phase_boundary"])
	trace := semMap(cp["stage_trace"])
	bpolicy := semMap(policy["phase_boundary"])
	at := semHas([]string{"paused-human-gate", "completed"}, text(cp["status"])) || semHas([]string{"handoff", "subagent", "compact"}, text(boundary["decision"])) || (semHas([]string{"orchestrate", "resume"}, text(cp["mode"])) && (text(cp["stage"]) != "stage.entry-triage" || text(trace["completed_work_unit"]) != ""))
	if at && (boundary == nil || trace == nil) {
		return s.reject("CHECKPOINT_BOUNDARY", "当前边界缺少 phase_boundary 或 stage_trace")
	}
	if cp["phase_boundary"] != nil {
		if boundary == nil {
			return s.reject("CHECKPOINT_BOUNDARY", "phase_boundary 必须为对象")
		}
		evidence := semMap(bpolicy["evidence"])
		for _, key := range semStrings(evidence["required"]) {
			if strings.TrimSpace(text(boundary[key])) == "" {
				return s.reject("CHECKPOINT_BOUNDARY", "缺少 phase_boundary."+key)
			}
		}
		if !semHas(semStrings(bpolicy["choices"]), text(boundary["decision"])) {
			return s.reject("CHECKPOINT_BOUNDARY", "边界决策未登记")
		}
		for _, key := range semStrings(semMap(evidence["conditional"])[text(boundary["decision"])]) {
			r := text(boundary[key])
			if strings.TrimSpace(r) == "" {
				return s.reject("CHECKPOINT_BOUNDARY", "缺少 "+key)
			}
			if strings.HasSuffix(key, "_ref") {
				if _, err = s.bytes(r); err != nil {
					return err
				}
			}
		}
		if boundary["decision"] == "compact" && semanticDefinition(s.registry, "stages", text(boundary["next_phase"])) == nil {
			return s.reject("CHECKPOINT_BOUNDARY", "compact next_phase 未登记")
		}
	}
	if cp["stage_trace"] != nil {
		if trace == nil {
			return s.reject("CHECKPOINT_BOUNDARY", "stage_trace 必须为对象")
		}
		for _, key := range semStrings(semMap(policy["checkpoint_policy"])["required_stage_trace_fields"]) {
			if key == "stage" {
				if trace[key] != cp["stage"] {
					return s.reject("CHECKPOINT_BOUNDARY", "stage_trace.stage 不一致")
				}
			} else {
				if _, ok := trace[key].([]any); !ok {
					return s.reject("CHECKPOINT_BOUNDARY", "stage_trace."+key+" 必须为数组")
				}
			}
		}
		if trace["completed_work_unit"] != nil && semanticDefinition(s.registry, "work_units", text(trace["completed_work_unit"])) == nil {
			return s.reject("CHECKPOINT_BOUNDARY", "completed_work_unit 未登记")
		}
		for _, key := range []string{"upstream_refs", "artifact_refs"} {
			for _, row := range semList(trace[key]) {
				r := text(row)
				if r == "" {
					r = text(semMap(row)["ref"])
				}
				if _, err = s.bytes(r); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func semanticCountersign(roles map[string]any, id string) (bool, error) {
	policy := semMap(roles["gate_policy"])
	for _, bucket := range []string{"check_reviews", "dual_digital_human", "digital_human_review"} {
		for _, row := range semList(policy[bucket]) {
			if text(semMap(row)["gate"]) == id {
				return true, nil
			}
		}
	}
	for _, bucket := range []string{"product_digital_human_with_biological_veto", "biological_human"} {
		if semHas(semStrings(policy[bucket]), id) {
			return true, nil
		}
	}
	for _, bucket := range []string{"evidence_only", "orchestrator", "automatic_checks"} {
		if semHas(semStrings(policy[bucket]), id) {
			return false, nil
		}
	}
	return false, fmt.Errorf("GATE_POLICY_REQUIRED: 未分类门禁或检查 %s", id)
}
func (s *semanticSession) gateChecks(cp map[string]any, ref, gateID string) error {
	gate := semanticDefinition(s.registry, "gates", gateID)
	if gate == nil {
		return s.reject("GATE_UNKNOWN", "未知门禁: "+gateID)
	}
	checks := semMap(cp["checks"])
	seen := map[string]bool{}
	active := map[string]bool{}
	var check func(string) error
	proofs := func(item map[string]any, definition map[string]any, id string) error {
		if err := s.basis(item["basis"]); err != nil {
			return err
		}
		refs := map[string]bool{}
		for _, row := range semList(item["basis"]) {
			refs[text(semMap(row)["ref"])] = true
		}
		for _, kind := range semStrings(definition["evidence"]) {
			rows := semStrings(semMap(item["evidence"])[kind])
			if len(rows) == 0 {
				return s.reject("GATE_EVIDENCE", "缺少绑定证据: "+id+"/"+kind)
			}
			for _, r := range rows {
				if !refs[r] {
					return s.reject("GATE_EVIDENCE", "证据未绑定当前依据: "+r)
				}
			}
		}
		required, err := semanticCountersign(s.roles, id)
		if err != nil {
			return s.unavailable("CAPABILITY", err.Error())
		}
		if required {
			if !refs[text(item["approval_ref"])] || !refs[text(item["subject_ref"])] {
				return s.reject("APPROVAL_CONTEXT_REQUIRED", "未绑定批准与主体: "+id)
			}
			if err = s.verify("approval", text(item["approval_ref"]), map[string]string{"checkpoint": ref, "boundary": id}); err != nil {
				return err
			}
		}
		return nil
	}
	check = func(id string) error {
		if active[id] {
			return s.reject("REFERENCE_CYCLE", "门禁依赖循环: "+id)
		}
		if seen[id] {
			return nil
		}
		definition := semanticDefinition(s.registry, "checks", id)
		item := semMap(checks[id])
		if definition == nil || item == nil {
			return s.reject("GATE_CHECK_REQUIRED", "缺少当前检查: "+id)
		}
		active[id] = true
		defer delete(active, id)
		if err := s.basis(item["basis"]); err != nil {
			return err
		}
		if item["status"] == "not-applicable" {
			if item["applicable"] != false || strings.TrimSpace(text(item["reason"])) == "" {
				return s.reject("GATE_APPLICABILITY", "不适用检查缺少评估原因: "+id)
			}
			seen[id] = true
			s.report.Applicability = append(s.report.Applicability, map[string]any{"id": id, "status": "not-applicable", "reason": item["reason"], "source_ref": ref})
			return nil
		}
		if item["applicable"] != true || !semHas([]string{"passed", "approved"}, text(item["status"])) {
			return s.reject("GATE_CHECK_BLOCKED", "当前检查未通过: "+id)
		}
		for _, dep := range semStrings(definition["requires_checks"]) {
			if err := check(dep); err != nil {
				return err
			}
			if semMap(checks[dep])["status"] == "not-applicable" {
				return s.reject("GATE_DEPENDENCY", "通过检查依赖了不适用检查: "+id)
			}
		}
		if err := proofs(item, definition, id); err != nil {
			return err
		}
		seen[id] = true
		return nil
	}
	for _, id := range semStrings(gate["requires_checks"]) {
		if err := check(id); err != nil {
			return err
		}
	}
	item := semMap(semMap(cp["gates"])[gateID])
	if item["status"] != "approved" {
		return s.reject("GATE_BLOCKED", "门禁未批准: "+gateID)
	}
	if gateID == "gate.plan-approved" {
		if err := s.verify("plan-aggregate", ref, map[string]string{}); err != nil {
			return err
		}
	}
	if err := proofs(item, gate, gateID); err != nil {
		return err
	}
	for id := range seen {
		for _, row := range semList(semMap(checks[id])["basis"]) {
			m := semMap(row)
			covered := false
			for _, binding := range semList(item["basis"]) {
				b := semMap(binding)
				if m["ref"] == b["ref"] && m["digest"] == b["digest"] {
					covered = true
				}
			}
			if !covered {
				return s.reject("GATE_COVERAGE", "聚合批准缺少检查依据: "+id+"/"+text(m["ref"]))
			}
		}
	}
	return nil
}
func verifyTrackingEntrySemantic(s *semanticSession, ref string, opts map[string]string) error {
	cp, err := s.doc(ref)
	if err != nil {
		return err
	}
	identity, err := s.doc("yss-project.yaml")
	if err != nil {
		return err
	}
	if identity["repository_mode"] == "template-source" {
		s.report.Applicability = append(s.report.Applicability, map[string]any{"id": "stage-tracking", "status": "not-applicable", "reason": "模板维护不创建产品阶段工作项"})
		return nil
	}
	workUnit := ""
	if text(cp["task_id"]) != "" {
		workUnit = text(cp["work_unit_id"])
		if trackedUnits[workUnit] == "" {
			return nil
		}
		checkpointRef := text(cp["checkpoint_ref"])
		if opts["checkpoint"] != "" && checkpointRef != opts["checkpoint"] {
			return s.reject("TRACKING", "任务 checkpoint 与消费者范围不一致")
		}
		if checkpointRef == "" {
			config, e := tracker(s.v)
			if e != nil {
				return e
			}
			version, _ := integer(config["lifecycle_tracking_version"])
			if version == 1 {
				return s.reject("TRACKING", "阶段任务缺少持久化 checkpoint 引用")
			}
			return nil
		}
		ref = checkpointRef
		cp, err = s.doc(ref)
		if err != nil {
			return err
		}
		if err = s.verify("checkpoint-boundary", ref, nil); err != nil {
			return err
		}
	}
	if cp["stage_tracking"] != nil {
		if err = s.validateSchema(trackingSchemaRef, cp["stage_tracking"]); err != nil {
			return err
		}
	}
	_, err = checkStage(s.v, cp, ref)
	if err != nil {
		return s.reject("TRACKING", err.Error())
	}
	if workUnit != "" {
		t, err := asTracking(cp)
		if err != nil {
			return err
		}
		if t == nil {
			return nil
		}
		index := map[string]WorkItem{}
		for _, item := range t.Items {
			index[item.ID] = item
		}
		runnable := false
		exists := false
		for _, item := range t.Items {
			if item.WorkUnit != workUnit {
				continue
			}
			exists = true
			if item.Progress == "blocked" || item.Progress == "cancelled" || item.Deferred != nil || item.RecheckRequired {
				continue
			}
			ready := true
			for _, dep := range item.Dependencies {
				if index[dep].Progress != "completed" {
					ready = false
				}
			}
			for _, binding := range item.SourceRefs {
				b, e := s.bind(binding.Ref)
				if e != nil || b.Digest != binding.Digest {
					ready = false
				}
			}
			for _, completion := range item.Completion {
				for _, binding := range completion.EvidenceRefs {
					b, e := s.bind(binding.Ref)
					if e != nil || b.Digest != binding.Digest {
						ready = false
					}
				}
			}
			if ready {
				runnable = true
			}
		}
		if !exists || !runnable {
			return s.reject("TRACKING", "阶段入口缺少可执行的当前工作项")
		}
	}
	return nil
}
func verifyCheckpointSemantic(s *semanticSession, ref string, opts map[string]string) error {
	cp, err := s.doc(ref)
	if err != nil {
		return err
	}
	if opts["history"] == "true" {
		return verifyHistoricalCheckpointSemantic(s, cp)
	}
	if err = s.validateSchema(".template-spec/process/schemas/lifecycle-checkpoint.schema.json", cp); err != nil {
		return err
	}
	if err = s.currentAsset(ref); err != nil {
		return err
	}
	identity, err := s.doc("yss-project.yaml")
	if err != nil {
		return err
	}
	if cp["repository_mode"] != identity["repository_mode"] {
		return s.reject("IDENTITY", "checkpoint 仓库身份与根合同矛盾")
	}
	for _, bucket := range []string{"gates", "checks", "artifacts"} {
		for id := range semMap(cp[bucket]) {
			if semanticDefinition(s.registry, bucket, id) == nil {
				return s.reject("IDENTITY", "未知或退役稳定ID: "+id)
			}
		}
	}
	for _, kind := range []string{"checkpoint-boundary", "tracking-entry"} {
		if err = s.verify(kind, ref, opts); err != nil {
			return err
		}
	}
	required := semHas([]string{"work-unit.technical-analysis", "work-unit.strategic-design-handoff", "work-unit.implementation-repository-preparation", "work-unit.ticket-decomposition", "work-unit.slice-implementation"}, text(cp["next_work_unit"]))
	if err = s.verify("business-checkpoint", ref, map[string]string{"required": fmt.Sprint(required)}); err != nil {
		return err
	}
	for id, row := range semMap(cp["gates"]) {
		if semMap(row)["status"] == "approved" {
			if err = s.gateChecks(cp, ref, id); err != nil {
				return err
			}
		}
	}
	if err = s.verify("plan-checkpoint", ref, opts); err != nil {
		return err
	}
	if err = s.verify("checkpoint-user-decisions", ref, opts); err != nil {
		return err
	}
	if err = s.verify("execution-scope", ref, opts); err != nil {
		return err
	}
	if text(semMap(cp["stage_trace"])["completed_work_unit"]) != "" {
		if err = s.verify("next-route", ref, opts); err != nil {
			return err
		}
	}
	recon := semMap(cp["context_reconciliation"])
	if cp["repository_mode"] == "project-instance" && semHas([]string{"paused-human-gate", "completed"}, text(cp["status"])) {
		if recon["status"] != "reconciled" || text(recon["ref"]) == "" {
			return s.reject("CONTEXT", "批准或完成前缺少当前 Context 对账")
		}
		if err = s.verify("context-reconciliation", text(recon["ref"]), opts); err != nil {
			return err
		}
	} else if cp["repository_mode"] == "template-source" && recon["status"] != "not-applicable" {
		return s.reject("CONTEXT", "模板维护 Context 必须带原因记录不适用")
	}
	return nil
}

// The retained historical contract changes exactly these two definitions. It
// cannot establish current ownership, approval, transition, or authorization.
func verifyHistoricalCheckpointSemantic(s *semanticSession, cp map[string]any) error {
	const ref = ".template-spec/process/schemas/lifecycle-checkpoint.schema.json"
	path, err := safefs.Path(s.root, ref)
	if err != nil {
		return err
	}
	issues, err := schema.ValidateValueWithReader(path, cp, func(file string) ([]byte, error) {
		local, err := filepath.Rel(s.root, file)
		if err != nil {
			return nil, err
		}
		bytes, err := s.bytes(filepath.ToSlash(local))
		if err != nil {
			return nil, err
		}
		if filepath.Clean(file) != filepath.Clean(path) {
			return bytes, nil
		}
		value, err := schema.Parse(bytes)
		if err != nil {
			return nil, err
		}
		doc := semMap(value)
		properties := semMap(doc["properties"])
		if properties["phase_boundary"] == nil || properties["stage_trace"] == nil {
			return nil, fmt.Errorf("历史结构适配不支持该本地 Schema")
		}
		properties["phase_boundary"] = map[string]any{"type": "object"}
		properties["stage_trace"] = map[string]any{"type": "object"}
		return json.Marshal(doc)
	})
	if err != nil {
		return s.unavailable("CAPABILITY", err.Error())
	}
	if len(issues) > 0 {
		return s.reject("SCHEMA", fmt.Sprintf("历史 checkpoint 结构拒绝: %v", issues))
	}
	s.report.Scope = "historical-structure"
	s.report.Applicability = append(s.report.Applicability, map[string]any{"id": "checkpoint", "historical_only": true, "current_approval_checked": false, "transition_checked": false})
	return nil
}
