package kernel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReviewRequirementScopeUsesSealedLogicalRoot(t *testing.T) {
	for _, root := range []string{"/missing-original-source", `C:\work\missing-source`} {
		t.Run(root, func(t *testing.T) {
			s := NewSemanticSession(context.Background(), t.TempDir(), nil)
			s.taskSnapshot = &taskSourceSnapshot{root: root}
			s.Aliases["drafts/spec.md"] = "sealed/flat-spec.md"
			if err := os.MkdirAll(filepath.Join(s.Root, "sealed"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(s.Root, "sealed/flat-spec.md"), []byte("sealed source"), 0644); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				actual, requested string
				match, invalid    bool
			}{
				{"drafts", "drafts/spec.md", true, false},
				{"drafts/spec.md", "drafts", true, false},
				{"drafts", "draftstuff", false, false},
				{"drafts", "other/spec.md", false, false},
				{"drafts", "../drafts/spec.md", false, true},
				{"drafts", "DRAFTS/spec.md", false, true},
			} {
				got, err := s.reviewScopesIntersectAtRoot(nil, []string{tc.actual}, []string{tc.requested}, root)
				if (err != nil) != tc.invalid || !tc.invalid && got != tc.match {
					t.Fatalf("%s vs %s: match=%v err=%v", tc.actual, tc.requested, got, err)
				}
			}
			if err := os.Symlink(t.TempDir(), filepath.Join(s.Root, "sealed/link")); err != nil {
				t.Fatal(err)
			}
			s.Aliases["drafts/link/file.md"] = "sealed/link/file.md"
			if _, err := s.reviewScopesIntersectAtRoot(nil, []string{"drafts"}, []string{"drafts/link/file.md"}, root); err == nil {
				t.Fatal("sealed alias escaped through symlink")
			}
			if _, err := s.reviewScopesIntersectAtRoot(nil, []string{"drafts"}, []string{"drafts/spec.md"}, root+"/other"); err == nil {
				t.Fatal("borrowed an unbound source root")
			}
		})
	}
}
