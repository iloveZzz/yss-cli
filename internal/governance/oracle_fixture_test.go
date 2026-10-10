package governance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// 固定模板源的 Node 预言机裁决录像。
//
// 差异比对原本每条断言都冷启动一次 Node 与 Python，慢，而且预言机会随锁定模板漂移。
// 这里把预言机对同一输入的裁决录下来，默认回放；录制时记下所依据的固定模板提交，
// 与 docs/source-lock.json 不一致时测试失败并提示重新录制。
//
// YSS_ORACLE_MODE=fixture|live|record
//   - fixture：只读 testdata/oracle/<name>.json，不需要 Node。
//   - live：每次调用真实预言机（需要 YSS_LEGACY_ORACLE_ROOT），与此前行为一致。
//   - record：live，并把裁决写入 fixture（由 tools/oracle-record 驱动，需要 YSS_ORACLE_TEMPLATE_COMMIT）。
//
// 未设置时：设了 YSS_LEGACY_ORACLE_ROOT 则为 live（保持原有行为），否则为 fixture。
const oracleFixtureSchemaVersion = 1

type oracleMode string

const (
	oracleModeFixture oracleMode = "fixture"
	oracleModeLive    oracleMode = "live"
	oracleModeRecord  oracleMode = "record"
)

// oracleRequest 是一次预言机调用的身份：脚本、相对化后的参数、标准输入以及所读根目录的内容摘要。
type oracleRequest struct {
	Script string            `json:"script"`
	Args   []string          `json:"args,omitempty"`
	Stdin  string            `json:"stdin,omitempty"`
	Tree   map[string]string `json:"tree,omitempty"`
}

func (r oracleRequest) key() string {
	raw, err := json.Marshal(r)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type oracleEntry struct {
	Test     string   `json:"test"`
	Script   string   `json:"script"`
	Args     []string `json:"args,omitempty"`
	ExitCode int      `json:"exit_code"`
	Output   string   `json:"output"`
}

type oracleFixtureFile struct {
	SchemaVersion  int                    `json:"schema_version"`
	TemplateCommit string                 `json:"template_commit"`
	Entries        map[string]oracleEntry `json:"entries"`
}

var oracleFixtureMu sync.Mutex

// oracleFixtureScripts 登记走 fixture 的脚本；未登记的脚本保持原有的“只在设置预言机根时才调用”。
var oracleFixtureScripts = map[string]string{
	"verify-approval-record":        "approval",
	"verify-maintenance-checkpoint": "maintenance",
}

func oracleFixtureFor(script string) string { return oracleFixtureScripts[script] }

func oracleModeFromEnv(t *testing.T) oracleMode {
	t.Helper()
	switch value := os.Getenv("YSS_ORACLE_MODE"); value {
	case "fixture", "live", "record":
		return oracleMode(value)
	case "":
	default:
		t.Fatalf("YSS_ORACLE_MODE 只能是 fixture、live 或 record: %q", value)
	}
	if os.Getenv("YSS_LEGACY_ORACLE_ROOT") != "" {
		return oracleModeLive
	}
	return oracleModeFixture
}

func oracleFixturePath(name string) string {
	return filepath.Join("testdata", "oracle", name+".json")
}

func oracleLockedSpecCommit(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "source-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Profiles map[string]struct {
			TemplateCommit string `json:"templateCommit"`
		} `json:"profiles"`
	}
	if err = json.Unmarshal(raw, &lock); err != nil {
		t.Fatal(err)
	}
	commit := lock.Profiles["spec"].TemplateCommit
	if commit == "" {
		t.Fatal("docs/source-lock.json 缺少 spec 的 templateCommit")
	}
	return commit
}

func oracleReadFixture(t *testing.T, name string) oracleFixtureFile {
	t.Helper()
	raw, err := os.ReadFile(oracleFixturePath(name))
	if err != nil {
		t.Fatalf("预言机 fixture %s 不存在: %v；用 go run ./tools/oracle-record --template-root <固定模板检出> 录制", name, err)
	}
	var f oracleFixtureFile
	if err = json.Unmarshal(raw, &f); err != nil || f.SchemaVersion != oracleFixtureSchemaVersion || f.Entries == nil {
		t.Fatalf("预言机 fixture %s 无效: schema=%d err=%v", name, f.SchemaVersion, err)
	}
	return f
}

// oracleVerdict 运行或回放一次预言机调用，返回退出码与（已规范化的）输出。
func oracleVerdict(t *testing.T, fixture string, request oracleRequest, test string, live func() (int, string)) (int, string) {
	t.Helper()
	mode := oracleModeFromEnv(t)
	if mode != oracleModeFixture {
		code, output := live()
		if mode == oracleModeRecord {
			oracleRecord(t, fixture, request, oracleEntry{Test: test, Script: request.Script, Args: request.Args, ExitCode: code, Output: output})
		}
		return code, output
	}
	f := oracleReadFixture(t, fixture)
	if locked := oracleLockedSpecCommit(t); f.TemplateCommit != locked {
		t.Fatalf("预言机 fixture %s 录制于模板 %s，而 docs/source-lock.json 现固定 %s；用 go run ./tools/oracle-record --template-root <固定模板检出> 重新录制", fixture, f.TemplateCommit, locked)
	}
	entry, ok := f.Entries[request.key()]
	if !ok {
		t.Fatalf("预言机 fixture %s 没有这次调用的裁决（%s %v）；测试输入或内嵌 Bundle 变了，用 go run ./tools/oracle-record --template-root <固定模板检出> 重新录制", fixture, request.Script, request.Args)
	}
	return entry.ExitCode, entry.Output
}

func oracleRecord(t *testing.T, fixture string, request oracleRequest, entry oracleEntry) {
	t.Helper()
	commit := os.Getenv("YSS_ORACLE_TEMPLATE_COMMIT")
	if commit == "" {
		t.Fatal("record 模式需要 YSS_ORACLE_TEMPLATE_COMMIT；请通过 tools/oracle-record 运行")
	}
	oracleFixtureMu.Lock()
	defer oracleFixtureMu.Unlock()
	f := oracleFixtureFile{SchemaVersion: oracleFixtureSchemaVersion, TemplateCommit: commit, Entries: map[string]oracleEntry{}}
	if raw, err := os.ReadFile(oracleFixturePath(fixture)); err == nil {
		if err = json.Unmarshal(raw, &f); err != nil || f.Entries == nil {
			t.Fatalf("已有 fixture 无法解析: %v", err)
		}
		if f.TemplateCommit != commit {
			t.Fatalf("fixture 已按模板 %s 录制，不能混入 %s；先删除旧文件", f.TemplateCommit, commit)
		}
	}
	f.Entries[request.key()] = entry
	if err := os.MkdirAll(filepath.Dir(oracleFixturePath(fixture)), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(oracleFixturePath(fixture), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// oracleTreeDigest 把根目录里所有普通文件的相对路径与内容摘要纳入调用身份：
// 预言机读取的权威资产来自内嵌 Bundle，Bundle 变了键就变，旧录像不会被误用。
func oracleTreeDigest(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		tree[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// oracleRelative 把参数里落在 root 下的绝对路径改写成与机器无关的 <root>/相对路径。
func oracleRelative(args []string, root string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		switch {
		case arg == root:
			out[i] = "<root>"
		case strings.HasPrefix(arg, root+string(filepath.Separator)):
			out[i] = "<root>/" + filepath.ToSlash(strings.TrimPrefix(arg, root+string(filepath.Separator)))
		default:
			out[i] = arg
		}
	}
	return out
}

func oracleNormalize(output string, replacements ...[2]string) string {
	for _, pair := range replacements {
		if pair[0] != "" {
			output = strings.ReplaceAll(output, pair[0], pair[1])
		}
	}
	return output
}

func oracleExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exited *exec.ExitError
	if errors.As(err, &exited) {
		return exited.ExitCode()
	}
	return -1
}
