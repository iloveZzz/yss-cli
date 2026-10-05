package governance

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func (s *semanticSession) maintenanceReview(evidence map[string]any) error {
	mode := map[string]string{"focused-independent-review": "focused-independent", "formal-independent-review": "formal-independent"}[text(evidence["kind"])]
	ref := text(evidence["command"])
	if mode == "" || ref == "" {
		return s.reject("MAINTENANCE_REVIEW", "独立审查证据类型或引用无效")
	}
	if strings.HasPrefix(ref, "maintenance:") {
		if err := s.basis([]any{map[string]any{"ref": ref, "digest": evidence["evidence_digest"]}}); err != nil {
			return err
		}
	}
	raw, err := s.bytes(ref)
	if err != nil {
		return err
	}
	if strings.HasSuffix(strings.ToLower(ref), ".md") {
		if mode == "formal-independent" {
			return s.reject("MAINTENANCE_REVIEW", "正式独立审查须使用结构化记录")
		}
		return s.maintenanceReviewBody(string(raw), true)
	}
	if !semHas([]string{".json", ".yaml", ".yml"}, path.Ext(strings.ToLower(ref))) {
		return s.reject("MAINTENANCE_REVIEW", "审查结论须为 Markdown 或结构化记录")
	}
	record, err := s.doc(ref)
	if err != nil {
		return err
	}
	if err = s.validateSchema(".template-source/process/schemas/maintenance-review-record.schema.json", record); err != nil {
		return err
	}
	if record["review_mode"] != mode || record["status"] != "approved" || record["reviewer_id"] == record["implementation_actor_id"] {
		return s.reject("MAINTENANCE_REVIEW", "审查模式、结论或独立身份无效")
	}
	if _, err = time.Parse(time.RFC3339Nano, text(record["reviewed_at"])); err != nil {
		return s.reject("MAINTENANCE_REVIEW", "审查时间无法解析")
	}
	for _, row := range semList(record["findings"]) {
		f := semMap(row)
		if semHas([]string{"violation", "drift", "new_impacts"}, text(f["disposition"])) || !semHas([]string{"resolved", "not-applicable"}, text(f["status"])) {
			return s.reject("MAINTENANCE_REVIEW", "approved 审查记录仍包含阻断 finding")
		}
	}
	manifestRef := text(record["candidate_snapshot_ref"])
	manifest, err := s.doc(manifestRef)
	if err != nil {
		return err
	}
	if err = s.maintenanceCandidate(manifest, record); err != nil {
		return err
	}
	task, err := s.doc(text(record["task_package_ref"]))
	if err != nil {
		return err
	}
	if err = s.validateSchema(approvalTaskSchemaRef, task); err != nil {
		return err
	}
	rc, contract := semMap(task["review_context"]), semMap(task["contract"])
	if task["execution_state"] != "Reviewer" || task["actor_id"] != record["reviewer_id"] || rc["implementation_actor_id"] != record["implementation_actor_id"] || contract["kind"] != "template-maintenance" || contract["status"] != "issued" || !semHas(task["inputs"], manifestRef) || !semHas(task["expected_evidence_files"], text(record["review_report_ref"])) {
		return s.reject("MAINTENANCE_REVIEW", "正式审查任务与候选、实施者或结果未绑定")
	}
	for _, k := range []string{"core_skills", "forbidden_skills"} {
		if _, ok := semMap(task["skill_source"])[k].([]any); !ok {
			return s.reject("MAINTENANCE_REVIEW", "审查任务缺少角色技能约束")
		}
	}
	for _, k := range []string{"forbidden_actions", "expected_outputs"} {
		if _, ok := task[k].([]any); !ok {
			return s.reject("MAINTENANCE_REVIEW", "审查任务缺少禁止事项或预期输出")
		}
	}
	body, err := s.bytes(text(record["review_report_ref"]))
	if err != nil {
		return err
	}
	if err = s.maintenanceReviewBody(string(body), false); err != nil {
		return err
	}
	for _, k := range []string{"reviewer_id", "implementation_actor_id", "candidate_digest"} {
		if !strings.Contains(string(body), text(record[k])) {
			return s.reject("MAINTENANCE_REVIEW", "审查报告未绑定 "+k)
		}
	}
	return nil
}

func (s *semanticSession) maintenanceReviewBody(body string, legacy bool) error {
	for _, pattern := range []string{
		`只(?:提出|用于|是一份?).{0,12}审查请求`, `不是.{0,16}(?:独立审查|审查).{0,12}(?:结论|记录)`, `不(?:是|构成).{0,20}(?:审查结论|通过结论)`, `须由(?:其他|非实施者|独立审查者).{0,40}(?:改写|给出结论|完成审查)`, `本条\s*result=pass\s*只表示.{0,60}不表示审查已通过`,
		`(?i)(?:审查结论|结论|status|裁决)\s*[：:]?[^\n]{0,30}(?:未通过|changes-requested|blocked|失败|未闭合)`, `(?:不能|不得|不可|尚未|不应|无法|并非|不是|未能)[^\n]{0,40}(?:正式)?独立审查通过`,
	} {
		if regexp.MustCompile(pattern).MatchString(body) {
			return s.reject("MAINTENANCE_REVIEW", "审查报告是请求、自述或包含否定结论")
		}
	}
	if !regexp.MustCompile(`(?i)(?:审查角色|审查者|reviewer_id)\s*[：:]`).MatchString(body) {
		return s.reject("MAINTENANCE_REVIEW", "审查报告缺少独立身份")
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
		return s.reject("MAINTENANCE_REVIEW", "审查报告缺少明确通过结论")
	}
	return nil
}

func (s *semanticSession) maintenanceCandidate(manifest, record map[string]any) error {
	for _, k := range []string{"review_mode", "review_base_ref", "merge_base", "implementation_candidate_ref", "candidate_snapshot_ref", "candidate_digest", "tracked_diff_command", "commit_list_command", "untracked_inventory_command", "untracked_diff_command", "snapshot_stream_ref", "tracked_diff_ref"} {
		if strings.TrimSpace(text(manifest[k])) == "" {
			return s.reject("MAINTENANCE_CANDIDATE", "候选 Manifest 缺少 "+k)
		}
	}
	version, ok := integer(manifest["schema_version"])
	if !ok || version < 1 || version > 2 {
		return s.unavailable("CAPABILITY", "未知维护候选 Manifest 版本")
	}
	if manifest["review_mode"] != "worktree" || manifest["candidate_snapshot_ref"] != record["candidate_snapshot_ref"] || manifest["candidate_digest"] != record["candidate_digest"] {
		return s.reject("MAINTENANCE_CANDIDATE", "审查候选主体或摘要不一致")
	}
	for _, k := range []string{"untracked_files", "untracked_path_bytes"} {
		if _, ok := manifest[k].([]any); !ok {
			return s.reject("MAINTENANCE_CANDIDATE", "候选缺少 untracked inventory")
		}
	}
	packed := manifest["storage"] == "packed-stream"
	if !packed {
		if manifest["storage"] != nil && manifest["storage"] != "" {
			return s.unavailable("CAPABILITY", "未知候选流存储格式")
		}
		if _, ok := manifest["untracked_content_refs"].([]any); !ok {
			return s.reject("MAINTENANCE_CANDIDATE", "候选缺少独立 untracked snapshots")
		}
	} else if manifest["untracked_content_refs"] != nil {
		return s.reject("MAINTENANCE_CANDIDATE", "packed-stream 不允许重复保存 untracked snapshots")
	}
	streamRef, diffRef := text(manifest["snapshot_stream_ref"]), text(manifest["tracked_diff_ref"])
	if version == 2 {
		excluded, ok := manifest["excluded_paths"].([]any)
		if !ok || len(excluded) != 0 || manifest["reference_base"] != "bundle" || manifest["workspace_id"] != safefs.Digest([]byte(s.root)) {
			return s.reject("MAINTENANCE_CANDIDATE", "候选 Bundle 不属于当前工作区或包含排除范围")
		}
		if streamRef != "candidate.bin" || diffRef != "tracked.diff" {
			return s.reject("MAINTENANCE_CANDIDATE", "候选成员须为同目录规范文件")
		}
		base := path.Dir(text(record["candidate_snapshot_ref"]))
		streamRef, diffRef = base+"/"+streamRef, base+"/"+diffRef
	}
	stream, err := s.bytes(streamRef)
	if err != nil {
		return err
	}
	if safefs.Digest(stream) != strings.TrimPrefix(text(record["candidate_digest"]), "sha256:") {
		return s.reject("MAINTENANCE_CANDIDATE", "候选流字节摘要过期")
	}
	diff, err := s.bytes(diffRef)
	if err != nil {
		return err
	}
	const magic = "YSS-WORKTREE-CANDIDATE-V1\x00"
	if !bytes.HasPrefix(stream, []byte(magic)) {
		return s.reject("MAINTENANCE_CANDIDATE", "候选流格式标记无效")
	}
	position := len(magic)
	take := func(n uint64) ([]byte, error) {
		if n > 9007199254740991 || n > uint64(len(stream)-position) {
			return nil, s.reject("MAINTENANCE_CANDIDATE", "候选流截断或长度无效")
		}
		out := stream[position : position+int(n)]
		position += int(n)
		return out, nil
	}
	length := func(width uint64) (uint64, error) {
		raw, e := take(width)
		if e != nil {
			return 0, e
		}
		if width == 8 {
			return binary.BigEndian.Uint64(raw), nil
		}
		return uint64(binary.BigEndian.Uint32(raw)), nil
	}
	marker, err := take(1)
	if err != nil {
		return err
	}
	if marker[0] != 0x54 {
		return s.reject("MAINTENANCE_CANDIDATE", "缺少 tracked record")
	}
	n, err := length(8)
	if err != nil {
		return err
	}
	tracked, err := take(n)
	if err != nil {
		return err
	}
	if !bytes.Equal(tracked, diff) {
		return s.reject("MAINTENANCE_CANDIDATE", "tracked diff 与候选流不一致")
	}
	paths := []any{}
	var previous []byte
	for position < len(stream) {
		if err = s.guard(); err != nil {
			return err
		}
		marker, err = take(1)
		if err != nil {
			return err
		}
		if marker[0] != 0x55 {
			return s.reject("MAINTENANCE_CANDIDATE", "候选流 record kind 无效")
		}
		n, err = length(8)
		if err != nil {
			return err
		}
		rawPath, err := take(n)
		if err != nil {
			return err
		}
		if len(rawPath) == 0 || bytes.ContainsRune(rawPath, 0) || previous != nil && bytes.Compare(previous, rawPath) >= 0 {
			return s.reject("MAINTENANCE_CANDIDATE", "候选路径为空、重复或排序非法")
		}
		previous = rawPath
		mode, err := length(4)
		if err != nil {
			return err
		}
		kind, err := take(1)
		if err != nil {
			return err
		}
		if kind[0] != 0x52 && kind[0] != 0x4c || kind[0] == 0x52 && mode&0170000 != 0100000 || kind[0] == 0x4c && mode&0170000 != 0120000 {
			return s.reject("MAINTENANCE_CANDIDATE", "候选 kind 与 mode 无效")
		}
		n, err = length(8)
		if err != nil {
			return err
		}
		content, err := take(n)
		if err != nil {
			return err
		}
		if !packed {
			refs := semStrings(manifest["untracked_content_refs"])
			if len(refs) <= len(paths) {
				return s.reject("MAINTENANCE_CANDIDATE", "候选 untracked snapshot 数量不足")
			}
			stored, err := s.bytes(refs[len(paths)])
			if err != nil {
				return err
			}
			if !bytes.Equal(content, stored) {
				return s.reject("MAINTENANCE_CANDIDATE", "untracked snapshot 字节不一致")
			}
		}
		paths = append(paths, base64.StdEncoding.EncodeToString(rawPath))
	}
	if !apEqual(paths, manifest["untracked_path_bytes"]) || len(semList(manifest["untracked_files"])) != len(paths) || !packed && len(semList(manifest["untracked_content_refs"])) != len(paths) {
		return s.reject("MAINTENANCE_CANDIDATE", "候选 untracked inventory 与流不一致")
	}
	return nil
}
