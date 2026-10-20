package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func syncFixture(t *testing.T) (string, string, Env, *fakeGitHub) {
	t.Helper()
	root := project(t, true)
	gh := &fakeGitHub{labels: map[string][]string{"acme/app/issues/12": {"bug", "needs-triage"}}, updated: map[string]string{"acme/app/issues/12": "a"}}
	server := httptest.NewServer(gh)
	t.Cleanup(server.Close)
	e := env(server)
	if _, err := Run(context.Background(), "link", root, map[string]string{"ticket": ".work/feat/parent-ticket.md", "issue": "acme/app#12", "apply": "true"}, Env{}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "plan.json")
	if _, err := Run(context.Background(), "sync", root, map[string]string{"plan": "true", "out": file}, e); err != nil {
		t.Fatal(err)
	}
	return root, file, e, gh
}

func TestSyncPlanBindsV2SourceAndSelection(t *testing.T) {
	_, file, e, _ := syncFixture(t)
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p["schema_version"] != float64(2) || p["api_base"] != e.getenv("GITHUB_API_URL") || p["selection"] == nil || strings.Contains(string(raw), "test-token") {
		t.Fatalf("v2 must bind source and selection without credentials: %s", raw)
	}
}

func TestSyncRejectsRehashedIllegalPlan(t *testing.T) {
	for _, name := range []string{"labels", "issue", "duplicate", "v1", "unknown"} {
		t.Run(name, func(t *testing.T) {
			root, file, e, gh := syncFixture(t)
			raw, _ := os.ReadFile(file)
			var p SyncPlan
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "labels":
				p.Items[0].Desired = []string{"arbitrary"}
				p.Items[0].Add = []string{"arbitrary"}
				p.Items[0].Remove = []string{"bug", "needs-triage"}
			case "issue":
				p.Items[0].Repository = "other/repo"
			case "duplicate":
				p.Items = append(p.Items, p.Items[0])
			case "v1":
				p.SchemaVersion = 1
			}
			p.PlanDigest = p.digest()
			raw, _ = json.Marshal(p)
			if name == "unknown" {
				raw = append([]byte(`{"unrecognized":true,`), raw[1:]...)
			}
			if err := os.WriteFile(file, raw, 0600); err != nil {
				t.Fatal(err)
			}
			_, err := Run(context.Background(), "sync", root, map[string]string{"apply": "true", "plan-file": file, "confirm-remote": "github"}, e)
			if code(err) != "PLAN" || gh.patches != 0 {
				t.Fatalf("illegal plan must fail before writes: %v writes=%d", err, gh.patches)
			}
		})
	}
}

func TestSyncRejectsChangedAPIBase(t *testing.T) {
	root, file, e, gh := syncFixture(t)
	other := httptest.NewServer(gh)
	defer other.Close()
	previous := e.Getenv
	e.Getenv = func(k string) string {
		if k == "GITHUB_API_URL" {
			return other.URL
		}
		return previous(k)
	}
	_, err := Run(context.Background(), "sync", root, map[string]string{"apply": "true", "plan-file": file, "confirm-remote": "github"}, e)
	if code(err) != "PLAN" || gh.patches != 0 {
		t.Fatalf("changed API must be rejected: %v", err)
	}
}

func TestSyncReportsUnknownWriteWithoutRetry(t *testing.T) {
	root := project(t, true)
	gh := &fakeGitHub{labels: map[string][]string{"acme/app/issues/12": {"needs-triage"}}, updated: map[string]string{"acme/app/issues/12": "a"}}
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gh.ServeHTTP(w, r)
			return
		}
		writes++
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = connection.Close()
	}))
	defer server.Close()
	e := env(server)
	if _, err := Run(context.Background(), "link", root, map[string]string{"ticket": ".work/feat/parent-ticket.md", "issue": "acme/app#12", "apply": "true"}, Env{}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "plan.json")
	if _, err := Run(context.Background(), "sync", root, map[string]string{"plan": "true", "out": file}, e); err != nil {
		t.Fatal(err)
	}
	out, err := Run(context.Background(), "sync", root, map[string]string{"apply": "true", "plan-file": file, "confirm-remote": "github"}, e)
	result, ok := out.(map[string]any)
	if code(err) != "NETWORK" || !ok || result["remote_state"] != "unknown" || result["local_recorded"] != false || writes != 1 {
		t.Fatalf("disconnected write must remain unknown and never retry: %v %v writes=%d", out, err, writes)
	}
	var receipt interface{ ErrorResult() any }
	if !errors.As(err, &receipt) {
		t.Fatal("error must preserve result")
	}
}
