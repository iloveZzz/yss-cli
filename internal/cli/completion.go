package cli

import (
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func addCompletionCommand(root *cobra.Command, inv *invocation) {
	parent := addCommand(root, inv, "completion", nil)
	parent.Short = "生成 Bash、Zsh、Fish 或 PowerShell 补全脚本。"
	parent.RunE = func(cmd *cobra.Command, args []string) error {
		return argumentError("completion 需要 bash|zsh|fish|powershell 子命令")
	}
	for name, generate := range map[string]func(io.Writer) error{
		"bash":       func(w io.Writer) error { return root.GenBashCompletionV2(w, true) },
		"zsh":        root.GenZshCompletion,
		"fish":       func(w io.Writer) error { return root.GenFishCompletion(w, true) },
		"powershell": root.GenPowerShellCompletionWithDesc,
	} {
		child := addCommand(parent, inv, name, nil)
		child.Short = "将 " + name + " 补全脚本写到 stdout；不安装、不修改 Shell 配置。"
		child.RunE = func(cmd *cobra.Command, args []string) error {
			inv.command = "completion"
			inv.rawOutput = true
			return generate(cmd.OutOrStdout())
		}
	}
}

func configureCompletion(root *cobra.Command, rawArgs []string) {
	terminated := len(rawArgs) > 1 && slices.Contains(rawArgs[1:len(rawArgs)-1], "--")
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		path := strings.TrimPrefix(cmd.CommandPath(), "yss ")
		cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, word string) ([]string, cobra.ShellCompDirective) {
			localFlag := false
			cmd.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) { localFlag = localFlag || f.Changed })
			// Cobra suppresses child suggestions after a local flag; YSS accepts that placement.
			if localFlag && len(args) == 0 && !terminated {
				children := []string{}
				for _, child := range cmd.Commands() {
					if child.IsAvailableCommand() && strings.HasPrefix(child.Name(), word) {
						children = append(children, cobra.CompletionWithDesc(child.Name(), child.Short))
					}
				}
				if len(children) > 0 {
					return children, cobra.ShellCompDirectiveNoFileComp
				}
			}
			switch path {
			case "check", "xml inspect", "xml query", "archive verify", "archive unpack", "reading apply-enable", "context check", "context verify", "contract check", "evidence check", "handoff check", "lifecycle status", "lifecycle verify", "stage status", "stage check":
				return nil, cobra.ShellCompDirectiveDefault
			case "archive pack":
				return nil, cobra.ShellCompDirectiveFilterDirs
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		register := func(f *pflag.Flag) {
			_ = cmd.RegisterFlagCompletionFunc(f.Name, func(_ *cobra.Command, args []string, word string) ([]string, cobra.ShellCompDirective) {
				if choices := f.Annotations["yss.choices"]; len(choices) > 0 {
					return choices, cobra.ShellCompDirectiveNoFileComp
				}
				if f.NoOptDefVal != "" {
					return []string{"true", "false"}, cobra.ShellCompDirectiveNoFileComp
				}
				if containsName(strings.Fields("root target-dir backend-root tool-root output-dir review-out home run-dir implementation-root template-checkout"), f.Name) || (f.Name == "out" && path == "bundle export") || (f.Name == "output" && path == "archive unpack") || (f.Name == "source" && path == "archive pack") {
					return nil, cobra.ShellCompDirectiveFilterDirs
				}
				placeholder := strings.Join(f.Annotations["yss.placeholder"], "")
				if strings.Contains(placeholder, "路径") || strings.Contains(placeholder, "文件") || strings.Contains(placeholder, "目录") || strings.Contains(placeholder, "归档") || f.Name == "input" && path == "lifecycle target" {
					return nil, cobra.ShellCompDirectiveDefault
				}
				return nil, cobra.ShellCompDirectiveNoFileComp
			})
		}
		cmd.LocalNonPersistentFlags().VisitAll(register)
		cmd.PersistentFlags().VisitAll(register)
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
}

// Visibility narrows suggestions only; validateFlagValues still enforces scope.
func updateCompletionFlags(cmd *cobra.Command, kind string) {
	path := strings.TrimPrefix(cmd.CommandPath(), "yss ")
	if !strings.HasSuffix(path, " verify") {
		return
	}
	group := strings.Fields(path)[0]
	if !containsName([]string{"contract", "evidence", "handoff", "lifecycle"}, group) {
		return
	}
	allowed := verificationOptions(group, kind)
	cmd.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) { f.Hidden = !containsName(allowed, f.Name) })
}
