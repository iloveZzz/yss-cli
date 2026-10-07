// Package compat preserves legacy discovery and rejection contracts. Native
// execution is opt-in until the legacy success schemas and policies have parity.
package compat

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/cli"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/project"
)

//go:embed spec-help.txt
var specHelp string

const legacyGap = "旧成功 schema、force/prune、分发选择与旧事务格式尚未等价闭合；请继续使用固定版本旧 CLI。显式 --native 使用 Go 协议 1，不能视为旧接口等价证明"

type legacyOptions struct {
	command string
	values  map[string]string
	flags   map[string]bool
}

// Run is the alias seam used by cmd/yss; it never invokes Node or Python.
func Run(ctx context.Context, alias string, args []string, stdout, stderr io.Writer) int {
	p, ok := profileForAlias(alias)
	if !ok {
		fmt.Fprintln(stderr, "UNKNOWN_ALIAS: "+alias)
		return 1
	}
	for _, arg := range args {
		if arg == "--native" {
			return runNative(ctx, p, args, stdout, stderr)
		}
	}
	jsonOutput := contains(args, "--json")
	if p.Name == "spec" && (len(args) == 0 || args[0] != "migrate") {
		if containsAny(args, "--help", "-h", "-help") {
			fmt.Fprint(stdout, specHelp)
			return 0
		}
		if containsAny(args, "--version", "-v", "-version") {
			fmt.Fprintf(stdout, "create-yss-spec %s\n", p.LegacyVersion)
			return 0
		}
	}
	opts, err := parseLegacy(p, args)
	if err != nil {
		return legacyFailure(p, opts.command, jsonOutput, err.code, err.message, stdout, stderr)
	}
	if opts.flags["help"] {
		help := specialHelp(p.LegacyCommand)
		if jsonOutput {
			_ = json.NewEncoder(stdout).Encode(map[string]any{"schemaVersion": 1, "command": "help", "status": "ok", "help": help})
		} else {
			fmt.Fprint(stdout, help)
		}
		return 0
	}
	if opts.flags["version"] {
		if jsonOutput {
			_ = json.NewEncoder(stdout).Encode(map[string]any{"schemaVersion": 1, "command": "version", "status": "ok", "packageName": p.LegacyCommand, "cliVersion": p.LegacyVersion})
		} else {
			fmt.Fprintln(stdout, p.LegacyVersion)
		}
		return 0
	}
	return legacyFailure(p, opts.command, jsonOutput, "UNPORTED", legacyGap, stdout, stderr)
}

func profileForAlias(alias string) (domain.Profile, bool) {
	alias = strings.TrimSuffix(filepath.Base(alias), ".exe")
	for _, p := range domain.Profiles {
		if alias == p.LegacyCommand {
			return p, true
		}
	}
	return domain.Profile{}, false
}
func contains(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}
func containsAny(args []string, values ...string) bool {
	for _, v := range values {
		if contains(args, v) {
			return true
		}
	}
	return false
}

type legacyError struct{ code, message string }

func parseLegacy(p domain.Profile, args []string) (legacyOptions, *legacyError) {
	o := legacyOptions{"init", map[string]string{}, map[string]bool{}}
	if len(args) > 0 && args[0] == "migrate" {
		return parseMigration(args[1:])
	}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		if p.Name != "spec" || containsAny([]string{args[0]}, "attach", "sync", "diff", "doctor", "skills", "assets", "update", "upgrade") {
			o.command = args[0]
			args = args[1:]
		}
	}
	values := map[string]string{"--target-dir": "targetDir", "--project-name": "projectName", "--business-domain": "businessDomain", "--team-size": "teamSize", "--issue-tracker": "issueTracker"}
	if p.Name == "spec" {
		values["--agent-runtime"] = "agentRuntime"
	}
	flags := map[string]string{"--plan": "plan", "--prune": "prune", "--migrate-layout": "migrateLayout", "--apply": "apply", "--dry-run": "dryRun", "--force": "force", "--json": "json", "--git-init": "gitInit", "--help": "help", "-h": "help", "--version": "version", "-v": "version", "--include-example-docs": "includeExampleDocs", "--no-example-docs": "includeExampleDocs"}
	for i := 0; i < len(args); i++ {
		flag := args[i]
		if key, ok := values[flag]; ok {
			if i+1 == len(args) || strings.HasPrefix(args[i+1], "--") || p.Name != "spec" && strings.HasPrefix(args[i+1], "-") {
				message := flag + " 缺少值"
				if p.Name == "spec" {
					message = flag + " 需要一个值"
				}
				return o, &legacyError{"INVALID", message}
			}
			i++
			o.values[key] = args[i]
		} else if key, ok := flags[flag]; ok {
			o.flags[key] = flag != "--no-example-docs"
		} else {
			message := "未知参数: " + flag
			if p.Name == "spec" {
				message = "不支持的参数：" + flag
			}
			return o, &legacyError{"INVALID", message}
		}
	}
	if p.Name == "spec" {
		if o.command == "init" {
			if o.flags["prune"] {
				return o, &legacyError{"INVALID", "--prune 仅适用于 sync"}
			}
			if o.flags["migrateLayout"] {
				return o, &legacyError{"INVALID", "--migrate-layout 仅适用于 attach/sync"}
			}
		}
		return o, nil
	}
	if !containsAny([]string{o.command}, "init", "attach", "sync", "diff", "doctor", "recover", "update", "upgrade") {
		return o, &legacyError{"INVALID", "未知命令"}
	}
	if o.flags["apply"] && (o.flags["dryRun"] || o.flags["plan"]) {
		return o, &legacyError{"INVALID", "--apply 与 --dry-run 不可同时使用"}
	}
	if o.flags["gitInit"] && o.command != "init" {
		return o, &legacyError{"INVALID", "--git-init 仅用于 init"}
	}
	accepted := map[string][]string{
		"init":   {"projectName", "businessDomain", "teamSize", "targetDir", "issueTracker", "gitInit", "includeExampleDocs", "dryRun"},
		"attach": {"projectName", "businessDomain", "teamSize", "targetDir", "issueTracker", "includeExampleDocs", "dryRun", "apply", "force", "plan", "migrateLayout"},
		"sync":   {"targetDir", "dryRun", "apply", "force", "plan", "prune", "migrateLayout"},
		"diff":   {"targetDir", "dryRun"}, "doctor": {"targetDir"}, "recover": {"targetDir", "dryRun", "apply"}, "update": {"dryRun", "force"}, "upgrade": {"dryRun", "force"},
	}
	if !o.flags["help"] && !o.flags["version"] {
		for _, flag := range args {
			if !strings.HasPrefix(flag, "-") {
				continue
			}
			key := values[flag]
			if key == "" {
				key = flags[flag]
			}
			if !containsAny(append(accepted[o.command], "help", "version", "json"), key) {
				return o, &legacyError{"INVALID", flag + " 不适用于 " + o.command}
			}
		}
	}
	tracker := o.values["issueTracker"]
	if tracker != "" && !containsAny([]string{tracker}, "local", "local-markdown", "github", "gitlab") {
		return o, &legacyError{"INVALID", "issue-tracker 必须为 local-markdown、github 或 gitlab"}
	}
	for _, key := range []string{"projectName", "businessDomain", "teamSize"} {
		for _, r := range o.values[key] {
			if r < 32 || r == 127 {
				return o, &legacyError{"INVALID", key + " 不允许控制字符"}
			}
		}
	}
	if p.Name == "design" && o.command == "init" && !o.flags["help"] && !o.flags["version"] {
		for _, key := range []string{"projectName", "businessDomain", "targetDir"} {
			if o.values[key] == "" || o.values[key] == "未指定" {
				return o, &legacyError{"INVALID", "init 需要 --project-name、--business-domain 和 --target-dir"}
			}
		}
	}
	return o, nil
}

func parseMigration(args []string) (legacyOptions, *legacyError) {
	o := legacyOptions{"migrate", map[string]string{}, map[string]bool{}}
	action := "status"
	if len(args) > 0 {
		action = args[0]
		args = args[1:]
	}
	allowed := map[string][]string{"plan": {"targetDir", "output", "archiveDir", "resolutions", "prune", "migrateLayout", "json"}, "apply": {"targetDir", "plan", "json"}, "status": {"targetDir", "json"}, "recover": {"targetDir", "apply", "json"}, "rollback": {"targetDir", "apply", "json"}}
	if _, ok := allowed[action]; !ok {
		return o, &legacyError{"ARGS", "migrate plan|apply|status|recover|rollback"}
	}
	values := map[string]string{"--target-dir": "targetDir", "--output": "output", "--plan": "plan", "--archive-dir": "archiveDir", "--resolutions": "resolutions"}
	flags := map[string]string{"--apply": "apply", "--prune": "prune", "--migrate-layout": "migrateLayout", "--json": "json"}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		flag := args[i]
		key := values[flag]
		if key == "" {
			key = flags[flag]
		}
		if key == "" || !contains(allowed[action], key) {
			return o, &legacyError{"ARGS", "不适用的参数: " + flag}
		}
		if seen[key] {
			return o, &legacyError{"ARGS", "重复参数: " + flag}
		}
		seen[key] = true
		if values[flag] != "" {
			if i+1 == len(args) || strings.HasPrefix(args[i+1], "--") {
				return o, &legacyError{"ARGS", "参数缺少值: " + flag}
			}
			i++
			o.values[key] = args[i]
		} else {
			o.flags[key] = true
		}
	}
	return o, nil
}

func legacyFailure(p domain.Profile, command string, wantsJSON bool, code, message string, stdout, stderr io.Writer) int {
	if p.Name == "spec" && command != "migrate" {
		if wantsJSON {
			_ = json.NewEncoder(stdout).Encode(ErrorEnvelope(message, mapSpecCode(code, message)))
		} else {
			fmt.Fprintln(stderr, message)
		}
	} else {
		if wantsJSON {
			_ = json.NewEncoder(stdout).Encode(map[string]any{"schemaVersion": 1, "command": command, "status": "error", "code": code, "message": message})
		}
		if p.Name != "spec" || !wantsJSON {
			fmt.Fprintln(stderr, message)
		}
	}
	return 1
}
func specialHelp(alias string) string {
	return alias + " init|attach|sync|diff|doctor|recover|update|upgrade|migrate\nmigrate plan|apply|status|recover|rollback：固定计划、持久归档和最近升级回退。\n--target-dir <目录> --project-name <名称> --business-domain <领域> --team-size <规模>\n--issue-tracker <local-markdown|github|gitlab> --git-init --no-example-docs\nattach/sync 默认只预览，写入必须 --apply；冲突显式 --apply --force。\n--plan --prune（仅 sync） --migrate-layout（旧目录显式迁移） --dry-run --json --version\ndoctor/diff/recover 默认只读；recover --apply 恢复未完成事务。init 只接受空目录。\n"
}

// runNative intentionally preserves the native envelope, not legacy success JSON.
func runNative(ctx context.Context, p domain.Profile, args []string, stdout, stderr io.Writer) int {
	translated := []string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--native":
			continue
		case "--profile":
			return nativeFailure(p, "ARGUMENT", "旧入口 Profile 固定，不允许 --profile", args, stdout, stderr)
		case "--force", "--prune", "--migrate-layout", "--git-init", "--agent-runtime", "--issue-tracker", "--include-example-docs", "--no-example-docs", "--archive-dir", "--resolutions":
			return nativeFailure(p, "UNPORTED", "尚未等价迁移参数: "+a, args, stdout, stderr)
		case "--dry-run":
			translated = append(translated, "--plan")
		default:
			if strings.HasPrefix(a, "--profile=") {
				return nativeFailure(p, "ARGUMENT", "旧入口 Profile 固定，不允许 --profile", args, stdout, stderr)
			}
			translated = append(translated, a)
		}
	}
	if len(translated) == 0 || strings.HasPrefix(translated[0], "-") {
		translated = append([]string{"init"}, translated...)
	}
	if translated[0] == "upgrade" {
		translated[0] = "update"
	}
	if translated[0] == "recover" && !contains(translated, "--apply") {
		translated = append([]string{"migrate", "status"}, translated[1:]...)
	}
	if translated[0] == "migrate" && len(translated) > 1 {
		action := translated[1]
		if action == "plan" {
			translated = append([]string{"migrate", "--plan"}, translated[2:]...)
			for i := range translated {
				if translated[i] == "--output" {
					translated[i] = "--out"
				}
			}
		}
		if action == "apply" {
			translated = append([]string{"migrate", "--apply"}, translated[2:]...)
			for i := range translated {
				if translated[i] == "--plan" {
					translated[i] = "--plan-file"
				}
			}
		}
		if (action == "rollback" || action == "recover") && !contains(translated, "--apply") {
			return nativeFailure(p, "UNPORTED", "旧恢复/回退预览格式尚未迁移；原生执行必须显式 --apply，状态用 migrate status", args, stdout, stderr)
		}
	}
	// Native transaction commands do not rebuild a project Plan. Bind the fixed
	// alias to the installed identity before inspecting or mutating its journal.
	transactionCommand := translated[0] == "recover" || translated[0] == "migrate" && len(translated) > 1 && containsAny([]string{translated[1]}, "status", "recover", "rollback")
	if transactionCommand {
		root := "."
		rootOverride := ""
		for i, arg := range translated {
			if (arg == "--target-dir" || arg == "--root") && i+1 < len(translated) {
				if arg == "--root" {
					rootOverride = translated[i+1]
				} else {
					root = translated[i+1]
				}
			}
			if key, value, ok := strings.Cut(arg, "="); ok {
				if key == "--root" {
					rootOverride = value
				}
				if key == "--target-dir" {
					root = value
				}
			}
		}
		if rootOverride != "" {
			root = rootOverride
		}
		if _, err := project.Detect(root, p.Name, false); err != nil {
			code := "IDENTITY"
			var e *domain.Error
			if errors.As(err, &e) {
				code = e.Code
			}
			return nativeFailure(p, code, err.Error(), args, stdout, stderr)
		}
	}
	translated = append(translated, "--profile", p.Name)
	return cli.Run(ctx, translated, stdout, stderr)
}
func nativeFailure(p domain.Profile, code, message string, args []string, stdout, stderr io.Writer) int {
	if contains(args, "--json") {
		_ = json.NewEncoder(stdout).Encode(domain.Envelope{OutputVersion: 1, Version: domain.Version, ProtocolVersion: 1, Profile: p.Name, Command: "compat", Status: "error", Code: code, Result: map[string]any{"message": message}})
	} else {
		fmt.Fprintln(stderr, code+": "+message)
	}
	return 1
}
