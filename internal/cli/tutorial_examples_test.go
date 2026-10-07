package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/helpview"
)

func tutorialRun(t *testing.T, args ...string) (int, []byte, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	exit := Run(context.Background(), args, &out, &stderr)
	return exit, out.Bytes(), stderr.String()
}

func TestTutorialFourProfileInitializationAndReadOnlyEntry(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		t.Run(profile, func(t *testing.T) {
			root := filepath.Join(base, profile)
			plan := filepath.Join(base, profile+"-plan.json")
			for _, args := range [][]string{{"init", "--profile", profile, "--root", root, "--project-name", "教程试用", "--plan", "--out", plan, "--human"}, {"init", "--profile", profile, "--root", root, "--apply", "--plan-file", plan, "--json"}, {"doctor", "--root", root, "--json"}, {"context", "verify", "--root", root, "--json"}} {
				if exit, output, stderr := tutorialRun(t, args...); exit != 0 {
					t.Fatalf("%v: exit=%d %s %s", args, exit, output, stderr)
				}
			}
			view, err := helpview.Load(profile)
			if err != nil {
				t.Fatal(err)
			}
			if exit, output, stderr := tutorialRun(t, "lifecycle", "query", "--root", root, "--id", view.EntryWorkUnit, "--json"); exit != 0 {
				t.Fatalf("entry: %d %s %s", exit, output, stderr)
			}
			// Every stage's identity/order is checked against the fixed authority
			// in helpview tests; exercise the actual entry command once per Profile.
			stage := view.Stages[0]
			if exit, output, stderr := tutorialRun(t, "stage", "query", "--root", root, "--id", stage.ID, "--json"); exit != 0 {
				t.Fatalf("%s: %d %s %s", stage.ID, exit, output, stderr)
			}
		})
	}
}

func TestTutorialStageRawPlanAndEnvelopeBoundary(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "project")
	if exit, output, stderr := tutorialRun(t, "init", "--profile", "spec", "--root", root, "--json"); exit != 0 {
		t.Fatalf("init: %d %s %s", exit, output, stderr)
	}
	cp := ".work/cli-tutorial-test/checkpoint.json"
	for _, pair := range [][2]string{{"checkpoint.json", cp}, {"items.json", "docs/work-items.json"}} {
		content, err := os.ReadFile("testdata/tutorial/" + pair[0])
		if err != nil {
			t.Fatal(err)
		}
		dailyWrite(t, root, pair[1], content)
	}
	args := []string{"stage", "register", "--root", root, "--checkpoint", cp, "--items", "docs/work-items.json"}
	exit, raw, stderr := tutorialRun(t, args...)
	if exit != 0 {
		t.Fatalf("register: %d %s %s", exit, raw, stderr)
	}
	var plan map[string]any
	if err = json.Unmarshal(raw, &plan); err != nil || plan["outputVersion"] != nil || plan["kind"] != "stage-tracking-go-plan" {
		t.Fatalf("原始重定向格式失配: %s %v", raw, err)
	}
	dailyWrite(t, root, "docs/stage-plan.json", raw)
	exit, envelope, stderr := tutorialRun(t, append(args, "--json")...)
	if exit != 0 {
		t.Fatalf("envelope: %d %s %s", exit, envelope, stderr)
	}
	dailyWrite(t, root, "docs/envelope-plan.json", envelope)
	if exit, _, _ := tutorialRun(t, "stage", "apply", "--root", root, "--plan-file", "docs/envelope-plan.json", "--json"); exit == 0 {
		t.Fatal("envelope 不得当作计划消费")
	}
	items, err := os.ReadFile(filepath.Join(root, "docs/work-items.json"))
	if err != nil {
		t.Fatal(err)
	}
	dailyWrite(t, root, "docs/work-items.json", append(append([]byte{}, items...), '\n'))
	if exit, output, _ := tutorialRun(t, "stage", "apply", "--root", root, "--plan-file", "docs/stage-plan.json", "--json", "--diagnostics"); exit == 0 || !strings.Contains(string(output), "SAVED_STAGE_PLAN_CHANGED") || !strings.Contains(string(output), `"recheck"`) {
		t.Fatalf("过期计划未诊断: %d %s", exit, output)
	}
	dailyWrite(t, root, "docs/work-items.json", items)
	if exit, output, stderr := tutorialRun(t, "stage", "apply", "--root", root, "--plan-file", "docs/stage-plan.json", "--json"); exit != 0 {
		t.Fatalf("apply: %d %s %s", exit, output, stderr)
	}
	current, err := os.ReadFile(filepath.Join(root, cp))
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint map[string]any
	if err = json.Unmarshal(current, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if len(checkpoint["gates"].(map[string]any)) != 0 {
		t.Fatal("案例创建了业务批准")
	}
}
