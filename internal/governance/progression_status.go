package governance

import (
	"context"
	"path"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

// Target evaluation is deliberately independent of lifecycle status and Node
// writers. It consumes current evidence without issuing an execution permit.
func progressionRead(ctx context.Context, root, cpRef string) (map[string]any, error) {
	s := newSemanticSession(ctx, root, map[string]string{"checkpoint": cpRef})
	return progressionReadSession(s)
}

func progressionReadSession(s *semanticSession) (map[string]any, error) {
	root, cpRef := s.root, s.checkpointRef
	if err := projectIdentity(s.v); err != nil {
		return nil, err
	}
	profile, policy, err := progressionPolicy(s)
	if err != nil {
		return nil, err
	}
	configRef, cp, err := progressionLocation(s, cpRef)
	if err != nil {
		return nil, err
	}
	if cp["profile_id"] != nil && cp["profile_id"] != domain.Profiles[profile].ID {
		return nil, s.reject("IDENTITY", "checkpoint Profile 与工程不符")
	}
	p := map[string]any{"enabled": true, "root": root, "profile": profile, "feature_id": cp["feature_id"], "checkpoint_ref": cpRef, "config_ref": configRef, "config_digest": nil, "target": policy["default_target"], "target_source": "profile-default", "intent_source": "当前Profile默认职责终点", "status": "pending", "read_only": true, "approval_created": false, "execution_authorization": "not-evaluated"}
	responsibilityTarget := "profile-terminal"
	target := ProgressionTarget{FeatureID: text(cp["feature_id"]), CheckpointRef: cpRef, Consumers: []ProgressionConsumer{}}
	if profile == "spec" {
		p["intent_source"] = "Spec默认完成完整业务验收"
		responsibilityTarget = "business-accepted"
		scope, e := s.executionScope()
		if e != nil {
			return nil, e
		}
		if scope != nil {
			p["target"], p["target_source"] = "backend-deliverable", "execution-scope-terminal"
			responsibilityTarget = "backend-deliverable"
			p["intent_source"] = "既有plan-to-backend职责终点"
		}
	} else {
		p["target"], p["target_source"] = "profile-terminal", "profile-terminal"
	}
	p["profile_terminal_target"] = responsibilityTarget
	present, err := s.exists(configRef)
	if err != nil {
		return nil, err
	}
	if present {
		raw, e := s.bytes(configRef)
		if e != nil {
			return nil, e
		}
		target, e = parseProgressionTarget(raw)
		if e != nil {
			return nil, e
		}
		if target.FeatureID != text(cp["feature_id"]) || target.CheckpointRef != cpRef || profile != "spec" {
			return nil, s.reject("PROGRESSION_BINDING", "目标配置未绑定当前 Spec 功能 checkpoint")
		}
		if e = progressionScope(s, target.Target); e != nil {
			return nil, e
		}
		p["config_digest"], p["target"], p["target_source"] = "sha256:"+safefs.Digest(raw), target.Target, "feature-intent"
		p["intent_source"] = target.IntentSource
	}
	target.Target = text(p["target"])
	for ref, key := range map[string]string{cpRef: "checkpoint_digest", guidanceContractRef(profile): "contract_digest"} {
		raw, e := s.bytes(ref)
		if e != nil {
			return nil, e
		}
		p[key] = "sha256:" + safefs.Digest(raw)
	}
	if profile == "spec" {
		materials, err := progressionIntentMaterials(s, cpRef, configRef, cp)
		if err != nil {
			return nil, err
		}
		p["intent_materials"] = materials
	}
	coordination, participants := progressionConsumers(s, profile, cpRef, cp, target)
	policyRow := semMap(semMap(policy["completion_policy"])[target.Target])
	status, reason := "pending", "等待当前目标的批准与交付证据"
	if profile != "spec" {
		status, reason = progressionProfileTerminal(s, profile, cpRef, cp)
	} else if len(policyRow) == 0 {
		status, reason = "blocked", "当前政策缺少所选目标的完成规则"
	} else if blockers, ok := cp["blockers"].([]any); !ok || len(blockers) > 0 {
		status, reason = "blocked", "checkpoint 缺少有效无阻塞事实"
	} else {
		owner := map[string]string{"product-design-completed": "design", "backend-deliverable": "backend", "frontend-accepted": "frontend"}[target.Target]
		declaredOwner := false
		for _, c := range target.Consumers {
			if c.Profile == owner {
				declaredOwner = true
			}
		}
		if participant := participants[owner]; participant != nil {
			state := semMap(coordination[owner])
			if state["input_status"] != "verified" {
				status, reason = "pending", "独立消费者的当前来源接收尚未核验"
				if state["input_status"] == "blocked" {
					status, reason = "blocked", text(state["reason"])
				}
			} else {
				localRef := participant.checkpointRef
				localCP, e := participant.doc(localRef)
				if e != nil {
					status, reason = "blocked", e.Error()
				} else if owner == "design" {
					_, localPolicy, e := progressionPolicy(participant)
					if e != nil {
						status, reason = "blocked", e.Error()
					} else {
						status, reason = progressionMilestone(participant, localRef, localCP, target.Target, semMap(semMap(localPolicy["completion_policy"])[target.Target]), target)
					}
				} else {
					status, reason = progressionProfileTerminal(participant, owner, localRef, localCP)
				}
			}
		} else if declaredOwner {
			status, reason = "pending", "等待显式消费者当前checkpoint及来源接收："+owner
			if semMap(coordination[owner])["input_status"] == "blocked" {
				status, reason = "blocked", text(semMap(coordination[owner])["reason"])
			}
		} else {
			status, reason = progressionMilestone(s, cpRef, cp, target.Target, policyRow, target)
		}
		// An explicit implementation handoff is part of the selected goal;
		// consumers may continue after it under their own registered contracts.
		if (status == "reached" || status == "not-applicable") && semMap(policyRow["conditional_required_checks"])["strategic-handoff-delivery"] == "explicit-external-implementation-consumer" && progressionExternalImplementation(target, root) {
			source, sourceCP := s, cp
			if design := participants["design"]; design != nil {
				source = design
				sourceCP, err = design.doc(design.checkpointRef)
			}
			if err == nil {
				err = progressionCheck(source, source.checkpointRef, sourceCP, "strategic-handoff-delivery")
			}
			if err != nil {
				status, reason = "pending", err.Error()
			}
		}
		if target.Target == "business-accepted" && status == "reached" {
			status, reason = progressionBusinessCompletion(s, cpRef, cp, policyRow, target, coordination, participants)
		}
	}
	p["status"], p["reason"] = status, reason
	terminalStatus, terminalReason := status, reason
	if profile == "spec" && target.Target != responsibilityTarget {
		if blockers, ok := cp["blockers"].([]any); !ok || len(blockers) > 0 {
			terminalStatus, terminalReason = "blocked", "当前 Profile 缺少有效无阻塞事实"
		} else {
			terminalStatus, terminalReason = progressionMilestone(s, cpRef, cp, responsibilityTarget, semMap(semMap(policy["completion_policy"])[responsibilityTarget]), target)
		}
	}
	if profile == "spec" && target.Target != responsibilityTarget && responsibilityTarget == "business-accepted" && terminalStatus == "reached" {
		terminalStatus, terminalReason = progressionBusinessCompletion(s, cpRef, cp, semMap(semMap(policy["completion_policy"])[responsibilityTarget]), target, coordination, participants)
	}
	p["completion"] = map[string]any{"stage": map[string]any{"status": "not-evaluated", "stage": cp["stage"]}, "milestone": map[string]any{"status": status, "target": target.Target}, "profile": map[string]any{"status": terminalStatus, "reason": terminalReason}, "business": map[string]any{"status": "not-evaluated"}}
	if stageStatus, stageReason := progressionStage(s, cpRef, cp); stageStatus != "not-evaluated" {
		semMap(p["completion"])["stage"] = map[string]any{"status": stageStatus, "stage": cp["stage"], "reason": stageReason}
	}
	if status == "not-applicable" && cp["stage"] == "stage.product-design" && target.Target == "product-design-completed" {
		semMap(p["completion"])["stage"] = map[string]any{"status": status, "stage": cp["stage"], "reason": reason}
	}
	if profile == "spec" && responsibilityTarget == "business-accepted" {
		semMap(p["completion"])["business"] = map[string]any{"status": terminalStatus, "reason": terminalReason}
	}
	// Read all dispatch qualifications before the final input observation. A
	// waiting consumer is actionable only after its actual source is ready.
	await := map[string]any{}
	if status == "pending" {
		for _, c := range target.Consumers {
			row := semMap(coordination[c.Profile])
			if (row["input_status"] != "verified" || row["completion_status"] != "reached") && progressionConsumerAwaitable(s, profile, cpRef, cp, target, c, coordination, participants) {
				await = map[string]any{"profile": c.Profile, "root": c.Root, "checkpoint_ref": c.CheckpointRef, "reason": row["reason"], "work_unit": nil}
				if participant := participants[c.Profile]; participant != nil {
					local, _ := participant.doc(c.CheckpointRef)
					await["work_unit"] = local["next_work_unit"]
				}
				break
			}
		}
	}
	if e := s.finish(); e != nil {
		p["status"], p["reason"], p["inputs_current"] = "blocked", e.Error(), false
		for _, layer := range semMap(p["completion"]) {
			item := semMap(layer)
			item["status"], item["reason"] = "blocked", e.Error()
		}
		for _, row := range coordination {
			item := semMap(row)
			item["input_status"], item["completion_status"], item["reason"] = "blocked", "blocked", e.Error()
		}
	} else {
		p["inputs_current"] = true
	}
	p["input_digests"] = s.inputs()
	p["reached"] = p["inputs_current"] == true && (p["status"] == "reached" || p["status"] == "not-applicable")
	next := map[string]any{"kind": "continue", "work_unit": cp["next_work_unit"], "root_work_unit": cp["next_work_unit"], "profile": profile, "root": root, "checkpoint_ref": cpRef, "reason": p["reason"], "execution_authorization": "not-evaluated"}
	if p["status"] == "reached" || p["status"] == "not-applicable" {
		next["kind"] = "target-reached"
	} else if p["status"] == "blocked" {
		next["kind"] = "verify-current-inputs"
	} else if len(await) > 0 {
		next["kind"] = "await-consumer"
		for key, value := range await {
			next[key] = value
		}
	}
	return map[string]any{"action": "target", "read_only": true, "progression": p, "coordination": coordination, "next_action": next}, nil
}

func progressionConsumerAwaitable(s *semanticSession, profile, cpRef string, cp map[string]any, target ProgressionTarget, consumer ProgressionConsumer, coordination map[string]any, participants map[string]*semanticSession) bool {
	required := map[string][]string{"product-design-completed": {"design"}, "backend-deliverable": {"design", "backend"}, "frontend-accepted": {"design", "backend", "frontend"}, "business-accepted": {"design", "backend", "frontend"}}
	if !semHas(required[target.Target], consumer.Profile) {
		return false
	}
	row := semMap(coordination[consumer.Profile])
	if row["input_status"] == "verified" {
		row["source_ready"] = true
		return true
	}
	if row["input_status"] == "blocked" {
		return false
	}
	if consumer.Profile == "design" {
		if profile != "spec" {
			return false
		}
		_, _, err := verifySpecBaselineSource(s, cpRef)
		row["source_ready"] = err == nil
		return err == nil
	}
	source, sourceRef, sourceCP := s, cpRef, cp
	if design := participants["design"]; design != nil {
		if semMap(coordination["design"])["input_status"] != "verified" {
			return false
		}
		source, sourceRef = design, design.checkpointRef
		sourceCP, _ = source.doc(sourceRef)
	}
	if consumer.Profile == "frontend" {
		for _, c := range target.Consumers {
			if c.Profile != "backend" {
				continue
			}
			backend := participants["backend"]
			if backend == nil || semMap(coordination["backend"])["input_status"] != "verified" {
				return false
			}
			a, err := progressionBackendAuthorization(backend, backend.checkpointRef, "", true)
			if err == nil {
				err = backend.verify("backend-terminal", a.TerminalRef, map[string]string{"checkpoint": backend.checkpointRef})
			}
			row["source_ready"] = err == nil
			return err == nil
		}
	}
	err := progressionCheck(source, sourceRef, sourceCP, "strategic-handoff-delivery")
	row["source_ready"] = err == nil
	return err == nil
}

// A real root acceptance cannot complete the business while a declared
// participant consumes stale inputs or has unfinished local responsibilities.
func progressionBusinessQualification(status, reason string, consumers []ProgressionConsumer, coordination map[string]any) (string, string) {
	if status != "reached" {
		return status, reason
	}
	for _, c := range consumers {
		row := semMap(coordination[c.Profile])
		if row["input_status"] == "blocked" {
			return "blocked", text(row["reason"])
		}
		if row["input_status"] != "verified" || row["completion_status"] != "reached" {
			return "pending", "整业务职责等待显式消费者当前输入与本端验收：" + c.Profile
		}
	}
	return status, reason
}

// A delivery acceptance cannot substitute a strategic-package report for the
// implementation declared by the current approved source. The policy selects
// these checks; their evidence remains owned by the existing terminal readers.
func progressionBusinessCompletion(s *semanticSession, cpRef string, cp, policy map[string]any, target ProgressionTarget, coordination map[string]any, participants map[string]*semanticSession) (string, string) {
	status, reason := progressionBusinessQualification("reached", "当前批准、依据与交付检查已核验", target.Consumers, coordination)
	if status != "reached" {
		return status, reason
	}
	conditional := semMap(policy["conditional_required_checks"])
	if conditional["backend-delivery"] == nil || conditional["frontend-implementation"] == nil {
		return "blocked", "业务完成政策缺少当前后端及前端适用性规则"
	}
	if _, _, err := verifySpecBaselineSource(s, cpRef); err != nil {
		return "blocked", err.Error()
	}
	stageRef := checkpointAsset(cp, "stage_decision_package_ref", "artifact.stage-decision-package")
	stage, err := s.doc(stageRef)
	if err != nil {
		return "blocked", err.Error()
	}
	impact := semMap(stage["impact_assessment"])
	for _, key := range []string{"backend", "api", "data", "frontend"} {
		if _, ok := impact[key].(bool); !ok {
			return "blocked", "当前批准影响评估未明确实现适用性：" + key
		}
		if semMap(cp["delivery_impacts"])[key] == true && impact[key] == false {
			return "blocked", "checkpoint 实现影响与当前批准影响评估冲突：" + key
		}
	}
	backendRequired := impact["backend"] == true || impact["api"] == true || impact["data"] == true
	if condition := conditional["backend-delivery"]; condition != nil {
		if condition != "backend-api-data-impact" {
			return "blocked", "未知业务后端完成条件"
		}
		if backendRequired && participants["backend"] == nil {
			if err := backendBusinessLocalEvidence(s, cpRef); err != nil {
				return "blocked", err.Error()
			}
		}
	}
	if condition := conditional["frontend-implementation"]; condition != nil {
		if condition != "frontend-impact" {
			return "blocked", "未知业务前端完成条件"
		}
		check := semMap(semMap(cp["checks"])["check.frontend-implementation-verified"])
		if check["status"] == "not-applicable" {
			if impact["frontend"] != false || semMap(cp["delivery_impacts"])["frontend"] == true || check["applicable"] != false || text(check["reason"]) == "" {
				return "blocked", "前端不适用与当前批准实现范围冲突"
			}
			bound := false
			for _, row := range semList(check["basis"]) {
				if semMap(row)["ref"] == stageRef {
					bound = true
				}
			}
			if !bound {
				return "blocked", "前端不适用未绑定当前批准影响评估"
			}
			if err := s.basis(check["basis"]); err != nil {
				return "blocked", err.Error()
			}
			// A documentation-only scope has no future implementation Slice. A
			// backend implementation, however, must explicitly exclude frontend
			// work in its current approved Slice, local or jointly qualified.
			owners := []*semanticSession{s}
			if backend := participants["backend"]; backend != nil {
				owners = append(owners, backend)
			}
			checked := false
			for _, owner := range owners {
				ownerRef, ownerCP := cpRef, cp
				if owner != s {
					ownerRef = owner.checkpointRef
					ownerCP, err = owner.doc(ownerRef)
					if err != nil {
						return "blocked", err.Error()
					}
				}
				if progressionSliceRef(ownerCP) == "" {
					continue
				}
				checked = true
				na, err := progressionSliceNoFrontend(owner, ownerRef, ownerCP)
				if err != nil {
					return "blocked", err.Error()
				}
				if !na {
					return "blocked", "当前批准Slice要求前端实现，不能以不适用完成业务"
				}
			}
			if backendRequired && !checked {
				return "blocked", "前端不适用缺少当前批准Slice"
			}
		} else if semHas([]string{"passed", "approved"}, text(check["status"])) {
			if participants["frontend"] == nil {
				if err := progressionCheck(s, cpRef, cp, "frontend-implementation"); err != nil {
					return "blocked", err.Error()
				}
			}
		} else {
			return "blocked", "缺少当前实际前端实现验收或可信不适用依据"
		}
	}
	return status, reason
}

func progressionExternalImplementation(t ProgressionTarget, root string) bool {
	for _, c := range t.Consumers {
		if (c.Profile == "backend" || c.Profile == "frontend") && c.Root != root {
			return true
		}
	}
	return false
}

func progressionMilestone(s *semanticSession, cpRef string, cp map[string]any, target string, policy map[string]any, intent ProgressionTarget) (string, string) {
	if len(semStrings(policy["required_gates"])) == 0 || len(semStrings(policy["required_checks"])) == 0 {
		return "blocked", "目标政策缺少可复算的门禁或交付检查"
	}
	for _, id := range semStrings(policy["required_gates"]) {
		gate := semMap(semMap(cp["gates"])[id])
		if len(gate) == 0 || gate["status"] != "approved" && gate["status"] != "not-applicable" {
			return "pending", "等待当前门禁：" + id
		}
	}
	if err := s.authorities(); err != nil {
		return "blocked", err.Error()
	}
	na := false
	for _, id := range semStrings(policy["required_gates"]) {
		gate := semMap(semMap(cp["gates"])[id])
		if gate["status"] == "not-applicable" {
			if semMap(policy["conditional_gates"])[id] != "product-design-impact" || gate["applicable"] != nil && gate["applicable"] != false || text(gate["reason"]) == "" {
				return "blocked", "不适用门禁缺少政策、原因与依据"
			}
			stageRef := checkpointAsset(cp, "stage_decision_package_ref", "artifact.stage-decision-package")
			if stageRef == "" {
				return "blocked", "不适用产品设计缺少已绑定影响评估"
			}
			stage, err := s.doc(stageRef)
			if err != nil {
				return "blocked", err.Error()
			}
			impact := semMap(stage["impact_assessment"])
			if impact["product_design"] == true || impact["ui"] == true || impact["product_design"] != false && impact["ui"] != false || semMap(cp["delivery_impacts"])["ui"] == true {
				return "blocked", "产品设计不适用与当前影响评估冲突"
			}
			bound := false
			for _, row := range semList(gate["basis"]) {
				if semMap(row)["ref"] == stageRef {
					bound = true
				}
			}
			if !bound {
				return "blocked", "不适用门禁未绑定当前影响评估"
			}
			if err = s.basis(gate["basis"]); err != nil {
				return "blocked", err.Error()
			}
			noUI, err := progressionNoUI(s, cpRef, cp)
			if err != nil {
				return "blocked", err.Error()
			}
			if !noUI {
				return "blocked", "当前批准Spec基线存在产品设计影响"
			}
			na = true
			continue
		}
		if err := s.gateChecks(cp, cpRef, id); err != nil {
			return "blocked", err.Error()
		}
	}
	frontendNA := false
	if target == "frontend-accepted" && policy["no_ui_presentation"] == "not-applicable-with-current-impact-evidence" {
		noFrontend, err := progressionNoFrontend(s, cpRef, cp)
		if err != nil {
			return "blocked", err.Error()
		}
		frontendNA = noFrontend
	}
	for _, check := range semStrings(policy["required_checks"]) {
		if frontendNA && check == "frontend-implementation" {
			continue
		}
		if err := progressionCheck(s, cpRef, cp, check); err != nil {
			return "blocked", err.Error()
		}
	}
	if na && target == "product-design-completed" {
		return "not-applicable", "当前影响评估确认产品设计不适用；适用前置条件已核验"
	}
	if frontendNA {
		return "not-applicable", "当前批准影响评估及Slice确认前端实现不适用；适用前置门禁已核验"
	}
	return "reached", "当前批准、依据与交付检查已核验"
}

func progressionNoFrontend(s *semanticSession, cpRef string, cp map[string]any) (bool, error) {
	if _, err := progressionNoUI(s, cpRef, cp); err != nil {
		return false, err
	}
	stageRef := checkpointAsset(cp, "stage_decision_package_ref", "artifact.stage-decision-package")
	if stageRef == "" {
		return false, s.reject("APPLICABILITY", "前端不适用缺少当前批准影响评估")
	}
	stage, err := s.doc(stageRef)
	if err != nil {
		return false, err
	}
	if semMap(stage["impact_assessment"])["frontend"] != false || semMap(cp["delivery_impacts"])["frontend"] == true || semMap(cp["delivery_impacts"])["ui"] == true {
		return false, nil
	}
	return progressionSliceNoFrontend(s, cpRef, cp)
}

func progressionSliceNoFrontend(s *semanticSession, cpRef string, cp map[string]any) (bool, error) {
	sliceRef := progressionSliceRef(cp)
	if sliceRef == "" {
		return false, s.reject("APPLICABILITY", "前端不适用缺少当前批准Slice")
	}
	if sliceRef != semMap(semMap(cp["gates"])["gate.slice-contract-approved"])["subject_ref"] {
		return false, s.reject("APPLICABILITY", "前端不适用Slice不是当前门禁批准资产")
	}
	if err := s.gateChecks(cp, cpRef, "gate.slice-contract-approved"); err != nil {
		return false, err
	}
	c, err := loadNativeSlice(s, sliceRef)
	if err != nil {
		return false, err
	}
	if c.Normalized["status"] != "approved" || semMap(c.Normalized["frontend"])["status"] != "not-applicable" {
		return false, nil
	}
	return true, nil
}

func progressionSliceRef(cp map[string]any) string {
	return first(text(semMap(semMap(cp["human_review"])["implementation"])["slice_contract_ref"]), checkpointAsset(cp, "slice_contract_ref", "artifact.slice-implementation-contract"), text(semMap(semMap(cp["gates"])["gate.slice-contract-approved"])["subject_ref"]))
}

func progressionNoUI(s *semanticSession, cpRef string, cp map[string]any) (bool, error) {
	if cp["upstream_spec_baseline"] != nil {
		if err := verifyInheritedSpecCheckpoint(s, cpRef, cp); err != nil {
			return false, err
		}
		manifest, _, err := verifySpecBaselineReceipt(s, text(semMap(cp["upstream_spec_baseline"])["receipt_ref"]), true)
		return err == nil && semMap(manifest["source"])["product_design_required"] == false, err
	}
	source, _, err := verifySpecBaselineSource(s, cpRef)
	return err == nil && source["product_design_required"] == false, err
}

func progressionCheck(s *semanticSession, cpRef string, cp map[string]any, check string) error {
	switch check {
	case "business-tickets-draft", "business-tickets-formal":
		ref := checkpointAsset(cp, "business_ticket_set_ref", "artifact.business-ticket-set")
		if ref == "" {
			return s.reject("BUSINESS_TICKETS", "缺少当前业务票集合")
		}
		mode := "draft"
		if check == "business-tickets-formal" {
			mode = "formal"
		}
		set, err := contractBusinessTicketsMode(s, ref, mode)
		if err != nil {
			return err
		}
		spec := checkpointAsset(cp, "spec_ref", "artifact.spec")
		if spec != "" && semMap(set["spec"])["ref"] != spec {
			return s.reject("PROGRESSION_BINDING", "业务票没有绑定当前 Spec")
		}
		if check == "business-tickets-draft" && cp["upstream_spec_baseline"] == nil {
			_, _, err = verifySpecBaselineSource(s, cpRef)
		}
		return err
	case "inherited-spec-baseline":
		return verifyInheritedSpecCheckpoint(s, cpRef, cp)
	case "backend-delivery":
		a, err := progressionBackendAuthorization(s, cpRef, "", true)
		if err != nil {
			return err
		}
		return s.verify("backend-terminal", a.TerminalRef, map[string]string{"checkpoint": cpRef})
	case "frontend-implementation":
		local, err := localFrontendImplementationSelected(s, cpRef, cp)
		if err != nil {
			return err
		}
		if local {
			return progressionFrontendImplementationCompletion(s, cpRef, cp)
		}
		ref := guidanceArtifact(cp, "artifact.frontend-delivery-acceptance", "frontend_delivery_ref", "acceptance_ref")
		if ref == "" {
			return s.reject("FRONTEND_DELIVERY_REQUIRED", "缺少当前前端交付接收合同")
		}
		d, err := s.doc(ref)
		if err != nil {
			return err
		}
		return s.verify("frontend-delivery", ref, map[string]string{"slice": text(d["slice_id"]), "phase": "implementation", "checkpoint": cpRef})
	case "business-acceptance":
		refs := semStrings(semMap(semMap(semMap(cp["gates"])["gate.delivery-accepted"])["evidence"])["evidence.fresh-verification"])
		if len(refs) == 0 {
			return s.reject("VERIFICATION_REQUIRED", "业务完成缺少当前实际验证记录")
		}
		for _, ref := range refs {
			if err := s.verify("verification", ref, map[string]string{"checkpoint": cpRef}); err != nil {
				return err
			}
		}
		return nil
	case "strategic-handoff-delivery":
		ref, present, err := guidanceStrategicDeliveryRef(s, cp)
		if err != nil {
			return err
		}
		if !present {
			ref = guidanceArtifact(cp, "artifact.strategic-handoff-delivery", "strategic_delivery_ref")
		}
		if ref == "" {
			return s.reject("WAITING_PROFILE_INPUT", "等待当前战略交接交付包")
		}
		_, _, err = contractOpenStrategicDelivery(s, ref)
		return err
	default:
		return s.unavailable("CAPABILITY", "未知目标完成检查："+check)
	}
}

func progressionProfileTerminal(s *semanticSession, profile, cpRef string, cp map[string]any) (string, string) {
	if blockers, ok := cp["blockers"].([]any); !ok || len(blockers) > 0 {
		return "blocked", "当前 Profile 缺少有效无阻塞事实"
	}
	if profile == "spec" {
		_, policy, err := progressionPolicy(s)
		if err != nil {
			return "blocked", err.Error()
		}
		target := "business-accepted"
		scope, err := s.executionScope()
		if err != nil {
			return "blocked", err.Error()
		}
		if scope != nil {
			target = "backend-deliverable"
		}
		return progressionMilestone(s, cpRef, cp, target, semMap(semMap(policy["completion_policy"])[target]), ProgressionTarget{})
	}
	_, policy, err := progressionPolicy(s)
	if err != nil {
		return "blocked", err.Error()
	}
	return progressionMilestone(s, cpRef, cp, "profile-terminal", semMap(semMap(policy["completion_policy"])["profile-terminal"]), ProgressionTarget{})
}

func progressionConsumers(s *semanticSession, profile, cpRef string, cp map[string]any, target ProgressionTarget) (map[string]any, map[string]*semanticSession) {
	rows, sessions := map[string]any{}, map[string]*semanticSession{}
	for _, c := range target.Consumers {
		row := map[string]any{"profile": c.Profile, "root": c.Root, "checkpoint_ref": c.CheckpointRef, "input_status": "pending", "completion_status": "not-evaluated"}
		rows[c.Profile] = row
		if c.Root == s.root {
			row["input_status"], row["reason"] = "blocked", "消费者不能指向主控工程"
			continue
		}
		child := newSemanticSession(s.ctx, c.Root, map[string]string{"checkpoint": c.CheckpointRef})
		s.children = append(s.children, child)
		present, err := child.exists(c.CheckpointRef)
		if err != nil || !present {
			row["reason"] = "等待明确目标 checkpoint"
			continue
		}
		_, localCP, err := progressionLocation(child, c.CheckpointRef)
		if err == nil {
			p, e := child.doc(".template-spec/process/harness-profile.yaml")
			err = e
			if err == nil && (localCP["feature_id"] != cp["feature_id"] || p["profile_id"] != domain.Profiles[c.Profile].ID || localCP["profile_id"] != nil && localCP["profile_id"] != p["profile_id"]) {
				err = child.reject("PROGRESSION_BINDING", "消费者 feature/Profile 不匹配")
			}
		}
		if err != nil {
			row["input_status"], row["reason"] = "blocked", err.Error()
			continue
		}
		sessions[c.Profile] = child
		row["completion_status"], row["completion_reason"] = progressionProfileTerminal(child, c.Profile, c.CheckpointRef, localCP)
	}
	for _, name := range []string{"design", "backend", "frontend"} {
		var c ProgressionConsumer
		for _, candidate := range target.Consumers {
			if candidate.Profile == name {
				c = candidate
			}
		}
		if c.Profile == "" {
			continue
		}
		row := semMap(rows[c.Profile])
		if sessions[c.Profile] == nil {
			continue
		}
		source, sourceProfile, sourceRef, sourceCP := s, profile, cpRef, cp
		if c.Profile == "backend" || c.Profile == "frontend" {
			if design := sessions["design"]; design != nil {
				if semMap(rows["design"])["input_status"] != "verified" {
					row["reason"] = "先核验独立 Design 的当前来源接收"
					continue
				}
				source, sourceProfile, sourceRef = design, "design", design.checkpointRef
				sourceCP, _ = design.doc(sourceRef)
			}
		}
		if c.Profile == "frontend" {
			if backend := sessions["backend"]; backend != nil {
				if semMap(rows["backend"])["input_status"] != "verified" {
					row["reason"] = "先核验显式后端的当前来源接收"
					continue
				}
				source, sourceProfile, sourceRef = backend, "backend", backend.checkpointRef
				sourceCP, _ = backend.doc(sourceRef)
			}
		}
		engineering, _ := guidanceEngineering(c.Root, c.Profile)
		status, reason, _ := guidanceTargetInputSession(s.ctx, source, sourceProfile, sourceRef, sourceCP, c.Root, c.Profile, engineering, sessions[c.Profile])
		if status == "verified" {
			row["input_status"] = "verified"
			row["input"] = sessions[c.Profile].profileInput
		} else if status == "blocked" {
			row["input_status"] = "blocked"
		} else {
			row["input_status"] = "pending"
		}
		row["reason"] = reason
		if row["completion_status"] == "reached" {
			local, _ := sessions[c.Profile].doc(c.CheckpointRef)
			progressionConsumerEvidence(sessions[c.Profile], c, local, row)
		}
	}
	return rows, sessions
}

func progressionConsumerEvidence(s *semanticSession, c ProgressionConsumer, cp, row map[string]any) {
	refs := map[string]any{}
	if c.Profile == "backend" {
		a, err := progressionBackendAuthorization(s, c.CheckpointRef, "", true)
		if err == nil {
			terminal, e := s.doc(a.TerminalRef)
			if e == nil {
				binding := semMap(terminal["delivery"])
				delivery, e := s.doc(text(binding["ref"]))
				if e == nil {
					refs["delivery"] = map[string]any{"ref": binding["ref"], "digest": binding["digest"], "terminal_ref": a.TerminalRef, "delivery_id": delivery["delivery_id"], "version": delivery["version"], "slice_id": semMap(delivery["scope"])["slice_id"], "bundle_ref": terminal["bundle_ref"], "bundle_digest": terminal["bundle_digest"], "verification": delivery["verification"]}
				}
			}
		}
	} else if c.Profile == "frontend" {
		ref := checkpointAsset(cp, "frontend_delivery_ref", "artifact.frontend-delivery-acceptance")
		if ref == "" {
			ref = first(text(cp["acceptance_ref"]), text(semMap(cp["verification"])["acceptance_ref"]))
		}
		if ref != "" {
			doc, err := s.doc(ref)
			raw, e := s.bytes(ref)
			if err == nil && e == nil {
				refs["acceptance"] = map[string]any{"ref": ref, "digest": "sha256:" + safefs.Digest(raw), "slice_id": doc["slice_id"], "backend_delivery": doc["backend_delivery"], "implementation": doc["implementation"], "strategic_handoff": doc["strategic_handoff"]}
			}
		}
	} else if c.Profile == "design" {
		ref, _, err := guidanceStrategicDeliveryRef(s, cp)
		if err == nil && ref != "" {
			record, e := s.doc(ref)
			raw, err := s.bytes(ref)
			if e == nil && err == nil {
				refs["delivery"] = map[string]any{"ref": ref, "digest": "sha256:" + safefs.Digest(raw), "bundle_digest": record["bundle_digest"], "verification": semMap(cp["verification"])["strategic_delivery"]}
			}
		}
	}
	_, policy, err := progressionPolicy(s)
	if err == nil {
		gates := []any{}
		for _, id := range semStrings(semMap(semMap(policy["completion_policy"])["profile-terminal"])["required_gates"]) {
			gate := semMap(semMap(cp["gates"])[id])
			gates = append(gates, map[string]any{"gate_id": id, "approval_ref": gate["approval_ref"], "subject_ref": gate["subject_ref"], "basis": gate["basis"], "evidence": gate["evidence"]})
		}
		refs["acceptance_basis"] = gates
	}
	row["evidence"] = refs
}

func isProgressionEvidence(ref string) bool {
	return strings.EqualFold(path.Base(ref), progressionFile)
}

func progressionStage(s *semanticSession, cpRef string, cp map[string]any) (string, string) {
	if cp["status"] != "completed" && cp["status"] != "paused-human-gate" {
		return "pending", "当前阶段未到已登记退出边界"
	}
	if err := s.authorities(); err != nil {
		return "blocked", err.Error()
	}
	found := false
	for _, row := range semList(s.registry["gates"]) {
		gate := semMap(row)
		if gate["stage"] != cp["stage"] {
			continue
		}
		item := semMap(semMap(cp["gates"])[text(gate["id"])])
		if item["status"] == "approved" {
			if err := s.gateChecks(cp, cpRef, text(gate["id"])); err != nil {
				return "blocked", err.Error()
			}
			found = true
		} else if item["status"] == "not-applicable" {
			return "not-evaluated", "当前阶段含条件门禁，须由对应目标政策核验"
		} else {
			return "pending", "当前阶段门禁未闭合：" + text(gate["id"])
		}
	}
	if !found {
		return "not-evaluated", "当前阶段无可独立复算的退出门禁"
	}
	return "reached", "当前阶段适用门禁的当前批准已核验"
}

func rejectProgressionEvidence(s *semanticSession, ref string) error {
	if isProgressionEvidence(ref) {
		return s.reject("PROGRESSION_EVIDENCE", "推进目标意图不得作为批准或交接证据："+ref)
	}
	return nil
}

type backendAuthorization struct{ Mode, CheckpointRef, TerminalRef string }

// A feature target permits one delivery work unit; it never widens a scope or
// makes another feature inherit this feature's terminal record.
func progressionBackendAuthorization(s *semanticSession, cpRef, terminalRef string, readOnly bool) (backendAuthorization, error) {
	if err := projectIdentity(s.v); err != nil {
		return backendAuthorization{}, err
	}
	scope, err := s.executionScope()
	if err != nil {
		return backendAuthorization{}, err
	}
	if scope != nil {
		return backendAuthorization{"execution-scope", cpRef, ".yss-backend-delivery.json"}, nil
	}
	profile, policy, err := progressionPolicy(s)
	if err != nil {
		return backendAuthorization{}, err
	}
	if cpRef == "" {
		cpRef = s.checkpointRef
	}
	if profile == "backend" {
		config, cp, err := progressionLocation(s, cpRef)
		if err != nil {
			return backendAuthorization{}, err
		}
		if cp["profile_id"] != nil && cp["profile_id"] != domain.Profiles[profile].ID {
			return backendAuthorization{}, s.reject("IDENTITY", "checkpoint Profile 与后端工程不符")
		}
		a := backendAuthorization{"backend-profile", cpRef, path.Join(path.Dir(config), "backend-delivery.json")}
		if terminalRef != "" && terminalRef != a.TerminalRef {
			return a, s.reject("PROGRESSION_BINDING", "后端Profile终点与显式功能不符")
		}
		return a, nil
	}
	if profile != "spec" {
		return backendAuthorization{}, s.reject("EXECUTION_SCOPE", "仅 Spec 支持单功能后端交付目标")
	}
	if cpRef == "" && terminalRef != "" {
		meta, e := contractTicketMetadata(s, path.Join(path.Dir(terminalRef), "map.md"))
		if e != nil {
			return backendAuthorization{}, e
		}
		cpRef = text(meta["checkpoint_ref"])
	}
	config, cp, err := progressionLocation(s, cpRef)
	if err != nil {
		return backendAuthorization{}, err
	}
	if cp["profile_id"] != nil && cp["profile_id"] != domain.Profiles[profile].ID {
		return backendAuthorization{}, s.reject("IDENTITY", "checkpoint Profile 与 Spec 工程不符")
	}
	a := backendAuthorization{"feature-target", cpRef, path.Join(path.Dir(config), "backend-delivery.json")}
	if terminalRef != "" && terminalRef != a.TerminalRef {
		return a, s.reject("PROGRESSION_BINDING", "后端交付终点与当前功能不符")
	}
	if readOnly {
		return a, nil
	}
	if !semHas(policy["writer_profiles"], profile) {
		return a, s.unavailable("CAPABILITY", "当前政策不能设置后端交付目标")
	}
	raw, err := s.bytes(config)
	if err != nil {
		return a, err
	}
	target, err := parseProgressionTarget(raw)
	if err != nil {
		return a, err
	}
	if target.FeatureID != text(cp["feature_id"]) || target.CheckpointRef != cpRef {
		return a, s.reject("PROGRESSION_BINDING", "后端目标未绑定当前功能")
	}
	requested := false
	for _, c := range target.Consumers {
		if c.Profile == "backend" && c.Root != s.root {
			requested = true
		}
	}
	if !requested {
		return a, s.reject("EXECUTION_SCOPE", "本功能未明确请求后端交付目标或外部后端承接")
	}
	return a, nil
}
