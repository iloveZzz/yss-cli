package governance

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CI supplies the exact template commit from source-lock.json. Developer-only
// oracles retain the existing local fallback when no explicit source is given.
func governanceOracleRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv("YSS_LEGACY_ORACLE_ROOT")
	if root == "" {
		root = "/Users/zhudaoming/Projects/yss-spec-project-template"
	}
	resolved, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(resolved, "scripts/lib/lifecycle-registry.mjs")); err != nil {
		if os.Getenv("YSS_LEGACY_ORACLE_ROOT") != "" {
			t.Fatalf("explicit fixed template oracle is unavailable: %v", err)
		}
		t.Skip("development-only fixed template oracle unavailable")
	}
	return resolved
}

func governanceOracleURL(file string) string {
	name := filepath.ToSlash(file)
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	return (&url.URL{Scheme: "file", Path: name}).String()
}

func TestGovernanceOracleURLQuotesPaths(t *testing.T) {
	file := filepath.Join(t.TempDir(), "模板 #1.mjs")
	value := governanceOracleURL(file)
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "file" || !strings.Contains(value, "%23") || !strings.Contains(value, "%20") {
		t.Fatalf("unsafe module URL: %s %v", value, err)
	}
}
