package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
)

type errorDefinition struct {
	summary, recovery, help string
	version                 bool
}

var errorFamilies = map[string]errorDefinition{
	"argument":    {"命令或参数不符合当前接口", "核对当前子命令、必需参数、可选值和互斥条件。", "yss --help", false},
	"identity":    {"项目身份或路径无法核验", "核对实际项目根与 Profile；新项目使用 init，已有工程使用 attach，旧实例先诊断再显式 migrate。", "yss doctor --help", false},
	"version":     {"当前来源、格式或能力不兼容", "核对 CLI、协议、模板来源及实例格式，选择对应程序升级或实例迁移入口。", "yss capabilities --help", true},
	"plan":        {"保存计划或当前输入未通过核验", "检查计划类型、根目录和输入；处理变化后保存新计划，重新审阅再应用。", "yss sync --help", false},
	"conflict":    {"文件冲突或用户后续修改阻止操作", "查看冲突路径、基线与当前字节，保留现场；处理差异后重新规划。", "yss diff --help", false},
	"transaction": {"事务或恢复状态需要处理", "先查询所属事务及恢复材料；项目、迁移和程序事务各自恢复。", "yss recover --help", false},
	"binding":     {"插件 binding 与固定来源不一致", "通过对应插件的 project-upgrade-plan/apply 或 project-migration-plan/apply 更新身份和 binding。", "yss sync --help", true},
	"governance":  {"当前治理条件或证据未满足", "消费逐项诊断与恢复条件，补齐当前资产、实际验证及独立审查证据，再核验当前边界。", "yss lifecycle verify --help", false},
	"schema":      {"输入资产格式或 Schema 校验失败", "检查报告中的文件、字段及本地引用；按照当前锁定 Schema 修复输入。", "yss contract check --help", false},
	"network":     {"固定来源网络请求失败", "检查连接、HTTP 状态和限流；确认固定官方来源后重新查询。", "yss upgrade --help", false},
	"artifact":    {"发行包或安装材料核验失败", "核对本机平台、固定来源、归档摘要、manifest 和安装收据。", "yss update status --help", true},
	"runtime":     {"运行记录或运行存储状态异常", "核对运行标识、所有者、存储目录及当前记录，保留证据后按对应入口恢复。", "yss runtime --help", false},
	"archive":     {"归档或 XML 输入无法读取", "检查源文件、类型、大小限制及可移植路径，修复后重新只读检查。", "yss archive --help", false},
	"internal":    {"操作未完成", "保留原始错误、命令和版本信息；先查看命令帮助，仍无法恢复时提交最小复现。", "yss --help", false},
}

// Stable code inventory. New public failures must get a catalog entry; tests
// inspect production call sites, including the small subsystem fail helpers.
var errorCodes = strings.Fields(`AMBIGUOUS ARCHIVE ARGUMENT ARTIFACT ASSET ASSET_ALIAS ASSET_DEPTH ASSET_DUPLICATE_KEY ASSET_EMPTY ASSET_KEY ASSET_MULTIPLE_DOCUMENTS ASSET_NUMBER ASSET_PARSE ASSET_TAG ASSET_UNICODE ASSET_UTF8 BASELINE BASE_BUNDLE BINDING BINDING_CONFLICT BINDING_REQUIRED BUNDLE BUNDLE_RULE CANCELLED CANDIDATE_DRIFT CI_CONFIG CI_CONFLICT CI_EVIDENCE_DRIFT CI_IDENTITY CI_INPUT CI_RECEIPT CI_REFERENCE CI_SCOPE CI_SOURCE CONCURRENT CONFLICT CONTEXT CONTEXT_REFERENCE CONTEXT_SNAPSHOT CONTEXT_SNAPSHOT_STALE DIGEST EXISTS GOVERNED_REQUIRED IDENTITY INPUT INPUT_DRIFT INSTALLATION INTERRUPTED KIND LEGACY LEGACY_INTERRUPTED LEGACY_POLICY LIFECYCLE LIFECYCLE_ID LOCKED MERGE MIGRATION_REQUIRED NETWORK PATH PLAN PLAN_REQUIRED PLAN_VERSION PLATFORM PROFILE PROJECT_CI_REJECTED PROTECTED READ_ONLY RECOVERY_FAILED RESOLUTION RESOLUTION_POLICY RESOLUTION_STALE ROOT RUNTIME RUNTIME_BUSY RUNTIME_DATA RUNTIME_INTEGRITY RUNTIME_OWNER RUNTIME_SCHEMA RUNTIME_STATE SCHEMA SCHEMA_DRAFT_UNSUPPORTED SCHEMA_ID SCHEMA_ID_CONFLICT SCHEMA_OFFLINE SCHEMA_REF SCHEMA_REGEX_INCOMPATIBLE SCHEMA_VALIDATION SCOPE SKILL STATE SYNC_REQUIRED TRACKER TRACKING_CANCEL TRACKING_COMPLETION TRACKING_CONFLICT TRACKING_DEFERRAL TRACKING_DEFINITION TRACKING_DEPENDENCY TRACKING_ENTRY TRACKING_FEATURE TRACKING_ID TRACKING_IDENTITY TRACKING_PATH TRACKING_PROFILE TRACKING_PROGRESS TRACKING_REQUIRED TRACKING_ROUTE TRACKING_SCHEMA TRACKING_STAGE TRACKING_STALE TRACKING_TRANSITION UNPORTED VERIFY VERSION WORK_LAYOUT_CHECKPOINT WORK_LAYOUT_CONFIG WORK_LAYOUT_FEATURE WORK_LAYOUT_MIGRATION_REQUIRED WORK_LAYOUT_RESERVED XML XML_LIMIT ZIP ZIP_LIMIT CAPABILITY EXECUTION INTERNAL EVIDENCE PROVENANCE PERMISSION NOT_FOUND`)

func init() {
	errorCodes = append(errorCodes, strings.Fields(`CHECKPOINT_REQUIRED PROFILE_LINKS PROFILE_ROUTE TRANSACTION_PROFILE TRANSACTION_SCOPE`)...)
	errorCodes = append(errorCodes, strings.Fields(`ASSET_TRANSACTION_PENDING CONTEXT_MISSING FRONTEND_PROBE_AUTHORIZATION FRONTEND_PROBE_UNAVAILABLE GIT_BASELINE HANDOFF_POLICY_CAPABILITY NEEDS_INFO PLAN_REVIEW_POLICY_INVALID PLAN_REVIEW_PROTOCOL_REQUIRED READONLY_SOURCE_LAYOUT_REQUIRED WORK_LAYOUT_PATH WORK_LAYOUT_REFERENCE WORK_LAYOUT_SOURCE WORK_LAYOUT_VERIFY APPROVAL_REBIND_REQUIRED IMMUTABLE WORK_LAYOUT_SCHEMA`)...)
	errorCodes = append(errorCodes, strings.Fields(`ARGS INVALID UNKNOWN_ALIAS GIT_IGNORE WORK_LAYOUT_PERMISSION MISSING_REFERENCE YSS_ARGUMENT_INVALID YSS_COMMAND_FAILED YSS_FAMILY_IDENTITY_INVALID YSS_GIT_PROTECTED YSS_IDENTITY_INVALID YSS_METADATA_INVALID YSS_MIGRATION_CONFLICT YSS_OWNERSHIP_PROTECTED YSS_PATH_SAFETY YSS_SNAPSHOT_INVALID YSS_TARGET_INVALID YSS_UNPORTED YSS_UNSAFE_PATH`)...)
}

func errorFamily(code string) string {
	// These codes belong to frozen compatibility interfaces. Classify their
	// registered identifiers without reinterpreting the legacy message rules.
	switch code {
	case "ARGS", "INVALID", "UNKNOWN_ALIAS", "YSS_ARGUMENT_INVALID":
		return "argument"
	case "YSS_PATH_SAFETY", "YSS_UNSAFE_PATH", "YSS_TARGET_INVALID", "YSS_FAMILY_IDENTITY_INVALID", "YSS_IDENTITY_INVALID":
		return "identity"
	case "YSS_METADATA_INVALID", "YSS_SNAPSHOT_INVALID", "YSS_UNPORTED":
		return "version"
	case "YSS_GIT_PROTECTED", "YSS_OWNERSHIP_PROTECTED", "YSS_MIGRATION_CONFLICT", "GIT_IGNORE":
		return "conflict"
	}
	switch {
	case code == "ARGUMENT" || code == "KIND" || code == "SCOPE":
		return "argument"
	case code == "IDENTITY" || code == "ROOT" || code == "PATH" || code == "PROFILE" || code == "PROFILE_LINKS" || code == "PERMISSION" || code == "NOT_FOUND":
		return "identity"
	case code == "UNPORTED" || code == "CAPABILITY" || code == "VERSION" || code == "LEGACY" || code == "MIGRATION_REQUIRED" || code == "SYNC_REQUIRED" || strings.HasPrefix(code, "LEGACY_POLICY") || strings.HasPrefix(code, "BUNDLE") || code == "BASELINE" || code == "PROVENANCE":
		return "version"
	case strings.HasPrefix(code, "BINDING"):
		return "binding"
	case strings.HasPrefix(code, "PLAN") || strings.HasPrefix(code, "RESOLUTION") || code == "INPUT_DRIFT" || code == "CANDIDATE_DRIFT" || code == "BASE_BUNDLE":
		return "plan"
	case code == "CONFLICT" || code == "CONCURRENT" || code == "EXISTS" || code == "PROTECTED" || code == "MERGE" || code == "IMMUTABLE":
		return "conflict"
	case code == "INTERRUPTED" || code == "LEGACY_INTERRUPTED" || code == "STATE" || code == "LOCKED" || code == "RECOVERY_FAILED" || code == "CANCELLED" || code == "AMBIGUOUS" || code == "ASSET_TRANSACTION_PENDING" || code == "TRANSACTION_PROFILE" || code == "TRANSACTION_SCOPE":
		return "transaction"
	case strings.HasPrefix(code, "RUNTIME"):
		return "runtime"
	case strings.HasPrefix(code, "SCHEMA") || strings.HasPrefix(code, "ASSET_"):
		return "schema"
	case strings.HasPrefix(code, "ZIP") || strings.HasPrefix(code, "XML") || code == "ARCHIVE":
		return "archive"
	case code == "NETWORK":
		return "network"
	case code == "ARTIFACT" || code == "DIGEST" || code == "PLATFORM" || code == "INSTALLATION":
		return "artifact"
	case strings.HasPrefix(code, "CI_") || strings.HasPrefix(code, "TRACKING_") || strings.HasPrefix(code, "CONTEXT") || strings.HasPrefix(code, "LIFECYCLE") || strings.HasPrefix(code, "WORK_LAYOUT") || strings.HasPrefix(code, "FRONTEND_PROBE") || code == "MISSING_REFERENCE" || code == "HANDOFF_POLICY_CAPABILITY" || code == "GIT_BASELINE" || code == "NEEDS_INFO" || code == "READONLY_SOURCE_LAYOUT_REQUIRED" || code == "APPROVAL_REBIND_REQUIRED" || code == "PROJECT_CI_REJECTED" || code == "GOVERNED_REQUIRED" || code == "EVIDENCE" || code == "VERIFY" || code == "INPUT" || code == "SKILL" || code == "TRACKER" || code == "READ_ONLY" || code == "ASSET" || code == "CHECKPOINT_REQUIRED" || code == "PROFILE_ROUTE":
		return "governance"
	default:
		return "internal"
	}
}

func diagnosticFor(code string, err error, o options, profile string, result any) *domain.Diagnostic {
	def := errorFamilies[errorFamily(code)]
	d := &domain.Diagnostic{SchemaVersion: 1, ID: code, Code: code, Summary: def.summary, Cause: err.Error(), Context: map[string]any{"command": strings.Join(o.args, " "), "profile": profile}, Help: helpCommand(o.args)}
	root := o.values["root"]
	if root != "" {
		if p, e := filepath.Abs(root); e == nil {
			root = p
		}
		d.Context["root"] = root
	}
	for _, key := range []string{"tool-root", "file", "checkpoint", "plan-file", "task", "artifact", "implementation-root"} {
		if value := o.values[key]; value != "" {
			d.Context[key] = value
		}
	}
	var detail interface{ DiagnosticDetail() domain.ErrorDetail }
	if errors.As(err, &detail) {
		info := detail.DiagnosticDetail()
		d.ID = info.ID
		d.Cause = info.Cause
		for k, v := range info.Context {
			d.Context[k] = v
		}
	}
	if d.Cause != err.Error() {
		d.OriginalCause = err.Error()
	}
	d.Context["argv"] = commandArgv(o)
	var pe *os.PathError
	if errors.As(err, &pe) {
		d.Context["path"] = pe.Path
		d.Context["operation"] = pe.Op
		if errors.Is(pe.Err, os.ErrPermission) {
			d.ID = "FILESYSTEM_PERMISSION_DENIED"
		}
		if errors.Is(pe.Err, os.ErrNotExist) {
			d.ID = "INPUT_FILE_NOT_FOUND"
		}
	}
	d.Steps = []domain.DiagnosticStep{{Effect: "read-only", Description: def.recovery, Argv: strings.Fields(d.Help)}}
	d.Recheck = []domain.DiagnosticStep{{Effect: "read-only", Description: "先确认当前命令的输入条件，再重新执行原操作。", Argv: strings.Fields(d.Help)}}
	program := len(o.args) > 0 && (o.args[0] == "update" || o.args[0] == "upgrade")
	if program && o.values["tool-root"] != "" {
		d.Steps = append(d.Steps, domain.DiagnosticStep{Effect: "read-only", Description: "检查程序安装与事务状态。", Argv: []string{"yss", "update", "status", "--tool-root", o.values["tool-root"], "--json"}})
		d.Recheck = []domain.DiagnosticStep{d.Steps[len(d.Steps)-1]}
	} else if root != "" && code != "ARGUMENT" && code != "KIND" {
		d.Recheck = []domain.DiagnosticStep{{Effect: "read-only", Description: "确认项目身份、基线和冲突已恢复。", Argv: []string{"yss", "doctor", "--root", root, "--json"}}}
	}
	if d.ID == "PROJECT_ROOT_NOT_FOUND" {
		d.Steps = []domain.DiagnosticStep{{Effect: "read-only", Description: "将 --root 改为实际项目根目录；准备新建项目时先查看 init 帮助。", Argv: []string{"yss", "init", "--help"}}}
	}
	if d.ID == "PROJECT_METADATA_MISSING" {
		d.PossibleCauses = []string{"--root 指向了其他目录", "尚未初始化或接管该工程", "该实例使用历史元数据，需先诊断其格式"}
	}
	if d.ID == "SAVED_STAGE_PLAN_CHANGED" {
		action, _ := d.Context["planAction"].(string)
		checkpoint, _ := d.Context["checkpoint"].(string)
		file, _ := d.Context["file"].(string)
		if (action == "register" || action == "update") && checkpoint != "" && file != "" {
			d.Steps = []domain.DiagnosticStep{{Effect: "read-only", Description: "核对受影响输入后重新生成原始阶段计划。保存时重定向到项目内新文件，不加 --json；审阅后用 stage apply 消费。", Argv: []string{"yss", "stage", action, "--root", root, "--checkpoint", checkpoint, "--items", file}}}
			d.Recheck = []domain.DiagnosticStep{{Effect: "read-only", Description: "确认当前工作项有效、无过期证据；原保存计划已失效，不直接重试应用。", Argv: []string{"yss", "stage", "status", "--root", root, "--checkpoint", checkpoint, "--json"}}}
		}
	}
	if code == "ARGUMENT" {
		key := strings.Join(o.args, " ")
		if spec, ok := commands[key]; ok {
			d.Examples = strings.Split(safeExampleText(spec.help.examples), "\n")
		}
	}
	if code == "LEGACY_INTERRUPTED" {
		d.Steps = []domain.DiagnosticStep{{Effect: "read-only", Description: "核对旧元数据中的来源与原执行器版本；保留旧锁和事务，使用对应固定旧 CLI 的恢复入口。", Argv: []string{"yss", "help", "tutorial", "maintenance"}}}
		d.Unverified = append(d.Unverified, "实际旧执行器版本以旧实例来源和历史安装记录为准；没有记录时先调查。")
	} else if errorFamily(code) == "transaction" {
		if program && o.values["tool-root"] != "" {
			d.Steps = append(d.Steps, domain.DiagnosticStep{Effect: "write", Description: "恢复未完成程序事务。", Condition: "已核对安装状态与归档，且当前操作授权覆盖恢复。", Argv: []string{"yss", "update", "recover", "--tool-root", o.values["tool-root"], "--json"}})
		} else if root != "" && len(o.args) > 0 && o.args[0] == "migrate" {
			d.Steps = append(d.Steps, domain.DiagnosticStep{Effect: "read-only", Description: "先检查项目事务状态。", Argv: []string{"yss", "recover", "--root", root, "--json"}}, domain.DiagnosticStep{Effect: "write", Description: "恢复已确认的原生迁移事务。", Condition: "已核对 migration 事务范围与归档；此子命令直接写入。", Argv: []string{"yss", "migrate", "recover", "--root", root, "--json"}})
		} else if root != "" {
			d.Steps = append(d.Steps, domain.DiagnosticStep{Effect: "read-only", Description: "查询项目事务；旧 CLI 事务由固定旧执行器恢复。", Argv: []string{"yss", "recover", "--root", root, "--json"}}, domain.DiagnosticStep{Effect: "write", Description: "恢复已确认的原生项目事务。", Condition: "原生项目事务状态已核对，且当前授权覆盖恢复。", Argv: []string{"yss", "recover", "--root", root, "--apply", "--json"}})
		}
	}
	if d.ID == "RECOVERY_USER_MODIFICATION" {
		d.Steps = []domain.DiagnosticStep{{Effect: "read-only", Description: "保留当前修改与归档，比较受影响路径；由负责人确定保留内容和后续恢复范围。", Argv: []string{"yss", "diff", "--root", root, "--json"}}}
		d.Recheck = []domain.DiagnosticStep{{Effect: "read-only", Description: "受影响文件与已确认事务基线一致后，再查询恢复状态；否则保持阻断。", Argv: []string{"yss", "recover", "--root", root, "--json"}}}
	}
	if errorFamily(code) == "binding" {
		d.Steps = []domain.DiagnosticStep{{Effect: "read-only", Description: def.recovery, Condition: "使用已绑定插件的公开计划接口；取得新的保存计划后再应用。", Argv: []string{"yss", "sync", "--help"}}}
	}
	if def.version || d.ID == "METADATA_SCHEMA_UNSUPPORTED" || d.ID == "LEGACY_SCHEMA_UNSUPPORTED" {
		d.Version = map[string]any{"currentCLI": domain.Version, "protocolVersion": domain.ProtocolVersion, "minimumSupportedVersion": "尚未登记", "introducedVersion": "尚未登记", "fixedVersion": "尚未登记", "basis": "CLI/协议依据当前二进制；模板依据固定 Bundle；实例格式依据产生错误位置的已观察输入。最低/引入/修复版本尚无兼容记录。"}
		if b, err := bundle.Load(profile); err == nil {
			d.Version["currentTemplateCommit"] = b.TemplateCommit
		}
		for _, key := range []string{"metadataSchema", "instanceProtocol", "instanceCLI", "templateCommit", "metadataFile"} {
			if v, ok := d.Context[key]; ok {
				d.Version[key] = v
			}
		}
		d.Steps = append(d.Steps, domain.DiagnosticStep{Effect: "network", Description: "需要远程确认正式版本时只读查询。", Condition: "此步骤访问固定官方来源，不下载或安装。", Argv: []string{"yss", "upgrade", "--check", "--json"}})
	}
	m := resultObject(result)
	if m["diagnostics"] != nil || m["diagnostic"] != nil || m["receipt"] != nil {
		d.Report = result
	}
	if m["diagnostics"] != nil && isSemanticCommand(o) {
		argv := append([]string{"yss"}, o.args...)
		keys := make([]string, 0, len(o.values))
		for k := range o.values {
			if k != "json" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			argv = append(argv, "--"+k+"="+o.values[k])
		}
		argv = append(argv, "--json")
		d.Recheck = []domain.DiagnosticStep{{Effect: "read-only", Description: "补齐报告中的当前输入与证据后重新核验；核验不执行登记的业务测试或创建批准。", Argv: argv}}
	}
	if errorFamily(code) == "internal" {
		d.Context["environment"] = runtime.GOOS + "/" + runtime.GOARCH
		d.Context["cliVersion"] = domain.Version
		d.Unverified = append(d.Unverified, "未登记更具体的原因；反馈时提供脱敏的最小复现、原始错误、版本及来源。https://github.com/iloveZzz/yss-cli/issues")
	}
	return d
}

func commandArgv(o options) []string {
	if o.rawArgs != nil {
		return append([]string{"yss"}, o.rawArgs...)
	}
	argv := append([]string{"yss"}, o.args...)
	keys := []string{}
	for key := range o.values {
		if key != "json" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		argv = append(argv, "--"+key+"="+o.values[key])
	}
	return argv
}

func quoteArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\r'\"$`;&|<>*?()[]{}!\\") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
func formatArgv(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = quoteArg(a)
	}
	return strings.Join(out, " ")
}
func effectLabel(effect string) string {
	switch effect {
	case "write":
		return "写入"
	case "network":
		return "联网查询"
	default:
		return "只读"
	}
}

func renderHumanDiagnostic(d *domain.Diagnostic) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s：%s\n问题类型：%s\n原因：%s\n", d.Code, d.Summary, d.ID, d.Cause)
	if d.OriginalCause != "" {
		fmt.Fprintf(&out, "原始原因：%s\n", d.OriginalCause)
	}
	keys := make([]string, 0, len(d.Context))
	for k := range d.Context {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v := d.Context[k]; v != "" {
			if k == "argv" {
				fmt.Fprintf(&out, "实际命令：%s\n", formatArgv(v.([]string)))
				continue
			}
			fmt.Fprintf(&out, "%s：%v\n", humanLabel(k), v)
		}
	}
	for _, cause := range d.PossibleCauses {
		fmt.Fprintf(&out, "可能原因：%s\n", cause)
	}
	for _, fact := range d.Unverified {
		fmt.Fprintf(&out, "待核验：%s\n", fact)
	}
	if len(d.Examples) > 0 {
		fmt.Fprintf(&out, "最小示例（按帮助中的适用条件准备输入）：\n  %s\n", d.Examples[0])
	}
	for _, step := range d.Steps {
		fmt.Fprintf(&out, "处理（%s）：%s\n", effectLabel(step.Effect), step.Description)
		if step.Condition != "" {
			fmt.Fprintf(&out, "  条件：%s\n", step.Condition)
		}
		if len(step.Argv) > 0 {
			fmt.Fprintf(&out, "  %s\n", formatArgv(step.Argv))
		}
	}
	if d.Report != nil {
		renderReport(&out, resultObject(d.Report))
	}
	if d.Version != nil {
		fmt.Fprintf(&out, "版本：当前 CLI %v；协议 %v；最低支持版本 %v；修复版本 %v\n", d.Version["currentCLI"], d.Version["protocolVersion"], d.Version["minimumSupportedVersion"], d.Version["fixedVersion"])
		fmt.Fprintf(&out, "版本依据：%v\n", d.Version["basis"])
		for _, key := range []string{"introducedVersion", "currentTemplateCommit", "templateCommit", "metadataSchema", "instanceProtocol", "instanceCLI", "metadataFile"} {
			if value, ok := d.Version[key]; ok {
				fmt.Fprintf(&out, "%s：%v\n", humanLabel(key), value)
			}
		}
	}
	for _, step := range d.Recheck {
		fmt.Fprintf(&out, "复验：%s\n  %s\n", step.Description, formatArgv(step.Argv))
	}
	fmt.Fprintf(&out, "相关帮助：%s；yss help errors %s", d.Help, d.Code)
	return out.String()
}

func renderReport(out *strings.Builder, m map[string]any) {
	if rows, ok := m["diagnostics"].([]any); ok {
		for _, v := range rows {
			if row, ok := v.(map[string]any); ok {
				fmt.Fprintf(out, "检查诊断：%s — %s\n", firstText(row, "code", "id"), firstText(row, "message", "reason"))
				if text := firstText(row, "recovery"); text != "" {
					fmt.Fprintf(out, "恢复条件：%s\n", text)
				}
				if ref := row["source_ref"]; ref != nil && ref != "" {
					fmt.Fprintf(out, "证据来源：%v\n", ref)
				}
			}
		}
	}
	if rows, ok := m["checks"].([]any); ok {
		for _, v := range rows {
			if row, ok := v.(map[string]any); ok {
				fmt.Fprintf(out, "检查：%s — %s\n", firstText(row, "id", "name", "code"), statusLabel(firstText(row, "status", "passed")))
				if message := firstText(row, "message", "reason"); message != "" {
					fmt.Fprintf(out, "  %s\n", message)
				}
			}
		}
	}
	if value := m["diagnostic"]; value != nil {
		if diagnostic, ok := value.(map[string]any); ok {
			keys := []string{}
			for key := range diagnostic {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				fmt.Fprintf(out, "安装%s：%s\n", humanLabel(key), compactValue(diagnostic[key]))
			}
		} else {
			fmt.Fprintf(out, "安装诊断：%s\n", compactValue(value))
		}
	}
	if receipt, ok := m["receipt"].(map[string]any); ok {
		fmt.Fprintf(out, "事务回执：%s；类型 %s；状态 %s\n", firstText(receipt, "transactionId"), firstText(receipt, "kind"), statusLabel(firstText(receipt, "status")))
	}
}

func firstText(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := m[key]; value != nil && value != "" {
			return fmt.Sprint(value)
		}
	}
	return ""
}
func statusLabel(value string) string {
	labels := map[string]string{"passed": "通过", "blocked": "阻断", "failed": "未通过", "error": "错误", "not-applicable": "不适用", "not-evaluated": "未评估", "true": "通过", "false": "未通过", "valid": "有效"}
	if label := labels[value]; label != "" {
		return label
	}
	if value == "" {
		return "待核验"
	}
	return value
}

func renderErrorHelp(code string) (string, error) {
	if code == "" {
		var out strings.Builder
		out.WriteString("YSS 错误索引（查看详情：yss help errors <错误码>）\n")
		codes := append([]string{}, errorCodes...)
		sort.Strings(codes)
		for _, c := range codes {
			fmt.Fprintf(&out, "  %-30s %s\n", c, errorFamilies[errorFamily(c)].summary)
		}
		return out.String(), nil
	}
	known := false
	for _, c := range errorCodes {
		if c == code {
			known = true
			break
		}
	}
	if !known {
		return "", argumentError("未知错误码: " + code + "；查看 yss help errors")
	}
	def := errorFamilies[errorFamily(code)]
	extra := ""
	if code == "IDENTITY" {
		extra = "\n常见类型：PROJECT_ROOT_NOT_FOUND、PROJECT_METADATA_MISSING、PROFILE_IDENTITY_CONFLICT、METADATA_SCHEMA_UNSUPPORTED。"
	}
	if code == "PATH" {
		extra = "\n常见类型：UNSAFE_ROOT_COMPONENT；核对错误中的实际路径组件，使用已确认的普通目录。"
	}
	if code == "LEGACY_INTERRUPTED" {
		extra += "\n常见类型：LEGACY_EXECUTOR_PENDING。保留旧事务；固定旧执行器的 recover 或 migrate recover 消费原事务，原生 recover --apply 不能替代。"
	}
	if code == "INPUT_DRIFT" {
		extra += "\n常见类型：SAVED_PLAN_INPUT_CHANGED、SAVED_STAGE_PLAN_CHANGED。先核对 affectedInputs/path；原计划失效后重新生成与审阅。"
	}
	if code == "RECOVERY_FAILED" {
		extra += "\n常见类型：RECOVERY_USER_MODIFICATION。保留用户当前内容和归档，确认内容与恢复范围后再检查，不能覆盖后续修改。"
	}
	if strings.HasPrefix(code, "BINDING") {
		extra += "\n常见类型：PLUGIN_BINDING_SOURCE_MISMATCH、PLUGIN_BINDING_UPGRADE_REQUIRED。通过实际绑定插件的公开升级/迁移计划更新，身份与 binding 同一事务应用。"
	}
	if code == "UNPORTED" {
		extra += "\n能力边界：核对 yss capabilities --json。只有当前帮助及能力表明确支持的入口可用；结构 check 只验证格式，不能替代领域 verify。"
	}
	if def.version {
		extra += "\n版本：最低支持版本与修复版本尚未登记时按实际输出标注；远程版本用 yss upgrade --check --json 查询。"
	}
	detail := "详细运行诊断：在统一原生命令上增加 --human，或 --json --diagnostics。"
	if strings.HasPrefix(code, "YSS_") || code == "ARGS" || code == "INVALID" || code == "UNKNOWN_ALIAS" {
		detail = "历史兼容入口：保持其冻结输出和参数合同。使用 yss help errors " + code + " 查阅诊断；不要向历史入口追加 --human 或 --diagnostics。"
	}
	return fmt.Sprintf("%s — %s\n\n原因：以当前执行错误和逐项诊断为依据；可能原因与已确认事实分别显示。%s\n\n处理：%s\n复验：修复当前输入后重复原只读核验；写入前重新保存并审阅计划。\n帮助：%s\n%s", code, def.summary, extra, def.recovery, def.help, detail), nil
}
