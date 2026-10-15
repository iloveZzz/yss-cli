package gitops

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	yaml "go.yaml.in/yaml/v3"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

// HooksDir is tracked, so a team shares one set of scripts; each clone still
// opts in by running hooks install (core.hooksPath is local configuration).
const HooksDir = ".githooks"

const hookMarker = "# yss-managed hook; reinstall with: yss git hooks install"

var chainLine = regexp.MustCompile(`(?m)^# yss-chain: (\S+)$`)

func hookScript(chain string) string {
	dir := `"$(git rev-parse --git-common-dir)/hooks"`
	if chain != "-" {
		dir = "'" + chain + "'"
	}
	return "#!/bin/sh\n" + hookMarker + "\n# yss-chain: " + chainOrDash(chain) + "\n" +
		"chain_dir=" + dir + "\n" +
		`if [ -x "$chain_dir/commit-msg" ] && ! [ "$chain_dir/commit-msg" -ef "$0" ]; then
  "$chain_dir/commit-msg" "$@" || exit $?
fi
if command -v yss >/dev/null 2>&1; then
  exec yss git verify-message --file "$1" >/dev/null
fi
echo "yss: 未找到 yss，已跳过提交信息检查" >&2
exit 0
`
}

func chainOrDash(chain string) string {
	if chain == "" {
		return "-"
	}
	return chain
}

func (r *Runner) buildHooks(ctx context.Context, params map[string]string) (Plan, error) {
	if err := known(params); err != nil {
		return Plan{}, err
	}
	if _, err := LoadPolicy(r.Root); err != nil {
		return Plan{}, err
	}
	current, err := r.ConfigValue(ctx, "core.hooksPath")
	if err != nil {
		return Plan{}, err
	}
	const ref = HooksDir + "/commit-msg"
	before, err := safefs.Describe(r.Root, ref)
	if err != nil {
		return Plan{}, err
	}
	chain := "-"
	if current != "" && strings.TrimSuffix(current, "/") != HooksDir {
		if !hooksPattern.MatchString(current) || strings.Contains(current, "..") || strings.Contains(current, "'") {
			return Plan{}, domain.Fail("INPUT", "现有 core.hooksPath 含不支持的字符，无法链式调用: "+current)
		}
		chain = current
	}
	if before.Type == "file" {
		existing, rerr := os.ReadFile(filepath.Join(r.Root, ref))
		if rerr != nil {
			return Plan{}, rerr
		}
		if !bytes.Contains(existing, []byte(hookMarker)) {
			return Plan{}, domain.Fail("CONFLICT", ref+" 已存在且不是 yss 生成的钩子；不覆盖")
		}
		if m := chainLine.FindSubmatch(existing); m != nil && strings.TrimSuffix(current, "/") == HooksDir {
			chain = string(m[1])
		}
	} else if before.Type != "missing" {
		return Plan{}, domain.Fail("PATH", ref+" 不是普通文件")
	}
	if chain != "-" && !hooksPattern.MatchString(chain) {
		return Plan{}, domain.Fail("INPUT", "钩子链路径无效: "+chain)
	}
	script := hookScript(chain)
	obs := map[string]string{"hooksPath": current, "chain": chain, "file:" + ref: before.Type + ":" + before.Digest}
	effects := []string{"写入 " + ref + "（commit-msg：校验 Ticket trailer）", "git config --local core.hooksPath " + HooksDir}
	if chain != "-" {
		effects = append(effects, "先链式调用原钩子目录 "+chain)
	} else {
		effects = append(effects, "先链式调用原 .git/hooks/commit-msg（若存在）")
	}
	p := Plan{Observed: obs, Effects: effects}
	if before.Type == "file" {
		existing, _ := os.ReadFile(filepath.Join(r.Root, ref))
		if string(existing) == script {
			p.Effects = append(p.Effects, "脚本已是最新，仅确认配置")
			return p, nil
		}
	}
	if st, e := os.Lstat(filepath.Join(r.Root, HooksDir)); e != nil {
		if !os.IsNotExist(e) {
			return Plan{}, e
		}
		p.Operations = append(p.Operations, transaction.Operation{Path: HooksDir, Directory: true})
	} else if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return Plan{}, domain.Fail("PATH", HooksDir+" 不是普通目录")
	}
	op := transaction.Operation{Path: ref, Data: []byte(script), Mode: 0755}
	if before.Type == "file" {
		op.Before = &before
	}
	p.Operations = append(p.Operations, op)
	return p, nil
}

func (r *Runner) applyHooks(ctx context.Context, p Plan) (any, error) {
	written := false
	if len(p.Operations) > 0 {
		if _, err := transaction.ApplyContextWithValidation(ctx, r.Root, p.Kind, p.Operations, nil, nil, nil); err != nil {
			return nil, err
		}
		written = true
	}
	if _, err := r.Mutate(ctx, "", "config", "--local", "core.hooksPath", HooksDir); err != nil {
		return nil, domain.WithMessage(err, err.Error()+"（钩子文件已写入；修复后重新规划即可）")
	}
	return map[string]any{"status": "applied", "hooksPath": HooksDir, "scriptWritten": written, "chain": p.Observed["chain"], "note": "core.hooksPath 是本地配置；其他克隆需各自运行 yss git hooks install"}, nil
}

// ---- policy set ----

type policyFile struct {
	SchemaVersion     int      `yaml:"schema_version"`
	DefaultBranch     string   `yaml:"default_branch"`
	ProtectedBranches []string `yaml:"protected_branches"`
	BranchPattern     string   `yaml:"branch_pattern"`
	TicketTrailer     string   `yaml:"ticket_trailer"`
	TicketPattern     string   `yaml:"ticket_pattern"`
	ReviewTrailer     string   `yaml:"review_trailer"`
	RequireSigned     bool     `yaml:"require_signed"`
}

func (r *Runner) buildPolicySet(ctx context.Context, params map[string]string) (Plan, error) {
	if err := known(params, "key", "value"); err != nil {
		return Plan{}, err
	}
	pol, err := LoadPolicy(r.Root)
	if err != nil {
		return Plan{}, err
	}
	key, raw := params["key"], params["value"]
	switch key {
	case "default_branch", "branch_pattern", "ticket_trailer", "ticket_pattern", "review_trailer":
		value, perr := stringValue(raw)
		if perr != nil {
			return Plan{}, perr
		}
		switch key {
		case "default_branch":
			pol.DefaultBranch = value
		case "branch_pattern":
			pol.BranchPattern = value
		case "ticket_trailer":
			pol.TicketTrailer = value
		case "ticket_pattern":
			pol.TicketPattern = value
		case "review_trailer":
			pol.ReviewTrailer = value
		}
	case "protected_branches":
		var list []string
		if json.Unmarshal([]byte(raw), &list) != nil {
			return Plan{}, domain.Fail("INPUT", "protected_branches 的 --value 须为 JSON 字符串数组")
		}
		pol.ProtectedBranches = list
	case "require_signed":
		if raw != "true" && raw != "false" {
			return Plan{}, domain.Fail("INPUT", "require_signed 的 --value 须为 true 或 false")
		}
		pol.RequireSigned = raw == "true"
	default:
		return Plan{}, domain.Fail("INPUT", "不支持的策略字段: "+key)
	}
	if pol.ProtectedBranches == nil {
		pol.ProtectedBranches = []string{}
	}
	if err = pol.compile(); err != nil {
		return Plan{}, err
	}
	data, err := yaml.Marshal(policyFile{1, pol.DefaultBranch, pol.ProtectedBranches, pol.BranchPattern, pol.TicketTrailer, pol.TicketPattern, pol.ReviewTrailer, pol.RequireSigned})
	if err != nil {
		return Plan{}, err
	}
	before, err := safefs.Describe(r.Root, PolicyRef)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Observed: map[string]string{"file:" + PolicyRef: before.Type + ":" + before.Digest}, Effects: []string{"写入 " + PolicyRef + "：" + key + " = " + raw}}
	if before.Type == "file" {
		p.Effects = append(p.Effects, "整文件按固定格式重写；原有注释不会保留")
	}
	for dir := path0(PolicyRef); dir != "."; dir = path0(dir) {
		if _, e := os.Lstat(filepath.Join(r.Root, filepath.FromSlash(dir))); e != nil {
			if !os.IsNotExist(e) {
				return Plan{}, e
			}
			p.Operations = append([]transaction.Operation{{Path: dir, Directory: true}}, p.Operations...)
		}
	}
	op := transaction.Operation{Path: PolicyRef, Data: data, Mode: 0644}
	if before.Type == "file" {
		op.Before = &before
	}
	p.Operations = append(p.Operations, op)
	return p, nil
}

func path0(p string) string { return filepath.ToSlash(filepath.Dir(filepath.FromSlash(p))) }

func stringValue(raw string) (string, error) {
	var s string
	if json.Valid([]byte(raw)) {
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			return "", domain.Fail("INPUT", "该字段的 --value 须为字符串")
		}
		return s, nil
	}
	return raw, nil
}

func (r *Runner) applyPolicySet(ctx context.Context, p Plan) (any, error) {
	if _, err := transaction.ApplyContextWithValidation(ctx, r.Root, p.Kind, p.Operations, nil, nil, nil); err != nil {
		return nil, err
	}
	pol, err := LoadPolicy(r.Root)
	if err != nil {
		return nil, domain.WithMessage(err, "策略已写入但重新读取失败: "+err.Error())
	}
	return map[string]any{"status": "applied", "policy": pol}, nil
}

// ---- commit message check (called by the commit-msg hook) ----

// VerifyMessage checks the ticket trailer of a commit message file.
func VerifyMessage(p Policy, file string) (map[string]any, error) {
	st, err := os.Lstat(file)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 1<<20 {
		return nil, domain.Fail("INPUT", "提交信息文件不可读或过大: "+file)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	lines := []string{}
	for _, l := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	subject, _, _ := strings.Cut(text, "\n")
	for _, skip := range []string{"Merge ", "Revert ", "fixup! ", "squash! ", "amend! "} {
		if strings.HasPrefix(subject, skip) {
			return map[string]any{"status": "skipped", "reason": skip}, nil
		}
	}
	if text == "" {
		return nil, domain.Fail("INPUT", "提交信息为空")
	}
	paragraphs := strings.Split(text, "\n\n")
	last := paragraphs[len(paragraphs)-1]
	if len(paragraphs) > 1 {
		for _, l := range strings.Split(last, "\n") {
			key, value, ok := strings.Cut(l, ": ")
			if ok && strings.EqualFold(key, p.TicketTrailer) && p.TicketValid(strings.TrimSpace(value)) {
				return map[string]any{"status": "ok", "ticket": strings.TrimSpace(value)}, nil
			}
		}
	}
	return nil, domain.Fail("INPUT", "提交信息末段缺少符合 "+p.TicketPattern+" 的 "+p.TicketTrailer+" trailer（例如 \""+p.TicketTrailer+": FEAT-1\"）")
}
