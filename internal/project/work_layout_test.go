package project

import (
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func workNativeFixture(t *testing.T) string {
	t.Helper()
	root := freshRoot(t)
	p, e := Build(root, "spec", "init", map[string]string{"projectName": "工作包迁移项目"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Apply(p); e != nil {
		t.Fatal(e)
	}
	tracker := filepath.Join(root, ".template-spec/agents/issue-tracker.md")
	b, e := os.ReadFile(tracker)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(tracker, []byte(strings.Replace(string(b), "root: .work", "root: docs/.scratch", 1)), 0644); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"init", "--quiet"}, {"add", "CONTEXT.md"}} {
		if out, e := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); e != nil {
			t.Fatalf("%s %v", out, e)
		}
	}
	workFile(t, root, "business.txt", "无关人工资产")
	return root
}

func workFile(t *testing.T, root, ref, text string) {
	t.Helper()
	p := filepath.Join(root, ref)
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(text), 0640); e != nil {
		t.Fatal(e)
	}
}
func TestWorkLayoutMigrationRoundtrip(t *testing.T) {
	root := workNativeFixture(t)
	workFile(t, root, "docs/.scratch/report/spec.md", "# 导出\n验收：下载报表。\n")
	workFile(t, root, "docs/.scratch/report/parent-ticket.md", "# 导出\nStatus: needs-info\n[规格](docs/.scratch/report/spec.md)\n")
	workFile(t, root, "docs/.scratch/report/verification/historic.log", "original output docs/.scratch/report/spec.md\n")
	workFile(t, root, ".yss/asset-transactions/history.json", `{"original_ref":"docs/.scratch/report/spec.md","receipt":"historical"}`)
	for ref, mode := range map[string]os.FileMode{"docs/.scratch": 0700, "docs/.scratch/report": 0750} {
		if e := os.Chmod(filepath.Join(root, ref), mode); e != nil {
			t.Fatal(e)
		}
	}
	gitBefore, e := workGitIdentity(root)
	if e != nil {
		t.Fatal(e)
	}
	p, e := BuildWorkLayout(root, "")
	if e != nil {
		t.Fatal(e)
	}
	if !p.ReadyToApply {
		t.Fatalf("blocked: %+v", p.Blockers)
	}
	if _, e = Apply(p); e != nil {
		t.Fatal(e)
	}
	for ref, mode := range map[string]os.FileMode{".work": 0700, ".work/report": 0750, ".work/report/spec.md": 0640} {
		st, e := os.Stat(filepath.Join(root, ref))
		if e != nil || st.Mode().Perm() != mode {
			t.Fatalf("permission not retained %s: %v", ref, e)
		}
	}
	gitAfter, e := workGitIdentity(root)
	if e != nil || gitAfter != gitBefore {
		t.Fatal("migration changed Git identity or index")
	}
	b, e := os.ReadFile(filepath.Join(root, ".work/report/parent-ticket.md"))
	if e != nil || !strings.Contains(string(b), ".work/report/spec.md") {
		t.Fatalf("%s %v", b, e)
	}
	if _, e = os.Stat(filepath.Join(root, "docs/.scratch/report/spec.md")); !os.IsNotExist(e) {
		t.Fatal("source not retired")
	}
	for ref, want := range map[string]string{".work/report/verification/historic.log": "original output docs/.scratch/report/spec.md\n", ".yss/asset-transactions/history.json": `{"original_ref":"docs/.scratch/report/spec.md","receipt":"historical"}`} {
		got, e := os.ReadFile(filepath.Join(root, ref))
		if e != nil || string(got) != want {
			t.Fatalf("historical bytes changed %s: %s %v", ref, got, e)
		}
	}
	again, e := BuildWorkLayout(root, "")
	if e != nil || len(again.Changes) != 0 {
		t.Fatalf("not idempotent: %+v %v", again, e)
	}
	workFile(t, root, ".work/report/spec.md", "manual newer edit\n")
	if _, e := Apply(again); e == nil {
		t.Fatal("stale no-op plan accepted input drift")
	}
	workFile(t, root, ".work/report/spec.md", "# 导出\n验收：下载报表。\n")
	if e := os.Chmod(filepath.Join(root, "docs/.scratch/report"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := transaction.RollbackKind(root, "migrate"); e == nil {
		t.Fatal("source directory permission edit was overwritten")
	}
	if _, e := os.Stat(filepath.Join(root, ".work/report/spec.md")); e != nil {
		t.Fatal("failed rollback changed candidate")
	}
	if e := os.Chmod(filepath.Join(root, "docs/.scratch/report"), 0750); e != nil {
		t.Fatal(e)
	}
	if _, e = transaction.RollbackKind(root, "migrate"); e != nil {
		t.Fatal(e)
	}
	if st, e := os.Stat(filepath.Join(root, "docs/.scratch/report")); e != nil || st.Mode().Perm() != 0750 {
		t.Fatal("source directory permissions changed")
	}
	if current, e := workGitIdentity(root); e != nil || current != gitBefore {
		t.Fatal("rollback changed index")
	}
	b, e = os.ReadFile(filepath.Join(root, "docs/.scratch/report/spec.md"))
	if e != nil || string(b) != "# 导出\n验收：下载报表。\n" {
		t.Fatalf("restore: %s %v", b, e)
	}
}

func TestWorkLayoutMigrationValidatesReferencesOutsideWorkRoot(t *testing.T) {
	root := workNativeFixture(t)
	workFile(t, root, "docs/.scratch/report/spec.md", "# draft\n")
	workFile(t, root, "docs/guide.md", "[dangling](../.work/report/missing.md)\n")
	p, e := BuildWorkLayout(root, "")
	if e != nil {
		t.Fatal(e)
	}
	if p.ReadyToApply {
		t.Fatal("outside-work current reference escaped closure")
	}
	if _, e := Apply(p); e == nil {
		t.Fatal("dangling reference plan applied")
	}
}
func TestWorkLayoutMigrationBlocksApprovalAndConflicts(t *testing.T) {
	root := workNativeFixture(t)
	workFile(t, root, "docs/.scratch/report/spec.md", "# 已批准规格\n")
	workFile(t, root, "docs/.scratch/report/gates/approval.json", `{"gate_id":"gate.plan-approved","decision":"approved","subject_ref":"docs/.scratch/report/spec.md"}`)
	p, e := BuildWorkLayout(root, "")
	if e != nil {
		t.Fatal(e)
	}
	if p.ReadyToApply || len(p.Blockers) == 0 {
		t.Fatal("approval inherited")
	}
	if _, e = Apply(p); e == nil {
		t.Fatal("blocked plan applied")
	}
	workFile(t, root, ".work/report/spec.md", "different")
	p, e = BuildWorkLayout(root, "")
	if e != nil {
		t.Fatal(e)
	}
	if p.ReadyToApply {
		t.Fatal("conflict accepted")
	}
}
func TestWorkLayoutMigrationRejectsNewDependencyAndConcurrentRollback(t *testing.T) {
	root := workNativeFixture(t)
	workFile(t, root, "docs/.scratch/report/spec.md", "# 原规格\n")
	p, e := BuildWorkLayout(root, "")
	if e != nil {
		t.Fatal(e)
	}
	workFile(t, root, "docs/.scratch/report/new.md", "new")
	if _, e = Apply(p); e == nil {
		t.Fatal("new input not detected")
	}
	p, e = BuildWorkLayout(root, "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Apply(p); e != nil {
		t.Fatal(e)
	}
	workFile(t, root, ".work/report/spec.md", "人工后续修改")
	if _, e = transaction.RollbackKind(root, "migrate"); e == nil {
		t.Fatal("rollback overwrote edit")
	}
}
