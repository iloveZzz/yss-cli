package kernel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRemovedSpecBaselineImportRejected(t *testing.T) {
	for _, payload := range []string{
		`{"schema_version":1,"upstream_spec_baseline":{}}`,
		`{"schema_version":1,"profile":"spec","upstream_spec_baseline":null}`,
		`{"schema_version":1,"kind":"spec-baseline-import"}`,
		`{"schema_version":1,"approval_context":{"source_baseline":{"receipt_ref":"old.json"}}}`,
	} {
		t.Run(payload, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, "source.json")
			if err := os.WriteFile(file, []byte(payload), 0600); err != nil {
				t.Fatal(err)
			}
			s := NewSemanticSession(context.Background(), root, nil)
			if _, err := s.Doc("source.json"); err == nil {
				t.Fatal("removed Spec baseline import accepted")
			}
			actual, err := os.ReadFile(file)
			if err != nil || string(actual) != payload {
				t.Fatal("input changed", err)
			}
		})
	}
}
