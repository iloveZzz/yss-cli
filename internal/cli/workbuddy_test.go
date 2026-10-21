package cli

import (
	"context"
	"strings"
	"testing"
)

func TestWorkBuddyCLISelection(t *testing.T) {
	for _, runtime := range []string{"workbuddy", "codebuddy", "unknown"} {
		_, out, stderr := captureCommand(t, context.Background(), []string{"skills", "resolve", "probe", "--agent-runtime", runtime, "--root", t.TempDir(), "--json"}, false)
		invalid := strings.Contains(out+stderr, "值无效")
		if invalid != (runtime != "workbuddy") {
			t.Fatalf("runtime %s: %s %s", runtime, out, stderr)
		}
	}
	_, out, stderr := captureCommand(t, context.Background(), []string{"skills", "resolve", "--help"}, false)
	if !strings.Contains(out+stderr, "workbuddy") {
		t.Fatalf("help missing runtime: %s %s", out, stderr)
	}
	code, out, stderr := captureCommand(t, context.Background(), []string{"lifecycle", "route", "--agent-runtime", "workbuddy"}, false)
	if code == 0 || !strings.Contains(out+stderr, "agent-runtime") {
		t.Fatalf("lifecycle flag scope expanded: %d %s %s", code, out, stderr)
	}
}
