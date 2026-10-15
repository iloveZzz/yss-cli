package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHumanSummaryPreservesPipeAndJSON(t *testing.T) {
	for _, mode := range []string{"pipe", "human", "json"} {
		t.Run(mode, func(t *testing.T) {
			args := []string{"version"}
			if mode != "pipe" {
				args = append(args, "--"+mode)
			}
			var out, stderr bytes.Buffer
			if code := Run(context.Background(), args, &out, &stderr); code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, &stderr)
			}
			if mode == "human" {
				if !strings.Contains(out.String(), "CLI 版本：") || json.Valid(out.Bytes()) {
					t.Fatalf("expected Chinese summary: %s", &out)
				}
				return
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if mode == "pipe" && result["version"] == nil {
				t.Fatalf("raw result changed: %#v", result)
			}
			if mode == "json" && (result["outputVersion"] != float64(1) || result["diagnostic"] != nil || result["result"] == nil) {
				t.Fatalf("envelope changed: %#v", result)
			}
		})
	}
}

func TestDiagnosticsDescribeMissingRootAndPreserveMachineResult(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "missing project")
	var envelopes []map[string]any
	for _, extra := range [][]string{nil, {"--diagnostics"}} {
		args := append([]string{"doctor", "--root", root, "--json"}, extra...)
		var out, stderr bytes.Buffer
		if code := Run(context.Background(), args, &out, &stderr); code != 1 {
			t.Fatalf("exit=%d %s %s", code, &out, &stderr)
		}
		var env map[string]any
		if err := json.Unmarshal(out.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		envelopes = append(envelopes, env)
	}
	if !reflect.DeepEqual(envelopes[0]["result"], envelopes[1]["result"]) || envelopes[0]["diagnostic"] != nil {
		t.Fatal("existing JSON changed")
	}
	d, ok := envelopes[1]["diagnostic"].(map[string]any)
	if !ok || d["id"] != "PROJECT_ROOT_NOT_FOUND" || d["cause"] == nil || d["recheck"] == nil {
		t.Fatalf("missing actionable diagnosis: %#v", envelopes[1])
	}
	context := d["context"].(map[string]any)
	if context["root"] != root {
		t.Fatalf("wrong directory: %#v", context)
	}
}

func TestPresentationFlagsAndTerminalBoundary(t *testing.T) {
	for _, args := range [][]string{{"version", "--human", "--json"}, {"version", "--diagnostics"}} {
		var out, stderr bytes.Buffer
		if code := Run(context.Background(), args, &out, &stderr); code != 2 {
			t.Fatalf("invalid flags exit %d", code)
		}
	}
	var out, stderr bytes.Buffer
	if code := RunWithTerminal(context.Background(), []string{"version"}, &out, &stderr, func(io.Writer) bool { return true }); code != 0 || !strings.Contains(out.String(), "CLI 版本：") {
		t.Fatalf("terminal summary %d %s", code, &out)
	}
	out.Reset()
	if code := Run(context.Background(), []string{"init", "--help", "--human", "--json"}, &out, &stderr); code != 0 || !strings.Contains(out.String(), "用法: yss init") {
		t.Fatalf("help priority %d %s", code, &stderr)
	}
}

func TestUnknownOptionSuggestionsStayWithinTheSelectedCommand(t *testing.T) {
	for _, args := range [][]string{{"sync", "--wat", "--json"}, {"--wat", "sync", "--json"}, {"stage", "update", "--refres", "--json"}} {
		var out, stderr bytes.Buffer
		if code := Run(context.Background(), args, &out, &stderr); code != 2 {
			t.Fatalf("expected argument rejection, got %d", code)
		}
		if strings.Contains(out.String(), "--gate") || strings.Contains(out.String(), "使用: --refresh") {
			t.Fatalf("unavailable suggestion: %s", &out)
		}
	}
}

func TestPresentationChoicesAfterMalformedFlagAndDiagnosticsOnSuccess(t *testing.T) {
	var out, stderr bytes.Buffer
	if exit := Run(context.Background(), []string{"doctor", "--wat", "--human"}, &out, &stderr); exit != 2 || out.Len() != 0 || !strings.Contains(stderr.String(), "处理（只读）") {
		t.Fatalf("human parse failure: %d %s %s", exit, &out, &stderr)
	}
	out.Reset()
	stderr.Reset()
	if exit := Run(context.Background(), []string{"doctor", "--wat", "--json", "--diagnostics"}, &out, &stderr); exit != 2 || !strings.Contains(out.String(), `"diagnostic"`) {
		t.Fatalf("diagnostics parse failure: %d %s", exit, &out)
	}
	out.Reset()
	stderr.Reset()
	if exit := Run(context.Background(), []string{"version", "--json", "--diagnostics"}, &out, &stderr); exit != 0 || strings.Contains(out.String(), `"diagnostic"`) {
		t.Fatalf("success diagnostic changed: %d %s", exit, &out)
	}
}

func TestFrozenCompatibilityHelpDoesNotAdvertiseNewPresentationFlags(t *testing.T) {
	for _, path := range []string{"compat create-yss-spec", "compat-api native.snapshot"} {
		content, err := renderHelp(strings.Fields(path))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(content, "--human") || strings.Contains(content, "--diagnostics") {
			t.Fatalf("冻结入口不应增加呈现参数: %s", content)
		}
	}
}

func TestHumanSavedPlanNextCommandKeepsExecutorScope(t *testing.T) {
	for _, scenario := range []struct {
		args, expected []string
	}{
		{[]string{"skills", "ensure", "yss-tactical-design", "--root", "/project with spaces", "--out", "/plan.json"}, []string{"yss", "skills", "ensure", "yss-tactical-design", "--root", "/project with spaces", "--apply", "--plan-file", "/plan.json", "--json"}},
		{[]string{"assets", "ensure", "stage.system-data-engineering", "--root", "/project with spaces", "--out", "/plan.json"}, []string{"yss", "assets", "ensure", "stage.system-data-engineering", "--root", "/project with spaces", "--apply", "--plan-file", "/plan.json", "--json"}},
		{[]string{"update", "plan", "--tool-root", "/tool with spaces", "--out", "/plan.json"}, []string{"yss", "update", "apply", "--tool-root", "/tool with spaces", "--plan-file", "/plan.json", "--json"}},
		{[]string{"migrate", "plan", "--root", "/project with spaces", "--out", "/plan.json"}, []string{"yss", "migrate", "apply", "--root", "/project with spaces", "--plan-file", "/plan.json", "--json"}},
		{[]string{"sync", "--out", "/plan.json"}, []string{"yss", "sync", "--root", "/actual project", "--apply", "--plan-file", "/plan.json", "--json"}},
		{[]string{"handoff", "import", "--kind", "spec-baseline", "--root", "/project with spaces", "--plan", "--out", "/plan.json"}, []string{"yss", "handoff", "import", "--root", "/project with spaces", "--apply", "--plan-file", "/plan.json", "--json", "--kind", "spec-baseline"}},
	} {
		o, err := parse(scenario.args)
		if err != nil {
			t.Fatal(err)
		}
		result := map[string]any{"digest": "saved", "changes": []any{}, "root": "/actual project"}
		output := renderHumanSuccess(o.args[0], o, "spec", result)
		if !strings.Contains(output, formatArgv(scenario.expected)) {
			t.Fatalf("%v 缺少有效下一步 %s: %s", scenario.args, formatArgv(scenario.expected), output)
		}
		apply, err := parse(scenario.expected[1:])
		if err != nil {
			t.Fatal(err)
		}
		if err := validateArguments(apply.args[0], apply); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRouteHumanSummaryTranslatesExistingFieldsOnly(t *testing.T) {
	args := []string{"lifecycle", "route", "--root", "/spec project", "--task", "task.md", "--implementation-root", "/impl", "--base", "abc123"}
	o, err := parse(args)
	if err != nil {
		t.Fatal(err)
	}
	daily := map[string]any{"kind": "lifecycle.route", "delivery_path": "daily", "reasons": []any{"符合日常路线"}, "changed_files": []any{"src/a.js", "src/b.js"}, "diagnostics": []any{}}
	output := renderHumanSuccess("lifecycle", o, "spec", daily)
	for _, want := range []string{"日常路径", "原因：符合日常路线", "当前改动：2 个文件", "yss lifecycle verify-daily --root '/spec project' --task task.md --implementation-root /impl --base abc123", "不授予实现、批准或发布资格"} {
		if !strings.Contains(output, want) {
			t.Fatalf("日常摘要缺少 %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "digest") || strings.Contains(output, "git_inputs") {
		t.Fatalf("摘要不应倾倒摘要与 Git 输入字段:\n%s", output)
	}
	governed := map[string]any{"kind": "lifecycle.route", "delivery_path": "governed", "reasons": []any{"风险影响需要正式治理: data-migration"}, "diagnostics": []any{map[string]any{"code": "GOVERNED_REQUIRED", "message": "风险影响需要正式治理: data-migration", "recovery": "保留已有编辑和证据，恢复最近可信正式阶段"}}}
	output = renderHumanSuccess("lifecycle", o, "spec", governed)
	for _, want := range []string{"正式路径", "阻断：风险影响需要正式治理: data-migration", "恢复：保留已有编辑和证据"} {
		if !strings.Contains(output, want) {
			t.Fatalf("正式摘要缺少 %q:\n%s", want, output)
		}
	}
	// 缺少交付路径时回退到通用输出，不编造结论。
	fallback := renderHumanSuccess("lifecycle", o, "spec", map[string]any{"kind": "lifecycle.route"})
	if strings.Contains(fallback, "日常路径") || strings.Contains(fallback, "正式路径") {
		t.Fatalf("没有交付路径时不应给出路径结论:\n%s", fallback)
	}
}
