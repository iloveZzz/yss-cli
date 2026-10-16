package cli

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/gitops"
	"github.com/iloveZzz/yss-cli/internal/project"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

const gitMinimumVersion = "2.31"

var gitVersionPattern = regexp.MustCompile(`git version (\d+)\.(\d+)`)

// gitActions lists the subcommands that take an action word, and the plan
// kind each one produces. Everything else is a single-word read-only query.
var gitActions = map[string]struct{ action, kind string }{
	"branch":  {"start", gitops.KindBranchStart},
	"hooks":   {"install", gitops.KindHooks},
	"release": {"tag", gitops.KindTag},
}

var gitSingleWrites = map[string]string{"commit": gitops.KindCommit, "push": gitops.KindPush}

// gitCommand serves yss git. Queries are read-only; writes (branch start,
// commit, hooks install, policy set, release tag) only run from a saved plan
// and never contact a remote.
func gitCommand(ctx context.Context, root string, o options) (any, string, error) {
	if len(o.args) < 2 || len(o.args) > 3 {
		return nil, "", domain.Fail("ARGUMENT", "git 需要一个子命令；查看 yss git --help")
	}
	writeKind := ""
	if spec, ok := gitActions[o.args[1]]; ok {
		if len(o.args) != 3 || o.args[2] != spec.action {
			return nil, "", domain.Fail("ARGUMENT", "git "+o.args[1]+" 需要 "+spec.action+" 子命令")
		}
		writeKind = spec.kind
	} else if kind, ok := gitSingleWrites[o.args[1]]; ok && len(o.args) == 2 {
		writeKind = kind
	} else if o.args[1] == "policy" && len(o.args) == 3 {
		if o.args[2] != "set" {
			return nil, "", domain.Fail("ARGUMENT", "git policy 只支持 set 子命令")
		}
		writeKind = gitops.KindPolicySet
	} else if len(o.args) != 2 {
		return nil, "", domain.Fail("ARGUMENT", "git "+o.args[1]+" 不接受额外位置参数")
	}
	profile, err := gitIdentity(root, o.values["profile"])
	if err != nil {
		return nil, "", err
	}
	r, err := gitops.NewRunner(root)
	if err != nil {
		return nil, profile, err
	}
	if err = r.TopLevel(ctx); err != nil {
		return nil, profile, err
	}
	if writeKind != "" {
		result, werr := gitWrite(ctx, r, writeKind, o)
		return result, profile, werr
	}
	var result any
	switch o.args[1] {
	case "verify-message":
		var p gitops.Policy
		if p, err = gitops.LoadPolicy(root); err == nil {
			result, err = gitops.VerifyMessage(p, o.values["file"])
		}
	case "status":
		result, err = gitStatus(ctx, r, o.values["task"])
	case "base":
		result, err = gitBase(ctx, r, o.values["branch"])
	case "submodules":
		var subs []gitops.Submodule
		if subs, err = r.Submodules(ctx); err == nil {
			result = map[string]any{"submodules": subs, "count": len(subs)}
		}
	case "doctor":
		result, err = gitDoctor(ctx, r)
	case "guard":
		result, err = gitGuard(ctx, r)
	case "policy":
		var p gitops.Policy
		if p, err = gitops.LoadPolicy(root); err == nil {
			result = p
		}
	case "audit":
		result, err = gitAudit(ctx, r, o.values["since"], o.values["until"])
	case "changelog":
		result, err = gitChangelog(ctx, r, o.values["since"], o.values["until"])
	default:
		err = domain.Fail("ARGUMENT", "未知 git 子命令: "+o.args[1])
	}
	return result, profile, err
}

// gitIdentity checks the project identity only where one is declared; the
// read-only queries also serve repositories that are not YSS instances.
func gitIdentity(root, explicit string) (string, error) {
	if _, err := os.Lstat(filepath.Join(root, "yss-project.yaml")); errors.Is(err, fs.ErrNotExist) {
		return explicit, nil
	}
	id, err := project.Detect(root, explicit, false)
	if err != nil {
		return "", err
	}
	return id.Profile.Name, nil
}

func gitStatus(ctx context.Context, r *gitops.Runner, task string) (any, error) {
	s, err := r.ReadStatus(ctx)
	if err != nil {
		return nil, err
	}
	ops, err := r.Operations(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{"staged": 0, "unstaged": 0, "untracked": 0, "unmerged": 0}
	for _, e := range s.Entries {
		switch {
		case e.Kind == "untracked":
			counts["untracked"]++
		case e.Kind == "unmerged":
			counts["unmerged"]++
		default:
			if e.Index != "" {
				counts["staged"]++
			}
			if e.Worktree != "" {
				counts["unstaged"]++
			}
		}
	}
	out := map[string]any{"branch": s.Branch, "entries": s.Entries, "counts": counts, "clean": len(s.Entries) == 0, "operations": ops}
	if task != "" {
		out["task"] = map[string]any{"id": task, "branchBound": !s.Branch.Detached && strings.Contains(s.Branch.Head, task)}
	}
	return out, nil
}

// gitBase resolves the merge base that lifecycle route --base consumes. A
// local branch wins over origin/<branch>, and the ref used is reported.
func gitBase(ctx context.Context, r *gitops.Runner, branch string) (any, error) {
	if branch == "" {
		p, err := gitops.LoadPolicy(r.Root)
		if err != nil {
			return nil, err
		}
		branch = p.DefaultBranch
	}
	head, err := r.Commit(ctx, "HEAD")
	if err != nil {
		return nil, err
	}
	ref := ""
	for _, candidate := range []string{"refs/heads/" + branch, "refs/remotes/origin/" + branch} {
		if _, e := r.Commit(ctx, candidate); e == nil {
			ref = candidate
			break
		}
	}
	if ref == "" {
		return nil, domain.Fail("INPUT", "找不到基线分支 "+branch+"（本地或 origin）；用 --branch 指定")
	}
	base, err := r.Line(ctx, "merge-base", head, ref)
	if err != nil {
		return nil, domain.Fail("INPUT", "HEAD 与 "+ref+" 没有共同祖先")
	}
	return map[string]any{"base": base, "ref": ref, "head": head, "next": "yss lifecycle route --base " + base}, nil
}

type gitCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func gitDoctor(ctx context.Context, r *gitops.Runner) (any, error) {
	checks := []gitCheck{}
	add := func(name, status, detail string) { checks = append(checks, gitCheck{name, status, detail}) }
	version, err := r.Line(ctx, "version")
	if err != nil {
		return nil, err
	}
	if m := gitVersionPattern.FindStringSubmatch(version); m == nil {
		add("git-version", "warn", version+"；无法识别版本")
	} else if major, _ := strconv.Atoi(m[1]); major < 2 || major == 2 && atoi(m[2]) < 31 {
		add("git-version", "fail", version+"；需要 ≥ "+gitMinimumVersion)
	} else {
		add("git-version", "ok", version)
	}
	name, err := r.ConfigValue(ctx, "user.name")
	if err != nil {
		return nil, err
	}
	email, err := r.ConfigValue(ctx, "user.email")
	if err != nil {
		return nil, err
	}
	if name == "" || email == "" {
		add("commit-identity", "warn", "user.name 或 user.email 未配置；后续提交命令需要")
	} else {
		add("commit-identity", "ok", name+" <"+email+">")
	}
	remotes, err := r.Query(ctx, "remote")
	if err != nil {
		return nil, err
	}
	if names := strings.Fields(string(remotes)); len(names) == 0 {
		add("remote", "warn", "未配置远端")
	} else {
		add("remote", "ok", strings.Join(names, ", "))
	}
	shallow, err := r.Line(ctx, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return nil, err
	}
	if shallow == "true" {
		add("history", "warn", "浅克隆；base/audit 可能缺少历史")
	} else {
		add("history", "ok", "完整历史")
	}
	for _, key := range []string{"core.autocrlf", "core.hooksPath", "filter.lfs.process"} {
		value, err := r.ConfigValue(ctx, key)
		if err != nil {
			return nil, err
		}
		if value == "" {
			value = "(未设置)"
		}
		add(key, "info", value)
	}
	if _, err := gitops.LoadPolicy(r.Root); err != nil {
		add("policy", "fail", err.Error())
	} else {
		add("policy", "ok", "可读取")
	}
	status := "ok"
	for _, c := range checks {
		if c.Status == "fail" {
			status = "fail"
		} else if c.Status == "warn" && status == "ok" {
			status = "warn"
		}
	}
	return map[string]any{"status": status, "checks": checks}, nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

type gitGuardError struct {
	message string
	report  map[string]any
}

func (e *gitGuardError) Error() string    { return e.message }
func (e *gitGuardError) ErrorResult() any { return e.report }
func (e *gitGuardError) Unwrap() error    { return domain.Fail("CONFLICT", e.message) }

// gitGuard blocks before init/attach/sync style transactions: uncommitted
// changes, an operation in progress or a pending yss transaction.
func gitGuard(ctx context.Context, r *gitops.Runner) (any, error) {
	s, err := r.ReadStatus(ctx)
	if err != nil {
		return nil, err
	}
	ops, err := r.Operations(ctx)
	if err != nil {
		return nil, err
	}
	reasons := []string{}
	if len(s.Entries) > 0 {
		reasons = append(reasons, strconv.Itoa(len(s.Entries))+" 个未提交路径")
	}
	if len(ops) > 0 {
		reasons = append(reasons, "进行中的 "+strings.Join(ops, "、"))
	}
	pending := []string{}
	if _, err := os.Lstat(filepath.Join(r.Root, ".yss", "transactions")); err == nil {
		tx, err := transaction.Status(r.Root)
		if err != nil {
			return nil, err
		}
		pending = tx.Pending
		if len(pending) > 0 {
			reasons = append(reasons, strconv.Itoa(len(pending))+" 个未完成 yss 事务")
		}
	}
	report := map[string]any{"status": "clear", "entries": s.Entries, "operations": ops, "pendingTransactions": pending, "reasons": reasons}
	if len(reasons) > 0 {
		report["status"] = "blocked"
		return nil, &gitGuardError{"工作区未就绪: " + strings.Join(reasons, "；"), report}
	}
	return report, nil
}

func gitRange(ctx context.Context, r *gitops.Runner, since, until string) (string, string, error) {
	if since == "" {
		return "", "", domain.Fail("ARGUMENT", "需要 --since <起始引用>")
	}
	if until == "" {
		until = "HEAD"
	}
	from, err := r.Commit(ctx, since)
	if err != nil {
		return "", "", err
	}
	to, err := r.Commit(ctx, until)
	return from, to, err
}

func gitAudit(ctx context.Context, r *gitops.Runner, since, until string) (any, error) {
	p, err := gitops.LoadPolicy(r.Root)
	if err != nil {
		return nil, err
	}
	from, to, err := gitRange(ctx, r, since, until)
	if err != nil {
		return nil, err
	}
	head, err := r.ReadStatus(ctx)
	if err != nil {
		return nil, err
	}
	onProtected := (until == "" || until == "HEAD") && !head.Branch.Detached && p.Protected(head.Branch.Head)
	commits, err := r.Log(ctx, p, from, to, onProtected)
	if err != nil {
		return nil, err
	}
	findings := gitops.Audit(p, commits, onProtected)
	counts := map[string]int{}
	for _, f := range findings {
		counts[f.Rule]++
	}
	status := "ok"
	if len(findings) > 0 {
		status = "attention"
	}
	return map[string]any{"range": from + ".." + to, "commits": len(commits), "policy": p.Source, "firstParent": onProtected, "counts": counts, "findings": findings, "status": status}, nil
}

func gitChangelog(ctx context.Context, r *gitops.Runner, since, until string) (any, error) {
	p, err := gitops.LoadPolicy(r.Root)
	if err != nil {
		return nil, err
	}
	from, to, err := gitRange(ctx, r, since, until)
	if err != nil {
		return nil, err
	}
	commits, err := r.Log(ctx, p, from, to, false)
	if err != nil {
		return nil, err
	}
	return map[string]any{"range": from + ".." + to, "commits": len(commits), "groups": gitops.Changelog(commits)}, nil
}
