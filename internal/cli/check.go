package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/gitops"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

// yss check / yss verify --gate 只聚合现有 verify 命令：识别资产后在进程内调用同一校验引擎，
// 不新增校验语义，也不授予批准或执行。

// checkTemplateDirs 是模板与技能来源目录；其中的模板文件带占位符，不是实例资产。
var checkTemplateDirs = []string{".template-spec/", ".template-source/", ".agents/", ".claude/", "profiles/", "node_modules/", ".git/"}

// checkContextCodes 表示校验器需要调用方提供独立当前上下文，结论记为未评估而非失败。
var checkContextCodes = map[string]bool{"USER_DECISION_CONTEXT_REQUIRED": true, "APPROVAL_CONTEXT_REQUIRED": true}

type checkDetector struct {
	assetKind string
	match     func(map[string]any) bool
	// args 返回现有 verify 命令参数；needsCheckpoint 为真且未提供 --checkpoint 时不运行。
	args            func(ref, checkpoint string) []string
	needsCheckpoint bool
	scope           string
}

func checkHas(doc map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, ok := doc[key]; !ok {
			return false
		}
	}
	return true
}

func checkKind(kind string) func(map[string]any) bool {
	return func(doc map[string]any) bool { return doc["kind"] == kind }
}

// 顺序即优先级；字段签名取自对应 Schema 的必填字段或 kind 常量。
var checkDetectors = []checkDetector{
	{"user-decision", checkKind("user-decision"), func(ref, cp string) []string {
		return []string{"evidence", "verify", "--kind", "user-decision", "--file", ref, "--checkpoint", cp}
	}, true, "current"},
	{"approval-record", func(d map[string]any) bool { return checkHas(d, "gate_id", "decision", "actor_kind") }, func(ref, _ string) []string {
		return []string{"evidence", "check", "--schema", ".template-spec/process/schemas/approval-record.schema.json", "--file", ref}
	}, false, "structure"},
	{"lifecycle-checkpoint", func(d map[string]any) bool { return checkHas(d, "repository_mode", "mode", "stage", "artifacts") }, func(ref, _ string) []string {
		return []string{"contract", "check", "--schema", ".template-spec/process/schemas/lifecycle-checkpoint.schema.json", "--file", ref}
	}, false, "structure"},
	{"context-reconciliation", func(d map[string]any) bool { return checkHas(d, "repository_mode", "work_unit", "context_snapshot") }, func(ref, _ string) []string {
		return []string{"evidence", "verify", "--kind", "context-reconciliation", "--file", ref}
	}, false, "current"},
	{"openapi-draft-validation", checkKind("openapi-draft-validation"), func(ref, _ string) []string {
		return []string{"contract", "verify", "--kind", "openapi-validation", "--file", ref}
	}, false, "current"},
	{"scaffold-architecture-decisions", checkKind("scaffold-architecture-decisions"), func(ref, _ string) []string {
		return []string{"contract", "verify", "--kind", "scaffold-decisions", "--file", ref}
	}, false, "current"},
}

// checkFinding 是统一失败格式：每条都给出定位、修复建议和可复现命令。
type checkFinding struct {
	Code    string `json:"code"`
	File    string `json:"file"`
	Pointer string `json:"pointer,omitempty"`
	Message string `json:"message"`
	Fix     string `json:"fix"`
	Command string `json:"command"`
}

type checkItem struct {
	File      string         `json:"file"`
	AssetKind string         `json:"asset_kind,omitempty"`
	Scope     string         `json:"scope,omitempty"`
	Status    string         `json:"status"`
	Reason    string         `json:"reason,omitempty"`
	Command   string         `json:"command,omitempty"`
	Findings  []checkFinding `json:"findings,omitempty"`
}

type checkReport struct {
	Kind                   string         `json:"kind"`
	Status                 string         `json:"status"`
	ReadOnly               bool           `json:"read_only"`
	ExecutionAuthorization string         `json:"execution_authorization"`
	Selection              map[string]any `json:"selection"`
	Summary                map[string]int `json:"summary"`
	Items                  []checkItem    `json:"items"`
	Findings               []checkFinding `json:"findings"`
}

type checkFailure struct{ report *checkReport }

// Error 列出前若干条发现，使人读输出也能直接看到定位、修复与复现命令。
func (e *checkFailure) Error() string {
	var out strings.Builder
	out.WriteString("检查未通过：" + strconv.Itoa(e.report.Summary["failed"]) + " 项失败")
	for i, f := range e.report.Findings {
		if i == checkFindingPreview {
			out.WriteString("\n  …其余见 --json 的 findings")
			break
		}
		location := f.File
		if f.Pointer != "" {
			location += "#" + f.Pointer
		}
		out.WriteString("\n  " + location + " " + f.Code + "：" + f.Fix)
		if f.Command != "" {
			out.WriteString("\n    复现：" + f.Command)
		}
	}
	return out.String()
}

const checkFindingPreview = 5

func (e *checkFailure) ErrorResult() any { return e.report }
func (e *checkFailure) Unwrap() error {
	return &domain.Error{Code: "CHECK_FAILED", Message: e.Error(), Exit: 1}
}

func checkCommand(ctx context.Context, root string, o options) (any, error) {
	refs, selection, err := checkSelection(ctx, root, o)
	if err != nil {
		return nil, err
	}
	report := newCheckReport("yss-check", selection)
	checkpoint := o.values["checkpoint"]
	for _, ref := range refs {
		report.add(checkFile(ctx, root, ref, checkpoint))
	}
	return report.finish()
}

func gateVerifyCommand(ctx context.Context, root string, o options) (any, error) {
	if len(o.args) > 1 {
		return nil, domain.Fail("ARGUMENT", "yss verify 不接受位置参数；使用 --gate 与 --checkpoint")
	}
	gate, checkpoint := strings.TrimSpace(o.values["gate"]), strings.TrimSpace(o.values["checkpoint"])
	if gate == "" || checkpoint == "" {
		return nil, domain.Fail("ARGUMENT", "需要 --gate <门禁ID> 与 --checkpoint <当前 checkpoint>")
	}
	if !strings.HasPrefix(gate, "gate.") {
		gate = "gate." + gate
	}
	report := newCheckReport("yss-gate-verify", map[string]any{"gate": gate, "checkpoint": checkpoint})
	steps := []struct {
		kind string
		args []string
	}{
		{"lifecycle-checkpoint", []string{"lifecycle", "verify", "--checkpoint", checkpoint}},
		{"gate-checks", []string{"contract", "verify", "--kind", "gate-checks", "--gate", gate, "--file", checkpoint}},
	}
	for _, step := range steps {
		report.add(runCheck(ctx, root, checkpoint, step.kind, "current", step.args))
	}
	return report.finish()
}

func newCheckReport(kind string, selection map[string]any) *checkReport {
	return &checkReport{Kind: kind, ReadOnly: true, ExecutionAuthorization: "not-evaluated", Selection: selection,
		Summary: map[string]int{"passed": 0, "failed": 0, "not_evaluated": 0, "skipped": 0}, Items: []checkItem{}, Findings: []checkFinding{}}
}

func (r *checkReport) add(item checkItem) {
	key := strings.ReplaceAll(item.Status, "-", "_")
	r.Summary[key]++
	r.Items = append(r.Items, item)
	r.Findings = append(r.Findings, item.Findings...)
}

// finish：任何失败即失败；没有失败但存在未评估项时不报告通过。
func (r *checkReport) finish() (any, error) {
	switch {
	case r.Summary["failed"] > 0:
		r.Status = "failed"
		return nil, &checkFailure{r}
	case r.Summary["not_evaluated"] > 0:
		r.Status = "incomplete"
	case r.Summary["passed"] == 0:
		r.Status = "no-assets"
	default:
		r.Status = "passed"
	}
	return r, nil
}

func checkSelection(ctx context.Context, root string, o options) ([]string, map[string]any, error) {
	explicit := o.args[1:]
	changed := o.values["changed"] == "true"
	base := strings.TrimSpace(o.values["base"])
	if len(explicit) > 0 && changed {
		return nil, nil, domain.Fail("ARGUMENT", "文件参数与 --changed 互斥")
	}
	if base != "" && !changed {
		return nil, nil, domain.Fail("ARGUMENT", "--base 只与 --changed 同用")
	}
	if len(explicit) == 0 && !changed {
		return nil, nil, domain.Fail("ARGUMENT", "需要文件参数或 --changed，例如 yss check --changed")
	}
	refs := []string{}
	for _, raw := range explicit {
		ref, err := checkRef(root, raw)
		if err != nil {
			return nil, nil, err
		}
		refs = append(refs, ref)
	}
	selection := map[string]any{"mode": "files"}
	if changed {
		var err error
		if refs, err = changedRefs(ctx, root, base); err != nil {
			return nil, nil, err
		}
		selection = map[string]any{"mode": "changed", "base": base}
	}
	sort.Strings(refs)
	return dedupe(refs), selection, nil
}

func checkRef(root, raw string) (string, error) {
	path := raw
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, raw)
	}
	rel, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", domain.Fail("PATH", "文件必须位于项目根内: "+raw)
	}
	rel = filepath.ToSlash(rel)
	if _, err = safefs.Path(root, rel); err != nil {
		return "", err
	}
	return rel, nil
}

// changedRefs 取工作树相对 HEAD 的改动与未跟踪文件；给出 --base 时再并入 base..HEAD 的已提交改动。
func changedRefs(ctx context.Context, root, base string) ([]string, error) {
	runner, err := gitops.NewRunner(root)
	if err != nil {
		return nil, err
	}
	status, err := runner.ReadStatus(ctx)
	if err != nil {
		return nil, err
	}
	refs := []string{}
	for _, entry := range status.Entries {
		if !entry.Submodule {
			refs = append(refs, entry.Path)
		}
	}
	if base != "" {
		if !checkBaseRef.MatchString(base) || strings.Contains(base, "..") {
			return nil, domain.Fail("ARGUMENT", "--base 必须是合法的 Git 引用或完整 SHA")
		}
		out, err := runner.Query(ctx, "log", "--format=", "--name-only", base+"..HEAD", "--")
		if err != nil {
			return nil, domain.Fail("BASELINE", "无法读取 "+base+"..HEAD 的提交改动: "+err.Error())
		}
		for _, line := range strings.Split(string(out), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				refs = append(refs, line)
			}
		}
	}
	return refs, nil
}

var checkBaseRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/~^@{}-]{0,199}$`)

func dedupe(refs []string) []string {
	out := []string{}
	for i, ref := range refs {
		if i == 0 || ref != refs[i-1] {
			out = append(out, ref)
		}
	}
	return out
}

func checkFile(ctx context.Context, root, ref, checkpoint string) checkItem {
	item := checkItem{File: ref, Status: "skipped"}
	ext := strings.ToLower(filepath.Ext(ref))
	if ext != ".yaml" && ext != ".yml" && ext != ".json" {
		item.Reason = "非 YAML/JSON 资产"
		return item
	}
	for _, dir := range checkTemplateDirs {
		if strings.HasPrefix(ref, dir) {
			item.Reason = "模板或技能来源，不是实例资产"
			return item
		}
	}
	path, err := safefs.Path(root, ref)
	if err != nil {
		return failedItem(item, "PATH", err.Error(), "使用项目根内的普通文件路径", "")
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		item.Reason = "文件已删除"
		return item
	}
	if err != nil {
		return failedItem(item, "INPUT", "无法读取: "+err.Error(), "确认文件可读后重试", "")
	}
	parsed, err := schema.Parse(raw)
	if err != nil {
		return failedItem(item, "PARSE", err.Error(), "修正 YAML/JSON 语法后重试", "yss check "+ref)
	}
	doc, _ := parsed.(map[string]any)
	for _, detector := range checkDetectors {
		if doc == nil || !detector.match(doc) {
			continue
		}
		args := detector.args(ref, checkpoint)
		if detector.needsCheckpoint && checkpoint == "" {
			item.AssetKind, item.Scope, item.Status = detector.assetKind, detector.scope, "not-evaluated"
			item.Reason = "需要独立当前上下文，不从待验记录反推"
			item.Command = commandLine(root, detector.args(ref, "<当前checkpoint>"))
			return item
		}
		return runCheck(ctx, root, ref, detector.assetKind, detector.scope, args)
	}
	item.Reason = "未识别的资产类型；需要时直接运行对应 yss <组> verify --kind"
	return item
}

func failedItem(item checkItem, code, message, fix, command string) checkItem {
	item.Status = "failed"
	item.Findings = []checkFinding{{Code: code, File: item.File, Message: message, Fix: fix, Command: command}}
	return item
}

// runCheck 在进程内调用现有 verify 命令并把其 envelope 转为统一结论。
func runCheck(ctx context.Context, root, ref, assetKind, scope string, args []string) checkItem {
	full := append(append([]string{}, args...), "--root", root, "--json")
	command := commandLine(root, args)
	item := checkItem{File: ref, AssetKind: assetKind, Scope: scope, Command: command}
	var out bytes.Buffer
	exit := RunWithTerminal(ctx, full, &out, io.Discard, nil)
	var env struct {
		Status string          `json:"status"`
		Code   string          `json:"code"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		return failedItem(item, "INTERNAL", "校验器输出不可解析", "运行 command 查看原始输出", command)
	}
	if exit == 0 && env.Status == "ok" {
		item.Status = "passed"
		return item
	}
	var result struct {
		Message     string `json:"message"`
		Diagnostics []struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			SourceRef string `json:"source_ref"`
			Recovery  string `json:"recovery"`
		} `json:"diagnostics"`
	}
	_ = json.Unmarshal(env.Result, &result)
	item.Status = "failed"
	for _, d := range result.Diagnostics {
		if checkContextCodes[d.Code] {
			item.Status = "not-evaluated"
			item.Reason = d.Message
			continue
		}
		file := d.SourceRef
		if file == "" {
			file = ref
		}
		item.Findings = append(item.Findings, checkFinding{Code: d.Code, File: file, Pointer: schemaPointer(d.Message), Message: d.Message, Fix: checkFix(d.Code, d.Message, d.Recovery), Command: command})
	}
	if len(item.Findings) > 0 {
		item.Status = "failed"
	} else if item.Status == "failed" {
		message := result.Message
		if message == "" {
			message = env.Code
		}
		item.Findings = []checkFinding{{Code: env.Code, File: ref, Message: message, Fix: checkFix(env.Code, message, ""), Command: command}}
	}
	return item
}

var schemaPointerPattern = regexp.MustCompile(`SCHEMA_VALIDATION (/[^ \]]*)`)

func schemaPointer(message string) string {
	if m := schemaPointerPattern.FindStringSubmatch(message); m != nil {
		return m[1]
	}
	return ""
}

// checkFix 给出按错误类别区分的修复建议；校验器自带的具体 recovery 优先于通用句。
func checkFix(code, message, recovery string) string {
	generic := recovery == "" || strings.HasPrefix(recovery, "修复当前输入、能力或证据")
	if !generic {
		return recovery
	}
	switch {
	case code == "SCHEMA":
		if p := schemaPointer(message); p != "" {
			return "按 Schema 修正字段 " + p + " 后重跑 command"
		}
		return "按资产声明的 Schema 修正结构后重跑 command"
	case code == "INPUT" || code == "PATH":
		return "确认文件存在、位于项目根内且是普通文件"
	case code == "CAPABILITY":
		return "按 message 升级依赖（如 Git ≥ 2.44）或 yss 后重跑"
	case strings.HasSuffix(code, "_CONTEXT_REQUIRED"):
		return "提供 --checkpoint 等独立当前上下文后重跑"
	case strings.HasPrefix(code, "GATE_"):
		return "补齐门禁要求的检查、证据或批准绑定后重跑；门禁定义见 yss explain <门禁ID>"
	}
	return "按 message 修正资产后重跑 command"
}

func commandLine(root string, args []string) string {
	parts := []string{"yss"}
	for _, arg := range append(append([]string{}, args...), "--root", root, "--json") {
		if strings.ContainsAny(arg, " \t'\"<>") {
			arg = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
		}
		parts = append(parts, arg)
	}
	return strings.Join(parts, " ")
}

var checkStatusText = map[string]string{"passed": "通过", "incomplete": "未完成（存在未评估项，不代表通过）", "no-assets": "没有可识别的实例资产"}

func renderCheck(m map[string]any) string {
	var out strings.Builder
	status, _ := m["status"].(string)
	out.WriteString("检查结论：" + checkStatusText[status] + "\n")
	items, _ := m["items"].([]any)
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		line := "  " + explainString(item["status"]) + "  " + explainString(item["file"])
		if kind := explainString(item["asset_kind"]); kind != "" {
			line += "（" + kind + "）"
		}
		if reason := explainString(item["reason"]); reason != "" {
			line += "：" + reason
		}
		out.WriteString(line + "\n")
		if item["status"] == "not-evaluated" && explainString(item["command"]) != "" {
			out.WriteString("    补充上下文后运行：" + explainString(item["command"]) + "\n")
		}
	}
	out.WriteString("不授予批准或执行；机器可读结果：--json")
	return out.String()
}
