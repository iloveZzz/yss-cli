package cli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCLICompletionScripts(t *testing.T) {
	for shell, marker := range map[string]string{"bash": "bash completion V2 for yss", "zsh": "#compdef yss", "fish": "fish completion for yss", "powershell": "powershell completion for yss"} {
		code, out, errOut := captureCommand(t, context.Background(), []string{"completion", shell}, false)
		if code != 0 || errOut != "" || !strings.Contains(out, marker) || strings.Contains(out, `"outputVersion"`) {
			t.Errorf("%s script: %d %s %s", shell, code, out, errOut)
		}
	}
	code, out, errOut := captureCommand(t, context.Background(), []string{"completion", "unknown"}, false)
	if code != 2 || out != "" || !strings.Contains(errOut, "ARGUMENT") {
		t.Fatalf("unknown shell: %d %s %s", code, out, errOut)
	}
}

func completionResult(t *testing.T, args ...string) (string, int) {
	t.Helper()
	code, out, errOut := captureCommand(t, context.Background(), args, false)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := lines[len(lines)-1]
	if code != 0 || errOut != "" || !strings.HasPrefix(last, ":") || strings.Contains(out, `"outputVersion"`) || strings.Contains(out, "前置条件:") {
		t.Fatalf("raw completion: %v: %d %s %s", args, code, out, errOut)
	}
	directive, err := strconv.Atoi(strings.TrimPrefix(last, ":"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines[:len(lines)-1], "\n"), directive
}

func TestCLICompletionProtocol(t *testing.T) {
	for _, entry := range []string{"__complete", "__completeNoDesc"} {
		for _, word := range []string{"", "--help", "--json"} {
			completionResult(t, entry, "version", word)
		}
	}
	out, directive := completionResult(t, "__completeNoDesc", "init", "--profile", "")
	if directive != 4 || !strings.Contains(out, "spec") || !strings.Contains(out, "design") || !strings.Contains(out, "backend") {
		t.Fatalf("profile choices: %d %s", directive, out)
	}
	_, directive = completionResult(t, "__completeNoDesc", "init", "--root", "")
	if directive != 16 {
		t.Fatalf("directory completion directive=%d", directive)
	}
	_, directive = completionResult(t, "__completeNoDesc", "check", "")
	if directive != 0 {
		t.Fatalf("file positional directive=%d", directive)
	}
	_, directive = completionResult(t, "__completeNoDesc", "explain", "")
	if directive != 4 {
		t.Fatalf("non-path positional directive=%d", directive)
	}
	out, _ = completionResult(t, "__completeNoDesc", "contract", "verify", "--kind", "task", "--")
	if !strings.Contains(out, "--history") || strings.Contains(out, "--mode") || strings.Contains(out, "--review-ref") {
		t.Fatalf("kind-specific flags: %s", out)
	}
}

func TestCLICompletionHasNoEffects(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("PATH", root)
	if err = os.WriteFile("yss-project.yaml", []byte("invalid: [\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"init", "upgrade", "mcp"} {
		completionResult(t, "__complete", cmd, "")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile("yss-project.yaml")
	if len(entries) != 1 || entries[0].Name() != "yss-project.yaml" || string(raw) != "invalid: [\n" {
		t.Fatal("completion changed project")
	}
}

func TestCLICompletionSubcommandsAfterFlags(t *testing.T) {
	for _, entry := range []string{"__complete", "__completeNoDesc"} {
		for _, tc := range []struct {
			args []string
			want string
		}{
			{[]string{"git", "--root", "/private/tmp", ""}, "policy"},
			{[]string{"git", "branch", "--root", "/private/tmp", "st"}, "start"},
			{[]string{"git", "policy", "--root", "/private/tmp", ""}, "set"},
		} {
			out, directive := completionResult(t, append([]string{entry}, tc.args...)...)
			if directive != 4 || !strings.Contains(out, tc.want) {
				t.Errorf("subcommands missing after flags: %v: %d %s", tc.args, directive, out)
			}
		}
		out, directive := completionResult(t, entry, "git", "policy", "--root", "/private/tmp", "--", "")
		if directive != 4 || out != "" {
			t.Errorf("terminator promoted a subcommand: %d %s", directive, out)
		}
	}
}
