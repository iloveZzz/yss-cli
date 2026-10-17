package bundle

import (
	"encoding/base64"
	"strings"
	"testing"
)

const stackRegistry = `skills:
  - { id: lombok, impacts: [backend] }
  - { id: yss-dto, impacts: [backend, api] }
  - { id: java-style, impacts: [backend] }
  - { id: yss-ui, impacts: [frontend] }
  - { id: yss-api-integration, impacts: [frontend, api] }
  - { id: yss-design-system, impacts: [prototype, frontend] }
  - { id: contract-compiler, impacts: [lifecycle, backend, frontend] }
  - { id: backend-review, impacts: [backend, quality] }
  - { id: tdd, impacts: [quality] }
skill_dependencies:
  backend-review:
    - { skill: java-style, type: context-required }
  contract-compiler:
    - { skill: lombok, type: context-conditional, when: pojo }
`

func stackBundle() *Bundle {
	return &Bundle{Files: map[string]File{skillRegistryRef: {Data: base64.StdEncoding.EncodeToString([]byte(stackRegistry))}}}
}

func TestStackExclusions(t *testing.T) {
	for stack, want := range map[string]string{
		"java": "yss-api-integration,yss-ui",
		"vue":  "lombok,yss-dto",
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
