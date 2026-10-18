package handoff

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

func TestHandoffClosureIncludesPreviouslyScannedRegularFile(t *testing.T) {
	root := t.TempDir()
	for ref, content := range map[string]string{"source.json": "{}", "docs/proof.json": "{}", "CONTEXT.md": "# Context", ".template-spec/agents/digital-human-roles.yaml": "{}"} {
		file := filepath.Join(root, ref)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s := kernel.NewSemanticSession(ctx, root, nil)
	if _, err := s.Scan("source.json"); err != nil {
		t.Fatal(err)
	}
	refs, err := contractHandoffClosure(s, "source.json", &NativeHandoff{Handoff: map[string]any{}, Config: map[string]any{"additional_files": []any{"docs"}}})
	if err != nil || len(refs) != 4 {
		t.Fatalf("regular source must be included once: refs=%v err=%v", refs, err)
	}
}
