package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

// Inject cancellation or a user edit only once the local transaction has begun
// preparation, after all remote requests and their local rechecks have finished.
type trackerPreparationContext struct {
	context.Context
	mu   sync.Mutex
	root string
	once func()
}

func (c *trackerPreparationContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.once != nil {
		refs, _ := filepath.Glob(filepath.Join(c.root, ".yss/transactions/.preparing-*/plan.json"))
		if len(refs) > 0 {
			once := c.once
			c.once = nil
			once()
		}
	}
	return c.Context.Err()
}

func TestTrackerLocalFailuresKeepOriginalCodeAndTransaction(t *testing.T) {
	for _, scenario := range []string{"cancelled", "drift", "pending", "remote-and-local"} {
		t.Run(scenario, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			ref := ".work/feat/parent-ticket.md"
			for path, data := range map[string]string{
				".template-spec/agents/issue-tracker.md": "---\ntracker:\n  platform: local-markdown\n  root: .work\n  remote_mirror: true\n---\n",
				ref:                                      "# ticket\nStatus: ready-for-human\nTracker-Link: github:acme/app#12\n",
			} {
				p := filepath.Join(root, path)
				if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(data), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "remote-and-local" {
				p := filepath.Join(root, ".work/feat/issues/01.md")
				if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("# first\nStatus: ready-for-human\nTracker-Link: github:acme/app#13\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			labelsByIssue := map[string][]string{"12": {"needs-triage"}, "13": {"needs-triage"}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				number := strings.Split(strings.Split(r.URL.Path, "/issues/")[1], "/")[0]
				if scenario == "remote-and-local" && number == "12" && r.Method != http.MethodGet {
					w.WriteHeader(http.StatusUnprocessableEntity)
					return
				}
				if r.Method == http.MethodPost {
					labelsByIssue[number] = []string{"needs-triage", "ready-for-human"}
				}
				if r.Method == http.MethodDelete {
					labelsByIssue[number] = []string{"ready-for-human"}
				}
				labels := []map[string]string{}
				for _, label := range labelsByIssue[number] {
					labels = append(labels, map[string]string{"name": label})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"number": json.Number(number), "state": "open", "updated_at": "a", "labels": labels})
			}))
			defer server.Close()
			t.Setenv("GH_TOKEN", "test-token")
			t.Setenv("GITHUB_API_URL", server.URL)
			plan := filepath.Join(t.TempDir(), "plan.json")
			if exit, out, _ := captureCommand(t, context.Background(), []string{"tracker", "sync", "--root", root, "--plan", "--out", plan, "--json"}, false); exit != 0 {
				t.Fatal(out)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			applyCtx := context.Context(ctx)
			wantCode := "INTERRUPTED"
			switch scenario {
			case "cancelled":
				wantCode = "CANCELLED"
				applyCtx = &trackerPreparationContext{Context: ctx, root: root, once: cancel}
			case "drift":
				wantCode = "CONCURRENT"
				applyCtx = &trackerPreparationContext{Context: ctx, root: root, once: func() {
					if err := os.WriteFile(filepath.Join(root, ref), []byte("# user edit\nStatus: needs-info\n"), 0644); err != nil {
						t.Error(err)
					}
				}}
			default:
				// Leave a genuine published transaction blocked by a user edit.
				_, err := transaction.ApplyContextWithValidation(context.Background(), root, "tracker-link", []transaction.Operation{{Path: "blocker.txt", Data: []byte("candidate"), Mode: 0644, Before: &domain.Descriptor{Type: "missing"}}}, nil, nil, func() error {
					if err := os.WriteFile(filepath.Join(root, "blocker.txt"), []byte("user"), 0644); err != nil {
						return err
					}
					return errors.New("reject candidate")
				})
				if err == nil {
					t.Fatal("expected a blocked transaction fixture")
				}
			}
			exit, out, _ := captureCommand(t, applyCtx, []string{"tracker", "sync", "--root", root, "--apply", "--plan-file", plan, "--confirm-remote", "github", "--json"}, false)
			envelope := commandEnvelope(t, out)
			result := envelope["result"].(map[string]any)
			if scenario == "remote-and-local" {
				wantCode = "TRACKER_REMOTE"
			}
			if exit != 1 || envelope["code"] != wantCode || result["status"] != "partial" || result["remote_changed"] != true || result["local_recorded"] != false {
				t.Fatalf("original error and remote effects must survive: %d %s", exit, out)
			}
			localCode := wantCode
			if scenario == "remote-and-local" {
				localCode = "INTERRUPTED"
			}
			if result["local_error_code"] != localCode {
				t.Fatalf("local failure needs its own code: %s", out)
			}
			if scenario == "cancelled" || scenario == "pending" || scenario == "remote-and-local" {
				tx, ok := result["transaction"].(map[string]any)
				if !ok || tx["status"] == "" || scenario != "cancelled" && tx["transactionId"] == nil {
					t.Fatalf("local transaction receipt must survive failure: %s", out)
				}
			}
			raw, err := os.ReadFile(filepath.Join(root, ref))
			if err != nil || strings.Contains(string(raw), "Tracker-Synced:") || scenario == "drift" && !strings.Contains(string(raw), "# user edit") {
				t.Fatalf("failure must preserve unsynced/user bytes: %s %v", raw, err)
			}
		})
	}
}
