package evidence

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"path"
	"regexp"
	"strings"
	"time"
)

func MaintenanceReview(s *kernel.SemanticSession, evidence map[string]any) error {
	mode := map[string]string{"focused-independent-review": "focused-independent", "formal-independent-review": "formal-independent"}[kernel.Text(evidence["kind"])]
	ref := kernel.Text(evidence["command"])
	if mode == "" || ref == "" {
		return s.Reject("MAINTENANCE_REVIEW", "独立审查证据类型或引用无效")
	}
	if strings.HasPrefix(ref, "maintenance:") {
		if err := s.Basis([]any{map[string]any{"ref": ref, "digest": evidence["evidence_digest"]}}); err != nil {
			return err
		}
	}
	raw, err := s.Bytes(ref)
	if err != nil {
		return err
	}
	if strings.HasSuffix(strings.ToLower(ref), ".md") {
		if mode == "formal-independent" {
			return s.Reject("MAINTENANCE_REVIEW", "正式独立审查须使用结构化记录")
		}
		return maintenanceReviewBody(s, string(raw), true)
	}
	if !kernel.SemHas([]string{".json", ".yaml", ".yml"}, path.Ext(strings.ToLower(ref))) {
		return s.Reject("MAINTENANCE_REVIEW", "审查结论须为 Markdown 或结构化记录")
	}
	record, err := s.Doc(ref)
	if err != nil {
		return err
	}
	if err = s.ValidateSchema(".template-source/process/schemas/maintenance-review-record.schema.json", record); err != nil {
		return err
	}
	if record["review_mode"] != mode || record["status"] != "approved" || record["reviewer_id"] == record["implementation_actor_id"] {
		return s.Reject("MAINTENANCE_REVIEW", "审查模式、结论或独立身份无效")
	}
	if _, err = time.Parse(time.RFC3339Nano, kernel.Text(record["reviewed_at"])); err != nil {
		return s.Reject("MAINTENANCE_REVIEW", "审查时间无法解析")
	}
	for _, row := range kernel.SemList(record["findings"]) {
		f := kernel.SemMap(row)
		if kernel.SemHas([]string{"violation", "drift", "new_impacts"}, kernel.Text(f["disposition"])) || !kernel.SemHas([]string{"resolved", "not-applicable"}, kernel.Text(f["status"])) {
			return s.Reject("MAINTENANCE_REVIEW", "approved 审查记录仍包含阻断 finding")
		}
	}
	manifestRef := kernel.Text(record["candidate_snapshot_ref"])
	manifest, err := s.Doc(manifestRef)
	if err != nil {
		return err
	}
	if err = s.MaintenanceCandidate(manifest, record); err != nil {
		return err
	}
	task, err := s.Doc(kernel.Text(record["task_package_ref"]))
	if err != nil {
		return err
	}
	if err = s.ValidateSchema(kernel.ApprovalTaskSchemaRef, task); err != nil {
		return err
	}
	rc, contract := kernel.SemMap(task["review_context"]), kernel.SemMap(task["contract"])
	if task["execution_state"] != "Reviewer" || task["actor_id"] != record["reviewer_id"] || rc["implementation_actor_id"] != record["implementation_actor_id"] || contract["kind"] != "template-maintenance" || contract["status"] != "issued" || !kernel.SemHas(task["inputs"], manifestRef) || !kernel.SemHas(task["expected_evidence_files"], kernel.Text(record["review_report_ref"])) {
		return s.Reject("MAINTENANCE_REVIEW", "正式审查任务与候选、实施者或结果未绑定")
	}
	for _, k := range []string{"core_skills", "forbidden_skills"} {
		if _, ok := kernel.SemMap(task["skill_source"])[k].([]any); !ok {
			return s.Reject("MAINTENANCE_REVIEW", "审查任务缺少角色技能约束")
		}
	}
	for _, k := range []string{"forbidden_actions", "expected_outputs"} {
		if _, ok := task[k].([]any); !ok {
			return s.Reject("MAINTENANCE_REVIEW", "审查任务缺少禁止事项或预期输出")
		}
	}
	body, err := s.Bytes(kernel.Text(record["review_report_ref"]))
	if err != nil {
		return err
	}
	if err = maintenanceReviewBody(s, string(body), false); err != nil {
		return err
	}
	for _, k := range []string{"reviewer_id", "implementation_actor_id", "candidate_digest"} {
		if !strings.Contains(string(body), kernel.Text(record[k])) {
			return s.Reject("MAINTENANCE_REVIEW", "审查报告未绑定 "+k)
		}
	}
	return nil
}

func maintenanceReviewBody(s *kernel.SemanticSession, body string, legacy bool) error {
	for _, pattern := range []string{
		`只(?:提出|用于|是一份?).{0,12}审查请求`, `不是.{0,16}(?:独立审查|审查).{0,12}(?:结论|记录)`, `不(?:是|构成).{0,20}(?:审查结论|通过结论)`, `须由(?:其他|非实施者|独立审查者).{0,40}(?:改写|给出结论|完成审查)`, `本条\s*result=pass\s*只表示.{0,60}不表示审查已通过`,
		`(?i)(?:审查结论|结论|status|裁决)\s*[：:]?[^\n]{0,30}(?:未通过|changes-requested|blocked|失败|未闭合)`, `(?:不能|不得|不可|尚未|不应|无法|并非|不是|未能)[^\n]{0,40}(?:正式)?独立审查通过`,
	} {
		if regexp.MustCompile(pattern).MatchString(body) {
			return s.Reject("MAINTENANCE_REVIEW", "审查报告是请求、自述或包含否定结论")
		}
	}
	if !regexp.MustCompile(`(?i)(?:审查角色|审查者|reviewer_id)\s*[：:]`).MatchString(body) {
		return s.Reject("MAINTENANCE_REVIEW", "审查报告缺少独立身份")
	}
	approved := regexp.MustCompile("(?mi)^(?:[-*]\\s*)?(?:审查结论|结论)\\s*[：:]\\s*(?:`|\\*\\*)?(?:pass|approved|通过)(?:`|\\*\\*)?(?:[。.]|\\s|$)").MatchString(body)
	if legacy {
		approved = approved || regexp.MustCompile(`(?m)^通过[。.]?`).MatchString(body) || regexp.MustCompile(`本次(?:正式|聚焦)?独立审查通过`).MatchString(body)
		for _, line := range strings.Split(body, "\n") {
			if regexp.MustCompile(`裁决[：:]\s*.{0,40}(?:通过|闭合)`).MatchString(line) && !strings.Contains(line, "未闭合") {
				approved = true
			}
		}
	}
	if !approved {
		return s.Reject("MAINTENANCE_REVIEW", "审查报告缺少明确通过结论")
	}
	return nil
}
