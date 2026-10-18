package cli

import (
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCLIOptionTerminator(t *testing.T) {
	for _, args := range [][]string{{"version", "--", "--help", "--json"}, {"git", "--", "policy"}} {
		code, out, errOut := captureCommand(t, context.Background(), args, false)
		if code != 2 || out != "" || !strings.Contains(errOut, "ARGUMENT") {
			t.Errorf("terminator activated a switch or subcommand: %v: %d %s %s", args, code, out, errOut)
		}
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeCheckFile(t, root, "-example.yaml", "name: other\n")
	code, out, errOut := captureCommand(t, context.Background(), []string{"check", "--root", root, "--json", "--", "-example.yaml"}, false)
	if code != 0 || errOut != "" {
		t.Fatalf("dash file: %d %s %s", code, out, errOut)
	}
	if result := commandEnvelope(t, out)["result"].(map[string]any); result["status"] != "no-assets" {
		t.Fatalf("dash file: %s", out)
	}
}

func TestCLIInputPlacement(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(root, "space directory")
	writeCheckFile(t, root, "version", "notes\n")
	for _, args := range [][]string{
		{"--root", root, "check", "version", "--json"},
		{"check", "--root=" + root, "version", "--json"},
	} {
		code, out, errOut := captureCommand(t, context.Background(), args, false)
		if code != 0 || errOut != "" || commandEnvelope(t, out)["command"] != "check" {
			t.Fatalf("value became command: %v: %d %s %s", args, code, out, errOut)
		}
	}
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	for _, args := range [][]string{
		{"--root", root, "git", "branch", "start", "--json"},
		{"git", "--root", root, "branch", "start", "--json"},
		{"git", "branch", "--root", root, "start", "--json"},
		{"git", "branch", "start", "--root", root, "--json"},
	} {
		code, out, errOut := captureCommand(t, context.Background(), args, false)
		if code != 1 || errOut != "" || commandEnvelope(t, out)["code"] != "PLAN_REQUIRED" {
			t.Fatalf("multi-level flags: %v: %d %s %s", args, code, out, errOut)
		}
	}
	args := []string{"--root", root, "git", "branch", "start", "--bogus", "--json", "--diagnostics"}
	_, out, _ := captureCommand(t, context.Background(), args, false)
	env := commandEnvelope(t, out)
	got := env["diagnostic"].(map[string]any)["context"].(map[string]any)["argv"]
	want := append([]string{"yss"}, args...)
	var gotArgs []string
	for _, v := range got.([]any) {
		gotArgs = append(gotArgs, v.(string))
	}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("raw argv changed: %v want %v", got, want)
	}
}

func TestCLIFlagPresence(t *testing.T) {
	for _, args := range [][]string{
		{"check", "--root="}, {"version", "--human=1"}, {"version", "-hV"},
		{"scaffold", "ddd", "--standalone=true", "--contract-file=x"},
	} {
		code, out, _ := captureCommand(t, context.Background(), append(args, "--json"), false)
		if code != 2 || commandEnvelope(t, out)["code"] != "ARGUMENT" {
			t.Fatalf("presence: %v %d %s", args, code, out)
		}
	}
	code, out, _ := captureCommand(t, context.Background(), []string{"scaffold", "ddd", "--standalone=false", "--contract-file=x", "--json"}, false)
	if code != 2 || !strings.Contains(out, "project-name") || strings.Contains(out, "不得携带") {
		t.Fatalf("false treated as true: %d %s", code, out)
	}
}

func TestCLIArgumentErrorProfile(t *testing.T) {
	code, out, errOut := captureCommand(t, context.Background(), []string{"contract", "verify", "a", "b", "--profile", "backend", "--json"}, false)
	if code != 2 || errOut != "" || commandEnvelope(t, out)["profile"] != "backend" {
		t.Errorf("argument error lost parsed profile: %d %s %s", code, out, errOut)
	}
	code, out, errOut = captureCommand(t, context.Background(), []string{"version", "extra", "--json=true", "--json=false"}, false)
	if code != 2 || out != "" || !strings.Contains(errOut, "ARGUMENT") {
		t.Fatalf("parsed output selection overridden by failure probe: %d %s %s", code, out, errOut)
	}
}

func TestCLIDefaultHelpRequiresValidFlags(t *testing.T) {
	for _, args := range [][]string{{"--bogus", "--json"}, {"--json=TRUE"}, {"--root"}} {
		code, out, errOut := captureCommand(t, context.Background(), args, false)
		if code != 2 || !strings.Contains(out+errOut, "ARGUMENT") {
			t.Errorf("invalid flags became help: %v: %d %s %s", args, code, out, errOut)
		}
	}
	for _, args := range [][]string{{"--version", "--version=false", "--json"}, {"--root", "/private/tmp"}, {"--file=x"}} {
		code, out, errOut := captureCommand(t, context.Background(), args, false)
		if code != 0 || errOut != "" || !strings.Contains(out, "用法:") || strings.Contains(out, `"outputVersion"`) {
			t.Errorf("valid no-command flags lost help: %v: %d %s %s", args, code, out, errOut)
		}
	}
}

func TestCLIPreflightErrorState(t *testing.T) {
	for _, bad := range []string{"-x", "--file"} {
		code, out, errOut := captureCommand(t, context.Background(), []string{"contract", "verify", "--profile", "backend", bad, "--json"}, false)
		if code != 2 || errOut != "" || commandEnvelope(t, out)["profile"] != "backend" {
			t.Errorf("preflight lost parsed profile (%s): %d %s %s", bad, code, out, errOut)
		}
	}
	code, out, errOut := captureCommand(t, context.Background(), []string{"--version", "-x", "--json"}, false)
	if code != 2 || errOut != "" || commandEnvelope(t, out)["command"] != "version" {
		t.Errorf("preflight lost version alias: %d %s %s", code, out, errOut)
	}
}

func TestCLIVersionAliasProjectFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--root", "/private/tmp", "--version", "--json"},
		{"--profile", "backend", "-V", "--json"},
		{"capabilities", "--profile", "backend", "-V", "--json"},
	} {
		code, out, errOut := captureCommand(t, context.Background(), args, false)
		if code != 0 || errOut != "" || commandEnvelope(t, out)["command"] != "version" {
			t.Errorf("version alias rejected project flags: %v: %d %s %s", args, code, out, errOut)
		}
	}
}

func TestCommandTreeCoverage(t *testing.T) {
	root := newRootCommand(nil, nil, nil)
	for _, path := range []string{
		"archive",
		"archive pack",
		"archive unpack",
		"archive verify",
		"assets",
		"assets ensure",
		"assets list",
		"attach",
		"bundle",
		"bundle export",
		"bundle inspect",
		"capabilities",
		"check",
		"context",
		"context check",
		"context query",
		"context verify",
		"contract",
		"contract check",
		"contract verify",
		"contract view",
		"contract publish-review",
		"diff",
		"doctor",
		"evidence",
		"evidence check",
		"evidence verify",
		"explain",
		"git",
		"git audit",
		"git base",
		"git branch",
		"git changelog",
		"git commit",
		"git doctor",
		"git guard",
		"git hooks",
		"git policy",
		"git push",
		"git release",
		"git status",
		"git submodules",
		"git verify-message",
		"handoff",
		"handoff check",
		"handoff export",
		"handoff import",
		"handoff verify",
		"init",
		"lifecycle",
		"lifecycle query",
		"lifecycle route",
		"lifecycle status",
		"lifecycle target",
		"lifecycle verify",
		"lifecycle verify-daily",
		"mcp",
		"migrate",
		"profile",
		"profile prepare",
		"project-ci",
		"project-ci apply",
		"project-ci check",
		"project-ci install",
		"project-ci plan",
		"project-ci transition",
		"project-ci verify",
		"reading",
		"reading affected",
		"reading apply-enable",
		"reading check-views",
		"reading plan-enable",
		"reading render",
		"recover",
		"rollback",
		"runtime",
		"runtime begin",
		"runtime commands",
		"runtime complete",
		"runtime event",
		"runtime events",
		"runtime inspect",
		"runtime pin",
		"runtime pins",
		"runtime run",
		"runtime unpin",
		"scaffold",
		"scaffold ddd",
		"scaffold mvc",
		"skills",
		"skills ensure",
		"skills list",
		"skills resolve",
		"stage",
		"stage apply",
		"stage check",
		"stage plan",
		"stage query",
		"stage register",
		"stage status",
		"stage update",
		"sync",
		"trace",
		"tracker",
		"tracker link",
		"tracker status",
		"tracker sync",
		"update",
		"update apply",
		"update plan",
		"update recover",
		"update rollback",
		"update status",
		"upgrade",
		"verify",
		"version",
		"xml",
		"xml inspect",
		"xml query",
		"git branch start",
		"git hooks install",
		"git release tag",
		"git policy set",
	} {
		cmd, rest := lookupCommand(root, strings.Fields(path))
		if len(rest) > 0 || cmd.RunE == nil {
			t.Errorf("missing entry: %s", path)
			continue
		}
		code, out, errOut := captureCommand(t, context.Background(), append(strings.Fields(path), "--help"), false)
		if code != 0 || out == "" || errOut != "" {
			t.Errorf("help %s: %d %s %s", path, code, out, errOut)
		}
	}
}
