package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance"
	"github.com/iloveZzz/yss-cli/internal/project"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"github.com/iloveZzz/yss-cli/internal/updater"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type options struct {
	args       []string
	values     map[string]string
	json       bool
	duplicates []string
}

func parse(args []string) (options, error) {
	o := options{values: map[string]string{}}
	for _, a := range args {
		if a == "--json" || a == "--json=true" {
			o.json = true
		}
	}
	boolean := booleanOptions()
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-h" {
			a = "--help"
		}
		if a == "-V" {
			a = "--version"
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") {
			return o, argumentError("未知短参数: " + a)
		}
		if !strings.HasPrefix(a, "--") {
			o.args = append(o.args, a)
			continue
		}
		a = strings.TrimPrefix(a, "--")
		key, _, _ := strings.Cut(a, "=")
		if _, known := argumentSpecs[key]; !known {
			return o, unknownOption(key, optionNames())
		}
		if key, value, ok := strings.Cut(a, "="); ok {
			if _, exists := o.values[key]; exists {
				o.duplicates = append(o.duplicates, key)
			}
			if boolean[key] && value != "true" && value != "false" {
				return o, domain.Fail("ARGUMENT", "布尔参数只接受 true/false: --"+key)
			}
			o.values[key] = value
			continue
		}
		if _, exists := o.values[a]; exists {
			o.duplicates = append(o.duplicates, a)
		}
		if boolean[a] {
			o.values[a] = "true"
			continue
		}
		if i+1 == len(args) || strings.HasPrefix(args[i+1], "--") || (args[i+1] == "-h" || args[i+1] == "-V") {
			return o, argumentError("参数缺少值: --" + a + " " + argumentSpecs[a].placeholder)
		}
		i++
		o.values[a] = args[i]
	}
	o.json = o.values["json"] == "true"
	if isSemanticCommand(o) && len(o.duplicates) > 0 {
		return o, &domain.Error{Code: "ARGUMENT", Message: "重复参数: --" + o.duplicates[0], Exit: 2}
	}
	return o, nil
}
func isSemanticCommand(o options) bool {
	if len(o.args) < 2 {
		return false
	}
	group, action := o.args[0], o.args[1]
	if group == "lifecycle" && (action == "route" || action == "verify-daily") {
		return true
	}
	return action == "verify" && (group == "lifecycle" || group == "contract" || group == "evidence" || group == "handoff") || group == "project-ci" && (action == "check" || action == "verify") && o.values["scope"] != "native-go"
}
func semInputCode(code string) bool {
	switch code {
	case "ARGUMENT", "ROOT", "PATH", "IDENTITY", "LEGACY", "BASELINE", "UNPORTED", "CANCELLED", "CAPABILITY", "INPUT", "EXECUTION", "INTERNAL":
		return true
	}
	return false
}
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	o, err := parse(args)
	command := ""
	if len(o.args) > 0 {
		command = o.args[0]
	}
	if o.values["version"] == "true" {
		command = "version"
	}
	if err == nil && (command == "" || o.values["help"] == "true" || command == "help") {
		var help string
		help, err = renderHelp(o.args)
		if err == nil {
			fmt.Fprintln(stdout, help)
			return 0
		}
	}
	if err == nil {
		err = validateArguments(command, o)
	}
	inputError := err != nil
	delete(o.values, "help")
	delete(o.values, "version")
	profile := o.values["profile"]
	if err == nil {
		var result any
		result, profile, err = execute(ctx, command, o)
		if err == nil {
			if o.json {
				_ = json.NewEncoder(stdout).Encode(domain.Envelope{OutputVersion: 1, Version: domain.Version, ProtocolVersion: domain.ProtocolVersion, Command: command, Profile: profile, Status: "ok", Code: "OK", Result: result})
			} else {
				b, _ := json.MarshalIndent(result, "", "  ")
				fmt.Fprintln(stdout, string(b))
			}
			return 0
		}
	}
	code := "INTERNAL"
	exit := 1
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		code = "CANCELLED"
	}
	var de *domain.Error
	if errors.As(err, &de) {
		code = de.Code
		if de.Exit != 0 {
			exit = de.Exit
		}
	}
	if isSemanticCommand(o) && semInputCode(code) {
		exit = 2
	}
	var se *schema.Error
	if errors.As(err, &se) {
		code = se.Code
	}
	var result any = map[string]any{"message": err.Error()}
	var reported interface{ ErrorResult() any }
	if errors.As(err, &reported) {
		result = reported.ErrorResult()
	} else if isSemanticCommand(o) && !inputError {
		result = governance.InputFailureReport(ctx, o.values["root"], command, err)
	}
	if code == "ARGUMENT" {
		exit = 2
		message := err.Error()
		if !strings.Contains(message, "--help") {
			message += "；查看 " + helpCommand(o.args)
		}
		if inputError || !isSemanticCommand(o) {
			result = map[string]any{"message": message}
		}
		err = errors.New(message)
	}
	if o.json {
		_ = json.NewEncoder(stdout).Encode(domain.Envelope{OutputVersion: 1, Version: domain.Version, ProtocolVersion: domain.ProtocolVersion, Command: command, Profile: profile, Status: "error", Code: code, Result: result})
	} else {
		if isSemanticCommand(o) && !inputError {
			b, _ := json.MarshalIndent(result, "", "  ")
			fmt.Fprintln(stdout, string(b))
		}
		fmt.Fprintln(stderr, code+": "+err.Error())
	}
	return exit
}
func execute(ctx context.Context, command string, o options) (any, string, error) {
	if command == "upgrade" {
		if len(o.args) != 1 || len(o.duplicates) > 0 {
			return nil, "", &domain.Error{Code: "ARGUMENT", Message: "upgrade 不接受子命令、位置参数或重复参数", Exit: 2}
		}
		for key, value := range o.values {
			switch key {
			case "json", "help", "check":
			case "to", "tool-root":
				if strings.TrimSpace(value) == "" {
					return nil, "", &domain.Error{Code: "ARGUMENT", Message: "upgrade 参数不能为空: --" + key, Exit: 2}
				}
			default:
				return nil, "", &domain.Error{Code: "ARGUMENT", Message: "upgrade 不支持 --" + key + "；项目模板升级使用 yss sync", Exit: 2}
			}
		}
		result, err := (updater.UpgradeClient{}).Run(ctx, updater.UpgradeRequest{Check: o.values["check"] == "true", To: o.values["to"], ToolRoot: o.values["tool-root"]})
		return result, "", err
	}
	profile := o.values["profile"]
	root := o.values["root"]
	for _, flag := range []string{"root", "target-dir"} {
		if value, present := o.values[flag]; present && strings.TrimSpace(value) == "" {
			return nil, profile, domain.Fail("ARGUMENT", "项目根参数不能为空: --"+flag)
		}
	}
	if root == "" {
		root = o.values["target-dir"]
	}
	if root == "" {
		root = "."
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, profile, err
	}
	if err = ctx.Err(); err != nil {
		return nil, profile, domain.Wrap("CANCELLED", err)
	}
	if command == "init" || command == "attach" || command == "sync" || command == "diff" || command == "doctor" || command == "recover" || command == "rollback" || command == "version" || command == "capabilities" {
		if len(o.args) > 1 {
			return nil, profile, domain.Fail("ARGUMENT", "不支持额外子命令或位置参数")
		}
	}
	if command == "migrate" && len(o.args) > 1 {
		if len(o.args) > 2 {
			return nil, profile, domain.Fail("ARGUMENT", "迁移子命令参数过多")
		}
		switch o.args[1] {
		case "plan":
			o.values["plan"] = "true"
		case "apply":
			o.values["apply"] = "true"
		case "status", "recover", "rollback":
		default:
			return nil, profile, domain.Fail("ARGUMENT", "未知迁移子命令: "+o.args[1])
		}
	}
	if command == "bundle" {
		return bundleCommand(ctx, o)
	}
	if command == "version" || o.values["version"] == "true" {
		return map[string]any{"version": domain.Version, "protocolVersion": domain.ProtocolVersion, "profiles": domain.Profiles, "source": domain.BuildProvenance(), "cliCommit": domain.BuildProvenance().Commit, "sourceState": domain.BuildProvenance().SourceState}, profile, nil
	}
	if command == "capabilities" {
		return capabilities(), profile, nil
	}
	if command == "update" {
		if len(o.args) > 2 {
			return nil, "", domain.Fail("ARGUMENT", "程序升级子命令参数过多")
		}
		for k := range o.values {
			switch k {
			case "json", "help", "version", "tool-root", "artifact", "sha256", "plan", "apply", "out", "plan-file":
			default:
				return nil, "", domain.Fail("ARGUMENT", "程序升级不接受项目参数: --"+k)
			}
		}
		action := "plan"
		if len(o.args) > 1 {
			action = o.args[1]
		}
		if action == "status" || action == "recover" || action == "rollback" {
			for k := range o.values {
				if k != "json" && k != "tool-root" {
					return nil, "", domain.Fail("ARGUMENT", "程序状态、恢复及回退仅接受 --tool-root 与 --json")
				}
			}
			if action == "recover" {
				r, e := updater.Recover(ctx, o.values["tool-root"])
				return r, "", e
			}
			if action == "rollback" {
				r, e := updater.Rollback(ctx, o.values["tool-root"])
				return r, "", e
			}
			r, e := updater.Status(o.values["tool-root"])
			return r, "", e
		}
		if action != "plan" && action != "apply" {
			return nil, "", domain.Fail("ARGUMENT", "程序升级只支持 plan|apply|status|recover|rollback")
		}
		if action == "apply" || o.values["apply"] == "true" {
			if o.values["plan-file"] == "" {
				return nil, "", domain.Fail("PLAN_REQUIRED", "程序安装须 --apply --plan-file <已保存计划>")
			}
			p, e := updater.ReadPlan(o.values["plan-file"])
			if e != nil {
				return nil, "", e
			}
			toolRoot, e := filepath.Abs(o.values["tool-root"])
			if e != nil || o.values["tool-root"] == "" || p.ToolRoot != toolRoot {
				return nil, "", domain.Fail("PLAN", "程序升级计划与显式工具根不匹配")
			}
			r, e := updater.Apply(ctx, p)
			return r, "", e
		}
		if o.values["artifact"] == "" || o.values["sha256"] == "" {
			return nil, "", domain.Fail("ARGUMENT", "离线升级须指定 --artifact 与 --sha256；程序升级不迁移项目")
		}
		p, e := updater.Build(o.values["tool-root"], o.values["artifact"], o.values["sha256"])
		if e != nil {
			return nil, "", e
		}
		if file := o.values["out"]; file != "" {
			if e = updater.SavePlan(p, file); e != nil {
				return nil, "", e
			}
		}
		return p, "", nil
	}
	if command == "rollback" {
		for k := range o.values {
			switch k {
			case "root", "target-dir", "profile", "json", "help", "apply":
			default:
				return nil, profile, domain.Fail("ARGUMENT", "rollback不支持参数: --"+k)
			}
		}
		if e := project.CheckLegacyState(root, profile); e != nil {
			return nil, profile, e
		}
		id, e := project.RecoveryIdentity(root, profile)
		if e != nil {
			return nil, profile, e
		}
		profile = id.Profile.Name
		if o.values["apply"] != "true" {
			r, e := transaction.Status(root)
			return r, profile, e
		}
		r, e := transaction.RollbackContextWithValidator(ctx, root, func(summary transaction.Summary, paths []string) error {
			switch summary.Kind {
			case "init", "attach", "sync", "migrate", "skills", "assets":
			default:
				return domain.Fail("KIND", "rollback仅支持项目事务")
			}
			current, e := project.RecoveryIdentity(root, profile)
			if e != nil {
				return e
			}
			if current.Profile.Name != profile {
				return domain.Fail("IDENTITY", "回退时Profile已变化")
			}
			return nil
		})
		return r, profile, e
	}
	if command == "recover" || command == "migrate" && len(o.args) > 1 && (o.args[1] == "rollback" || o.args[1] == "recover" || o.args[1] == "status") {
		for k := range o.values {
			switch k {
			case "root", "target-dir", "profile", "json", "help", "version", "apply":
			default:
				return nil, profile, domain.Fail("ARGUMENT", "恢复/状态不支持参数: --"+k)
			}
		}
		if e := project.CheckLegacyState(root, profile); e != nil {
			return nil, profile, e
		}
		recovering := command == "recover" || len(o.args) > 1 && o.args[1] == "recover"
		// The explicit migrate recovery subcommand retains its published write
		// semantics; generic recover previews unless --apply is supplied.
		applyRecovery := command == "migrate" || o.values["apply"] == "true"
		if recovering {
			preparation, handled, e := project.RecoverPreparation(ctx, root, profile, applyRecovery)
			if e != nil {
				return nil, profile, e
			}
			if handled {
				return preparation, profile, nil
			}
		}
		id, e := project.RecoveryIdentity(root, profile)
		if e != nil {
			return nil, profile, e
		}
		profile = id.Profile.Name
		if e = project.CheckLegacyState(root, profile); e != nil {
			return nil, profile, e
		}
		var r transaction.Result
		switch {
		case command == "recover" || o.args[1] == "recover":
			if !applyRecovery {
				r, err = transaction.Status(root)
				break
			}
			r, err = transaction.RecoverContextWithValidator(ctx, root, func(summary transaction.Summary, _ []string) error {
				if summary.Kind != "init" && summary.Kind != "attach" && summary.Kind != "sync" && summary.Kind != "migrate" && summary.Kind != "skills" && summary.Kind != "assets" {
					return domain.Fail("KIND", "recover仅支持项目事务")
				}
				current, e := project.RecoveryIdentity(root, profile)
				if e != nil {
					return e
				}
				if current.Profile.Name != profile {
					return domain.Fail("IDENTITY", "恢复时Profile已变化")
				}
				return project.CheckLegacyState(root, profile)
			})
		case o.args[1] == "rollback":
			r, err = transaction.RollbackKind(root, "migrate")
		default:
			r, err = transaction.Status(root)
		}
		return r, profile, err
	}
	if command == "init" || command == "attach" || command == "sync" || command == "diff" || command == "doctor" || command == "migrate" || command == "skills" || command == "assets" {
		if o.values["apply"] == "true" && o.values["plan-file"] != "" {
			if o.values["binding-file"] != "" || o.values["full"] != "" {
				return nil, profile, domain.Fail("ARGUMENT", "apply consumes binding and resource selection from the saved plan")
			}
			p, e := project.ReadPlan(o.values["plan-file"])
			if e != nil {
				return nil, profile, e
			}
			if p.Root != root || (profile != "" && profile != p.Profile) || p.Command != command {
				return nil, profile, domain.Fail("PLAN", "计划与命令、项目根或 Profile 不匹配")
			}
			r, e := project.ApplyContext(ctx, p)
			return r, p.Profile, e
		}
		var selection []string
		if o.values["full"] == "true" {
			if command != "init" && command != "attach" {
				return nil, profile, domain.Fail("ARGUMENT", "--full only supports init or attach")
			}
			b, e := bundle.Load(profile)
			if e != nil {
				return nil, profile, e
			}
			for ref := range b.Files {
				if profile == "spec" && (strings.HasPrefix(ref, ".cursor/skills/") || strings.HasPrefix(ref, ".pi/skills/")) {
					continue
				}
				selection = append(selection, ref)
			}
			sort.Strings(selection)
		}
		id, e := project.Detect(root, profile, command == "init" || command == "attach")
		if e != nil {
			return nil, profile, e
		}
		profile = id.Profile.Name
		if command == "skills" || command == "assets" {
			if len(o.args) < 2 {
				return nil, profile, domain.Fail("ARGUMENT", "需要 list 或 ensure 子命令")
			}
			b, e := bundle.Load(profile)
			if e != nil {
				return nil, profile, e
			}
			if o.args[1] == "list" {
				if command == "assets" {
					return b.StageRequirements, profile, nil
				}
				names := map[string]bool{}
				for ref := range b.Files {
					if strings.HasPrefix(ref, ".agents/skills/") {
						name := strings.Split(strings.TrimPrefix(ref, ".agents/skills/"), "/")[0]
						names[name] = true
					}
				}
				list := []string{}
				for name := range names {
					list = append(list, name)
				}
				sort.Strings(list)
				return list, profile, nil
			}
			if o.args[1] != "ensure" || len(o.args) < 3 {
				return nil, profile, domain.Fail("ARGUMENT", "需要 ensure <标识>")
			}
			if command == "assets" {
				for _, stage := range o.args[2:] {
					r, ok := b.StageRequirements[stage]
					if !ok {
						return nil, profile, domain.Fail("ASSET", "未知阶段或 Profile 不支持按阶段补装: "+stage)
					}
					selection = append(selection, r.Paths...)
					for _, s := range r.Skills {
						for ref := range b.Files {
							if strings.HasPrefix(ref, ".agents/skills/"+s+"/") || strings.HasPrefix(ref, ".codex/skills/"+s+"/") {
								selection = append(selection, ref)
							}
						}
					}
				}
			} else {
				for _, name := range o.args[2:] {
					if r, ok := b.SkillRequirements[name]; ok {
						if r.UnsupportedReason != "" {
							return nil, profile, domain.Fail("UNPORTED", r.UnsupportedReason)
						}
						selection = append(selection, r.Paths...)
						for _, dep := range r.Skills {
							for ref := range b.Files {
								if strings.HasPrefix(ref, ".agents/skills/"+dep+"/") || strings.HasPrefix(ref, ".codex/skills/"+dep+"/") {
									selection = append(selection, ref)
								}
							}
						}
					}
					prefix := ".agents/skills/" + name + "/"
					count := 0
					for ref := range b.Files {
						if strings.HasPrefix(ref, prefix) || strings.HasPrefix(ref, ".codex/skills/"+name+"/") {
							selection = append(selection, ref)
							count++
						}
					}
					if count == 0 {
						return nil, profile, domain.Fail("SKILL", "快照未登记 Skill: "+name)
					}
				}
			}
		}
		vars := map[string]string{"projectName": o.values["project-name"], "businessDomain": o.values["business-domain"], "teamSize": o.values["team-size"]}
		if tracker, ok := o.values["issue-tracker"]; ok {
			if command != "init" && command != "attach" {
				return nil, profile, domain.Fail("ARGUMENT", "tracker更改需要单独迁移，不支持此命令参数")
			}
			switch tracker {
			case "local-markdown", "github", "gitlab":
				vars["issueTracker"] = tracker
			default:
				return nil, profile, domain.Fail("ARGUMENT", "未知issue-tracker")
			}
		}
		var binding *project.Binding
		if file := o.values["binding-file"]; file != "" {
			binding, e = project.ReadBinding(file)
			if e != nil {
				return nil, profile, e
			}
		}
		p, e := project.BuildWithBinding(root, profile, command, vars, selection, binding)
		if e != nil {
			return nil, profile, e
		}
		if command == "doctor" {
			checks, e := governance.Run("context", "verify", root, nil)
			if e != nil {
				return nil, profile, e
			}
			identity := map[string]any{"root": id.Root, "profile": id.Profile, "repositoryMode": id.Mode, "legacyFile": id.LegacyFile}
			if id.Native != nil {
				identity["native"] = map[string]any{"schemaVersion": id.Native.SchemaVersion, "protocolVersion": id.Native.ProtocolVersion, "cliVersion": id.Native.CLIVersion, "templateVersion": id.Native.TemplateVersion, "legacyCliVersion": id.Native.LegacyCLIVersion, "templateCommit": id.Native.TemplateCommit, "snapshotHash": id.Native.SnapshotHash, "manifestHash": id.Native.ManifestHash, "templateSourceState": id.Native.TemplateSourceState, "baselineDigest": id.Native.BaselineDigest, "managedFileCount": len(id.Native.Managed)}
			}
			return map[string]any{"identity": identity, "context": checks, "plan": p.Public(), "executionCore": func() string {
				if id.Native == nil {
					return "legacy"
				}
				return "go-native"
			}(), "stableReady": false}, profile, nil
		}
		if path := o.values["out"]; path != "" {
			if e = project.SavePlan(p, path); e != nil {
				return nil, profile, e
			}
		}
		if command == "init" && o.values["plan"] != "true" {
			r, e := project.ApplyContext(ctx, p)
			return r, profile, e
		}
		if o.values["apply"] == "true" {
			return nil, profile, domain.Fail("PLAN_REQUIRED", "非初始化写入需要 --apply --plan-file <已保存的计划>")
		}
		return p.Public(), profile, nil
	}
	action := "status"
	if len(o.args) > 1 {
		action = o.args[1]
	}
	if len(o.args) > 2 {
		for i, a := range o.args[2:] {
			o.values[fmt.Sprintf("arg%d", i)] = a
		}
	}
	if isSemanticCommand(o) {
		r, err := governance.RunContext(ctx, command, action, root, o.values)
		if report, ok := r.(*governance.SemanticReport); ok && report.Profile != "" {
			profile = report.Profile
		}
		if report, ok := r.(*governance.DailyReport); ok && report.Profile != "" {
			profile = report.Profile
		}
		return r, profile, err
	}
	if command == "context" {
		source, err := contextTemplateSource(root, profile)
		if err != nil {
			return nil, profile, err
		}
		if source {
			r, err := governance.RunContext(ctx, command, action, root, o.values)
			if result, ok := r.(map[string]any); ok {
				result["repository_mode"] = "template-source"
				result["approval_created"] = false
			}
			return r, profile, err
		}
	}
	id, err := project.Detect(root, profile, false)
	if err != nil {
		return nil, profile, err
	}
	profile = id.Profile.Name
	if command != "context" && !isSemanticCommand(o) {
		if _, err := governance.Run("context", "verify", root, nil); err != nil {
			return nil, profile, err
		}
	}
	r, err := governance.RunContext(ctx, command, action, root, o.values)
	return r, profile, err
}

// Context alone accepts source identity for read-only vocabulary checks.
// Instance and all other governance command boundaries remain unchanged.
func contextTemplateSource(root, profile string) (bool, error) {
	file, err := safefs.Path(root, "yss-project.yaml")
	if err != nil {
		return false, err
	}
	if _, err := os.Lstat(file); os.IsNotExist(err) {
		return false, nil // Let normal instance identity detection explain missing identity.
	} else if err != nil {
		return false, err
	}
	v, err := schema.LoadFile(file)
	if err != nil {
		return false, err
	}
	m, ok := v.(map[string]any)
	if !ok || m["repository_mode"] != "template-source" {
		return false, nil
	}
	n, ok := m["schema_version"].(json.Number)
	rational, valid := new(big.Rat).SetString(string(n))
	if !ok || !valid || rational.Cmp(big.NewRat(1, 1)) != 0 {
		return false, domain.Fail("IDENTITY", "模板源身份 schema 不支持")
	}
	if profile != "" {
		if _, err := domain.GetProfile(profile); err != nil {
			return false, err
		}
	}
	refs := []string{domain.MetadataFile}
	for _, p := range domain.Profiles {
		refs = append(refs, p.Metadata)
	}
	for _, ref := range refs {
		file, err := safefs.Path(root, ref)
		if err != nil {
			return false, err
		}
		if _, err = os.Lstat(file); err == nil {
			return false, domain.Fail("IDENTITY", "模板源不能携带实例 metadata: "+ref)
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	return true, nil
}
func capabilities() map[string]any {
	return map[string]any{"releaseQualification": "external-release-manifest", "version": domain.Version, "native": []string{"identity", "fixed-offline-bundles", "init", "attach-plan-and-apply", "diff", "sync-plan-and-apply", "migrate-plan-and-apply", "transaction-recover", "latest-migration-rollback", "contextual-help", "offline-tutorial", "online-program-upgrade", "offline-program-update", "program-update-recover-and-rollback", "schema", "strict-yaml", "context", "lifecycle-query", "stage-register-and-update", "scoped-project-ci", "runtime-basic-records", "runtime-record-queries-and-pins", "safe-zip-and-xml", "legacy-discovery-and-rejections", "JavaScript-native-transport"}, "governanceCandidate": map[string]any{"status": "implemented", "targetVersion": domain.Version, "readOnly": true, "approval_created": false, "defaultCIScope": "complete-governance", "runtimeStore": []string{"off"}, "interfaces": []string{"lifecycle.route", "lifecycle.verify-daily", "lifecycle.verify", "contract.verify:slice,scaffold,task", "evidence.verify:approval,user-decision,verification", "handoff.verify:package,consumption", "project-ci.check", "project-ci.verify"}, "exitCodes": map[string]int{"passed": 0, "rejected": 1, "inputCapabilityExecution": 2}}, "requiredReleaseEvidence": []string{"historical-fixed-executor-recovery", "plugin-consumer-cutover-verification", "native-declared-release-platform-validation", "fixed-source-release-gate"}, "legacyRuntimeRetained": false, "historicalRecovery": "external-fixed-packages"}
}

var _ = os.ErrNotExist
