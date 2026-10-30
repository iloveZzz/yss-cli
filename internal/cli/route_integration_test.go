//go:build !yss_nobundle

package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// 端到端：用内嵌 Bundle 初始化真实 backend 实例，再对同一实现仓走 lifecycle route。
// 需要先 node tools/fetch-bundle.mjs；yss 读取实现仓时要求 Git 支持 --no-lazy-fetch（2.44+），否则跳过。

type routeFixture struct {
	project, impl, base, skillRef, skillDigest string
	inputRef, inputDigest                      string
}

var (
	fixtureOnce     sync.Once
	fixtureDir      string
	fixture         routeFixture
	fixtureErr      error
	backendSeedOnce sync.Once
	backendSeed     string
	backendSeedErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if fixtureDir != "" {
		os.RemoveAll(fixtureDir)
	}
	os.Exit(code)
}

func requireModernGit(t *testing.T) {
	t.Helper()
	if exec.Command("git", "--no-lazy-fetch", "version").Run() != nil {
		// CI 设置 YSS_TEST_REQUIRE_GIT=1，避免因运行器 git 过旧而静默跳过。
		if os.Getenv("YSS_TEST_REQUIRE_GIT") == "1" {
			t.Fatal("git 不支持 --no-lazy-fetch（需 2.44+），但 YSS_TEST_REQUIRE_GIT=1 要求执行实现仓路由用例")
		}
		t.Skip("当前 git 不支持 --no-lazy-fetch（需 2.44+），跳过实现仓路由用例")
	}
}

func runYSS(args ...string) (int, map[string]any, error) {
	var out, errb bytes.Buffer
	code := Run(context.Background(), append(args, "--json"), &out, &errb)
	var env map[string]any
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		return code, nil, fmt.Errorf("输出不是 JSON envelope: %v\nstdout=%s\nstderr=%s", err, out.String(), errb.String())
	}
	return code, env, nil
}

func gitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-c", "user.name=T", "-c", "user.email=t@example.com", "-c", "init.defaultBranch=main", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func setupFixture() (routeFixture, error) {
	backendSeedOnce.Do(setupBackendSeed)
	if backendSeedErr != nil {
		return routeFixture{}, backendSeedErr
	}
	dir := filepath.Dir(backendSeed)
	f := routeFixture{project: filepath.Join(dir, "proj"), impl: filepath.Join(dir, "impl")}
	if err := copyBackendSeed(f.project); err != nil {
		return f, err
	}
	var err error
	if err := os.MkdirAll(f.impl, 0o755); err != nil {
		return f, err
	}
	if err := os.WriteFile(filepath.Join(f.impl, "a.txt"), []byte("a\n"), 0o644); err != nil {
		return f, err
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"commit", "-qm", "init"}} {
		if out, err := gitIn(f.impl, args...); err != nil {
			return f, fmt.Errorf("git %v: %v %s", args, err, out)
		}
	}
	if f.base, err = gitIn(f.impl, "rev-parse", "HEAD"); err != nil {
		return f, err
	}
	// 候选改动：只改 scope 内的 a.txt。
	if err := os.WriteFile(filepath.Join(f.impl, "a.txt"), []byte("a\nb\n"), 0o644); err != nil {
		return f, err
	}
	// 任取实例中第一个分发的技能作为“适用技能”输入；只要求原字节摘要一致。
	skills, err := filepath.Glob(filepath.Join(f.project, ".agents", "skills", "*", "SKILL.md"))
	if err != nil || len(skills) == 0 {
		return f, fmt.Errorf("实例没有分发任何技能: %v", err)
	}
	rel, err := filepath.Rel(f.project, skills[0])
	if err != nil {
		return f, err
	}
	f.skillRef = filepath.ToSlash(rel)
	raw, err := os.ReadFile(skills[0])
	if err != nil {
		return f, err
	}
	sum := sha256.Sum256(raw)
	f.skillDigest = "sha256:" + hex.EncodeToString(sum[:])
	f.inputRef = "business-input.md"
	input := []byte("Standalone Backend: update a.txt only.\n")
	sum = sha256.Sum256(input)
	f.inputDigest = "sha256:" + hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(f.project, f.inputRef), input, 0644); err != nil {
		return f, err
	}
	return f, nil
}

func routeFixtureFor(t *testing.T) routeFixture {
	t.Helper()
	requireModernGit(t)
	fixtureOnce.Do(func() { fixture, fixtureErr = setupFixture() })
	if fixtureErr != nil {
		t.Fatal(fixtureErr)
	}
	return fixture
}

func setupBackendSeed() {
	fixtureDir, backendSeedErr = os.MkdirTemp("", "yss-route-")
	if backendSeedErr != nil {
		return
	}
	dir, err := filepath.EvalSymlinks(fixtureDir)
	if err != nil {
		backendSeedErr = err
		return
	}
	backendSeed = filepath.Join(dir, "backend")
	code, env, err := runYSS("init", "--installation", "governed", "--profile", "backend", "--root", backendSeed, "--project-name", "Demo")
	if err != nil || code != 0 {
		backendSeedErr = fmt.Errorf("init 失败: code=%d env=%v err=%v", code, env, err)
	}
}

func backendFixtureFor(t *testing.T) string {
	t.Helper()
	backendSeedOnce.Do(setupBackendSeed)
	if backendSeedErr != nil {
		t.Fatal(backendSeedErr)
	}
	return backendSeed
}

func isolatedBackendFixture(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "backend")
	backendFixtureFor(t)
	if err = copyBackendSeed(root); err != nil {
		t.Fatal(err)
	}
	return root
}

func copyBackendSeed(root string) error {
	return copyFixtureTree(backendSeed, root)
}

func copyFixtureTree(source, root string) error {
	return filepath.WalkDir(source, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		ref, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		// The seed's initialization archive is bound to its original root.
		if ref == filepath.Join(".yss", "transactions") && entry.IsDir() {
			return filepath.SkipDir
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		target := filepath.Join(root, ref)
		if info.IsDir() {
			if err := os.Mkdir(target, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chmod(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("夹具包含非普通文件: %s", ref)
		}
		in, err := os.Open(name)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		inErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if inErr != nil {
			return inErr
		}
		return os.Chmod(target, info.Mode().Perm())
	})
}

// writeTask 写入一份带 fenced JSON 证据的任务记录；mutate 在序列化前修改证据。
func writeTask(t *testing.T, f routeFixture, id string, mutate func(map[string]any)) string {
	t.Helper()
	evidence := map[string]any{
		"schema_version": 2, "policy_version": 2,
		"task_id": id, "delivery_path": "daily", "formal_bindings": []any{},
		"goal_ref": "#目标", "acceptance_ref": "#验收",
		"repository":     map[string]any{"root": f.impl, "baseline_sha": f.base, "baseline_ref": "#基线", "rollback_ref": "#回滚"},
		"scope":          map[string]any{"paths": []any{"a.txt"}, "risk_ref": "#风险", "impacts": []any{"no-api"}},
		"skills":         []any{map[string]any{"ref": f.skillRef, "digest": f.skillDigest}},
		"business_input": map[string]any{"mode": "standalone", "side": "backend", "ref": f.inputRef, "digest": f.inputDigest, "conflicts": []any{}},
		"inputs":         []any{},
		"commands":       []any{map[string]any{"argv": []any{"true"}, "cwd": "."}},
		"api":            map[string]any{"mode": "none", "reason": "只改文本"},
	}
	if mutate != nil {
		mutate(evidence)
	}
	payload, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	ref := ".work/" + strings.ToLower(id) + "/task.md"
	body := "# 任务 " + id + "\n\n## 目标\n修正 a.txt。\n\n## 验收\na.txt 含 b。\n\n## 基线\nmain 当前提交。\n\n## 回滚\ngit revert。\n\n## 风险\n仅文本改动。\n\n<!-- yss-task-evidence -->\n```json\n" + string(payload) + "\n```\n"
	path := filepath.Join(f.project, filepath.FromSlash(ref))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return ref
}

func route(t *testing.T, f routeFixture, ref string) (int, string, map[string]any) {
	t.Helper()
	code, env, err := runYSS("lifecycle", "route", "--root", f.project, "--task", ref, "--implementation-root", f.impl, "--base", f.base)
	if err != nil {
		t.Fatal(err)
	}
	result, _ := env["result"].(map[string]any)
	path, _ := result["delivery_path"].(string)
	if result["approval_created"] != false {
		t.Errorf("route 不得创建批准: %v", result["approval_created"])
	}
	return code, path, env
}

// summary 只取排查所需字段，避免失败时倾倒整个 envelope。
func summary(env map[string]any) string {
	result, _ := env["result"].(map[string]any)
	var messages []string
	for _, key := range []string{"diagnostics", "blockers"} {
		rows, _ := result[key].([]any)
		for _, row := range rows {
			if m, ok := row.(map[string]any); ok {
				messages = append(messages, fmt.Sprint(m["code"], ": ", m["message"]))
			}
		}
	}
	return fmt.Sprintf("code=%v reasons=%v diagnostics=%v", env["code"], result["reasons"], messages)
}

func TestRouteDecisionsOnInitializedInstance(t *testing.T) {
	f := routeFixtureFor(t)
	for _, tc := range []struct {
		name, id, want string
		code           int
		mutate         func(map[string]any)
	}{
		{"齐全的低风险记录走日常路径", "DAILY-1", "daily", 0, nil},
		{"记录未选择日常路线时走正式流程", "GOV-1", "governed", 0, func(e map[string]any) { e["delivery_path"] = "governed" }},
		{"排除影响强制正式流程", "GOV-2", "governed", 0, func(e map[string]any) {
			e["scope"].(map[string]any)["impacts"] = []any{"data-migration"}
		}},
		{"未登记的影响不会被当作低风险", "GOV-3", "governed", 0, func(e map[string]any) {
			e["scope"].(map[string]any)["impacts"] = []any{"something-new"}
		}},
		{"未声明 formal_bindings 不能证明没有正式绑定", "INFO-1", "needs-info", 2, func(e map[string]any) { delete(e, "formal_bindings") }},
		{"缺少 API 影响声明", "INFO-2", "needs-info", 2, func(e map[string]any) { delete(e, "api") }},
		{"技能摘要不匹配被拒绝", "REJ-1", "needs-info", 1, func(e map[string]any) {
			e["skills"] = []any{map[string]any{"ref": f.skillRef, "digest": "sha256:" + strings.Repeat("0", 64)}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, path, env := route(t, f, writeTask(t, f, tc.id, tc.mutate))
			if code != tc.code || path != tc.want {
				t.Errorf("退出码=%d 路径=%q，期望 %d / %q；%s", code, path, tc.code, tc.want, summary(env))
			}
		})
	}
}

func TestRouteResolvesBaseRef(t *testing.T) {
	f := routeFixtureFor(t)
	ref := writeTask(t, f, "REF-1", nil)
	code, env, err := runYSS("lifecycle", "route", "--root", f.project, "--task", ref, "--implementation-root", f.impl, "--base", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	result, _ := env["result"].(map[string]any)
	if code != 0 || result["delivery_path"] != "daily" || result["base_ref"] != "HEAD" || result["base_sha"] != f.base {
		t.Errorf("--base HEAD 应解析为完整 SHA 并回显：退出码=%d 路径=%v base_ref=%v base_sha=%v；%s", code, result["delivery_path"], result["base_ref"], result["base_sha"], summary(env))
	}
	code, env, err = runYSS("lifecycle", "route", "--root", f.project, "--task", ref, "--implementation-root", f.impl, "--base", "no-such-branch")
	if err != nil {
		t.Fatal(err)
	}
	if code != 2 {
		t.Errorf("无法解析的引用应返回不可判定（退出码 2），得到 %d；%s", code, summary(env))
	}
}

func TestRouteNeverDowngradesFormalBinding(t *testing.T) {
	f := routeFixtureFor(t)
	// 正式资产（带 stage）被记录引用时，即使记录写 daily 也必须走正式流程。
	formal := ".work/formal-1/checkpoint.yaml"
	path := filepath.Join(f.project, filepath.FromSlash(formal))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stage: stage.plan\ntask: BOUND-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, got, env := route(t, f, writeTask(t, f, "BOUND-1", func(e map[string]any) {
		e["formal_bindings"] = []any{map[string]any{"ref": formal}}
	}))
	if code != 0 || got != "governed" {
		t.Fatalf("正式绑定的任务被降级: 退出码=%d 路径=%q；%s", code, got, summary(env))
	}
}

func TestRouteRejectsFrontendWork(t *testing.T) {
	f := routeFixtureFor(t)
	code, _, env := route(t, f, writeTask(t, f, "FE-1", func(e map[string]any) {
		e["scope"].(map[string]any)["paths"] = []any{"apps/frontend/src/App.vue"}
	}))
	if code != 1 || env["code"] != "FRONTEND_REMOVED" {
		t.Errorf("前端任务应被拒绝: 退出码=%d code=%v", code, env["code"])
	}
}
