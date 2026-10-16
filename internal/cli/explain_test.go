package cli

import (
	"os"
	"path/filepath"
	"testing"
)

const explainRegistryFixture = `schema_version: 1
stages:
  - id: stage.plan
    name: Plan
    goal: 确认目标
gates:
  - id: gate.plan-approved
    name: Plan 批准
    trigger: Plan 结论进入 Spec
work_units:
  - id: work-unit.plan-opportunity
    name: Plan 机会
    completion: 机会已确认
`

const explainGlossaryFixture = "# 流程术语表\n\n## 新手先懂这些词\n\n- **工作单元**：列表里的说明不算表格行。\n\n| 术语 | 含义 | 英文标识 | 避免 / 备注 |\n|---|---|---|---|\n| 工作单元 | 一次有界执行。 | — | 不等同于主阶段。 |\n| `ready-for-agent` | 可交给 Agent。 | — | — |\n\n后续段落 | 不是表格\n"

func explainFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".template-spec", "process")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"lifecycle-registry.yaml": explainRegistryFixture, "process-glossary.md": explainGlossaryFixture} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func explainIDs(t *testing.T, root, query string) []string {
	t.Helper()
	result, err := explainCommand(root, []string{query})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range result.(map[string]any)["matches"].([]explainMatch) {
		if m.ID != "" {
			ids = append(ids, m.ID)
		} else {
			ids = append(ids, "term:"+m.Term)
		}
	}
	return ids
}

func TestExplainMatchesRegistryAndGlossary(t *testing.T) {
	t.Parallel()
	root := explainFixture(t)
	for query, want := range map[string]string{
		"gate.plan-approved": "gate.plan-approved",
		"plan-approved":      "gate.plan-approved",         // 去掉类别前缀也能精确命中
		"Plan 批准":            "gate.plan-approved",         // 按名称包含
		"工作单元":               "term:工作单元",                  // 只取术语表格，不取新手列表
		"READY-FOR-AGENT":    "term:`ready-for-agent`",     // 不区分大小写
		"plan.plan-opp":      "",                           // 不存在
		"opportunity":        "work-unit.plan-opportunity", // ID 片段
	} {
		got := explainIDs(t, root, query)
		if want == "" {
			if len(got) != 0 {
				t.Errorf("%q 不应命中: %v", query, got)
			}
			continue
		}
		if len(got) == 0 || got[0] != want {
			t.Errorf("%q 首个结果 = %v, want %s", query, got, want)
		}
	}
	// 精确 ID 排在名称包含之前。
	if got := explainIDs(t, root, "plan"); len(got) < 2 || got[0] != "stage.plan" {
		t.Errorf("精确匹配应排在前面: %v", got)
	}
}

func TestExplainRejectsMissingInput(t *testing.T) {
	t.Parallel()
	if _, err := explainCommand(explainFixture(t), nil); err == nil {
		t.Error("缺少查询词应报错")
	}
	if _, err := explainCommand(t.TempDir(), []string{"x"}); err == nil {
		t.Error("非 YSS 项目应报错")
	}
}
