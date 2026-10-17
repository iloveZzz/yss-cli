package trace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

func write(t *testing.T, root, ref, content string) string {
	t.Helper()
	path := filepath.Join(root, ref)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// fixture：AC-1 有切片和验证；AC-2 只在业务票里；spec 依据在冻结后被改动。
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, ".template-spec/agents/issue-tracker.md", "---\ntracker:\n  platform: local-markdown\n  root: .work\n  remote_mirror: false\n---\n")
	frozen := write(t, root, ".work/feat/spec.md", "v1\n")
	write(t, root, ".work/feat/spec.md", "v2\n")
	design := write(t, root, ".work/feat/design.md", "d\n")
	write(t, root, ".work/feat/business-tickets/BT-1.md", "---\nschema_version: 1\nkind: business-ticket\nid: BT-1\nrequirement_refs: [FR-1]\nacceptance_refs: [AC-1, AC-2]\n---\n# 提交申请\n")
	write(t, root, ".work/feat/issues/01-submit.md", "---\nkind: vertical-slice-ticket\nstatus: ready-for-human\nbusiness_ticket_refs: [BT-1]\nacceptance_refs: [AC-1]\n---\n## 验收\n")
	write(t, root, ".work/feat/slices/SLICE-1.yaml", "schema_version: 3\ncontract_id: c1\ncontract_version: v1\nslice_id: SLICE-1\nbasis:\n  spec: {ref: .work/feat/spec.md, digest: "+frozen+"}\n  design: {ref: .work/feat/design.md, digest: "+design+"}\nverification:\n  test: {command: ./mvnw test, cwd: ., expected_evidence: [log], acceptance_refs: [AC-1]}\n")
	write(t, root, ".work/feat/notes.md", "没有 front matter 的笔记\n")
	return root
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=T", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	run("commit", "-q", "--allow-empty", "-m", "base")
	run("tag", "base")
	for _, c := range []struct{ file, msg string }{
		{"a.txt", "feat: submit\n\nTicket: SLICE-1"},
		{"b.txt", "chore: no ticket"},
		{"c.txt", "fix: other\n\nTicket: OTHER-9"},
	} {
		write(t, dir, c.file, c.file)
		run("add", c.file)
		run("commit", "-q", "-m", c.msg)
	}
	return dir
}

func TestTraceMatrixAndGaps(t *testing.T) {
	root := fixture(t)
	r, err := Build(context.Background(), root, Options{Feature: "feat", ImplementationRoot: gitRepo(t), Since: "base"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Matrix) != 2 || r.Status != "gaps-found" {
		t.Fatalf("矩阵: %+v status=%s", r.Matrix, r.Status)
	}
	ac1 := r.Matrix[0]
	if ac1.Acceptance != "AC-1" || strings.Join(ac1.Slices, ",") != "SLICE-1" || strings.Join(ac1.Verifications, ",") != "SLICE-1/test" || strings.Join(ac1.Requirements, ",") != "FR-1" || len(ac1.Commits) != 1 {
		t.Errorf("AC-1 行: %+v", ac1)
	}
	g := r.Gaps
	if strings.Join(g.AcceptanceWithoutSlice, ",") != "AC-2" || strings.Join(g.AcceptanceWithoutTest, ",") != "AC-2" {
		t.Errorf("AC-2 应同时缺切片与验证: %+v", g)
	}
	if len(g.CommitsWithoutTicket) != 1 || len(g.CommitsUnmapped) != 1 {
		t.Errorf("提交缺口: %+v", g)
	}
	if len(g.BasisChanged) != 1 || g.BasisChanged[0].Key != "spec" {
		t.Errorf("只有被改动的 spec 依据应报告漂移: %+v", g.BasisChanged)
	}
	var mapped Commit
	for _, c := range r.Commits {
		if len(c.Slices) > 0 {
			mapped = c
		}
	}
	if strings.Join(mapped.Files, ",") != "a.txt" {
		t.Errorf("提交应带改动文件: %+v", mapped)
	}
	if len(r.SliceTickets) != 1 || len(r.BusinessTickets) != 1 {
		t.Errorf("资产识别: %d %d", len(r.SliceTickets), len(r.BusinessTickets))
	}
}

func TestTraceWithoutCommitsAndHTML(t *testing.T) {
	root := fixture(t)
	r, err := Build(context.Background(), root, Options{Feature: "feat"})
	if err != nil || len(r.Commits) != 0 || r.CommitRange != nil {
		t.Fatalf("不给实现仓时不读取提交: %v %+v", err, r)
	}
	out := filepath.Join(t.TempDir(), "trace.html")
	if err = WriteHTML(root, out, r); err != nil {
		t.Fatal(err)
	}
	page, _ := os.ReadFile(out)
	for _, want := range []string{"<title>追溯矩阵 feat</title>", "AC-2", "无切片", "prefers-color-scheme"} {
		if !strings.Contains(string(page), want) {
			t.Errorf("HTML 缺少 %q", want)
		}
	}
	if err = WriteHTML(root, out, r); err == nil {
		t.Error("不覆盖已有报告")
	}
	if err = WriteHTML(root, filepath.Join(root, "trace.html"), r); err == nil {
		t.Error("报告不得写入项目内")
	}
}

func TestTraceArgumentsAndStrictError(t *testing.T) {
	root := fixture(t)
	for _, opts := range []Options{{Feature: "../etc"}, {Feature: "missing"}, {Feature: "feat", ImplementationRoot: gitRepo(t)}} {
		if _, err := Build(context.Background(), root, opts); err == nil {
			t.Errorf("应拒绝 %+v", opts)
		}
	}
	r, _ := Build(context.Background(), root, Options{Feature: "feat"})
	var de *domain.Error
	if err := error(&GapsFound{Report: r}); !errors.As(err, &de) || de.Code != "TRACE_GAPS" || de.Exit != 1 {
		t.Errorf("strict 失败码: %v", err)
	}
}
