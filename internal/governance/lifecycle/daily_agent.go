package lifecycle

import "github.com/iloveZzz/yss-cli/internal/governance/kernel"
import "github.com/iloveZzz/yss-cli/internal/safefs"
import "encoding/json"

type dailyAgentFailure struct {
	cause  error
	report map[string]any
}

func (e *dailyAgentFailure) Error() string    { return e.cause.Error() }
func (e *dailyAgentFailure) Unwrap() error    { return e.cause }
func (e *dailyAgentFailure) ErrorResult() any { return e.report }

func (d *dailySession) selectAgentRules() error {
	for _, key := range []string{"work_unit_id", "work_unit", "current_work_unit"} {
		unit := kernel.Text(d.record[key])
		if unit == "" {
			continue
		}
		if d.agentUnit != "" && d.agentUnit != unit {
			return d.info("当前记录的工作单元声明冲突")
		}
		d.agentUnit = unit
	}
	if d.agentUnit == "" {
		if d.report.DeliveryPath == "governed" {
			return d.info("需要当前正式工作单元，不能从 daily 路由推测")
		}
		d.agentUnit = "work-unit.entry-triage"
	}
	d.agentRules = d.policy
	if d.agentUnit == "work-unit.entry-triage" {
		return nil
	}
	routes, _ := kernel.Object(d.orchestration["work_unit_routes"])
	rule, ok := kernel.Object(routes[d.agentUnit])
	if !ok {
		return d.info("当前工作单元缺少已登记规则: " + d.agentUnit)
	}
	d.agentRules = map[string]any{"delivery_path": d.policy, "work_unit": rule}
	return nil
}

func (d *dailySession) agentView() map[string]any {
	next := "investigate-missing-facts"
	if d.report.DeliveryPath == "daily" || d.report.DeliveryPath == "standard" {
		next = "continue-single-task-record"
	}
	if d.report.StandardRequired {
		next = "add-spec-lite-and-standard-decision"
	}
	if d.report.DeliveryPath == "governed" {
		next = "resume-trusted-governed-work-unit"
	}
	bindings, _ := json.Marshal(d.report.Inputs)
	sources := map[string]any{"read_set": "sha256:" + safefs.Digest(bindings), "orchestration_contract": d.orchestrationDigest, "implementation_base": d.base, "diff": d.report.DiffDigest}
	// Preserve the complete applicable policy; a byte budget must never truncate rules.
	return map[string]any{
		"schema_version": 1, "view": "agent", "route": d.report.DeliveryPath,
		"work_unit":   d.agentUnit,
		"next_action": next, "rules": d.agentRules, "source_digests": sources,
		"blockers": d.report.Diagnostics, "reasons": d.report.Reasons,
		"required_checks": d.report.RequiredChecks, "task_ref": d.taskRef,
		"read_only": true, "execution_allowed": false, "approval_created": false, "execution_authorization": "not-evaluated",
	}
}
