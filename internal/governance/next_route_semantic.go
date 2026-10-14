package governance

import (
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/governance/approval"
	"github.com/iloveZzz/yss-cli/internal/governance/handoff"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

func init() {
	kernel.RegisterSemanticValidator("next-route", verifyNextRouteSemantic)
	kernel.RegisterSemanticValidator("execution-scope", verifyExecutionScopeSemantic)
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
	if profile == "harness.backend-delivery" {
		put("harness-entry", "technical-design")
		put("technical-design", "implementation-repository-preparation")
		put("implementation-repository-preparation", "slice-contract")
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
	put("business-ticket-formalization", "technical-analysis", "strategic-design-handoff")
	put("strategic-design-handoff", "technical-analysis")
	put("technical-analysis", "implementation-repository-preparation")
	put("implementation-repository-preparation", "ticket-decomposition")
	put("ticket-decomposition", "slice-implementation")
	put("slice-implementation", "code-review")
	put("code-review", "release-and-retrospective", "slice-implementation", "backend-delivery")
	put("backend-delivery", "release-and-retrospective")
	put("release-and-retrospective")
	return m
}

func verifyExecutionScopeSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	state, err := s.Doc(ref)
	if err != nil {
		return err
	}
	scope, err := s.ExecutionScope()
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
				return s.Reject("EXECUTION_SCOPE", "后端终点不得继续其他工作单元")
			}
			return s.Verify("backend-terminal", ".yss-backend-delivery.json", opts)
		}
		next := kernel.Text(state["next_work_unit"])
		if next == "" && kernel.SemHas([]string{"resume", "orchestrate"}, kernel.Text(state["mode"])) {
			next = "work-unit.entry-triage"
		}
		if next != "" {
			if !kernel.SemHas(scope["allowed_work_units"], next) {
				return s.Reject("EXECUTION_SCOPE", "下一工作单元超出后端职责范围")
			}
			terminal, e := s.Exists(".yss-backend-delivery.json")
			if e != nil {
				return e
			}
			if terminal {
				if e = s.Verify("backend-terminal", ".yss-backend-delivery.json", opts); e != nil {
					return e
				}
				if !kernel.SemHas([]string{"audit", "route"}, kernel.Text(state["mode"])) {
					return s.Reject("EXECUTION_SCOPE", "后端交付已完成，不允许恢复实现或自动发布")
				}
			}
		}
	}
	if kernel.SemMap(state["delivery_impacts"])["frontend"] == true {
		return s.Reject("EXECUTION_SCOPE", "后端职责禁止派发生产前端实现")
	}
	if state["ui_impact"] == true {
		deferred := kernel.SemMap(state["downstream_frontend"])
		for _, k := range []string{"owner", "ticket_ref", "verification_plan", "target_version"} {
			if kernel.Text(deferred[k]) == "" {
				return s.Reject("EXECUTION_SCOPE", "UI 下游职责缺少 "+k)
			}
		}
	}
	if state["slice_contract"] != nil {
		state = kernel.SemMap(state["slice_contract"])
	}
	if state["contract_id"] != nil && state["work_units"] != nil {
		if kernel.SemMap(state["backend"])["status"] != "required" || kernel.SemMap(state["frontend"])["status"] != "not-applicable" {
			return s.Reject("EXECUTION_SCOPE", "后端职责仅允许后端 Slice")
		}
		for _, area := range kernel.SemStrings(kernel.SemMap(state["common"])["impacted_areas"]) {
			if area == "ui" || area == "frontend" {
				return s.Reject("EXECUTION_SCOPE", "Slice 越过后端职责")
			}
		}
		for _, row := range kernel.SemList(state["work_units"]) {
			if kernel.SemMap(row)["role_id"] == "role.frontend-engineer" {
				return s.Reject("EXECUTION_SCOPE", "后端职责不能派发前端工程师")
			}
		}
	}
	return nil
}

func verifyNextRouteSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	state, err := s.Doc(ref)
	if err != nil {
		return err
	}
	cpRef := ref
	taskRef := ""
	current, next := kernel.Text(kernel.SemMap(state["stage_trace"])["completed_work_unit"]), kernel.Text(state["next_work_unit"])
	if opts["current"] != "" {
		taskRef = ref
		current, next = opts["current"], opts["next"]
		cpRef = opts["checkpoint"]
		if cpRef == "" {
			return s.Reject("CHECKPOINT_REQUIRED", "正式任务流转缺少当前 checkpoint")
		}
		cp, e := s.Doc(cpRef)
		if e != nil {
			return e
		}
		state = cp
		// Results may add evidence, never replace current approval expectations.
	}
	state, err = s.TransitionState(cpRef, taskRef)
	if err != nil {
		return err
	}
	if current == "" {
		return s.Reject("TRANSITION_ORIGIN_REQUIRED", "当前流转缺少已完成工作单元")
	}
	identity, err := s.Doc("yss-project.yaml")
	if err != nil {
		return err
	}
	profile, err := s.Doc(".template-spec/process/harness-profile.yaml")
	if err != nil {
		return err
	}
	routes := nativeNextRoutes(kernel.Text(profile["profile_id"]))
	allowed, known := routes[current]
	if !known {
		return s.Reject("ROUTE", "当前 Profile 不支持工作单元: "+current)
	}
	if current == "work-unit.entry-triage" && identity["repository_mode"] == "template-source" {
		allowed = []string{"work-unit.maintenance-research", "work-unit.ssot-update"}
	}
	if profile["profile_id"] == "harness.spec-template" && (current == "work-unit.strategic-design-handoff" || next == "work-unit.strategic-design-handoff") {
		config, cp, e := kernel.ProgressionLocation(s, cpRef)
		if e != nil {
			return e
		}
		raw, e := s.Bytes(config)
		if e != nil {
			return e
		}
		intent, e := parseProgressionTarget(raw)
		if e != nil {
			return e
		}
		if intent.FeatureID != kernel.Text(cp["feature_id"]) || intent.CheckpointRef != cpRef || !progressionExternalImplementation(intent, s.Root) {
			return s.Reject("PROFILE_ROUTE", "Spec 战略交接需要同功能显式外部专职消费者")
		}
		if current == "work-unit.strategic-design-handoff" {
			if e = progressionCheck(s, cpRef, cp, "strategic-handoff-delivery"); e != nil {
				return e
			}
		}
	}
	if current == "work-unit.entry-triage" && profile["profile_id"] == handoff.SpecBaselineProfile && state["upstream_spec_baseline"] != nil {
		if err = handoff.VerifyInheritedSpecCheckpoint(s, cpRef, state); err != nil {
			return err
		}
		manifest, _, e := handoff.VerifySpecBaselineReceipt(s, kernel.Text(kernel.SemMap(state["upstream_spec_baseline"])["receipt_ref"]), false)
		if e != nil {
			return e
		}
		allowed = []string{"work-unit.business-ticket-formalization"}
		if kernel.SemMap(manifest["source"])["product_design_required"] == true {
			allowed = []string{"work-unit.prototype-design"}
		}
	}
	if (current == "work-unit.maintenance-research" || next == "work-unit.maintenance-research") && identity["repository_mode"] != "template-source" {
		return s.Reject("ROUTE", "模板研究仅用于 template-source")
	}
	scope, err := s.ExecutionScope()
	if err != nil {
		return err
	}
	if scope == nil {
		backendRequested := false
		if next == "work-unit.backend-delivery" {
			_, e := progressionBackendAuthorization(s, cpRef, "", false)
			if e != nil {
				return e
			}
			backendRequested = true
		}
		filtered := []string{}
		for _, route := range allowed {
			if route != "work-unit.backend-delivery" || backendRequested {
				filtered = append(filtered, route)
			}
		}
		allowed = filtered
		if current == "work-unit.backend-delivery" {
			a, e := progressionBackendAuthorization(s, cpRef, "", true)
			if e != nil {
				return e
			}
			if e = s.Verify("backend-terminal", a.TerminalRef, map[string]string{"checkpoint": cpRef}); e != nil {
				return e
			}
		}
	} else {
		if !kernel.SemHas(scope["allowed_work_units"], current) || next != "" && !kernel.SemHas(scope["allowed_work_units"], next) {
			return s.Reject("EXECUTION_SCOPE", "阶段超出职责范围")
		}
		terminal, e := s.Exists(".yss-backend-delivery.json")
		if e != nil {
			return e
		}
		if current == "work-unit.backend-delivery" && next == "" {
			return s.Verify("backend-terminal", ".yss-backend-delivery.json", nil)
		}
		if terminal {
			return s.Reject("EXECUTION_SCOPE", "后端交付已完成，不允许恢复实现或自动发布")
		}
		if next == "" {
			return s.Reject("EXECUTION_SCOPE", "不能将中间阶段声明为后端终点")
		}
		if override, ok := kernel.SemMap(scope["route_overrides"])[current]; ok {
			allowed = kernel.SemStrings(override)
		}
		if err = s.Verify("execution-scope", cpRef, nil); err != nil {
			return err
		}
	}
	if next == "" && len(allowed) != 0 || next != "" && !kernel.SemHas(allowed, next) {
		return s.Reject("ROUTE", fmt.Sprintf("非法流转 %s → %s", current, next))
	}
	if err = s.TrackingTransition(state, cpRef, current, next); err != nil {
		return err
	}
	if err = s.Verify("reading-transition", cpRef, map[string]string{"current": current}); err != nil {
		return err
	}
	if kernel.Text(profile["profile_id"]) == "harness.spec-template" || kernel.Text(profile["profile_id"]) == "harness.business-ddd-strategy-handoff" {
		if err = s.Verify("business-checkpoint", cpRef, map[string]string{"required": fmt.Sprint(current == "work-unit.business-ticket-formalization")}); err != nil {
			return err
		}
		if current == "work-unit.spec-synthesis" || next == "work-unit.spec-synthesis" {
			if err = s.Verify("plan-spec-entry", cpRef, nil); err != nil {
				return err
			}
		}
	}
	if next == "work-unit.implementation-repository-preparation" {
		if err = s.Verify("technical-transition", cpRef, map[string]string{"task-ref": taskRef}); err != nil {
			return err
		}
	}
	if next == "work-unit.ticket-decomposition" || next == "work-unit.slice-contract" && kernel.Text(profile["profile_id"]) == "harness.backend-delivery" {
		if err = s.Verify("repository-ready", cpRef, nil); err != nil {
			return err
		}
	}
	if next == "work-unit.service-project-initialization" || current == "work-unit.service-project-initialization" {
		if err = s.Verify("service-transition", cpRef, map[string]string{"completed": fmt.Sprint(current == "work-unit.service-project-initialization"), "task-ref": taskRef}); err != nil {
			return err
		}
	}
	if current == "work-unit.code-review" && kernel.SemHas([]string{"work-unit.backend-delivery", "work-unit.release-and-retrospective"}, next) || current == "work-unit.verification" && next == "" && kernel.Text(profile["profile_id"]) == "harness.backend-delivery" {
		if err = s.Verify("backend-review", cpRef, nil); err != nil {
			return err
		}
	}
	if current == "work-unit.maintenance-research" {
		if err = s.Verify("research-completion", cpRef, map[string]string{"continuing": fmt.Sprint(next != ""), "task-ref": taskRef}); err != nil {
			return err
		}
	}
	review := kernel.SemMap(state["human_review"])
	return approval.AssertWorkUnitUserDecisionSemantic(s, current, map[string]any{"user_decisions": review["user_decisions"], "user_decision_not_applicable": review["not_applicable"]})
}
