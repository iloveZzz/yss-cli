package kernel

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type semanticGitObservation struct {
	Root   string
	Args   []string
	Output string
	Exit   int
}

func (s *SemanticSession) GitBindings() []SemanticGitInput {
	rows := []SemanticGitInput{}
	for _, row := range s.gitInputs {
		rows = append(rows, SemanticGitInput{Root: row.Root, Arguments: row.Args, Digest: "sha256:" + safefs.Digest([]byte(row.Output)), ExitCode: row.Exit})
	}
	for _, child := range s.Children {
		rows = append(rows, child.GitBindings()...)
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
func (s *SemanticSession) Git(root string, args ...string) ([]byte, error) {
	if s.V.Virtual != nil {
		return nil, s.Reject("REPOSITORY", "归档来源不执行物理 Git 查询")
	}
	if root != s.Root && s.ExternalViews[root] == nil {
		return nil, s.Reject("REPOSITORY", "Git 工程根未登记: "+root)
	}
	if _, err := safefs.Path(root, "yss-project.yaml"); err != nil {
		return nil, s.Unavailable("PATH", err.Error())
	}
	row, err := s.gitFresh(root, args)
	if err != nil {
		return nil, err
	}
	key := root + "\x00" + strings.Join(args, "\x00")
	if old, ok := s.gitInputs[key]; ok && (old.Output != row.Output || old.Exit != row.Exit) {
		return nil, s.Unavailable("INPUT_DRIFT", "Git 输入在校验期间变化")
	}
	s.gitInputs[key] = row
	if row.Exit != 0 {
		return nil, s.Reject("GIT_EVIDENCE", fmt.Sprintf("Git 只读查询失败 (%d): %s", row.Exit, strings.Join(args, " ")))
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
		if len(args) < 5 || args[1] != "--all" || args[2] != "--format=%H" || args[3] != "--" {
			return false
		}
		for _, ref := range args[4:] {
			if safefs.ValidateRef(ref) != nil || strings.HasPrefix(ref, ":") {
				return false
			}
		}
		return true
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
		return len(args) == 3 && SemHas([]string{"-e", "-p", "-s", "-t", "blob"}, args[1]) && !strings.HasPrefix(args[2], "-")
	}
	return true
}

type SemanticLimitedBuffer struct{ bytes.Buffer }

func (b *SemanticLimitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 64<<20 {
		return 0, errors.New("Git 输出超过64MiB")
	}
	return b.Buffer.Write(p)
}

func (s *SemanticSession) gitFresh(root string, args []string) (semanticGitObservation, error) {
	row := semanticGitObservation{Root: root, Args: append([]string(nil), args...), Exit: 0}
	if err := s.Guard(); err != nil {
		return row, err
	}
	if !semanticGitArguments(args) {
		return row, s.Unavailable("CAPABILITY", "不支持的 Git 只读查询")
	}
	path, err := exec.LookPath("git")
	if err != nil {
		return row, s.Unavailable("CAPABILITY", "Git 基线校验需要 git")
	}
	flags := []string{"--no-pager", "--no-optional-locks", "--no-lazy-fetch", "--no-replace-objects", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "diff.autoRefreshIndex=false", "-c", "core.pager=cat", "-c", "diff.external=", "-c", "core.attributesFile="}
	query := append([]string(nil), args...)
	if query[0] == "diff" || query[0] == "show" {
		query = append([]string{query[0], "--no-ext-diff", "--no-textconv"}, query[1:]...)
	}
	cmd := exec.CommandContext(s.Ctx, path, append(flags, query...)...)
	cmd.Dir = root
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GIT_") && !strings.HasPrefix(e, "PAGER=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var stdout, stderr SemanticLimitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	row.Output = stdout.String()
	if e := s.Guard(); e != nil {
		return row, e
	}
	if err != nil {
		if strings.Contains(stderr.String(), "unknown option") && strings.Contains(stderr.String(), "no-lazy-fetch") {
			return row, s.Unavailable("CAPABILITY", "Git 不支持禁止自动下载；请升级 Git 后重验")
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			row.Exit = exit.ExitCode()
		} else {
			return row, s.Unavailable("EXECUTION", err.Error())
		}
	}
	return row, nil
}

// External reference roots come from the consumer's explicit scope, never from
// a candidate record. They remain observed file views, without SQLite access.
func (s *SemanticSession) ReferenceView(ref string) (*View, string, error) {
	if filepath.IsAbs(ref) {
		// Legacy decision contracts permit absolute references inside the current
		// consumer root. Preserve the recorded identity, normalize only the read.
		local, err := filepath.Rel(s.Root, ref)
		if err != nil || local == "." || filepath.Clean(ref) != ref {
			return nil, "", s.Unavailable("PATH", "绝对引用必须指向当前项目根内的规范文件")
		}
		local = filepath.ToSlash(local)
		if err := safefs.ValidateRef(local); err != nil {
			return nil, "", s.Unavailable("PATH", "绝对引用越出当前项目根")
		}
		return s.V, s.LocalRef(local), nil
	}
	if strings.HasPrefix(ref, "maintenance:") {
		if s.V.Virtual != nil {
			return nil, "", s.Reject("PATH", "归档来源不读取外部物理文件")
		}
		identity, err := s.Doc("yss-project.yaml")
		if err != nil {
			return nil, "", err
		}
		if Text(identity["repository_mode"]) != "template-source" {
			return nil, "", s.Reject("PATH", "maintenance 引用仅适用于 template-source")
		}
		// Bind inputs used by the runtime location's registered-root deny list.
		if _, err = s.V.Watch(worklayout.TrackerRef); err != nil {
			return nil, "", err
		}
		roots, err := worklayout.ReadScanRoots(s.Root)
		if err != nil {
			return nil, "", err
		}
		for _, r := range append(roots, "docs", ".template-spec/implementation", ".template-spec/projects", ".template-spec/project") {
			directories := map[string][]string{}
			entries, err := scanRegistrations(s.Root, r, s.Guard, func(ref string) error {
				_, err := s.V.Watch(ref)
				return err
			}, func(ref string, directory, pruned bool) error {
				if pruned {
					// The parent names and exact boundary identity were observed;
					// no child enumeration or Git-internal read took place.
					if err := s.RecordDirectoryVisit(directories, ref, false); err != nil {
						return err
					}
					return s.V.ObserveRegistrationBoundary(ref)
				}
				return s.RecordDirectoryVisit(directories, ref, directory)
			})
			if err != nil {
				return nil, "", err
			}
			if err = s.RecordDirectories(directories); err != nil {
				return nil, "", err
			}
			if old, ok := s.registrationScans[r]; ok && !EqualStrings(old, entries) {
				return nil, "", s.Unavailable("INPUT_DRIFT", "登记扫描集合变化: "+r)
			}
			if s.registrationScans == nil {
				s.registrationScans = map[string][]string{}
			}
			s.registrationScans[r] = entries
		}
		if _, err = s.V.Watch(".gitmodules"); err != nil {
			return nil, "", err
		}
		dir, err := RuntimeLocation(s.Root, s.Args["home"])
		if err != nil {
			return nil, "", s.Unavailable("PATH", err.Error())
		}
		return s.externalReferenceView(filepath.Join(dir, "maintenance"), strings.TrimPrefix(ref, "maintenance:"))
	}
	if strings.HasPrefix(ref, "run:") {
		root := s.Args["run-dir"]
		if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return nil, "", s.Unavailable("ARGUMENT", "run 引用需要消费者显式提供规范绝对 --run-dir")
		}
		return s.externalReferenceView(root, strings.TrimPrefix(ref, "run:"))
	}
	return s.V, s.LocalRef(ref), nil
}

func (s *SemanticSession) externalReferenceView(root, ref string) (*View, string, error) {
	if s.V.Virtual != nil {
		return nil, "", s.Reject("PATH", "归档来源不读取外部物理文件")
	}
	if _, err := safefs.Path(root, ref); err != nil {
		return nil, "", s.Unavailable("PATH", err.Error())
	}
	if root == s.Root {
		return s.V, ref, nil
	}
	v := s.ExternalViews[root]
	if v == nil {
		v = NewView(root)
		v.bindIdentity = true
		s.ExternalViews[root] = v
	}
	return v, ref, nil
}

// Fixed-tool provenance is separate from project assets and source payloads.
// A caller may explicitly supply the physical tool tree. No embedded template
// or receiving-project authority is used to fill a missing source asset.
func (s *SemanticSession) toolBytes(ref string) ([]byte, error) {
	source, err := s.ToolSourceSession()
	if err != nil {
		return nil, err
	}
	return source.Bytes(ref)
}

func (s *SemanticSession) ToolSourceSession() (*SemanticSession, error) {
	root := s.Args["tool-root"]
	if root == "" || root == s.Root {
		return s, nil
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, s.Unavailable("ARGUMENT", "--tool-root 必须是规范绝对目录")
	}
	if _, err := safefs.Path(root, "yss-project.yaml"); err != nil {
		return nil, s.Unavailable("PATH", err.Error())
	}
	if s.toolSource == nil {
		s.toolSource = NewSemanticSession(s.Ctx, root, map[string]string{})
		s.Children = append(s.Children, s.toolSource)
	}
	return s.toolSource, nil
}

// A validated scaffold registration may reserve a destination that does not
// exist yet. Observe absence only; this grants no external read/write root.
func (s *SemanticSession) ObservedExternalAbsence(target, registrationRef string) error {
	if !filepath.IsAbs(target) || filepath.Clean(target) != target || target == filepath.VolumeName(target)+string(filepath.Separator) {
		return s.Unavailable("PATH", "待初始化目标必须是规范绝对路径")
	}
	if _, err := s.Bytes(registrationRef); err != nil {
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
				return s.Unavailable("PATH", "待初始化目标缺少安全存在祖先")
			}
			ancestor = parent
			continue
		}
		if err != nil {
			return s.Unavailable("INPUT", err.Error())
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return s.Unavailable("PATH", "待初始化目标祖先必须是普通目录")
		}
		break
	}
	if len(missing) == 0 {
		return s.Reject("SCAFFOLD_TARGET_EXISTS", "待初始化目标已存在")
	}
	if _, err := safefs.Path(ancestor, ".yss-absence-observation"); err != nil {
		return s.Unavailable("PATH", err.Error())
	}
	observer := NewSemanticSession(s.Ctx, ancestor, map[string]string{})
	s.Children = append(s.Children, observer)
	v := observer.V
	for _, file := range missing {
		local, err := filepath.Rel(ancestor, file)
		if err != nil {
			return s.Unavailable("PATH", err.Error())
		}
		descriptor, err := v.Watch(filepath.ToSlash(local))
		if err != nil {
			return s.Unavailable("INPUT", err.Error())
		}
		if descriptor.Type != "missing" {
			return s.Unavailable("INPUT_DRIFT", "待初始化目标在观察期间出现")
		}
	}
	return nil
}
