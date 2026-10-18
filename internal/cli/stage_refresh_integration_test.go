//go:build !yss_nobundle

package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// stage refresh 与模板 Node refreshTracking 同义（已在同一状态下对拍 stage_tracking 逐字段一致）：
// 漂移项沿依赖传递，除 cancelled 外重置为 pending 并标记 recheck_required，旧绑定保留为历史。

func stagePlan(t *testing.T, dir, ref string, args ...string) {
	t.Helper()
	var out, errb bytes.Buffer
	if code := Run(context.Background(), append([]string{"stage"}, append(args, "--root", dir, "--checkpoint", ".work/f1/checkpoint.json")...), &out, &errb); code != 0 {
		t.Fatalf("stage %v: %d %s", args, code, errb.String())
	}
	writeCheckFile(t, dir, ref, out.String())
	if code, env, _ := runYSS("stage", "apply", "--root", dir, "--plan-file", ref); code != 0 {
		t.Fatalf("apply %v: %v", args, env)
	}
}

func writeJSON(t *testing.T, dir, ref string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeCheckFile(t, dir, ref, string(raw))
}

func TestStageRefreshAndFilters(t *testing.T) {
	dir := commandProject(t)
	raw, err := os.ReadFile(filepath.Join(dir, ".template-spec/process/templates/lifecycle-checkpoint-template.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cp map[string]any
	if err = json.Unmarshal(raw, &cp); err != nil {
		t.Fatal(err)
	}
	cp["feature_id"], cp["stage"], cp["mode"], cp["status"], cp["next_work_unit"] = "f1", "stage.plan", "orchestrate", "running", "work-unit.plan-requirements"
	if trace, ok := cp["stage_trace"].(map[string]any); ok {
		trace["stage"] = "stage.plan"
	}
	writeJSON(t, dir, ".work/f1/checkpoint.json", cp)
	writeCheckFile(t, dir, "docs/src.md", "v1\n")
	writeCheckFile(t, dir, "docs/src2.md", "a\n")
	writeCheckFile(t, dir, "docs/ev.md", "evidence\n")
	item := func(id, source string, deps ...string) map[string]any {
		return map[string]any{"id": id, "title": id, "owner": "pm", "scope": id, "stage": "stage.plan", "work_unit": "work-unit.plan-requirements", "acceptance": []string{"ok"}, "dependencies": deps, "source_refs": []string{source}}
	}
	// plan-dep 自身来源不变，只经依赖传递漂移；plan-cancel 同源漂移但已取消。
	writeJSON(t, dir, "docs/items.json", []any{item("plan-req", "docs/src.md"), item("plan-dep", "docs/src2.md", "plan-req"), item("plan-cancel", "docs/src.md")})
	sum := sha256.Sum256([]byte("evidence\n"))
	writeJSON(t, dir, "docs/done.json", map[string]any{"id": "plan-req", "progress": "completed", "completion": []any{map[string]any{"criterion": "ok", "evidence_refs": []any{map[string]string{"ref": "docs/ev.md", "digest": "sha256:" + hex.EncodeToString(sum[:])}}}}})
	writeJSON(t, dir, "docs/cancel.json", map[string]any{"id": "plan-cancel", "progress": "cancelled", "cancellation_reason": "不做"})
	stagePlan(t, dir, "docs/p1.json", "plan", "--items", "docs/items.json")
	stagePlan(t, dir, "docs/p2.json", "update", "--item", "docs/done.json")
	stagePlan(t, dir, "docs/p3.json", "update", "--item", "docs/cancel.json")

	writeCheckFile(t, dir, "docs/src.md", "v2\n") // 来源漂移
	if code, env, _ := runYSS("stage", "check", "--root", dir, "--checkpoint", ".work/f1/checkpoint.json"); code == 0 || env["code"] != "TRACKING_STALE" {
		t.Fatalf("漂移的完成项应被拒绝: %v", env)
	}
	if code, env, _ := runYSS("stage", "update", "--root", dir, "--checkpoint", ".work/f1/checkpoint.json", "--item", "docs/done.json", "--refresh"); code != 2 || env["code"] != "ARGUMENT" {
		t.Fatalf("refresh 只用于 plan/register: %v", env)
	}
	var out, errb bytes.Buffer
	if code := Run(context.Background(), []string{"stage", "plan", "--root", dir, "--checkpoint", ".work/f1/checkpoint.json", "--refresh"}, &out, &errb); code != 0 {
		t.Fatalf("refresh plan: %s", errb.String())
	}
	writeCheckFile(t, dir, "docs/refresh.json", out.String())
	if code, env, _ := runYSS("stage", "apply", "--root", dir, "--plan-file", "docs/refresh.json", "--refresh"); code != 2 {
		t.Fatalf("apply 不接受 --refresh: %v", env)
	}
	if code, env, _ := runYSS("stage", "apply", "--root", dir, "--plan-file", "docs/refresh.json"); code != 0 {
		t.Fatalf("应用 refresh 计划（重建计划须带回 refresh）: %v", env)
	}

	code, env, _ := runYSS("stage", "query", "--root", dir, "--checkpoint", ".work/f1/checkpoint.json", "--stage", "stage.plan")
	if code != 0 {
		t.Fatalf("query: %v", env)
	}
	state := map[string]string{}
	for _, raw := range env["result"].(map[string]any)["items"].([]any) {
		it := raw.(map[string]any)
		state[it["id"].(string)] = it["progress"].(string) + map[bool]string{true: "/recheck"}[it["recheck_required"] == true]
	}
	want := map[string]string{"plan-req": "pending/recheck", "plan-dep": "pending/recheck", "plan-cancel": "cancelled"}
	for id, w := range want {
		if state[id] != w {
			t.Errorf("%s: %s，期望 %s（全部：%v）", id, state[id], w, state)
		}
	}
	code, env, _ = runYSS("stage", "query", "--root", dir, "--checkpoint", ".work/f1/checkpoint.json", "--work-unit", "work-unit.spec-synthesis")
	if items, _ := env["result"].(map[string]any)["items"].([]any); code != 0 || len(items) != 0 {
		t.Errorf("不匹配的工作单元应返回空 items: %v", env)
	}
}
