package identitymeta

import (
	"path/filepath"
	"testing"
)

func TestProfileLinksRejectDesignWithoutFiltering(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	for _, profile := range []string{"backend", "design", "unknown"} {
		links := &ProfileLinks{SchemaVersion: 1, Links: map[string]string{profile: target}}
		err := ValidateProfileLinks(root, links)
		if (err == nil) != (profile == "backend") {
			t.Fatalf("profile=%s: %v", profile, err)
		}
		if links.Links[profile] != target {
			t.Fatal("refusal filtered original input")
		}
	}
	if err := ValidateProfileLinks(root, &ProfileLinks{SchemaVersion: 1, Links: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
}
