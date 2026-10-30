//go:build !yss_nobundle

package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

// 当前受信 Bundle 内的 standard 策略支持标准路由；对受管政策的本地修改须拒绝。

var standardPolicyBlock = regexp.MustCompile(`(?m)^    standard:\n(?:      .*\n|\n)*`)

type triageVariant struct {
	once    sync.Once
	project string
	err     error
}

var standardEnabled, standardDisabled triageVariant

// fixture 复制共享实例并改写其 request-triage.yaml；改写未生效即失败，不静默沿用原策略。
func (v *triageVariant) fixture(t *testing.T, name string, rewrite func(string) string) routeFixture {
	t.Helper()
	f := routeFixtureFor(t)
	v.once.Do(func() {
		dir := filepath.Join(fixtureDir, name)
		if out, err := exec.Command("cp", "-a", f.project, dir).CombinedOutput(); err != nil {
			v.err = err
			t.Log(string(out))
			return
		}
		// Init journals bind an absolute root; a new fixture must not replay the parent's transaction.
		if err := os.RemoveAll(filepath.Join(dir, ".yss")); err != nil {
			v.err = err
			return
		}
		ref := filepath.Join(dir, ".template-spec/process/orchestration/entry/request-triage.yaml")
		raw, err := os.ReadFile(ref)
		if err != nil {
			v.err = err
			return
		}
		text := rewrite(string(raw))
		if text == "" {
			v.err = os.ErrInvalid
			return
		}
		v.err = os.WriteFile(ref, []byte(text), 0o644)
		v.project = dir
	})
	if v.err != nil {
		t.Fatal(v.err)
	}
	f.project = v.project
	return f
}

func standardFixtureFor(t *testing.T) routeFixture {
	return standardEnabled.fixture(t, "standard", func(raw string) string {
		if !standardPolicyBlock.MatchString(raw) {
			return ""
		}
		return raw
	})
}

func standardDisabledFixtureFor(t *testing.T) routeFixture {
	return standardDisabled.fixture(t, "standard-disabled", func(raw string) string {
		if !standardPolicyBlock.MatchString(raw) {
			return "" // fixture must actually remove the installed policy
		}
		return standardPolicyBlock.ReplaceAllString(raw, "")
	})
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
	// Synthetic source messages exercise the real policy2 reader; a bare signature is no longer approval evidence.
	writeJSON := func(ref string, value any) string {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return digestOf(t, f.project, ref, string(raw))
	}
	capture := func(name, actor, principal, at, text, replyTo string) map[string]any {
		ref := dir + "/" + name + ".json"
		message := map[string]any{"id": name, "actor_kind": actor, "principal_ref": principal, "sent_at": at, "text": text, "original_ref": "test-only://" + id + "/" + name}
		if replyTo != "" {
			message["reply_to"] = replyTo
		}
		digest := writeJSON(ref, map[string]any{"source_kind": "session-export", "messages": []any{message}})
		return map[string]any{"kind": "session-export", "ref": ref, "digest": digest, "message_id": name}
	}
	request := map[string]any{"id": id, "requester_ref": "zhangsan", "requester_source": capture("request", "biological-human", "zhangsan", "2026-01-01T00:00:00Z", "请准备本次具体方案。", ""), "items": []any{map[string]any{"id": "D1", "boundary": "standard-change-approved", "title": "确认范围", "subject": map[string]any{"ref": dir + "/spec-lite.md", "version": "v1", "digest": spec}, "basis": []any{}, "scope": []string{"a.txt"}, "changes": "增加 b", "risks": []any{}, "recommendation": "批准", "next_actions": []string{"修改 a.txt"}, "responder_ref": "zhangsan"}}}
	requestDigest := kernel.ApDigest(request)
	presentation := fmt.Sprintf("决定请求 %s (%s)\n提问者：zhangsan\n事项 D1 / standard-change-approved：确认范围\n资产：%s/spec-lite.md；版本：v1；摘要：%s\n依据：无额外依据\n关键变化：增加 b\n范围：a.txt\n风险：已评估，无已知残余风险\n推荐方案：批准\n批准后动作：修改 a.txt\n回复负责人：zhangsan\n请明确批准、拒绝或提出修改；多事项回复须说明事项，或明确批准以上全部事项。", id, requestDigest, dir, spec)
	request["presented_source"] = capture("presentation", "agent", "agent", "2026-01-01T00:01:00Z", presentation, "")
	responseSource := capture("response", "biological-human", "zhangsan", "2026-01-01T00:02:00Z", "批准", "test-only://"+id+"/presentation")
	replyRef := dir + "/decision.json"
	reply := writeJSON(replyRef, map[string]any{"schema_version": 1, "kind": "user-decision", "request": request, "responses": []any{map[string]any{"source": responseSource, "principal_ref": "zhangsan", "responded_at": "2026-01-01T00:02:00Z", "text": "批准", "request_digest": requestDigest, "selection": "single", "item_ids": []string{"D1"}, "decision": "approved", "approved_scope": []string{"a.txt"}}}})
	return func(e map[string]any) {
		e["delivery_path"] = "standard"
		e["scope"].(map[string]any)["impacts"] = []any{"behavior-change"}
		e["spec_lite"] = map[string]any{"ref": dir + "/spec-lite.md", "digest": spec}
		e["standard_decision"] = map[string]any{"id": "standard-change-approved", "decision": "approved", "actor_kind": "human", "decided_by": "zhangsan", "spec_lite_digest": spec, "response_ref": replyRef, "response_digest": reply}
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

func TestStandardPolicyRemovalRejectsRoutingWithoutWriting(t *testing.T) {
	f := standardDisabledFixtureFor(t)
	for _, tc := range []struct {
		id     string
		mutate func(map[string]any)
	}{
		{"STD-OFF-1", standardEvidence(t, f, "STD-OFF-1")},
		{"STD-OFF-2", func(e map[string]any) { e["scope"].(map[string]any)["impacts"] = []any{"behavior-change"} }},
	} {
		ref := writeTask(t, f, tc.id, tc.mutate)
		policy := filepath.Join(f.project, ".template-spec/process/orchestration/entry/request-triage.yaml")
		task := filepath.Join(f.project, ref)
		files := []string{policy, task, filepath.Join(f.project, ".yss.json")}
		before := map[string][]byte{}
		for _, file := range files {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			before[file] = raw
		}
		code, path, env := route(t, f, ref)
		if code == 0 || env["code"] != "GOVERNANCE_POLICY" || !strings.Contains(summary(env), "request-triage.yaml") {
			t.Errorf("%s: 修改受管政策必须拒绝路由：退出码=%d 路径=%q；%s", tc.id, code, path, summary(env))
		}
		for _, file := range files {
			raw, err := os.ReadFile(file)
			if err != nil || string(raw) != string(before[file]) {
				t.Fatalf("拒绝后文件变化 %s: %v", file, err)
			}
		}
	}
}
