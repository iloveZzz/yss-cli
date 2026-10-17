package governance

import "github.com/iloveZzz/yss-cli/internal/governance/approval"

import (
	"github.com/iloveZzz/yss-cli/internal/governance/handoff"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
	"path/filepath"
	"sort"
	"strings"
)

func init() {
	kernel.RegisterSemanticValidator("plan-checkpoint", verifyPlanCheckpointSemantic)
	kernel.RegisterRoleIndependentValidator("plan-checkpoint", verifyPlanCheckpointSemantic)
	kernel.RegisterSemanticValidator("plan-review-task", verifyPlanReviewTaskSemantic)
	kernel.RegisterRoleIndependentValidator("plan-review-task", verifyPlanReviewTaskSemantic)
	kernel.RegisterSemanticValidator("plan-spec-entry", verifyPlanEntrySemantic)
	kernel.RegisterRoleIndependentValidator("plan-spec-entry", verifyPlanEntrySemantic)
	kernel.RegisterSemanticValidator("plan-aggregate", verifyPlanAggregateSemantic)
	kernel.RegisterRoleIndependentValidator("plan-aggregate", verifyPlanAggregateSemantic)
	kernel.RegisterSemanticValidator("source-plan-approval", verifySourcePlanApprovalSemantic)
	kernel.RegisterRoleIndependentValidator("source-plan-approval", verifySourcePlanApprovalSemantic)
}

func verifySourcePlanApprovalSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	checkpoint, err := sourcePlanApprovalCheckpoint(s, ref, opts)
	if err != nil {
		return err
	}
	cp, err := s.Doc(checkpoint)
	if err != nil {
		return err
	}
	if opts["asset-ref"] != "" && cp["plan_review_ref"] != opts["asset-ref"] {
		return s.Reject("HANDOFF_PLAN_OWNER_REQUIRED", "来源 checkpoint 的 Plan 主体与待消费资产矛盾")
	}
	if err = s.ValidateSchema(".template-spec/process/schemas/lifecycle-checkpoint.schema.json", cp); err != nil {
		return err
	}
	if kernel.First(kernel.Text(cp["plan_approval_ref"]), kernel.Text(kernel.SemMap(kernel.SemMap(cp["gates"])["gate.plan-approved"])["approval_ref"])) != ref {
		return s.Reject("HANDOFF_PLAN_OWNER_REQUIRED", "来源 checkpoint 未持有该 Plan 聚合批准")
	}
	return s.Verify("plan-aggregate", checkpoint, map[string]string{"approval": ref})
}

func sourcePlanApprovalCheckpoint(s *kernel.SemanticSession, ref string, opts map[string]string) (string, error) {
	checkpoint := opts["checkpoint-ref"]
	if checkpoint != "" {
		if err := kernel.RejectProgressionEvidence(s, checkpoint); err != nil {
			return "", err
		}
		bound, err := s.Bind(checkpoint)
		if err != nil {
			return "", err
		}
		if bound.Digest != opts["checkpoint-digest"] {
			return "", s.Reject("HANDOFF_PLAN_OWNER_REQUIRED", "源 Plan 封存 checkpoint 原始字节漂移")
		}
		owner, err := s.Doc(checkpoint)
		if err != nil {
			return "", err
		}
		gate := kernel.SemMap(kernel.SemMap(owner["gates"])["gate.plan-approved"])
		if gate["status"] != "approved" || kernel.Text(gate["approval_ref"]) != ref {
			return "", s.Reject("HANDOFF_PLAN_OWNER_REQUIRED", "源 Plan 封存 checkpoint 未绑定当前批准引用")
		}
		if primary := kernel.Text(owner["plan_approval_ref"]); primary != "" && primary != ref {
			return "", s.Reject("HANDOFF_PLAN_OWNER_REQUIRED", "源 Plan 封存 checkpoint 聚合批准引用冲突")
		}
	}
	if opts["task-ref"] != "" {
		task, err := s.Doc(opts["task-ref"])
		if err != nil {
			return "", err
		}
		taskCheckpoint := kernel.Text(task["checkpoint_ref"])
		if checkpoint != "" && taskCheckpoint != "" && taskCheckpoint != checkpoint {
			return "", s.Reject("HANDOFF_PLAN_OWNER_REQUIRED", "源 Plan 任务与封存 checkpoint 归属不一致")
		}
		if checkpoint == "" {
			checkpoint = taskCheckpoint
		}
	}
	if checkpoint == "" {
		candidates := []string{}
		originals := []string{}
		for original := range s.Aliases {
			originals = append(originals, original)
		}
		// Ordinary roots retain Tracker-defined compatibility read roots. Filter
		// the existing observed inventory; never discover an owner in an arbitrary
		// directory, or let a sealed package consult the receiving work layout.
		// A direct fixed-Context view keeps the same Tracker owner boundary;
		// manifest-backed child sessions still use only their sealed inventory.
		contextOnly := s.Args["source-context-snapshot"] == "true" && len(s.Aliases) == 1 && s.Aliases["CONTEXT.md"] == "source-context.snapshot.md"
		if (len(s.Aliases) == 0 || contextOnly) && s.V.Virtual == nil && s.RuleSession == nil {
			tracker, err := s.Bytes(worklayout.TrackerRef)
			if err != nil {
				return "", err
			}
			layout, err := worklayout.FromDocument(s.Root, tracker)
			if err != nil {
				return "", err
			}
			for _, candidate := range s.ContextInventory {
				allowed := strings.HasPrefix(candidate, "docs/") || strings.HasPrefix(candidate, ".yss/") || !strings.Contains(candidate, "/")
				for _, root := range layout.ScanRoots {
					if !strings.HasPrefix(root, "docs/") && strings.HasPrefix(candidate, root+"/") {
						allowed = true
					}
				}
				if allowed {
					originals = append(originals, candidate)
				}
			}
		}
		sort.Strings(originals)
		for _, original := range originals {
			if !kernel.SemHas([]string{".json", ".yaml", ".yml"}, filepath.Ext(original)) {
				continue
			}
			value, err := s.Doc(original)
			if err != nil {
				continue
			}
			if value["gate_id"] != nil || value["gates"] == nil {
				continue
			}
			if kernel.Text(kernel.SemMap(kernel.SemMap(value["gates"])["gate.plan-approved"])["approval_ref"]) == ref {
				candidates = append(candidates, original)
			}
		}
		if len(candidates) != 1 {
			return "", s.Reject("HANDOFF_PLAN_OWNER_REQUIRED", "来源 Plan 批准缺少唯一已导出的当前 checkpoint")
		}
		checkpoint = candidates[0]
	}
	return checkpoint, nil
}

func planNormalizedBasis(value any) []any {
	out := []any{}
	for _, r := range kernel.SemList(value) {
		m := kernel.SemMap(r)
		out = append(out, map[string]any{"ref": m["ref"], "digest": strings.TrimPrefix(kernel.Text(m["digest"]), "sha256:")})
	}
	return out
}

func verifyPlanReviewTaskSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	task, err := s.Doc(ref)
	if err != nil {
		return err
	}
	rc := kernel.SemMap(task["review_context"])
	candidateRef, candidateDigest := kernel.Text(rc["candidate_ref"]), kernel.Text(rc["candidate_digest"])
	if s.GovernanceVersion == 2 {
		if kernel.ApNumber(task["schema_version"]) != 3 {
			return s.Reject("TASK_VERSION", "当前Plan任务须为task3")
		}
		candidate := kernel.ApMap(rc["candidate"])
		candidateRef, candidateDigest = kernel.Text(candidate["ref"]), kernel.Text(candidate["digest"])
	}
	checkIDs := kernel.SemStrings(rc["check_ids"])
	applicable := false
	for _, id := range checkIDs {
		if kernel.SemHas([]string{"check.domain-strategy-approved", "check.stage-decision-package-approved", "gate.plan-approved"}, id) {
			applicable = true
		}
	}
	if !applicable {
		return nil
	}
	binding := kernel.SemMap(rc["plan_review_binding"])
	if binding == nil {
		return s.Reject("PLAN_REVIEW_ATTEMPT_UNREGISTERED", "Plan 专业任务未绑定登记尝试")
	}
	cpRef := kernel.Text(binding["checkpoint_ref"])
	if opts["checkpoint"] != "" && opts["checkpoint"] != cpRef {
		return s.Reject("PLAN_REVIEW_CHECKPOINT_DRIFT", "消费者 checkpoint 与任务声明不一致")
	}
	cp, err := s.Doc(cpRef)
	if err != nil {
		return err
	}
	c, _, err := approval.PlanControl(s, cp)
	if err != nil {
		return err
	}
	var a map[string]any
	for _, row := range kernel.SemList(c["attempts"]) {
		m := kernel.SemMap(row)
		if m["attempt_id"] == binding["attempt_id"] {
			a = m
		}
	}
	if a == nil || !kernel.ApEqual(approval.PlanBinding(c, a), binding) {
		return s.Reject("PLAN_REVIEW_ATTEMPT_UNREGISTERED", "任务不属于当前登记周期")
	}
	if kernel.Text(a["task_digest"]) == "" || !kernel.SemHas([]string{"reserved", "dispatched", "completed"}, kernel.Text(a["status"])) || opts["require-completed"] == "true" && a["status"] != "completed" {
		return s.Reject("PLAN_REVIEW_NOT_COMPLETED", "任务字节未绑定或审查尚未完成")
	}
	if !kernel.ApEqual(rc["check_ids"], a["check_ids"]) || candidateRef != a["candidate_ref"] || !kernel.ApEqual(planNormalizedBasis(rc["basis"]), a["basis"]) {
		return s.Reject("PLAN_REVIEW_TASK_DRIFT", "任务候选、范围或依据变化")
	}
	taskBinding, err := approval.PlanTaskAsset(s, a)
	if err != nil {
		return err
	}
	bytes, err := s.Bytes(kernel.Text(taskBinding["ref"]))
	if err != nil {
		return err
	}
	actual, err := s.Doc(kernel.Text(taskBinding["ref"]))
	if err != nil {
		return err
	}
	if safefs.Digest(bytes) != kernel.Text(taskBinding["digest"]) || !kernel.ApEqual(actual, task) {
		return s.Reject("PLAN_REVIEW_TASK_DRIFT", "登记任务实际字节变化")
	}
	subjectBytes, err := s.Bytes(candidateRef)
	if err != nil {
		return err
	}
	subject, err := s.Doc(candidateRef)
	if err != nil {
		return err
	}
	digest, err := kernel.SemanticDocumentDigest(subject["current_approvals"])
	if err != nil {
		return err
	}
	if safefs.Digest(subjectBytes) != strings.TrimPrefix(candidateDigest, "sha256:") || ((s.GovernanceVersion != 2 || subject["plan_review_binding"] != nil) && !kernel.ApEqual(subject["plan_review_binding"], binding)) || kernel.SemMap(subject["source_checkpoint"])["ref"] != binding["checkpoint_ref"] || kernel.SemMap(subject["source_checkpoint"])["digest"] != a["candidate_digest"] || digest != kernel.Text(a["candidate_digest"]) || (s.GovernanceVersion != 2 && !kernel.ApEqual(subject["current_approvals"], rc["current_approvals"])) {
		return s.Reject("PLAN_REVIEW_TASK_DRIFT", "审阅主体与登记候选摘要不一致")
	}
	all := kernel.SemList(subject["current_approvals"])
	rows := []any{}
	for _, row := range all {
		m := kernel.SemMap(row)
		if opts["boundary"] == "" || m["boundary"] == opts["boundary"] {
			rows = append(rows, row)
		}
	}
	if opts["boundary"] != "" && len(rows) != 1 {
		return s.Reject("PLAN_REVIEW_COVERAGE_REQUIRED", "任务未唯一覆盖消费检查")
	}
	basis := []any{}
	for _, item := range kernel.SemList(a["basis"]) {
		b := kernel.SemMap(item)
		inSelected, inAll := false, false
		for _, r := range rows {
			for _, p := range kernel.SemList(kernel.SemMap(r)["basis"]) {
				if kernel.SemMap(p)["ref"] == b["ref"] {
					inSelected = true
				}
			}
		}
		for _, r := range all {
			for _, p := range kernel.SemList(kernel.SemMap(r)["basis"]) {
				if kernel.SemMap(p)["ref"] == b["ref"] {
					inAll = true
				}
			}
		}
		if opts["boundary"] == "" || inSelected || !inAll {
			basis = append(basis, item)
		}
	}
	if err = s.Basis(basis); err != nil {
		return err
	}
	for _, row := range rows {
		r := kernel.SemMap(row)
		proof := append([]any{map[string]any{"ref": r["subject_ref"], "digest": r["subject_digest"]}}, kernel.SemList(r["basis"])...)
		if err = s.Basis(proof); err != nil {
			return err
		}
		bucket := "gates"
		if strings.HasPrefix(kernel.Text(r["boundary"]), "check.") {
			bucket = "checks"
		}
		current := kernel.SemMap(kernel.SemMap(cp[bucket])[kernel.Text(r["boundary"])])
		if current != nil && (current["subject_ref"] != r["subject_ref"] || current["subject_digest"] != nil && strings.TrimPrefix(kernel.Text(current["subject_digest"]), "sha256:") != kernel.Text(r["subject_digest"]) || !kernel.ApEqual(current["approval_scope"], r["approval_scope"]) || current["drafter_principal_ref"] != r["drafter_principal_ref"]) {
			return s.Reject("PLAN_REVIEW_CANDIDATE_DRIFT", "当前检查主体、范围或作者变化")
		}
	}
	kernel.SemanticCoverage(s, "plan_review_task", map[string]any{"task_ref": ref, "binding": binding, "members": rows, "read_only": true, "execution_allowed": false})
	return nil
}

func verifyPlanCheckpointSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	cp, err := s.Doc(ref)
	if err != nil {
		return err
	}
	if cp["repository_mode"] != "project-instance" {
		return nil
	}
	if cp["upstream_spec_baseline"] != nil && (kernel.SemMap(kernel.SemMap(cp["gates"])["gate.plan-approved"])["status"] != "approved" || kernel.SemMap(kernel.SemMap(cp["gates"])["gate.spec-baseline-approved"])["status"] != "approved") {
		return handoff.VerifyInheritedSpecCheckpoint(s, ref, cp)
	}
	if s.DraftPreparation(cp) {
		return nil
	}
	if cp["next_work_unit"] == "work-unit.spec-synthesis" || cp["stage"] == "stage.spec-architecture" || kernel.SemMap(cp["stage_trace"])["completed_work_unit"] == "work-unit.spec-synthesis" {
		return s.Verify("plan-spec-entry", ref, opts)
	}
	if cp["stage"] != "stage.plan" || !kernel.SemHas([]string{"resume", "orchestrate"}, kernel.Text(cp["mode"])) {
		return nil
	}
	prior := kernel.SemMap(kernel.SemMap(cp["gates"])["gate.plan-approved"])["status"] == "approved"
	for _, id := range []string{"check.domain-strategy-approved", "check.stage-decision-package-approved"} {
		r := kernel.SemMap(kernel.SemMap(cp["checks"])[id])
		if kernel.Text(r["approval_ref"]) != "" || kernel.SemHas([]string{"approved", "passed"}, kernel.Text(r["status"])) {
			prior = true
		}
	}
	if cp["plan_review_control"] == nil && !prior {
		return nil
	}
	c, p, err := approval.PlanControl(s, cp)
	if err != nil {
		return err
	}
	h, known := kernel.Integer(c["history_count"])
	max, _ := kernel.Integer(kernel.SemMap(p["limits"])["total"])
	if (!known || h+int64(len(kernel.SemList(c["attempts"]))) > max) && !kernel.SemHas([]string{"history-unknown", "diagnosis-required", "blocked"}, kernel.Text(c["status"])) {
		return s.Reject("PLAN_REVIEW_BUDGET_EXHAUSTED", "超限或未知历史只能诊断补证据")
	}
	for _, row := range kernel.SemList(c["attempts"]) {
		a := kernel.SemMap(row)
		if a["status"] != "completed" {
			if err = s.Verify("plan-review-task", kernel.Text(a["task_ref"]), map[string]string{"checkpoint": ref}); err != nil {
				return err
			}
		}
	}
	return nil
}

func planCheckPolicy(s *kernel.SemanticSession) (map[string]any, error) {
	p := kernel.SemMap(kernel.SemanticDefinition(s.Registry, "stages", "stage.plan")["spec_entry"])
	if p == nil || len(kernel.SemStrings(p["required_checks"])) == 0 {
		return nil, s.Unavailable("CAPABILITY", "缺少本地 Plan 入口策略")
	}
	for _, bucket := range []string{"gates", "checks"} {
		ids := []string{}
		for _, r := range kernel.SemList(s.Registry[bucket]) {
			m := kernel.SemMap(r)
			if m["stage"] == "stage.plan" {
				ids = append(ids, kernel.Text(m["id"]))
			}
		}
		key := "gate_impacts"
		if bucket == "checks" {
			key = "check_impacts"
		}
		declared := []string{}
		for id := range kernel.SemMap(p[key]) {
			declared = append(declared, id)
		}
		sort.Strings(ids)
		sort.Strings(declared)
		if !kernel.EqualStrings(ids, declared) {
			return nil, s.Unavailable("CAPABILITY", "Plan 影响规则覆盖不完整")
		}
	}
	return p, nil
}

func planSelectApproval(doc map[string]any, boundary string) map[string]any {
	if doc["kind"] != "review-bundle" {
		if doc["gate_id"] == boundary {
			return doc
		}
		return nil
	}
	for _, row := range kernel.SemList(doc["reviews"]) {
		r := kernel.SemMap(row)
		if r["gate_id"] == boundary {
			out := map[string]any{}
			for k, v := range doc {
				out[k] = v
			}
			for k, v := range r {
				out[k] = v
			}
			delete(out, "reviews")
			delete(out, "kind")
			return out
		}
	}
	return nil
}

func planHistoricalLinked(s *kernel.SemanticSession, c map[string]any, ref string) (bool, error) {
	if kernel.SemMap(c["provenance"])["kind"] != "adopted" {
		return false, nil
	}
	b, err := s.Bytes(ref)
	if err != nil {
		return false, err
	}
	digest := safefs.Digest(b)
	for _, row := range kernel.SemList(c["history_evidence"]) {
		m := kernel.SemMap(row)
		if m["ref"] == ref && strings.TrimPrefix(kernel.Text(m["digest"]), "sha256:") == digest {
			return true, nil
		}
		doc, err := s.Doc(kernel.Text(m["ref"]))
		if err != nil {
			return false, err
		}
		if doc["kind"] == "plan-review-history-classification" && doc["feature_id"] == c["feature_id"] {
			for _, entry := range kernel.SemList(doc["entries"]) {
				e := kernel.SemMap(entry)
				if e["kind"] == "professional" && e["ref"] == ref && strings.TrimPrefix(kernel.Text(e["digest"]), "sha256:") == digest {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func verifyPlanAggregateSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	cp, err := s.Doc(ref)
	if err != nil {
		return err
	}
	if kernel.SemMap(kernel.SemMap(cp["gates"])["gate.plan-approved"])["status"] != "approved" {
		return s.Reject("PLAN_GATE_BLOCKED", "Plan 聚合门禁未批准")
	}
	if opts["approval"] != "" && opts["approval"] != kernel.First(kernel.Text(cp["plan_approval_ref"]), kernel.Text(kernel.SemMap(kernel.SemMap(cp["gates"])["gate.plan-approved"])["approval_ref"])) {
		return s.Reject("APPROVAL_CURRENT_INVALID", "待验批准不是当前 Plan 聚合记录")
	}
	return verifyPlanEntrySemantic(s, ref, opts)
}

func planEvidence(s *kernel.SemanticSession, basis []any, refs any) error {
	rows := kernel.SemStrings(refs)
	if len(rows) == 0 {
		return s.Reject("PLAN_EVIDENCE_REQUIRED", "检查项缺少当前依据")
	}
	for _, ref := range rows {
		found := false
		for _, row := range basis {
			if kernel.SemMap(row)["ref"] == ref {
				found = true
			}
		}
		if !found {
			return s.Reject("PLAN_EVIDENCE_REQUIRED", "检查引用未绑定审阅包: "+ref)
		}
	}
	return nil
}

func verifyPlanEntrySemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	cp, err := s.Doc(ref)
	if err != nil {
		return err
	}
	if cp["upstream_spec_baseline"] != nil && kernel.SemMap(kernel.SemMap(cp["gates"])["gate.plan-approved"])["status"] != "approved" {
		return handoff.VerifyInheritedSpecCheckpoint(s, ref, cp)
	}
	reviewRef := kernel.Text(cp["plan_review_ref"])
	feature := kernel.Text(cp["feature_id"])
	if reviewRef == "" || feature == "" {
		return s.Reject("PLAN_CONTEXT_REQUIRED", "缺少 Plan 审阅包或功能身份")
	}
	review, err := s.Doc(reviewRef)
	if err != nil {
		return err
	}
	policy, err := planCheckPolicy(s)
	if err != nil {
		return err
	}
	version, ok := kernel.Integer(review["schema_version"])
	if !ok || version != 1 || review["kind"] != "plan-entry-review" || review["gate_id"] != "gate.plan-approved" || review["feature_id"] != feature {
		return s.Reject("PLAN_REVIEW_INVALID", "审阅包身份或范围不匹配")
	}
	if review["review_protocol"] != nil && !kernel.SemHas([]string{"bundled-plan-review-v1", approval.NativePlanProtocol}, kernel.Text(review["review_protocol"])) {
		return s.Unavailable("CAPABILITY", "未知 Plan 审查协议")
	}
	basis := kernel.SemList(review["basis"])
	if err = s.Basis(basis); err != nil {
		return err
	}
	for _, r := range []string{kernel.Text(review["plan_ref"]), "CONTEXT.md", ".template-spec/process/lifecycle-registry.yaml", kernel.Text(review["context_reconciliation_ref"])} {
		if err = planEvidence(s, basis, []any{r}); err != nil {
			return err
		}
	}
	checks := kernel.SemMap(review["checks"])
	ids := kernel.SemStrings(policy["required_checks"])
	if len(checks) != len(ids) {
		return s.Reject("PLAN_CHECK_REQUIRED", "检查项缺失或存在未知项")
	}
	for _, id := range ids {
		check := kernel.SemMap(checks[id])
		if check["status"] != "passed" {
			return s.Reject("PLAN_CHECK_BLOCKED", "Plan 检查未通过: "+id)
		}
		if err = planEvidence(s, basis, check["evidence_refs"]); err != nil {
			return err
		}
	}
	openItems, ok := review["open_items"].([]any)
	if !ok {
		return s.Reject("PLAN_OPEN_ITEMS", "必须显式列出未决项")
	}
	for _, row := range openItems {
		item := kernel.SemMap(row)
		_, criticalOK := item["critical"].(bool)
		_, blockerOK := item["runnable_blocker"].(bool)
		if kernel.Text(item["id"]) == "" || !criticalOK || !blockerOK {
			return s.Reject("PLAN_OPEN_ITEMS", "未决项分类不完整")
		}
		if item["status"] != "resolved" {
			if item["status"] != "deferred" || item["critical"] == true || item["runnable_blocker"] == true {
				return s.Reject("PLAN_OPEN_ITEMS", "关键问题或可执行阻塞未解决")
			}
			for _, field := range []string{"noncritical_reason", "owner", "resolution_point", "downstream_recipient"} {
				if strings.TrimSpace(kernel.Text(item[field])) == "" {
					return s.Reject("PLAN_OPEN_ITEMS", "延期项缺少 "+field)
				}
			}
		}
		if err = planEvidence(s, basis, item["evidence_refs"]); err != nil {
			return err
		}
	}
	c, p, err := approval.PlanControl(s, cp)
	if err != nil {
		return err
	}
	internal := kernel.SemMap(review["internal_checks"])
	applicable := []string{}
	bundles := map[string]map[string]any{}
	for id, impact := range kernel.SemMap(policy["check_impacts"]) {
		hit, ok := kernel.SemMap(review["impacts"])[kernel.Text(impact)].(bool)
		if !ok {
			return s.Reject("PLAN_IMPACT_REQUIRED", "未评估影响面: "+kernel.Text(impact))
		}
		check := kernel.SemMap(internal[id])
		if err = planEvidence(s, basis, check["evidence_refs"]); err != nil {
			return err
		}
		if !hit {
			if check["status"] != "not-applicable" || strings.TrimSpace(kernel.Text(check["reason"])) == "" {
				return s.Reject("PLAN_APPLICABILITY", "未命中检查须有原因与证据")
			}
			continue
		}
		if check["status"] != "approved" {
			return s.Reject("PLAN_CHECK_BLOCKED", "命中检查未批准: "+id)
		}
		if err = planEvidence(s, basis, []any{check["approval_ref"], check["subject_ref"]}); err != nil {
			return err
		}
		approvalRef := kernel.Text(check["approval_ref"])
		source, err := s.Doc(approvalRef)
		if err != nil {
			return err
		}
		record, err := approval.SelectApprovalRecordSemantic(s, source, id)
		if err != nil {
			return err
		}
		if record["subject_ref"] != check["subject_ref"] || !kernel.SemHas(kernel.SemStrings(check["approval_scope"]), feature) || !kernel.ApSetEqual(check["approval_scope"], record["approval_scope"]) {
			return s.Reject("PLAN_APPROVAL_CONTEXT", "会签主体或范围不匹配")
		}
		if source["kind"] == "review-bundle" {
			if _, err = approval.ReviewBundleRowsSemantic(s, source); err != nil {
				return err
			}
			v, _ := kernel.Integer(source["schema_version"])
			if (v != 2 && !(s.GovernanceVersion == 2 && v == 3)) || source["plan_review_binding"] == nil {
				linked, e := planHistoricalLinked(s, c, approvalRef)
				if e != nil {
					return e
				}
				if !linked {
					return s.Reject("PLAN_REVIEW_BINDING_REQUIRED", "新 Plan 审查须绑定当前周期")
				}
			} else {
				var declared map[string]any
				policy := kernel.SemMap(s.Roles["gate_policy"])
				if s.GovernanceVersion == 2 {
					policy = kernel.ApMap(s.GovernancePolicy["gate_policy"])
				}
				for _, row := range kernel.SemList(kernel.SemMap(policy["review_execution"])["review_bundles"]) {
					m := kernel.SemMap(row)
					if m["aggregate_gate"] == "gate.plan-approved" {
						declared = m
					}
				}
				binding := kernel.SemMap(source["plan_review_binding"])
				if declared == nil || source["bundle_id"] != declared["bundle_id"] || source["work_unit_id"] != declared["work_unit"] || binding["protocol_version"] != approval.NativePlanProtocol || binding["cycle_id"] != c["cycle_id"] || binding["scope_digest"] != c["scope_digest"] {
					return s.Reject("PLAN_REVIEW_BINDING_REQUIRED", "组合结论与本地策略或周期不一致")
				}
				reviewIDs := []string{}
				for _, row := range kernel.SemList(source["reviews"]) {
					reviewIDs = append(reviewIDs, kernel.Text(kernel.SemMap(row)["gate_id"]))
				}
				if !kernel.SameStringSet(reviewIDs, kernel.SemStrings(binding["check_ids"])) {
					return s.Reject("PLAN_REVIEW_COVERAGE_REQUIRED", "组合结论未覆盖实际检查范围")
				}
				a := approval.PlanLatest(c, id)
				if a == nil {
					return s.Reject("PLAN_REVIEW_RESULT_REQUIRED", "检查缺少登记尝试")
				}
				taskBinding, err := approval.PlanTaskAsset(s, a)
				if err != nil {
					return err
				}
				if a["attempt_id"] != binding["attempt_id"] || a["status"] != "completed" || taskBinding["ref"] != source["review_task_ref"] || taskBinding["digest"] != source["review_task_digest"] {
					return s.Reject("PLAN_REVIEW_RESULT_REQUIRED", "检查缺少登记且通过的当前结论")
				}
				passed := false
				for _, row := range kernel.SemList(a["check_results"]) {
					m := kernel.SemMap(row)
					if m["check_id"] == id && m["status"] == "passed" {
						passed = true
					}
				}
				if !passed {
					return s.Reject("PLAN_REVIEW_RESULT_REQUIRED", "检查未通过")
				}
				task, err := s.Doc(kernel.Text(source["review_task_ref"]))
				if err != nil {
					return err
				}
				if !kernel.ApEqual(kernel.SemMap(task["review_context"])["plan_review_binding"], binding) {
					return s.Reject("PLAN_REVIEW_TASK_DRIFT", "组合结论与任务绑定不一致")
				}
			}
			bundles[approvalRef] = source
		} else {
			linked, e := planHistoricalLinked(s, c, approvalRef)
			if e != nil {
				return e
			}
			if !linked {
				return s.Reject("PLAN_REVIEW_BUNDLE_REQUIRED", "专业结论缺少当前组合审查")
			}
		}
		expected, err := approval.PlanExpected(s, id, check, basis, c)
		if err != nil {
			return err
		}
		if err = approval.AssertCurrentApprovalSemantic(s, record, expected); err != nil {
			return err
		}
		applicable = append(applicable, id)
	}
	for _, row := range kernel.SemList(s.Registry["checks"]) {
		definition := kernel.SemMap(row)
		id := kernel.Text(definition["id"])
		if definition["stage"] == "stage.plan" && kernel.SemMap(internal[id])["status"] == "approved" {
			for _, dependency := range kernel.SemStrings(definition["requires_checks"]) {
				if kernel.SemMap(internal[dependency])["status"] != "approved" {
					return s.Reject("PLAN_DEPENDENCY", "内部检查依赖未批准: "+dependency)
				}
			}
		}
	}
	reconRef := kernel.Text(review["context_reconciliation_ref"])
	recon, err := s.Doc(reconRef)
	if err != nil {
		return err
	}
	if recon["status"] != "reconciled" || recon["repository_mode"] != "project-instance" {
		return s.Reject("CONTEXT", "Plan Context 尚未调和")
	}
	if err = s.Verify("context-reconciliation", reconRef, opts); err != nil {
		return err
	}
	if err = approval.PlanEntryControl(s, cp, c, p, ref, review, basis, applicable); err != nil {
		return err
	}
	if _, err = approval.AssertUserDecisionRequirementSemantic(s, map[string]any{"boundary": "gate.plan-approved", "subject_ref": reviewRef, "scope": []any{feature}, "user_decision_ref": cp["plan_user_decision_ref"], "continuation_ref": cp["plan_continuation_ref"]}); err != nil {
		return err
	}
	return approval.PlanAggregateRecord(s, cp, review, basis, bundles)
}
