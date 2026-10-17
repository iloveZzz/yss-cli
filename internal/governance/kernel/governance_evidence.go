package kernel

import (
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

// EvidenceAsset uses the same confined, observed bytes as all semantic inputs.
func (s *SemanticSession) EvidenceAsset(asset map[string]any) ([]byte, error) {
	ref, digest := Text(asset["ref"]), Text(asset["digest"])
	if !IsSHA256(digest) || ref == "" {
		return nil, s.Reject("EVIDENCE_BINDING", "证据缺少明确引用和64位摘要")
	}
	raw, err := s.Bytes(ref)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 || safefs.Digest(raw) != digest {
		return nil, s.Reject("EVIDENCE_BINDING", "证据为空或字节摘要不符: "+ref)
	}
	return raw, nil
}

// EvidenceFact reuses the JSON/YAML execution-log carrier; it creates no extra identity registry.
func (s *SemanticSession) EvidenceFact(ref string) (map[string]any, error) {
	raw, err := s.Bytes(ref)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, "```json\n") && strings.HasSuffix(text, "\n```") {
		text = strings.TrimSuffix(strings.TrimPrefix(text, "```json\n"), "\n```")
	}
	value, err := schema.Parse([]byte(text))
	if err != nil {
		return nil, s.Reject("EVIDENCE_SOURCE", "原始执行事实必须为JSON/YAML记录: "+ref)
	}
	record, ok := Object(value)
	if !ok || len(record) == 0 {
		return nil, s.Reject("EVIDENCE_SOURCE", "原始执行事实为空: "+ref)
	}
	return record, nil
}

func evidenceTime(value any) bool {
	when, err := time.Parse(time.RFC3339Nano, Text(value))
	return err == nil && !when.After(time.Now().Add(time.Minute))
}

func evidenceArgv(value any) bool {
	if !ApIsArray(value) || len(ApArray(value)) == 0 {
		return false
	}
	for _, arg := range ApArray(value) {
		if _, ok := arg.(string); !ok {
			return false
		}
	}
	return Text(ApArray(value)[0]) != ""
}

func executionFacts(s *SemanticSession, record map[string]any, candidateDigest string) error {
	exit, ok := Integer(record["exit_code"])
	if !ok || exit < 0 || !evidenceTime(record["executed_at"]) || strings.TrimSpace(Text(record["command"])) == "" && !evidenceArgv(record["argv"]) {
		return s.Reject("EVIDENCE_EXECUTION", "原始日志缺少实际命令、退出码或执行时间")
	}
	if !IsSHA256(candidateDigest) || record["candidate_digest"] != candidateDigest {
		return s.Reject("EVIDENCE_CANDIDATE", "原始日志未绑定当前候选；重填登记摘要不能替代原始记录")
	}
	return nil
}

// ValidateProvenance checks the source log, task and actual tool. Stable identity is optional unless independence is requested.
func (s *SemanticSession) ValidateProvenance(provenance map[string]any, expectedTask, candidateDigest string) (map[string]any, error) {
	for _, key := range []string{"tool", "task_ref", "execution_ref"} {
		if strings.TrimSpace(Text(provenance[key])) == "" {
			return nil, s.Reject("EVIDENCE_PROVENANCE", "来源缺少"+key)
		}
	}
	if expectedTask != "" && provenance["task_ref"] != expectedTask {
		return nil, s.Reject("EVIDENCE_PROVENANCE", "实际来源不属于当前任务")
	}
	if _, err := s.Bytes(Text(provenance["task_ref"])); err != nil {
		return nil, err
	}
	identity := ApMap(provenance["identity"])
	switch identity["status"] {
	case "available":
		if Text(identity["principal_ref"]) == "" || Text(identity["session_ref"]) == "" || len(identity) != 3 {
			return nil, s.Reject("EVIDENCE_PROVENANCE", "可用身份缺少真实账号或会话来源")
		}
	case "unavailable":
		if strings.TrimSpace(Text(identity["reason"])) == "" || len(identity) != 2 {
			return nil, s.Reject("EVIDENCE_PROVENANCE", "不可用稳定身份必须如实说明原因")
		}
	default:
		return nil, s.Reject("EVIDENCE_PROVENANCE", "未知身份可用状态")
	}
	actual, err := s.EvidenceFact(Text(provenance["execution_ref"]))
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"tool", "task_ref", "identity"} {
		if !ApEqual(actual[key], provenance[key]) {
			return nil, s.Reject("EVIDENCE_PROVENANCE", "来源登记与原始执行记录不符: "+key)
		}
	}
	if err = executionFacts(s, actual, candidateDigest); err != nil {
		return nil, err
	}
	return actual, nil
}

// Mandatory commands require successful original executions, not not-run or unapproved not-applicable assertions.
func (s *SemanticSession) ValidateVerificationResults(results any, commands []string, candidate map[string]any) error {
	if _, err := s.EvidenceAsset(candidate); err != nil {
		return err
	}
	return s.ValidateVerificationResultsDigest(results, commands, Text(candidate["digest"]))
}

// ValidateVerificationResultsDigest requires the caller to compute the candidate from verified current sources.
func (s *SemanticSession) ValidateVerificationResultsDigest(results any, commands []string, digest string) error {
	if !IsSHA256(digest) {
		return s.Reject("EVIDENCE_CANDIDATE", "缺少原生候选摘要")
	}
	candidate := map[string]any{"digest": digest}
	if !ApUnique(commands) || !ApIsArray(results) || len(ApArray(results)) == 0 {
		return s.Reject("EVIDENCE_VERIFICATION", "正式验证命令与实际结果不能为空")
	}
	seen := map[string]bool{}
	for _, row := range ApRows(results) {
		command := Text(row["command"])
		if !SemHas(commands, command) || seen[command] || row["status"] != "passed" || ApNumber(row["exit_code"]) != 0 || row["candidate_digest"] != candidate["digest"] {
			return s.Reject("EVIDENCE_VERIFICATION", "命令未实际通过、重复或没有绑定当前候选")
		}
		if _, err := s.EvidenceAsset(map[string]any{"ref": row["evidence_ref"], "digest": row["evidence_digest"]}); err != nil {
			return err
		}
		actual, err := s.EvidenceFact(Text(row["evidence_ref"]))
		if err != nil {
			return err
		}
		if err = executionFacts(s, actual, Text(candidate["digest"])); err != nil {
			return err
		}
		if status, present := actual["status"]; present && status != "passed" {
			return s.Reject("EVIDENCE_VERIFICATION", "原始执行状态不是通过")
		}
		if result, present := actual["result"]; present && result != "passed" {
			return s.Reject("EVIDENCE_VERIFICATION", "原始执行结果不是通过")
		}
		for _, key := range []string{"command", "exit_code", "executed_at", "candidate_digest"} {
			if !ApEqual(actual[key], row[key]) {
				return s.Reject("EVIDENCE_VERIFICATION", "原始日志与验证登记不符: "+key)
			}
		}
		seen[command] = true
	}
	if len(seen) != len(commands) {
		return s.Reject("EVIDENCE_VERIFICATION", "尚有必需命令没有当前实际通过证据")
	}
	return nil
}

func reviewResult(s *SemanticSession, actual map[string]any, scope []string) error {
	if actual["result"] != "passed" || !ApIsArray(actual["blocking_findings"]) || len(ApArray(actual["blocking_findings"])) != 0 || !SameStringSet(actual["scope"], scope) || ApNumber(actual["exit_code"]) != 0 {
		return s.Reject("EVIDENCE_REVIEW", "原始复核未通过、存在阻断问题或范围不一致")
	}
	return nil
}

// requiredMode comes from applicable user/external requirements. implementation and
// implementationTask must come from the consumer's verified task/execution facts,
// never from this review's independence declaration. Review tasks may differ.
func (s *SemanticSession) ValidateReviewEvidence(review, candidate map[string]any, scope []string, requiredMode string, implementation map[string]any, implementationTask string) error {
	if _, err := s.EvidenceAsset(candidate); err != nil {
		return err
	}
	return s.ValidateReviewEvidenceDigest(review, candidate, scope, requiredMode, implementation, implementationTask, candidate)
}

// ValidateReviewEvidenceDigest requires the caller to compute the candidate from verified current sources.
func (s *SemanticSession) ValidateReviewEvidenceDigest(review, candidate map[string]any, scope []string, requiredMode string, implementation map[string]any, implementationTask string, implementationCandidate map[string]any) error {
	if !IsSHA256(Text(candidate["digest"])) || Text(candidate["ref"]) == "" {
		return s.Reject("EVIDENCE_CANDIDATE", "缺少原生候选绑定")
	}
	if !ApUnique(scope) {
		return s.Reject("EVIDENCE_REVIEW", "复核范围必须明确且非空")
	}
	if implementationTask == "" {
		return s.Reject("EVIDENCE_INDEPENDENCE", "缺少可信实施任务")
	}
	if _, err := s.ValidateProvenance(implementation, implementationTask, Text(implementationCandidate["digest"])); err != nil {
		return err
	}
	mode := Text(review["mode"])
	if !SemHas([]string{"self-check", "independent-review", "external-required-review"}, mode) || !SemHas([]string{"self-check", "independent-review", "external-required-review"}, requiredMode) {
		return s.Reject("EVIDENCE_REVIEW", "未知复核方式或消费要求")
	}
	if requiredMode == "external-required-review" && mode != "external-required-review" || requiredMode == "independent-review" && mode == "self-check" {
		return s.Reject("EVIDENCE_REVIEW", "自检或普通复核不能替代当前必需复核")
	}
	actual, err := s.ValidateProvenance(ApMap(review["provenance"]), "", Text(candidate["digest"]))
	if err != nil {
		return err
	}
	if err = reviewResult(s, actual, scope); err != nil {
		return err
	}
	if mode == "self-check" {
		if review["independence"] != nil || review["external"] != nil {
			return s.Reject("EVIDENCE_REVIEW", "自检不能携带独立或外部批准声明")
		}
		return nil
	}
	if mode == "independent-review" && review["external"] != nil {
		return s.Reject("EVIDENCE_REVIEW", "独立复核不能混写外部批准")
	}
	if mode == "independent-review" || requiredMode == "independent-review" || review["independence"] != nil {
		if err = s.validateIndependence(ApMap(review["independence"]), ApMap(review["provenance"]), candidate, scope, implementation, implementationTask, implementationCandidate); err != nil {
			return err
		}
	}
	if mode == "external-required-review" {
		return s.validateExternalRequirement(ApMap(review["external"]), candidate, scope, implementation)
	}
	return nil
}

func (s *SemanticSession) validateIndependence(ind, reviewer, candidate map[string]any, scope []string, trustedImplementation map[string]any, implementationTask string, implementationCandidate map[string]any) error {
	implementation, indReviewer := ApMap(ind["implementation"]), ApMap(ind["reviewer"])
	if !ApEqual(implementation, trustedImplementation) {
		return s.Reject("EVIDENCE_INDEPENDENCE", "独立性声明未绑定当前任务的真实实施来源")
	}
	ii, ri := ApMap(implementation["identity"]), ApMap(indReviewer["identity"])
	if !ApEqual(indReviewer, reviewer) || ii["status"] != "available" || ri["status"] != "available" || ii["session_ref"] == ri["session_ref"] || implementation["execution_ref"] == indReviewer["execution_ref"] || ind["implementation_context_ref"] == ind["reviewer_context_ref"] || ind["readonly"] != true || !ApEqual(ind["candidate"], candidate) || !SameStringSet(ind["scope"], scope) {
		return s.Reject("EVIDENCE_INDEPENDENCE", "独立复核需要不同真实执行上下文、同一候选范围和只读结果；换名字或同会话不能证明独立")
	}
	for _, p := range []map[string]any{implementation, indReviewer} {
		expectedTask, digest := "", Text(candidate["digest"])
		if ApEqual(p, trustedImplementation) {
			expectedTask, digest = implementationTask, Text(implementationCandidate["digest"])
		}
		actual, err := s.ValidateProvenance(p, expectedTask, digest)
		if err != nil {
			return err
		}
		if !SameStringSet(actual["scope"], scope) {
			return s.Reject("EVIDENCE_INDEPENDENCE", "实施与复核执行记录未绑定同一检查范围")
		}
	}
	originalRefs := map[string]bool{}
	for _, pair := range []struct {
		ref string
		p   map[string]any
	}{{Text(ind["implementation_context_ref"]), implementation}, {Text(ind["reviewer_context_ref"]), indReviewer}} {
		capture, err := s.EvidenceFact(pair.ref)
		if err != nil {
			return err
		}
		identity := ApMap(pair.p["identity"])
		digest := Text(candidate["digest"])
		if ApEqual(pair.p, trustedImplementation) {
			digest = Text(implementationCandidate["digest"])
		}
		if capture["source_kind"] != "session-export" || capture["session_ref"] != identity["session_ref"] || !ApIsArray(capture["messages"]) {
			return s.Reject("EVIDENCE_INDEPENDENCE", "缺少可核对的原始会话导出")
		}
		found := false
		for _, message := range ApRows(capture["messages"]) {
			if message["principal_ref"] == identity["principal_ref"] && Text(message["original_ref"]) != "" && Text(message["id"]) != "" && evidenceTime(message["sent_at"]) && strings.Contains(Text(message["text"]), Text(pair.p["execution_ref"])) && strings.Contains(Text(message["text"]), digest) {
				original := Text(message["original_ref"])
				if originalRefs[original] {
					return s.Reject("EVIDENCE_INDEPENDENCE", "两份上下文复用了同一原始执行来源")
				}
				originalRefs[original] = true
				found = true
				break
			}
		}
		if !found {
			return s.Reject("EVIDENCE_INDEPENDENCE", "会话来源未记录当前候选的真实执行引用")
		}
	}
	evidence := ApMap(ind["evidence"])
	if _, err := s.EvidenceAsset(evidence); err != nil {
		return err
	}
	actual, err := s.EvidenceFact(Text(evidence["ref"]))
	if err != nil {
		return err
	}
	if actual["readonly"] != true || actual["candidate_digest"] != candidate["digest"] || !ApEqual(actual["identity"], ri) || evidence["ref"] != indReviewer["execution_ref"] {
		return s.Reject("EVIDENCE_INDEPENDENCE", "独立证据不是当前审查执行的只读原始结果")
	}
	return reviewResult(s, actual, scope)
}

// ValidateExternalRules consumes known constraints. Discovery and applicability belong to the calling boundary.
func (s *SemanticSession) ValidateExternalRules(rules, candidate map[string]any, scope []string, implementation map[string]any) error {
	if _, err := s.EvidenceAsset(candidate); err != nil {
		return err
	}
	return s.ValidateExternalRulesDigest(rules, candidate, scope, implementation)
}

// Candidate bytes or composition and implementation provenance must already be verified by the caller.
func (s *SemanticSession) ValidateExternalRulesDigest(rules, candidate map[string]any, scope []string, implementation map[string]any) error {
	if !IsSHA256(Text(candidate["digest"])) || Text(candidate["ref"]) == "" {
		return s.Reject("EVIDENCE_CANDIDATE", "缺少当前外部约束候选")
	}
	if !ApUnique(scope) {
		return s.Reject("EXTERNAL_REQUIREMENT", "外部约束范围缺失")
	}
	switch rules["state"] {
	case "none-known":
		if len(rules) != 1 {
			return s.Reject("EXTERNAL_REQUIREMENT", "none-known不能隐去已有规则")
		}
		return nil
	case "known-unverifiable":
		return s.Reject("EXTERNAL_REQUIREMENT", "已知外部要求无法核验；依赖动作必须暂停")
	case "known-verifiable":
		if len(ApArray(rules["requirements"])) == 0 {
			return s.Reject("EXTERNAL_REQUIREMENT", "已知规则缺少实际要求")
		}
		for _, requirement := range ApRows(rules["requirements"]) {
			if err := s.validateExternalRequirement(requirement, candidate, scope, implementation); err != nil {
				return err
			}
		}
		return nil
	default:
		return s.Reject("EXTERNAL_REQUIREMENT", "外部规则状态未知或缺失")
	}
}

func (s *SemanticSession) validateExternalRequirement(requirement, candidate map[string]any, scope []string, implementation map[string]any) error {
	if requirement["status"] != "satisfied" || Text(requirement["source"]) == "" || !SameStringSet(requirement["scope"], scope) {
		return s.Reject("EXTERNAL_REQUIREMENT", "外部要求未满足或来源、范围不符")
	}
	if subject, ok := Object(requirement["subject"]); ok && !ApEqual(subject, candidate) {
		return s.Reject("EXTERNAL_REQUIREMENT", "外部批准主体不是当前候选")
	}
	for _, key := range []string{"rule", "result"} {
		if _, err := s.EvidenceAsset(ApMap(requirement[key])); err != nil {
			return err
		}
	}
	rule, err := s.EvidenceFact(Text(ApMap(requirement["rule"])["ref"]))
	if err != nil {
		return err
	}
	result, err := s.EvidenceFact(Text(ApMap(requirement["result"])["ref"]))
	if err != nil {
		return err
	}
	// Unknown platform rules cannot silently be treated as satisfied by a generic local record.
	for key := range rule {
		if !SemHas([]string{"source", "scope", "required_principals", "distinct_principals", "description"}, key) {
			return s.Unavailable("EXTERNAL_RULE_UNSUPPORTED", "未支持的外部规则字段: "+key)
		}
	}
	if rule["source"] != requirement["source"] || !SameStringSet(rule["scope"], scope) || result["source"] != requirement["source"] || !ApEqual(result["rule"], requirement["rule"]) || !ApEqual(result["subject"], candidate) || !SameStringSet(result["scope"], scope) || result["status"] != "satisfied" || result["revoked"] != false || !evidenceTime(result["verified_at"]) || Text(result["original_ref"]) == "" {
		return s.Reject("EXTERNAL_REQUIREMENT", "外部原始来源、候选、范围、状态或当前性不可核验")
	}
	if expiry, present := result["expires_at"]; present {
		expires, err := time.Parse(time.RFC3339Nano, Text(expiry))
		if err != nil || !expires.After(time.Now()) {
			return s.Reject("EXTERNAL_REQUIREMENT", "外部来源声明的有效期已过期或无效")
		}
	}
	if !ApUnique(result["principals"]) {
		return s.Reject("EXTERNAL_REQUIREMENT", "外部结果缺少可核对主体")
	}
	if required, exists := rule["required_principals"]; exists {
		if !ApUnique(required) {
			return s.Unavailable("EXTERNAL_RULE_UNSUPPORTED", "外部规则主体条件无效")
		}
		for _, principal := range ApStrings(required) {
			if !ApContains(result["principals"], principal) {
				return s.Reject("EXTERNAL_REQUIREMENT", "外部规则指定主体尚未满足")
			}
		}
	}
	if distinct, exists := rule["distinct_principals"]; exists {
		must, ok := distinct.(bool)
		if !ok {
			return s.Unavailable("EXTERNAL_RULE_UNSUPPORTED", "外部账号分离条件无效")
		}
		identity := ApMap(implementation["identity"])
		if must && (identity["status"] != "available" || Text(identity["principal_ref"]) == "" || result["implementation_principal_ref"] != identity["principal_ref"] || ApContains(result["principals"], Text(identity["principal_ref"]))) {
			return s.Reject("EXTERNAL_REQUIREMENT", "外部规则要求不同账号，当前结果不满足")
		}
	}
	return nil
}
