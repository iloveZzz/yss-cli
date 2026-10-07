package project

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func workDescribe(root, ref string) (domain.Descriptor, error) {
	p, e := safefs.Path(root, ref)
	if e != nil {
		return domain.Descriptor{}, e
	}
	st, e := os.Lstat(p)
	if e == nil && st.IsDir() {
		return domain.Descriptor{Type: "directory", Mode: uint32(st.Mode().Perm())}, nil
	}
	return safefs.Describe(root, ref)
}
func workSourceDirectories(root, source string) (map[string]uint32, error) {
	dirs := map[string]uint32{}
	p, e := safefs.Path(root, source)
	if e != nil {
		return nil, e
	}
	if _, e = os.Lstat(p); os.IsNotExist(e) {
		return dirs, nil
	} else if e != nil {
		return nil, e
	}
	e = filepath.WalkDir(p, func(file string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		ref, e := filepath.Rel(root, file)
		if e != nil {
			return e
		}
		ref = filepath.ToSlash(ref)
		if _, e = safefs.Path(root, ref); e != nil {
			return e
		}
		if entry.IsDir() {
			if _, e := os.Lstat(filepath.Join(file, ".git")); e == nil {
				return domain.Fail("WORK_LAYOUT_PATH", "工作包包含嵌套 Git 仓库: "+ref)
			}
			st, e := entry.Info()
			if e != nil {
				return e
			}
			if st.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
				return domain.Fail("WORK_LAYOUT_PERMISSION", "工作包目录包含不支持的特殊权限: "+ref)
			}
			dirs[ref] = uint32(st.Mode().Perm())
		}
		return nil
	})
	return dirs, e
}
func workIgnoredTarget(root, target string) (bool, error) {
	if _, e := os.Lstat(filepath.Join(root, ".git")); os.IsNotExist(e) {
		return false, nil
	}
	cmd := exec.Command("git", "-C", root, "check-ignore", "--no-index", "-v", "--", target)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, e := cmd.Output()
	if e != nil {
		if exit, ok := e.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			return false, nil
		}
		return false, domain.Wrap("GIT_IGNORE", e)
	}
	// Verbose check-ignore can also report an explicit negation (exit 0).
	rule, _, ok := bytes.Cut(out, []byte("\t"))
	if !ok {
		return false, domain.Fail("GIT_IGNORE", "Git 忽略诊断格式无效")
	}
	fields := strings.SplitN(string(rule), ":", 3)
	return len(fields) == 3 && !strings.HasPrefix(fields[2], "!"), nil
}
