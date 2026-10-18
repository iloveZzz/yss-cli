//go:build !yss_nobundle

package cli

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/project"
)

func commandProject(t *testing.T) string {
	t.Helper()
	source := checkProject(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "project")
	if err = filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		ref, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if ref == filepath.Join(".yss", "transactions") || ref == ".work" {
			return filepath.SkipDir
		}
		target := filepath.Join(root, ref)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, info.Mode().Perm())
	}); err != nil {
		t.Fatal(err)
	}
	return root
}

func commandOK(t *testing.T, root string, args ...string) map[string]any {
	t.Helper()
	code, out, errOut := captureCommand(t, context.Background(), append(args, "--root", root, "--json"), false)
	if code != 0 || errOut != "" {
		t.Fatalf("%v: %d %s %s", args, code, out, errOut)
	}
	return commandEnvelope(t, out)["result"].(map[string]any)
}

func TestCLILocalPlanGuards(t *testing.T) {
	root := commandProject(t)
	before, err := os.ReadFile(filepath.Join(root, "yss-project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"sync", "--apply"}, {"skills", "ensure", "yss-domain", "--apply"},
		{"profile", "prepare", "--apply"}, {"lifecycle", "target", "--input", "input.yaml"},
	} {
		code, out, _ := captureCommand(t, context.Background(), append(args, "--root", root, "--json"), false)
		if code != 1 || commandEnvelope(t, out)["code"] != "PLAN_REQUIRED" {
			t.Fatalf("%v: %d %s", args, code, out)
		}
	}
	after, _ := os.ReadFile(filepath.Join(root, "yss-project.yaml"))
	if string(before) != string(after) {
		t.Fatal("missing plan changed identity")
	}
}

func TestCLIParentActionScope(t *testing.T) {
	root := commandProject(t)
	commandOK(t, root, "tracker")
	for _, args := range [][]string{{"skills"}, {"profile"}, {"git"}, {"reading", "render"}, {"reading", "apply-enable"}} {
		code, out, _ := captureCommand(t, context.Background(), append(args, "--root", root, "--json"), false)
		if code != 2 || commandEnvelope(t, out)["code"] != "ARGUMENT" {
			t.Fatalf("parent/action: %v: %d %s", args, code, out)
		}
	}
	code, out, _ := captureCommand(t, context.Background(), []string{"update", "--json"}, false)
	if code != 2 || commandEnvelope(t, out)["code"] != "ARGUMENT" {
		t.Fatalf("update default plan: %d %s", code, out)
	}
}

func TestCLIProjectPlanGuards(t *testing.T) {
	root := commandProject(t)
	planFile := filepath.Join(t.TempDir(), "sync.json")
	commandOK(t, root, "sync", "--plan", "--out", planFile)
	commandOK(t, root, "sync", "--apply", "--plan-file", planFile)
	code, out, _ := captureCommand(t, context.Background(), []string{"sync", "--root", t.TempDir(), "--apply", "--plan-file", planFile, "--json"}, false)
	if code != 1 || commandEnvelope(t, out)["code"] != "PLAN" {
		t.Fatalf("root binding: %d %s", code, out)
	}
	planFile = filepath.Join(t.TempDir(), "fresh.json")
	commandOK(t, root, "sync", "--plan", "--out", planFile)
	p, err := project.ReadPlan(planFile)
	if err != nil {
		t.Fatal(err)
	}
	ref := "CONTEXT.md"
	if _, ok := p.Inputs[ref]; !ok {
		t.Fatal("expected observed CONTEXT.md input")
	}
	file := filepath.Join(root, ref)
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	changed := append(raw, []byte("\n<!-- user change -->\n")...)
	if err = os.WriteFile(file, changed, 0644); err != nil {
		t.Fatal(err)
	}
	code, out, _ = captureCommand(t, context.Background(), []string{"sync", "--root", root, "--apply", "--plan-file", planFile, "--json"}, false)
	if code != 1 || commandEnvelope(t, out)["code"] != "INPUT_DRIFT" {
		t.Fatalf("input drift: %d %s", code, out)
	}
	after, _ := os.ReadFile(file)
	if string(after) != string(changed) {
		t.Fatal("drift guard overwrote user change")
	}
}

func TestCLIExportNoOverwrite(t *testing.T) {
	root := commandProject(t)
	code, out, _ := captureCommand(t, context.Background(), []string{"bundle", "export", "--profile", "spec", "--json"}, false)
	if code != 2 || commandEnvelope(t, out)["code"] != "ARGUMENT" {
		t.Fatalf("missing export out: %d %s", code, out)
	}
	writeCheckFile(t, root, ".work/command-export/notes.md", "# notes\n")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	html := filepath.Join(dir, "trace.html")
	commandOK(t, root, "trace", "--feature", "command-export")
	if _, err = os.Stat(html); !os.IsNotExist(err) {
		t.Fatal("query wrote HTML")
	}
	if err = os.WriteFile(html, []byte("user HTML"), 0600); err != nil {
		t.Fatal(err)
	}
	code, out, _ = captureCommand(t, context.Background(), []string{"trace", "--root", root, "--feature", "command-export", "--html", html, "--json"}, false)
	if code != 1 || commandEnvelope(t, out)["code"] != "EXISTS" {
		t.Fatalf("HTML overwrite: %d %s", code, out)
	}
	raw, _ := os.ReadFile(html)
	if string(raw) != "user HTML" {
		t.Fatal("HTML overwritten")
	}
}

func TestCLILocalWriteModes(t *testing.T) {
	root := commandProject(t)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	begin := commandOK(t, root, "runtime", "begin", "--home", home, "--kind", "command")
	id, token := begin["id"].(string), begin["token"].(string)
	commandOK(t, root, "runtime", "event", "--home", home, "--id", id, "--token", token, "--type", "note", "--value", `"hello"`)
	commandOK(t, root, "runtime", "complete", "--home", home, "--id", id, "--token", token, "--status", "passed", "--exit-code", "0")
	inspect := commandOK(t, root, "runtime", "inspect", "--home", home)
	runs := inspect["runs"].([]any)
	if len(runs) != 1 || runs[0].(map[string]any)["status"] != "passed" || runs[0].(map[string]any)["id"] != id {
		t.Fatalf("runtime state: %v", inspect)
	}
	writeCheckFile(t, root, "transport/a.txt", "payload\n")
	zipFile := filepath.Join(home, "a.zip")
	commandOK(t, root, "archive", "pack", "--source", "transport", "--output", zipFile)
	raw, err := os.ReadFile(zipFile)
	if err != nil {
		t.Fatal(err)
	}
	writeCheckFile(t, root, "transport.zip", string(raw))
	commandOK(t, root, "archive", "verify", "transport.zip")
	unpack := filepath.Join(home, "unpacked")
	commandOK(t, root, "archive", "unpack", "transport.zip", "--output", unpack)
	got, err := os.ReadFile(filepath.Join(unpack, "a.txt"))
	if err != nil || string(got) != "payload\n" {
		t.Fatalf("archive content: %q %v", got, err)
	}
	code, _, _ := captureCommand(t, context.Background(), []string{"archive", "unpack", "transport.zip", "--output", unpack, "--root", root, "--json"}, false)
	if code == 0 {
		t.Fatal("existing unpack directory accepted")
	}
	ref := ".work/command-link/parent-ticket.md"
	writeCheckFile(t, root, ref, "# Command link\n\nStatus: backlog\n\nBody unchanged.\n")
	before, _ := os.ReadFile(filepath.Join(root, ref))
	commandOK(t, root, "tracker", "link", "--ticket", ref, "--issue", "example/repo#1")
	preview, _ := os.ReadFile(filepath.Join(root, ref))
	if string(preview) != string(before) {
		t.Fatal("preview changed Ticket")
	}
	commandOK(t, root, "tracker", "link", "--ticket", ref, "--issue", "example/repo#1", "--apply")
	after, _ := os.ReadFile(filepath.Join(root, ref))
	if string(after) != strings.Replace(string(before), "Status: backlog", "Status: backlog\nTracker-Link: github:example/repo#1", 1) {
		t.Fatalf("unexpected Ticket write: %s", after)
	}
}

func TestCLITrackerRemoteGuards(t *testing.T) {
	root := commandProject(t)
	writeCheckFile(t, root, ".template-spec/agents/issue-tracker.md", "---\ntracker:\n  lifecycle_tracking_version: 1\n  platform: local-markdown\n  root: .work\n  remote_mirror: true\n---\n# tracker\n")
	t.Setenv("GH_TOKEN", "test-token")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_API_URL", "http://127.0.0.1:1")
	for _, tc := range []struct {
		args []string
		code string
		exit int
	}{
		{[]string{"tracker", "sync"}, "PLAN_REQUIRED", 1},
		{[]string{"tracker", "sync", "--confirm-remote", "github"}, "ARGUMENT", 2},
		{[]string{"tracker", "sync", "--apply", "--plan-file", "unused-plan.json"}, "CAPABILITY", 1},
	} {
		code, out, _ := captureCommand(t, context.Background(), append(tc.args, "--root", root, "--json"), false)
		if code != tc.exit || commandEnvelope(t, out)["code"] != tc.code {
			t.Fatalf("Tracker remote guard: %d %s", code, out)
		}
	}
}
