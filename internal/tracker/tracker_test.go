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
	"sync"
	"testing"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

// fakeGitHub 只实现 GET / PATCH /repos/{o}/{r}/issues/{n}，记录写入次数。
type fakeGitHub struct {
	mu      sync.Mutex
	labels  map[string][]string
	updated map[string]string
	patches int
	fail    string
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer test-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/repos/")
	if _, ok := f.labels[key]; !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method == http.MethodPatch {
		if key == f.fail {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		var body struct {
			Labels []string `json:"labels"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.labels[key] = body.Labels
		f.updated[key] = "2026-10-17T10:00:0" + string(rune('0'+f.patches)) + "Z"
		f.patches++
	}
	labels := []map[string]string{}
	for _, l := range f.labels[key] {
		labels = append(labels, map[string]string{"name": l})
	}
	number := key[strings.LastIndex(key, "/")+1:]
	_ = json.NewEncoder(w).Encode(map[string]any{"number": json.Number(number), "state": "open", "updated_at": f.updated[key], "html_url": "https://github.com/" + key, "labels": labels})
}

func project(t *testing.T, mirror bool) string {
	t.Helper()
	root := t.TempDir()
	write := func(ref, content string) {
		path := filepath.Join(root, ref)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	flag := "false"
	if mirror {
		flag = "true"
	}
	write(".template-spec/agents/issue-tracker.md", "---\ntracker:\n  lifecycle_tracking_version: 1\n  platform: local-markdown\n  root: .work\n  remote_mirror: "+flag+"\n---\n# tracker\n")
	write(".work/feat/parent-ticket.md", "# 功能 A\n\nStatus: ready-for-human\n\n正文\n")
	write(".work/feat/issues/01-slice.md", "# 切片 1\n\nStatus: claimed\n")
	return root
}

func env(server *httptest.Server) Env {
	vars := map[string]string{"GH_TOKEN": "test-token", "GITHUB_API_URL": server.URL}
	return Env{Getenv: func(k string) string { return vars[k] }, Now: func() time.Time { return time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC) }}
}

func code(err error) string {
	var de *domain.Error
	if errors.As(err, &de) {
		return de.Code
	}
	return ""
}

func TestLinkPreviewApplyAndConflict(t *testing.T) {
	root := project(t, true)
	ctx := context.Background()
	ref := ".work/feat/parent-ticket.md"
	out, err := Run(ctx, "link", root, map[string]string{"ticket": ref, "issue": "acme/app#12"}, Env{})
	if err != nil || out.(map[string]any)["status"] != "planned" {
		t.Fatalf("默认只预览: %v %v", out, err)
	}
	if raw, _ := os.ReadFile(filepath.Join(root, ref)); strings.Contains(string(raw), "Tracker-Link") {
		t.Fatal("预览不得写文件")
	}
	if _, err = Run(ctx, "link", root, map[string]string{"ticket": ref, "issue": "acme/app#12", "apply": "true"}, Env{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(root, ref))
	if !strings.Contains(string(raw), "Status: ready-for-human\nTracker-Link: github:acme/app#12\n") {
		t.Fatalf("链接行应紧跟 Status: %s", raw)
	}
	if _, err = Run(ctx, "link", root, map[string]string{"ticket": ref, "issue": "acme/app#13", "apply": "true"}, Env{}); code(err) != "TRACKER_CONFLICT" {
		t.Fatalf("不覆盖已有不同链接: %v", err)
	}
	for _, args := range []map[string]string{
		{"ticket": "README.md", "issue": "acme/app#1"},
		{"ticket": ref, "issue": "acme/app"},
		{"ticket": ref, "issue": "-x/app#1"},
	} {
		if _, err = Run(ctx, "link", root, args, Env{}); err == nil {
			t.Errorf("应拒绝 %v", args)
		}
	}
}

func TestSyncPlanConfirmApplyAndRecord(t *testing.T) {
	root := project(t, true)
	ctx := context.Background()
	gh := &fakeGitHub{labels: map[string][]string{"acme/app/issues/12": {"bug", "needs-triage"}}, updated: map[string]string{"acme/app/issues/12": "2026-10-16T00:00:00Z"}}
	server := httptest.NewServer(gh)
	defer server.Close()
	if _, err := Run(ctx, "link", root, map[string]string{"ticket": ".work/feat/parent-ticket.md", "issue": "acme/app#12", "apply": "true"}, Env{}); err != nil {
		t.Fatal(err)
	}
	planFile := filepath.Join(t.TempDir(), "plan.json")
	out, err := Run(ctx, "sync", root, map[string]string{"plan": "true", "out": planFile}, env(server))
	if err != nil {
		t.Fatal(err)
	}
	plan := out.(map[string]any)["plan"].(SyncPlan)
	if len(plan.Items) != 1 || strings.Join(plan.Items[0].Add, ",") != "ready-for-human" || strings.Join(plan.Items[0].Remove, ",") != "needs-triage" {
		t.Fatalf("计划应只替换五态标签、保留 bug: %+v", plan.Items)
	}
	if len(plan.Skipped) != 1 || !strings.Contains(plan.Skipped[0].Reason, "未链接") {
		t.Fatalf("未链接 Ticket 应跳过: %+v", plan.Skipped)
	}
	if _, err = Run(ctx, "sync", root, map[string]string{"apply": "true", "plan-file": planFile}, env(server)); code(err) != "CAPABILITY" || gh.patches != 0 {
		t.Fatalf("缺少 --confirm-remote 不得触达远端: %v patches=%d", err, gh.patches)
	}
	out, err = Run(ctx, "sync", root, map[string]string{"apply": "true", "plan-file": planFile, "confirm-remote": "github"}, env(server))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(gh.labels["acme/app/issues/12"], ","); got != "bug,ready-for-human" || gh.patches != 1 {
		t.Fatalf("远端标签: %s patches=%d", got, gh.patches)
	}
	raw, _ := os.ReadFile(filepath.Join(root, ".work/feat/parent-ticket.md"))
	if !strings.Contains(string(raw), "Tracker-Synced: 2026-10-17T09:00:00Z labels=bug,ready-for-human") {
		t.Fatalf("本地应记录同步: %s", raw)
	}
	if out.(map[string]any)["local_recorded"] != true {
		t.Fatalf("结果应声明已记录本地: %v", out)
	}
	if _, err = Run(ctx, "sync", root, map[string]string{"apply": "true", "plan-file": planFile, "confirm-remote": "github"}, env(server)); code(err) != "INPUT_DRIFT" {
		t.Fatalf("同一计划重放应因本地变化拒绝: %v", err)
	}
}

func TestSyncRefusesRemoteDriftAndDisabledMirror(t *testing.T) {
	ctx := context.Background()
	gh := &fakeGitHub{labels: map[string][]string{"acme/app/issues/12": {"needs-triage"}}, updated: map[string]string{"acme/app/issues/12": "2026-10-16T00:00:00Z"}}
	server := httptest.NewServer(gh)
	defer server.Close()

	off := project(t, false)
	if _, err := Run(ctx, "sync", off, map[string]string{"plan": "true"}, env(server)); code(err) != "TRACKER" {
		t.Fatalf("未启用 remote_mirror 时拒绝: %v", err)
	}

	root := project(t, true)
	if _, err := Run(ctx, "link", root, map[string]string{"ticket": ".work/feat/parent-ticket.md", "issue": "acme/app#12", "apply": "true"}, Env{}); err != nil {
		t.Fatal(err)
	}
	planFile := filepath.Join(t.TempDir(), "plan.json")
	if _, err := Run(ctx, "sync", root, map[string]string{"plan": "true", "out": planFile}, env(server)); err != nil {
		t.Fatal(err)
	}
	gh.mu.Lock()
	gh.labels["acme/app/issues/12"] = []string{"needs-info"} // 规划后他人改了远端
	gh.updated["acme/app/issues/12"] = "2026-10-17T08:00:00Z"
	gh.mu.Unlock()
	if _, err := Run(ctx, "sync", root, map[string]string{"apply": "true", "plan-file": planFile, "confirm-remote": "github"}, env(server)); code(err) != "TRACKER_DRIFT" || gh.patches != 0 {
		t.Fatalf("远端漂移须拒绝且不写入: %v patches=%d", err, gh.patches)
	}
	if _, err := Run(ctx, "sync", root, map[string]string{"plan": "true", "out": filepath.Join(root, "plan.json")}, env(server)); code(err) != "PATH" {
		t.Fatalf("计划不得保存在项目内: %v", err)
	}
	noToken := Env{Getenv: func(string) string { return "" }}
	if _, err := Run(ctx, "sync", root, map[string]string{"plan": "true"}, noToken); code(err) != "TRACKER_AUTH" {
		t.Fatalf("缺少令牌: %v", err)
	}
}

func TestStatusReportsRemoteAgreement(t *testing.T) {
	ctx := context.Background()
	gh := &fakeGitHub{labels: map[string][]string{"acme/app/issues/12": {"ready-for-human"}}, updated: map[string]string{"acme/app/issues/12": "x"}}
	server := httptest.NewServer(gh)
	defer server.Close()
	root := project(t, true)
	if _, err := Run(ctx, "link", root, map[string]string{"ticket": ".work/feat/parent-ticket.md", "issue": "acme/app#12", "apply": "true"}, Env{}); err != nil {
		t.Fatal(err)
	}
	out, err := Run(ctx, "status", root, map[string]string{"fetch": "true"}, env(server))
	if err != nil {
		t.Fatal(err)
	}
	rows := out.(map[string]any)["tickets"].([]map[string]any)
	var parent map[string]any
	for _, row := range rows {
		if row["ticket"].(Ticket).Ref == ".work/feat/parent-ticket.md" {
			parent = row
		}
	}
	if len(rows) != 2 || parent["in_sync"] != true {
		t.Fatalf("链接 Ticket 应与远端一致: %v", rows)
	}
	if out.(map[string]any)["read_only"] != true || gh.patches != 0 {
		t.Fatal("status 只读")
	}
}

func TestSyncPartialFailureRecordsOnlySucceeded(t *testing.T) {
	ctx := context.Background()
	gh := &fakeGitHub{
		labels:  map[string][]string{"acme/app/issues/12": {"needs-triage"}, "acme/app/issues/13": {"needs-triage"}},
		updated: map[string]string{"acme/app/issues/12": "a", "acme/app/issues/13": "b"},
		fail:    "acme/app/issues/12",
	}
	server := httptest.NewServer(gh)
	defer server.Close()
	root := project(t, true)
	slice := filepath.Join(root, ".work/feat/issues/01-slice.md")
	if err := os.WriteFile(slice, []byte("# 切片 1\n\nStatus: needs-info\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for ref, issue := range map[string]string{".work/feat/issues/01-slice.md": "acme/app#13", ".work/feat/parent-ticket.md": "acme/app#12"} {
		if _, err := Run(ctx, "link", root, map[string]string{"ticket": ref, "issue": issue, "apply": "true"}, Env{}); err != nil {
			t.Fatal(err)
		}
	}
	planFile := filepath.Join(t.TempDir(), "plan.json")
	if _, err := Run(ctx, "sync", root, map[string]string{"plan": "true", "out": planFile}, env(server)); err != nil {
		t.Fatal(err)
	}
	// 计划按路径排序：先 issues/01-slice.md（#13 成功），再 parent-ticket.md（#12 失败）。
	out, err := Run(ctx, "sync", root, map[string]string{"apply": "true", "plan-file": planFile, "confirm-remote": "github"}, env(server))
	if code(err) != "TRACKER_REMOTE" {
		t.Fatalf("远端失败应如实返回: %v", err)
	}
	result := out.(map[string]any)
	if result["status"] != "partial" || result["remote_changed"] != true || result["local_recorded"] != true {
		t.Fatalf("部分完成须如实报告: %v", result)
	}
	sliceRaw, _ := os.ReadFile(slice)
	parentRaw, _ := os.ReadFile(filepath.Join(root, ".work/feat/parent-ticket.md"))
	if !strings.Contains(string(sliceRaw), "Tracker-Synced:") || strings.Contains(string(parentRaw), "Tracker-Synced:") {
		t.Fatalf("只记录成功项: slice=%s parent=%s", sliceRaw, parentRaw)
	}
}
