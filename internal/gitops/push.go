package gitops

import (
	"context"
	"net/url"
	"regexp"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

var urlScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

// remoteURL returns the configured push URL after refusing the forms that
// either carry credentials into the plan file or select a command transport.
func (r *Runner) remoteURL(ctx context.Context, remote string) (string, error) {
	for _, key := range []string{"remote." + remote + ".pushurl", "remote." + remote + ".url"} {
		value, err := r.ConfigValue(ctx, key)
		if err != nil {
			return "", err
		}
		if value == "" {
			continue
		}
		switch {
		case strings.HasPrefix(value, "-") || strings.Contains(value, "::") && !urlScheme.MatchString(value) && !strings.Contains(value, "@"):
			return "", domain.Fail("INPUT", "远端 URL 使用了不支持的传输: "+remote)
		case urlScheme.MatchString(value):
			u, err := url.Parse(value)
			if err != nil {
				return "", domain.Fail("INPUT", "远端 URL 无法解析: "+remote)
			}
			switch u.Scheme {
			case "https", "http", "ssh", "file", "git":
			default:
				return "", domain.Fail("INPUT", "远端 URL 使用了不支持的协议 "+u.Scheme+": "+remote)
			}
			if _, has := u.User.Password(); has {
				return "", domain.Fail("INPUT", "远端 "+remote+" 的 URL 内嵌了密码；改用凭据助手或 SSH 密钥，避免写入计划文件")
			}
		}
		return value, nil
	}
	return "", domain.Fail("INPUT", "远端没有配置 URL: "+remote)
}

func (r *Runner) buildPush(ctx context.Context, params map[string]string) (Plan, error) {
	if err := known(params, "remote", "tag"); err != nil {
		return Plan{}, err
	}
	pol, err := LoadPolicy(r.Root)
	if err != nil {
		return Plan{}, err
	}
	s, err := r.state(ctx)
	if err != nil {
		return Plan{}, err
	}
	if s.head == "unborn" || len(s.ops) > 0 {
		return Plan{}, domain.Fail("CONFLICT", "仓库没有提交或存在进行中的 Git 操作: "+strings.Join(s.ops, "、"))
	}
	for _, e := range s.status.Entries {
		if e.Kind == "unmerged" {
			return Plan{}, domain.Fail("CONFLICT", "存在未解决的冲突: "+e.Path)
		}
	}
	remote := params["remote"]
	if !remotePattern.MatchString(remote) {
		return Plan{}, domain.Fail("INPUT", "无效的远端名: "+remote)
	}
	names, err := r.Query(ctx, "remote")
	if err != nil {
		return Plan{}, err
	}
	found := false
	for _, n := range strings.Fields(string(names)) {
		found = found || n == remote
	}
	if !found {
		return Plan{}, domain.Fail("INPUT", "没有名为 "+remote+" 的远端")
	}
	remoteLocation, err := r.remoteURL(ctx, remote)
	if err != nil {
		return Plan{}, err
	}
	obs := s.observe()
	obs["remote"], obs["url"] = remote, remoteLocation

	if tag := params["tag"]; tag != "" {
		if !tagNamePattern.MatchString(tag) {
			return Plan{}, domain.Fail("INPUT", "标签名须形如 v1.2.3: "+tag)
		}
		kind, code, _ := r.Try(ctx, "cat-file", "-t", "refs/tags/"+tag)
		if code != 0 || strings.TrimSpace(string(kind)) != "tag" {
			return Plan{}, domain.Fail("INPUT", "本地没有带注释标签 "+tag+"（用 yss git release tag 创建）")
		}
		object, err := r.Line(ctx, "rev-parse", "--verify", "refs/tags/"+tag)
		if err != nil {
			return Plan{}, err
		}
		commit, err := r.Commit(ctx, "refs/tags/"+tag)
		if err != nil {
			return Plan{}, err
		}
		obs["tag"], obs["tagObject"], obs["tagCommit"] = tag, object, commit
		return Plan{Observed: obs, Effects: []string{"推送标签 " + tag + " 到 " + remote + "（" + remoteLocation + "）", "标签指向提交 " + commit + "；不会改写远端已有标签"}}, nil
	}

	branch := s.status.Branch
	if branch.Detached {
		return Plan{}, domain.Fail("CONFLICT", "当前是分离 HEAD；先切换到分支")
	}
	if pol.Protected(branch.Head) {
		return Plan{}, domain.Fail("CONFLICT", "分支 "+branch.Head+" 受保护，不直接推送；经分支和评审合并")
	}
	if !safeBranch.MatchString(branch.Head) || !pol.BranchAllowed(branch.Head) {
		return Plan{}, domain.Fail("CONFLICT", "分支名不符合策略 branch_pattern: "+branch.Head)
	}
	// Only the local copy of the remote's state is known offline; git itself
	// still refuses a non-fast-forward at apply time.
	tracking := ""
	if sha, e := r.Commit(ctx, "refs/remotes/"+remote+"/"+branch.Head); e == nil {
		tracking = sha
		if _, code, _ := r.Try(ctx, "merge-base", "--is-ancestor", sha, s.head); code != 0 {
			return Plan{}, domain.Fail("CONFLICT", "本地分支与 "+remote+"/"+branch.Head+" 已分叉或落后；先同步后重新规划")
		}
		if sha == s.head {
			return Plan{}, domain.Fail("INPUT", "没有需要推送的提交（"+remote+"/"+branch.Head+" 已是 HEAD）")
		}
	}
	from := tracking
	if from == "" {
		base, _, e := r.resolveBase(ctx, pol, "")
		if e != nil {
			return Plan{}, domain.Fail("INPUT", "该分支尚未推送且找不到默认分支，无法确定要审计的提交范围")
		}
		if from, e = r.Line(ctx, "merge-base", s.head, base); e != nil {
			return Plan{}, domain.Fail("INPUT", "HEAD 与默认分支没有共同祖先")
		}
	}
	commits, err := r.Log(ctx, pol, from, s.head, false)
	if err != nil {
		return Plan{}, err
	}
	if len(commits) == 0 {
		return Plan{}, domain.Fail("INPUT", "没有需要推送的提交")
	}
	if findings := Audit(pol, commits, false); len(findings) > 0 {
		first := findings[0]
		return Plan{}, domain.Fail("CONFLICT", "待推送的 "+itoa(len(commits))+" 个提交有 "+itoa(len(findings))+" 项策略问题，例如 "+first.Rule+"（"+first.SHA[:10]+" "+first.Subject+"）；修正后重新规划，或用 yss git audit 查看")
	}
	obs["tracking"], obs["pushSet"] = tracking, itoa(len(commits))
	effects := []string{"推送分支 " + branch.Head + " 到 " + remote + "（" + remoteLocation + "）", itoa(len(commits)) + " 个提交已通过策略审计，HEAD " + s.head, "不使用 --force；远端拒绝时原样报告"}
	if branch.Upstream == "" {
		effects = append(effects, "分支尚无上游，推送后设置为 "+remote+"/"+branch.Head)
	}
	obs["setUpstream"] = map[bool]string{true: "yes", false: "no"}[branch.Upstream == ""]
	return Plan{Observed: obs, Effects: effects}, nil
}

func (r *Runner) applyPush(ctx context.Context, p Plan) (any, error) {
	remote := p.Params["remote"]
	if tag := p.Params["tag"]; tag != "" {
		out, err := r.Mutate(ctx, "", "push", remote, "refs/tags/"+tag+":refs/tags/"+tag)
		if err != nil {
			return nil, err
		}
		return map[string]any{"status": "applied", "remote": remote, "tag": tag, "commit": p.Observed["tagCommit"], "output": string(out)}, nil
	}
	branch := p.Observed["branch"]
	args := []string{"push"}
	if p.Observed["setUpstream"] == "yes" {
		args = append(args, "-u")
	}
	out, err := r.Mutate(ctx, "", append(args, remote, "refs/heads/"+branch+":refs/heads/"+branch)...)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "applied", "remote": remote, "branch": branch, "commit": p.Observed["head"], "output": string(out)}, nil
}
