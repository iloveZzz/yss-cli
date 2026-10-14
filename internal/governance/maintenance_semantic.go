package governance

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"strings"
)

func init() { registerSemanticValidator("maintenance-checkpoint", verifyMaintenanceSemantic) }

func verifyMaintenanceSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	doc, err := s.Doc(ref)
	if err != nil {
		return err
	}
	history := opts["history"] == "true"
	policyRef, policyVersion := ".template-source/process/maintenance-intensity.yaml", int64(2)
	levels := []string{"L1", "L2"}
	if history {
		policyRef, policyVersion = ".template-source/process/maintenance-intensity-v1.yaml", 1
		levels = append(levels, "L3")
	}
	policy, err := s.Doc(policyRef)
	if err != nil {
		return err
	}
	version, ok := kernel.Integer(doc["schema_version"])
	if !ok || version < 1 || version > 2 {
		return s.Unavailable("CAPABILITY", "未知维护 checkpoint 版本")
	}
	fields := []string{"schema_version", "intensity", "classification_reason", "triggers", "changed_assets", "verification_evidence", "review_mode", "escalation"}
	if version == 2 {
		fields = append(fields, "target_state", "current_state", "verification_profile", "review_round", "candidate_digest")
	}
	for key := range doc {
		if !kernel.SemHas(fields, key) {
			return s.Reject("MAINTENANCE", "维护 checkpoint 未知字段: "+key)
		}
	}
	for _, key := range []string{"classification_reason", "escalation"} {
		if strings.TrimSpace(kernel.Text(doc[key])) == "" {
			return s.Reject("MAINTENANCE", "维护 checkpoint 缺少 "+key)
		}
	}
	if _, ok := doc["changed_assets"].([]any); !ok || len(kernel.SemList(doc["changed_assets"])) == 0 {
		return s.Reject("MAINTENANCE", "维护影响资产不能为空")
	}
	for _, row := range kernel.SemList(doc["changed_assets"]) {
		if strings.TrimSpace(kernel.Text(row)) == "" {
			return s.Reject("MAINTENANCE", "维护影响资产引用非法")
		}
	}
	if _, ok := doc["triggers"].([]any); !ok {
		return s.Reject("MAINTENANCE", "维护 triggers 必须为数组")
	}
	if _, ok := doc["verification_evidence"].([]any); !ok {
		return s.Reject("MAINTENANCE", "维护证据必须为数组")
	}
	rank := func(v string) int {
		for i, x := range levels {
			if v == x {
				return i
			}
		}
		return -1
	}
	pv, valid := kernel.Integer(policy["schema_version"])
	policyLevels := kernel.SemMap(policy["levels"])
	if !valid || pv != policyVersion || rank(kernel.Text(policy["default_level"])) < 0 || len(policyLevels) != len(levels) {
		return s.Unavailable("CAPABILITY", "本地维护强度策略无效")
	}
	for level := range policyLevels {
		if rank(level) < 0 {
			return s.Unavailable("CAPABILITY", "未知维护策略等级: "+level)
		}
	}
	triggers := map[string]int{}
	for i, level := range levels {
		rows := kernel.SemList(kernel.SemMap(kernel.SemMap(policy["levels"])[level])["triggers"])
		if len(rows) == 0 {
			return s.Unavailable("CAPABILITY", "维护策略缺少 "+level+" triggers")
		}
		for _, row := range rows {
			t := kernel.Text(row)
			if t == "" {
				return s.Unavailable("CAPABILITY", "维护触发规则为空")
			}
			if _, dup := triggers[t]; dup {
				return s.Unavailable("CAPABILITY", "维护触发规则重复")
			}
			triggers[t] = i
		}
	}
	for _, t := range kernel.SemStrings(policy["counterexample_triggers"]) {
		if _, known := triggers[t]; !known {
			return s.Unavailable("CAPABILITY", "未知维护反例触发规则")
		}
	}
	minimum := 0
	if len(kernel.SemList(doc["triggers"])) == 0 {
		minimum = rank(kernel.Text(policy["default_level"]))
	}
	for _, t := range kernel.SemStrings(doc["triggers"]) {
		r, known := triggers[t]
		if !known {
			return s.Reject("MAINTENANCE", "未知维护 trigger: "+t)
		}
		if r > minimum {
			minimum = r
		}
	}
	level := kernel.Text(doc["intensity"])
	if rank(level) < 0 {
		return s.Reject("MAINTENANCE", "当前 intensity 必须是 L1 或 L2；L3 仅供 --history 历史读取")
	}
	if rank(level) < minimum {
		return s.Reject("MAINTENANCE", "维护强度低于当前策略要求")
	}
	modes := map[string][]string{"L1": {"self-check", "human-checkpoint"}, "L2": {"self-check", "human-checkpoint", "focused-independent", "formal-independent"}}
	if history {
		modes["L3"] = modes["L2"]
		modes["L2"] = []string{"self-check", "human-checkpoint", "focused-independent"}
	}
	mode := kernel.Text(doc["review_mode"])
	if !kernel.SemHas(modes[level], mode) {
		return s.Reject("MAINTENANCE", "维护审查模式不适用于该强度")
	}
	kinds := map[string]bool{}
	for _, row := range kernel.SemList(doc["verification_evidence"]) {
		e := kernel.SemMap(row)
		kind := kernel.Text(e["kind"])
		if kind == "" || strings.TrimSpace(kernel.Text(e["command"])) == "" || e["result"] != "pass" {
			return s.Reject("MAINTENANCE_EVIDENCE", "维护证据须绑定本轮实际通过命令")
		}
		if strings.HasPrefix(kernel.Text(e["evidence_ref"]), "maintenance:") {
			if err = s.Basis([]any{map[string]any{"ref": e["evidence_ref"], "digest": e["evidence_digest"]}}); err != nil {
				return err
			}
		}
		if kernel.SemHas([]string{"focused-independent-review", "formal-independent-review"}, kind) {
			if err = s.MaintenanceReview(e); err != nil {
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
		if err = maintenanceStateSemantic(s, doc, kinds, reviewKind, history); err != nil {
			return err
		}
	}
	required := []string{"fresh-verification", "self-check"}
	if level == "L1" {
		required = []string{"relevant-check"}
	} else if history && level == "L2" {
		required = append([]string{"counterexample"}, required...)
	}
	if history && level == "L3" && mode == "formal-independent" {
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
			return s.Reject("MAINTENANCE_EVIDENCE", "维护证据缺少 "+k)
		}
	}
	if !history {
		for _, trigger := range kernel.SemStrings(doc["triggers"]) {
			if !kernel.SemHas(policy["counterexample_triggers"], trigger) {
				continue
			}
			found := false
			for _, row := range kernel.SemList(doc["verification_evidence"]) {
				e := kernel.SemMap(row)
				_, hasRun := e["run_ref"]
				if e["kind"] == "counterexample" && e["trigger"] == trigger && hasRun {
					found = true
				}
			}
			if !found {
				return s.Reject("MAINTENANCE_COUNTEREXAMPLE", "触发规则缺少定向反例: "+trigger)
			}
		}
		for _, row := range kernel.SemList(doc["verification_evidence"]) {
			e := kernel.SemMap(row)
			_, hasRun := e["run_ref"]
			if e["kind"] != "counterexample" || !hasRun {
				continue
			}
			trigger := kernel.Text(e["trigger"])
			if !kernel.SemHas(doc["triggers"], trigger) {
				return s.Reject("MAINTENANCE_COUNTEREXAMPLE", "自愿反例证据的 trigger 必须属于当前维护范围")
			}
			if err = s.MaintenanceCounterexample(e, trigger); err != nil {
				return err
			}
		}
	}
	s.Report.Applicability = append(s.Report.Applicability, map[string]any{"id": "maintenance-intensity", "intensity": level, "minimum_intensity": levels[minimum], "historical_only": opts["history"] == "true"})
	return nil
}

func maintenanceStateSemantic(s *kernel.SemanticSession, doc map[string]any, kinds map[string]bool, reviewKind string, history bool) error {
	states := []string{"implementation-ready", "review-ready", "release-ready"}
	target, current := kernel.Text(doc["target_state"]), kernel.Text(doc["current_state"])
	profile := kernel.Text(doc["verification_profile"])
	round, ok := kernel.Integer(doc["review_round"])
	digest, digestPresent := doc["candidate_digest"]
	profiles := []string{"fast", "release"}
	if history {
		profiles = append(profiles, "candidate")
	}
	if !digestPresent || !kernel.SemHas(states, target) || !kernel.SemHas(append(append([]string{}, states...), "needs-human"), current) || !kernel.SemHas(profiles, profile) || !ok || round < 0 || round > 2 || digest != nil && !kernel.IsSHA256(strings.TrimPrefix(kernel.Text(digest), "sha256:")) {
		return s.Reject("MAINTENANCE_STATE", "维护状态、轮次或候选摘要无效")
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
		return s.Reject("MAINTENANCE_STATE", "当前状态越过目标状态")
	}
	if current == "implementation-ready" {
		if profile != "fast" || round != 0 || digest != nil {
			return s.Reject("MAINTENANCE_STATE", "实现状态不能冻结审查候选")
		}
		return nil
	}
	self := kernel.SemHas([]string{"self-check", "human-checkpoint"}, kernel.Text(doc["review_mode"]))
	releaseCommand := func(kind string) error {
		count := 0
		for _, row := range kernel.SemList(doc["verification_evidence"]) {
			e := kernel.SemMap(row)
			if e["kind"] == kind {
				count++
				if e["command"] != "scripts/verify-template" {
					return s.Reject("MAINTENANCE_STATE", "完整门禁命令必须为 scripts/verify-template")
				}
			}
		}
		if count != 1 {
			return s.Reject("MAINTENANCE_STATE", "完整门禁须恰好一条 "+kind)
		}
		return nil
	}
	if self {
		if round != 0 || digest != nil {
			return s.Reject("MAINTENANCE_STATE", "自检不得冻结候选或设置审查轮次")
		}
	} else {
		if digest == nil || round < 1 {
			return s.Reject("MAINTENANCE_STATE", "审查状态须绑定当前候选与轮次")
		}
		for _, k := range []string{"candidate-verification", "initial-release-verification", "review-task-packages"} {
			if !kinds[k] {
				return s.Reject("MAINTENANCE_STATE", "审查准备缺少 "+k)
			}
		}
		if err := releaseCommand("initial-release-verification"); err != nil {
			return err
		}
	}
	if current == "review-ready" {
		expected := "release"
		if self {
			expected = "fast"
		} else if history {
			expected = "candidate"
		}
		if profile != expected {
			return s.Reject("MAINTENANCE_STATE", "审查状态与验证 profile 不符")
		}
		return nil
	}
	if current == "needs-human" {
		if target != "release-ready" || round != 2 || !(profile == "release" || history && profile == "candidate") {
			return s.Reject("MAINTENANCE_STATE", "needs-human 须来自第二轮发布目标")
		}
		return nil
	}
	if profile != "release" || !kinds["final-release-verification"] || reviewKind != "" && !kinds[reviewKind] {
		return s.Reject("MAINTENANCE_STATE", "发布状态缺少完整验证与当前独立审查")
	}
	return releaseCommand("final-release-verification")
}
