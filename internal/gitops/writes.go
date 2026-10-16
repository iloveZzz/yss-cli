package gitops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

var (
	slugPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,60}$`)
	typePattern    = regexp.MustCompile(`^[a-z]{2,12}$`)
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)
)

// ---- branch start ----

func (r *Runner) buildBranch(ctx context.Context, params map[string]string) (Plan, error) {
	if err := known(params, "task", "type", "slug", "from"); err != nil {
		return Plan{}, err
	}
	pol, err := LoadPolicy(r.Root)
	if err != nil {
		return Plan{}, err
	}
	task, kind, slug := params["task"], params["type"], params["slug"]
	if !pol.TicketValid(task) {
		return Plan{}, domain.Fail("INPUT", "Ticket 不符合策略 ticket_pattern: "+task)
	}
	if !typePattern.MatchString(kind) || !slugPattern.MatchString(slug) {
		return Plan{}, domain.Fail("INPUT", "--type 须为小写字母，--slug 须为小写字母、数字和连字符")
	}
	name := kind + "/" + task + "-" + slug
	if !safeBranch.MatchString(name) || !pol.BranchAllowed(name) {
		return Plan{}, domain.Fail("INPUT", "分支名不符合策略 branch_pattern: "+name)
	}
	if _, code, qerr := r.Try(ctx, "check-ref-format", "--branch", name); code != 0 || (qerr != nil && !isStderr(qerr)) {
		return Plan{}, domain.Fail("INPUT", "不是合法的分支名: "+name)
	}
	if _, e := r.Commit(ctx, "refs/heads/"+name); e == nil {
		return Plan{}, domain.Fail("CONFLICT", "分支已存在: "+name)
	}
	s, err := r.state(ctx)
	if err != nil {
		return Plan{}, err
	}
	if s.head == "unborn" || len(s.ops) > 0 {
		return Plan{}, domain.Fail("CONFLICT", "仓库没有提交或存在进行中的 Git 操作")
	}
	for _, e := range s.status.Entries {
		if e.Kind == "unmerged" {
			return Plan{}, domain.Fail("CONFLICT", "存在未解决的冲突: "+e.Path)
		}
	}
	base, ref, err := r.resolveBase(ctx, pol, params["from"])
	if err != nil {
		return Plan{}, err
	}
	obs := s.observe()
	obs["base"] = base
	return Plan{Observed: obs, Effects: []string{"创建并切换到分支 " + name, "起点 " + base + "（" + ref + "）；未提交的改动随工作区保留"}}, nil
}

func (r *Runner) applyBranch(ctx context.Context, p Plan) (any, error) {
	name := p.Params["type"] + "/" + p.Params["task"] + "-" + p.Params["slug"]
	if _, err := r.Mutate(ctx, "", "switch", "-c", name, p.Observed["base"]); err != nil {
		return nil, err
	}
	return map[string]any{"status": "applied", "branch": name, "base": p.Observed["base"], "next": "yss lifecycle route --base " + p.Observed["base"]}, nil
}

func isStderr(err error) bool { var t *stderrText; return errors.As(err, &t) }

// ---- commit ----

func (r *Runner) buildCommit(ctx context.Context, params map[string]string) (Plan, error) {
	if err := known(params, "task", "message", "paths"); err != nil {
		return Plan{}, err
	}
	pol, err := LoadPolicy(r.Root)
	if err != nil {
		return Plan{}, err
	}
	if !pol.TicketValid(params["task"]) {
		return Plan{}, domain.Fail("INPUT", "Ticket 不符合策略 ticket_pattern: "+params["task"])
	}
	if _, err = commitMessage(pol, params); err != nil {
		return Plan{}, err
	}
	requested, err := splitPaths(params["paths"])
	if err != nil {
		return Plan{}, err
	}
	s, err := r.state(ctx)
	if err != nil {
		return Plan{}, err
	}
	if err = s.requireReady(false, pol); err != nil {
		return Plan{}, err
	}
	name, _ := r.ConfigValue(ctx, "user.name")
	email, _ := r.ConfigValue(ctx, "user.email")
	if name == "" || email == "" {
		return Plan{}, domain.Fail("CAPABILITY", "未配置 user.name / user.email，无法提交")
	}
	covered := func(p string) bool {
		for _, want := range requested {
			if p == want || strings.HasPrefix(p, want+"/") {
				return true
			}
		}
		return false
	}
	obs := s.observe()
	files := []string{}
	excluded := []string{}
	for _, e := range s.status.Entries {
		if !covered(e.Path) {
			excluded = append(excluded, e.Path)
			continue
		}
		if e.Orig != "" && !covered(e.Orig) {
			return Plan{}, domain.Fail("INPUT", "重命名需同时列出原路径: "+e.Orig)
		}
		digest, derr := fileDigest(r.Root, e)
		if derr != nil {
			return Plan{}, derr
		}
		obs["file:"+e.Path] = digest
		files = append(files, e.Path)
	}
	for _, want := range requested {
		hit := false
		for _, f := range files {
			if f == want || strings.HasPrefix(f, want+"/") {
				hit = true
			}
		}
		if !hit {
			return Plan{}, domain.Fail("INPUT", "路径没有可提交的改动: "+want)
		}
	}
	sort.Strings(files)
	sort.Strings(excluded)
	effects := []string{"在分支 " + s.status.Branch.Head + " 提交 " + itoa(len(files)) + " 个文件", "Ticket: " + params["task"]}
	if len(excluded) > 0 {
		effects = append(effects, "范围外 "+itoa(len(excluded))+" 个路径不会提交")
	}
	return Plan{Observed: obs, Effects: append(effects, files...), Excluded: excluded}, nil
}

func (r *Runner) applyCommit(ctx context.Context, p Plan) (any, error) {
	pol, err := LoadPolicy(r.Root)
	if err != nil {
		return nil, err
	}
	paths, _ := splitPaths(p.Params["paths"])
	message, _ := commitMessage(pol, p.Params)
	if _, err = r.Mutate(ctx, "", append([]string{"add", "-A", "--"}, paths...)...); err != nil {
		return nil, domain.WithMessage(err, err.Error()+"（暂存区可能已更新，请用 yss git status 查看）")
	}
	args := []string{"commit", "--only", "-F", "-"}
	if pol.RequireSigned {
		args = append(args, "-S")
	}
	if _, err = r.Mutate(ctx, message, append(append(args, "--"), paths...)...); err != nil {
		return nil, domain.WithMessage(err, err.Error()+"（暂存区可能已更新，请用 yss git status 查看）")
	}
	head, err := r.Commit(ctx, "HEAD")
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "applied", "commit": head, "branch": p.Observed["branch"], "ticket": p.Params["task"], "excluded": p.Excluded}, nil
}

func commitMessage(pol Policy, params map[string]string) (string, error) {
	subject := strings.TrimSpace(params["message"])
	if subject == "" || strings.ContainsAny(subject, "\r\n\x00") || !utf8.ValidString(subject) || len([]rune(subject)) > 200 {
		return "", domain.Fail("INPUT", "--message 须为 1–200 字的单行文本")
	}
	return subject + "\n\n" + pol.TicketTrailer + ": " + params["task"] + "\n", nil
}

func splitPaths(list string) ([]string, error) {
	if list == "" {
		return nil, domain.Fail("INPUT", "需要 --paths <路径,路径…>；提交只包含明确列出的路径")
	}
	seen := map[string]bool{}
	out := []string{}
	for _, p := range strings.Split(list, ",") {
		if err := ValidatePath(p); err != nil {
			return nil, err
		}
		if path.Clean(p) != p {
			return nil, domain.Fail("INPUT", "路径须为规范形式: "+p)
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

// fileDigest binds the exact bytes a commit will contain.
func fileDigest(root string, e Entry) (string, error) {
	if e.Submodule {
		return "submodule", nil
	}
	full := filepath.Join(root, filepath.FromSlash(e.Path))
	st, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	switch {
	case st.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(full)
		return "link:" + target, err
	case st.IsDir():
		return "dir", nil
	case !st.Mode().IsRegular():
		return "", domain.Fail("PATH", "不支持的文件类型: "+e.Path)
	}
	f, err := os.Open(full)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)) + ":" + itoa(int(st.Mode().Perm())), nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for ; n > 0; n /= 10 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
	}
	return string(digits)
}

// ---- release tag ----

func (r *Runner) buildTag(ctx context.Context, params map[string]string) (Plan, error) {
	if err := known(params, "tag-version", "evidence-file"); err != nil {
		return Plan{}, err
	}
	pol, err := LoadPolicy(r.Root)
	if err != nil {
		return Plan{}, err
	}
	version := params["tag-version"]
	if !versionPattern.MatchString(version) {
		return Plan{}, domain.Fail("INPUT", "--tag-version 须为 MAJOR.MINOR.PATCH[-预发布]: "+version)
	}
	name := "v" + version
	if _, e := r.Commit(ctx, "refs/tags/"+name); e == nil {
		return Plan{}, domain.Fail("CONFLICT", "标签已存在: "+name)
	}
	s, err := r.state(ctx)
	if err != nil {
		return Plan{}, err
	}
	if err = s.requireReady(true, pol); err != nil {
		return Plan{}, err
	}
	if s.status.Branch.Head != pol.DefaultBranch {
		return Plan{}, domain.Fail("CONFLICT", "发布标签须在默认分支 "+pol.DefaultBranch+" 上，当前 "+s.status.Branch.Head)
	}
	if len(s.status.Entries) > 0 {
		return Plan{}, domain.Fail("CONFLICT", "工作区有 "+itoa(len(s.status.Entries))+" 个未提交路径；发布标签前须干净（hooks install / policy set 的写入需按其提示提交，.yss/ 可加入 .gitignore）")
	}
	name2, _ := r.ConfigValue(ctx, "user.name")
	email, _ := r.ConfigValue(ctx, "user.email")
	if name2 == "" || email == "" {
		return Plan{}, domain.Fail("CAPABILITY", "未配置 user.name / user.email，无法创建标签")
	}
	obs := s.observe()
	effects := []string{"在 " + s.head + " 创建本地标签 " + name + "（不推送）"}
	if ev := params["evidence-file"]; ev != "" {
		if err = ValidatePath(ev); err != nil {
			return Plan{}, err
		}
		if _, code, _ := r.Try(ctx, "ls-files", "--error-unmatch", "--", ev); code != 0 {
			return Plan{}, domain.Fail("INPUT", "证据文件未纳入版本控制: "+ev)
		}
		d, derr := fileDigest(r.Root, Entry{Path: ev})
		if derr != nil || d == "missing" || strings.HasPrefix(d, "link:") || d == "dir" {
			return Plan{}, domain.Fail("INPUT", "证据文件必须是仓库内的普通文件: "+ev)
		}
		obs["file:"+ev] = d
		effects = append(effects, "标签信息记录证据 "+ev+" 的摘要")
	}
	return Plan{Observed: obs, Effects: effects}, nil
}

func (r *Runner) applyTag(ctx context.Context, p Plan) (any, error) {
	pol, err := LoadPolicy(r.Root)
	if err != nil {
		return nil, err
	}
	name := "v" + p.Params["tag-version"]
	message := "Release " + name
	if ev := p.Params["evidence-file"]; ev != "" {
		message += "\n\nEvidence: " + ev + " sha256:" + strings.SplitN(p.Observed["file:"+ev], ":", 2)[0]
	}
	flag := "-a"
	if pol.RequireSigned {
		flag = "-s"
	}
	if _, err = r.Mutate(ctx, "", "tag", flag, name, "-m", message, p.Observed["head"]); err != nil {
		return nil, err
	}
	return map[string]any{"status": "applied", "tag": name, "commit": p.Observed["head"], "pushed": false}, nil
}
