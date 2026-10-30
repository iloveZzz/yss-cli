package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/trace"
	"github.com/spf13/cobra"
)

type invocation struct {
	o           options
	command     string
	result      any
	profile     string
	executed    bool
	rawOutput   bool
	parseFailed bool
}

func newRootCommand(inv *invocation, stdout, stderr io.Writer) *cobra.Command {
	if inv == nil {
		inv = &invocation{}
	}
	if inv.o.values == nil {
		inv.o.values = map[string]string{}
	}
	root := &cobra.Command{Use: "yss", SilenceErrors: true, SilenceUsage: true}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return argumentError("未知命令: " + args[0] + suggestions(args[0], childNames(cmd)))
		}
		return nil
	}
	root.RunE = func(cmd *cobra.Command, args []string) error { return inv.run(cmd, args, coreCommand) }
	for _, name := range commonOptionNames {
		addRegisteredFlag(root, inv, name, true)
	}
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error { inv.parseFailed = true; return flagError(cmd, err) })
	addCoreCommands(root, inv)
	addProjectCommands(root, inv)
	addGovernanceCommands(root, inv)
	addGitCommands(root, inv)
	addProgramCommands(root, inv)
	addTrackerCommands(root, inv)
	addHelpCommands(root, inv)
	addCompletionCommand(root, inv)
	configureCompletion(root, inv.o.rawArgs)
	return root
}

func addCommand(parent *cobra.Command, inv *invocation, name string, handler func(context.Context, options) (any, string, error)) *cobra.Command {
	path := strings.TrimPrefix(parent.CommandPath()+" "+name, "yss ")
	topic := helpTopics[path]
	cmd := &cobra.Command{Use: name, Short: topic.summary, Example: topic.examples}
	cmd.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return argumentError("命令 " + strings.TrimPrefix(cmd.CommandPath(), "yss ") + " 不接受额外位置参数: " + args[0])
		}
		return nil
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error { return inv.run(cmd, args, handler) }
	parent.AddCommand(cmd)
	return cmd
}

func (inv *invocation) run(cmd *cobra.Command, args []string, handler func(context.Context, options) (any, string, error)) error {
	inv.o.args = append(strings.Fields(strings.TrimPrefix(cmd.CommandPath(), "yss")), args...)
	inv.command = ""
	if len(inv.o.args) > 0 {
		inv.command = inv.o.args[0]
	}
	inv.o.json = inv.o.values["json"] == "true"
	if inv.o.values["version"] == "true" {
		inv.command = "version"
	}
	inv.profile = inv.o.values["profile"]
	if err := validateFlagValues(cmd, inv.o); err != nil {
		return err
	}
	if isSemanticCommand(inv.o) && len(inv.o.duplicates) > 0 {
		return argumentError("重复参数: --" + inv.o.duplicates[0])
	}
	if inv.command == "mcp" {
		inv.rawOutput = true
		root := inv.o.values["root"]
		if root == "" {
			root = "."
		}
		return ServeMCP(cmd.Context(), root, os.Stdin, cmd.OutOrStdout())
	}
	o := inv.o
	o.values = make(map[string]string, len(inv.o.values))
	for k, v := range inv.o.values {
		o.values[k] = v
	}
	for _, key := range []string{"help", "version", "human", "diagnostics"} {
		delete(o.values, key)
	}
	inv.executed = true
	if err := cmd.Context().Err(); err != nil && inv.command != "upgrade" {
		return domain.Wrap("CANCELLED", err)
	}
	if len(o.args) > 0 {
		root, err := projectRoot(o)
		if err != nil {
			return err
		}
		if err := rejectLegacySpecCommand(root, inv.command); err != nil {
			return err
		}
	}
	var err error
	if inv.command == "version" {
		if len(o.args) == 0 {
			o.args = []string{"version"}
		} else {
			o.args = append([]string{"version"}, o.args[1:]...)
		}
		inv.result, inv.profile, err = coreCommand(cmd.Context(), o)
	} else {
		inv.result, inv.profile, err = handler(cmd.Context(), o)
	}
	return err
}

func projectRoot(o options) (string, error) {
	for _, name := range []string{"root", "target-dir"} {
		if value, ok := o.values[name]; ok && strings.TrimSpace(value) == "" {
			return "", argumentError("项目根参数不能为空: --" + name)
		}
	}
	root := o.values["root"]
	if root == "" {
		root = o.values["target-dir"]
	}
	if root == "" {
		root = "."
	}
	return filepath.Abs(root)
}

func addCoreCommands(root *cobra.Command, inv *invocation) {
	for _, name := range []string{"version", "capabilities", "explain", "check", "verify", "trace", "mcp"} {
		cmd := addCommand(root, inv, name, coreCommand)
		switch name {
		case "explain":
			addFlags(cmd, inv, "root")
			cmd.Args = maxPositionals(1)
		case "check":
			addFlags(cmd, inv, "root changed base checkpoint")
			cmd.Args = cobra.ArbitraryArgs
		case "verify":
			addFlags(cmd, inv, "root gate checkpoint")
		case "trace":
			addFlags(cmd, inv, "root feature implementation-root since until html strict")
		case "mcp":
			addFlags(cmd, inv, "root")
		}
	}
}

func maxPositionals(max int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > max {
			return argumentError("命令 " + cmd.CommandPath() + " 不支持这些额外位置参数")
		}
		return nil
	}
}

func addHelpCommands(root *cobra.Command, inv *invocation) {
	help := addCommand(root, inv, "help", nil)
	help.RunE = func(cmd *cobra.Command, args []string) error { return argumentError("使用 yss help [命令]") }
	help.Short = "显示离线中文帮助、教程、示例与错误说明。"
	for _, name := range []string{"tutorial", "examples", "errors"} {
		child := addCommand(help, inv, name, nil)
		child.RunE = help.RunE
		child.Short = "离线" + name + "帮助"
		child.Args = cobra.ArbitraryArgs
	}
	root.SetHelpCommand(help)
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		text, err := renderHelpFrom(root, strings.Fields(strings.TrimSpace(strings.TrimPrefix(cmd.CommandPath(), "yss"))))
		if err == nil {
			fmt.Fprintln(cmd.OutOrStdout(), text)
		}
	})
}

func childNames(cmd *cobra.Command) []string {
	names := []string{}
	for _, child := range cmd.Commands() {
		if !child.Hidden {
			names = append(names, child.Name())
		}
	}
	return names
}

func lookupCommand(root *cobra.Command, path []string) (*cobra.Command, []string) {
	cmd := root
	for i, name := range path {
		var next *cobra.Command
		for _, child := range cmd.Commands() {
			if child.Name() == name {
				next = child
				break
			}
		}
		if next == nil {
			return cmd, path[i:]
		}
		cmd = next
	}
	return cmd, nil
}

func commandPaths(root *cobra.Command) []string {
	paths := []string{}
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, child := range cmd.Commands() {
			if child.Hidden {
				continue
			}
			paths = append(paths, strings.TrimPrefix(child.CommandPath(), "yss "))
			walk(child)
		}
	}
	walk(root)
	return paths
}

func traceCommand(ctx context.Context, root string, o options) (any, error) {
	report, err := trace.Build(ctx, root, trace.Options{Feature: o.values["feature"], ImplementationRoot: o.values["implementation-root"], Since: o.values["since"], Until: o.values["until"]})
	if err != nil {
		return nil, err
	}
	if out := o.values["html"]; out != "" {
		if err = trace.WriteHTML(root, out, report); err != nil {
			return nil, err
		}
	}
	if o.values["strict"] == "true" && report.HasGaps() {
		return nil, &trace.GapsFound{Report: report}
	}
	return report, nil

}

func coreCommand(ctx context.Context, o options) (any, string, error) {

	profile := o.values["profile"]
	root, err := projectRoot(o)
	if err != nil {
		return nil, profile, err
	}
	if err = ctx.Err(); err != nil {
		return nil, profile, domain.Wrap("CANCELLED", err)
	}

	command := "version"
	if len(o.args) > 0 {
		command = o.args[0]
	}
	if (command == "version" || command == "capabilities") && len(o.args) > 1 {
		return nil, profile, argumentError("不支持额外子命令或位置参数")
	}
	switch command {
	case "version":
		return map[string]any{"version": domain.Version, "protocolVersion": domain.ProtocolVersion, "profiles": domain.Profiles, "source": domain.BuildProvenance(), "cliCommit": domain.BuildProvenance().Commit, "sourceState": domain.BuildProvenance().SourceState}, profile, nil
	case "capabilities":
		return capabilities(), profile, nil
	case "explain":
		result, e := explainCommand(root, o.args[1:])
		return result, profile, e
	case "check":
		result, e := checkCommand(ctx, root, o)
		return result, profile, e
	case "verify":
		result, e := gateVerifyCommand(ctx, root, o)
		return result, profile, e
	case "trace":
		result, e := traceCommand(ctx, root, o)
		return result, profile, e
	}
	return nil, profile, argumentError("未知核心命令: " + command)
}
