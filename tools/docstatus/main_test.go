package main

import (
	"strings"
	"testing"
)

func TestTemplateSourceLabel(t *testing.T) {
	commit := strings.Repeat("a", 40)
	for _, tc := range []struct {
		name   string
		source templateSource
		ok     bool
	}{
		{"released", templateSource{Tag: "v3.5.10", Commit: commit}, true},
		{"committed", templateSource{Commit: commit, Version: "git:" + commit, State: "committed"}, true},
		{"unversioned", templateSource{Commit: commit}, false},
		{"wrong-version", templateSource{Commit: commit, Version: "git:" + strings.Repeat("b", 40), State: "committed"}, false},
		{"working-tree", templateSource{Commit: commit, Version: "git:" + commit, State: "working-tree"}, false},
		{"bad-commit", templateSource{Commit: strings.Repeat("x", 40), Version: "git:" + strings.Repeat("x", 40), State: "committed"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, e := templateLabel(tc.source)
			if (e == nil) != tc.ok {
				t.Fatalf("source label: %v", e)
			}
		})
	}
}
