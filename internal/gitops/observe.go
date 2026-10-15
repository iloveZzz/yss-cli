package gitops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Branch is the porcelain v2 branch header.
type Branch struct {
	Head     string `json:"head"`
	Detached bool   `json:"detached"`
	Commit   string `json:"commit,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
}

// Entry is one changed path; Index and Worktree are the XY status letters.
type Entry struct {
	Path      string `json:"path"`
	Orig      string `json:"origPath,omitempty"`
	Kind      string `json:"kind"`
	Index     string `json:"index,omitempty"`
	Worktree  string `json:"worktree,omitempty"`
	Submodule bool   `json:"submodule,omitempty"`
}

type Status struct {
	Branch  Branch  `json:"branch"`
	Entries []Entry `json:"entries"`
}

// ReadStatus parses `status --porcelain=v2 -z`, which is stable across git
// versions and locales and keeps paths byte-exact.
func (r *Runner) ReadStatus(ctx context.Context) (Status, error) {
	out, err := r.Query(ctx, "status", "--porcelain=v2", "-z", "--branch", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		return Status{}, err
	}
	s := Status{Entries: []Entry{}}
	tokens := strings.Split(string(out), "\x00")
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if t == "" {
			continue
		}
		switch t[0] {
		case '#':
			parseBranchHeader(&s.Branch, t)
		case '?':
			s.Entries = append(s.Entries, Entry{Path: t[2:], Kind: "untracked"})
		case '1', '2', 'u':
			fields := strings.SplitN(t, " ", map[byte]int{'1': 9, '2': 10, 'u': 11}[t[0]])
			if len(fields) < 3 {
				continue
			}
			e := Entry{Path: fields[len(fields)-1], Index: dot(fields[1][:1]), Worktree: dot(fields[1][1:]), Submodule: strings.HasPrefix(fields[2], "S")}
			switch t[0] {
			case '1':
				e.Kind = "changed"
			case '2':
				e.Kind = "renamed"
				if i+1 < len(tokens) {
					i++
					e.Orig = tokens[i]
				}
			case 'u':
				e.Kind = "unmerged"
			}
			s.Entries = append(s.Entries, e)
		}
	}
	return s, nil
}

func dot(s string) string {
	if s == "." {
		return ""
	}
	return s
}

func parseBranchHeader(b *Branch, line string) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return
	}
	switch fields[1] {
	case "branch.oid":
		if fields[2] != "(initial)" {
			b.Commit = fields[2]
		}
	case "branch.head":
		b.Head = fields[2]
		b.Detached = fields[2] == "(detached)"
	case "branch.upstream":
		b.Upstream = fields[2]
	case "branch.ab":
		if len(fields) == 4 {
			b.Ahead, _ = strconv.Atoi(strings.TrimPrefix(fields[2], "+"))
			b.Behind, _ = strconv.Atoi(strings.TrimPrefix(fields[3], "-"))
		}
	}
}

// Operations reports merges, rebases and similar sequences left in progress.
func (r *Runner) Operations(ctx context.Context) ([]string, error) {
	markers := []struct{ path, name string }{
		{"MERGE_HEAD", "merge"}, {"rebase-merge", "rebase"}, {"rebase-apply", "rebase"},
		{"CHERRY_PICK_HEAD", "cherry-pick"}, {"REVERT_HEAD", "revert"}, {"BISECT_LOG", "bisect"},
	}
	args := []string{"rev-parse"}
	for _, m := range markers {
		args = append(args, "--git-path", m.path)
	}
	out, err := r.Query(ctx, args...)
	if err != nil {
		return nil, err
	}
	paths := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	found := []string{}
	seen := map[string]bool{}
	for i, p := range paths {
		if i >= len(markers) || p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(r.Root, p)
		}
		if _, e := os.Lstat(p); e == nil && !seen[markers[i].name] {
			seen[markers[i].name] = true
			found = append(found, markers[i].name)
		} else if e != nil && !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
	}
	return found, nil
}

// ConfigValue reads one config key; a missing key is the empty string.
func (r *Runner) ConfigValue(ctx context.Context, key string) (string, error) {
	out, code, err := r.Try(ctx, "config", "--get", key)
	var text *stderrText
	if err != nil && !errors.As(err, &text) {
		return "", err
	}
	if code != 0 {
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

// Submodule is one line of `git submodule status --recursive`.
type Submodule struct {
	Path        string `json:"path"`
	Commit      string `json:"commit"`
	State       string `json:"state"`
	LocalEntry  string `json:"localEntry,omitempty"`
	Initialized bool   `json:"initialized"`
}

func (r *Runner) Submodules(ctx context.Context) ([]Submodule, error) {
	out := []Submodule{}
	if _, err := os.Lstat(filepath.Join(r.Root, ".gitmodules")); errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	data, err := r.Query(ctx, "submodule", "status", "--recursive")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if len(line) < 2 {
			continue
		}
		fields := strings.Fields(line[1:])
		if len(fields) < 2 {
			continue
		}
		s := Submodule{Commit: fields[0], Path: fields[1], Initialized: line[0] != '-'}
		s.State = map[byte]string{' ': "current", '-': "uninitialized", '+': "commit-mismatch", 'U': "conflict"}[line[0]]
		for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
			if st, e := os.Lstat(filepath.Join(r.Root, filepath.FromSlash(s.Path), name)); e == nil && st.Mode().IsRegular() {
				s.LocalEntry = name
				break
			}
		}
		out = append(out, s)
	}
	return out, nil
}
