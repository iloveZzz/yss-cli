package worklayout_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestWorkLayoutProductionConsumersHaveNoLegacyHardcodes(t *testing.T) {
	root, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	// These are explicit historical compatibility or migration contracts. Current
	// governance, CLI examples and asset writers must use the shared resolver.
	exceptions := map[string]bool{
		"internal/worklayout/layout.go":              true,
		"internal/project/work_layout.go":            true,
		"internal/project/work_layout_resolution.go": true,
		"internal/bundle/native_work_layout.go":      true,
		"internal/bundle/producer.go":                true,
		"internal/bundle/upgrade.go":                 true,
	}
	old := regexp.MustCompile(`docs(?:/|\\/)(?:\.|\\\.)scratch`)
	e = filepath.WalkDir(filepath.Join(root, "internal"), func(file string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
			return nil
		}
		ref, e := filepath.Rel(root, file)
		if e != nil {
			return e
		}
		ref = filepath.ToSlash(ref)
		data, e := os.ReadFile(file)
		if e != nil {
			return e
		}
		if old.Match(data) && !exceptions[ref] {
			t.Errorf("production work root hardcode: %s", ref)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
