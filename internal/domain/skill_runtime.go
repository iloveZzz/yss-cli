package domain

// SkillRuntimeRoot names a platform projection; the canonical source is not a runtime.
func SkillRuntimeRoot(runtime string) (string, bool) {
	roots := map[string]string{
		"claude": ".claude/skills", "codex": ".codex/skills",
		"cursor": ".cursor/skills", "pi": ".pi/skills", "workbuddy": ".codebuddy/skills",
	}
	root, ok := roots[runtime]
	return root, ok
}

func SkillRuntimeForRoot(root string) (string, bool) {
	for _, runtime := range []string{"claude", "codex", "cursor", "pi", "workbuddy"} {
		if candidate, _ := SkillRuntimeRoot(runtime); candidate == root {
			return runtime, true
		}
	}
	return "", false
}
