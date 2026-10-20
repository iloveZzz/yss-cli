package updater

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

func TestArchiveRejectsNonRegularAndOversizedInputs(t *testing.T) {
	base := updaterTemp(t)
	file := filepath.Join(base, "regular.tar.gz")
	if err := os.WriteFile(file, []byte("archive"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"directory", "symlink", "oversized", "regular-digest"} {
		t.Run(scenario, func(t *testing.T) {
			artifact := filepath.Join(base, scenario+".tar.gz")
			want := "ARTIFACT"
			switch scenario {
			case "directory":
				if err := os.Mkdir(artifact, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(file, artifact); err != nil {
					t.Skipf("symlink capability unavailable: %v", err)
				}
			case "oversized":
				f, err := os.Create(artifact)
				if err != nil {
					t.Fatal(err)
				}
				err = f.Truncate(maxArchive + 1)
				_ = f.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "regular-digest":
				artifact, want = file, "DIGEST"
			}
			_, err := Build(filepath.Join(base, "tool"), artifact, strings.Repeat("0", 64))
			var de *domain.Error
			if !errors.As(err, &de) || de.Code != want {
				t.Fatalf("%s: expected %s, got %v", scenario, want, err)
			}
		})
	}
}
