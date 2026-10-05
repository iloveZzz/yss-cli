package governance

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

const decisionSchemaRef = ".template-spec/process/schemas/user-decision.schema.json"

type decisionValidated struct {
	itemID, boundary, principal string
	scope                       []string
}
type decisionValidation struct {
	requestID string
	validated []decisionValidated
	outcomes  map[string]map[string]any
}

func init() {
	registerSemanticValidator("user-decision", verifyUserDecisionSemantic)
	registerSemanticValidator("checkpoint-user-decisions", verifyCheckpointUserDecisionsSemantic)
}
func decisionRequestDigest(request map[string]any) string {
	snapshot := apCopy(request)
	delete(snapshot, "presented_source")
	return apDigest(snapshot)
}
func decisionTime(value any) (time.Time, bool) {
	s := apText(value)
	for _, layout := range []string{time.RFC3339Nano, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, "2006-01-02", "2006-01-02T15:04:05"} {
		t, e := time.Parse(layout, s)
		if e == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
func decisionMessage(s *semanticSession, source map[string]any) (map[string]any, error) {
	policy := apMap(s.roles["user_decision_policy"])
	if source == nil || !apContains(policy["source_kinds"], apText(source["kind"])) {
		return nil, apFail(s, "user-decision-source-invalid", "需要原始消息、会话导出或用户确认文件")
	}
	ref := apText(source["ref"])
	raw, e := s.bytes(ref)
	if e != nil {
		return nil, e
	}
	if apDigest(raw) != source["digest"] {
		return nil, apFail(s, "user-decision-source-changed", ref)
	}
	capture, e := s.doc(ref)
	if e != nil {
		return nil, e
	}
	if capture["source_kind"] != source["kind"] || !apIsArray(capture["messages"]) {
		return nil, apFail(s, "user-decision-source-invalid", "来源不是原始消息封装")
	}
	var match map[string]any
	count := 0
	var preceding map[string]any
	for _, message := range apRows(capture["messages"]) {
		if message["id"] == source["message_id"] {
			match = apCopy(message)
			match["_preceding_message"] = preceding
			count++
		}
		preceding = message
	}
	if count != 1 {
		return nil, apFail(s, "user-decision-source-invalid", "消息 ID 缺失或重复")
	}
	for _, field := range []string{"principal_ref", "original_ref", "sent_at", "text"} {
		if strings.TrimSpace(apText(match[field])) == "" {
			return nil, apFail(s, "user-decision-missing", "原始消息 "+field)
		}
	}
	if _, ok := decisionTime(match["sent_at"]); !ok {
		return nil, apFail(s, "user-decision-source-invalid", "原始消息时间无效")
	}
	return match, nil
}

func renderDecisionRequestSemantic(request map[string]any) string {
	lines := []string{fmt.Sprintf("决定请求 %s (%s)", apText(request["id"]), decisionRequestDigest(request)), "提问者：" + apText(request["requester_ref"])}
	for _, item := range apRows(request["items"]) {
		subject := apMap(item["subject"])
		basis := []string{}
		for _, asset := range apRows(item["basis"]) {
			basis = append(basis, fmt.Sprintf("%s@%s (%s)", apText(asset["ref"]), apText(asset["version"]), apText(asset["digest"])))
		}
		basisText := strings.Join(basis, "；")
		if basisText == "" {
			basisText = "无额外依据"
		}
		risks := strings.Join(apStrings(item["risks"]), "；")
		if risks == "" {
			risks = "已评估，无已知残余风险"
		}
		lines = append(lines, fmt.Sprintf("事项 %s / %s：%s", apText(item["id"]), apText(item["boundary"]), apText(item["title"])), fmt.Sprintf("资产：%s；版本：%s；摘要：%s", apText(subject["ref"]), apText(subject["version"]), apText(subject["digest"])), "依据："+basisText, "关键变化："+apText(item["changes"]), "范围："+strings.Join(apStrings(item["scope"]), "；"), "风险："+risks, "推荐方案："+apText(item["recommendation"]), "批准后动作："+strings.Join(apStrings(item["next_actions"]), "；"), "回复负责人："+apText(item["responder_ref"]))
	}
	lines = append(lines, "请明确批准、拒绝或提出修改；多事项回复须说明事项，或明确批准以上全部事项。")
	return strings.Join(lines, "\n")
}

var decisionApprovePattern = regexp.MustCompile(`(?i)(同意|批准|确认|接受|可以|好的|^好[。！!\s]*$|\b(approve|approved|agree|agreed|accept|yes|ok)\b)`)
var decisionRejectPattern = regexp.MustCompile(`(?i)(不同意|不批准|拒绝|反对|不要|需[要]?修改|请修改|暂不|reject|decline|do not approve|don't approve|disagree)`)
var decisionAllQuestion = regexp.MustCompile(`(?i)是否\s*(明确)?(确认|批准|同意|接受)[^\n]{0,32}(以上)?全部\s*(\d+|[一二三四五六七八九十]+)?\s*(项|事项)`)
var decisionShortApproval = regexp.MustCompile(`(?i)^(确认|同意|批准|接受|可以|好的|好|yes|ok)[。.!！\s]*$`)
var decisionContinue = regexp.MustCompile(`(?i)^(继续|continue|go ahead)[。.!！\s]*$`)
var decisionAllText = regexp.MustCompile(`(?i)(全部|所有|\ball\b)`)

func validateUserDecisionSemantic(s *semanticSession, record map[string]any, expected []map[string]any) (decisionValidation, error) {
	result := decisionValidation{outcomes: map[string]map[string]any{}}
	if e := s.validateSchema(decisionSchemaRef, record); e != nil {
		return result, e
	}
	request := apMap(record["request"])
	result.requestID = apText(request["id"])
	original, e := decisionMessage(s, apMap(request["requester_source"]))
	if e != nil {
		return result, e
	}
	if apText(original["actor_kind"]) != "biological-human" || original["principal_ref"] != request["requester_ref"] {
		return result, apFail(s, "user-decision-responder-mismatch", "提问者来源不匹配")
	}
	presentation, e := decisionMessage(s, apMap(request["presented_source"]))
	if e != nil {
		return result, e
	}
	fields := []string{}
	for _, item := range apRows(request["items"]) {
		subject := apMap(item["subject"])
		fields = append(fields, apText(item["title"]), apText(subject["ref"]), apText(subject["version"]), apText(item["changes"]), apText(item["recommendation"]))
		for _, k := range []string{"scope", "risks", "next_actions"} {
			fields = append(fields, apStrings(item[k])...)
		}
		for _, asset := range apRows(item["basis"]) {
			fields = append(fields, apText(asset["ref"]), apText(asset["version"]))
		}
	}
	if apText(presentation["text"]) != renderDecisionRequestSemantic(request) {
		for _, field := range fields {
			if !strings.Contains(apText(presentation["text"]), field) {
				return result, apFail(s, "user-decision-presentation-mismatch", "原始展示未覆盖当前资产、版本、变化、风险、范围和后续动作")
			}
		}
	}
	requestTime, _ := decisionTime(original["sent_at"])
	presentedTime, _ := decisionTime(presentation["sent_at"])
	if presentedTime.Before(requestTime) {
		return result, apFail(s, "user-decision-source-invalid", "展示时间早于用户请求")
	}
	items := map[string]map[string]any{}
	for _, item := range apRows(request["items"]) {
		id := apText(item["id"])
		if items[id] != nil {
			return result, apFail(s, "user-decision-ambiguous", "事项 ID 重复")
		}
		items[id] = item
		if item["responder_ref"] != request["requester_ref"] {
			delegated, e := decisionMessage(s, apMap(item["delegation_source"]))
			if e != nil {
				return result, e
			}
			delegatedTime, _ := decisionTime(delegated["sent_at"])
			if apText(delegated["actor_kind"]) != "biological-human" || delegated["principal_ref"] != request["requester_ref"] || !strings.Contains(apText(delegated["text"]), apText(item["responder_ref"])) || !strings.Contains(apText(delegated["text"]), id) || delegatedTime.After(presentedTime) {
				return result, apFail(s, "user-decision-delegation-invalid", "负责人须有提问者在展示前指向该事项的指定原文")
			}
		}
	}
	messageIDs := map[string]bool{}
	var previousTime time.Time
	for _, response := range apRows(record["responses"]) {
		sourceSpec := apMap(response["source"])
		source, e := decisionMessage(s, sourceSpec)
		if e != nil {
			return result, e
		}
		identity := apText(sourceSpec["ref"]) + "#" + apText(sourceSpec["message_id"])
		if messageIDs[identity] {
			return result, apFail(s, "user-decision-ambiguous", "回复消息重复")
		}
		messageIDs[identity] = true
		preceding := apMap(source["_preceding_message"])
		if source["reply_to"] != presentation["original_ref"] && source["request_digest"] != decisionRequestDigest(request) && !(preceding["original_ref"] == presentation["original_ref"] && preceding["text"] == presentation["text"]) {
			return result, apFail(s, "user-decision-subject-mismatch", "原始回复没有指向当前请求的来源关联")
		}
		if apText(source["actor_kind"]) != "biological-human" || source["principal_ref"] != response["principal_ref"] || source["sent_at"] != response["responded_at"] || source["text"] != response["text"] {
			return result, apFail(s, "user-decision-responder-mismatch", "记录与原始回复身份、时间或原文不一致")
		}
		if response["request_digest"] != decisionRequestDigest(request) {
			return result, apFail(s, "user-decision-stale", "回复绑定的请求快照已变化")
		}
		responseTime, ok := decisionTime(response["responded_at"])
		if !ok || responseTime.Before(presentedTime) || !previousTime.IsZero() && responseTime.Before(previousTime) {
			return result, apFail(s, "user-decision-source-invalid", "回复须在展示之后并按原始时间排序")
		}
		previousTime = responseTime
		if apText(response["decision"]) == "approved" {
			explicit := apText(source["decision"]) == "approved" && apIsArray(source["item_ids"])
			for _, id := range apStrings(response["item_ids"]) {
				explicit = explicit && apContains(source["item_ids"], id)
			}
			contextualAll := apText(response["selection"]) == "explicit-all" && len(items) > 1 && decisionAllQuestion.MatchString(apText(presentation["text"])) && decisionShortApproval.MatchString(strings.TrimSpace(apText(source["text"])))
			if !explicit && (!decisionApprovePattern.MatchString(apText(source["text"])) || decisionRejectPattern.MatchString(apText(source["text"]))) {
				return result, apFail(s, "user-decision-ambiguous", "原文不能明确支持批准结论")
			}
			if !explicit && !contextualAll && apText(response["selection"]) == "explicit-all" && len(items) > 1 && !decisionAllText.MatchString(apText(source["text"])) {
				return result, apFail(s, "user-decision-ambiguous", "批量批准原文必须明确全部事项")
			}
			if !explicit && apText(response["selection"]) == "explicit-items" && len(items) > 1 {
				for _, id := range apStrings(response["item_ids"]) {
					if !strings.Contains(apText(source["text"]), id) && !strings.Contains(apText(source["text"]), apText(items[id]["title"])) {
						return result, apFail(s, "user-decision-ambiguous", "原文未指向所选事项")
					}
				}
			}
		}
		if apText(response["selection"]) == "single" && len(items) != 1 || len(items) > 1 && decisionContinue.MatchString(strings.TrimSpace(apText(response["text"]))) {
			return result, apFail(s, "user-decision-ambiguous", "多事项不能用单项回复或继续批准")
		}
		if apText(response["selection"]) == "explicit-all" {
			if len(apArray(response["item_ids"])) != len(items) {
				return result, apFail(s, "user-decision-ambiguous", "全部批准必须枚举全部事项")
			}
			for id := range items {
				if !apContains(response["item_ids"], id) {
					return result, apFail(s, "user-decision-ambiguous", "全部批准缺少事项")
				}
			}
		}
		for _, id := range apStrings(response["item_ids"]) {
			item := items[id]
			if item == nil {
				return result, apFail(s, "user-decision-subject-mismatch", id)
			}
			if item["responder_ref"] != response["principal_ref"] {
				return result, apFail(s, "user-decision-responder-mismatch", id)
			}
			if apText(response["decision"]) == "approved" {
				if len(apArray(response["approved_scope"])) == 0 {
					return result, apFail(s, "user-decision-scope-mismatch", id)
				}
				for _, scope := range apStrings(response["approved_scope"]) {
					covered := false
					for _, selected := range apStrings(response["item_ids"]) {
						covered = covered || apContains(items[selected]["scope"], scope)
					}
					if !covered {
						return result, apFail(s, "user-decision-scope-mismatch", id)
					}
				}
			}
			answer := apCopy(response)
			previous := result.outcomes[id]
			if apText(response["decision"]) == "approved" && apText(previous["decision"]) == "approved" {
				scopes := apStrings(previous["approved_scope"])
				for _, scope := range apStrings(response["approved_scope"]) {
					if !apContains(scopes, scope) {
						scopes = append(scopes, scope)
					}
				}
				answer["approved_scope"] = scopes
			}
			result.outcomes[id] = answer
		}
	}
	for _, want := range expected {
		boundary, ref := apText(want["boundary"]), apText(want["subject_ref"])
		if strings.TrimSpace(boundary) == "" || strings.TrimSpace(ref) == "" {
			return result, apFail(s, "user-decision-missing", "当前决定边界或资产引用缺失")
		}
		if len(apArray(want["scope"])) == 0 {
			return result, apFail(s, "user-decision-scope-mismatch", "当前批准范围缺失")
		}
		matches := []map[string]any{}
		for _, item := range apRows(request["items"]) {
			match := item["boundary"] == boundary && apMap(item["subject"])["ref"] == ref
			for _, scope := range apStrings(want["scope"]) {
				match = match && apContains(item["scope"], scope)
			}
			if match {
				matches = append(matches, item)
			}
		}
		if len(matches) != 1 {
			return result, apFail(s, "user-decision-subject-mismatch", boundary)
		}
		item := matches[0]
		if e = decisionCurrentAsset(s, apMap(item["subject"]), "user-decision-stale"); e != nil {
			return result, e
		}
		for _, asset := range apRows(item["basis"]) {
			if e = decisionCurrentAsset(s, asset, "user-decision-stale"); e != nil {
				return result, e
			}
		}
		answer := result.outcomes[apText(item["id"])]
		if answer == nil {
			return result, apFail(s, "user-decision-response-required", apText(item["id"]))
		}
		if apText(answer["decision"]) != "approved" {
			return result, apFail(s, "user-decision-not-approved", apText(item["id"])+": "+apText(answer["decision"]))
		}
		for _, scope := range apStrings(want["scope"]) {
			if !apContains(answer["approved_scope"], scope) {
				return result, apFail(s, "user-decision-scope-mismatch", apText(item["id"]))
			}
		}
		result.validated = append(result.validated, decisionValidated{apText(item["id"]), boundary, apText(answer["principal_ref"]), apStrings(want["scope"])})
	}
	return result, nil
}

func decisionCurrentAsset(s *semanticSession, asset map[string]any, code string) error {
	b, e := s.bytes(apText(asset["ref"]))
	if e != nil {
		return e
	}
	if apDigest(b) != asset["digest"] {
		return apFail(s, code, "当前资产过期: "+apText(asset["ref"]))
	}
	return nil
}
func assertUserDecisionRequirementSemantic(s *semanticSession, requirement map[string]any) (decisionValidation, error) {
	if requirement == nil {
		return decisionValidation{}, apFail(s, "user-decision-response-required", "缺少当前决定及其范围")
	}
	if apTruthy(requirement["user_decision_ref"]) && apTruthy(requirement["continuation_ref"]) {
		return decisionValidation{}, apFail(s, "user-decision-proof-conflict", "当前决定与批准延续不得同时提供")
	}
	if apTruthy(requirement["continuation_ref"]) {
		return assertDecisionContinuationSemantic(s, requirement)
	}
	ref := apText(requirement["user_decision_ref"])
	if ref == "" {
		return decisionValidation{}, apFail(s, "user-decision-missing", "user_decision_ref 缺失")
	}
	record, e := s.doc(ref)
	if e != nil {
		return decisionValidation{}, e
	}
	return validateUserDecisionSemantic(s, record, []map[string]any{requirement})
}

// Public current verification requires a separate consumer's expectation. The
// legacy script's record.request.items default is intentionally not authority
// for this API. Internal consumers already supply their own requirement maps.
func decisionPublicRequirements(s *semanticSession, opts map[string]string) ([]map[string]any, bool, error) {
	cpRef := opts["checkpoint"]
	if cpRef == "" {
		cpRef = s.checkpointRef
	}
	count := 0
	for _, ref := range []string{opts["requirements"], cpRef, opts["task"]} {
		if ref != "" {
			count++
		}
	}
	if count != 1 {
		return nil, false, apFail(s, "USER_DECISION_CONTEXT_REQUIRED", "须明确提供唯一当前 requirements、checkpoint 或正式 task，不从待验记录反推范围")
	}
	var value any
	collection := false
	if ref := opts["requirements"]; ref != "" {
		b, e := s.bytes(ref)
		if e != nil {
			return nil, false, e
		}
		value, e = schema.Parse(b)
		if e != nil {
			return nil, false, s.unavailable("INPUT", e.Error())
		}
	} else if cpRef != "" {
		cp, e := s.doc(cpRef)
		if e != nil {
			return nil, false, e
		}
		if e = s.validateSchema(".template-spec/process/schemas/lifecycle-checkpoint.schema.json", cp); e != nil {
			return nil, false, e
		}
		identity, e := s.doc("yss-project.yaml")
		if e != nil {
			return nil, false, e
		}
		if cp["repository_mode"] != identity["repository_mode"] {
			return nil, false, apFail(s, "IDENTITY", "当前 checkpoint 与根仓库身份矛盾")
		}
		currentRef := cpRef
		if v, local, err := s.referenceView(cpRef); err != nil {
			return nil, false, err
		} else if v == s.v {
			currentRef = local
		}
		if e = s.currentAsset(currentRef); e != nil {
			return nil, false, e
		}
		value = apMap(cp["human_review"])["user_decisions"]
		if len(apArray(value)) == 0 {
			value = cp["user_decisions"]
		}
		collection = true
	} else {
		if e := s.verify("task", opts["task"], nil); e != nil {
			return nil, false, e
		}
		task, e := s.doc(opts["task"])
		if e != nil {
			return nil, false, e
		}
		value = task["user_decisions"]
		collection = true
	}
	array, ok := value.([]any)
	if !ok || len(array) == 0 {
		return nil, collection, apFail(s, "USER_DECISION_CONTEXT_REQUIRED", "当前消费者必须提供非空事项数组")
	}
	rows := apRows(array)
	if len(rows) != len(array) {
		return nil, collection, apFail(s, "user-decision-scope-mismatch", "当前事项必须全部为对象")
	}
	seen := map[string]bool{}
	for _, want := range rows {
		if strings.TrimSpace(apText(want["boundary"])) == "" || strings.TrimSpace(apText(want["subject_ref"])) == "" || !apIsArray(want["scope"]) || len(apArray(want["scope"])) == 0 {
			return nil, collection, apFail(s, "user-decision-scope-mismatch", "当前事项缺少独立边界、主体或范围")
		}
		scope := map[string]bool{}
		for _, raw := range apArray(want["scope"]) {
			id := strings.TrimSpace(apText(raw))
			if id == "" || scope[id] {
				return nil, collection, apFail(s, "user-decision-scope-mismatch", "当前事项范围必须为非空且不重复的字符串")
			}
			scope[id] = true
		}
		key := string(apCanonical(want))
		if seen[key] {
			return nil, collection, apFail(s, "user-decision-scope-mismatch", "当前事项重复")
		}
		seen[key] = true
	}
	return rows, collection, nil
}

func decisionSameRef(s *semanticSession, left, right string) (bool, error) {
	lv, lr, e := s.referenceView(left)
	if e != nil {
		return false, e
	}
	rv, rr, e := s.referenceView(right)
	if e != nil {
		return false, e
	}
	if _, e = safefs.Path(lv.root, lr); e != nil {
		return false, s.unavailable("PATH", e.Error())
	}
	if _, e = safefs.Path(rv.root, rr); e != nil {
		return false, s.unavailable("PATH", e.Error())
	}
	return lv.root == rv.root && lr == rr, nil
}

func verifyUserDecisionSemantic(s *semanticSession, ref string, opts map[string]string) error {
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
		target := apText(want[key])
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
			return apFail(s, "user-decision-subject-mismatch", "当前事项未指向待验原始记录或延续证明")
		}
		if apTruthy(want["continuation_ref"]) && apTruthy(want["user_decision_ref"]) {
			return apFail(s, "user-decision-proof-conflict", "当前决定与批准延续不得同时提供")
		}
		if !continuation && apTruthy(want["continuation_ref"]) {
			return apFail(s, "user-decision-proof-conflict", "延续证明需要显式 --continuation")
		}
		selected = append(selected, want)
	}
	if len(selected) == 0 {
		return apFail(s, "USER_DECISION_CONTEXT_REQUIRED", "当前消费者未登记待验记录或延续证明")
	}
	if continuation {
		for _, want := range selected {
			if _, e = assertUserDecisionRequirementSemantic(s, want); e != nil {
				return e
			}
		}
		return nil
	}
	record, e := s.doc(ref)
	if e != nil {
		return e
	}
	_, e = validateUserDecisionSemantic(s, record, selected)
	return e
}

func assertWorkUnitUserDecisionSemantic(s *semanticSession, workUnit string, state map[string]any) error {
	policy := apMap(s.roles["user_decision_policy"])
	boundaries := apStrings(apMap(policy["work_unit_gates"])[workUnit])
	if boundary := apText(apMap(policy["work_units"])[workUnit]); boundary != "" {
		boundaries = append(boundaries, boundary)
	}
	for _, boundary := range boundaries {
		notApplicable := apFind(state["user_decision_not_applicable"], "boundary", boundary)
		if workUnit == "work-unit.technical-analysis" && strings.TrimSpace(apText(notApplicable["reason"])) != "" {
			continue
		}
		requirement := apFind(state["user_decisions"], "boundary", boundary)
		if requirement == nil {
			return apFail(s, "user-decision-response-required", boundary)
		}
		if _, e := assertUserDecisionRequirementSemantic(s, requirement); e != nil {
			return e
		}
	}
	return nil
}

func verifyCheckpointUserDecisionsSemantic(s *semanticSession, ref string, opts map[string]string) error {
	checkpoint, e := s.doc(ref)
	if e != nil {
		return e
	}
	if apText(checkpoint["repository_mode"]) != "project-instance" {
		return nil
	}
	status := apText(checkpoint["status"])
	advancing := status == "running" || status == "completed" || apText(checkpoint["mode"]) == "resume" && status == "routing"
	if !advancing {
		return nil
	}
	review := apMap(checkpoint["human_review"])
	state := map[string]any{"user_decisions": review["user_decisions"], "user_decision_not_applicable": review["not_applicable"]}
	completedUnit := apText(apMap(checkpoint["stage_trace"])["completed_work_unit"])
	if completedUnit != "" {
		if e = assertWorkUnitUserDecisionSemantic(s, completedUnit, state); e != nil {
			return e
		}
	}
	if apText(checkpoint["next_work_unit"]) == "work-unit.slice-implementation" {
		implementation := apCopy(apMap(review["implementation"]))
		for k, v := range state {
			implementation[k] = v
		}
		if e = assertImplementationDecisionSemantic(s, implementation); e != nil {
			return e
		}
	}
	for _, requirement := range apRows(review["required_decisions"]) {
		if _, e = assertUserDecisionRequirementSemantic(s, requirement); e != nil {
			return e
		}
	}
	if external := apMap(review["external_input"]); external != nil {
		requirement := apCopy(external)
		requirement["boundary"] = "external-input"
		if _, e = assertUserDecisionRequirementSemantic(s, requirement); e != nil {
			return e
		}
	}
	if status == "completed" {
		if len(apArray(checkpoint["blockers"])) > 0 {
			return apFail(s, "LIFECYCLE_CONTROL", "仍有阻塞，不可完成")
		}
		for _, value := range apMap(checkpoint["gates"]) {
			gate := apMap(value)
			if !apContains([]string{"approved", "not-applicable"}, apText(gate["status"])) {
				return apFail(s, "LIFECYCLE_CONTROL", "仍有未通过门禁，不可完成")
			}
		}
		completionGate := "gate.delivery-accepted"
		if apFind(s.registry["gates"], "id", "gate.strategic-design-handoff-approved") != nil {
			completionGate = "gate.strategic-design-handoff-approved"
		}
		if apText(apMap(apMap(checkpoint["gates"])[completionGate])["status"]) != "approved" {
			return apFail(s, "LIFECYCLE_CONTROL", "阶段完成须当前交付或战略交接验收，不等于发布授权")
		}
	}
	return nil
}

func assertApprovalUserDecisionSemantic(s *semanticSession, record map[string]any) (decisionValidation, error) {
	policy := apMap(s.roles["user_decision_policy"])
	requirement := map[string]any{"boundary": record["gate_id"], "subject_ref": record["subject_ref"], "scope": record["approval_scope"], "user_decision_ref": record["user_decision_ref"]}
	if apTruthy(record["continuation_ref"]) {
		delete(requirement, "user_decision_ref")
		requirement["continuation_ref"] = record["continuation_ref"]
		return assertUserDecisionRequirementSemantic(s, requirement)
	}
	for _, id := range apStrings(policy["required_capabilities"]) {
		if id != "strategic-decision-reuse-v1" && id != "business-ticket-approval-v1" {
			return decisionValidation{}, apFail(s, "user-decision-reuse-invalid", "接收工具不支持源用户决定策略")
		}
	}
	if !apTruthy(record["decision_reuse_ref"]) {
		return assertUserDecisionRequirementSemantic(s, requirement)
	}
	if apText(record["gate_id"]) != "gate.strategic-design-handoff-approved" || !apContains(policy["required_capabilities"], "strategic-decision-reuse-v1") {
		return decisionValidation{}, apFail(s, "user-decision-reuse-invalid", "未登记复用边界或能力")
	}
	reuse, e := s.doc(apText(record["decision_reuse_ref"]))
	if e != nil {
		return decisionValidation{}, e
	}
	bad := func(m string) (decisionValidation, error) {
		return decisionValidation{}, apFail(s, "user-decision-reuse-invalid", m)
	}
	if apNumber(reuse["schema_version"]) != 1 || apText(reuse["kind"]) != "strategic-decision-reuse-v1" || reuse["target_gate"] != record["gate_id"] || reuse["scope_ref"] != record["subject_ref"] || !apSetEqual(reuse["scope"], record["approval_scope"]) {
		return bad("目标边界、资产或范围不匹配")
	}
	manifest, e := s.doc(apText(reuse["scope_ref"]))
	if e != nil {
		return decisionValidation{}, e
	}
	if apNumber(manifest["schema_version"]) != 1 || apText(manifest["kind"]) != "strategic-delivery-scope" || len(apArray(manifest["assets"])) == 0 || !apIsArray(manifest["risks"]) || !apIsArray(manifest["conditions"]) || len(apArray(reuse["requirements"])) == 0 {
		return bad("交付范围清单或原决定不完整")
	}
	type approvedRow struct{ item, requirement map[string]any }
	approved := []approvedRow{}
	result := decisionValidation{outcomes: map[string]map[string]any{}}
	for _, want := range apRows(reuse["requirements"]) {
		validated, e := assertUserDecisionRequirementSemantic(s, want)
		if e != nil {
			return result, e
		}
		source, e := s.doc(apText(want["user_decision_ref"]))
		if e != nil {
			return result, e
		}
		for _, item := range apRows(apMap(source["request"])["items"]) {
			for _, v := range validated.validated {
				if v.itemID == apText(item["id"]) {
					approved = append(approved, approvedRow{item, want})
					result.validated = append(result.validated, v)
				}
			}
		}
	}
	identities := map[string]bool{}
	coveredScope := map[string]bool{}
	for _, asset := range apRows(manifest["assets"]) {
		identity := apText(asset["boundary"]) + ":" + apText(asset["ref"])
		if apText(asset["ref"]) == "" || apText(asset["version"]) == "" || apText(asset["boundary"]) == "" || len(apArray(asset["scope"])) == 0 || identities[identity] {
			return bad("资产身份、专业边界、范围缺失或重复")
		}
		identities[identity] = true
		if e = decisionCurrentAsset(s, asset, "user-decision-reuse-invalid"); e != nil {
			return result, e
		}
		cover := false
		for _, row := range approved {
			match := row.item["boundary"] == asset["boundary"]
			for _, scope := range apStrings(asset["scope"]) {
				match = match && apContains(row.requirement["scope"], scope)
			}
			sources := append([]map[string]any{apMap(row.item["subject"])}, apRows(row.item["basis"])...)
			for _, source := range sources {
				if match && source["ref"] == asset["ref"] && source["version"] == asset["version"] && source["digest"] == asset["digest"] {
					cover = true
				}
			}
		}
		if !cover {
			return bad("没有原决定覆盖: " + apText(asset["ref"]))
		}
		for _, scope := range apStrings(asset["scope"]) {
			coveredScope[scope] = true
		}
	}
	for _, scope := range apStrings(reuse["scope"]) {
		if !coveredScope[scope] {
			return bad("交付范围超出资产批准范围")
		}
	}
	for _, pair := range [][2]string{{"risks", "risks"}, {"conditions", "next_actions"}} {
		for _, value := range apStrings(manifest[pair[0]]) {
			covered := false
			for _, row := range approved {
				covered = covered || apContains(row.item[pair[1]], value)
			}
			if !covered {
				return bad("新增未批准风险或授权条件: " + value)
			}
		}
	}
	return result, nil
}

func assertDecisionContinuationSemantic(s *semanticSession, requirement map[string]any) (decisionValidation, error) {
	bad := func(m string) (decisionValidation, error) {
		return decisionValidation{}, apFail(s, "user-decision-continuation-invalid", m)
	}
	policy := apMap(apMap(s.roles["user_decision_policy"])["continuation"])
	if apText(policy["capability"]) != "approved-scope-continuation-v1" || !apContains(policy["boundaries"], apText(requirement["boundary"])) {
		return bad("未授权的延续边界")
	}
	proof, e := s.doc(apText(requirement["continuation_ref"]))
	if e != nil {
		return decisionValidation{}, e
	}
	if apNumber(proof["schema_version"]) != 1 || apText(proof["kind"]) != "approved-scope-continuation-v1" || proof["boundary"] != requirement["boundary"] || apMap(proof["subject"])["ref"] != requirement["subject_ref"] || !apSetEqual(proof["scope"], requirement["scope"]) {
		return bad("当前边界、资产或范围不匹配")
	}
	current := func(asset map[string]any, parse bool) (map[string]any, error) {
		if apText(asset["ref"]) == "" || apText(asset["version"]) == "" || apText(asset["digest"]) == "" {
			return nil, apFail(s, "user-decision-continuation-invalid", "当前证据缺少身份")
		}
		if e := decisionCurrentAsset(s, asset, "user-decision-continuation-invalid"); e != nil {
			return nil, e
		}
		if parse {
			return s.doc(apText(asset["ref"]))
		}
		return nil, nil
	}
	source := apMap(proof["source"])
	if source == nil || apTruthy(source["continuation_ref"]) || apText(source["boundary"]) != "delivery-scope" {
		return bad("必须引用原始交付范围批准，不能链式扩权")
	}
	sourceResult, e := assertUserDecisionRequirementSemantic(s, source)
	if e != nil {
		return sourceResult, e
	}
	mandate, e := s.doc(apText(source["subject_ref"]))
	if e != nil {
		return sourceResult, e
	}
	if apNumber(mandate["schema_version"]) != 1 || apText(mandate["kind"]) != "delivery-authorization" || !apIsArray(mandate["targets"]) {
		return bad("缺少已展示并批准的交付授权清单")
	}
	for _, scope := range apStrings(requirement["scope"]) {
		if !apContains(source["scope"], scope) {
			return bad("超出原始批准范围")
		}
	}
	matches := []map[string]any{}
	for _, target := range apRows(mandate["targets"]) {
		match := target["boundary"] == requirement["boundary"] && target["subject_ref"] == requirement["subject_ref"]
		for _, scope := range apStrings(requirement["scope"]) {
			match = match && apContains(target["scope"], scope)
		}
		if match {
			matches = append(matches, target)
		}
	}
	if len(matches) != 1 {
		return bad("原授权未唯一覆盖当前边界、资产和范围")
	}
	target := matches[0]
	for _, field := range []string{"business_scope", "acceptance", "contract_commitments", "authorization", "risk_acceptance", "quality", "external_commitments"} {
		if !apUnique(apMap(target["decision_basis"])[field]) {
			return bad("原授权缺少完整决定依据")
		}
	}
	if len(apArray(mandate["basis"])) == 0 {
		return bad("原授权缺少可读取决定依据")
	}
	for _, asset := range apRows(mandate["basis"]) {
		if _, e = current(asset, false); e != nil {
			return sourceResult, e
		}
	}
	external, e := current(apMap(mandate["external_policy"]), true)
	if e != nil {
		return sourceResult, e
	}
	if apText(external["status"]) != "confirmed" || !apIsArray(external["requirements"]) {
		return bad("项目外部审批要求尚未确认")
	}
	if _, e = current(apMap(proof["subject"]), false); e != nil {
		return sourceResult, e
	}
	if apText(requirement["boundary"]) == "implementation-scope" {
		manifest, e := s.doc(apText(apMap(proof["subject"])["ref"]))
		if e != nil {
			return sourceResult, e
		}
		if apText(manifest["kind"]) != "implementation-scope" || !apIsArray(manifest["slices"]) {
			return bad("实施范围清单无效")
		}
		for _, ticket := range apStrings(requirement["scope"]) {
			slices := []map[string]any{}
			for _, slice := range apRows(manifest["slices"]) {
				if apText(slice["ticket_ref"]) == ticket {
					slices = append(slices, slice)
				}
			}
			limit := apMap(apMap(target["implementation_limits"])[ticket])
			if len(slices) != 1 || limit == nil || !apSetEqual(slices[0]["repositories"], limit["repositories"]) || !apSetEqual(slices[0]["allowed_write_paths"], limit["allowed_write_paths"]) {
				return bad("实施仓库或写路径超出原授权")
			}
		}
	}
	if len(apArray(proof["basis"])) == 0 {
		return bad("缺少本次实际验证证据")
	}
	for _, asset := range apRows(proof["basis"]) {
		if _, e = current(asset, false); e != nil {
			return sourceResult, e
		}
	}
	review, e := current(apMap(proof["review"]), true)
	if e != nil {
		return sourceResult, e
	}
	actor := apFind(s.roles["roles"], "id", apText(review["role_id"]))
	if actor == nil || apFind(s.roles["runtimes"], "id", apText(review["runtime_id"])) == nil || apText(review["principal_ref"]) == "" || apText(review["drafter_principal_ref"]) == "" || review["principal_ref"] == review["drafter_principal_ref"] {
		return bad("缺少独立专业审查身份")
	}
	eligible := apMap(apMap(s.roles["gate_policy"])["continuation_reviews"])[apText(requirement["boundary"])]
	if eligible == nil {
		eligible = apFind(apMap(s.roles["gate_policy"])["dual_digital_human"], "gate", apText(requirement["boundary"]))["countersigners"]
	}
	if !apContains(eligible, apText(review["role_id"])) {
		return bad("审查能力不覆盖当前边界")
	}
	if apText(review["decision"]) != "approved" || !apContains([]string{"within-approved-scope", "presentation-only", "implementation-detail"}, apText(review["classification"])) || !apIsArray(review["material_changes"]) || len(apArray(review["material_changes"])) != 0 || !apIsArray(review["findings"]) {
		return bad("存在未知/实质变化或未完成影响分析")
	}
	for _, finding := range apRows(review["findings"]) {
		if apText(finding["id"]) == "" || apText(finding["reason"]) == "" || !apContains([]string{"requirement-violation", "missing-evidence", "important-risk", "suggestion"}, apText(finding["kind"])) {
			return bad("审查发现未分类")
		}
		if apText(finding["kind"]) == "suggestion" {
			if !apTruthy(finding["follow_up"]) {
				return bad("非阻断建议缺少待办")
			}
		} else if apText(finding["status"]) != "resolved" {
			return bad("未解决缺陷、证据缺口或重要风险")
		}
	}
	if review["boundary"] != requirement["boundary"] || !apSetEqual(review["scope"], requirement["scope"]) || !apEqual(review["subject"], proof["subject"]) || !apEqual(review["basis"], proof["basis"]) || !apEqual(review["decision_basis"], target["decision_basis"]) {
		return bad("审查未绑定当前资产、范围、证据或决定依据")
	}
	comparison := apMap(review["comparison"])
	if strings.TrimSpace(apText(review["reason"])) == "" || apMap(comparison["before"]) == nil || apMap(comparison["after"]) == nil {
		return bad("缺少前后差异和等价说明")
	}
	beforeCovered := false
	for _, asset := range apRows(mandate["basis"]) {
		beforeCovered = beforeCovered || apEqual(asset, comparison["before"])
	}
	if !beforeCovered || !apEqual(comparison["after"], proof["subject"]) {
		return bad("前后差异未绑定原授权快照和当前资产")
	}
	for _, obligation := range apRows(external["requirements"]) {
		if apText(obligation["id"]) == "" || !apUnique(obligation["boundaries"]) || !apUnique(obligation["principals"]) {
			return bad("外部审批制度记录不完整")
		}
		if !apContains(obligation["boundaries"], apText(requirement["boundary"])) {
			continue
		}
		for _, principal := range apStrings(obligation["principals"]) {
			var reply map[string]any
			for _, candidate := range apRows(proof["external_decisions"]) {
				if candidate["obligation_id"] == obligation["id"] && apText(candidate["principal_ref"]) == principal {
					reply = candidate
					break
				}
			}
			want := apMap(reply["requirement"])
			if want == nil || apTruthy(want["continuation_ref"]) || want["boundary"] != requirement["boundary"] || want["subject_ref"] != requirement["subject_ref"] || !apSetEqual(want["scope"], requirement["scope"]) {
				return bad("外部强制审批缺失或范围不符")
			}
			validated, e := assertUserDecisionRequirementSemantic(s, want)
			if e != nil {
				return sourceResult, e
			}
			if len(validated.validated) == 0 {
				return bad("外部审批缺少真实回复")
			}
			for _, v := range validated.validated {
				if v.principal != principal {
					return bad("外部审批人与制度不符")
				}
			}
		}
	}
	return sourceResult, nil
}
