package gitops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePolicy(t *testing.T, body string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(PolicyRef))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func defaultPolicy(t *testing.T) Policy {
	t.Helper()
	p := DefaultPolicy()
	if err := p.compile(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultPolicyRules(t *testing.T) {
	t.Parallel()
	p := defaultPolicy(t)
	for id, want := range map[string]bool{"FEAT-1": true, "A1-20": true, "feat-1": false, "FEAT-": false, "FEAT-1 ": false, "#12": false} {
		if p.TicketValid(id) != want {
			t.Errorf("TicketValid(%q) != %v", id, want)
		}
	}
	for name, want := range map[string]bool{"feat/x": true, "fix/a.b_c-1": true, "main": false, "feature/x": false, "feat/a/b": false} {
		if p.BranchAllowed(name) != want {
			t.Errorf("BranchAllowed(%q) != %v", name, want)
		}
	}
	if !p.Protected("main") || p.Protected("feat/x") {
		t.Error("默认只保护 main")
	}
}

func TestLoadPolicy(t *testing.T) {
	t.Parallel()
	missing, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := LoadPolicy(missing)
	if err != nil || p.Source != "default" || !p.TicketValid("X-1") {
		t.Fatalf("缺少策略文件时应使用默认值: %+v, %v", p, err)
	}

	p, err = LoadPolicy(writePolicy(t, "schema_version: 1\nticket_pattern: '^#[0-9]+$'\nprotected_branches: [main, release]\n"))
	if err != nil {
		t.Fatalf("合法策略被拒绝: %v", err)
	}
	if p.Source != PolicyRef || !p.TicketValid("#12") || p.TicketValid("FEAT-1") || !p.Protected("release") {
		t.Errorf("策略字段未生效: %+v", p)
	}

	for name, body := range map[string]string{
		"缺少版本":   "ticket_trailer: Issue\n",
		"未知字段":   "schema_version: 1\nextra: 1\n",
		"版本错误":   "schema_version: 2\n",
		"非法正则":   "schema_version: 1\nbranch_pattern: '('\n",
		"非法分支名":  "schema_version: 1\ndefault_branch: '-x'\n",
		"非法尾注名":  "schema_version: 1\nticket_trailer: 'has space'\n",
		"空保护列表项": "schema_version: 1\nprotected_branches: ['']\n",
		"签名类型错误": "schema_version: 1\nrequire_signed: 'yes'\n",
		"顶层非对象":  "- a\n",
	} {
		if _, err := LoadPolicy(writePolicy(t, body)); err == nil {
			t.Errorf("%s: 应拒绝而不是忽略", name)
		}
	}
}

func TestAuditFindings(t *testing.T) {
	t.Parallel()
	p := defaultPolicy(t)
	p.RequireSigned = true
	commits := []Commit{
		{SHA: "a", Parents: []string{"p"}, Author: "Ann", Email: "ann@x", Subject: "ok", Tickets: []string{"FEAT-1"}, Reviewers: []string{"Bob <bob@x>"}, Signature: "G"},
		{SHA: "b", Parents: []string{"p"}, Author: "Ann", Email: "ann@x", Subject: "no ticket", Tickets: []string{"feat-1"}, Signature: "G"},
		{SHA: "c", Parents: []string{"p"}, Author: "Ann", Email: "ann@x", Subject: "self", Tickets: []string{"FEAT-2"}, Reviewers: []string{"Ann Other <ann@x>"}, Signature: "U"},
		{SHA: "d", Parents: []string{"p"}, Author: "Ann", Email: "ann@x", Subject: "unsigned", Tickets: []string{"FEAT-3"}, Signature: "N"},
		{SHA: "m", Parents: []string{"p", "q"}, Subject: "merge"},
	}
	var got []string
	for _, f := range Audit(p, commits, false) {
		got = append(got, f.SHA+":"+f.Rule)
	}
	if want := "b:ticket-trailer c:self-review d:unsigned"; strings.Join(got, " ") != want {
		t.Errorf("Audit = %v, want %s", got, want)
	}
	direct := Audit(defaultPolicy(t), commits[:1], true)
	if len(direct) != 1 || direct[0].Rule != "direct-commit" {
		t.Errorf("保护分支一级历史的非合并提交应报告 direct-commit: %+v", direct)
	}
}

func TestChangelogGroupsByTicket(t *testing.T) {
	t.Parallel()
	groups := Changelog([]Commit{
		{SHA: "1", Tickets: []string{"B-1"}},
		{SHA: "2"},
		{SHA: "3", Tickets: []string{"A-1", "B-1"}},
	})
	var got []string
	for _, g := range groups {
		var shas []string
		for _, c := range g.Commits {
			shas = append(shas, c.SHA)
		}
		got = append(got, g.Ticket+"="+strings.Join(shas, ","))
	}
	if want := "B-1=1,3 A-1=3 unlinked=2"; strings.Join(got, " ") != want {
		t.Errorf("Changelog = %v, want %s", got, want)
	}
	if len(Changelog(nil)) != 0 {
		t.Error("空输入不应产生 unlinked 分组")
	}
	if v := values(" A-1 \x1d\x1dB-2"); strings.Join(v, ",") != "A-1,B-2" {
		t.Errorf("values = %v", v)
	}
}

func TestVerifyMessage(t *testing.T) {
	t.Parallel()
	p := defaultPolicy(t)
	dir := t.TempDir()
	check := func(body string) (map[string]any, error) {
		file := filepath.Join(dir, "msg")
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return VerifyMessage(p, file)
	}
	if r, err := check("feat: x\n\nbody\n\nTicket: FEAT-9\n# comment\n"); err != nil || r["ticket"] != "FEAT-9" {
		t.Errorf("有效 trailer 应通过: %v, %v", r, err)
	}
	if r, err := check("fix: y\r\n\r\nticket: OPS-2\r\n"); err != nil || r["ticket"] != "OPS-2" {
		t.Errorf("trailer 名称不区分大小写且接受 CRLF: %v, %v", r, err)
	}
	if r, err := check("Merge branch 'x'\n"); err != nil || r["status"] != "skipped" {
		t.Errorf("合并提交应跳过: %v, %v", r, err)
	}
	for name, body := range map[string]string{
		"缺少 trailer":   "feat: x\n\nbody\n",
		"只有标题行":        "Ticket: FEAT-1\n",
		"trailer 不在末段": "feat: x\n\nTicket: FEAT-1\n\nmore text\n",
		"格式不符":         "feat: x\n\nTicket: feat-1\n",
		"只有注释":         "# only comment\n",
	} {
		if _, err := check(body); err == nil {
			t.Errorf("%s: 应拒绝", name)
		}
	}
	if _, err := VerifyMessage(p, filepath.Join(dir, "missing")); err == nil {
		t.Error("不存在的文件应拒绝")
	}
}
