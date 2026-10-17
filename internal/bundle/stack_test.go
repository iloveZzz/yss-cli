package bundle

import (
	"encoding/base64"
	"strings"
	"testing"
)

const stackRegistry = `skills:
  - { id: lombok, stack: java, impacts: [backend] }
  - { id: yss-dto, stack: java, impacts: [backend, api] }
  - { id: java-style, stack: java, impacts: [backend] }
  - { id: backend-review, stack: java, impacts: [backend, quality] }
  - { id: yss-ui, stack: vue, impacts: [frontend] }
  - { id: yss-api-integration, stack: vue, impacts: [frontend, api] }
  - { id: theme-token, stack: vue, impacts: [frontend, prototype] }
  - { id: yss-design-system, impacts: [prototype, frontend] }
  - { id: contract-compiler, impacts: [lifecycle, backend, frontend] }
  - { id: tdd, impacts: [quality] }
skill_dependencies:
  backend-review:
    - { skill: java-style, type: context-required }
  code-review:
    - { skill: lombok, type: context-conditional, when: persistence, stacks: [java] }
`

func stackBundle() *Bundle { return registryBundle(stackRegistry) }

func registryBundle(text string) *Bundle {
	return &Bundle{Files: map[string]File{skillRegistryRef: {Data: base64.StdEncoding.EncodeToString([]byte(text))}}}
}

func TestStackExclusions(t *testing.T) {
	for stack, want := range map[string]string{
		"java": "theme-token,yss-api-integration,yss-ui",
		"vue":  "backend-review,java-style,lombok,yss-dto",
		"none": "",
		"":     "",
	} {
		got, err := StackExclusions(stackBundle(), stack)
		if err != nil || strings.Join(got, ",") != want {
			t.Errorf("%q: got %v err %v, want %s", stack, got, err, want)
		}
	}
}

func TestStackExclusionsRejectsUnknownAndMissingRegistry(t *testing.T) {
	if _, err := StackExclusions(stackBundle(), "go"); err == nil {
		t.Error("未知技术栈必须拒绝")
	}
	if _, err := StackExclusions(&Bundle{Files: map[string]File{}}, "java"); err == nil {
		t.Error("缺少注册表时不能猜测分组")
	}
}

func TestStackExclusionsFailsClosedOnUndeclaredOrContradictoryList(t *testing.T) {
	if _, err := StackExclusions(registryBundle("skills:\n  - { id: lombok, impacts: [backend] }\n"), "java"); err == nil || !strings.Contains(err.Error(), "stack 名单") {
		t.Errorf("未声明 stack 时不得按 impacts 推断: %v", err)
	}
	for name, deps := range map[string]string{
		"通用技能强依赖另一栈": "  tdd:\n    - { skill: lombok, type: context-required }\n",
		"条件依赖未限定栈":   "  tdd:\n    - { skill: lombok, type: context-conditional, when: pojo }\n",
		"条件依赖限定含所选栈": "  tdd:\n    - { skill: lombok, type: context-conditional, when: pojo, stacks: [java, vue] }\n",
	} {
		text := "skills:\n  - { id: lombok, stack: java }\n  - { id: tdd }\nskill_dependencies:\n" + deps
		if _, err := StackExclusions(registryBundle(text), "vue"); err == nil || !strings.Contains(err.Error(), "矛盾") {
			t.Errorf("%s: %v", name, err)
		}
	}
	text := "skills:\n  - { id: lombok, stack: java }\n  - { id: tdd }\nskill_dependencies:\n  tdd:\n    - { skill: lombok, type: review-only }\n"
	if got, err := StackExclusions(registryBundle(text), "vue"); err != nil || strings.Join(got, ",") != "lombok" {
		t.Errorf("非上下文依赖不阻止裁剪: %v %v", got, err)
	}
}
