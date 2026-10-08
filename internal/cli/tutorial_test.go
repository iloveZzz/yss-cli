package cli

import (
	"bytes"
	"context"
	"github.com/iloveZzz/yss-cli/internal/helpview"
	"strings"
	"testing"
)

func TestLifecycleTutorialHasOrderedStagesAndActionableReadingPaths(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"help", "tutorial", "governed"}, &out, &stderr); code != 0 {
		t.Fatalf("exit=%d %s", code, &stderr)
	}
	previous := -1
	for _, id := range []string{"stage.entry-triage", "stage.plan", "stage.spec-architecture", "stage.product-design", "stage.system-data-engineering", "stage.ticket-formalization", "stage.vertical-slice-implementation", "stage.verification-release-retrospective"} {
		index := strings.Index(out.String(), id)
		if index <= previous {
			t.Fatalf("missing or unordered %s: %s", id, &out)
		}
		previous = index
	}
	for _, text := range []string{"来源摘要", "前置条件", "预期结果", "下一步", "失败恢复"} {
		if !strings.Contains(out.String(), text) {
			t.Fatalf("missing %s", text)
		}
	}
	business := strings.Index(out.String(), "产品设计校准后完成 work-unit.business-ticket-formalization")
	engineering := strings.Index(out.String(), "5. 系统 / 数据架构与工程契约")
	if business < 0 || business >= engineering || !strings.Contains(out.String(), "工程契约和实现仓库准备须先闭合") {
		t.Fatalf("教程必须区分工程设计前的业务正式化与工程准备后的实现切片准入: %s", &out)
	}
	for _, args := range [][]string{{"help", "tutorial", "daily"}, {"help", "tutorial", "spec"}, {"help", "tutorial", "design"}, {"help", "tutorial", "backend"}, {"help", "tutorial", "frontend"}, {"help", "examples", "sync"}, {"help", "errors", "IDENTITY"}} {
		out.Reset()
		stderr.Reset()
		if code := Run(context.Background(), args, &out, &stderr); code != 0 || len(out.Bytes()) == 0 {
			t.Fatalf("%v exit=%d %s", args, code, &stderr)
		}
	}
}

func TestAllCommandHelpAndExamplesHaveACompleteReadingPath(t *testing.T) {
	for key := range commands {
		for _, prefix := range [][]string{{"help"}, {"help", "examples"}} {
			args := append(append([]string{}, prefix...), strings.Fields(key)...)
			var out, stderr bytes.Buffer
			if code := Run(context.Background(), args, &out, &stderr); code != 0 {
				t.Fatalf("%v: %d %s", args, code, &stderr)
			}
			for _, term := range []string{"预期结果", "下一步", "yss "} {
				if !strings.Contains(out.String(), term) {
					t.Fatalf("%v 缺少 %s", args, term)
				}
			}
			if !strings.Contains(out.String(), "前置条件") && !strings.Contains(out.String(), "适用条件") {
				t.Fatalf("%v 缺少条件", args)
			}
		}
	}
}

func TestTutorialCommandsUseOnlySupportedOptionsAndKinds(t *testing.T) {
	for _, topic := range tutorialTopics[:7] {
		content, err := renderTutorial(topic)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "yss ") {
				continue
			}
			line, _, _ = strings.Cut(line, " > ")
			args := strings.Fields(strings.TrimPrefix(line, "yss "))
			for i := range args {
				args[i] = strings.Trim(args[i], "\"")
			}
			o, err := parse(args)
			if err == nil {
				err = validateArguments(o.args[0], o)
			}
			if err != nil {
				t.Fatalf("%s: 无效命令 %s: %v", topic, line, err)
			}
		}
	}
}

func TestEachProfileTutorialUsesItsFixedEntryAndAllowedOrder(t *testing.T) {
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		view, err := helpview.Load(profile)
		if err != nil {
			t.Fatal(err)
		}
		content, err := profileTutorial(profile)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(content, "--id "+view.EntryWorkUnit) {
			t.Fatalf("%s 错误入口", profile)
		}
		last := -1
		for _, stage := range view.Stages {
			index := strings.Index(content, "（"+stage.ID+"）")
			if index <= last {
				t.Fatalf("%s 阶段顺序错误 %s", profile, stage.ID)
			}
			last = index
		}
	}
}
