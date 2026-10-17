package bundle

import (
	"encoding/base64"
	"sort"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

const skillRegistryRef = ".template-spec/agents/yss-skill-registry.yaml"

// Stacks 是 init/attach --full 可选的技术栈；none 保持完整分发。
var Stacks = []string{"java", "vue", "none"}

// StackExclusions 返回选择某个技术栈时不分发的技能：java 排除 stack: vue 的技能，vue 排除
// stack: java 的技能，未声明 stack 的技能为通用技能，始终保留。分组只取 Bundle 内注册表的
// 显式 stack 名单，不按 impacts 推断。保留技能对被排除技能的 context-required 依赖，以及未用
// stacks 限定到另一栈的 context-conditional 依赖，说明名单与依赖矛盾，直接拒绝而不自行放回。
func StackExclusions(b *Bundle, stack string) ([]string, error) {
	if stack == "" || stack == "none" {
		return nil, nil
	}
	if stack != "java" && stack != "vue" {
		return nil, domain.Fail("ARGUMENT", "--stack 只支持 java、vue 或 none")
	}
	f, ok := b.Files[skillRegistryRef]
	if !ok {
		return nil, domain.Fail("BUNDLE", "Bundle 缺少技能注册表，不能按技术栈裁剪")
	}
	raw, err := base64.StdEncoding.DecodeString(f.Data)
	if err != nil {
		return nil, err
	}
	doc, err := schema.Parse(raw)
	if err != nil {
		return nil, domain.Fail("BUNDLE", "技能注册表无法解析: "+err.Error())
	}
	registry, _ := doc.(map[string]any)
	skills, _ := registry["skills"].([]any)
	if len(skills) == 0 {
		return nil, domain.Fail("BUNDLE", "技能注册表没有技能")
	}
	other := "vue"
	if stack == "vue" {
		other = "java"
	}
	excluded := map[string]bool{}
	declared := false
	for _, item := range skills {
		s, _ := item.(map[string]any)
		id, _ := s["id"].(string)
		value, has := s["stack"]
		if !has {
			continue
		}
		declared = true
		name, _ := value.(string)
		if name != "java" && name != "vue" {
			return nil, domain.Fail("BUNDLE", "技能注册表 stack 只允许 java 或 vue: "+id)
		}
		if id != "" && name == other {
			excluded[id] = true
		}
	}
	if !declared {
		return nil, domain.Fail("BUNDLE", "Bundle 内技能注册表未声明 stack 名单，不能按技术栈裁剪；升级到包含 stack 字段的模板版本")
	}
	deps, _ := registry["skill_dependencies"].(map[string]any)
	owners := make([]string, 0, len(deps))
	for owner := range deps {
		owners = append(owners, owner)
	}
	sort.Strings(owners)
	for _, owner := range owners {
		if excluded[owner] {
			continue
		}
		list, _ := deps[owner].([]any)
		for _, row := range list {
			m, _ := row.(map[string]any)
			target, _ := m["skill"].(string)
			kind, _ := m["type"].(string)
			if !excluded[target] || (kind != "context-required" && kind != "context-conditional") {
				continue
			}
			if kind == "context-conditional" && onlyFor(m["stacks"], other) {
				continue
			}
			return nil, domain.Fail("BUNDLE", "技能注册表 stack 名单与依赖矛盾: "+owner+" → "+target+"（"+kind+"），--stack "+stack+" 不能裁剪")
		}
	}
	out := make([]string, 0, len(excluded))
	for id := range excluded {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// onlyFor 判断依赖的 stacks 限定是否只含指定栈（未声明视为所有栈都适用）。
func onlyFor(value any, stack string) bool {
	list, _ := value.([]any)
	if len(list) == 0 {
		return false
	}
	for _, v := range list {
		if name, _ := v.(string); name != stack {
			return false
		}
	}
	return true
}
