package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrackerPartialFailurePreservesEnvelope(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		".template-spec/agents/issue-tracker.md": "---\ntracker:\n  platform: local-markdown\n  root: .work\n  remote_mirror: true\n---\n",
		".work/feat/issues/01.md":                "# first\nStatus: needs-info\nTracker-Link: github:acme/app#13\n",
		".work/feat/parent-ticket.md":            "# second\nStatus: ready-for-human\nTracker-Link: github:acme/app#12\n",
	}
	for ref, data := range files {
		p := filepath.Join(root, ref)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && strings.HasSuffix(r.URL.Path, "/12") {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		number := 13
		if strings.HasSuffix(r.URL.Path, "/12") {
			number = 12
		}
		labels := []map[string]string{{"name": "needs-triage"}}
		if r.Method != http.MethodGet {
			labels = []map[string]string{{"name": "needs-info"}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"number": number, "state": "open", "updated_at": "a", "labels": labels})
	}))
	defer server.Close()
	t.Setenv("GH_TOKEN", "test-token")
	t.Setenv("GITHUB_API_URL", server.URL)
	plan := filepath.Join(t.TempDir(), "plan.json")
	ctx := context.Background()
	if code, out, _ := captureCommand(t, ctx, []string{"tracker", "sync", "--root", root, "--plan", "--out", plan, "--json"}, false); code != 0 {
		t.Fatal(out)
	}
	exit, out, _ := captureCommand(t, ctx, []string{"tracker", "sync", "--root", root, "--apply", "--plan-file", plan, "--confirm-remote", "github", "--json"}, false)
	env := commandEnvelope(t, out)
	result := env["result"].(map[string]any)
	if exit != 1 || env["code"] != "TRACKER_REMOTE" || result["status"] != "partial" || result["remote_changed"] != true || result["remote_state"] != "changed" || result["items"] == nil || result["local_recorded"] != true {
		t.Fatal(fmt.Sprintf("partial effects must survive CLI envelope: %d %s", exit, out))
	}
}
