//go:build !yss_nobundle

package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// standard 路线：固定 Bundle 的模板尚未带 standard 策略，用例在实例副本里注入策略块；
// 未注入的共享实例同时用来证明“未启用时 standard 记录走 governed”。

const standardPolicy = `    standard:
      version: 1
      impacts:
        - behavior-change
        - new-capability
      decision:
        id: standard-change-approved
        actor_kind: human
`

var (
	standardOnce    sync.Once
	standardProject string
	standardErr     error
)

func standardFixtureFor(t *testing.T) routeFixture {
	t.Helper()
	f := routeFixtureFor(t)
	standardOnce.Do(func() {
		dir := filepath.Join(fixtureDir, "standard")
		if out, err := exec.Command("cp", "-a", f.project, dir).CombinedOutput(); err != nil {
			standardErr = err
			t.Log(string(out))
			return
		}
		ref := filepath.Join(dir, ".template-spec/process/orchestration/entry/request-triage.yaml")
		raw, err := os.ReadFile(ref)
		if err != nil {
			standardErr = err
			return
		}
		text := strings.Replace(string(raw), "  delivery_path:\n    version: 1\n", "  delivery_path:\n    version: 1\n"+standardPolicy, 1)
		if text == string(raw) {
			standardErr = os.ErrInvalid
			return
		}
		standardErr = os.WriteFile(ref, []byte(text), 0o644)
		standardProject = dir
	})
	if standardErr != nil {
		t.Fatal(standardErr)
	}
	f.project = standardProject
	return f
}

func digestOf(t *testing.T, project, ref, content string) string {
	t.Helper()
	writeCheckFile(t, project, ref, content)
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// standardEvidence 给记录补上 Spec-lite 与绑定其摘要的生物人决定。
func standardEvidence(t *testing.T, f routeFixture, id string) func(map[string]any) {
	dir := ".work/" + strings.ToLower(id)
	spec := digestOf(t, f.project, dir+"/spec-lite.md", "# Spec-lite\n\n目标：a.txt 增加 b。\n")
	reply := digestOf(t, f.project, dir+"/decision-reply.md", "同意按 Spec-lite 实施。—— 张三\n")
	return func(e map[string]any) {
		e["delivery_path"] = "standard"
		e["scope"].(map[string]any)["impacts"] = []any{"behavior-change"}
		e["spec_lite"] = map[string]any{"ref": dir + "/spec-lite.md", "digest": spec}
		e["standard_decision"] = map[string]any{"id": "standard-change-approved", "decision": "approved", "actor_kind": "human", "decided_by": "zhangsan", "spec_lite_digest": spec, "response_ref": dir + "/decision-reply.md", "response_digest": reply}
	}
}

func TestStandardRouteOnEnabledPolicy(t *testing.T) {
	f := standardFixtureFor(t)
	decision := func(id string, change func(map[string]any)) func(map[string]any) {
		base := standardEvidence(t, f, id)
		return func(e map[string]any) {
			base(e)
			if change != nil {
				change(e)
			}
		}
	}
	for _, tc := range []struct {
		name, id, want string
		code           int
		mutate         func(map[string]any)
	}{
		{"齐全的 standard 记录走 standard", "STD-1", "standard", 0, decision("STD-1", nil)},
		{"daily 记录命中 standard 影响时升级而非放行", "STD-2", "standard", 0, func(e map[string]any) {
			e["scope"].(map[string]any)["impacts"] = []any{"behavior-change"}
		}},
		{"standard 记录不放宽排除影响", "STD-3", "governed", 0, decision("STD-3", func(e map[string]any) {
			e["scope"].(map[string]any)["impacts"] = []any{"behavior-change", "data-migration"}
		})},
		{"决定未绑定当前 Spec-lite 摘要被拒绝", "STD-4", "needs-info", 1, decision("STD-4", func(e map[string]any) {
			e["standard_decision"].(map[string]any)["spec_lite_digest"] = "sha256:" + strings.Repeat("1", 64)
		})},
		{"数字人不能作出 standard 决定", "STD-5", "needs-info", 1, decision("STD-5", func(e map[string]any) {
			e["standard_decision"].(map[string]any)["actor_kind"] = "digital-human"
		})},
		{"缺少 Spec-lite 时需要补事实", "STD-6", "needs-info", 2, decision("STD-6", func(e map[string]any) { delete(e, "spec_lite") })},
		{"daily 级影响也可以声明 standard（只升不降）", "STD-7", "standard", 0, decision("STD-7", func(e map[string]any) {
			e["scope"].(map[string]any)["impacts"] = []any{"no-api"}
		})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, path, env := route(t, f, writeTask(t, f, tc.id, tc.mutate))
			if code != tc.code || path != tc.want {
				t.Errorf("退出码=%d 路径=%q，期望 %d / %q；%s", code, path, tc.code, tc.want, summary(env))
			}
			if tc.id == "STD-2" && env["result"].(map[string]any)["standard_required"] != true {
				t.Errorf("升级须标记 standard_required: %s", summary(env))
			}
		})
	}
}

func TestStandardEscalationBlocksVerifyDaily(t *testing.T) {
	f := standardFixtureFor(t)
	ref := writeTask(t, f, "STD-8", func(e map[string]any) {
		e["scope"].(map[string]any)["impacts"] = []any{"new-capability"}
	})
	code, env, err := runYSS("lifecycle", "verify-daily", "--root", f.project, "--task", ref, "--implementation-root", f.impl, "--base", f.base)
	if err != nil {
		t.Fatal(err)
	}
	if code != 1 || env["code"] != "STANDARD_REQUIRED" {
		t.Fatalf("daily 记录命中 standard 影响时 verify-daily 必须拒绝: 退出码=%d %s", code, summary(env))
	}
}

func TestStandardDisabledFallsBackToGoverned(t *testing.T) {
	f := routeFixtureFor(t)
	for _, tc := range []struct {
		id     string
		mutate func(map[string]any)
	}{
		{"STD-OFF-1", standardEvidence(t, f, "STD-OFF-1")},
		{"STD-OFF-2", func(e map[string]any) { e["scope"].(map[string]any)["impacts"] = []any{"behavior-change"} }},
	} {
		code, path, env := route(t, f, writeTask(t, f, tc.id, tc.mutate))
		if code != 0 || path != "governed" {
			t.Errorf("%s: 未启用 standard 时必须走 governed：退出码=%d 路径=%q；%s", tc.id, code, path, summary(env))
		}
	}
}
