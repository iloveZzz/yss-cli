package kernel

import (
	"fmt"
	"regexp"
	"strings"
	"time"
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

func decisionRequestDigest(request map[string]any) string {
	snapshot := ApCopy(request)
	delete(snapshot, "presented_source")
	return ApDigest(snapshot)
}

func DecisionTime(value any) (time.Time, bool) {
	s := ApText(value)
	for _, layout := range []string{time.RFC3339Nano, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, "2006-01-02", "2006-01-02T15:04:05"} {
		t, e := time.Parse(layout, s)
		if e == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func decisionMessage(s *SemanticSession, source map[string]any) (map[string]any, error) {
	policy := ApMap(s.Roles["user_decision_policy"])
	if source == nil || !ApContains(policy["source_kinds"], ApText(source["kind"])) {
		return nil, ApFail(s, "user-decision-source-invalid", "需要原始消息、会话导出或用户确认文件")
	}
	ref := ApText(source["ref"])
	raw, e := s.Bytes(ref)
	if e != nil {
		return nil, e
	}
	if ApDigest(raw) != source["digest"] {
		return nil, ApFail(s, "user-decision-source-changed", ref)
	}
	capture, e := s.Doc(ref)
	if e != nil {
		return nil, e
	}
	if capture["source_kind"] != source["kind"] || !ApIsArray(capture["messages"]) {
		return nil, ApFail(s, "user-decision-source-invalid", "来源不是原始消息封装")
	}
	var match map[string]any
	count := 0
	var preceding map[string]any
	for _, message := range ApRows(capture["messages"]) {
		if message["id"] == source["message_id"] {
			match = ApCopy(message)
			match["_preceding_message"] = preceding
			count++
		}
		preceding = message
	}
	if count != 1 {
		return nil, ApFail(s, "user-decision-source-invalid", "消息 ID 缺失或重复")
	}
	for _, field := range []string{"principal_ref", "original_ref", "sent_at", "text"} {
		if strings.TrimSpace(ApText(match[field])) == "" {
			return nil, ApFail(s, "user-decision-missing", "原始消息 "+field)
		}
	}
	if _, ok := DecisionTime(match["sent_at"]); !ok {
		return nil, ApFail(s, "user-decision-source-invalid", "原始消息时间无效")
	}
	return match, nil
}

func renderDecisionRequestSemantic(request map[string]any) string {
	lines := []string{fmt.Sprintf("决定请求 %s (%s)", ApText(request["id"]), decisionRequestDigest(request)), "提问者：" + ApText(request["requester_ref"])}
	for _, item := range ApRows(request["items"]) {
		subject := ApMap(item["subject"])
		basis := []string{}
		for _, asset := range ApRows(item["basis"]) {
			basis = append(basis, fmt.Sprintf("%s@%s (%s)", ApText(asset["ref"]), ApText(asset["version"]), ApText(asset["digest"])))
		}
		basisText := strings.Join(basis, "；")
		if basisText == "" {
			basisText = "无额外依据"
		}
		risks := strings.Join(ApStrings(item["risks"]), "；")
		if risks == "" {
			risks = "已评估，无已知残余风险"
		}
		lines = append(lines, fmt.Sprintf("事项 %s / %s：%s", ApText(item["id"]), ApText(item["boundary"]), ApText(item["title"])), fmt.Sprintf("资产：%s；版本：%s；摘要：%s", ApText(subject["ref"]), ApText(subject["version"]), ApText(subject["digest"])), "依据："+basisText, "关键变化："+ApText(item["changes"]), "范围："+strings.Join(ApStrings(item["scope"]), "；"), "风险："+risks, "推荐方案："+ApText(item["recommendation"]), "批准后动作："+strings.Join(ApStrings(item["next_actions"]), "；"), "回复负责人："+ApText(item["responder_ref"]))
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

func ValidateUserDecisionSemantic(s *SemanticSession, record map[string]any, expected []map[string]any) (decisionValidation, error) {
	result := decisionValidation{outcomes: map[string]map[string]any{}}
	if e := s.ValidateSchema(decisionSchemaRef, record); e != nil {
		return result, e
	}
	request := ApMap(record["request"])
	result.requestID = ApText(request["id"])
	original, e := decisionMessage(s, ApMap(request["requester_source"]))
	if e != nil {
		return result, e
	}
	if ApText(original["actor_kind"]) != "biological-human" || original["principal_ref"] != request["requester_ref"] {
		return result, ApFail(s, "user-decision-responder-mismatch", "提问者来源不匹配")
	}
	presentation, e := decisionMessage(s, ApMap(request["presented_source"]))
	if e != nil {
		return result, e
	}
	fields := []string{}
	for _, item := range ApRows(request["items"]) {
		subject := ApMap(item["subject"])
		fields = append(fields, ApText(item["title"]), ApText(subject["ref"]), ApText(subject["version"]), ApText(item["changes"]), ApText(item["recommendation"]))
		for _, k := range []string{"scope", "risks", "next_actions"} {
			fields = append(fields, ApStrings(item[k])...)
		}
		for _, asset := range ApRows(item["basis"]) {
			fields = append(fields, ApText(asset["ref"]), ApText(asset["version"]))
		}
	}
	if ApText(presentation["text"]) != renderDecisionRequestSemantic(request) {
		for _, field := range fields {
			if !strings.Contains(ApText(presentation["text"]), field) {
				return result, ApFail(s, "user-decision-presentation-mismatch", "原始展示未覆盖当前资产、版本、变化、风险、范围和后续动作")
			}
		}
	}
	requestTime, _ := DecisionTime(original["sent_at"])
	presentedTime, _ := DecisionTime(presentation["sent_at"])
	if presentedTime.Before(requestTime) {
		return result, ApFail(s, "user-decision-source-invalid", "展示时间早于用户请求")
	}
	items := map[string]map[string]any{}
	for _, item := range ApRows(request["items"]) {
		id := ApText(item["id"])
		if items[id] != nil {
			return result, ApFail(s, "user-decision-ambiguous", "事项 ID 重复")
		}
		items[id] = item
		if item["responder_ref"] != request["requester_ref"] {
			delegated, e := decisionMessage(s, ApMap(item["delegation_source"]))
			if e != nil {
				return result, e
			}
			delegatedTime, _ := DecisionTime(delegated["sent_at"])
			if ApText(delegated["actor_kind"]) != "biological-human" || delegated["principal_ref"] != request["requester_ref"] || !strings.Contains(ApText(delegated["text"]), ApText(item["responder_ref"])) || !strings.Contains(ApText(delegated["text"]), id) || delegatedTime.After(presentedTime) {
				return result, ApFail(s, "user-decision-delegation-invalid", "负责人须有提问者在展示前指向该事项的指定原文")
			}
		}
	}
	messageIDs := map[string]bool{}
	var previousTime time.Time
	for _, response := range ApRows(record["responses"]) {
		sourceSpec := ApMap(response["source"])
		source, e := decisionMessage(s, sourceSpec)
		if e != nil {
			return result, e
		}
		identity := ApText(sourceSpec["ref"]) + "#" + ApText(sourceSpec["message_id"])
		if messageIDs[identity] {
			return result, ApFail(s, "user-decision-ambiguous", "回复消息重复")
		}
		messageIDs[identity] = true
		preceding := ApMap(source["_preceding_message"])
		if source["reply_to"] != presentation["original_ref"] && source["request_digest"] != decisionRequestDigest(request) && !(preceding["original_ref"] == presentation["original_ref"] && preceding["text"] == presentation["text"]) {
			return result, ApFail(s, "user-decision-subject-mismatch", "原始回复没有指向当前请求的来源关联")
		}
		if ApText(source["actor_kind"]) != "biological-human" || source["principal_ref"] != response["principal_ref"] || source["sent_at"] != response["responded_at"] || source["text"] != response["text"] {
			return result, ApFail(s, "user-decision-responder-mismatch", "记录与原始回复身份、时间或原文不一致")
		}
		if response["request_digest"] != decisionRequestDigest(request) {
			return result, ApFail(s, "user-decision-stale", "回复绑定的请求快照已变化")
		}
		responseTime, ok := DecisionTime(response["responded_at"])
		if !ok || responseTime.Before(presentedTime) || !previousTime.IsZero() && responseTime.Before(previousTime) {
			return result, ApFail(s, "user-decision-source-invalid", "回复须在展示之后并按原始时间排序")
		}
		previousTime = responseTime
		if ApText(response["decision"]) == "approved" {
			explicit := ApText(source["decision"]) == "approved" && ApIsArray(source["item_ids"])
			for _, id := range ApStrings(response["item_ids"]) {
				explicit = explicit && ApContains(source["item_ids"], id)
			}
			contextualAll := ApText(response["selection"]) == "explicit-all" && len(items) > 1 && decisionAllQuestion.MatchString(ApText(presentation["text"])) && decisionShortApproval.MatchString(strings.TrimSpace(ApText(source["text"])))
			if !explicit && (!decisionApprovePattern.MatchString(ApText(source["text"])) || decisionRejectPattern.MatchString(ApText(source["text"]))) {
				return result, ApFail(s, "user-decision-ambiguous", "原文不能明确支持批准结论")
			}
			if !explicit && !contextualAll && ApText(response["selection"]) == "explicit-all" && len(items) > 1 && !decisionAllText.MatchString(ApText(source["text"])) {
				return result, ApFail(s, "user-decision-ambiguous", "批量批准原文必须明确全部事项")
			}
			if !explicit && ApText(response["selection"]) == "explicit-items" && len(items) > 1 {
				for _, id := range ApStrings(response["item_ids"]) {
					if !strings.Contains(ApText(source["text"]), id) && !strings.Contains(ApText(source["text"]), ApText(items[id]["title"])) {
						return result, ApFail(s, "user-decision-ambiguous", "原文未指向所选事项")
					}
				}
			}
		}
		if ApText(response["selection"]) == "single" && len(items) != 1 || len(items) > 1 && decisionContinue.MatchString(strings.TrimSpace(ApText(response["text"]))) {
			return result, ApFail(s, "user-decision-ambiguous", "多事项不能用单项回复或继续批准")
		}
		if ApText(response["selection"]) == "explicit-all" {
			if len(ApArray(response["item_ids"])) != len(items) {
				return result, ApFail(s, "user-decision-ambiguous", "全部批准必须枚举全部事项")
			}
			for id := range items {
				if !ApContains(response["item_ids"], id) {
					return result, ApFail(s, "user-decision-ambiguous", "全部批准缺少事项")
				}
			}
		}
		for _, id := range ApStrings(response["item_ids"]) {
			item := items[id]
			if item == nil {
				return result, ApFail(s, "user-decision-subject-mismatch", id)
			}
			if item["responder_ref"] != response["principal_ref"] {
				return result, ApFail(s, "user-decision-responder-mismatch", id)
			}
			if ApText(response["decision"]) == "approved" {
				if len(ApArray(response["approved_scope"])) == 0 {
					return result, ApFail(s, "user-decision-scope-mismatch", id)
				}
				for _, scope := range ApStrings(response["approved_scope"]) {
					covered := false
					for _, selected := range ApStrings(response["item_ids"]) {
						covered = covered || ApContains(items[selected]["scope"], scope)
					}
					if !covered {
						return result, ApFail(s, "user-decision-scope-mismatch", id)
					}
				}
			}
			answer := ApCopy(response)
			previous := result.outcomes[id]
			if ApText(response["decision"]) == "approved" && ApText(previous["decision"]) == "approved" {
				scopes := ApStrings(previous["approved_scope"])
				for _, scope := range ApStrings(response["approved_scope"]) {
					if !ApContains(scopes, scope) {
						scopes = append(scopes, scope)
					}
				}
				answer["approved_scope"] = scopes
			}
			result.outcomes[id] = answer
		}
	}
	for _, want := range expected {
		boundary, ref := ApText(want["boundary"]), ApText(want["subject_ref"])
		if strings.TrimSpace(boundary) == "" || strings.TrimSpace(ref) == "" {
			return result, ApFail(s, "user-decision-missing", "当前决定边界或资产引用缺失")
		}
		if len(ApArray(want["scope"])) == 0 {
			return result, ApFail(s, "user-decision-scope-mismatch", "当前批准范围缺失")
		}
		matches := []map[string]any{}
		for _, item := range ApRows(request["items"]) {
			match := item["boundary"] == boundary && ApMap(item["subject"])["ref"] == ref
			for _, scope := range ApStrings(want["scope"]) {
				match = match && ApContains(item["scope"], scope)
			}
			if match {
				matches = append(matches, item)
			}
		}
		if len(matches) != 1 {
			return result, ApFail(s, "user-decision-subject-mismatch", boundary)
		}
		item := matches[0]
		if e = DecisionCurrentAsset(s, ApMap(item["subject"]), "user-decision-stale"); e != nil {
			return result, e
		}
		for _, asset := range ApRows(item["basis"]) {
			if e = DecisionCurrentAsset(s, asset, "user-decision-stale"); e != nil {
				return result, e
			}
		}
		answer := result.outcomes[ApText(item["id"])]
		if answer == nil {
			return result, ApFail(s, "user-decision-response-required", ApText(item["id"]))
		}
		if ApText(answer["decision"]) != "approved" {
			return result, ApFail(s, "user-decision-not-approved", ApText(item["id"])+": "+ApText(answer["decision"]))
		}
		for _, scope := range ApStrings(want["scope"]) {
			if !ApContains(answer["approved_scope"], scope) {
				return result, ApFail(s, "user-decision-scope-mismatch", ApText(item["id"]))
			}
		}
		result.validated = append(result.validated, decisionValidated{ApText(item["id"]), boundary, ApText(answer["principal_ref"]), ApStrings(want["scope"])})
	}
	return result, nil
}

func DecisionCurrentAsset(s *SemanticSession, asset map[string]any, code string) error {
	b, e := s.Bytes(ApText(asset["ref"]))
	if e != nil {
		return e
	}
	if ApDigest(b) != asset["digest"] {
		return ApFail(s, code, "当前资产过期: "+ApText(asset["ref"]))
	}
	return nil
}

func AssertUserDecisionRequirementSemantic(s *SemanticSession, requirement map[string]any) (decisionValidation, error) {
	if requirement == nil {
		return decisionValidation{}, ApFail(s, "user-decision-response-required", "缺少当前决定及其范围")
	}
	if ApTruthy(requirement["user_decision_ref"]) && ApTruthy(requirement["continuation_ref"]) {
		return decisionValidation{}, ApFail(s, "user-decision-proof-conflict", "当前决定与批准延续不得同时提供")
	}
	if ApTruthy(requirement["continuation_ref"]) {
		return assertDecisionContinuationSemantic(s, requirement)
	}
	ref := ApText(requirement["user_decision_ref"])
	if ref == "" {
		return decisionValidation{}, ApFail(s, "user-decision-missing", "user_decision_ref 缺失")
	}
	record, e := s.Doc(ref)
	if e != nil {
		return decisionValidation{}, e
	}
	return ValidateUserDecisionSemantic(s, record, []map[string]any{requirement})
}

func assertApprovalUserDecisionSemantic(s *SemanticSession, record map[string]any) (decisionValidation, error) {
	policy := ApMap(s.Roles["user_decision_policy"])
	requirement := map[string]any{"boundary": record["gate_id"], "subject_ref": record["subject_ref"], "scope": record["approval_scope"], "user_decision_ref": record["user_decision_ref"]}
	if ApTruthy(record["continuation_ref"]) {
		delete(requirement, "user_decision_ref")
		requirement["continuation_ref"] = record["continuation_ref"]
		return AssertUserDecisionRequirementSemantic(s, requirement)
	}
	for _, id := range ApStrings(policy["required_capabilities"]) {
		if id != "strategic-decision-reuse-v1" && id != "business-ticket-approval-v1" {
			return decisionValidation{}, ApFail(s, "user-decision-reuse-invalid", "接收工具不支持源用户决定策略")
		}
	}
	if !ApTruthy(record["decision_reuse_ref"]) {
		return AssertUserDecisionRequirementSemantic(s, requirement)
	}
	if ApText(record["gate_id"]) != "gate.strategic-design-handoff-approved" || !ApContains(policy["required_capabilities"], "strategic-decision-reuse-v1") {
		return decisionValidation{}, ApFail(s, "user-decision-reuse-invalid", "未登记复用边界或能力")
	}
	reuse, e := s.Doc(ApText(record["decision_reuse_ref"]))
	if e != nil {
		return decisionValidation{}, e
	}
	bad := func(m string) (decisionValidation, error) {
		return decisionValidation{}, ApFail(s, "user-decision-reuse-invalid", m)
	}
	if ApNumber(reuse["schema_version"]) != 1 || ApText(reuse["kind"]) != "strategic-decision-reuse-v1" || reuse["target_gate"] != record["gate_id"] || reuse["scope_ref"] != record["subject_ref"] || !ApSetEqual(reuse["scope"], record["approval_scope"]) {
		return bad("目标边界、资产或范围不匹配")
	}
	manifest, e := s.Doc(ApText(reuse["scope_ref"]))
	if e != nil {
		return decisionValidation{}, e
	}
	if ApNumber(manifest["schema_version"]) != 1 || ApText(manifest["kind"]) != "strategic-delivery-scope" || len(ApArray(manifest["assets"])) == 0 || !ApIsArray(manifest["risks"]) || !ApIsArray(manifest["conditions"]) || len(ApArray(reuse["requirements"])) == 0 {
		return bad("交付范围清单或原决定不完整")
	}
	type approvedRow struct{ item, requirement map[string]any }
	approved := []approvedRow{}
	result := decisionValidation{outcomes: map[string]map[string]any{}}
	for _, want := range ApRows(reuse["requirements"]) {
		validated, e := AssertUserDecisionRequirementSemantic(s, want)
		if e != nil {
			return result, e
		}
		source, e := s.Doc(ApText(want["user_decision_ref"]))
		if e != nil {
			return result, e
		}
		for _, item := range ApRows(ApMap(source["request"])["items"]) {
			for _, v := range validated.validated {
				if v.itemID == ApText(item["id"]) {
					approved = append(approved, approvedRow{item, want})
					result.validated = append(result.validated, v)
				}
			}
		}
	}
	identities := map[string]bool{}
	coveredScope := map[string]bool{}
	for _, asset := range ApRows(manifest["assets"]) {
		identity := ApText(asset["boundary"]) + ":" + ApText(asset["ref"])
		if ApText(asset["ref"]) == "" || ApText(asset["version"]) == "" || ApText(asset["boundary"]) == "" || len(ApArray(asset["scope"])) == 0 || identities[identity] {
			return bad("资产身份、专业边界、范围缺失或重复")
		}
		identities[identity] = true
		if e = DecisionCurrentAsset(s, asset, "user-decision-reuse-invalid"); e != nil {
			return result, e
		}
		cover := false
		for _, row := range approved {
			match := row.item["boundary"] == asset["boundary"]
			for _, scope := range ApStrings(asset["scope"]) {
				match = match && ApContains(row.requirement["scope"], scope)
			}
			sources := append([]map[string]any{ApMap(row.item["subject"])}, ApRows(row.item["basis"])...)
			for _, source := range sources {
				if match && source["ref"] == asset["ref"] && source["version"] == asset["version"] && source["digest"] == asset["digest"] {
					cover = true
				}
			}
		}
		if !cover {
			return bad("没有原决定覆盖: " + ApText(asset["ref"]))
		}
		for _, scope := range ApStrings(asset["scope"]) {
			coveredScope[scope] = true
		}
	}
	for _, scope := range ApStrings(reuse["scope"]) {
		if !coveredScope[scope] {
			return bad("交付范围超出资产批准范围")
		}
	}
	for _, pair := range [][2]string{{"risks", "risks"}, {"conditions", "next_actions"}} {
		for _, value := range ApStrings(manifest[pair[0]]) {
			covered := false
			for _, row := range approved {
				covered = covered || ApContains(row.item[pair[1]], value)
			}
			if !covered {
				return bad("新增未批准风险或授权条件: " + value)
			}
		}
	}
	return result, nil
}

func assertDecisionContinuationSemantic(s *SemanticSession, requirement map[string]any) (decisionValidation, error) {
	bad := func(m string) (decisionValidation, error) {
		return decisionValidation{}, ApFail(s, "user-decision-continuation-invalid", m)
	}
	policy := ApMap(ApMap(s.Roles["user_decision_policy"])["continuation"])
	if ApText(policy["capability"]) != "approved-scope-continuation-v1" || !ApContains(policy["boundaries"], ApText(requirement["boundary"])) {
		return bad("未授权的延续边界")
	}
	proof, e := s.Doc(ApText(requirement["continuation_ref"]))
	if e != nil {
		return decisionValidation{}, e
	}
	if ApNumber(proof["schema_version"]) != 1 || ApText(proof["kind"]) != "approved-scope-continuation-v1" || proof["boundary"] != requirement["boundary"] || ApMap(proof["subject"])["ref"] != requirement["subject_ref"] || !ApSetEqual(proof["scope"], requirement["scope"]) {
		return bad("当前边界、资产或范围不匹配")
	}
	current := func(asset map[string]any, parse bool) (map[string]any, error) {
		if ApText(asset["ref"]) == "" || ApText(asset["version"]) == "" || ApText(asset["digest"]) == "" {
			return nil, ApFail(s, "user-decision-continuation-invalid", "当前证据缺少身份")
		}
		if e := DecisionCurrentAsset(s, asset, "user-decision-continuation-invalid"); e != nil {
			return nil, e
		}
		if parse {
			return s.Doc(ApText(asset["ref"]))
		}
		return nil, nil
	}
	source := ApMap(proof["source"])
	if source == nil || ApTruthy(source["continuation_ref"]) || ApText(source["boundary"]) != "delivery-scope" {
		return bad("必须引用原始交付范围批准，不能链式扩权")
	}
	sourceResult, e := AssertUserDecisionRequirementSemantic(s, source)
	if e != nil {
		return sourceResult, e
	}
	mandate, e := s.Doc(ApText(source["subject_ref"]))
	if e != nil {
		return sourceResult, e
	}
	if ApNumber(mandate["schema_version"]) != 1 || ApText(mandate["kind"]) != "delivery-authorization" || !ApIsArray(mandate["targets"]) {
		return bad("缺少已展示并批准的交付授权清单")
	}
	for _, scope := range ApStrings(requirement["scope"]) {
		if !ApContains(source["scope"], scope) {
			return bad("超出原始批准范围")
		}
	}
	matches := []map[string]any{}
	for _, target := range ApRows(mandate["targets"]) {
		match := target["boundary"] == requirement["boundary"] && target["subject_ref"] == requirement["subject_ref"]
		for _, scope := range ApStrings(requirement["scope"]) {
			match = match && ApContains(target["scope"], scope)
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
		if !apUnique(ApMap(target["decision_basis"])[field]) {
			return bad("原授权缺少完整决定依据")
		}
	}
	if len(ApArray(mandate["basis"])) == 0 {
		return bad("原授权缺少可读取决定依据")
	}
	for _, asset := range ApRows(mandate["basis"]) {
		if _, e = current(asset, false); e != nil {
			return sourceResult, e
		}
	}
	external, e := current(ApMap(mandate["external_policy"]), true)
	if e != nil {
		return sourceResult, e
	}
	if ApText(external["status"]) != "confirmed" || !ApIsArray(external["requirements"]) {
		return bad("项目外部审批要求尚未确认")
	}
	if _, e = current(ApMap(proof["subject"]), false); e != nil {
		return sourceResult, e
	}
	if ApText(requirement["boundary"]) == "implementation-scope" {
		manifest, e := s.Doc(ApText(ApMap(proof["subject"])["ref"]))
		if e != nil {
			return sourceResult, e
		}
		if ApText(manifest["kind"]) != "implementation-scope" || !ApIsArray(manifest["slices"]) {
			return bad("实施范围清单无效")
		}
		for _, ticket := range ApStrings(requirement["scope"]) {
			slices := []map[string]any{}
			for _, slice := range ApRows(manifest["slices"]) {
				if ApText(slice["ticket_ref"]) == ticket {
					slices = append(slices, slice)
				}
			}
			limit := ApMap(ApMap(target["implementation_limits"])[ticket])
			if len(slices) != 1 || limit == nil || !ApSetEqual(slices[0]["repositories"], limit["repositories"]) || !ApSetEqual(slices[0]["allowed_write_paths"], limit["allowed_write_paths"]) {
				return bad("实施仓库或写路径超出原授权")
			}
		}
	}
	if len(ApArray(proof["basis"])) == 0 {
		return bad("缺少本次实际验证证据")
	}
	for _, asset := range ApRows(proof["basis"]) {
		if _, e = current(asset, false); e != nil {
			return sourceResult, e
		}
	}
	review, e := current(ApMap(proof["review"]), true)
	if e != nil {
		return sourceResult, e
	}
	actor := ApFind(s.Roles["roles"], "id", ApText(review["role_id"]))
	if actor == nil || ApFind(s.Roles["runtimes"], "id", ApText(review["runtime_id"])) == nil || ApText(review["principal_ref"]) == "" || ApText(review["drafter_principal_ref"]) == "" || review["principal_ref"] == review["drafter_principal_ref"] {
		return bad("缺少独立专业审查身份")
	}
	eligible := ApMap(ApMap(s.Roles["gate_policy"])["continuation_reviews"])[ApText(requirement["boundary"])]
	if eligible == nil {
		eligible = ApFind(ApMap(s.Roles["gate_policy"])["dual_digital_human"], "gate", ApText(requirement["boundary"]))["countersigners"]
	}
	if !ApContains(eligible, ApText(review["role_id"])) {
		return bad("审查能力不覆盖当前边界")
	}
	if ApText(review["decision"]) != "approved" || !ApContains([]string{"within-approved-scope", "presentation-only", "implementation-detail"}, ApText(review["classification"])) || !ApIsArray(review["material_changes"]) || len(ApArray(review["material_changes"])) != 0 || !ApIsArray(review["findings"]) {
		return bad("存在未知/实质变化或未完成影响分析")
	}
	for _, finding := range ApRows(review["findings"]) {
		if ApText(finding["id"]) == "" || ApText(finding["reason"]) == "" || !ApContains([]string{"requirement-violation", "missing-evidence", "important-risk", "suggestion"}, ApText(finding["kind"])) {
			return bad("审查发现未分类")
		}
		if ApText(finding["kind"]) == "suggestion" {
			if !ApTruthy(finding["follow_up"]) {
				return bad("非阻断建议缺少待办")
			}
		} else if ApText(finding["status"]) != "resolved" {
			return bad("未解决缺陷、证据缺口或重要风险")
		}
	}
	if review["boundary"] != requirement["boundary"] || !ApSetEqual(review["scope"], requirement["scope"]) || !ApEqual(review["subject"], proof["subject"]) || !ApEqual(review["basis"], proof["basis"]) || !ApEqual(review["decision_basis"], target["decision_basis"]) {
		return bad("审查未绑定当前资产、范围、证据或决定依据")
	}
	comparison := ApMap(review["comparison"])
	if strings.TrimSpace(ApText(review["reason"])) == "" || ApMap(comparison["before"]) == nil || ApMap(comparison["after"]) == nil {
		return bad("缺少前后差异和等价说明")
	}
	beforeCovered := false
	for _, asset := range ApRows(mandate["basis"]) {
		beforeCovered = beforeCovered || ApEqual(asset, comparison["before"])
	}
	if !beforeCovered || !ApEqual(comparison["after"], proof["subject"]) {
		return bad("前后差异未绑定原授权快照和当前资产")
	}
	for _, obligation := range ApRows(external["requirements"]) {
		if ApText(obligation["id"]) == "" || !apUnique(obligation["boundaries"]) || !apUnique(obligation["principals"]) {
			return bad("外部审批制度记录不完整")
		}
		if !ApContains(obligation["boundaries"], ApText(requirement["boundary"])) {
			continue
		}
		for _, principal := range ApStrings(obligation["principals"]) {
			var reply map[string]any
			for _, candidate := range ApRows(proof["external_decisions"]) {
				if candidate["obligation_id"] == obligation["id"] && ApText(candidate["principal_ref"]) == principal {
					reply = candidate
					break
				}
			}
			want := ApMap(reply["requirement"])
			if want == nil || ApTruthy(want["continuation_ref"]) || want["boundary"] != requirement["boundary"] || want["subject_ref"] != requirement["subject_ref"] || !ApSetEqual(want["scope"], requirement["scope"]) {
				return bad("外部强制审批缺失或范围不符")
			}
			validated, e := AssertUserDecisionRequirementSemantic(s, want)
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
