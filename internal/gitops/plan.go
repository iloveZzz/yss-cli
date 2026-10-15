package gitops

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

const (
	KindBranchStart = "git-branch-start"
	KindCommit      = "git-commit"
	KindTag         = "git-release-tag"
	KindHooks       = "git-hooks-install"
	KindPolicySet   = "git-policy-set"

	planVersion = 1
	planLimit   = 8 << 20
)

// Plan binds a write to the repository state it was computed from. Observed
// holds HEAD, the status digest and per-file digests; any change between plan
// and apply produces a different digest and the apply is refused.
type Plan struct {
	SchemaVersion int                     `json:"schemaVersion"`
	Kind          string                  `json:"kind"`
	Root          string                  `json:"root"`
	Params        map[string]string       `json:"params"`
	Observed      map[string]string       `json:"observed"`
	Effects       []string                `json:"effects"`
	Excluded      []string                `json:"excluded,omitempty"`
	Operations    []transaction.Operation `json:"operations,omitempty"`
	PlanDigest    string                  `json:"planDigest"`
}

func (p Plan) digest() string {
	p.PlanDigest = ""
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Public is the reviewable view: operation bytes stay in the saved file.
func (p Plan) Public() map[string]any {
	ops := []map[string]any{}
	for _, op := range p.Operations {
		ops = append(ops, map[string]any{"path": op.Path, "directory": op.Directory, "bytes": len(op.Data)})
	}
	return map[string]any{"kind": p.Kind, "root": p.Root, "params": p.Params, "observed": p.Observed, "effects": p.Effects, "excluded": p.Excluded, "fileOperations": ops, "planDigest": p.PlanDigest, "status": "planned"}
}

// SavePlan creates the file exclusively (0600). The plan must live outside the
// repository, otherwise saving it would itself change the status it binds.
func SavePlan(root, out string, p Plan) error {
	abs, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return domain.Fail("PATH", "计划文件所在目录不存在: "+filepath.Dir(abs))
	}
	top, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if dir == top || strings.HasPrefix(dir+string(filepath.Separator), top+string(filepath.Separator)) {
		return domain.Fail("PATH", "计划文件须保存在仓库之外，避免改变其绑定的状态")
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, filepath.Base(abs)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return domain.Fail("EXISTS", "计划文件已存在: "+out)
		}
		return err
	}
	_, werr := f.Write(append(b, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

func ReadPlan(file string) (Plan, error) {
	var p Plan
	st, err := os.Lstat(file)
	if err != nil {
		return p, domain.Fail("PLAN", "无法读取计划文件: "+file)
	}
	if !st.Mode().IsRegular() || st.Size() > planLimit {
		return p, domain.Fail("PLAN", "计划文件必须是不超过 8MiB 的普通文件")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return p, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&p); err != nil {
		return p, domain.Fail("PLAN", "计划文件格式无效: "+err.Error())
	}
	if _, err = dec.Token(); !errors.Is(err, io.EOF) {
		return p, domain.Fail("PLAN", "计划文件含多余内容")
	}
	if p.SchemaVersion != planVersion || p.PlanDigest == "" || p.PlanDigest != p.digest() {
		return p, domain.Fail("PLAN", "计划版本或摘要无效；重新生成计划")
	}
	return p, nil
}

// Build computes a fresh plan from the current repository state.
func (r *Runner) Build(ctx context.Context, kind string, params map[string]string) (Plan, error) {
	var p Plan
	var err error
	switch kind {
	case KindBranchStart:
		p, err = r.buildBranch(ctx, params)
	case KindCommit:
		p, err = r.buildCommit(ctx, params)
	case KindTag:
		p, err = r.buildTag(ctx, params)
	case KindHooks:
		p, err = r.buildHooks(ctx, params)
	case KindPolicySet:
		p, err = r.buildPolicySet(ctx, params)
	default:
		return p, domain.Fail("PLAN", "未知计划类型: "+kind)
	}
	if err != nil {
		return p, err
	}
	p.SchemaVersion, p.Kind, p.Root, p.Params = planVersion, kind, r.Root, params
	p.PlanDigest = p.digest()
	return p, nil
}

// Apply re-derives the plan from the saved parameters and runs it only when
// the result is byte-identical to what was reviewed.
func (r *Runner) Apply(ctx context.Context, saved Plan) (any, error) {
	if saved.Root != r.Root {
		return nil, domain.Fail("PLAN", "计划绑定的仓库根与 --root 不一致")
	}
	fresh, err := r.Build(ctx, saved.Kind, saved.Params)
	var de *domain.Error
	if errors.As(err, &de) && (de.Code == "INPUT" || de.Code == "CONFLICT") {
		return nil, domain.Fail("BASELINE", "仓库自计划生成后已变化，无法重新核验: "+de.Message)
	}
	if err != nil {
		return nil, err
	}
	if fresh.PlanDigest != saved.PlanDigest {
		changed := []string{}
		for k, v := range saved.Observed {
			if fresh.Observed[k] != v {
				changed = append(changed, k)
			}
		}
		for k := range fresh.Observed {
			if _, ok := saved.Observed[k]; !ok {
				changed = append(changed, k)
			}
		}
		sort.Strings(changed)
		if len(changed) > 3 {
			changed = append(changed[:3], "…")
		}
		return nil, domain.Fail("BASELINE", "仓库自计划生成后已变化，拒绝应用；重新规划（变化项: "+strings.Join(changed, ", ")+"）")
	}
	switch fresh.Kind {
	case KindBranchStart:
		return r.applyBranch(ctx, fresh)
	case KindCommit:
		return r.applyCommit(ctx, fresh)
	case KindTag:
		return r.applyTag(ctx, fresh)
	case KindHooks:
		return r.applyHooks(ctx, fresh)
	default:
		return r.applyPolicySet(ctx, fresh)
	}
}

func known(params map[string]string, allowed ...string) error {
	ok := map[string]bool{}
	for _, k := range allowed {
		ok[k] = true
	}
	for k := range params {
		if !ok[k] {
			return domain.Fail("PLAN", "计划含未知参数: "+k)
		}
	}
	return nil
}

type repoState struct {
	head   string
	status Status
	ops    []string
}

func (r *Runner) state(ctx context.Context) (repoState, error) {
	var s repoState
	var err error
	if s.head, err = r.Commit(ctx, "HEAD"); err != nil {
		s.head = "unborn"
	}
	if s.status, err = r.ReadStatus(ctx); err != nil {
		return s, err
	}
	s.ops, err = r.Operations(ctx)
	return s, err
}

func (s repoState) observe() map[string]string {
	return map[string]string{"head": s.head, "branch": s.status.Branch.Head, "status": s.status.Digest}
}

func (s repoState) requireReady(allowProtected bool, p Policy) error {
	switch {
	case s.head == "unborn":
		return domain.Fail("CONFLICT", "仓库还没有任何提交")
	case len(s.ops) > 0:
		return domain.Fail("CONFLICT", "存在进行中的 Git 操作: "+strings.Join(s.ops, "、"))
	case s.status.Branch.Detached:
		return domain.Fail("CONFLICT", "当前是分离 HEAD；先切换到分支")
	}
	for _, e := range s.status.Entries {
		if e.Kind == "unmerged" {
			return domain.Fail("CONFLICT", "存在未解决的冲突: "+e.Path)
		}
	}
	if !allowProtected && p.Protected(s.status.Branch.Head) {
		return domain.Fail("CONFLICT", "当前分支 "+s.status.Branch.Head+" 受保护；先用 yss git branch start 开分支")
	}
	return nil
}

// resolveBase picks the start point: an explicit ref, else the policy default
// branch (local first, then origin).
func (r *Runner) resolveBase(ctx context.Context, p Policy, from string) (string, string, error) {
	if from != "" {
		sha, err := r.Commit(ctx, from)
		return sha, from, err
	}
	for _, ref := range []string{"refs/heads/" + p.DefaultBranch, "refs/remotes/origin/" + p.DefaultBranch} {
		if sha, err := r.Commit(ctx, ref); err == nil {
			return sha, ref, nil
		}
	}
	return "", "", domain.Fail("INPUT", "找不到默认分支 "+p.DefaultBranch+"；用 --from 指定起点")
}
