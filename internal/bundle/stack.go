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

// StackExclusions 返回选择某个技术栈时不分发的技能：java 排除前端专项，vue 排除后端专项；
// 专项指 impacts 只含该端（可另含 api），其余技能始终保留。分组只取 Bundle
// 内注册表的 impacts；保留技能经 context-required 依赖到的技能放回。context-conditional 依赖
// 只在条件成立时需要（如 when: pojo），显式选栈后另一端的条件不会出现，不据此放回。
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
	other := "frontend"
	if stack == "vue" {
		other = "backend"
	}
	excluded := map[string]bool{}
	for _, item := range skills {
		s, _ := item.(map[string]any)
		id, _ := s["id"].(string)
		impacts := map[string]bool{}
		list, _ := s["impacts"].([]any)
		for _, v := range list {
			if name, ok := v.(string); ok {
				impacts[name] = true
			}
		}
		// 栈专项：影响另一端，且除 api 外不影响任何其他面。同时涉及生命周期、原型、质量或
		// 两端的技能（如合同编译器、设计系统、技术设计）属于流程能力，始终保留。
		onlyOther := impacts[other]
		for name := range impacts {
			if name != other && name != "api" {
				onlyOther = false
			}
		}
		if id != "" && onlyOther {
			excluded[id] = true
		}
	}
	deps, _ := registry["skill_dependencies"].(map[string]any)
	for changed := true; changed; {
		changed = false
		for owner, rows := range deps {
			if excluded[owner] {
				continue
			}
			list, _ := rows.([]any)
			for _, row := range list {
				m, _ := row.(map[string]any)
				target, _ := m["skill"].(string)
				kind, _ := m["type"].(string)
				if excluded[target] && kind == "context-required" {
					delete(excluded, target)
					changed = true
				}
			}
		}
	}
	out := make([]string, 0, len(excluded))
	for id := range excluded {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}
