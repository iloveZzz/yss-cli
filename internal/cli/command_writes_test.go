package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/gitops"
	"github.com/iloveZzz/yss-cli/internal/updater"
)

func TestCLIScaffoldInputGuards(t *testing.T) {
	out := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{
		{"scaffold", "ddd", "--force=true"},
		{"scaffold", "ddd", "--standalone=true", "--contract-file=x"},
		{"scaffold", "mvc", "--project-name=a", "--project-name=b"},
	} {
		args = append(args, "--output-dir", out, "--json")
		code, raw, errOut := captureCommand(t, context.Background(), args, false)
		if code != 2 || errOut != "" || commandEnvelope(t, raw)["code"] != "ARGUMENT" {
			t.Fatalf("write guard: %d %s %s", code, raw, errOut)
		}
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("rejected scaffold created output")
	}
}

func TestCLIGitWriteGuards(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.name=T", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "initial"}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	for _, args := range [][]string{{"branch", "start"}, {"hooks", "install"}, {"release", "tag"}, {"policy", "set"}, {"commit"}, {"push"}} {
		code, out, _ := captureCommand(t, context.Background(), append(append([]string{"git"}, args...), "--root", root, "--json"), false)
		if code != 1 || commandEnvelope(t, out)["code"] != "PLAN_REQUIRED" {
			t.Fatalf("missing plan %v: %d %s", args, code, out)
		}
	}
	for _, args := range [][]string{{"branch"}, {"branch", "wrong"}, {"hooks", "wrong"}, {"release", "tag", "extra"}, {"policy", "wrong"}} {
		code, out, _ := captureCommand(t, context.Background(), append(append([]string{"git"}, args...), "--root", root, "--json"), false)
		if code != 2 || commandEnvelope(t, out)["code"] != "ARGUMENT" {
			t.Fatalf("action arity %v: %d %s", args, code, out)
		}
	}
	planFile := filepath.Join(t.TempDir(), "policy.json")
	for _, args := range [][]string{
		{"git", "policy", "set", "--key", "default_branch", "--value", "develop", "--plan", "--out", planFile},
		{"git", "policy", "set", "--apply", "--plan-file", planFile},
	} {
		code, out, _ := captureCommand(t, context.Background(), append(args, "--root", root, "--json"), false)
		if code != 0 {
			t.Fatalf("policy plan/apply: %d %s", code, out)
		}
	}
	code, out, _ := captureCommand(t, context.Background(), []string{"git", "policy", "--root", root, "--json"}, false)
	if code != 0 || commandEnvelope(t, out)["result"].(map[string]any)["defaultBranch"] != "develop" {
		t.Fatalf("policy query: %d %s", code, out)
	}
	push := gitops.Plan{SchemaVersion: 1, Kind: gitops.KindPush, Root: root, Params: map[string]string{"remote": "origin"}}
	raw, _ := json.Marshal(push)
	sum := sha256.Sum256(raw)
	push.PlanDigest = hex.EncodeToString(sum[:])
	raw, _ = json.Marshal(push)
	pushFile := filepath.Join(t.TempDir(), "push.json")
	if err = os.WriteFile(pushFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	code, out, _ = captureCommand(t, context.Background(), []string{"git", "push", "--root", root, "--apply", "--plan-file", pushFile, "--json"}, false)
	if code != 1 || commandEnvelope(t, out)["code"] != "CAPABILITY" {
		t.Fatalf("missing remote confirmation: %d %s", code, out)
	}
}

func TestCLIProgramScope(t *testing.T) {
	for _, args := range [][]string{
		{"upgrade", "--to=2.0.0", "--to=2.1.0"}, {"upgrade", "--root=x"},
		{"update", "status", "--artifact=x"}, {"update", "status", "--root=x"},
	} {
		code, out, _ := captureCommand(t, context.Background(), append(args, "--json"), false)
		if code != 2 || commandEnvelope(t, out)["code"] != "ARGUMENT" {
			t.Fatalf("program scope %v: %d %s", args, code, out)
		}
	}
	code, out, _ := captureCommand(t, context.Background(), []string{"update", "apply", "--json"}, false)
	if code != 1 || commandEnvelope(t, out)["code"] != "PLAN_REQUIRED" {
		t.Fatalf("missing plan: %d %s", code, out)
	}
	p := updater.Plan{SchemaVersion: 1, Command: "update", ToolRoot: "/unused/tool-root"}
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	p.Digest = hex.EncodeToString(sum[:])
	raw, _ = json.Marshal(p)
	file := filepath.Join(t.TempDir(), "update.json")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	code, out, _ = captureCommand(t, context.Background(), []string{"update", "apply", "--tool-root", t.TempDir(), "--plan-file", file, "--json"}, false)
	if code != 1 || commandEnvelope(t, out)["code"] != "PLAN" || !strings.Contains(out, "工具根不匹配") {
		t.Fatalf("tool root binding: %d %s", code, out)
	}
}
