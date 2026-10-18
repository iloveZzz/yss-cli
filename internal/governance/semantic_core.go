package governance

import "github.com/iloveZzz/yss-cli/internal/governance/approval"

import (
	"context"
	"github.com/iloveZzz/yss-cli/internal/governance/contracts"
	"github.com/iloveZzz/yss-cli/internal/governance/handoff"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"sort"
)

type semanticFailure struct {
	cause  error
	report *kernel.SemanticReport
}

func (e *semanticFailure) Error() string { return e.cause.Error() }

func (e *semanticFailure) Unwrap() error { return e.cause }

func (e *semanticFailure) ErrorResult() any { return e.report }

// InputFailureReport keeps the readonly contract even before a project view
// can be opened (for example, malformed arguments or conflicting identity).
func InputFailureReport(ctx context.Context, root, kind string, err error) *kernel.SemanticReport {
	s := kernel.NewSemanticSession(ctx, root, nil)
	s.Report.Kind = kind
	s.Diagnostic("inputs", err)
	s.Report.Status = "error"
	return s.Report
}

func semanticRun(ctx context.Context, group, action, root string, args map[string]string) (any, error) {
	s := kernel.NewSemanticSession(ctx, root, args)
	ref := args["file"]
	if ref == "" {
		ref = args["arg0"]
	}
	if group == "lifecycle" {
		ref = args["checkpoint"]
		if ref == "" {
			ref = args["file"]
		}
	}
	if group == "handoff" && (args["kind"] == "package" || args["kind"] == "backend-delivery" || args["kind"] == "delivery") {
		ref = args["package"]
	}
	kind := args["kind"]
	switch group {
	case "lifecycle":
		kind = "checkpoint"
	case "handoff":
		kind = "handoff-" + kind
	}
	err := s.Guard()
	if err == nil {
		allowed := map[string][]string{"contract": {"governance-policy", "architecture-evidence", "slice-draft", "slice-execution-result", "task-baseline", "task-candidate", "task-selection", "governance-write", "continuation-comparison", "work-unit-user-decision", "slice", "scaffold", "task", "openapi-validation", "backend-delivery-terminal", "backend-review", "scaffold-decisions", "slice-preflight", "business-tickets", "plan-review-task", "plan-review-control", "plan-entry", "implementation-scope", "checkpoint-user-decisions", "spec-baseline-binding", "scaffold-user-decision", "checkpoint-approvals", "gate-checks"}, "evidence": {"approval", "user-decision", "verification", "context-reconciliation"}, "handoff": {"package", "consumption", "backend-delivery", "source", "delivery", "source-approval"}}
		if kinds, ok := allowed[group]; ok && !kernel.SemHas(kinds, args["kind"]) {
			err = s.Unavailable("ARGUMENT", "未知或不适用于该接口的领域类型: "+args["kind"])
		}
	}
	if err == nil {
		allowedFlags := map[string]bool{}
		for _, flag := range []string{"root", "target-dir", "profile", "json", "kind", "file", "arg0", "checkpoint", "home", "run-dir", "tool-root", "template-checkout"} {
			allowedFlags[flag] = true
		}
		for _, flag := range map[string][]string{
			"slice-execution-result": {"task"}, "slice": {"approval-ref", "unit"}, "slice-preflight": {"approval-ref", "unit"}, "verification": {"approval-ref", "task"},
			"openapi-validation": {"allow-template"}, "scaffold-decisions": {"allow-template"},
			"business-tickets": {"mode", "review-ref"},
			"gate-checks":      {"gate"}, "spec-baseline-binding": {"no-reconciliation"}, "handoff-source-approval": {"gate", "source-context-snapshot"}, "handoff-delivery": {"package"},
			"task": {"history", "task-ref"}, "approval": {"require-approved", "history", "gate", "boundary", "task", "expected", "binding"},
			"user-decision":  {"requirements", "continuation", "from-record", "task"},
			"handoff-source": {"history"}, "handoff-package": {"package"}, "handoff-backend-delivery": {"package"}, "handoff-consumption": {"consumer", "slice"}, "checkpoint": {"history"},
		}[kind] {
			allowedFlags[flag] = true
		}
		for flag := range args {
			if !allowedFlags[flag] {
				err = s.Unavailable("ARGUMENT", "该公开治理接口不接受参数: --"+flag)
				break
			}
		}
	}
	if err == nil {
		for flag, kinds := range map[string][]string{"mode": {"business-tickets"}, "review-ref": {"business-tickets"}, "requirements": {"user-decision"}, "continuation": {"user-decision"}, "from-record": {"user-decision"}, "no-reconciliation": {"spec-baseline-binding"}, "gate": {"approval", "handoff-source-approval", "gate-checks"}, "source-context-snapshot": {"handoff-source-approval"}, "boundary": {"approval"}, "task": {"approval", "user-decision", "verification", "slice-execution-result"}, "consumer": {"handoff-consumption"}, "package": {"handoff-package", "handoff-backend-delivery", "handoff-delivery"}, "require-approved": {"approval"}, "expected": {"approval"}, "binding": {"approval"}, "history": {"approval", "task", "checkpoint", "handoff-source"}, "allow-template": {"openapi-validation", "scaffold-decisions"}} {
			if _, provided := args[flag]; provided && !kernel.SemHas(kinds, kind) {
				err = s.Unavailable("ARGUMENT", "该领域类型不接受参数: --"+flag)
				break
			}
		}
	}
	if err == nil {
		selectors := []string{"file", "arg0"}
		if group == "lifecycle" {
			selectors = append(selectors, "checkpoint")
		}
		if kind == "handoff-package" || kind == "handoff-backend-delivery" || kind == "handoff-delivery" {
			selectors = append(selectors, "package")
		}
		selected := ""
		for _, key := range selectors {
			value := args[key]
			if value != "" {
				if selected != "" && selected != value {
					err = s.Unavailable("ARGUMENT", "同一资产的路径参数矛盾")
					break
				}
				selected = value
			}
		}
	}
	if err == nil && args["gate"] != "" && args["boundary"] != "" && args["gate"] != args["boundary"] {
		err = s.Unavailable("ARGUMENT", "gate 与 boundary 参数矛盾")
	}
	if err == nil && args["schema"] != "" {
		err = s.Unavailable("ARGUMENT", "verify 不允许替换领域 Schema")
	}
	if err == nil && ref == "" {
		err = s.Unavailable("ARGUMENT", "语义校验需要明确资产路径")
	}
	if err == nil {
		if value, provided := args["source-context-snapshot"]; provided {
			if value != "true" && value != "false" {
				err = s.Unavailable("ARGUMENT", "source-context-snapshot 必须为布尔开关")
			} else if value == "true" {
				// Only this explicit source-approval mode aliases the fixed readonly
				// glossary. Missing CONTEXT.md alone never selects a source view.
				s.Aliases["CONTEXT.md"] = "source-context.snapshot.md"
			}
		}
	}
	if err == nil && args["history"] == "true" {
		var original *kernel.SemanticSession
		original, err = s.OriginalGovernanceSource()
		if err == nil && original != nil {
			s = original
		}
	}
	if err == nil {
		if kind == "handoff-source" {
			err = handoff.HandoffSourceAuthorities(s, ref)
		} else {
			err = s.Authorities()
		}
	}
	if err == nil {
		_, err = s.ContextContract()
	}
	if err == nil {
		err = s.Verify(kind, ref, args)
		if err == nil {
			keys := map[string]string{"plan-entry": "plan_entry", "implementation-scope": "implementation_scope", "checkpoint-user-decisions": "checkpoint_user_decisions", "scaffold-user-decision": "scaffold_user_decision"}
			if key := keys[kind]; key != "" {
				kernel.SemanticCoverage(s, key, map[string]any{"result": "passed"})
			}
		}
	}
	if err != nil {
		s.Diagnostic(ref, err)
	}
	if final := s.Finish(); final != nil {
		s.Diagnostic(ref, final)
		err = final
	}
	// A domain report cannot outlive a rejected authority or final input check.
	if err != nil {
		if report, ok := s.Report.Coverage["business_tickets"].(map[string]any); ok && report["status"] == "passed" {
			report["status"] = "blocked"
			report["diagnostics"] = append(kernel.SemList(report["diagnostics"]), map[string]any{"code": kernel.SemanticCode(err), "message": err.Error(), "source_ref": ref, "locator": nil, "recovery": "修复当前输入、能力或证据后重新校验"})
		}
	}
	s.Report.ValidationDependencies = s.ValidationDependencies()
	s.Report.Inputs = s.Inputs()
	s.Report.GitInputs = s.GitBindings()
	s.Report.Checks = s.CollectedChecks()
	if err != nil {
		return s.Report, &semanticFailure{cause: err, report: s.Report}
	}
	return s.Report, nil
}

func init() {
	kernel.RegisterSemanticValidator("plan-entry", verifyPlanEntrySemantic)
	kernel.RegisterRoleIndependentValidator("plan-entry", verifyPlanEntrySemantic)
	kernel.RegisterSemanticValidator("gate-checks", verifyGateChecksPublic)
	kernel.RegisterRoleIndependentValidator("gate-checks", verifyGateChecksPublic)
	kernel.RegisterSemanticValidator("checkpoint-approvals", verifyCheckpointApprovalsPublic)
	kernel.RegisterRoleIndependentValidator("checkpoint-approvals", verifyCheckpointApprovalsPublic)
	kernel.RegisterSemanticValidator("implementation-scope", func(s *kernel.SemanticSession, ref string, opts map[string]string) error {
		state, err := s.Doc(ref)
		if err != nil {
			return err
		}
		return contracts.AssertImplementationScopeSemantic(s, state)
	})
	kernel.RegisterSemanticValidator("scaffold-user-decision", func(s *kernel.SemanticSession, ref string, opts map[string]string) error {
		decision, err := s.Doc(ref)
		if err != nil {
			return err
		}
		return contracts.ContractScaffoldUserDecision(s, decision)
	})
	kernel.RegisterSemanticValidator("spec-baseline-binding", handoff.VerifySpecBaselineBindingSemantic)
}

func verifyGateChecksPublic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	state, e := s.Doc(ref)
	if e != nil {
		return e
	}
	gate := opts["gate"]
	if gate == "" {
		return s.Unavailable("ARGUMENT", "当前门禁检查需要 --gate")
	}
	if e = approval.GateChecks(s, state, ref, gate); e != nil {
		return e
	}
	// Project only the already validated dependency graph in consumer order.
	checks := []string{}
	seen := map[string]bool{}
	var collect func(string)
	collect = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		checks = append(checks, id)
		item := kernel.SemMap(kernel.SemMap(state["checks"])[id])
		if item["status"] != "not-applicable" {
			definition := kernel.SemanticDefinition(s.Registry, "checks", id)
			for _, dep := range kernel.SemStrings(definition["requires_checks"]) {
				collect(dep)
			}
		}
	}
	definition := kernel.SemanticDefinition(s.Registry, "gates", gate)
	for _, id := range kernel.SemStrings(definition["requires_checks"]) {
		collect(id)
	}
	kernel.SemanticCoverage(s, "gate_checks", map[string]any{"gate": gate, "checks": checks, "result": "passed"})
	return nil
}
func verifyCheckpointApprovalsPublic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	state, e := s.Doc(ref)
	if e != nil {
		return e
	}
	gates := kernel.SemMap(state["gates"])
	keys := []string{}
	for key := range gates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, gate := range keys {
		if kernel.SemanticDefinition(s.Registry, "gates", gate) == nil {
			return s.Reject("APPROVAL_CURRENT_INVALID", "未知或已退役门禁: "+gate)
		}
		item, ok := gates[gate].(map[string]any)
		if !ok {
			return s.Reject("APPROVAL_CONTEXT_REQUIRED", "门禁状态必须是对象")
		}
		if item["status"] == "approved" {
			if e = approval.GateChecks(s, state, ref, gate); e != nil {
				return e
			}
		}
	}
	kernel.SemanticCoverage(s, "checkpoint_approvals", map[string]any{"result": "passed"})
	return nil
}
