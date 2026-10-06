package project

import (
	"encoding/json"
	"errors"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

func TestHistoricalRecoveryMustFinishBeforeNativeMigration(t *testing.T) {
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		for _, phase := range []string{"apply", "backup", "recovery-failed", "committed", "rolled-back"} {
			t.Run(profile+"/"+phase, func(t *testing.T) {
				root := freshRoot(t)
				ref := ".yss-harness-state/" + profile + "/transactions/fixture/journal.json"
				file := filepath.Join(root, filepath.FromSlash(ref))
				if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(map[string]any{"schemaVersion": 2, "id": "fixture", "profileId": domain.Profiles[profile].ID, "phase": phase, "operations": []any{}})
				if err := os.WriteFile(file, raw, 0600); err != nil {
					t.Fatal(err)
				}
				err := legacyRecoveryInputs(&Identity{Root: root, Profile: domain.Profiles[profile]}, map[string]domain.Descriptor{})
				if phase == "committed" || phase == "rolled-back" {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					var failure *domain.Error
					if !errors.As(err, &failure) || failure.Code != "LEGACY_INTERRUPTED" {
						t.Fatalf("wrong refusal: %v", err)
					}
				}
				got, _ := os.ReadFile(file)
				if string(got) != string(raw) {
					t.Fatal("native consumer rewrote old journal")
				}
			})
		}
	}
}
