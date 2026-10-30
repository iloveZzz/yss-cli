//go:build !yss_nobundle

package cli

import (
	"context"
	"path/filepath"
	"testing"
)

func TestNewSpecDoesNotEnterLegacyLifecycle(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spec")
	commandOK(t, root, "init", "--profile", "spec")
	for _, args := range [][]string{{"lifecycle", "status"}, {"handoff", "check"}, {"stage", "status"}, {"profile", "prepare"}} {
		full := append(append([]string{}, args...), "--root", root, "--json")
		code, out, _ := captureCommand(t, context.Background(), full, false)
		if code == 0 || commandEnvelope(t, out)["code"] != "SPEC_COMMAND_NOT_APPLICABLE" {
			t.Fatalf("legacy command %v: %d %s", args, code, out)
		}
	}
	code, out, _ := captureCommand(t, context.Background(), []string{"project", "inspect", "--root", root, "--json"}, false)
	if code != 0 || commandEnvelope(t, out)["status"] != "ok" {
		t.Fatalf("inspect must remain available: %d %s", code, out)
	}
}
