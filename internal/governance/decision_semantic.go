package governance

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"strings"
)

func init() {
	registerSemanticValidator("user-decision", verifyUserDecisionSemantic)
	registerSemanticValidator("checkpoint-user-decisions", verifyCheckpointUserDecisionsSemantic)
}

// Public current verification requires a separate consumer's expectation. The
// legacy script's record.request.items default is intentionally not authority
// for this API. Internal consumers already supply their own requirement maps.
func decisionPublicRequirements(s *kernel.SemanticSession, opts map[string]string) ([]map[string]any, bool, error) {
	cpRef := opts["checkpoint"]
	if cpRef == "" {
		cpRef = s.CheckpointRef
	}
	count := 0
	for _, ref := range []string{opts["requirements"], cpRef, opts["task"]} {
		if ref != "" {
			count++
		}
	}
	// 显式的 --from-record：调用方声明「待验记录自身的事项就是要核验的范围」，对应旧命令行省略 --requirements 的行为；
	// 只在单条记录、非延续证明、且没有其他独立范围时可用，默认仍不从记录反推。
	if opts["from-record"] == "true" {
		if count != 0 || opts["continuation"] == "true" {
			return nil, false, kernel.ApFail(s, "USER_DECISION_CONTEXT_REQUIRED", "--from-record 不能与 requirements、checkpoint、task 或 continuation 同用")
		}
		return decisionRequirementsFromRecord(s, opts)
	}
	if count != 1 {
		return nil, false, kernel.ApFail(s, "USER_DECISION_CONTEXT_REQUIRED", "须明确提供唯一当前 requirements、checkpoint 或正式 task，不从待验记录反推范围")
	}
	var value any
	collection := false
	if ref := opts["requirements"]; ref != "" {
		b, e := s.Bytes(ref)
		if e != nil {
			return nil, false, e
		}
		value, e = schema.Parse(b)
		if e != nil {
			return nil, false, s.Unavailable("INPUT", e.Error())
		}
	} else if cpRef != "" {
		cp, e := s.Doc(cpRef)
		if e != nil {
			return nil, false, e
		}
		if e = s.ValidateSchema(".template-spec/process/schemas/lifecycle-checkpoint.schema.json", cp); e != nil {
			return nil, false, e
		}
		identity, e := s.Doc("yss-project.yaml")
		if e != nil {
			return nil, false, e
		}
		if cp["repository_mode"] != identity["repository_mode"] {
			return nil, false, kernel.ApFail(s, "IDENTITY", "当前 checkpoint 与根仓库身份矛盾")
		}
		currentRef := cpRef
		if v, local, err := s.ReferenceView(cpRef); err != nil {
			return nil, false, err
		} else if v == s.V {
			currentRef = local
		}
		if e = s.CurrentAsset(currentRef); e != nil {
			return nil, false, e
		}
		value = kernel.ApMap(cp["human_review"])["user_decisions"]
		if len(kernel.ApArray(value)) == 0 {
			value = cp["user_decisions"]
		}
		collection = true
	} else {
		if e := s.Verify("task", opts["task"], nil); e != nil {
			return nil, false, e
		}
		task, e := s.Doc(opts["task"])
		if e != nil {
			return nil, false, e
		}
		value = task["user_decisions"]
		collection = true
	}
	if single, isObject := value.(map[string]any); isObject && !collection {
		value = []any{single} // requirements 文件可以只写一项
	}
	array, ok := value.([]any)
	if !ok || len(array) == 0 {
		return nil, collection, kernel.ApFail(s, "USER_DECISION_CONTEXT_REQUIRED", "当前消费者必须提供非空事项数组")
	}
	rows := kernel.ApRows(array)
	if len(rows) != len(array) {
		return nil, collection, kernel.ApFail(s, "user-decision-scope-mismatch", "当前事项必须全部为对象")
	}
	seen := map[string]bool{}
	for _, want := range rows {
		if strings.TrimSpace(kernel.ApText(want["boundary"])) == "" || strings.TrimSpace(kernel.ApText(want["subject_ref"])) == "" || !kernel.ApIsArray(want["scope"]) || len(kernel.ApArray(want["scope"])) == 0 {
			return nil, collection, kernel.ApFail(s, "user-decision-scope-mismatch", "当前事项缺少独立边界、主体或范围")
		}
		scope := map[string]bool{}
		for _, raw := range kernel.ApArray(want["scope"]) {
			id := strings.TrimSpace(kernel.ApText(raw))
			if id == "" || scope[id] {
				return nil, collection, kernel.ApFail(s, "user-decision-scope-mismatch", "当前事项范围必须为非空且不重复的字符串")
			}
			scope[id] = true
		}
		key := string(kernel.ApCanonical(want))
		if seen[key] {
			return nil, collection, kernel.ApFail(s, "user-decision-scope-mismatch", "当前事项重复")
		}
		seen[key] = true
	}
	return rows, collection, nil
}

func decisionSameRef(s *kernel.SemanticSession, left, right string) (bool, error) {
	lv, lr, e := s.ReferenceView(left)
	if e != nil {
		return false, e
	}
	rv, rr, e := s.ReferenceView(right)
	if e != nil {
		return false, e
	}
	if _, e = safefs.Path(lv.Root, lr); e != nil {
		return false, s.Unavailable("PATH", e.Error())
	}
	if _, e = safefs.Path(rv.Root, rr); e != nil {
		return false, s.Unavailable("PATH", e.Error())
	}
	return lv.Root == rv.Root && lr == rr, nil
}

func verifyUserDecisionSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	expected, collection, e := decisionPublicRequirements(s, opts)
	if e != nil {
		return e
	}
	continuation := opts["continuation"] == "true"
	selected := []map[string]any{}
	for _, want := range expected {
		key := "user_decision_ref"
		if continuation {
			key = "continuation_ref"
		}
		target := kernel.ApText(want[key])
		match := target == "" && !collection && !continuation
		if target != "" {
			match, e = decisionSameRef(s, target, ref)
			if e != nil {
				return e
			}
		}
		if !match {
			if collection {
				continue
			}
			return kernel.ApFail(s, "user-decision-subject-mismatch", "当前事项未指向待验原始记录或延续证明")
		}
		if kernel.ApTruthy(want["continuation_ref"]) && kernel.ApTruthy(want["user_decision_ref"]) {
			return kernel.ApFail(s, "user-decision-proof-conflict", "当前决定与批准延续不得同时提供")
		}
		if !continuation && kernel.ApTruthy(want["continuation_ref"]) {
			return kernel.ApFail(s, "user-decision-proof-conflict", "延续证明需要显式 --continuation")
		}
		selected = append(selected, want)
	}
	if len(selected) == 0 {
		return kernel.ApFail(s, "USER_DECISION_CONTEXT_REQUIRED", "当前消费者未登记待验记录或延续证明")
	}
	if continuation {
		for _, want := range selected {
			if _, e = kernel.AssertUserDecisionRequirementSemantic(s, want); e != nil {
				return e
			}
		}
		return nil
	}
	record, e := s.Doc(ref)
	if e != nil {
		return e
	}
	_, e = kernel.ValidateUserDecisionSemantic(s, record, selected)
	return e
}

func assertWorkUnitUserDecisionSemantic(s *kernel.SemanticSession, workUnit string, state map[string]any) error {
	policy := kernel.ApMap(s.Roles["user_decision_policy"])
	boundaries := kernel.ApStrings(kernel.ApMap(policy["work_unit_gates"])[workUnit])
	if boundary := kernel.ApText(kernel.ApMap(policy["work_units"])[workUnit]); boundary != "" {
		boundaries = append(boundaries, boundary)
	}
	for _, boundary := range boundaries {
		notApplicable := kernel.ApFind(state["user_decision_not_applicable"], "boundary", boundary)
		if workUnit == "work-unit.technical-analysis" && strings.TrimSpace(kernel.ApText(notApplicable["reason"])) != "" {
			continue
		}
		requirement := kernel.ApFind(state["user_decisions"], "boundary", boundary)
		if requirement == nil {
			return kernel.ApFail(s, "user-decision-response-required", boundary)
		}
		if _, e := kernel.AssertUserDecisionRequirementSemantic(s, requirement); e != nil {
			return e
		}
	}
	return nil
}

func verifyCheckpointUserDecisionsSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	checkpoint, e := s.Doc(ref)
	if e != nil {
		return e
	}
	if kernel.ApText(checkpoint["repository_mode"]) != "project-instance" {
		return nil
	}
	status := kernel.ApText(checkpoint["status"])
	advancing := status == "running" || status == "completed" || kernel.ApText(checkpoint["mode"]) == "resume" && status == "routing"
	if !advancing {
		return nil
	}
	review := kernel.ApMap(checkpoint["human_review"])
	state := map[string]any{"user_decisions": review["user_decisions"], "user_decision_not_applicable": review["not_applicable"]}
	completedUnit := kernel.ApText(kernel.ApMap(checkpoint["stage_trace"])["completed_work_unit"])
	if completedUnit != "" {
		if e = assertWorkUnitUserDecisionSemantic(s, completedUnit, state); e != nil {
			return e
		}
	}
	if kernel.ApText(checkpoint["next_work_unit"]) == "work-unit.slice-implementation" {
		implementation := kernel.ApCopy(kernel.ApMap(review["implementation"]))
		for k, v := range state {
			implementation[k] = v
		}
		if e = assertImplementationDecisionSemantic(s, implementation); e != nil {
			return e
		}
	}
	for _, requirement := range kernel.ApRows(review["required_decisions"]) {
		if _, e = kernel.AssertUserDecisionRequirementSemantic(s, requirement); e != nil {
			return e
		}
	}
	if external := kernel.ApMap(review["external_input"]); external != nil {
		requirement := kernel.ApCopy(external)
		requirement["boundary"] = "external-input"
		if _, e = kernel.AssertUserDecisionRequirementSemantic(s, requirement); e != nil {
			return e
		}
	}
	if status == "completed" {
		if len(kernel.ApArray(checkpoint["blockers"])) > 0 {
			return kernel.ApFail(s, "LIFECYCLE_CONTROL", "仍有阻塞，不可完成")
		}
		for _, value := range kernel.ApMap(checkpoint["gates"]) {
			gate := kernel.ApMap(value)
			if !kernel.ApContains([]string{"approved", "not-applicable"}, kernel.ApText(gate["status"])) {
				return kernel.ApFail(s, "LIFECYCLE_CONTROL", "仍有未通过门禁，不可完成")
			}
		}
		completionGate := "gate.delivery-accepted"
		if kernel.ApFind(s.Registry["gates"], "id", "gate.strategic-design-handoff-approved") != nil {
			completionGate = "gate.strategic-design-handoff-approved"
		}
		if kernel.ApText(kernel.ApMap(kernel.ApMap(checkpoint["gates"])[completionGate])["status"]) != "approved" {
			return kernel.ApFail(s, "LIFECYCLE_CONTROL", "阶段完成须当前交付或战略交接验收，不等于发布授权")
		}
	}
	return nil
}

func decisionRequirementsFromRecord(s *kernel.SemanticSession, opts map[string]string) ([]map[string]any, bool, error) {
	ref := opts["file"]
	if ref == "" {
		ref = opts["arg0"]
	}
	record, e := s.Doc(ref)
	if e != nil {
		return nil, false, e
	}
	request := kernel.ApMap(record["request"])
	items := kernel.ApArray(request["items"])
	rows := []map[string]any{}
	for _, raw := range items {
		item := kernel.ApMap(raw)
		subject := kernel.ApMap(item["subject"])
		rows = append(rows, map[string]any{"boundary": item["boundary"], "subject_ref": subject["ref"], "scope": item["scope"], "user_decision_ref": ref})
	}
	if len(rows) == 0 {
		return nil, false, kernel.ApFail(s, "USER_DECISION_CONTEXT_REQUIRED", "待验记录没有可核验的事项")
	}
	return rows, false, nil
}
