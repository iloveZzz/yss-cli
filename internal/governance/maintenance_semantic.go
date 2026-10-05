package governance

import (
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

func init() { registerSemanticValidator("maintenance-checkpoint", verifyMaintenanceSemantic) }

func verifyMaintenanceSemantic(s *semanticSession, ref string, opts map[string]string) error {
	doc, err := s.doc(ref)
	if err != nil {
		return err
	}
	policy, err := s.doc(".template-source/process/maintenance-intensity.yaml")
	if err != nil {
		return err
	}
	levels := []string{"L1", "L2", "L3"}
	version, ok := integer(doc["schema_version"])
	if !ok || version < 1 || version > 2 {
		return s.unavailable("CAPABILITY", "未知维护 checkpoint 版本")
	}
	fields := []string{"schema_version", "intensity", "classification_reason", "triggers", "changed_assets", "verification_evidence", "review_mode", "escalation"}
	if version == 2 {
		fields = append(fields, "target_state", "current_state", "verification_profile", "review_round", "candidate_digest")
	}
	for key := range doc {
		if !semHas(fields, key) {
			return s.reject("MAINTENANCE", "维护 checkpoint 未知字段: "+key)
		}
	}
	for _, key := range []string{"classification_reason", "escalation"} {
		if strings.TrimSpace(text(doc[key])) == "" {
			return s.reject("MAINTENANCE", "维护 checkpoint 缺少 "+key)
		}
	}
	if _, ok := doc["changed_assets"].([]any); !ok || len(semList(doc["changed_assets"])) == 0 {
		return s.reject("MAINTENANCE", "维护影响资产不能为空")
	}
	for _, row := range semList(doc["changed_assets"]) {
		if strings.TrimSpace(text(row)) == "" {
			return s.reject("MAINTENANCE", "维护影响资产引用非法")
		}
	}
	if _, ok := doc["triggers"].([]any); !ok {
		return s.reject("MAINTENANCE", "维护 triggers 必须为数组")
	}
	if _, ok := doc["verification_evidence"].([]any); !ok {
		return s.reject("MAINTENANCE", "维护证据必须为数组")
	}
	rank := func(v string) int {
		for i, x := range levels {
			if v == x {
				return i
			}
		}
		return -1
	}
	pv, valid := integer(policy["schema_version"])
	if !valid || pv != 1 || rank(text(policy["default_level"])) < 0 {
		return s.unavailable("CAPABILITY", "本地维护强度策略无效")
	}
	triggers := map[string]int{}
	for i, level := range levels {
		rows := semList(semMap(semMap(policy["levels"])[level])["triggers"])
		if len(rows) == 0 {
			return s.unavailable("CAPABILITY", "维护策略缺少 "+level+" triggers")
		}
		for _, row := range rows {
			t := text(row)
			if t == "" {
				return s.unavailable("CAPABILITY", "维护触发规则为空")
			}
			if _, dup := triggers[t]; dup {
				return s.unavailable("CAPABILITY", "维护触发规则重复")
			}
			triggers[t] = i
		}
	}
	for _, t := range semStrings(policy["counterexample_triggers"]) {
		if _, known := triggers[t]; !known {
			return s.unavailable("CAPABILITY", "未知维护反例触发规则")
		}
	}
	minimum := 0
	if len(semList(doc["triggers"])) == 0 {
		minimum = rank(text(policy["default_level"]))
	}
	for _, t := range semStrings(doc["triggers"]) {
		r, known := triggers[t]
		if !known {
			return s.reject("MAINTENANCE", "未知维护 trigger: "+t)
		}
		if r > minimum {
			minimum = r
		}
	}
	level := text(doc["intensity"])
	if rank(level) < minimum {
		return s.reject("MAINTENANCE", "维护强度低于当前策略要求")
	}
	modes := map[string][]string{"L1": {"self-check", "human-checkpoint"}, "L2": {"self-check", "human-checkpoint", "focused-independent"}, "L3": {"self-check", "human-checkpoint", "focused-independent", "formal-independent"}}
	mode := text(doc["review_mode"])
	if !semHas(modes[level], mode) {
		return s.reject("MAINTENANCE", "维护审查模式不适用于该强度")
	}
	kinds := map[string]bool{}
	for _, row := range semList(doc["verification_evidence"]) {
		e := semMap(row)
		kind := text(e["kind"])
		if kind == "" || strings.TrimSpace(text(e["command"])) == "" || e["result"] != "pass" {
			return s.reject("MAINTENANCE_EVIDENCE", "维护证据须绑定本轮实际通过命令")
		}
		if strings.HasPrefix(text(e["evidence_ref"]), "maintenance:") {
			if err = s.basis([]any{map[string]any{"ref": e["evidence_ref"], "digest": e["evidence_digest"]}}); err != nil {
				return err
			}
		}
		if semHas([]string{"focused-independent-review", "formal-independent-review"}, kind) {
			if err = s.maintenanceReview(e); err != nil {
				return err
			}
		}
		kinds[kind] = true
	}
	reviewKind := ""
	if mode == "formal-independent" {
		reviewKind = "formal-independent-review"
	} else if mode == "focused-independent" {
		reviewKind = "focused-independent-review"
	}
	if version == 2 {
		if err = maintenanceStateSemantic(s, doc, kinds, reviewKind); err != nil {
			return err
		}
	}
	required := map[string][]string{"L1": {"relevant-check"}, "L2": {"counterexample", "fresh-verification", "self-check"}, "L3": {"fresh-verification", "self-check"}}[level]
	if mode == "formal-independent" {
		required = []string{"red", "green", "refactor", "pressure-scenario", "fresh-verification", reviewKind}
	} else if reviewKind != "" {
		out := []string{}
		for _, k := range required {
			if k != "self-check" {
				out = append(out, k)
			}
		}
		required = append(out, reviewKind)
	}
	for _, k := range required {
		pending := k == reviewKind && (opts["allow-pending-review"] == "true" || version == 2 && doc["current_state"] != "release-ready")
		if !pending && !kinds[k] {
			return s.reject("MAINTENANCE_EVIDENCE", "维护证据缺少 "+k)
		}
	}
	if opts["history"] != "true" {
		for _, trigger := range semStrings(doc["triggers"]) {
			if !semHas(policy["counterexample_triggers"], trigger) {
				continue
			}
			found := false
			for _, row := range semList(doc["verification_evidence"]) {
				e := semMap(row)
				if e["kind"] == "counterexample" && e["trigger"] == trigger {
					found = true
					if err = s.maintenanceCounterexample(e, trigger); err != nil {
						return err
					}
				}
			}
			if !found {
				return s.reject("MAINTENANCE_COUNTEREXAMPLE", "触发规则缺少定向反例: "+trigger)
			}
		}
	}
	s.report.Applicability = append(s.report.Applicability, map[string]any{"id": "maintenance-intensity", "intensity": level, "minimum_intensity": levels[minimum], "historical_only": opts["history"] == "true"})
	return nil
}

func maintenanceStateSemantic(s *semanticSession, doc map[string]any, kinds map[string]bool, reviewKind string) error {
	states := []string{"implementation-ready", "review-ready", "release-ready"}
	target, current := text(doc["target_state"]), text(doc["current_state"])
	profile := text(doc["verification_profile"])
	round, ok := integer(doc["review_round"])
	digest, digestPresent := doc["candidate_digest"]
	if !digestPresent || !semHas(states, target) || !semHas(append(append([]string{}, states...), "needs-human"), current) || !semHas([]string{"fast", "candidate", "release"}, profile) || !ok || round < 0 || round > 2 || digest != nil && !isSHA256(strings.TrimPrefix(text(digest), "sha256:")) {
		return s.reject("MAINTENANCE_STATE", "维护状态、轮次或候选摘要无效")
	}
	rank := func(v string) int {
		for i, x := range states {
			if v == x {
				return i
			}
		}
		return -1
	}
	if current != "needs-human" && rank(current) > rank(target) {
		return s.reject("MAINTENANCE_STATE", "当前状态越过目标状态")
	}
	if current == "implementation-ready" {
		if profile != "fast" || round != 0 || digest != nil {
			return s.reject("MAINTENANCE_STATE", "实现状态不能冻结审查候选")
		}
		return nil
	}
	self := semHas([]string{"self-check", "human-checkpoint"}, text(doc["review_mode"]))
	releaseCommand := func(kind string) error {
		count := 0
		for _, row := range semList(doc["verification_evidence"]) {
			e := semMap(row)
			if e["kind"] == kind {
				count++
				if e["command"] != "scripts/verify-template" {
					return s.reject("MAINTENANCE_STATE", "完整门禁命令必须为 scripts/verify-template")
				}
			}
		}
		if count != 1 {
			return s.reject("MAINTENANCE_STATE", "完整门禁须恰好一条 "+kind)
		}
		return nil
	}
	if self {
		if round != 0 || digest != nil {
			return s.reject("MAINTENANCE_STATE", "自检不得冻结候选或设置审查轮次")
		}
	} else {
		if digest == nil || round < 1 {
			return s.reject("MAINTENANCE_STATE", "审查状态须绑定当前候选与轮次")
		}
		for _, k := range []string{"candidate-verification", "initial-release-verification", "review-task-packages"} {
			if !kinds[k] {
				return s.reject("MAINTENANCE_STATE", "审查准备缺少 "+k)
			}
		}
		if err := releaseCommand("initial-release-verification"); err != nil {
			return err
		}
	}
	if current == "review-ready" {
		expected := "candidate"
		if self {
			expected = "fast"
		}
		if profile != expected {
			return s.reject("MAINTENANCE_STATE", "审查状态与验证 profile 不符")
		}
		return nil
	}
	if current == "needs-human" {
		if target != "release-ready" || round != 2 || !semHas([]string{"candidate", "release"}, profile) {
			return s.reject("MAINTENANCE_STATE", "needs-human 须来自第二轮发布目标")
		}
		return nil
	}
	if profile != "release" || !kinds["final-release-verification"] || reviewKind != "" && !kinds[reviewKind] {
		return s.reject("MAINTENANCE_STATE", "发布状态缺少完整验证与当前独立审查")
	}
	return releaseCommand("final-release-verification")
}

func (s *semanticSession) maintenanceCounterexample(evidence map[string]any, trigger string) error {
	run, err := s.doc(text(evidence["run_ref"]))
	if err != nil {
		return err
	}
	n, ok := integer(run["schema_version"])
	exit, exitOK := integer(run["exit_code"])
	if !ok || n != 1 || run["kind"] != "maintenance-counterexample-run" || run["trigger"] != trigger || run["command"] != evidence["command"] || !exitOK || exit != 0 {
		return s.reject("MAINTENANCE_COUNTEREXAMPLE", "反例运行身份、命令或退出码无效")
	}
	start, a := time.Parse(time.RFC3339Nano, text(run["started_at"]))
	end, b := time.Parse(time.RFC3339Nano, text(run["finished_at"]))
	if a != nil || b != nil || end.Before(start) {
		return s.reject("MAINTENANCE_COUNTEREXAMPLE", "反例运行时间无效")
	}
	logBinding := semMap(run["log"])
	if err = s.basis([]any{logBinding}); err != nil {
		return err
	}
	bytes, err := s.bytes(text(logBinding["ref"]))
	if err != nil {
		return err
	}
	value, err := schema.Parse(bytes)
	if err != nil {
		return s.reject("MAINTENANCE_COUNTEREXAMPLE", "运行日志无法解析")
	}
	log, valid := value.([]any)
	if !valid {
		return s.reject("MAINTENANCE_COUNTEREXAMPLE", "运行日志必须为数组")
	}
	if len(semList(run["assertions"])) == 0 {
		return s.reject("MAINTENANCE_COUNTEREXAMPLE", "反例缺少拒绝断言")
	}
	for _, row := range semList(run["assertions"]) {
		assertion := semMap(row)
		actual := apFind(log, "id", text(assertion["id"]))
		x, ok := integer(actual["exit_code"])
		output := text(actual["stderr"]) + "\n" + text(actual["stdout"])
		diagnostic := text(assertion["expected_diagnostic"])
		if text(assertion["id"]) == "" || assertion["expected"] != "reject" || assertion["actual"] != "rejected" || len(semList(actual["command"])) == 0 || !ok || x == 0 || strings.TrimSpace(output) == "" || strings.TrimSpace(diagnostic) == "" || !strings.Contains(output, diagnostic) {
			return s.reject("MAINTENANCE_COUNTEREXAMPLE", "断言缺少实际拒绝结果或预期原因")
		}
	}
	if err = s.basis(run["inputs"]); err != nil {
		return err
	}
	// This contract hashes JSON.stringify of the ordered input rows, rather
	// than the sorted structural digest used by approval documents.
	raw, err := s.bytes(text(evidence["run_ref"]))
	if err != nil {
		return err
	}
	encoded, err := orderedAssetJSON(raw, "inputs")
	if err != nil {
		return err
	}
	if text(run["input_digest"]) != "sha256:"+safefs.Digest(encoded) {
		return s.reject("MAINTENANCE_COUNTEREXAMPLE", "反例输入范围摘要不一致")
	}
	return nil
}
