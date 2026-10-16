package governance

import "github.com/iloveZzz/yss-cli/internal/governance/approval"

import (
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"path"
	"strings"
	"time"
)

const researchValidator = ".agents/skills/yss-research/scripts/validate-research-package.mjs"

func init() {
	kernel.RegisterSemanticValidator("research-completion", verifyResearchCompletionSemantic)
}

// The recorded Node command is an historical identifier, never an executable.
func verifyResearchCompletionSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	state, err := s.TransitionState(ref, opts["task-ref"])
	if err != nil {
		return err
	}
	reconciliation := kernel.SemMap(state["context_reconciliation"])
	if reconciliation["status"] != "not-applicable" || strings.TrimSpace(kernel.Text(reconciliation["reason"])) == "" {
		return s.Reject("RESEARCH_CONTEXT", "研究 Context 不适用说明缺失")
	}
	contextRef := kernel.Text(reconciliation["ref"])
	if _, err = s.Bytes(contextRef); err != nil {
		return err
	}
	for _, key := range []string{"blocking_signals", "drift", "violation", "new_impacts", "stale_candidates"} {
		a, ok := state[key].([]any)
		if !ok || len(a) != 0 {
			return s.Reject("RESEARCH_BLOCKED", "研究存在未解决状态: "+key)
		}
	}
	bound := kernel.SemMap(state["research_verification"])
	if err = s.Basis([]any{bound}); err != nil {
		return err
	}
	record, err := s.Doc(kernel.Text(bound["ref"]))
	if err != nil {
		return err
	}
	if kernel.ContractN(record["schema_version"]) != 1 || record["kind"] != "maintenance-research-verification" {
		return s.Reject("RESEARCH_VERIFICATION", "未知研究验证版本")
	}
	inputs := kernel.SemMap(record["inputs"])
	for _, key := range []string{"brief", "evidence", "validator"} {
		if err = s.Basis([]any{inputs[key]}); err != nil {
			return err
		}
	}
	briefRef, evidenceRef := kernel.Text(kernel.SemMap(inputs["brief"])["ref"]), kernel.Text(kernel.SemMap(inputs["evidence"])["ref"])
	if kernel.Text(kernel.SemMap(inputs["validator"])["ref"]) != researchValidator {
		return s.Reject("RESEARCH_VALIDATOR", "研究校验器身份不匹配")
	}
	// The recorded validator stays a bound input (its bytes must exist and match the
	// record above), but the rules enforced are the native semantics below, not a
	// fixed script version.
	if _, err = s.Bytes(researchValidator); err != nil {
		return err
	}
	started, a := time.Parse(time.RFC3339Nano, kernel.Text(record["started_at"]))
	completed, b := time.Parse(time.RFC3339Nano, kernel.Text(record["completed_at"]))
	exit, ok := kernel.Integer(record["exit_code"])
	if !ok || exit != 0 || a != nil || b != nil || completed.Before(started) {
		return s.Reject("RESEARCH_VERIFICATION", "研究验证未实际通过")
	}
	if !kernel.ApEqual(record["command"], []any{"node", researchValidator, briefRef, evidenceRef}) {
		return s.Reject("RESEARCH_COMMAND", "研究验证命令记录不匹配")
	}
	for _, key := range []string{"stdout", "stderr"} {
		if err = s.Basis([]any{record[key]}); err != nil {
			return err
		}
	}
	expected, err := validateResearchPackageSemantic(s, briefRef, evidenceRef)
	if err != nil {
		return err
	}
	if len(expected) == 0 {
		if _, present := inputs["competitive"]; present {
			return s.Reject("RESEARCH_BINDING", "无竞品扩展却登记竞品绑定")
		}
	} else {
		if err = s.Basis(inputs["competitive"]); err != nil {
			return err
		}
		if !kernel.ApEqual(inputs["competitive"], expected) {
			return s.Reject("RESEARCH_BINDING", "竞品输入闭包不匹配")
		}
	}
	refs := []string{contextRef, kernel.Text(bound["ref"]), briefRef, evidenceRef}
	for _, v := range expected {
		refs = append(refs, kernel.Text(kernel.SemMap(v)["ref"]))
	}
	for _, value := range refs {
		if !kernel.SemHas(state["evidence_refs"], value) {
			return s.Reject("RESEARCH_EVIDENCE", "研究证据未登记: "+value)
		}
	}
	if opts["continuing"] == "true" {
		authorization := kernel.SemMap(state["maintenance_authorization"])
		if err = s.Basis([]any{authorization}); err != nil {
			return err
		}
		requirement, e := s.Doc(kernel.Text(authorization["ref"]))
		if e != nil {
			return e
		}
		if requirement["boundary"] != "template-maintenance-scope" || !kernel.SemHas(requirement["scope"], "work-unit.ssot-update") {
			return s.Reject("RESEARCH_AUTHORIZATION", "继续维护缺少已有范围授权")
		}
		if _, e = approval.AssertUserDecisionRequirementSemantic(s, requirement); e != nil {
			return e
		}
	}
	return nil
}

func researchIndex(s *kernel.SemanticSession, value any, label string, nonempty bool) (map[string]map[string]any, error) {
	rows, ok := value.([]any)
	if !ok || nonempty && len(rows) == 0 {
		return nil, s.Reject("RESEARCH_STRUCTURE", label+" 必须是适用数组")
	}
	out := map[string]map[string]any{}
	for _, v := range rows {
		row, ok := kernel.Object(v)
		id := kernel.Text(row["id"])
		if !ok || strings.TrimSpace(id) == "" || out[id] != nil {
			return nil, s.Reject("RESEARCH_STRUCTURE", label+" ID 缺失或重复")
		}
		out[id] = row
	}
	return out, nil
}

func researchText(row map[string]any, key string) bool {
	v, ok := row[key].(string)
	return ok && strings.TrimSpace(v) != ""
}

func researchArray(row map[string]any, key string, nonempty bool) bool {
	a, ok := row[key].([]any)
	return ok && (!nonempty || len(a) > 0)
}

func researchChoice(row map[string]any, key string, values ...string) bool {
	return kernel.SemHas(values, kernel.Text(row[key]))
}

func validateResearchPackageSemantic(s *kernel.SemanticSession, briefRef, evidenceRef string) ([]any, error) {
	slug := strings.TrimSuffix(path.Base(briefRef), "-research-brief.md")
	if path.Dir(briefRef) != path.Dir(evidenceRef) || !strings.HasSuffix(briefRef, "-research-brief.md") || slug == "" || path.Base(evidenceRef) != slug+"-evidence.yaml" {
		return nil, s.Reject("RESEARCH_PATH", "研究简报与台账须同目录、同 slug")
	}
	brief, err := s.Bytes(briefRef)
	if err != nil {
		return nil, err
	}
	for _, heading := range []string{"Research Scope", "Executive Read", "Findings", "Counter-Signals", "Source Map", "Decision Handoff", "Evidence Limitations"} {
		if !strings.Contains(string(brief), "## "+heading) {
			return nil, s.Reject("RESEARCH_STRUCTURE", "简报缺少标题: "+heading)
		}
	}
	raw, err := s.Bytes(evidenceRef)
	if err != nil {
		return nil, err
	}
	// The existing evidence contract requires JSON-compatible YAML, not general YAML.
	parsed, parseErr := schema.Parse(raw)
	data, objectRoot := kernel.Object(parsed)
	if !json.Valid(raw) || parseErr != nil || !objectRoot {
		return nil, s.Reject("RESEARCH_STRUCTURE", "研究台账须为 JSON 对象")
	}
	if kernel.ContractN(data["schema_version"]) != 1 || !researchChoice(data, "profile", "technical-evidence", "strategy-evidence") || data["mode"] != "evidence-audited" {
		return nil, s.Reject("RESEARCH_STRUCTURE", "未知研究 Profile、模式或版本")
	}
	scope := kernel.SemMap(data["scope"])
	for _, key := range []string{"topic", "audience", "time_horizon"} {
		if !researchText(scope, key) {
			return nil, s.Reject("RESEARCH_STRUCTURE", "研究范围缺少 "+key)
		}
	}
	for _, key := range []string{"research_questions", "inclusion_criteria", "exclusion_criteria"} {
		if !researchArray(scope, key, true) {
			return nil, s.Reject("RESEARCH_STRUCTURE", "研究范围缺少 "+key)
		}
	}
	ownership := kernel.SemMap(data["ownership"])
	if ownership["research_owner"] != "yss-research" || !researchText(ownership, "downstream_owner") {
		return nil, s.Reject("RESEARCH_STRUCTURE", "研究 owner 缺失")
	}
	decision, present := ownership["decision_ref"]
	if !present || decision != nil && !researchText(ownership, "decision_ref") {
		return nil, s.Reject("RESEARCH_STRUCTURE", "decision_ref 必须明确为 null 或引用")
	}
	searches, err := researchIndex(s, data["search_log"], "search_log", true)
	if err != nil {
		return nil, err
	}
	for _, row := range searches {
		if !researchText(row, "channel") || !researchText(row, "query_or_corpus") || !researchText(row, "searched_at") || !researchChoice(row, "result", "results-found", "none-found", "access-failed", "excluded") {
			return nil, s.Reject("RESEARCH_STRUCTURE", "反向搜索记录不完整")
		}
	}
	evidence, err := researchIndex(s, data["evidence_items"], "evidence_items", true)
	if err != nil {
		return nil, err
	}
	for _, row := range evidence {
		for _, key := range []string{"source_class", "source_ref", "locator", "observed_at", "observation"} {
			if !researchText(row, key) {
				return nil, s.Reject("RESEARCH_STRUCTURE", "来源记录缺少 "+key)
			}
		}
		if !researchChoice(row, "source_level", "primary", "direct-experience", "near-primary", "secondary", "lead-only") || !researchChoice(row, "visibility", "public", "internal") || !researchChoice(row, "stance", "support", "counter") || !researchArray(row, "limitations", false) {
			return nil, s.Reject("RESEARCH_STRUCTURE", "来源级别、立场或限制非法")
		}
	}
	claims, err := researchIndex(s, data["claims"], "claims", true)
	if err != nil {
		return nil, err
	}
	for id, row := range claims {
		_, boolean := row["decision_bearing"].(bool)
		if !researchChoice(row, "claim_kind", "technical-fact", "user-problem", "business-constraint", "domain-boundary", "business-rule", "mvp", "non-goal", "success-criterion", "stage-decision-basis", "background") || !researchText(row, "statement") || !boolean || !researchArray(row, "evidence_refs", true) || !researchArray(row, "counter_signal_refs", true) || !researchChoice(row, "audit_status", "supported", "partially-supported", "unsupported", "not-audited") || !researchChoice(row, "confidence", "low", "medium", "high") || !researchChoice(row, "disposition", "publish", "qualify", "needs-deeper-research") {
			return nil, s.Reject("RESEARCH_STRUCTURE", "Claim 不完整: "+id)
		}
		primary := false
		for _, v := range kernel.SemList(row["evidence_refs"]) {
			e := evidence[kernel.Text(v)]
			if e == nil || e["stance"] != "support" || e["source_level"] == "lead-only" {
				return nil, s.Reject("RESEARCH_REFERENCE", "Claim 支持来源不合法: "+id)
			}
			primary = primary || e["source_level"] == "primary"
		}
		for _, v := range kernel.SemList(row["counter_signal_refs"]) {
			e, search := evidence[kernel.Text(v)], searches[kernel.Text(v)]
			if e == nil && search == nil || e != nil && e["stance"] != "counter" || search != nil && search["result"] != "none-found" {
				return nil, s.Reject("RESEARCH_REFERENCE", "Claim 反证引用不合法: "+id)
			}
		}
		if data["profile"] == "technical-evidence" && row["decision_bearing"] == true && !primary || data["profile"] == "strategy-evidence" && kernel.SemHas([]string{"user-problem", "business-constraint", "domain-boundary", "business-rule", "mvp", "non-goal", "success-criterion", "stage-decision-basis"}, kernel.Text(row["claim_kind"])) && row["decision_bearing"] != true {
			return nil, s.Reject("RESEARCH_EVIDENCE", "决策 Claim 缺少适用证据: "+id)
		}
		if row["audit_status"] == "partially-supported" && row["disposition"] != "qualify" || row["audit_status"] == "unsupported" && (row["disposition"] != "needs-deeper-research" || row["decision_bearing"] == true) || !strings.Contains(string(brief), id) {
			return nil, s.Reject("RESEARCH_EVIDENCE", "Claim 处置或简报引用不完整: "+id)
		}
	}
	summary := kernel.SemMap(data["audit_summary"])
	if !researchArray(data, "source_gaps", false) || summary["status"] != "complete" || !researchArray(summary, "audited_claim_ids", true) || !researchArray(summary, "notes", false) {
		return nil, s.Reject("RESEARCH_AUDIT", "研究审计未完成")
	}
	for _, v := range kernel.SemList(summary["audited_claim_ids"]) {
		if claims[kernel.Text(v)] == nil {
			return nil, s.Reject("RESEARCH_AUDIT", "审计引用未知 Claim")
		}
	}
	for id, row := range claims {
		if row["decision_bearing"] == true && (!kernel.SemHas(summary["audited_claim_ids"], id) || row["audit_status"] == "not-audited") {
			return nil, s.Reject("RESEARCH_AUDIT", "决策 Claim 未审计: "+id)
		}
	}
	if _, present := data["competitive_analysis"]; !present {
		return nil, nil
	}
	return validateCompetitiveResearchSemantic(s, data, evidenceRef, slug, claims, evidence, searches)
}
