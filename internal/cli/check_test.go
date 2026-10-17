package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

// 不依赖内嵌 Bundle：只覆盖资产识别前的选择、跳过、解析失败与结论汇总。

func writeCheckFile(t *testing.T, root, ref, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(ref))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckFileSkipsWithoutRunningVerifiers(t *testing.T) {
	root := t.TempDir()
	writeCheckFile(t, root, ".template-spec/templates/approval-record-template.yaml", "gate_id: g\ndecision: approved\nactor_kind: human\n")
	writeCheckFile(t, root, ".work/f/notes.md", "# notes\n")
	writeCheckFile(t, root, ".work/f/other.yaml", "name: other\n")
	writeCheckFile(t, root, ".work/f/broken.yaml", "a: [\n")
	cases := []struct{ ref, status, reason string }{
		{".template-spec/templates/approval-record-template.yaml", "skipped", "模板"},
		{".work/f/notes.md", "skipped", "非 YAML/JSON"},
		{".work/f/other.yaml", "skipped", "未识别"},
		{".work/f/missing.yaml", "skipped", "已删除"},
		{".work/f/broken.yaml", "failed", ""},
	}
	for _, c := range cases {
		item := checkFile(context.Background(), root, c.ref, "")
		if item.Status != c.status || !strings.Contains(item.Reason, c.reason) {
			t.Errorf("%s: status=%s reason=%q", c.ref, item.Status, item.Reason)
		}
	}
	broken := checkFile(context.Background(), root, ".work/f/broken.yaml", "")
	if len(broken.Findings) != 1 || broken.Findings[0].Code != "PARSE" || broken.Findings[0].Fix == "" || broken.Findings[0].Command == "" {
		t.Fatalf("解析失败须给出统一格式的发现: %+v", broken.Findings)
	}
}

func TestCheckUserDecisionWithoutCheckpointIsNotEvaluated(t *testing.T) {
	root := t.TempDir()
	writeCheckFile(t, root, ".work/f/decision.yaml", "schema_version: 1\nkind: user-decision\n")
	item := checkFile(context.Background(), root, ".work/f/decision.yaml", "")
	if item.Status != "not-evaluated" || item.AssetKind != "user-decision" {
		t.Fatalf("缺上下文必须未评估而不是运行或通过: %+v", item)
	}
	if !strings.Contains(item.Command, "--checkpoint '<当前checkpoint>'") {
		t.Fatalf("须给出补充上下文后的完整命令: %s", item.Command)
	}
}

func TestCheckReportNeverPassesWithGaps(t *testing.T) {
	for _, c := range []struct {
		statuses []string
		want     string
		fails    bool
	}{
		{[]string{"passed", "skipped"}, "passed", false},
		{[]string{"passed", "not-evaluated"}, "incomplete", false},
		{[]string{"skipped"}, "no-assets", false},
		{[]string{"passed", "failed", "not-evaluated"}, "failed", true},
	} {
		report := newCheckReport("yss-check", nil)
		for _, s := range c.statuses {
			report.add(checkItem{Status: s})
		}
		_, err := report.finish()
		if report.Status != c.want || (err != nil) != c.fails {
			t.Errorf("%v: status=%s err=%v", c.statuses, report.Status, err)
		}
	}
}

func TestCheckFailureCarriesCodeAndFindings(t *testing.T) {
	report := newCheckReport("yss-check", nil)
	report.add(checkItem{Status: "failed", Findings: []checkFinding{{Code: "SCHEMA", File: "a.yaml", Pointer: "/x", Fix: "修 x", Command: "yss evidence verify"}}})
	_, err := report.finish()
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != "CHECK_FAILED" {
		t.Fatalf("失败码应为 CHECK_FAILED: %v", err)
	}
	for _, want := range []string{"a.yaml#/x", "SCHEMA", "修 x", "复现：yss evidence verify"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("失败说明缺少 %q: %s", want, err.Error())
		}
	}
}

func TestCheckFixPrefersSpecificGuidance(t *testing.T) {
	message := "schema.json: [{SCHEMA_VALIDATION /context_snapshot/document_digest file:///x}]"
	if got := schemaPointer(message); got != "/context_snapshot/document_digest" {
		t.Fatalf("pointer=%q", got)
	}
	if got := checkFix("SCHEMA", message, "修复当前输入、能力或证据后重新校验"); !strings.Contains(got, "/context_snapshot/document_digest") {
		t.Fatalf("通用 recovery 应替换为按字段的建议: %s", got)
	}
	if got := checkFix("SCHEMA", message, "改用 v5 Schema"); got != "改用 v5 Schema" {
		t.Fatalf("校验器的具体 recovery 优先: %s", got)
	}
	if got := checkFix("GATE_EVIDENCE", "", ""); !strings.Contains(got, "yss explain") {
		t.Fatalf("门禁失败应指向门禁定义: %s", got)
	}
}

func TestCheckArguments(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{
		{"check"},
		{"check", "a.yaml", "--changed"},
		{"check", "--base", "HEAD"},
		{"verify", "--gate", "plan-approved"},
	} {
		code := Run(context.Background(), append(args, "--root", root, "--json"), &strings.Builder{}, &strings.Builder{})
		if code != 2 {
			t.Errorf("%v: 参数错误应返回 2，得到 %d", args, code)
		}
	}
	if _, err := checkRef(root, "../outside.yaml"); err == nil {
		t.Fatal("项目根外的路径必须拒绝")
	}
}

func TestCheckChangedSelectsWorkingTreeAndBaseRange(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=T", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	writeCheckFile(t, root, "base.md", "base\n")
	git("add", ".")
	git("commit", "-qm", "base")
	writeCheckFile(t, root, "committed.md", "c\n")
	git("add", ".")
	git("commit", "-qm", "next")
	writeCheckFile(t, root, "dirty.md", "d\n")
	writeCheckFile(t, root, "new/dir/asset.yaml", "a: 1\n") // 未跟踪目录须逐文件列出

	refs, _, err := checkSelection(context.Background(), root, options{args: []string{"check"}, values: map[string]string{"changed": "true"}})
	if err != nil || strings.Join(refs, ",") != "dirty.md,new/dir/asset.yaml" {
		t.Fatalf("工作树改动: %v %v", refs, err)
	}
	refs, _, err = checkSelection(context.Background(), root, options{args: []string{"check"}, values: map[string]string{"changed": "true", "base": "HEAD~1"}})
	if err != nil || strings.Join(refs, ",") != "committed.md,dirty.md,new/dir/asset.yaml" {
		t.Fatalf("base..HEAD 并入: %v %v", refs, err)
	}
	if _, _, err = checkSelection(context.Background(), root, options{args: []string{"check"}, values: map[string]string{"changed": "true", "base": "a..b"}}); err == nil {
		t.Fatal("非法 base 必须拒绝")
	}
}
