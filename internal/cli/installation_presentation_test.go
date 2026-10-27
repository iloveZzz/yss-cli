package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/project"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func TestInstallationReceiptPreservesTransactionFields(t *testing.T) {
	for _, kind := range []project.InstallationKind{project.InstallationSkills, project.InstallationGoverned} {
		for _, status := range []string{"completed", "failed"} {
			original := transaction.Result{Status: status, FileApplication: "applied", Verification: "passed", TransactionID: "tx", PlanDigest: "digest", BackupPath: "backup", Pending: []string{"pending"}}
			got := installationReceipt(original, kind)
			if got["installation"] != kind {
				t.Fatalf("missing installation: %+v", got)
			}
			delete(got, "installation")
			if !reflect.DeepEqual(got, resultObject(original)) {
				t.Fatalf("transaction fields lost: %+v", got)
			}
		}
	}
}

func TestInstallationHumanNextStep(t *testing.T) {
	for _, applied := range []bool{false, true} {
		for _, kind := range []project.InstallationKind{project.InstallationSkills, project.InstallationGoverned} {
			o := options{args: []string{"init"}, values: map[string]string{"root": "/tmp/project"}}
			if applied {
				o.values["apply"] = "true"
				o.values["plan-file"] = "/tmp/plan.json"
			}
			text := renderHumanSuccess("init", o, "spec", installationReceipt(transaction.Result{Status: "completed"}, kind))
			if !strings.Contains(text, "doctor") {
				t.Fatalf("no installation check: %s", text)
			}
			if kind == project.InstallationSkills && (strings.Contains(text, "lifecycle query") || !strings.Contains(text, "独立技能")) {
				t.Fatalf("skills routed to governance: %s", text)
			}
			if kind == project.InstallationGoverned && !strings.Contains(text, "lifecycle query") {
				t.Fatalf("governance navigation lost: %s", text)
			}
		}
	}
	o := options{args: []string{"init"}, values: map[string]string{"plan": "true"}}
	text := renderHumanSuccess("init", o, "spec", map[string]any{"installation": "skills", "changes": []any{}, "digest": "plan"})
	if !strings.Contains(text, "计划已生成") || strings.Contains(text, "已安装") || strings.Contains(text, "已验收") {
		t.Fatalf("plan claimed installation: %s", text)
	}
}

func TestInstallationHelpContract(t *testing.T) {
	text, err := renderHelp([]string{"init"})
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"默认 skills", "--installation governed", "--full"} {
		if !strings.Contains(text, part) {
			t.Fatalf("init help missing %s: %s", part, text)
		}
	}
	text, err = profileTutorial("spec")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "init --profile spec --installation governed") {
		t.Fatalf("governance tutorial uses skills installation: %s", text)
	}
}

func TestBoundPlanHumanApplyCommands(t *testing.T) {
	for _, tc := range []struct {
		args []string
		kind string
		want []string
	}{
		{[]string{"scaffold", "ddd"}, "scaffold-plan", []string{"yss", "scaffold", "ddd", "--apply", "--plan-file", "plan.json", "--expected-digest", "confirmed", "--json"}},
		{[]string{"init"}, "installation-plan", []string{"yss", "init", "--root", "中文 & root", "--apply", "--plan-file", "plan.json", "--json", "--profile", "spec", "--expected-digest", "confirmed"}},
		{[]string{"attach"}, "installation-plan", []string{"yss", "attach", "--root", "中文 & root", "--apply", "--plan-file", "plan.json", "--json", "--profile", "spec", "--expected-digest", "confirmed"}},
	} {
		o := options{args: tc.args, values: map[string]string{"root": "中文 & root", "profile": "spec", "plan": "true", "out": "plan.json"}}
		result := map[string]any{"kind": tc.kind, "digest": "confirmed"}
		if got := savedPlanApplyCommand(o, "spec", result); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%v: got %v want %v", tc.args, got, tc.want)
		}
		text := renderHumanSuccess(tc.args[0], o, "spec", result)
		if !strings.Contains(text, formatArgv(tc.want)) {
			t.Fatalf("presenter lost executable command: %s", text)
		}
	}
}

func TestScaffoldQueriesReportObservedOutcome(t *testing.T) {
	for _, tc := range []struct{ command, outcome, title string }{
		{"catalog", "", "查询完成"}, {"verify", "complete", "输出完整"},
		{"verify", "not-applied", "尚未应用"}, {"verify", "incomplete", "输出不完整"}, {"verify", "unknown", "状态待确认"},
	} {
		o := options{args: []string{"scaffold", tc.command}, values: map[string]string{}}
		text := renderHumanSuccess("scaffold", o, "", map[string]any{"outcome": tc.outcome})
		if !strings.Contains(strings.SplitN(text, "\n", 2)[0], tc.title) || strings.Contains(text, "脚手架已生成") {
			t.Fatalf("%s/%s misreported: %s", tc.command, tc.outcome, text)
		}
	}
}
