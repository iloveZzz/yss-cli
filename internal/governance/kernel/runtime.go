package kernel

import (
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var registeredPathPattern = regexp.MustCompile(`["']?(?:local_worktree|project_root|repository_root|local_path|root_path)["']?\s*:\s*("(?:\\.|[^"\\])*"|'(?:''|[^'])*'|[^\r\n"'#,}]+)|\|\s*(?:local_worktree|project_root|repository_root|local_path|root_path)\s*\|\s*([^|\r\n]+)`)

var gitlinkPattern = regexp.MustCompile(`(?m)^\s*path\s*=\s*(.+)$`)

func protectedRoots(root string) ([]string, error) {
	roots := []string{root}
	read := func(p string) error {
		b, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, match := range registeredPathPattern.FindAllStringSubmatch(string(b), -1) {
			s := strings.TrimSpace(match[1])
			if s == "" {
				s = strings.TrimSpace(match[2])
			}
			if strings.HasPrefix(s, "\"") {
				if err = json.Unmarshal([]byte(s), &s); err != nil {
					return domain.Fail("PATH", "已登记仓库路径无法解析")
				}
			} else if strings.HasPrefix(s, "'") {
				s = strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(s, "'"), "'"), "''", "'")
			} else {
				s = strings.Trim(s, "`")
			}
			if s == "" {
				continue
			}
			if !filepath.IsAbs(s) {
				s = filepath.Join(root, s)
			}
			real, err := filepath.EvalSymlinks(s)
			if err == nil {
				roots = append(roots, real)
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		return nil
	}
	scanRoots, err := worklayout.ReadScanRoots(root)
	if err != nil {
		return nil, err
	}
	for _, ref := range append(scanRoots, "docs", ".template-spec/implementation", ".template-spec/projects", ".template-spec/project") {
		p, err := safefs.Path(root, ref)
		if err != nil {
			return nil, err
		}
		err = filepath.WalkDir(p, func(p string, d os.DirEntry, e error) error {
			if os.IsNotExist(e) {
				return nil
			}
			if e != nil {
				return e
			}
			if d.Type()&os.ModeSymlink != 0 {
				return domain.Fail("PATH", "登记扫描拒绝符号链接: "+p)
			}
			if d.IsDir() {
				if d.Name() == ".git" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(p)
			if ext == ".md" || ext == ".json" || ext == ".yaml" || ext == ".yml" {
				return read(p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	p, err := safefs.Path(root, ".gitmodules")
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, match := range gitlinkPattern.FindAllStringSubmatch(string(b), -1) {
		ref := strings.TrimSpace(match[1])
		p, err := safefs.Path(root, ref)
		if err != nil {
			return nil, err
		}
		if real, err := filepath.EvalSymlinks(p); err == nil {
			roots = append(roots, real)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return roots, nil
}

func RuntimeLocation(root, home string) (string, error) {
	if home == "" {
		home = os.Getenv("YSS_RUNTIME_HOME")
	}
	if home == "" {
		base, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(base, ".yss-harness", "runtime")
	}
	home, err := ExternalPath(root, home)
	if err != nil {
		return "", err
	}
	protected, err := protectedRoots(root)
	if err != nil {
		return "", err
	}
	for _, p := range protected {
		if inside(p, home) {
			return "", domain.Fail("PATH", "运行存储不得进入已登记仓库: "+p)
		}
	}
	dir := filepath.Join(home, safefs.Digest([]byte(root)))
	if _, err = safefs.Path(dir, "runtime.sqlite"); err != nil {
		return "", err
	}
	return dir, nil
}
