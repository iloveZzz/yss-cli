package kernel

import "sort"

// CompileTaskSelection consumes work-unit routing and this Profile's check groups.
// The caller validates impacts against its current, digest-bound input facts.
func CompileTaskSelection(s *SemanticSession, unit string, impacts []string) (map[string]any, error) {
	if s.GovernanceVersion != 2 || ApFind(s.Registry["work_units"], "id", unit) == nil || !ApUnique(impacts) {
		return nil, s.Reject("TASK_SELECTION", "工作单元、政策或影响未确定")
	}
	route := ApMap(ApMap(s.GovernancePolicy["work_unit_routes"])[unit])
	mapping := ApMap(route["impact_check_ids"])
	if !ApUnique(route["check_ids"]) || len(mapping) == 0 || Text(route["primary_skill"]) == "" {
		return nil, s.Reject("TASK_SELECTION", "工作单元缺少完整的检查与Skill映射")
	}
	// Existing impact vocabulary shared by lifecycle impact routing and Slice source scope.
	knownImpacts := []string{"wording", "ui-state", "api-schema", "state-machine", "data-model", "service-boundary", "nfr", "backend", "domain", "api", "data", "persistence", "architecture", "cross-repo", "cross-boundary", "high-risk", "frontend", "ui"}
	for impact := range mapping {
		if !SemHas(knownImpacts, impact) {
			return nil, s.Reject("TASK_SELECTION", "未知影响映射: "+impact)
		}
	}
	checks := map[string]bool{}
	skills := map[string]bool{Text(route["primary_skill"]): true}
	for _, skill := range ApStrings(route["supporting_skills"]) {
		skills[skill] = true
	}
	for _, id := range ApStrings(route["check_ids"]) {
		checks[id] = true
	}
	for _, impact := range impacts {
		rows, ok := mapping[impact]
		if !ok || !ApIsArray(rows) {
			return nil, s.Reject("TASK_SELECTION", "未知或缺少影响映射: "+impact)
		}
		for _, id := range ApStrings(rows) {
			checks[id] = true
		}
	}
	policy := s.GatePolicy()
	// Validate even unselected entries so an unknown gate cannot hide in a dormant branch.
	known := map[string]bool{}
	groups := map[string][]string{}
	for _, bucket := range []string{"automatic_checks", "check_reviews", "technical_reviews", "user_decision_gates", "contract_approval_gates", "evidence_only", "work_unit_reviews"} {
		for _, entry := range ApArray(policy[bucket]) {
			id := Text(entry)
			if row, ok := Object(entry); ok {
				id = Text(row["gate"])
				if bucket == "work_unit_reviews" {
					id = Text(row["work_unit"])
				}
				groups[id] = ApStrings(row["capability_ids"])
			}
			known[id] = true
		}
	}
	for _, rows := range append([]any{route["check_ids"]}, mapValues(mapping)...) {
		if !ApIsArray(rows) || len(ApStrings(rows)) != len(ApArray(rows)) {
			return nil, s.Reject("TASK_SELECTION", "检查映射必须是数组")
		}
		seen := map[string]bool{}
		for _, id := range ApStrings(rows) {
			if !known[id] || seen[id] {
				return nil, s.Reject("TASK_SELECTION", "检查不属于当前Profile或重复: "+id)
			}
			seen[id] = true
		}
	}
	for id := range checks {
		for _, group := range groups[id] {
			definition := ApFind(s.GovernancePolicy["technical_checks"], "id", group)
			if definition == nil || !ApContains(definition["profiles"], s.Report.Profile) {
				return nil, s.Reject("TASK_SELECTION", "检查组不适用于当前Profile")
			}
			selected := definition["review_skills"]
			if override := ApMap(definition["review_skills_by_profile"])[s.Report.Profile]; override != nil {
				selected = override
			}
			for _, skill := range ApStrings(selected) {
				skills[skill] = true
			}
		}
	}
	checkIDs, skillIDs := []string{}, []string{}
	for id := range checks {
		checkIDs = append(checkIDs, id)
	}
	for id := range skills {
		skillIDs = append(skillIDs, id)
	}
	sort.Strings(checkIDs)
	sort.Strings(skillIDs)
	return map[string]any{"policy_ref": ".template-spec/process/orchestration-contract.yaml", "policy_digest": s.GovernanceDigest, "work_unit_id": unit, "impacts": impacts, "skills": skillIDs, "check_ids": checkIDs}, nil
}

func mapValues(m map[string]any) []any {
	out := make([]any, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func validateRoleIndependentSkillSource(s *SemanticSession, task map[string]any) error {
	source := ApMap(task["skill_source"])
	expected, err := CompileTaskSelection(s, Text(task["work_unit_id"]), ApStrings(source["impacts"]))
	if err != nil {
		return err
	}
	for _, key := range []string{"policy_ref", "policy_digest", "work_unit_id"} {
		if source[key] != expected[key] {
			return s.Reject("TASK_SELECTION", "技能来源未绑定当前政策/工作单元: "+key)
		}
	}
	for _, key := range []string{"skills", "check_ids"} {
		if !ApUnique(source[key]) || !ApSetEqual(source[key], expected[key]) {
			return s.Reject("TASK_SELECTION", "任务遗漏或替换当前必需项: "+key)
		}
	}
	return nil
}
