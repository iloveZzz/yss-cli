//go:build !yss_nobundle

package cli

import (
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
	if _, available := b.Files[".codebuddy/skills/code-review/SKILL.md"]; !available {
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
				projection := ".codebuddy/skills/" + strings.TrimPrefix(ref, ".agents/skills/")
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
	t.Run("assets-ensure-plan", func(t *testing.T) {
		root := commandProject(t) // Reuse the existing shared initialized fixture.
		result := commandOK(t, root, "assets", "ensure", "stage.spec-architecture", "--plan", "--out", filepath.Join(t.TempDir(), "assets-plan.json"))
		check(t, result)
	})
}
