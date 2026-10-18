package cli

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type optionValue struct {
	cmd     *cobra.Command
	inv     *invocation
	name    string
	boolean bool
}

func (v *optionValue) String() string {
	value, ok := v.inv.o.values[v.name]
	if !ok && v.boolean {
		return "false"
	}
	return value
}
func (v *optionValue) Type() string {
	if v.boolean {
		return "bool"
	}
	return "string"
}
func (v *optionValue) Set(value string) error {
	if v.boolean && value != "true" && value != "false" {
		return argumentError("布尔参数只接受 true/false: --" + v.name)
	}
	if _, ok := v.inv.o.values[v.name]; ok {
		v.inv.o.duplicates = append(v.inv.o.duplicates, v.name)
	}
	v.inv.o.values[v.name] = value
	if v.name == "kind" {
		updateCompletionFlags(v.cmd, value)
	}
	return nil
}

func addStringFlag(cmd *cobra.Command, inv *invocation, name, placeholder, description string, choices ...string) {
	cmd.Flags().Var(&optionValue{cmd: cmd, inv: inv, name: name}, name, description)
	f := cmd.Flags().Lookup(name)
	f.Annotations = map[string][]string{"yss.placeholder": {placeholder}, "yss.choices": choices}
}
func addBoolFlag(cmd *cobra.Command, inv *invocation, name, description string) {
	cmd.Flags().Var(&optionValue{cmd: cmd, inv: inv, name: name, boolean: true}, name, description)
	cmd.Flags().Lookup(name).NoOptDefVal = "true"
}

func addRegisteredFlag(cmd *cobra.Command, inv *invocation, name string, persistent bool) {
	if cmd.Flags().Lookup(name) != nil {
		return
	}
	spec := argumentSpecs[name]
	if choices, ok := commandChoices(strings.TrimPrefix(cmd.CommandPath(), "yss "))[name]; ok {
		spec.choices = choices
	}
	if persistent {
		short := ""
		if name == "help" {
			short = "h"
		}
		if name == "version" {
			short = "V"
		}
		f := cmd.PersistentFlags().VarPF(&optionValue{cmd: cmd, inv: inv, name: name, boolean: spec.boolean}, name, short, spec.description)
		if spec.boolean {
			f.NoOptDefVal = "true"
		}
		f.Annotations = map[string][]string{"yss.placeholder": {spec.placeholder}, "yss.choices": spec.choices}
		return
	}
	if spec.boolean {
		addBoolFlag(cmd, inv, name, spec.description)
	} else {
		addStringFlag(cmd, inv, name, spec.placeholder, spec.description, spec.choices...)
	}
}

func addFlags(cmd *cobra.Command, inv *invocation, names string) {
	for _, name := range strings.Fields(names) {
		if name == "arg0" || containsName(commonOptionNames, name) {
			continue
		}
		addRegisteredFlag(cmd, inv, name, false)
	}
}

func containsName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func flagNames(cmd *cobra.Command) []string {
	names := []string{}
	cmd.Flags().VisitAll(func(f *pflag.Flag) { names = append(names, f.Name) })
	cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		if !containsName(names, f.Name) {
			names = append(names, f.Name)
		}
	})
	sort.Strings(names)
	return names
}

func commandFlag(cmd *cobra.Command, name string) *pflag.Flag {
	if f := cmd.Flags().Lookup(name); f != nil {
		return f
	}
	return cmd.InheritedFlags().Lookup(name)
}

func flagError(cmd *cobra.Command, err error) error {
	var unknown *pflag.NotExistError
	if errors.As(err, &unknown) {
		return unknownOption(unknown.GetSpecifiedName(), flagNames(cmd))
	}
	var missing *pflag.ValueRequiredError
	if errors.As(err, &missing) {
		return argumentError("参数缺少值: --" + missing.GetSpecifiedName())
	}
	return argumentError("参数无效: " + err.Error())
}

func validateFlagValues(cmd *cobra.Command, o options) error {
	if o.values["human"] == "true" && o.json {
		return argumentError("--human 与 --json 互斥")
	}
	if o.values["diagnostics"] == "true" && !o.json {
		return argumentError("--diagnostics 必须与 --json 同用")
	}
	if o.values["version"] == "true" {
		return nil
	}
	path := strings.TrimPrefix(cmd.CommandPath(), "yss ")
	allowed := []string(nil)
	if strings.HasSuffix(path, " verify") && len(o.args) > 0 && containsName([]string{"lifecycle", "contract", "evidence", "handoff"}, o.args[0]) {
		allowed = append(append([]string{}, commonOptionNames...), verificationOptions(o.args[0], o.values["kind"])...)
	}
	if path == "skills list" && o.values["details"] == "true" {
		allowed = append(append([]string{}, commonOptionNames...), "root", "target-dir", "profile", "details")
	}
	keys := []string{}
	for k := range o.values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, name := range keys {
		if allowed != nil && !containsName(allowed, name) {
			return unknownOption(name, allowed)
		}
		f := commandFlag(cmd, name)
		if f == nil {
			return unknownOption(name, flagNames(cmd))
		}
		value := o.values[name]
		if f.NoOptDefVal == "" && strings.TrimSpace(value) == "" {
			return argumentError("参数不能为空: --" + name)
		}
		if choices := f.Annotations["yss.choices"]; len(choices) > 0 && !containsName(choices, value) {
			return argumentError(fmt.Sprintf("参数 --%s 的值无效: %s；可选值: %s", name, value, strings.Join(choices, "|")))
		}
		if name == "exit-code" {
			if _, err := strconv.Atoi(value); err != nil {
				return argumentError("--exit-code <整数> 必须为实际整数退出码")
			}
		}
	}
	return nil
}

func renderCommandOptions(cmd *cobra.Command) string {
	labels, descriptions := []string{}, []string{}
	columnWidth := 30
	for _, name := range flagNames(cmd) {
		f := commandFlag(cmd, name)
		label := "--" + name
		if f.Shorthand != "" {
			label = "-" + f.Shorthand + ", " + label
		}
		if placeholder := f.Annotations["yss.placeholder"]; len(placeholder) > 0 && placeholder[0] != "" {
			label += " " + placeholder[0]
		}
		description := f.Usage
		if choices := f.Annotations["yss.choices"]; len(choices) > 0 {
			description += "；可选值: " + strings.Join(choices, "|")
		}
		labels = append(labels, label)
		descriptions = append(descriptions, description)
		columnWidth = max(columnWidth, terminalWidth(label))
	}
	rows := []string{}
	for i, label := range labels {
		rows = append(rows, "  "+label+strings.Repeat(" ", columnWidth-terminalWidth(label)+2)+descriptions[i])
	}
	return strings.Join(rows, "\n")
}
