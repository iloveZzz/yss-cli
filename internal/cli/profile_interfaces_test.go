package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfilePreparationPublicPlanAndApply(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "design")
	run := func(args ...string) map[string]any {
		t.Helper()
		var out, stderr bytes.Buffer
		if exit := Run(context.Background(), append(args, "--json"), &out, &stderr); exit != 0 {
			t.Fatalf("%v exit=%d out=%s stderr=%s", args, exit, out.String(), stderr.String())
		}
		var envelope map[string]any
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope["result"].(map[string]any)
	}
	run("init", "--profile", "design", "--root", root)
	planFile := filepath.Join(base, "prepare.json")
	run("profile", "prepare", "--root", root, "--backend-root", filepath.Join(base, "backend"), "--frontend-root", filepath.Join(base, "frontend"), "--plan", "--out", planFile)
	if _, err = os.Stat(filepath.Join(root, ".yss-profile-links.json")); !os.IsNotExist(err) {
		t.Fatal("plan mutated source association")
	}
	result := run("profile", "prepare", "--root", root, "--apply", "--plan-file", planFile)
	if result["status"] != "completed" {
		t.Fatalf("%+v", result)
	}
	for _, profile := range []string{"backend", "frontend"} {
		run("doctor", "--root", filepath.Join(base, profile))
	}
	result = run("profile", "prepare", "--root", root, "--apply", "--plan-file", planFile)
	if result["status"] != "completed" {
		t.Fatalf("idempotent public retry: %+v", result)
	}
}

func TestProfilePreparationAndSpecBaselinePublicHelp(t *testing.T) {
	for _, command := range [][]string{{"profile", "prepare"}, {"handoff", "export"}, {"handoff", "import"}} {
		var out, stderr bytes.Buffer
		args := append(append([]string{}, command...), "--help")
		if exit := Run(context.Background(), args, &out, &stderr); exit != 0 {
			t.Fatalf("%v: exit=%d %s", command, exit, stderr.String())
		}
		if !strings.Contains(out.String(), "--plan-file") && command[1] != "export" {
			t.Fatalf("saved-plan interface absent: %s", out.String())
		}
	}
}

func TestSpecBaselineVerificationSelectorsRegistered(t *testing.T) {
	o, err := parse([]string{"handoff", "verify", "--root", "/missing", "--kind", "spec-baseline", "--package", "/package", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if err = validateArguments("handoff", o); err != nil {
		t.Fatal(err)
	}
}
