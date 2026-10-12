package governance

import (
	"context"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"sort"
)

func registerSemanticValidator(kind string, fn kernel.SemanticValidator) {
	if kind == "" || fn == nil || kernel.SemanticValidators[kind] != nil {
		panic("duplicate or invalid semantic validator: " + kind)
	}
	kernel.SemanticValidators[kind] = fn
}

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

func semSameSet(a, b any) bool {
	x, y := kernel.SemStrings(a), kernel.SemStrings(b)
	if len(x) == 0 || len(x) != len(y) {
		return false
	}
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] == "" || x[i] != y[i] || i > 0 && x[i] == x[i-1] {
			return false
		}
	}
	return true
}

func semanticOptions(args map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range args {
		out[k] = v
	}
	return out
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
	if group == "handoff" && args["kind"] == "package" {
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
		allowed := map[string][]string{"contract": {"slice", "scaffold", "task", "frontend-delivery"}, "evidence": {"approval", "user-decision", "verification"}, "handoff": {"package", "consumption"}}
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
			"slice": {"approval-ref", "unit"}, "verification": {"approval-ref", "task"},
			"frontend-delivery": {"slice", "phase", "unit"},
			"task":              {"history"}, "approval": {"require-approved", "history", "gate", "boundary", "task"},
			"user-decision":   {"requirements", "continuation", "from-record", "task"},
			"handoff-package": {"package"}, "handoff-consumption": {"consumer", "slice"}, "checkpoint": {"history"},
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
		for flag, kinds := range map[string][]string{"requirements": {"user-decision"}, "continuation": {"user-decision"}, "from-record": {"user-decision"}, "gate": {"approval"}, "boundary": {"approval"}, "task": {"approval", "user-decision", "verification"}, "consumer": {"handoff-consumption"}, "package": {"handoff-package"}, "require-approved": {"approval"}, "history": {"approval", "task", "checkpoint"}} {
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
		if kind == "handoff-package" {
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
		err = s.Authorities()
	}
	if err == nil {
		_, err = s.ContextContract()
	}
	if err == nil {
		err = s.Verify(kind, ref, args)
	}
	if err != nil {
		s.Diagnostic(ref, err)
	}
	if final := s.Finish(); final != nil {
		s.Diagnostic(ref, final)
		err = final
	}
	s.Report.Inputs = s.Inputs()
	s.Report.GitInputs = s.GitBindings()
	s.Report.Checks = s.CollectedChecks()
	if err != nil {
		return s.Report, &semanticFailure{cause: err, report: s.Report}
	}
	if kind == "frontend-delivery" && s.Report.Coverage["delivery_mode"] == "local-approved-assets" {
		s.Report.Coverage["inputs_current"] = true
	}
	return s.Report, nil
}
