// Package gitops wraps the system git for the yss git command group. The
// first phase is read-only: every invocation goes through an allow-list,
// strips inherited GIT_* overrides and disables pagers and fsmonitor so a
// query cannot write the repository or wait for a terminal.
package gitops

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

const (
	outputLimit = 64 << 20
	stderrLimit = 64 << 10
	callTimeout = 2 * time.Minute
)

// Read-only subcommands. Write commands are added only with their plan/apply
// guards, never by widening this list.
var queryCommands = map[string]bool{
	"version": true, "rev-parse": true, "status": true, "log": true, "merge-base": true, "check-ref-format": true, "check-ignore": true, "ls-files": true, "cat-file": true,
	"symbolic-ref": true, "show-ref": true, "config": true, "remote": true, "submodule": true,
}

// Runner executes read-only git queries in one repository root.
type Runner struct {
	Root string
	path string
}

func NewRunner(root string) (*Runner, error) {
	path, err := exec.LookPath("git")
	if err != nil {
		return nil, domain.Fail("CAPABILITY", "yss git 需要 PATH 中的 git")
	}
	return &Runner{Root: root, path: path}, nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		b.exceeded = true
		return 0, errors.New("git 输出超过上限")
	}
	return b.Buffer.Write(p)
}

// Try runs a query and returns its exit code; a non-zero exit is not an
// error because several queries (config --get, merge-base) use it as data.
func (r *Runner) Try(ctx context.Context, args ...string) ([]byte, int, error) {
	return r.try(ctx, nil, args...)
}

func (r *Runner) try(ctx context.Context, input []byte, args ...string) ([]byte, int, error) {
	if len(args) == 0 || !queryCommands[args[0]] {
		return nil, 0, domain.Fail("CAPABILITY", "不支持的 Git 只读查询")
	}
	if args[0] == "config" && !readOnlyConfig(args[1:]) {
		return nil, 0, domain.Fail("CAPABILITY", "git config 只允许读取")
	}
	if args[0] == "cat-file" && (len(args) != 3 || args[1] != "-t") {
		return nil, 0, domain.Fail("CAPABILITY", "git cat-file 只允许 -t")
	}
	if args[0] == "remote" && len(args) > 1 && args[1] != "-v" {
		return nil, 0, domain.Fail("CAPABILITY", "git remote 只允许列出")
	}
	if args[0] == "submodule" && (len(args) < 2 || args[1] != "status") {
		return nil, 0, domain.Fail("CAPABILITY", "git submodule 只允许 status")
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	flags := []string{"--no-pager", "--no-optional-locks", "--no-replace-objects", "-c", "core.fsmonitor=false", "-c", "core.pager=cat", "-c", "color.ui=false", "-c", "diff.external=", "-c", "log.showSignature=false"}
	query := append([]string(nil), args...)
	if query[0] == "log" {
		query = append([]string{"log", "--no-ext-diff", "--no-textconv"}, query[1:]...)
	}
	cmd := exec.CommandContext(ctx, r.path, append(flags, query...)...)
	cmd.Dir = r.Root
	cmd.Env = queryEnv()
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	stdout := &limitedBuffer{limit: outputLimit}
	stderr := &limitedBuffer{limit: stderrLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, 0, domain.Wrap("CANCELLED", ctx.Err())
	}
	if stdout.exceeded {
		return nil, 0, domain.Fail("EXECUTION", "git "+args[0]+" 输出超过 64MiB")
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return stdout.Bytes(), exit.ExitCode(), &stderrText{strings.TrimSpace(stderr.String())}
	}
	if err != nil {
		return nil, 0, domain.Wrap("EXECUTION", err)
	}
	return stdout.Bytes(), 0, nil
}

type stderrText struct{ text string }

func (e *stderrText) Error() string { return e.text }

// Query requires exit 0 and reports git's stderr otherwise.
func (r *Runner) Query(ctx context.Context, args ...string) ([]byte, error) {
	out, code, err := r.Try(ctx, args...)
	var text *stderrText
	if errors.As(err, &text) {
		message := "git " + args[0] + " 失败"
		if text.text != "" {
			message += ": " + firstLine(text.text)
		}
		return nil, domain.Fail("EXECUTION", message)
	}
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, domain.Fail("EXECUTION", "git "+args[0]+" 失败")
	}
	return out, nil
}

// Line runs Query and returns the trimmed first line.
func (r *Runner) Line(ctx context.Context, args ...string) (string, error) {
	out, err := r.Query(ctx, args...)
	if err != nil {
		return "", err
	}
	return firstLine(strings.TrimSpace(string(out))), nil
}

func queryEnv() []string {
	env := []string{}
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GIT_") && !strings.HasPrefix(e, "PAGER=") {
			env = append(env, e)
		}
	}
	return append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "LC_ALL=C")
}

func readOnlyConfig(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "--get", "--get-all", "--get-regexp", "--list":
		return true
	case "--local", "--global", "--show-origin", "--null":
		return readOnlyConfig(args[1:])
	case "-f", "--file":
		return len(args) > 1 && readOnlyConfig(args[2:])
	}
	return false
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// TopLevel requires root to be the top of a work tree, so relative paths in
// results always refer to the directory the user named.
func (r *Runner) TopLevel(ctx context.Context) error {
	out, code, err := r.Try(ctx, "rev-parse", "--show-toplevel")
	if code != 0 || err != nil {
		var text *stderrText
		if err != nil && !errors.As(err, &text) {
			return err
		}
		return domain.Fail("ROOT", "不是 Git 工作树: "+r.Root)
	}
	top := strings.TrimSpace(string(out))
	want, e1 := filepath.EvalSymlinks(r.Root)
	got, e2 := filepath.EvalSymlinks(top)
	if e1 != nil || e2 != nil || filepath.Clean(want) != filepath.Clean(got) {
		return domain.Fail("ROOT", "--root 须为 Git 仓库顶层: "+top)
	}
	return nil
}

// Commit resolves a user-supplied revision to a full commit SHA. Leading
// dashes are refused so a revision can never be read as an option.
func (r *Runner) Commit(ctx context.Context, rev string) (string, error) {
	if rev == "" || strings.HasPrefix(rev, "-") || strings.ContainsAny(rev, "\x00\n") {
		return "", domain.Fail("ARGUMENT", "无效的 Git 引用: "+rev)
	}
	out, code, err := r.Try(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
	var text *stderrText
	if err != nil && !errors.As(err, &text) {
		return "", err
	}
	if code != 0 {
		return "", domain.Fail("INPUT", "无法解析为提交: "+rev)
	}
	return strings.TrimSpace(string(out)), nil
}
