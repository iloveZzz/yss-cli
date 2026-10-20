package governance

import "github.com/iloveZzz/yss-cli/internal/governance/approval"

import (
	"context"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/contracts"
	"github.com/iloveZzz/yss-cli/internal/governance/handoff"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/governance/lifecycle"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"path"
)

// Target evaluation is deliberately independent of lifecycle status and Node
// writers. It consumes current evidence without issuing an execution permit.
func progressionRead(ctx context.Context, root, cpRef string) (map[string]any, error) {
	s := kernel.NewSemanticSession(ctx, root, map[string]string{"checkpoint": cpRef})
	return progressionReadSession(s)
}

func progressionReadSession(s *kernel.SemanticSession) (map[string]any, error) {
	root, cpRef := s.Root, s.CheckpointRef
	if err := kernel.ProjectIdentity(s.V); err != nil {
		return nil, err
	}
	profile, policy, err := kernel.ProgressionPolicy(s)
	if err != nil {
		return nil, err
	}
	configRef, cp, err := kernel.ProgressionLocation(s, cpRef)
	if err != nil {
		return nil, err
	}
	if cp["profile_id"] != nil && cp["profile_id"] != domain.Profiles[profile].ID {
		return nil, s.Reject("IDENTITY", "checkpoint Profile 与工程不符")
	}
	p := map[string]any{"enabled": true, "root": root, "profile": profile, "feature_id": cp["feature_id"], "checkpoint_ref": cpRef, "config_ref": configRef, "config_digest": nil, "target": policy["default_target"], "target_source": "profile-default", "intent_source": "当前Profile默认职责终点", "status": "pending", "read_only": true, "approval_created": false, "execution_authorization": "not-evaluated"}
	responsibilityTarget := "profile-terminal"
	target := ProgressionTarget{FeatureID: kernel.Text(cp["feature_id"]), CheckpointRef: cpRef, Consumers: []ProgressionConsumer{}}
	if profile == "spec" {
		p["intent_source"] = "Spec默认完成完整业务验收"
		responsibilityTarget = "business-accepted"
		scope, e := s.ExecutionScope()
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
	present, err := s.Exists(configRef)
	if err != nil {
		return nil, err
	}
	if present {
		raw, e := s.Bytes(configRef)
		if e != nil {
			return nil, e
		}
		target, e = parseProgressionTarget(raw)
		if e != nil {
			return nil, e
		}
		if e = progressionCurrentConsumers(s, target); e != nil {
			return nil, e
		}
		if target.FeatureID != kernel.Text(cp["feature_id"]) || target.CheckpointRef != cpRef || profile != "spec" {
			return nil, s.Reject("PROGRESSION_BINDING", "目标配置未绑定当前 Spec 功能 checkpoint")
		}
		if e = progressionScope(s, target.Target); e != nil {
			return nil, e
		}
		p["config_digest"], p["target"], p["target_source"] = "sha256:"+safefs.Digest(raw), target.Target, "feature-intent"
		p["intent_source"] = target.IntentSource
	}
	target.Target = kernel.Text(p["target"])
	for ref, key := range map[string]string{cpRef: "checkpoint_digest"} {
		raw, e := s.Bytes(ref)
		if e != nil {
			return nil, e
		}
		p[key] = "sha256:" + safefs.Digest(raw)
	}
	_, _, orchestrationDigest, contractErr := s.OrchestrationDocument(kernel.GuidanceContractRef(profile))
	if contractErr != nil {
		return nil, contractErr
	}
	p["contract_digest"] = orchestrationDigest
	if profile == "spec" {
		materials, err := contracts.ProgressionIntentMaterials(s, cpRef, configRef, cp)
		if err != nil {
			return nil, err
		}
		p["intent_materials"] = materials
	}
	coordination, participants := progressionConsumers(s, profile, cpRef, cp, target)
	policyRow := kernel.SemMap(kernel.SemMap(policy["completion_policy"])[target.Target])
	status, reason := "pending", "等待当前目标的批准与交付证据"
	if profile != "spec" {
		status, reason = progressionProfileTerminal(s, profile, cpRef, cp)
	} else if len(policyRow) == 0 {
		status, reason = "blocked", "当前政策缺少所选目标的完成规则"
	} else if blockers, ok := cp["blockers"].([]any); !ok || len(blockers) > 0 {
		status, reason = "blocked", "checkpoint 缺少有效无阻塞事实"
	} else {
		owner := map[string]string{"backend-deliverable": "backend"}[target.Target]
		declaredOwner := false
		for _, c := range target.Consumers {
			if c.Profile == owner {
				declaredOwner = true
			}
		}
		if participant := participants[owner]; participant != nil {
			state := kernel.SemMap(coordination[owner])
			if state["input_status"] != "verified" {
				status, reason = "pending", "独立消费者的当前来源接收尚未核验"
				if state["input_status"] == "blocked" {
					status, reason = "blocked", kernel.Text(state["reason"])
				}
			} else {
				localRef := participant.CheckpointRef
				localCP, e := participant.Doc(localRef)
				if e != nil {
					status, reason = "blocked", e.Error()
				} else {
					status, reason = progressionProfileTerminal(participant, owner, localRef, localCP)
				}
			}
		} else if declaredOwner {
			status, reason = "pending", "等待显式消费者当前checkpoint及来源接收："+owner
			if kernel.SemMap(coordination[owner])["input_status"] == "blocked" {
				status, reason = "blocked", kernel.Text(kernel.SemMap(coordination[owner])["reason"])
			}
		} else {
			status, reason = progressionMilestone(s, cpRef, cp, target.Target, policyRow, target)
		}
		// An explicit implementation handoff is part of the selected goal;
		// consumers may continue after it under their own registered contracts.
		if (status == "reached" || status == "not-applicable") && kernel.SemMap(policyRow["conditional_required_checks"])["strategic-handoff-delivery"] == "explicit-external-implementation-consumer" && progressionExternalImplementation(target, root) {
			source, sourceCP := s, cp

			if err == nil {
				err = progressionCheck(source, source.CheckpointRef, sourceCP, "strategic-handoff-delivery")
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
			terminalStatus, terminalReason = progressionMilestone(s, cpRef, cp, responsibilityTarget, kernel.SemMap(kernel.SemMap(policy["completion_policy"])[responsibilityTarget]), target)
		}
	}
	if profile == "spec" && target.Target != responsibilityTarget && responsibilityTarget == "business-accepted" && terminalStatus == "reached" {
		terminalStatus, terminalReason = progressionBusinessCompletion(s, cpRef, cp, kernel.SemMap(kernel.SemMap(policy["completion_policy"])[responsibilityTarget]), target, coordination, participants)
	}
	p["completion"] = map[string]any{"stage": map[string]any{"status": "not-evaluated", "stage": cp["stage"]}, "milestone": map[string]any{"status": status, "target": target.Target}, "profile": map[string]any{"status": terminalStatus, "reason": terminalReason}, "business": map[string]any{"status": "not-evaluated"}}
	if stageStatus, stageReason := progressionStage(s, cpRef, cp); stageStatus != "not-evaluated" {
		kernel.SemMap(p["completion"])["stage"] = map[string]any{"status": stageStatus, "stage": cp["stage"], "reason": stageReason}
	}
	if status == "not-applicable" && cp["stage"] == "stage.product-design" && target.Target == "product-design-completed" {
		kernel.SemMap(p["completion"])["stage"] = map[string]any{"status": status, "stage": cp["stage"], "reason": reason}
	}
	if profile == "spec" && responsibilityTarget == "business-accepted" {
		kernel.SemMap(p["completion"])["business"] = map[string]any{"status": terminalStatus, "reason": terminalReason}
	}
	// Read all dispatch qualifications before the final input observation. A
	// waiting consumer is actionable only after its actual source is ready.
	await := map[string]any{}
	if status == "pending" {
		for _, c := range target.Consumers {
			row := kernel.SemMap(coordination[c.Profile])
			if (row["input_status"] != "verified" || row["completion_status"] != "reached") && progressionConsumerAwaitable(s, profile, cpRef, cp, target, c, coordination, participants) {
				await = map[string]any{"profile": c.Profile, "root": c.Root, "checkpoint_ref": c.CheckpointRef, "reason": row["reason"], "work_unit": nil}
				if participant := participants[c.Profile]; participant != nil {
					local, _ := participant.Doc(c.CheckpointRef)
					await["work_unit"] = local["next_work_unit"]
				}
				break
			}
		}
	}
	if e := s.Finish(); e != nil {
		p["status"], p["reason"], p["inputs_current"] = "blocked", e.Error(), false
		for _, layer := range kernel.SemMap(p["completion"]) {
			item := kernel.SemMap(layer)
			item["status"], item["reason"] = "blocked", e.Error()
		}
		for _, row := range coordination {
			item := kernel.SemMap(row)
			item["input_status"], item["completion_status"], item["reason"] = "blocked", "blocked", e.Error()
		}
	} else {
		p["inputs_current"] = true
	}
	p["input_digests"] = s.Inputs()
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

func progressionConsumerAwaitable(s *kernel.SemanticSession, profile, cpRef string, cp map[string]any, target ProgressionTarget, consumer ProgressionConsumer, coordination map[string]any, participants map[string]*kernel.SemanticSession) bool {
	required := map[string][]string{"backend-deliverable": {"backend"}, "business-accepted": {"backend"}}
	if !kernel.SemHas(required[target.Target], consumer.Profile) {
		return false
	}
	row := kernel.SemMap(coordination[consumer.Profile])
	if row["input_status"] == "verified" {
		row["source_ready"] = true
		return true
	}
	if row["input_status"] == "blocked" {
		return false
	}

	source, sourceRef, sourceCP := s, cpRef, cp

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
		row := kernel.SemMap(coordination[c.Profile])
		if row["input_status"] == "blocked" {
			return "blocked", kernel.Text(row["reason"])
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
func progressionBusinessCompletion(s *kernel.SemanticSession, cpRef string, cp, policy map[string]any, target ProgressionTarget, coordination map[string]any, participants map[string]*kernel.SemanticSession) (string, string) {
	status, reason := progressionBusinessQualification("reached", "当前批准、依据与交付检查已核验", target.Consumers, coordination)
	if status != "reached" {
		return status, reason
	}
	conditional := kernel.SemMap(policy["conditional_required_checks"])
	if conditional["backend-delivery"] == nil {
		return "blocked", "业务完成政策缺少当前后端适用性规则"
	}
	if _, _, err := handoff.InspectSpecBaselineSource(s, cpRef); err != nil {
		return "blocked", err.Error()
	}
	stageRef := handoff.CheckpointAsset(cp, "stage_decision_package_ref", "artifact.stage-decision-package")
	stage, err := s.Doc(stageRef)
	if err != nil {
		return "blocked", err.Error()
	}
	impact := kernel.SemMap(stage["impact_assessment"])
	for _, key := range []string{"backend", "api", "data"} {
		if _, ok := impact[key].(bool); !ok {
			return "blocked", "当前批准影响评估未明确实现适用性：" + key
		}
		if kernel.SemMap(cp["delivery_impacts"])[key] == true && impact[key] == false {
			return "blocked", "checkpoint 实现影响与当前批准影响评估冲突：" + key
		}
	}
	backendRequired := impact["backend"] == true || impact["api"] == true || impact["data"] == true
	if condition := conditional["backend-delivery"]; condition != nil {
		if condition != "backend-api-data-impact" {
			return "blocked", "未知业务后端完成条件"
		}
		if backendRequired && participants["backend"] == nil {
			if err := contracts.BackendBusinessLocalEvidence(s, cpRef); err != nil {
				return "blocked", err.Error()
			}
		}
	}
	return status, reason
}

func progressionExternalImplementation(t ProgressionTarget, root string) bool {
	for _, c := range t.Consumers {
		if c.Profile == "backend" && c.Root != root {
			return true
		}
	}
	return false
}

func progressionMilestone(s *kernel.SemanticSession, cpRef string, cp map[string]any, target string, policy map[string]any, intent ProgressionTarget) (string, string) {
	if err := s.Authorities(); err != nil {
		return "blocked", err.Error()
	}
	if s.StageGatesEnabled() {
		gate := kernel.Text(kernel.ApMap(kernel.ApMap(s.GovernancePolicy["stage_gates"])["targets"])[target])
		if gate == "" {
			return "blocked", "当前四阶段政策未登记目标"
		}
		if err := approval.VerifyCheckpointStage(s, cpRef, gate, ""); err != nil {
			return "pending", err.Error()
		}
		for _, check := range kernel.SemStrings(policy["required_checks"]) {
			if err := progressionCheck(s, cpRef, cp, check); err != nil {
				return "pending", err.Error()
			}
		}
		return "reached", "当前阶段记录及适用交付证据已核验"
	}

	if len(kernel.SemStrings(policy["required_gates"])) == 0 || len(kernel.SemStrings(policy["required_checks"])) == 0 {
		return "blocked", "目标政策缺少可复算的门禁或交付检查"
	}
	for _, id := range kernel.SemStrings(policy["required_gates"]) {
		gate := kernel.SemMap(kernel.SemMap(cp["gates"])[id])
		if len(gate) == 0 || gate["status"] != "approved" && gate["status"] != "not-applicable" {
			return "pending", "等待当前门禁：" + id
		}
	}
	if err := s.Authorities(); err != nil {
		return "blocked", err.Error()
	}
	na := false
	for _, id := range kernel.SemStrings(policy["required_gates"]) {
		gate := kernel.SemMap(kernel.SemMap(cp["gates"])[id])
		if gate["status"] == "not-applicable" {
			if kernel.SemMap(policy["conditional_gates"])[id] != "product-design-impact" || gate["applicable"] != nil && gate["applicable"] != false || kernel.Text(gate["reason"]) == "" {
				return "blocked", "不适用门禁缺少政策、原因与依据"
			}
			stageRef := handoff.CheckpointAsset(cp, "stage_decision_package_ref", "artifact.stage-decision-package")
			if stageRef == "" {
				return "blocked", "不适用产品设计缺少已绑定影响评估"
			}
			stage, err := s.Doc(stageRef)
			if err != nil {
				return "blocked", err.Error()
			}
			impact := kernel.SemMap(stage["impact_assessment"])
			if impact["product_design"] == true || impact["ui"] == true || impact["product_design"] != false && impact["ui"] != false || kernel.SemMap(cp["delivery_impacts"])["ui"] == true {
				return "blocked", "产品设计不适用与当前影响评估冲突"
			}
			bound := false
			for _, row := range kernel.SemList(gate["basis"]) {
				if kernel.SemMap(row)["ref"] == stageRef {
					bound = true
				}
			}
			if !bound {
				return "blocked", "不适用门禁未绑定当前影响评估"
			}
			if err = s.Basis(gate["basis"]); err != nil {
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
		if err := approval.GateChecks(s, cp, cpRef, id); err != nil {
			return "blocked", err.Error()
		}
	}
	for _, check := range kernel.SemStrings(policy["required_checks"]) {
		if err := progressionCheck(s, cpRef, cp, check); err != nil {
			return "blocked", err.Error()
		}
	}
	if na && target == "product-design-completed" {
		return "not-applicable", "当前影响评估确认产品设计不适用；适用前置条件已核验"
	}
	return "reached", "当前批准、依据与交付检查已核验"
}

func progressionNoUI(s *kernel.SemanticSession, cpRef string, cp map[string]any) (bool, error) {

	source, _, err := handoff.InspectSpecBaselineSource(s, cpRef)
	return err == nil && source["product_design_required"] == false, err
}

func progressionCheck(s *kernel.SemanticSession, cpRef string, cp map[string]any, check string) error {
	switch check {
	case "business-tickets-draft", "business-tickets-formal":
		ref := handoff.CheckpointAsset(cp, "business_ticket_set_ref", "artifact.business-ticket-set")
		if ref == "" {
			return s.Reject("BUSINESS_TICKETS", "缺少当前业务票集合")
		}
		mode := "draft"
		if check == "business-tickets-formal" {
			mode = "formal"
		}
		set, err := contracts.ContractBusinessTicketsMode(s, ref, mode)
		if err != nil {
			return err
		}
		spec := handoff.CheckpointAsset(cp, "spec_ref", "artifact.spec")
		if spec != "" && kernel.SemMap(set["spec"])["ref"] != spec {
			return s.Reject("PROGRESSION_BINDING", "业务票没有绑定当前 Spec")
		}
		if check == "business-tickets-draft" {
			_, _, err = handoff.InspectSpecBaselineSource(s, cpRef)
		}
		return err
	case "backend-delivery":
		a, err := progressionBackendAuthorization(s, cpRef, "", true)
		if err != nil {
			return err
		}
		return s.Verify("backend-terminal", a.TerminalRef, map[string]string{"checkpoint": cpRef})
	case "business-acceptance":
		if s.StageGatesEnabled() {
			return approval.VerifyCheckpointStage(s, cpRef, "gate.delivery-accepted", "")
		}
		refs := kernel.SemStrings(kernel.SemMap(kernel.SemMap(kernel.SemMap(cp["gates"])["gate.delivery-accepted"])["evidence"])["evidence.fresh-verification"])
		if len(refs) == 0 {
			return s.Reject("VERIFICATION_REQUIRED", "业务完成缺少当前实际验证记录")
		}
		for _, ref := range refs {
			if err := s.Verify("verification", ref, map[string]string{"checkpoint": cpRef}); err != nil {
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
			return s.Reject("WAITING_PROFILE_INPUT", "等待当前战略交接交付包")
		}
		_, _, err = handoff.ContractOpenStrategicDelivery(s, ref)
		return err
	default:
		return s.Unavailable("CAPABILITY", "未知目标完成检查："+check)
	}
}

func progressionProfileTerminal(s *kernel.SemanticSession, profile, cpRef string, cp map[string]any) (string, string) {
	if blockers, ok := cp["blockers"].([]any); !ok || len(blockers) > 0 {
		return "blocked", "当前 Profile 缺少有效无阻塞事实"
	}
	if profile == "spec" {
		_, policy, err := kernel.ProgressionPolicy(s)
		if err != nil {
			return "blocked", err.Error()
		}
		target := "business-accepted"
		scope, err := s.ExecutionScope()
		if err != nil {
			return "blocked", err.Error()
		}
		if scope != nil {
			target = "backend-deliverable"
		}
		return progressionMilestone(s, cpRef, cp, target, kernel.SemMap(kernel.SemMap(policy["completion_policy"])[target]), ProgressionTarget{})
	}
	_, policy, err := kernel.ProgressionPolicy(s)
	if err != nil {
		return "blocked", err.Error()
	}
	return progressionMilestone(s, cpRef, cp, "profile-terminal", kernel.SemMap(kernel.SemMap(policy["completion_policy"])["profile-terminal"]), ProgressionTarget{})
}

func progressionConsumers(s *kernel.SemanticSession, profile, cpRef string, cp map[string]any, target ProgressionTarget) (map[string]any, map[string]*kernel.SemanticSession) {
	rows, sessions := map[string]any{}, map[string]*kernel.SemanticSession{}
	for _, c := range target.Consumers {
		row := map[string]any{"profile": c.Profile, "root": c.Root, "checkpoint_ref": c.CheckpointRef, "input_status": "pending", "completion_status": "not-evaluated"}
		rows[c.Profile] = row
		if c.Root == s.Root {
			row["input_status"], row["reason"] = "blocked", "消费者不能指向主控工程"
			continue
		}
		child := kernel.NewSemanticSession(s.Ctx, c.Root, map[string]string{"checkpoint": c.CheckpointRef})
		s.Children = append(s.Children, child)
		present, err := child.Exists(c.CheckpointRef)
		if err != nil || !present {
			row["reason"] = "等待明确目标 checkpoint"
			continue
		}
		_, localCP, err := kernel.ProgressionLocation(child, c.CheckpointRef)
		if err == nil {
			p, e := child.Doc(".template-spec/process/harness-profile.yaml")
			err = e
			if err == nil && (localCP["feature_id"] != cp["feature_id"] || p["profile_id"] != domain.Profiles[c.Profile].ID || localCP["profile_id"] != nil && localCP["profile_id"] != p["profile_id"]) {
				err = child.Reject("PROGRESSION_BINDING", "消费者 feature/Profile 不匹配")
			}
		}
		if err != nil {
			row["input_status"], row["reason"] = "blocked", err.Error()
			continue
		}
		sessions[c.Profile] = child
		row["completion_status"], row["completion_reason"] = progressionProfileTerminal(child, c.Profile, c.CheckpointRef, localCP)
	}
	for _, name := range []string{"backend"} {
		var c ProgressionConsumer
		for _, candidate := range target.Consumers {
			if candidate.Profile == name {
				c = candidate
			}
		}
		if c.Profile == "" {
			continue
		}
		row := kernel.SemMap(rows[c.Profile])
		if sessions[c.Profile] == nil {
			continue
		}
		source, sourceProfile, sourceRef, sourceCP := s, profile, cpRef, cp

		engineering, _ := guidanceEngineering(c.Root, c.Profile)
		status, reason, _ := guidanceTargetInputSession(s.Ctx, source, sourceProfile, sourceRef, sourceCP, c.Root, c.Profile, engineering, sessions[c.Profile])
		if status == "verified" {
			row["input_status"] = "verified"
			row["input"] = sessions[c.Profile].ProfileInput
		} else if status == "blocked" {
			row["input_status"] = "blocked"
		} else {
			row["input_status"] = "pending"
		}
		row["reason"] = reason
		if row["completion_status"] == "reached" {
			local, _ := sessions[c.Profile].Doc(c.CheckpointRef)
			progressionConsumerEvidence(sessions[c.Profile], c, local, row)
		}
	}
	return rows, sessions
}

func progressionConsumerEvidence(s *kernel.SemanticSession, c ProgressionConsumer, cp, row map[string]any) {
	refs := map[string]any{}
	if c.Profile == "backend" {
		a, err := progressionBackendAuthorization(s, c.CheckpointRef, "", true)
		if err == nil {
			terminal, e := s.Doc(a.TerminalRef)
			if e == nil {
				binding := kernel.SemMap(terminal["delivery"])
				delivery, e := s.Doc(kernel.Text(binding["ref"]))
				if e == nil {
					refs["delivery"] = map[string]any{"ref": binding["ref"], "digest": binding["digest"], "terminal_ref": a.TerminalRef, "delivery_id": delivery["delivery_id"], "version": delivery["version"], "slice_id": kernel.SemMap(delivery["scope"])["slice_id"], "bundle_ref": terminal["bundle_ref"], "bundle_digest": terminal["bundle_digest"], "verification": delivery["verification"]}
				}
			}
		}
	}
	_, policy, err := kernel.ProgressionPolicy(s)
	if err == nil {
		gates := []any{}
		for _, id := range kernel.SemStrings(kernel.SemMap(kernel.SemMap(policy["completion_policy"])["profile-terminal"])["required_gates"]) {
			gate := kernel.SemMap(kernel.SemMap(cp["gates"])[id])
			gates = append(gates, map[string]any{"gate_id": id, "approval_ref": gate["approval_ref"], "subject_ref": gate["subject_ref"], "basis": gate["basis"], "evidence": gate["evidence"]})
		}
		refs["acceptance_basis"] = gates
	}
	row["evidence"] = refs
}

func progressionStage(s *kernel.SemanticSession, cpRef string, cp map[string]any) (string, string) {
	if cp["status"] != "completed" && cp["status"] != "paused-human-gate" {
		return "pending", "当前阶段未到已登记退出边界"
	}
	if err := s.Authorities(); err != nil {
		return "blocked", err.Error()
	}
	if s.StageGatesEnabled() {
		for _, gate := range kernel.ApRows(kernel.ApMap(s.GovernancePolicy["stage_gates"])["gates"]) {
			if kernel.ApContains(gate["work_stages"], kernel.Text(cp["stage"])) {
				if err := approval.VerifyCheckpointStage(s, cpRef, kernel.Text(gate["id"]), ""); err != nil {
					return "pending", err.Error()
				}
				return "reached", "当前阶段记录已核验"
			}
		}
		return "not-evaluated", "当前工作属于阶段内部准备"
	}
	found := false
	for _, row := range kernel.SemList(s.Registry["gates"]) {
		gate := kernel.SemMap(row)
		if gate["stage"] != cp["stage"] {
			continue
		}
		item := kernel.SemMap(kernel.SemMap(cp["gates"])[kernel.Text(gate["id"])])
		if item["status"] == "approved" {
			if err := approval.GateChecks(s, cp, cpRef, kernel.Text(gate["id"])); err != nil {
				return "blocked", err.Error()
			}
			found = true
		} else if item["status"] == "not-applicable" {
			return "not-evaluated", "当前阶段含条件门禁，须由对应目标政策核验"
		} else {
			return "pending", "当前阶段门禁未闭合：" + kernel.Text(gate["id"])
		}
	}
	if !found {
		return "not-evaluated", "当前阶段无可独立复算的退出门禁"
	}
	return "reached", "当前阶段适用门禁的当前批准已核验"
}

type backendAuthorization = lifecycle.BackendAuthorization

// A feature target permits one delivery work unit; it never widens a scope or
// makes another feature inherit this feature's terminal record.
func progressionBackendAuthorization(s *kernel.SemanticSession, cpRef, terminalRef string, readOnly bool) (backendAuthorization, error) {
	if err := kernel.ProjectIdentity(s.V); err != nil {
		return backendAuthorization{}, err
	}
	scope, err := s.ExecutionScope()
	if err != nil {
		return backendAuthorization{}, err
	}
	if scope != nil {
		return backendAuthorization{"execution-scope", cpRef, ".yss-backend-delivery.json"}, nil
	}
	profile, policy, err := kernel.ProgressionPolicy(s)
	if err != nil {
		return backendAuthorization{}, err
	}
	if cpRef == "" {
		cpRef = s.CheckpointRef
	}
	if profile == "backend" {
		config, cp, err := kernel.ProgressionLocation(s, cpRef)
		if err != nil {
			return backendAuthorization{}, err
		}
		if cp["profile_id"] != nil && cp["profile_id"] != domain.Profiles[profile].ID {
			return backendAuthorization{}, s.Reject("IDENTITY", "checkpoint Profile 与后端工程不符")
		}
		a := backendAuthorization{"backend-profile", cpRef, path.Join(path.Dir(config), "backend-delivery.json")}
		if terminalRef != "" && terminalRef != a.TerminalRef {
			return a, s.Reject("PROGRESSION_BINDING", "后端Profile终点与显式功能不符")
		}
		return a, nil
	}
	if profile != "spec" {
		return backendAuthorization{}, s.Reject("EXECUTION_SCOPE", "仅 Spec 支持单功能后端交付目标")
	}
	if cpRef == "" && terminalRef != "" {
		meta, e := kernel.ContractTicketMetadata(s, path.Join(path.Dir(terminalRef), "map.md"))
		if e != nil {
			return backendAuthorization{}, e
		}
		cpRef = kernel.Text(meta["checkpoint_ref"])
	}
	config, cp, err := kernel.ProgressionLocation(s, cpRef)
	if err != nil {
		return backendAuthorization{}, err
	}
	if cp["profile_id"] != nil && cp["profile_id"] != domain.Profiles[profile].ID {
		return backendAuthorization{}, s.Reject("IDENTITY", "checkpoint Profile 与 Spec 工程不符")
	}
	a := backendAuthorization{"feature-target", cpRef, path.Join(path.Dir(config), "backend-delivery.json")}
	if terminalRef != "" && terminalRef != a.TerminalRef {
		return a, s.Reject("PROGRESSION_BINDING", "后端交付终点与当前功能不符")
	}
	if readOnly {
		return a, nil
	}
	if !kernel.SemHas(policy["writer_profiles"], profile) {
		return a, s.Unavailable("CAPABILITY", "当前政策不能设置后端交付目标")
	}
	raw, err := s.Bytes(config)
	if err != nil {
		return a, err
	}
	target, err := parseProgressionTarget(raw)
	if err != nil {
		return a, err
	}
	if target.FeatureID != kernel.Text(cp["feature_id"]) || target.CheckpointRef != cpRef {
		return a, s.Reject("PROGRESSION_BINDING", "后端目标未绑定当前功能")
	}
	requested := false
	for _, c := range target.Consumers {
		if c.Profile == "backend" && c.Root != s.Root {
			requested = true
		}
	}
	if !requested {
		return a, s.Reject("EXECUTION_SCOPE", "本功能未明确请求后端交付目标或外部后端承接")
	}
	return a, nil
}
