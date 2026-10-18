package cli

import (
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type preparedInput struct {
	args         []string
	helpArgs     []string
	help         bool
	completion   bool
	fallback     options
	version      bool
	parsedPrefix []string
}

// Reuse the tree's values for legacy no-command and version-alias parsing.
func inputFlags(root *cobra.Command) *pflag.FlagSet {
	flags := pflag.NewFlagSet("yss", pflag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root.PersistentFlags().VisitAll(flags.AddFlag)
	for _, path := range commandPaths(root) {
		cmd, _ := lookupCommand(root, strings.Fields(path))
		cmd.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
			if flags.Lookup(f.Name) == nil {
				flags.AddFlag(f)
			}
		})
	}
	return flags
}

// Only relocates the command path and probes help/presentation; pflag parses values.
func prepareInput(root *cobra.Command, rawArgs []string) (preparedInput, error) {
	p := preparedInput{help: len(rawArgs) == 0, fallback: options{values: map[string]string{}}}
	if len(rawArgs) > 0 && (rawArgs[0] == "__complete" || rawArgs[0] == "__completeNoDesc") {
		p.args = append([]string{}, rawArgs...)
		p.completion = true
		return p, nil
	}
	known := inputFlags(root)
	cmd := root
	path, rest := []string{}, []string{}
	selecting := true
	var inputErr error
	for i := 0; i < len(rawArgs); i++ {
		a := rawArgs[i]
		if a == "--" {
			rest = append(rest, rawArgs[i:]...)
			p.helpArgs = append(p.helpArgs, rawArgs[i+1:]...)
			break
		}
		if a == "--help" || a == "--help=true" || a == "-h" {
			p.help = true
		}
		if a == "--json" || a == "--json=true" {
			p.fallback.json = true
		}
		for _, name := range []string{"human", "diagnostics"} {
			if a == "--"+name || a == "--"+name+"=true" {
				p.fallback.values[name] = "true"
			}
			if a == "--"+name+"=false" {
				p.fallback.values[name] = "false"
			}
		}
		if a == "-V" || a == "--version" || a == "--version=true" {
			p.version = true
		}
		if a == "--version=false" {
			p.version = false
		}
		if strings.HasPrefix(a, "-") {
			rest = append(rest, a)
			if a == "-h" || a == "-V" {
				continue
			}
			if !strings.HasPrefix(a, "--") {
				if inputErr == nil {
					inputErr = argumentError("未知短参数: " + a)
					p.parsedPrefix = append([]string{}, rest[:len(rest)-1]...)
				}
				continue
			}
			name, _, equals := strings.Cut(strings.TrimPrefix(a, "--"), "=")
			if f := known.Lookup(name); f != nil && f.NoOptDefVal == "" && !equals && i+1 < len(rawArgs) {
				next := rawArgs[i+1]
				if strings.HasPrefix(next, "--") || next == "-h" || next == "-V" {
					if inputErr == nil {
						inputErr = argumentError("参数缺少值: --" + name)
						p.parsedPrefix = append([]string{}, rest[:len(rest)-1]...)
					}
				} else {
					i++
					rest = append(rest, next)
				}
			}
			continue
		}
		if len(path) == 0 && len(p.helpArgs) == 0 && a == "help" {
			p.help = true
			continue
		}
		p.helpArgs = append(p.helpArgs, a)
		if selecting {
			next, remaining := lookupCommand(cmd, []string{a})
			if len(remaining) == 0 {
				cmd = next
				path = append(path, a)
				continue
			}
			selecting = false
		}
		rest = append(rest, a)
	}
	p.args = append(path, rest...)
	return p, inputErr
}
