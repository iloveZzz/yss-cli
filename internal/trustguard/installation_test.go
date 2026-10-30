//go:build !yss_nobundle

package trustguard_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/project"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/trustguard"
)

func TestActualInstallationProfileMatchesTrustedBundle(t *testing.T) {
	for _, profile := range []string{"spec", "backend"} {
		t.Run(profile, func(t *testing.T) {
			root := t.TempDir()
			plan, err := project.Build(root, profile, "init", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, change := range plan.Changes {
				target := filepath.Join(root, change.Path)
				if change.After.Type == "directory" {
					if err = os.MkdirAll(target, 0755); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if change.After.Type != "file" {
					continue
				}
				if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
					t.Fatal(err)
				}
				data, e := base64.StdEncoding.DecodeString(change.Data)
				if e != nil {
					t.Fatal(e)
				}
				if err = os.WriteFile(target, data, os.FileMode(change.After.Mode)); err != nil {
					t.Fatal(err)
				}
			}
			raw, err := os.ReadFile(filepath.Join(root, ".yss.json"))
			if err != nil {
				t.Fatal(err)
			}
			var meta identitymeta.Metadata
			if err = json.Unmarshal(raw, &meta); err != nil {
				t.Fatal(err)
			}
			guard, err := trustguard.Open(&meta)
			if err != nil {
				t.Fatal(err)
			}
			const ref = ".template-spec/process/harness-profile.yaml"
			actual, err := safefs.Describe(root, ref)
			if err != nil {
				t.Fatal(err)
			}
			if err = guard.VerifyPolicy(ref, actual); err != nil {
				t.Fatal(err)
			}
			tracker := filepath.Join(root, ".template-spec/agents/issue-tracker.md")
			if _, err = os.Stat(tracker); err == nil {
				if err = os.WriteFile(tracker, []byte("user configured tracker\n"), 0644); err != nil {
					t.Fatal(err)
				}
				if err = guard.VerifyPolicy(ref, actual); err != nil {
					t.Fatalf("tracker customization affected policy: %v", err)
				}
			}
			if err = os.WriteFile(filepath.Join(root, ref), []byte("changed policy\n"), 0644); err != nil {
				t.Fatal(err)
			}
			changed, err := safefs.Describe(root, ref)
			if err != nil {
				t.Fatal(err)
			}
			if err = guard.VerifyPolicy(ref, changed); err == nil {
				t.Fatal("modified profile accepted")
			}
		})
	}
}
