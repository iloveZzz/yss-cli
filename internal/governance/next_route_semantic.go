package governance

import "fmt"

func init() {
	registerSemanticValidator("next-route", verifyNextRouteSemantic)
	registerSemanticValidator("execution-scope", verifyExecutionScopeSemantic)
}

// These are the fixed-source transition contracts, selected by the observed
// project Profile. Asset-provided routes never replace this contract.
func nativeNextRoutes(profile string) map[string][]string {
	m := map[string][]string{}
	put := func(current string, next ...string) {
		rows := []string{}
		for _, n := range next {
			rows = append(rows, "work-unit."+n)
		}
		m["work-unit."+current] = rows
	}
	if profile == "harness.backend-delivery" || profile == "harness.frontend-delivery" {
		if profile == "harness.backend-delivery" {
			put("harness-entry", "technical-design")
			put("technical-design", "implementation-repository-preparation")
			put("implementation-repository-preparation", "slice-contract")
		} else {
			put("harness-entry", "frontend-engineering-design", "slice-contract")
			put("frontend-engineering-design", "slice-contract")
		}
		put("slice-contract", "slice-implementation")
		put("slice-implementation", "verification")
		put("verification")
		put("ssot-update", "skill-projection-sync", "intensity-aware-verification")
		put("skill-projection-sync", "intensity-aware-verification")
		put("intensity-aware-verification", "intensity-aware-review")
		put("intensity-aware-review", "release-and-rollback")
		put("release-and-rollback")
		return m
	}
	put("entry-triage", "plan-opportunity", "plan-requirements")
	if profile == "harness.business-ddd-strategy-handoff" {
		put("plan-opportunity", "plan-requirements", "domain-strategy-design", "stage-decision")
		put("plan-requirements", "domain-strategy-design", "stage-decision")
		put("domain-strategy-design", "stage-decision")
		put("stage-decision", "spec-synthesis")
		put("spec-synthesis", "prototype-design", "business-ticket-formalization")
		put("prototype-design", "business-ticket-formalization")
		put("business-ticket-formalization", "strategic-design-handoff")
		put("strategic-design-handoff")
		return m
	}
	if profile != "harness.spec-template" {
		return nil
	}
	put("maintenance-research", "ssot-update")
	put("ssot-update", "skill-projection-sync", "intensity-aware-verification-v2")
	put("skill-projection-sync", "template-snapshot-build", "intensity-aware-verification-v2")
	put("template-snapshot-build", "attach-sync-integration", "intensity-aware-verification-v2")
	put("attach-sync-integration", "intensity-aware-verification-v2")
	put("intensity-aware-verification-v2", "intensity-aware-review-v2")
	put("intensity-aware-review-v2", "release-and-rollback")
	put("release-and-rollback")
	put("plan-opportunity", "plan-requirements", "domain-strategy-design", "stage-decision", "spec-synthesis")
	put("plan-requirements", "domain-strategy-design", "stage-decision", "spec-synthesis")
	put("domain-strategy-design", "stage-decision", "spec-synthesis")
	put("stage-decision", "spec-synthesis")
	put("spec-synthesis", "prototype-design-v2", "business-ticket-formalization", "technical-analysis")
	put("prototype-design-v2", "business-ticket-formalization", "technical-analysis")
	put("business-ticket-formalization", "technical-analysis")
	put("technical-analysis", "implementation-repository-preparation")
	put("implementation-repository-preparation", "service-project-initialization", "ticket-decomposition")
	put("service-project-initialization", "implementation-repository-preparation")
	put("ticket-decomposition", "slice-implementation")
	put("slice-implementation", "frontend-implementation-verification", "code-review")
	put("frontend-implementation-verification", "code-review")
	put("code-review", "release-and-retrospective", "slice-implementation", "backend-delivery")
	put("backend-delivery")
	put("release-and-retrospective")
	return m
}

func (s *semanticSession) executionScope() (map[string]any, error) {
	var plugin map[string]any
	present, err := s.exists(".yss-plugin.json")
	if err != nil {
		return nil, err
	}
	if present {
		plugin, err = s.doc(".yss-plugin.json")
		if err != nil {
			return nil, err
		}
	}
	present, err = s.exists(".yss-execution-scope.yaml")
	if err != nil {
		return nil, err
	}
	if !present {
		if semHas([]string{"yss-plan-to-backend", "yss-backend-delivery"}, text(plugin["plugin"])) {
			return nil, s.reject("EXECUTION_SCOPE", "插件项目缺少显式执行范围")
		}
		return nil, nil
	}
	marker, err := s.doc(".yss-execution-scope.yaml")
	if err != nil {
		return nil, err
	}
	identity, err := s.doc("yss-project.yaml")
	if err != nil {
		return nil, err
	}
	version, valid := integer(marker["schema_version"])
	if !valid || version != 1 || len(marker) != 2 || marker["scope_id"] != "plan-to-backend" || identity["repository_mode"] != "project-instance" {
		return nil, s.reject("EXECUTION_SCOPE", "执行范围版本、身份或字段非法")
	}
	if plugin != nil && (!semHas([]string{"yss-plan-to-backend", "yss-backend-delivery"}, text(plugin["plugin"])) || plugin["execution_scope"] != marker["scope_id"]) {
		return nil, s.reject("EXECUTION_SCOPE", "插件与执行范围冲突")
	}
	contract, err := s.doc(".agents/skills/yss-product-lifecycle/references/orchestration-contract.yaml")
	if err != nil {
		return nil, err
	}
	policy := semMap(semMap(contract["execution_scopes"])["plan-to-backend"])
	if policy["terminal_work_unit"] != "work-unit.backend-delivery" || len(semStrings(policy["allowed_work_units"])) == 0 {
		return nil, s.unavailable("CAPABILITY", "本地主控合同缺少执行范围策略")
	}
	return policy, nil
}

func verifyExecutionScopeSemantic(s *semanticSession, ref string, opts map[string]string) error {
	state, err := s.doc(ref)
	if err != nil {
		return err
	}
	scope, err := s.executionScope()
	if err != nil {
		return err
	}
	if scope == nil {
		return nil
	}
	// Checkpoint completion has a terminal obligation even when the stage trace
	// does not name a completed work unit. It cannot bypass the delivery proof.
	if state["repository_mode"] != nil && state["stage"] != nil {
		if state["status"] == "completed" {
			if state["next_work_unit"] != nil {
				return s.reject("EXECUTION_SCOPE", "后端终点不得继续其他工作单元")
			}
			return s.verify("backend-terminal", ".yss-backend-delivery.json", opts)
		}
		next := text(state["next_work_unit"])
		if next == "" && semHas([]string{"resume", "orchestrate"}, text(state["mode"])) {
			next = "work-unit.entry-triage"
		}
		if next != "" {
			if !semHas(scope["allowed_work_units"], next) {
				return s.reject("EXECUTION_SCOPE", "下一工作单元超出后端职责范围")
			}
			terminal, e := s.exists(".yss-backend-delivery.json")
			if e != nil {
				return e
			}
			if terminal {
				if e = s.verify("backend-terminal", ".yss-backend-delivery.json", opts); e != nil {
					return e
				}
				if !semHas([]string{"audit", "route"}, text(state["mode"])) {
					return s.reject("EXECUTION_SCOPE", "后端交付已完成，不允许恢复实现或自动发布")
				}
			}
		}
	}
	if semMap(state["delivery_impacts"])["frontend"] == true {
		return s.reject("EXECUTION_SCOPE", "后端职责禁止派发生产前端实现")
	}
	if state["ui_impact"] == true {
		deferred := semMap(state["downstream_frontend"])
		for _, k := range []string{"owner", "ticket_ref", "verification_plan", "target_version"} {
			if text(deferred[k]) == "" {
				return s.reject("EXECUTION_SCOPE", "UI 下游职责缺少 "+k)
			}
		}
	}
	if state["slice_contract"] != nil {
		state = semMap(state["slice_contract"])
	}
	if state["contract_id"] != nil && state["work_units"] != nil {
		if semMap(state["backend"])["status"] != "required" || semMap(state["frontend"])["status"] != "not-applicable" {
			return s.reject("EXECUTION_SCOPE", "后端职责仅允许后端 Slice")
		}
		for _, area := range semStrings(semMap(state["common"])["impacted_areas"]) {
			if area == "ui" || area == "frontend" {
				return s.reject("EXECUTION_SCOPE", "Slice 越过后端职责")
			}
		}
		for _, row := range semList(state["work_units"]) {
			if semMap(row)["role_id"] == "role.frontend-engineer" {
				return s.reject("EXECUTION_SCOPE", "后端职责不能派发前端工程师")
			}
		}
	}
	return nil
}

func (s *semanticSession) trackingTransition(cp map[string]any, ref, current, next string) error {
	identity, err := s.doc("yss-project.yaml")
	if err != nil {
		return err
	}
	if identity["repository_mode"] == "template-source" {
		return nil
	}
	if trackedUnits[current] == "" && trackedUnits[next] == "" {
		return nil
	}
	if _, err = checkStage(s.v, cp, ref); err != nil {
		return s.reject("TRACKING", err.Error())
	}
	t, err := asTracking(cp)
	if err != nil {
		return err
	}
	if t == nil {
		return nil
	}
	currentStage, nextStage := trackedUnits[current], trackedUnits[next]
	if current == "work-unit.business-ticket-formalization" && text(cp["profile_id"]) == "harness.business-ddd-strategy-handoff" {
		currentStage = "stage.ticket-formalization"
	}
	for _, item := range t.Items {
		exiting := item.WorkUnit == current || currentStage != "" && currentStage != nextStage && item.Stage == currentStage
		if exiting && !semHas([]string{"completed", "cancelled", "deferred"}, item.Progress) {
			return s.reject("TRACKING", "阶段流转仍有未完成工作项: "+item.ID)
		}
		if exiting && item.RecheckRequired {
			return s.reject("TRACKING", "阶段工作项需要重新核验: "+item.ID)
		}
	}
	return nil
}

func verifyNextRouteSemantic(s *semanticSession, ref string, opts map[string]string) error {
	state, err := s.doc(ref)
	if err != nil {
		return err
	}
	cpRef := ref
	taskRef := ""
	current, next := text(semMap(state["stage_trace"])["completed_work_unit"]), text(state["next_work_unit"])
	if opts["current"] != "" {
		taskRef = ref
		current, next = opts["current"], opts["next"]
		cpRef = opts["checkpoint"]
		if cpRef == "" {
			return s.reject("CHECKPOINT_REQUIRED", "正式任务流转缺少当前 checkpoint")
		}
		cp, e := s.doc(cpRef)
		if e != nil {
			return e
		}
		state = cp
		// Results may add evidence, never replace current approval expectations.
	}
	state, err = s.transitionState(cpRef, taskRef)
	if err != nil {
		return err
	}
	if current == "" {
		return s.reject("TRANSITION_ORIGIN_REQUIRED", "当前流转缺少已完成工作单元")
	}
	identity, err := s.doc("yss-project.yaml")
	if err != nil {
		return err
	}
	profile, err := s.doc(".template-spec/process/harness-profile.yaml")
	if err != nil {
		return err
	}
	routes := nativeNextRoutes(text(profile["profile_id"]))
	allowed, known := routes[current]
	if !known {
		return s.reject("ROUTE", "当前 Profile 不支持工作单元: "+current)
	}
	if current == "work-unit.entry-triage" && identity["repository_mode"] == "template-source" {
		allowed = []string{"work-unit.maintenance-research", "work-unit.ssot-update"}
	}
	if (current == "work-unit.maintenance-research" || next == "work-unit.maintenance-research") && identity["repository_mode"] != "template-source" {
		return s.reject("ROUTE", "模板研究仅用于 template-source")
	}
	scope, err := s.executionScope()
	if err != nil {
		return err
	}
	if scope == nil {
		filtered := []string{}
		for _, route := range allowed {
			if route != "work-unit.backend-delivery" {
				filtered = append(filtered, route)
			}
		}
		allowed = filtered
		if current == "work-unit.backend-delivery" {
			return s.reject("EXECUTION_SCOPE", "后端终点需要显式职责范围")
		}
	} else {
		if !semHas(scope["allowed_work_units"], current) || next != "" && !semHas(scope["allowed_work_units"], next) {
			return s.reject("EXECUTION_SCOPE", "阶段超出职责范围")
		}
		terminal, e := s.exists(".yss-backend-delivery.json")
		if e != nil {
			return e
		}
		if current == "work-unit.backend-delivery" && next == "" {
			return s.verify("backend-terminal", ".yss-backend-delivery.json", nil)
		}
		if terminal {
			return s.reject("EXECUTION_SCOPE", "后端交付已完成，不允许恢复实现或自动发布")
		}
		if next == "" {
			return s.reject("EXECUTION_SCOPE", "不能将中间阶段声明为后端终点")
		}
		if override, ok := semMap(scope["route_overrides"])[current]; ok {
			allowed = semStrings(override)
		}
		if err = s.verify("execution-scope", cpRef, nil); err != nil {
			return err
		}
	}
	if next == "" && len(allowed) != 0 || next != "" && !semHas(allowed, next) {
		return s.reject("ROUTE", fmt.Sprintf("非法流转 %s → %s", current, next))
	}
	if err = s.trackingTransition(state, cpRef, current, next); err != nil {
		return err
	}
	if err = s.verify("reading-transition", cpRef, map[string]string{"current": current}); err != nil {
		return err
	}
	if text(profile["profile_id"]) == "harness.spec-template" || text(profile["profile_id"]) == "harness.business-ddd-strategy-handoff" {
		if err = s.verify("business-checkpoint", cpRef, map[string]string{"required": fmt.Sprint(current == "work-unit.business-ticket-formalization")}); err != nil {
			return err
		}
		if current == "work-unit.spec-synthesis" || next == "work-unit.spec-synthesis" {
			if err = s.verify("plan-spec-entry", cpRef, nil); err != nil {
				return err
			}
		}
	}
	if err = taskFrontendDeliverySemantic(s, state); err != nil {
		return err
	}
	if next == "work-unit.implementation-repository-preparation" {
		if err = s.verify("technical-transition", cpRef, map[string]string{"task-ref": taskRef}); err != nil {
			return err
		}
	}
	if next == "work-unit.ticket-decomposition" || next == "work-unit.slice-contract" && text(profile["profile_id"]) == "harness.backend-delivery" {
		if err = s.verify("repository-ready", cpRef, nil); err != nil {
			return err
		}
	}
	if next == "work-unit.service-project-initialization" || current == "work-unit.service-project-initialization" {
		if err = s.verify("service-transition", cpRef, map[string]string{"completed": fmt.Sprint(current == "work-unit.service-project-initialization"), "task-ref": taskRef}); err != nil {
			return err
		}
	}
	if current == "work-unit.code-review" && semHas([]string{"work-unit.backend-delivery", "work-unit.release-and-retrospective"}, next) || current == "work-unit.verification" && next == "" && text(profile["profile_id"]) == "harness.backend-delivery" {
		if err = s.verify("backend-review", cpRef, nil); err != nil {
			return err
		}
	}
	if current == "work-unit.maintenance-research" {
		if err = s.verify("research-completion", cpRef, map[string]string{"continuing": fmt.Sprint(next != ""), "task-ref": taskRef}); err != nil {
			return err
		}
	}
	review := semMap(state["human_review"])
	return assertWorkUnitUserDecisionSemantic(s, current, map[string]any{"user_decisions": review["user_decisions"], "user_decision_not_applicable": review["not_applicable"]})
}
