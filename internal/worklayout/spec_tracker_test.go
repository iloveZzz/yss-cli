package worklayout

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSpecTrackerConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		bad        bool
	}{
		{"local", "platform: local-markdown\n  root: .work", false},
		{"github", "platform: github\n  repository: acme/spec", false},
		{"gitlab", "platform: gitlab\n  host: https://git.example.org\n  project: team/sub/spec", false},
		{"github-target", "platform: github", true},
		{"local-root", "platform: local-markdown", true},
		{"escape", "platform: local-markdown\n  root: ../outside", true},
		{"gitlab-host", "platform: gitlab\n  host: https://user:secret@git.example.org\n  project: team/spec", true},
		{"reserved", "platform: local-markdown\n  root: .git", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, TrackerRef)
			if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("---\ntracker:\n  "+tc.body+"\n---\n"), 0644); err != nil {
				t.Fatal(err)
			}
			got, err := ReadSpecTracker(root)
			if (err != nil) != tc.bad {
				t.Fatalf("got %+v, %v; bad=%v", got, err, tc.bad)
			}
		})
	}
}

func TestRemoteSpecScanDoesNotRequireLocalWritingRoot(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, TrackerRef)
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("---\ntracker:\n  platform: github\n  repository: acme/spec\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}
	roots, err := ReadScanRoots(root)
	if err != nil || len(roots) == 0 {
		t.Fatalf("ordinary remote config cannot be inspected before attach: %v %v", roots, err)
	}
	if _, err := Read(root); err == nil {
		t.Fatal("legacy writing-root validation was relaxed")
	}
}

// A malformed optional field must not silently disappear from a valid destination.
func TestSpecTrackerRejectsNonStringFields(t *testing.T) {
	for _, field := range []string{"repository", "host", "project"} {
		t.Run(field, func(t *testing.T) {
			raw := []byte("---\ntracker:\n  platform: local-markdown\n  root: .work\n  " + field + ": 42\n---\n")
			if got, err := ParseSpecTracker(t.TempDir(), raw); err == nil {
				t.Fatalf("malformed %s accepted: %+v", field, got)
			}
		})
	}
}
