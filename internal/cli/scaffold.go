package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/scaffold"
)

const scaffoldValues = "project-name base-package output-dir platform-profile spring-boot-version java-version group-id project-version parent-group-id parent-artifact-id parent-version yss-components-version contract-file contract-id contract-version approval-ref compiler-draft-ref persisted-ref"
const scaffoldContracts = "contract-file contract-id contract-version approval-ref compiler-draft-ref persisted-ref"
const scaffoldFlags = "standalone force"
const scaffoldPlanOptions = "plan apply out plan-file expected-digest"

func registerScaffoldHelp() {
	for _, key := range strings.Fields(scaffoldValues) {
		defineArgument(key, "<值>", "与内置脚手架技能同名的工程输入；不推测版本")
	}
	defineArgument("output-dir", "<父目录>", "输出为此目录下的 project-name 子目录；项目目录必须不存在")
	defineArgument("base-package", "<Java包名>", "Java 根包名，例如 com.yss.orders")
	for _, key := range strings.Fields(scaffoldFlags) {
		argumentSpecs[key] = argumentSpec{boolean: true, description: "与内置生成器同名的开关"}
	}
	const usage = "--project-name <名称> --base-package <Java包名> --output-dir <父目录> <精确平台及Maven坐标>"
	const notes = "无需 Harness 项目、需求合同或阶段批准。生成器与模板来自程序内置 Bundle，由程序直接生成，不依赖 Node.js 或 Python，不联网；目录保护需要 PATH 中的 Git。直接生成业务空骨架，不附带参考示例；不运行 Maven。目标存在、符号链接或 --force 均拒绝。显式合同参数保持技能的正式校验（与 contract verify --kind scaffold 同一治理核心），不回退到直接生成。"
	const example = " --project-name order-service --base-package com.yss.orders --output-dir ./backend --platform-profile spring-boot-3.5-jdk17 --spring-boot-version 3.5.16 --java-version 17 --group-id com.yss --project-version 1.0.0-SNAPSHOT --parent-group-id com.yss.cloud --parent-artifact-id yss-cloud-microservice --parent-version 3.2.0-SNAPSHOT --yss-components-version 3.2.0-SNAPSHOT"
	helpTopics["scaffold"] = helpTopic{summary: "生成或预演 DDD / MVC 后端工程，查询固定目录和输出。", usage: "<ddd|mvc|catalog|verify> [参数]", examples: "yss scaffold catalog --json\nyss scaffold ddd --help\nyss scaffold mvc --help", notes: notes}
	for _, architecture := range []string{"ddd", "mvc"} {
		helpTopics["scaffold "+architecture] = helpTopic{summary: "生成 " + architecture + " 后端业务空骨架。", usage: usage, options: "独立生成必填上述平台和 Maven 坐标。--standalone 可省略；不能和合同参数混用。独立模式固定禁用参考工程；正式模式按批准合同决定。--force 不支持。", examples: "yss scaffold " + architecture + example, notes: notes}
		topic := helpTopics["scaffold "+architecture]
		topic.options += "\n--plan --out <新文件>  内存预演，保存完整输出描述，不创建目标\n--apply --plan-file <文件> --expected-digest <摘要>  应用同一计划，不得夹带工程参数"
		helpTopics["scaffold "+architecture] = topic
	}
	helpTopics["scaffold catalog"] = helpTopic{summary: "读取固定 Bundle 支持的平台、坐标和字段。", usage: "[--json]", notes: "只读；平台验证资格与独立生成许可分别报告。"}
	helpTopics["scaffold verify"] = helpTopic{summary: "核查保存计划的实际脚手架输出。", usage: "--plan-file <文件> [--json]", notes: "只读；返回 complete、not-applied、incomplete 或 unknown。"}
	registerGroup("project", "只读检查安装身份与计划输出。", "[参数]", "", "yss project inspect --root ./demo --json", "不执行恢复或重新应用。", map[string]helpTopic{
		"inspect": {summary: "检查实际身份、安装方式及未结束事务。", usage: "--root <目录> [--json]"},
		"verify":  {summary: "核查 AI 安装保存计划的实际输出。", usage: "--plan-file <文件> [--json]"},
	})
}

func scaffoldCommand(ctx context.Context, o options) (any, string, error) {
	if len(o.args) == 2 && o.args[1] == "catalog" {
		if err := onlyOptions(o, "json"); err != nil {
			return nil, "", err
		}
		r, err := scaffold.DescribeCatalog(ctx)
		return r, "", err
	}
	if len(o.args) == 2 && o.args[1] == "verify" {
		if err := onlyOptions(o, "json plan-file"); err != nil {
			return nil, "", err
		}
		p, err := scaffold.ReadPlan(o.values["plan-file"])
		if err != nil {
			return nil, "", err
		}
		r, err := scaffold.VerifyPlanOutput(ctx, p)
		return r, "", err
	}
	if len(o.args) != 2 || (o.args[1] != "ddd" && o.args[1] != "mvc") {
		return nil, "", argumentError("请选择 yss scaffold ddd 或 yss scaffold mvc")
	}
	if len(o.duplicates) > 0 {
		return nil, "", argumentError("重复参数: --" + o.duplicates[0])
	}
	if o.values["apply"] == "true" {
		if err := onlyOptions(o, "json apply plan-file expected-digest"); err != nil {
			return nil, "", err
		}
		p, err := scaffold.ReadPlan(o.values["plan-file"])
		if err != nil {
			return nil, "", err
		}
		if p.Options.Architecture != o.args[1] {
			return nil, "", domain.Fail("PLAN", "计划架构与命令不一致")
		}
		r, err := scaffold.ApplyPlan(ctx, p, o.values["expected-digest"])
		if err != nil {
			return nil, "", err
		}
		return scaffoldResult(r, o.args[1])
	}
	if o.values["plan-file"] != "" || o.values["expected-digest"] != "" || o.values["out"] != "" && o.values["plan"] != "true" {
		return nil, "", argumentError("计划参数须与 --plan 或 --apply 配合使用")
	}
	formal := false
	for _, key := range strings.Fields(scaffoldContracts) {
		_, supplied := o.values[key]
		formal = formal || supplied
	}
	if formal && o.values["standalone"] == "true" {
		return nil, "", argumentError("--standalone 不得携带合同或批准参数")
	}
	if o.values["force"] == "true" {
		return nil, "", argumentError("initialize-only 脚手架不支持 --force")
	}
	required := "project-name base-package output-dir"
	if !formal {
		required += " platform-profile spring-boot-version java-version group-id project-version parent-group-id parent-artifact-id parent-version yss-components-version"
	}
	for _, key := range strings.Fields(required) {
		if strings.TrimSpace(o.values[key]) == "" {
			return nil, "", argumentError("缺少必填参数: --" + key)
		}
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, "", domain.Fail("CAPABILITY", "脚手架目录保护需要 PATH 中的 Git")
	}
	out, err := filepath.Abs(o.values["output-dir"])
	if err != nil {
		return nil, "", err
	}
	if _, err = safefs.Path(out, o.values["project-name"]); err != nil {
		return nil, "", err
	}
	for _, key := range strings.Fields(scaffoldValues) {
		if value, supplied := o.values[key]; supplied && (value == "" || strings.HasPrefix(value, "--")) {
			return nil, "", argumentError("参数 --" + key + " 缺少值")
		}
	}
	skill := "yss-ddd-scaffold-generator"
	if o.args[1] == "mvc" {
		skill = "yss-layered-mvc-scaffold-generator"
	}
	input := scaffold.Options{
		Architecture: o.args[1], OutputDir: out, Standalone: o.values["standalone"] == "true",
		ProjectName: o.values["project-name"], BasePackage: o.values["base-package"],
		PlatformProfile: o.values["platform-profile"], SpringBootVersion: o.values["spring-boot-version"], JavaVersion: o.values["java-version"],
		GroupID: o.values["group-id"], ProjectVersion: o.values["project-version"], ParentGroupID: o.values["parent-group-id"],
		ParentArtifactID: o.values["parent-artifact-id"], ParentVersion: o.values["parent-version"], YssComponentsVersion: o.values["yss-components-version"],
		ContractFile: o.values["contract-file"], ContractID: o.values["contract-id"], ContractVersion: o.values["contract-version"],
		ApprovalRef: o.values["approval-ref"], CompilerDraftRef: o.values["compiler-draft-ref"], PersistedRef: o.values["persisted-ref"],
	}
	if !formal {
		// 独立生成固定禁用参考示例；正式模式由批准合同的 reference_example 决定。
		without := false
		input.WithExample = &without
	}
	if o.values["plan"] == "true" {
		if o.values["out"] == "" {
			return nil, "", argumentError("预览须指定 --out <新计划文件>")
		}
		p, err := scaffold.BuildPlan(ctx, input)
		if err != nil {
			return nil, "", err
		}
		if err = scaffold.SavePlan(p, o.values["out"]); err != nil {
			return nil, "", err
		}
		return p.Public(), "", nil
	}
	generated, err := scaffold.Run(ctx, input)
	if ctx.Err() != nil {
		return nil, "", ctx.Err()
	}
	if err != nil {
		var rejection *scaffold.Error
		if errors.As(err, &rejection) {
			return nil, "", domain.Fail("EXECUTION", fmt.Sprintf("%s 生成失败: %s", skill, rejection.Message))
		}
		return nil, "", err
	}
	return scaffoldResult(generated, o.args[1])
}

func scaffoldResult(generated *scaffold.Result, architecture string) (any, string, error) {
	skill := "yss-ddd-scaffold-generator"
	if architecture == "mvc" {
		skill = "yss-layered-mvc-scaffold-generator"
	}
	inspection, err := bundle.Inspect("spec")
	if err != nil {
		return nil, "", err
	}
	manifest := scaffold.Plain(generated.Manifest).(map[string]any)
	return map[string]any{"projectRoot": generated.ProjectRoot, "manifestPath": generated.ManifestPath, "generatorSkill": skill, "architecture": architecture, "generationMode": manifest["generation_mode"], "completionLevel": manifest["completion_level"], "verification": manifest["verification"], "bundleHash": inspection.BundleHash, "sourceState": inspection.SourceState}, "", nil
}

func onlyOptions(o options, allowed string) error {
	if len(o.duplicates) > 0 {
		return argumentError("重复参数: --" + o.duplicates[0])
	}
	keys := map[string]bool{}
	for _, key := range strings.Fields(allowed) {
		keys[key] = true
	}
	for key := range o.values {
		if !keys[key] {
			return argumentError("当前操作不支持 --" + key)
		}
	}
	return nil
}
