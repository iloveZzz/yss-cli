package domain

import "testing"

func TestWorkBuddyRuntimeRoot(t *testing.T) {
	for runtime, want := range map[string]string{
		"workbuddy": ".workbuddy/skills", "claude": ".claude/skills",
		"codex": ".codex/skills", "cursor": ".cursor/skills", "pi": ".pi/skills",
	} {
		got, ok := SkillRuntimeRoot(runtime)
		if !ok || got != want {
			t.Fatalf("runtime %q: got %q, %v; want %q", runtime, got, ok, want)
		}
		back, ok := SkillRuntimeForRoot(want)
		if !ok || back != runtime {
			t.Fatalf("root %q: got %q, %v; want %q", want, back, ok, runtime)
		}
	}
	for _, invalid := range []string{"", "WorkBuddy", "codebuddy", "../workbuddy", ".agents/skills"} {
		if root, ok := SkillRuntimeRoot(invalid); ok || root != "" {
			t.Errorf("accepted invalid runtime %q", invalid)
		}
	}
	for _, invalid := range []string{"", ".agents/skills", "../.workbuddy/skills", ".workbuddy/skills/", ".codebuddy/skills"} {
		if runtime, ok := SkillRuntimeForRoot(invalid); ok || runtime != "" {
			t.Errorf("accepted invalid root %q", invalid)
		}
	}
}
