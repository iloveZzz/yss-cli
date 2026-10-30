//go:build !yss_nobundle

package cli

import (
	"context"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestNewGovernedSpecCLI(t *testing.T) {
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	if b.Installations["governed"].Distribution["specWorkflow"] != "matt-spec-v1" {
		t.Skip("fixed bundle predates new Spec workflow; use a truthful working-tree candidate")
	}
	root := filepath.Join(t.TempDir(), "spec")
	commandOK(t, root, "init", "--profile", "spec")
	raw, err := os.ReadFile(filepath.Join(root, ".yss.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		Distribution map[string]any `json:"distribution"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Distribution["installation"] != "governed" || meta.Distribution["specWorkflow"] != "matt-spec-v1" {
		t.Fatal("new workflow not installed")
	}
	for _, ref := range []string{"CONTEXT.md", ".template-spec/process/orchestration-contract.yaml", ".agents/skills/implement"} {
		if _, err := os.Stat(filepath.Join(root, ref)); !os.IsNotExist(err) {
			t.Fatalf("old product asset installed: %s", ref)
		}
	}
	doctor := commandOK(t, root, "doctor")
	if doctor["readiness"].(map[string]any)["skills"].(map[string]any)["status"] != "ready" {
		t.Fatal("new Spec doctor not ready")
	}
	if got := commandOK(t, root, "skills", "resolve", "yss-product-lifecycle"); got["status"] != "ready" {
		t.Fatalf("core skill not ready: %v", got)
	}
	plan := filepath.Join(t.TempDir(), "research-plan.json")
	commandOK(t, root, "skills", "ensure", "yss-research", "--plan", "--out", plan)
	commandOK(t, root, "skills", "ensure", "yss-research", "--apply", "--plan-file", plan)
	if got := commandOK(t, root, "skills", "resolve", "yss-research"); got["status"] != "ready" {
		t.Fatalf("research not ready: %v", got)
	}
	repeat := commandOK(t, root, "skills", "ensure", "yss-research", "--plan", "--out", filepath.Join(t.TempDir(), "repeat.json"))
	if len(repeat["changes"].([]any)) != 0 {
		t.Fatal("repeated ensure changed files")
	}
	code, out, _ := captureCommand(t, context.Background(), []string{"lifecycle", "status", "--root", root, "--json"}, false)
	if code == 0 || commandEnvelope(t, out)["code"] != "SPEC_COMMAND_NOT_APPLICABLE" {
		t.Fatalf("old lifecycle accepted: %d %s", code, out)
	}
}

func TestNewSpecCannotMigrateToSkills(t *testing.T) {
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	if b.Installations["governed"].Distribution["specWorkflow"] != "matt-spec-v1" {
		t.Skip("fixed bundle predates new Spec workflow")
	}
	root := filepath.Join(t.TempDir(), "spec")
	commandOK(t, root, "init", "--profile", "spec")
	plan := filepath.Join(t.TempDir(), "migration.json")
	code, out, _ := captureCommand(t, context.Background(), []string{"migrate", "--root", root, "--installation", "skills", "--plan", "--out", plan, "--json"}, false)
	if code == 0 || commandEnvelope(t, out)["code"] != "SPEC_MANUAL_MIGRATION_REQUIRED" {
		t.Fatalf("new workflow conversion was not refused: %d %s", code, out)
	}
	if _, err := os.Stat(plan); !os.IsNotExist(err) {
		t.Fatal("refused conversion wrote a plan")
	}
}

func TestNewSpecRemoteTrackerConfiguration(t *testing.T) {
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	if b.Installations["governed"].Distribution["specWorkflow"] != "matt-spec-v1" {
		t.Skip("fixed bundle predates new Spec workflow")
	}
	for _, action := range []string{"init", "attach"} {
		for _, platform := range []string{"github", "gitlab"} {
			t.Run(action+"/"+platform, func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "spec")
				if action == "attach" {
					if err := os.Mkdir(root, 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(root, "user.md"), []byte("keep"), 0644); err != nil {
						t.Fatal(err)
					}
				}
				args := []string{action, "--profile", "spec", "--issue-tracker", platform}
				if platform == "github" {
					args = append(args, "--tracker-repository", "acme/spec")
				} else {
					args = append(args, "--tracker-host", "https://git.example.org", "--tracker-project", "team/spec")
				}
				if action == "attach" {
					plan := filepath.Join(t.TempDir(), "attach-plan.json")
					commandOK(t, root, append(args, "--plan", "--out", plan)...)
					commandOK(t, root, "attach", "--apply", "--plan-file", plan)
				} else {
					commandOK(t, root, args...)
				}
				cfg, err := worklayout.ReadSpecTracker(root)
				if err != nil {
					t.Fatal(err)
				}
				if cfg.Platform != platform || (platform == "github" && cfg.Repository != "acme/spec") || (platform == "gitlab" && (cfg.Host != "https://git.example.org" || cfg.Project != "team/spec")) {
					t.Fatalf("target lost: %+v", cfg)
				}
			})
		}
	}
	root := filepath.Join(t.TempDir(), "invalid")
	plan := filepath.Join(t.TempDir(), "invalid-plan.json")
	code, out, _ := captureCommand(t, context.Background(), []string{"init", "--profile", "spec", "--root", root, "--issue-tracker", "github", "--plan", "--out", plan, "--json"}, false)
	if code == 0 || commandEnvelope(t, out)["code"] != "SPEC_TRACKER_CONFIG" {
		t.Fatalf("incomplete target accepted: %d %s", code, out)
	}
	if _, err := os.Stat(plan); !os.IsNotExist(err) {
		t.Fatal("invalid target wrote plan")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("invalid target wrote project")
	}
}

// Commands printed for a newly initialized Spec must work on that project.
func TestSpecQuickstartCommandsRunOnNewProject(t *testing.T) {
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	if b.Installations["governed"].Distribution["specWorkflow"] != "matt-spec-v1" {
		t.Skip("fixed bundle predates new Spec workflow")
	}
	root := filepath.Join(t.TempDir(), "spec")
	commandOK(t, root, "init", "--profile", "spec")
	code, tutorial, stderr := captureCommand(t, context.Background(), []string{"help", "tutorial", "quickstart"}, false)
	if code != 0 {
		t.Fatalf("help failed: %s", stderr)
	}
	checked := 0
	for _, line := range strings.Split(tutorial, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 3 || fields[0] != "yss" || (fields[1] != "doctor" && fields[1] != "skills" && fields[1] != "context" && fields[1] != "lifecycle") {
			continue
		}
		args := fields[1:]
		for i, v := range args {
			if v == "./demo-spec" {
				args[i] = root
			}
		}
		code, out, errout := captureCommand(t, context.Background(), args, false)
		if code != 0 {
			t.Errorf("quickstart command failed on its Spec project: %s (exit %d; %s %s)", line, code, out, errout)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("quickstart offers no post-init Spec verification command")
	}
}

func TestDailyTutorialTargetsBackendPolicy(t *testing.T) {
	code, tutorial, stderr := captureCommand(t, context.Background(), []string{"help", "tutorial", "daily"}, false)
	if code != 0 {
		t.Fatalf("help failed: %s", stderr)
	}
	for _, line := range strings.Split(tutorial, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "yss lifecycle ") && strings.Contains(line, "--root ./demo-spec") {
			t.Errorf("daily command targets new Spec, where lifecycle is unavailable: %s", line)
		}
	}
	if !strings.Contains(tutorial, "./demo-backend") {
		t.Fatal("daily tutorial has no concrete Backend project target")
	}
}

// An installed Backend project must contain each skill document the daily tutorial asks readers to open.
func TestBackendDailyTutorialResourceExistsInInstalledProject(t *testing.T) {
	root := filepath.Join(t.TempDir(), "backend")
	commandOK(t, root, "init", "--profile", "backend")
	code, tutorial, stderr := captureCommand(t, context.Background(), []string{"help", "tutorial", "daily"}, false)
	if code != 0 {
		t.Fatalf("help failed: %s", stderr)
	}
	refs := regexp.MustCompile(`\.agents/skills/[A-Za-z0-9_./-]+\.md`).FindAllString(tutorial, -1)
	if len(refs) == 0 {
		t.Fatal("daily tutorial offers no concrete project guidance resource")
	}
	for _, ref := range refs {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ref)))
		if err != nil || len(data) == 0 {
			t.Errorf("daily tutorial references missing or empty installed Backend resource %s: %v", ref, err)
		}
	}
}
