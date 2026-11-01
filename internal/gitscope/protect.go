// Package gitscope protects repository boundaries without requiring a branch.
package gitscope

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

// Check permits writes within the selected root, never through a nested checkout.
// An empty ref checks the root itself before it or transaction controls are created.
func Check(root, ref string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	for _, part := range strings.Split(filepath.ToSlash(root), "/") {
		if strings.EqualFold(part, ".git") {
			return domain.Fail("PROTECTED", "安装根不能位于 Git 内部目录: "+root)
		}
	}
	target := root
	if ref == "" {
		_, err = safefs.Path(root, ".git-boundary-check")
	} else {
		target, err = safefs.Path(root, ref)
	}
	if err != nil {
		return err
	}
	// Inspect directories without following .git files or directory links.
	for cursor := target; cursor != root; cursor = filepath.Dir(cursor) {
		st, e := os.Lstat(cursor)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if e == nil && st.IsDir() {
			marked, e := exists(filepath.Join(cursor, ".git"))
			if e != nil {
				return e
			}
			if marked {
				return domain.Fail("PROTECTED", "目标位于独立仓库或子模块: "+target)
			}
		}
	}
	for cursor := root; ; cursor = filepath.Dir(cursor) {
		marked, e := exists(filepath.Join(cursor, ".git"))
		if e != nil {
			return e
		}
		modules, e := exists(filepath.Join(cursor, ".gitmodules"))
		if e != nil {
			return e
		}
		var mounts []string
		if marked {
			top, e := git(cursor, "rev-parse", "--show-toplevel")
			if e != nil {
				return readError(cursor, e)
			}
			if filepath.Clean(strings.TrimSuffix(string(top), "\n")) != cursor {
				return readError(cursor, fmt.Errorf("Git 根目录不匹配"))
			}
			index, e := git(cursor, "ls-files", "--stage", "-z")
			if e != nil {
				return readError(cursor, e)
			}
			for _, record := range strings.Split(string(index), "\x00") {
				if record == "" {
					continue
				}
				parts := strings.SplitN(record, "\t", 2)
				if len(parts) != 2 {
					return readError(cursor, fmt.Errorf("Git index 记录无效"))
				}
				fields := strings.Fields(parts[0])
				if len(fields) != 3 {
					return readError(cursor, fmt.Errorf("Git index 字段无效"))
				}
				if fields[0] == "160000" {
					mounts = append(mounts, parts[1])
				}
			}
		}
		if modules {
			file, e := safefs.Path(cursor, ".gitmodules")
			if e != nil {
				return readError(cursor, e)
			}
			data, e := git(cursor, "config", "--null", "--file", file, "--get-regexp", `^submodule\..*\.path$`)
			var exit *exec.ExitError
			if e != nil && !(errors.As(e, &exit) && exit.ExitCode() == 1) {
				return readError(cursor, e)
			}
			for _, record := range strings.Split(string(data), "\x00") {
				if record == "" {
					continue
				}
				parts := strings.SplitN(record, "\n", 2)
				if len(parts) != 2 {
					return readError(cursor, fmt.Errorf(".gitmodules 路径记录无效"))
				}
				mounts = append(mounts, parts[1])
			}
		}
		for _, mount := range mounts {
			if e := safefs.ValidateRef(mount); e != nil {
				return readError(cursor, e)
			}
			boundary := filepath.Join(cursor, filepath.FromSlash(mount))
			relative, e := filepath.Rel(cursor, target)
			if e != nil {
				return e
			}
			// Fold components conservatively even before a mount exists on disk.
			targetParts := strings.Split(filepath.ToSlash(relative), "/")
			mountParts := strings.Split(mount, "/")
			inside := len(targetParts) >= len(mountParts)
			for i, part := range mountParts {
				if !inside || !strings.EqualFold(targetParts[i], part) {
					inside = false
					break
				}
			}
			if inside {
				return domain.Fail("PROTECTED", "目标位于子模块挂载点: "+boundary)
			}
		}
		// The nearest initialized checkout is the user's selected Git scope.
		if marked || filepath.Dir(cursor) == cursor {
			break
		}
	}
	return nil
}
func exists(p string) (bool, error) {
	_, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}
func git(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-c", "core.fsmonitor=false", "-C", root}, args...)...)
	cmd.Env = []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(entry), "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0")
	return cmd.Output()
}
func readError(root string, err error) error {
	return domain.Wrap("GIT_READ", fmt.Errorf("无法可靠读取 Git 边界 %s: %w", root, err))
}
