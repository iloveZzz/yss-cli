package gitops

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

const mutateTimeout = 5 * time.Minute

var (
	remotePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	shaPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	tagNamePattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)
	hooksPattern   = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
)

// mutationAllowed accepts only the exact argument shapes the plan appliers
// build. Every other write (reset, clean, checkout, push, ...) is refused here
// regardless of what a caller passes.
func mutationAllowed(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "switch":
		return len(args) == 4 && args[1] == "-c" && safeBranch.MatchString(args[2]) && shaPattern.MatchString(args[3])
	case "add":
		return len(args) >= 4 && args[1] == "-A" && args[2] == "--" && pathsSafe(args[3:])
	case "commit":
		sep := -1
		for i, a := range args {
			if a == "--" {
				sep = i
				break
			}
		}
		if sep < 0 || sep == len(args)-1 || !pathsSafe(args[sep+1:]) {
			return false
		}
		head := strings.Join(args[1:sep], " ")
		return head == "--only -F -" || head == "--only -F - -S"
	case "tag":
		return len(args) == 6 && (args[1] == "-a" || args[1] == "-s") && tagNamePattern.MatchString(args[2]) && args[3] == "-m" && args[4] != "" && shaPattern.MatchString(args[5])
	case "push":
		rest := args[1:]
		if len(rest) > 0 && rest[0] == "-u" {
			rest = rest[1:]
		}
		if len(rest) != 2 || !remotePattern.MatchString(rest[0]) {
			return false
		}
		src, dst, ok := strings.Cut(rest[1], ":")
		if !ok || src != dst || (len(args) == 4 && !strings.HasPrefix(src, "refs/heads/")) {
			return false
		}
		if name, isBranch := strings.CutPrefix(src, "refs/heads/"); isBranch {
			return safeBranch.MatchString(name)
		}
		name, isTag := strings.CutPrefix(src, "refs/tags/")
		return isTag && tagNamePattern.MatchString(name)
	case "config":
		return len(args) == 4 && args[1] == "--local" && args[2] == "core.hooksPath" && hooksPattern.MatchString(args[3]) && !strings.Contains(args[3], "..")
	}
	return false
}

func pathsSafe(paths []string) bool {
	for _, p := range paths {
		if ValidatePath(p) != nil {
			return false
		}
	}
	return true
}

// ValidatePath accepts a clean repository-relative path and nothing that git
// could read as an option, a pathspec magic prefix or a ref-like escape.
func ValidatePath(p string) error {
	bad := p == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, ":") || strings.HasPrefix(p, "-") ||
		strings.ContainsAny(p, "\x00\n\r\\,") || strings.HasSuffix(p, "/")
	if !bad {
		for _, part := range strings.Split(p, "/") {
			if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".git") {
				bad = true
			}
		}
	}
	if bad {
		return domain.Fail("INPUT", "无效的仓库相对路径: "+p)
	}
	return nil
}

// Mutate runs one allow-listed write. The user's own git configuration stays
// in force (identity, signing, hooks); only variables that redirect the
// repository are removed.
func (r *Runner) Mutate(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	if !mutationAllowed(args) {
		return nil, domain.Fail("CAPABILITY", "不支持的 Git 写入")
	}
	ctx, cancel := context.WithTimeout(ctx, mutateTimeout)
	defer cancel()
	flags := []string{"--no-pager", "--literal-pathspecs", "-c", "core.pager=cat", "-c", "color.ui=false"}
	cmd := exec.CommandContext(ctx, r.path, append(flags, args...)...)
	cmd.Dir = r.Root
	cmd.Env = mutateEnv()
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	stdout := &capBuffer{limit: stderrLimit}
	stderr := &capBuffer{limit: stderrLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, domain.Wrap("CANCELLED", ctx.Err())
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		message := "git " + args[0] + " 失败"
		text := strings.TrimSpace(stderr.String())
		if text == "" {
			text = strings.TrimSpace(stdout.String())
		}
		if text != "" {
			message += ": " + errorLine(text)
		}
		return nil, domain.Fail("EXECUTION", message)
	}
	if err != nil {
		return nil, domain.Wrap("EXECUTION", err)
	}
	// git reports push progress on stderr; keep it as the result text.
	return bytes.TrimSpace(append(stdout.Bytes(), append([]byte("\n"), stderr.Bytes()...)...)), nil
}

var redirectingEnv = []string{
	"GIT_DIR=", "GIT_WORK_TREE=", "GIT_INDEX_FILE=", "GIT_OBJECT_DIRECTORY=", "GIT_ALTERNATE_OBJECT_DIRECTORIES=",
	"GIT_COMMON_DIR=", "GIT_NAMESPACE=", "GIT_PREFIX=", "GIT_SEQUENCE_EDITOR=", "GIT_EDITOR=", "PAGER=",
}

func mutateEnv() []string {
	env := []string{}
next:
	for _, e := range os.Environ() {
		for _, prefix := range redirectingEnv {
			if strings.HasPrefix(e, prefix) {
				continue next
			}
		}
		env = append(env, e)
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "GIT_EDITOR=true", "LC_ALL=C")
}

// capBuffer keeps the first limit bytes and silently drops the rest, so a
// chatty hook can never fail a write that git itself completed.
type capBuffer struct {
	bytes.Buffer
	limit int
}

func (b *capBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// errorLine prefers the line that says why git failed over the banner lines
// (such as "To <url>") that precede it.
func errorLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		for _, marker := range []string{"rejected", "error:", "fatal:", "denied", "failed"} {
			if strings.Contains(line, marker) {
				return strings.TrimSpace(line)
			}
		}
	}
	return firstLine(text)
}
