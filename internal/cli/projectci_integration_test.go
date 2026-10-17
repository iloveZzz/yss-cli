//go:build !yss_nobundle

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// project-ci 安装：默认范围安装与 project-ci check 相同的完整治理检查，--scope native-go 保留有限检查；
// 计划范围与应用范围必须一致，人工改动的托管文件不被覆盖。

func ciProject(t *testing.T) string {
	t.Helper()
	// 事务记录绑定实例绝对路径，不能复制共享实例；整条安装链只 init 一次。
	dir := filepath.Join(t.TempDir(), "proj")
	if code, env, err := runYSS("init", "--profile", "spec", "--root", dir); err != nil || code != 0 {
		t.Fatalf("init: %d %v %v", code, env, err)
	}
	writeCheckFile(t, dir, "vendor/yss-cli/go.mod", "module github.com/iloveZzz/yss-cli\n\ngo 1.27.1\n")
	writeCheckFile(t, dir, "vendor/yss-cli/cmd/yss/main.go", "package main\n")
	return dir
}

// savePlan 按帮助说明不加 --json 保存原始计划。
func savePlan(t *testing.T, dir, ref string, args ...string) {
	t.Helper()
	var out, errb bytes.Buffer
	if code := Run(context.Background(), append([]string{"project-ci", "plan", "--root", dir, "--cli-source", "vendor/yss-cli"}, args...), &out, &errb); code != 0 {
		t.Fatalf("plan %v: %d %s", args, code, errb.String())
	}
	writeCheckFile(t, dir, ref, out.String())
}

func TestProjectCIInstallScopes(t *testing.T) {
	dir := ciProject(t)
	savePlan(t, dir, "docs/ci-plan.json")
	if code, env, _ := runYSS("project-ci", "apply", "--root", dir, "--scope", "native-go", "--plan-file", "docs/ci-plan.json"); code == 0 {
		t.Fatalf("应用范围与计划不一致时必须拒绝: %v", env)
	}
	code, env, err := runYSS("project-ci", "apply", "--root", dir, "--plan-file", "docs/ci-plan.json")
	if err != nil || code != 0 {
		t.Fatalf("apply: %d %v %v", code, env, err)
	}
	result := env["result"].(map[string]any)
	if result["scope"] != "complete-governance" || result["full_project_governance"] != true {
		t.Fatalf("默认应安装完整治理: %v", result)
	}
	workflow, err := os.ReadFile(filepath.Join(dir, ".github/workflows/yss-governance-native.yml"))
	if err != nil || !strings.Contains(string(workflow), "project-ci check --root \"$GITHUB_WORKSPACE\" --runtime-store off") || strings.Contains(string(workflow), "--scope native-go") {
		t.Fatalf("工作流须运行完整治理检查: %s", workflow)
	}
	for _, args := range [][]string{{"--runtime-store", "off"}, {"--scope", "native-go"}} {
		if code, env, _ := runYSS(append([]string{"project-ci", "check", "--root", dir}, args...)...); code != 0 {
			t.Errorf("安装完整范围后 check %v 应可读取配置: %v", args, env)
		}
	}

	// 同一实例上切换为有限范围：托管文件按凭据覆盖。
	savePlan(t, dir, "docs/native.json", "--scope", "native-go")
	if code, env, _ := runYSS("project-ci", "apply", "--root", dir, "--scope", "native-go", "--plan-file", "docs/native.json"); code != 0 {
		t.Fatalf("native-go 安装: %v", env)
	}
	workflowRef := filepath.Join(dir, ".github/workflows/yss-governance-native.yml")
	raw, _ := os.ReadFile(workflowRef)
	if !strings.Contains(string(raw), "--scope native-go") {
		t.Fatalf("有限范围工作流: %s", raw)
	}
	if err := os.WriteFile(workflowRef, append(raw, []byte("# 人工改动\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	code, env, _ = runYSS("project-ci", "plan", "--root", dir, "--cli-source", "vendor/yss-cli")
	if code == 0 || env["code"] != "CI_CONFLICT" {
		t.Fatalf("人工改动的托管文件不得覆盖: %d %v", code, env)
	}
	if code, env, _ := runYSS("project-ci", "transition", "--root", dir); code != 2 || env["code"] != "ARGUMENT" {
		t.Fatalf("transition 仍只支持 native-go，且给出参数错误而非“尚未迁移”: %d %v", code, env)
	}
}
