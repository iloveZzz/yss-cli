//go:build !yss_nobundle

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 端到端：在 yss init 生成的真实实例上，用实例自带模板构造资产，确认 check / verify --gate
// 调用的是现有 verify，并把结论转为统一格式。资产写入独立目录，不影响路由用例。

func checkProject(t *testing.T) string {
	t.Helper()
	fixtureOnce.Do(func() { fixture, fixtureErr = setupFixture() })
	if fixtureErr != nil {
		t.Fatal(fixtureErr)
	}
	return fixture.project
}

func copyTemplate(t *testing.T, project, from, to string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(project, from))
	if err != nil {
		t.Fatal(err)
	}
	writeCheckFile(t, project, to, string(raw))
}

func checkItems(t *testing.T, env map[string]any) map[string]map[string]any {
	t.Helper()
	result, _ := env["result"].(map[string]any)
	items, _ := result["items"].([]any)
	byKey := map[string]map[string]any{}
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		byKey[item["file"].(string)+"|"+explainString(item["asset_kind"])] = item
	}
	return byKey
}

func TestCheckRunsExistingVerifiers(t *testing.T) {
	project := checkProject(t)
	dir := ".work/check-it"
	copyTemplate(t, project, ".template-spec/templates/approval-record-template.yaml", dir+"/approval.yaml")
	copyTemplate(t, project, ".template-spec/process/templates/lifecycle-checkpoint-template.yaml", dir+"/checkpoint.yaml")
	copyTemplate(t, project, ".template-spec/process/templates/user-decision-template.yaml", dir+"/decision.yaml")

	code, env, err := runYSS("check", dir+"/approval.yaml", dir+"/checkpoint.yaml", dir+"/decision.yaml", "--root", project)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v env=%v", code, err, env)
	}
	result := env["result"].(map[string]any)
	if result["status"] != "incomplete" || result["execution_authorization"] != "not-evaluated" {
		t.Fatalf("存在未评估项时不能报告通过: %v", result)
	}
	items := checkItems(t, env)
	for key, want := range map[string]string{
		dir + "/approval.yaml|approval-record":        "passed",
		dir + "/checkpoint.yaml|lifecycle-checkpoint": "passed",
		dir + "/decision.yaml|user-decision":          "not-evaluated",
	} {
		if items[key]["status"] != want {
			t.Errorf("%s: %v", key, items[key])
		}
	}
	if command := explainString(items[dir+"/approval.yaml|approval-record"]["command"]); !strings.HasPrefix(command, "yss evidence verify --kind approval --history --file "+dir+"/approval.yaml") {
		t.Errorf("须记录实际调用的现有命令: %s", command)
	}
}

func TestCheckReportsSchemaFindingInUnifiedFormat(t *testing.T) {
	project := checkProject(t)
	ref := ".work/check-it/reconciliation.yaml"
	copyTemplate(t, project, ".template-spec/process/templates/context-reconciliation-template.yaml", ref)
	code, env, err := runYSS("check", ref, "--root", project)
	if err != nil || code != 1 || env["code"] != "CHECK_FAILED" {
		t.Fatalf("模板占位符应校验失败: code=%d err=%v env=%v", code, err, env)
	}
	findings, _ := env["result"].(map[string]any)["findings"].([]any)
	if len(findings) == 0 {
		t.Fatal("失败须有发现")
	}
	f := findings[0].(map[string]any)
	for _, key := range []string{"code", "file", "message", "fix", "command"} {
		if explainString(f[key]) == "" {
			t.Errorf("发现缺少 %s: %v", key, f)
		}
	}
	if f["code"] == "SCHEMA" && explainString(f["pointer"]) == "" {
		t.Errorf("Schema 失败须给出 JSON 指针: %v", f)
	}
}

func TestVerifyGateComposesCheckpointAndGateChecks(t *testing.T) {
	project := checkProject(t)
	ref := ".work/check-it/gate-checkpoint.yaml"
	copyTemplate(t, project, ".template-spec/process/templates/lifecycle-checkpoint-template.yaml", ref)
	code, env, err := runYSS("verify", "--gate", "plan-approved", "--checkpoint", ref, "--root", project)
	if err != nil {
		t.Fatal(err)
	}
	result := env["result"].(map[string]any)
	if result["kind"] != "yss-gate-verify" || result["selection"].(map[string]any)["gate"] != "gate.plan-approved" {
		t.Fatalf("门禁 ID 应规范为 gate.*: %v", result)
	}
	items := checkItems(t, env)
	if _, ok := items[ref+"|lifecycle-checkpoint"]; !ok {
		t.Errorf("须运行 lifecycle verify: %v", items)
	}
	gate, ok := items[ref+"|gate-checks"]
	if !ok {
		t.Fatalf("须运行 gate-checks: %v", items)
	}
	// 模板 checkpoint 没有门禁证据，门禁检查必须失败，整体不得通过。
	if gate["status"] != "failed" || code != 1 || env["code"] != "CHECK_FAILED" {
		t.Fatalf("缺证据的门禁不得通过: code=%d gate=%v", code, gate)
	}
}
