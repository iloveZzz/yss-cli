package project

import (
	"encoding/json"
	"errors"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLegacySpecMigrationRefusesUnprovenManagedPermissionChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows normalizes ordinary file modes")
	}
	root := freshRoot(t)
	const ref = "scripts/lib/context-contract.mjs"
	original := []byte("// actual previous managed bytes\n")
	file := filepath.Join(root, ref)
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	metadata := map[string]any{"metadataSchemaVersion": 3, "templateSource": domain.Profiles["spec"].TemplateSource, "templateCommit": strings.Repeat("a", 40), "managedFiles": map[string]any{ref: map[string]any{"contentHash": safefs.Digest(original)}}}
	raw, _ := json.Marshal(metadata)
	_ = os.WriteFile(filepath.Join(root, domain.Profiles["spec"].Metadata), raw, 0644)
	_ = os.WriteFile(filepath.Join(root, "yss-project.yaml"), []byte("schema_version: 1\nrepository_mode: project-instance\n"), 0644)
	plan, err := Build(root, "spec", "migrate", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, conflict := range plan.Conflicts {
		if conflict == ref {
			found = true
		}
	}
	if !found {
		t.Fatal("unrecorded legacy mode authorized overwrite")
	}
	_, err = Apply(plan)
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != "CONFLICT" {
		t.Fatalf("wrong refusal: %v", err)
	}
	observed, err := safefs.Describe(root, ref)
	if err != nil || observed.Mode != 0600 || observed.Digest != safefs.Digest(original) {
		t.Fatal("refused migration changed managed bytes/mode")
	}
}
