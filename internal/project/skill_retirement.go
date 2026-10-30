package project

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

const retirementSource = ".template-spec/agents/yss-skill-registry.yaml#/retirement_contract/retired_ids"
const legacyRetirementSource = "scripts/lib/skill-supply-chain.mjs#OBSOLETE"
const retirementJavaScript = "scripts/lib/skill-supply-chain.mjs"

func readSkillRetirement(b *bundle.Bundle) (map[string]bool, bool, error) {
	root := map[string]any{}
	if file, ok := b.Files[skillRegistryRef]; ok {
		raw, err := file.Render(nil)
		if err != nil {
			return nil, true, domain.Wrap("SKILL", err)
		}
		value, err := schema.Parse(raw)
		if err != nil {
			return nil, true, domain.Wrap("SKILL", err)
		}
		var valid bool
		root, valid = value.(map[string]any)
		if !valid {
			return nil, true, domain.Fail("SKILL", "退役技能注册表必须为对象")
		}
	}
	value, present := root["retirement_contract"]
	if !present {
		if _, exists := b.Files[retirementJavaScript]; !exists {
			return nil, false, nil
		}
		ids, err := legacyRetiredSkillIDs(b)
		if err != nil {
			return nil, true, err
		}
		return checkedRetiredSkillIDs(b, root, ids)
	}
	fail := func(reason string) (map[string]bool, bool, error) {
		return nil, true, domain.Fail("SKILL", "retirement_contract: "+reason)
	}
	contract, ok := value.(map[string]any)
	if !ok {
		return fail("必须为对象")
	}
	version, ok := contract["schema_version"].(json.Number)
	if !ok || (version != "1" && version != "2") {
		return fail("不支持的 schema_version")
	}
	fields := []string{"schema_version", "stages", "deprecated_new_use", "cleanup_statuses", "hard_retirement_tombstone", "retired_id_source", "failure_codes"}
	if version == "2" {
		fields = append(fields, "retired_ids")
	}
	if len(contract) != len(fields) {
		return fail("未知或缺失字段")
	}
	for _, field := range fields {
		if _, ok := contract[field]; !ok {
			return fail("缺少 " + field)
		}
	}
	if !retirementStringSet(contract["stages"], []string{"deprecated", "retired"}) || !retirementStringSet(contract["cleanup_statuses"], []string{"migration-only", "remove-ready"}) {
		return fail("stages/cleanup_statuses 无效")
	}
	if contract["deprecated_new_use"] != "forbidden" || contract["hard_retirement_tombstone"] != ".template-spec/agents/skill-migrations.md" {
		return fail("退役使用或 tombstone 合同无效")
	}
	codes, ok := contract["failure_codes"].(map[string]any)
	if !ok || len(codes) != 2 || codes["deprecated"] != "skill-deprecated" || codes["retired"] != "skill-retired" {
		return fail("failure_codes 无效")
	}
	if version == "1" {
		if contract["retired_id_source"] != legacyRetirementSource {
			return fail("v1 来源无效")
		}
		ids, err := legacyRetiredSkillIDs(b)
		if err != nil {
			return nil, true, err
		}
		return checkedRetiredSkillIDs(b, root, ids)
	}
	if contract["retired_id_source"] != retirementSource {
		return fail("v2 来源无效")
	}
	values, ok := contract["retired_ids"].([]any)
	if !ok {
		return fail("retired_ids 必须为字符串数组")
	}
	ids := map[string]bool{}
	pattern := regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	for _, value := range values {
		id, ok := value.(string)
		if !ok || !pattern.MatchString(id) || ids[id] {
			return fail("非法或重复的 retired_ids")
		}
		ids[id] = true
	}
	return checkedRetiredSkillIDs(b, root, ids)
}

func checkedRetiredSkillIDs(b *bundle.Bundle, root map[string]any, ids map[string]bool) (map[string]bool, bool, error) {
	fail := func(reason string) (map[string]bool, bool, error) {
		return nil, true, domain.Fail("SKILL", "retirement_contract: "+reason)
	}
	for _, key := range []string{"skills", "platform_skills", "external_skills"} {
		if value, exists := root[key]; exists {
			entries, ok := value.([]any)
			if !ok {
				return fail(key + " 必须为数组")
			}
			for _, value := range entries {
				entry, ok := value.(map[string]any)
				if !ok {
					return fail(key + " 条目必须为对象")
				}
				id, _ := entry["id"].(string)
				if ids[id] && !(key == "external_skills" && entry["source"] == "cross-repo-reference") {
					return fail("退役 canonical skill: " + id)
				}
			}
		}
	}
	for id := range ids {
		if _, exists := b.SkillRequirements[id]; exists {
			return fail("退役技能仍有需求: " + id)
		}
		for ref := range b.Files {
			for _, prefix := range []string{".agents/skills/", ".claude/skills/", ".workbuddy/skills/", ".codex/skills/", ".cursor/skills/", ".pi/skills/"} {
				if strings.HasPrefix(ref, prefix+id+"/") {
					return fail("退役物理技能: " + id)
				}
			}
		}
	}
	if file, exists := b.Files["skills-lock.json"]; exists {
		raw, err := file.Render(nil)
		if err != nil {
			return nil, true, domain.Wrap("SKILL", err)
		}
		var lock struct {
			Skills struct {
				Shared   map[string]json.RawMessage
				Platform map[string]map[string]json.RawMessage
			}
		}
		if err = json.Unmarshal(raw, &lock); err != nil {
			return nil, true, domain.Wrap("SKILL", err)
		}
		for id := range lock.Skills.Shared {
			if ids[id] {
				return fail("退役 lock skill: " + id)
			}
		}
		for _, group := range lock.Skills.Platform {
			for id := range group {
				if ids[id] {
					return fail("退役 platform lock skill: " + id)
				}
			}
		}
	}
	return ids, true, nil
}

func retirementStringSet(value any, want []string) bool {
	values, ok := value.([]any)
	if !ok || len(values) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		s, ok := value.(string)
		if !ok || seen[s] {
			return false
		}
		seen[s] = true
	}
	for _, s := range want {
		if !seen[s] {
			return false
		}
	}
	return true
}

func retiredSkillIDs(b *bundle.Bundle) (map[string]bool, error) {
	ids, present, err := readSkillRetirement(b)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, domain.Fail("SKILL", "固定来源缺少退役技能合同")
	}
	return ids, nil
}
