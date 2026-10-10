// oracle-record 把固定模板源的 Node 预言机裁决录成 internal/governance/testdata/oracle 下的 fixture。
//
// 用法（在 yss-cli 仓库根）：
//
//	go run ./tools/oracle-record --template-root <固定模板检出>   # 录制并回放核验
//	go run ./tools/oracle-record --check                          # 只回放核验已有 fixture
//
// 录制要求 --template-root 恰好检出 docs/source-lock.json 固定的 spec 模板提交且工作树干净，
// 并已安装其 Node 工具依赖（pnpm --dir <root>/.template-source/tooling/node install --frozen-lockfile）。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	governanceDir = "internal/governance"
	fixtureDir    = "internal/governance/testdata/oracle"
)

// 与 internal/governance/oracle_fixture_test.go 的 oracleFixtureScripts 保持一致。
var fixtureScripts = []string{"verify-approval-record", "verify-maintenance-checkpoint"}

var testFunc = regexp.MustCompile(`(?m)^func (Test\w+)\(t \*testing\.T\)`)

func main() {
	templateRoot := flag.String("template-root", "", "固定模板源检出（录制时必需）")
	checkOnly := flag.Bool("check", false, "只回放核验，不录制")
	timeout := flag.String("timeout", "15m", "单次 go test 的超时")
	flag.Parse()
	if err := run(*templateRoot, *checkOnly, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, "oracle-record:", err)
		os.Exit(1)
	}
}

func run(templateRoot string, checkOnly bool, timeout string) error {
	if raw, err := os.ReadFile("go.mod"); err != nil || !strings.Contains(string(raw), "module github.com/iloveZzz/yss-cli") {
		return fmt.Errorf("请在 yss-cli 仓库根运行")
	}
	commit, err := lockedSpecCommit()
	if err != nil {
		return err
	}
	tests, err := discoverTests()
	if err != nil {
		return err
	}
	pattern := "^(" + strings.Join(tests, "|") + ")$"
	fmt.Printf("固定 spec 模板 %s；涉及 %d 个测试\n", commit, len(tests))

	if !checkOnly {
		if templateRoot == "" {
			return fmt.Errorf("录制需要 --template-root")
		}
		root, err := filepath.Abs(templateRoot)
		if err != nil {
			return err
		}
		if err = requireFixedCheckout(root, commit); err != nil {
			return err
		}
		matches, _ := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
		for _, m := range matches {
			if err = os.Remove(m); err != nil {
				return err
			}
		}
		fmt.Println("录制：对真实预言机逐条取裁决")
		env := append(cleanEnv(), "YSS_ORACLE_MODE=record", "YSS_LEGACY_ORACLE_ROOT="+root, "YSS_SCHEMA_CORPUS_ROOT="+root, "YSS_ORACLE_TEMPLATE_COMMIT="+commit)
		if err = goTest(pattern, timeout, env); err != nil {
			return fmt.Errorf("录制失败（已写入的 fixture 不完整，请修复后重录）: %w", err)
		}
	}

	fmt.Println("回放核验：不设预言机根，只读 fixture")
	if err = goTest(pattern, timeout, append(cleanEnv(), "YSS_ORACLE_MODE=fixture")); err != nil {
		return fmt.Errorf("回放失败: %w", err)
	}
	return summarize()
}

// cleanEnv 去掉会改变模式的变量，由调用方显式设置。
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name := strings.SplitN(kv, "=", 2)[0]
		switch name {
		case "YSS_ORACLE_MODE", "YSS_LEGACY_ORACLE_ROOT", "YSS_SCHEMA_CORPUS_ROOT", "YSS_ORACLE_TEMPLATE_COMMIT":
			continue
		}
		env = append(env, kv)
	}
	return env
}

func goTest(pattern, timeout string, env []string) error {
	cmd := exec.Command("go", "test", "./"+governanceDir, "-run", pattern, "-count=1", "-timeout", timeout)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func lockedSpecCommit() (string, error) {
	raw, err := os.ReadFile("docs/source-lock.json")
	if err != nil {
		return "", err
	}
	var lock struct {
		Profiles map[string]struct {
			TemplateCommit string `json:"templateCommit"`
		} `json:"profiles"`
	}
	if err = json.Unmarshal(raw, &lock); err != nil {
		return "", err
	}
	commit := lock.Profiles["spec"].TemplateCommit
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(commit) {
		return "", fmt.Errorf("source-lock 的 spec templateCommit 无效: %q", commit)
	}
	return commit, nil
}

func requireFixedCheckout(root, commit string) error {
	head, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("%s 不是 git 检出: %w", root, err)
	}
	if head != commit {
		return fmt.Errorf("模板检出在 %s，而锁固定 %s；先 git -C %s checkout --detach %s", head, commit, root, commit)
	}
	if dirty, err := git(root, "status", "--porcelain"); err != nil || dirty != "" {
		return fmt.Errorf("模板检出不干净，录制只能基于已提交的固定来源")
	}
	if _, err = os.Stat(filepath.Join(root, ".template-source/tooling/node/node_modules")); err != nil {
		return fmt.Errorf("模板检出缺 Node 工具依赖：pnpm --dir %s/.template-source/tooling/node install --frozen-lockfile", root)
	}
	return nil
}

func git(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

// discoverTests 收集引用了登记脚本的测试文件里的全部测试函数，保证录制覆盖每一处调用。
func discoverTests() ([]string, error) {
	files, err := filepath.Glob(filepath.Join(governanceDir, "*_test.go"))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "oracle_fixture_test.go") {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		text := string(raw)
		uses := false
		for _, script := range fixtureScripts {
			if strings.Contains(text, `"`+script+`"`) || strings.Contains(text, `scripts/`+script) {
				uses = true
			}
		}
		if !uses {
			continue
		}
		for _, m := range testFunc.FindAllStringSubmatch(text, -1) {
			seen[m[1]] = true
		}
	}
	var names []string
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("没有找到引用登记脚本的测试")
	}
	return names, nil
}

func summarize() error {
	matches, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
	if err != nil {
		return err
	}
	for _, m := range matches {
		raw, err := os.ReadFile(m)
		if err != nil {
			return err
		}
		var f struct {
			TemplateCommit string                     `json:"template_commit"`
			Entries        map[string]json.RawMessage `json:"entries"`
		}
		if err = json.Unmarshal(raw, &f); err != nil {
			return err
		}
		fmt.Printf("  %-34s %3d 条裁决，模板 %s，%d 字节\n", filepath.Base(m), len(f.Entries), f.TemplateCommit[:8], len(raw))
	}
	return nil
}
