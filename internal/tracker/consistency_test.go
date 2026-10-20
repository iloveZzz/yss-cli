package tracker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncStopsOnLocalDriftAndPreservesConcurrentLabels(t *testing.T) {
	for _, scenario := range []string{"during-preflight", "before-write", "after-write", "unrelated-label"} {
		t.Run(scenario, func(t *testing.T) {
			root := project(t, true)
			ref := ".work/feat/parent-ticket.md"
			gh := &fakeGitHub{labels: map[string][]string{"acme/app/issues/12": {"bug", "needs-triage"}}, updated: map[string]string{"acme/app/issues/12": "a"}}
			gets, changed := 0, false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mutate := false
				if r.Method == http.MethodGet {
					gets++
					mutate = scenario == "during-preflight" && gets == 2 || scenario == "before-write" && gets == 3
				}
				if r.Method != http.MethodGet && !changed {
					if scenario == "after-write" {
						mutate = true
					}
					if scenario == "unrelated-label" {
						gh.mu.Lock()
						gh.labels["acme/app/issues/12"] = append(gh.labels["acme/app/issues/12"], "external")
						gh.mu.Unlock()
						changed = true
					}
				}
				if mutate {
					p := filepath.Join(root, ref)
					raw, _ := os.ReadFile(p)
					if err := os.WriteFile(p, []byte(strings.ReplaceAll(string(raw), "ready-for-human", "needs-info")), 0644); err != nil {
						t.Error(err)
					}
					changed = true
				}
				gh.ServeHTTP(w, r)
			}))
			defer server.Close()
			e := env(server)
			if _, err := Run(context.Background(), "link", root, map[string]string{"ticket": ref, "issue": "acme/app#12", "apply": "true"}, Env{}); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "plan.json")
			if _, err := Run(context.Background(), "sync", root, map[string]string{"plan": "true", "out": file}, e); err != nil {
				t.Fatal(err)
			}
			out, err := Run(context.Background(), "sync", root, map[string]string{"apply": "true", "plan-file": file, "confirm-remote": "github"}, e)
			raw, _ := os.ReadFile(filepath.Join(root, ref))
			if scenario == "unrelated-label" {
				if err != nil || !contains(gh.labels["acme/app/issues/12"], "external") || !contains(gh.labels["acme/app/issues/12"], "bug") {
					t.Fatalf("unrelated concurrent label must survive: %v %v", gh.labels, err)
				}
				return
			}
			if err == nil || strings.Contains(string(raw), "Tracker-Synced:") || !strings.Contains(string(raw), "Status: needs-info") {
				t.Fatalf("drift must preserve user bytes and refuse a sync marker: %v %s %v", err, raw, out)
			}
			if scenario != "after-write" && gh.patches != 0 {
				t.Fatalf("drift before writes must stop remote writes: %d", gh.patches)
			}
			if scenario == "after-write" {
				result, ok := out.(map[string]any)
				if !ok || result["status"] != "partial" || result["remote_changed"] != true || result["local_recorded"] != false {
					t.Fatalf("post-write failure must preserve remote effects: %v", out)
				}
			}
		})
	}
}
