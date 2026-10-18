package contracts

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

// TaskBaselineSource uses the existing engineering source convention: canonical
// {base_commit, roots, files:[{path, base_blob, sha256}]} over the real Git tree.
// The caller must register an external repository from its verified contract first.
func TaskBaselineSource(s *kernel.SemanticSession, root, commit string, scope []string) (map[string]any, error) {
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(commit) || !kernel.ApUnique(scope) || (!filepath.IsAbs(root) || filepath.Clean(root) != root) && !s.IsTaskSnapshotRoot(root) {
		return nil, s.Reject("TASK_BASELINE", "基线提交、工程根或范围不明确")
	}
	roots := append([]string{}, scope...)
	for i, ref := range roots {
		roots[i] = strings.TrimSuffix(ref, "/")
		if !kernel.ContractPath(roots[i]) {
			return nil, s.Reject("TASK_BASELINE", "基线范围必须为明确相对路径")
		}
		if _, err := s.TaskPath(root, roots[i]); err != nil {
			return nil, err
		}
	}
	if !kernel.ApUnique(roots) {
		return nil, s.Reject("TASK_BASELINE", "基线范围规范化后重复")
	}
	sort.Strings(roots)
	for _, q := range []struct {
		args []string
		want string
	}{{[]string{"rev-parse", "--show-toplevel"}, root}, {[]string{"rev-parse", commit + "^{commit}"}, commit}} {
		b, err := s.Git(root, q.args...)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(string(b)) != q.want {
			return nil, s.Reject("TASK_BASELINE", "Git根或提交不匹配")
		}
	}
	tree, err := s.Git(root, "ls-tree", "-r", "-z", commit)
	if err != nil {
		return nil, err
	}
	files := []any{}
	for _, row := range strings.Split(string(tree), "\x00") {
		parts := strings.SplitN(row, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		ref := parts[1]
		inside := false
		for _, r := range roots {
			inside = inside || ContractWithin(ref, r)
		}
		if !inside {
			continue
		}
		meta := strings.Fields(parts[0])
		if len(meta) != 3 || !kernel.SemHas([]string{"100644", "100755"}, meta[0]) {
			return nil, s.Reject("TASK_BASELINE", "基线范围含symlink/gitlink")
		}
		if _, err = s.TaskPath(root, ref); err != nil {
			return nil, err
		}
		raw, err := s.Git(root, "cat-file", "blob", meta[2])
		if err != nil {
			return nil, err
		}
		files = append(files, map[string]any{"path": ref, "base_blob": meta[2], "sha256": "sha256:" + safefs.Digest(raw)})
	}
	return map[string]any{"base_commit": commit, "roots": roots, "files": files}, nil
}

func validateTaskBaseline(s *kernel.SemanticSession, task map[string]any) error {
	baseline := kernel.ApMap(task["baseline"])
	source, err := TaskBaselineSource(s, kernel.Text(baseline["repository_root"]), kernel.Text(baseline["commit"]), kernel.ApStrings(task["allowed_write_paths"]))
	if err != nil {
		return err
	}
	if strings.TrimPrefix(ContractDigest(source), "sha256:") != baseline["digest"] {
		return s.Reject("TASK_BASELINE", fmt.Sprintf("基线摘要未绑定实际提交与范围: %s", baseline["repository_root"]))
	}
	return nil
}
