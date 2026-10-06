package governance

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

type semanticGitObservation struct {
	Root   string
	Args   []string
	Output string
	Exit   int
}

func (s *semanticSession) gitBindings() []SemanticGitInput {
	rows := []SemanticGitInput{}
	for _, row := range s.gitInputs {
		rows = append(rows, SemanticGitInput{Root: row.Root, Arguments: row.Args, Digest: "sha256:" + safefs.Digest([]byte(row.Output)), ExitCode: row.Exit})
	}
	for _, child := range s.children {
		rows = append(rows, child.gitBindings()...)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Root == rows[j].Root {
			return strings.Join(rows[i].Arguments, "\x00") < strings.Join(rows[j].Arguments, "\x00")
		}
		return rows[i].Root < rows[j].Root
	})
	return rows
}

// Only validators call this seam. Asset-declared commands are never dispatched.
func (s *semanticSession) git(root string, args ...string) ([]byte, error) {
	if s.v.virtual != nil {
		return nil, s.reject("REPOSITORY", "归档来源不执行物理 Git 查询")
	}
	if root != s.root && s.externalViews[root] == nil {
		return nil, s.reject("REPOSITORY", "Git 工程根未登记: "+root)
	}
	if _, err := safefs.Path(root, "yss-project.yaml"); err != nil {
		return nil, s.unavailable("PATH", err.Error())
	}
	row, err := s.gitFresh(root, args)
	if err != nil {
		return nil, err
	}
	key := root + "\x00" + strings.Join(args, "\x00")
	if old, ok := s.gitInputs[key]; ok && (old.Output != row.Output || old.Exit != row.Exit) {
		return nil, s.unavailable("INPUT_DRIFT", "Git 输入在校验期间变化")
	}
	s.gitInputs[key] = row
	if row.Exit != 0 {
		return nil, s.reject("GIT_EVIDENCE", fmt.Sprintf("Git 只读查询失败 (%d): %s", row.Exit, strings.Join(args, " ")))
	}
	return []byte(row.Output), nil
}

func semanticGitArguments(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "log":
		// Daily routing inspects only committed task history with a fixed,
		// non-executable format. Never accept arbitrary log formatting/options.
		if len(args) != 9 || args[1] != "--all" || args[2] != "--format=%H" || args[3] != "--" {
			return false
		}
		return args[4] == "docs/.scratch" && args[5] == ".scratch" && args[6] == "docs/tasks" && args[7] == ".template-spec/implementation" && safefs.ValidateRef(args[8]) == nil && !strings.HasPrefix(args[8], ":")
	case "rev-parse", "cat-file", "ls-tree", "ls-files", "status", "show", "diff":
	case "check-ignore":
		return len(args) == 4 && args[1] == "-q" && args[2] == "--" && !strings.ContainsAny(args[3], "\x00\r\n")
	case "config":
		return len(args) == 4 && args[1] == "--local" && args[2] == "--get" && args[3] == "remote.origin.url"
	default:
		return false
	}
	for _, a := range args[1:] {
		if strings.ContainsAny(a, "\x00\r\n") || a == "--help" || a == "-h" || a == "--filters" || strings.HasPrefix(a, "--output") || strings.HasPrefix(a, "--ext-diff") || strings.HasPrefix(a, "--textconv") || strings.HasPrefix(a, "--open-files-in-pager") || a == "--refresh" || a == "--resolve-undo" {
			return false
		}
	}
	if args[0] == "cat-file" {
		return len(args) == 3 && semHas([]string{"-e", "-p", "-s", "-t", "blob"}, args[1]) && !strings.HasPrefix(args[2], "-")
	}
	return true
}

type semanticLimitedBuffer struct{ bytes.Buffer }

func (b *semanticLimitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 64<<20 {
		return 0, errors.New("Git 输出超过64MiB")
	}
	return b.Buffer.Write(p)
}

func (s *semanticSession) gitFresh(root string, args []string) (semanticGitObservation, error) {
	row := semanticGitObservation{Root: root, Args: append([]string(nil), args...), Exit: 0}
	if err := s.guard(); err != nil {
		return row, err
	}
	if !semanticGitArguments(args) {
		return row, s.unavailable("CAPABILITY", "不支持的 Git 只读查询")
	}
	path, err := exec.LookPath("git")
	if err != nil {
		return row, s.unavailable("CAPABILITY", "Git 基线校验需要 git")
	}
	flags := []string{"--no-pager", "--no-optional-locks", "--no-lazy-fetch", "--no-replace-objects", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "core.pager=cat", "-c", "diff.external=", "-c", "core.attributesFile="}
	query := append([]string(nil), args...)
	if query[0] == "diff" || query[0] == "show" {
		query = append([]string{query[0], "--no-ext-diff", "--no-textconv"}, query[1:]...)
	}
	cmd := exec.CommandContext(s.ctx, path, append(flags, query...)...)
	cmd.Dir = root
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GIT_") && !strings.HasPrefix(e, "PAGER=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var stdout, stderr semanticLimitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	row.Output = stdout.String()
	if e := s.guard(); e != nil {
		return row, e
	}
	if err != nil {
		if strings.Contains(stderr.String(), "unknown option") && strings.Contains(stderr.String(), "no-lazy-fetch") {
			return row, s.unavailable("CAPABILITY", "Git 不支持禁止自动下载；请升级 Git 后重验")
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			row.Exit = exit.ExitCode()
		} else {
			return row, s.unavailable("EXECUTION", err.Error())
		}
	}
	return row, nil
}

// External reference roots come from the consumer's explicit scope, never from
// a candidate record. They remain observed file views, without SQLite access.
func (s *semanticSession) referenceView(ref string) (*view, string, error) {
	if filepath.IsAbs(ref) {
		// Legacy decision contracts permit absolute references inside the current
		// consumer root. Preserve the recorded identity, normalize only the read.
		local, err := filepath.Rel(s.root, ref)
		if err != nil || local == "." || filepath.Clean(ref) != ref {
			return nil, "", s.unavailable("PATH", "绝对引用必须指向当前项目根内的规范文件")
		}
		local = filepath.ToSlash(local)
		if err := safefs.ValidateRef(local); err != nil {
			return nil, "", s.unavailable("PATH", "绝对引用越出当前项目根")
		}
		return s.v, s.localRef(local), nil
	}
	if strings.HasPrefix(ref, "maintenance:") {
		identity, err := s.doc("yss-project.yaml")
		if err != nil {
			return nil, "", err
		}
		if text(identity["repository_mode"]) != "template-source" {
			return nil, "", s.reject("PATH", "maintenance 引用仅适用于 template-source")
		}
		// Bind inputs used by the runtime location's registered-root deny list.
		for _, r := range []string{"docs", ".scratch", ".template-spec/implementation", ".template-spec/projects", ".template-spec/project"} {
			if _, err = s.scan(r); err != nil {
				return nil, "", err
			}
		}
		if _, err = s.v.watch(".gitmodules"); err != nil {
			return nil, "", err
		}
		dir, err := runtimeLocation(s.root, s.args["home"])
		if err != nil {
			return nil, "", s.unavailable("PATH", err.Error())
		}
		return s.externalReferenceView(filepath.Join(dir, "maintenance"), strings.TrimPrefix(ref, "maintenance:"))
	}
	if strings.HasPrefix(ref, "run:") {
		root := s.args["run-dir"]
		if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return nil, "", s.unavailable("ARGUMENT", "run 引用需要消费者显式提供规范绝对 --run-dir")
		}
		return s.externalReferenceView(root, strings.TrimPrefix(ref, "run:"))
	}
	return s.v, s.localRef(ref), nil
}
func (s *semanticSession) externalReferenceView(root, ref string) (*view, string, error) {
	if s.v.virtual != nil {
		return nil, "", s.reject("PATH", "归档来源不读取外部物理文件")
	}
	if _, err := safefs.Path(root, ref); err != nil {
		return nil, "", s.unavailable("PATH", err.Error())
	}
	if root == s.root {
		return s.v, ref, nil
	}
	v := s.externalViews[root]
	if v == nil {
		v = newView(root)
		v.bindIdentity = true
		s.externalViews[root] = v
	}
	return v, ref, nil
}

// Fixed-tool provenance is separate from project assets and source payloads.
// A caller may explicitly supply the physical tool tree. No embedded template
// or receiving-project authority is used to fill a missing source asset.
func (s *semanticSession) toolBytes(ref string) ([]byte, error) {
	source, err := s.toolSourceSession()
	if err != nil {
		return nil, err
	}
	return source.bytes(ref)
}
func (s *semanticSession) toolSourceSession() (*semanticSession, error) {
	root := s.args["tool-root"]
	if root == "" || root == s.root {
		return s, nil
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, s.unavailable("ARGUMENT", "--tool-root 必须是规范绝对目录")
	}
	if _, err := safefs.Path(root, "yss-project.yaml"); err != nil {
		return nil, s.unavailable("PATH", err.Error())
	}
	if s.toolSource == nil {
		s.toolSource = newSemanticSession(s.ctx, root, map[string]string{})
		s.children = append(s.children, s.toolSource)
	}
	return s.toolSource, nil
}

// A validated scaffold registration may reserve a destination that does not
// exist yet. Observe absence only; this grants no external read/write root.
func (s *semanticSession) observedExternalAbsence(target, registrationRef string) error {
	if !filepath.IsAbs(target) || filepath.Clean(target) != target || target == filepath.VolumeName(target)+string(filepath.Separator) {
		return s.unavailable("PATH", "待初始化目标必须是规范绝对路径")
	}
	if _, err := s.bytes(registrationRef); err != nil {
		return err
	}
	ancestor := target
	missing := []string{}
	for {
		st, err := os.Lstat(ancestor)
		if os.IsNotExist(err) {
			missing = append(missing, ancestor)
			parent := filepath.Dir(ancestor)
			if parent == ancestor {
				return s.unavailable("PATH", "待初始化目标缺少安全存在祖先")
			}
			ancestor = parent
			continue
		}
		if err != nil {
			return s.unavailable("INPUT", err.Error())
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return s.unavailable("PATH", "待初始化目标祖先必须是普通目录")
		}
		break
	}
	if len(missing) == 0 {
		return s.reject("SCAFFOLD_TARGET_EXISTS", "待初始化目标已存在")
	}
	if _, err := safefs.Path(ancestor, ".yss-absence-observation"); err != nil {
		return s.unavailable("PATH", err.Error())
	}
	observer := newSemanticSession(s.ctx, ancestor, map[string]string{})
	s.children = append(s.children, observer)
	v := observer.v
	for _, file := range missing {
		local, err := filepath.Rel(ancestor, file)
		if err != nil {
			return s.unavailable("PATH", err.Error())
		}
		descriptor, err := v.watch(filepath.ToSlash(local))
		if err != nil {
			return s.unavailable("INPUT", err.Error())
		}
		if descriptor.Type != "missing" {
			return s.unavailable("INPUT_DRIFT", "待初始化目标在观察期间出现")
		}
	}
	return nil
}
