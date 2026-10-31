package main

import "testing"

func TestTemplateTag(t *testing.T) {
	for _, tc := range []struct {
		tag string
		ok  bool
	}{
		{"v3.5.10", true},
		{"v3.5.10-rc1", false},
		{"3.5.10", false},
		{"v03.5.10", false},
	} {
		if got := validTemplateTag(tc.tag); got != tc.ok {
			t.Fatalf("tag %q: %v", tc.tag, got)
		}
	}
}
