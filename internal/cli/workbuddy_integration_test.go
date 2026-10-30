//go:build !yss_nobundle

package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
)

func TestWorkBuddyCLIInstallationClosures(t *testing.T) {
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	if _, available := b.Files[".workbuddy/skills/code-review/SKILL.md"]; !available {
		t.Skip("fixed Bundle has not advanced to the WorkBuddy template; run with the new local candidate Bundle")
	}
	check := func(t *testing.T, result map[string]any) {
		t.Helper()
		paths := map[string]bool{}
		for _, item := range result["assets"].([]any) {
			paths[item.(map[string]any)["path"].(string)] = true
		}
		canonical := 0
		for ref := range paths {
			if strings.HasPrefix(ref, ".agents/skills/") {
				canonical++
				projection := ".workbuddy/skills/" + strings.TrimPrefix(ref, ".agents/skills/")
				if !paths[projection] {
					t.Fatalf("CLI plan omitted WorkBuddy resource %s", projection)
				}
			}
			if strings.HasPrefix(ref, ".codex/skills/") || strings.HasPrefix(ref, ".cursor/skills/") || strings.HasPrefix(ref, ".pi/skills/") {
				t.Fatalf("retired projection resurrected: %s", ref)
			}
		}
		if canonical == 0 {
			t.Fatal("fixture did not exercise a Skill closure")
		}
	}
	t.Run("full-init-plan", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "project")
		result := commandOK(t, root, "init", "--profile", "spec", "--full", "--plan", "--out", filepath.Join(t.TempDir(), "full-plan.json"))
		check(t, result)
	})
	t.Run("new-spec-rejects-stage-assets", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "project")
		commandOK(t, root, "init", "--profile", "spec")
		plan := filepath.Join(t.TempDir(), "assets-plan.json")
		code, out, _ := captureCommand(t, context.Background(), []string{"assets", "ensure", "stage.spec-architecture", "--root", root, "--plan", "--out", plan, "--json"}, false)
		if code == 0 || commandEnvelope(t, out)["code"] != "SPEC_COMMAND_NOT_APPLICABLE" {
			t.Fatalf("old stage path accepted: %d %s", code, out)
		}
		if _, err := os.Stat(plan); !os.IsNotExist(err) {
			t.Fatal("rejected stage wrote plan")
		}
	})
}
