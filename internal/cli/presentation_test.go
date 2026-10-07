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
