package scaffold

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// The generation-directory protection is the part of repository-scope-policy.mjs
// the scaffold generators use: a generated project must never overwrite a git
// submodule mount point or be written into an unusable submodule checkout.

const gitlinkMode = "160000"

var (
	stagePattern   = regexp.MustCompile(`(?i)^(\d+)\s+([0-9a-f]+)\s+(\d+)\s+(.+)$`)
	submoduleName  = regexp.MustCompile(`^\[submodule "(.+)"\]$`)
	submodulePath  = regexp.MustCompile(`^path\s*=\s*(.+)$`)
	trailingSlashe = regexp.MustCompile(`/+$`)
)

func normalizeSlash(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return ""
	}
	return trailingSlashe.ReplaceAllString(strings.ReplaceAll(text, `\`, "/"), "")
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func relativeSlash(root, target string) string {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return ""
	}
	return normalizeSlash(rel)
}

func uniqueResolved(items ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range items {
		if item == "" {
			continue
		}
		resolved, err := filepath.Abs(item)
		if err != nil || seen[resolved] {
			continue
		}
		seen[resolved] = true
		out = append(out, resolved)
	}
	return out
}

// collectGitRoots lists the ancestors (starting with start) that hold .git or .gitmodules.
func collectGitRoots(start string) []string {
	if start == "" {
		return nil
	}
	current, err := filepath.Abs(start)
	if err != nil {
		return nil
	}
	var roots []string
	for {
		if pathExists(filepath.Join(current, ".git")) || pathExists(filepath.Join(current, ".gitmodules")) {
			roots = append(roots, current)
		}
		next := filepath.Dir(current)
		if next == current {
			return roots
		}
		current = next
	}
}

func findGitRoot(start string) string {
	if roots := collectGitRoots(start); len(roots) > 0 {
		return roots[0]
	}
	return ""
}

func gitOutput(dir string, args ...string) (string, bool) {
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			command.Env = append(command.Env, entry)
		}
	}
	var stdout bytes.Buffer
	command.Stdout = &stdout
	if err := command.Run(); err != nil {
		return "", false
	}
	return stdout.String(), true
}

func gitModulePaths(repoRoot string) []string {
	data, err := os.ReadFile(filepath.Join(repoRoot, ".gitmodules"))
	if err != nil {
		return nil
	}
	var paths []string
	inModule := false
	for _, raw := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if submoduleName.MatchString(line) {
			inModule = true
			paths = append(paths, "")
			continue
		}
		if !inModule {
			continue
		}
		if match := submodulePath.FindStringSubmatch(line); match != nil {
			paths[len(paths)-1] = normalizeSlash(match[1])
		}
	}
	return paths
}

func gitStageMode(repoRoot, relative string) string {
	out, ok := gitOutput(repoRoot, "ls-files", "--stage", "--", relative)
	if !ok {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimRight(line, "\r"); line == "" {
			continue
		}
		if match := stagePattern.FindStringSubmatch(strings.TrimSpace(line)); match != nil {
			return match[1]
		}
		return ""
	}
	return ""
}

func gitSuperproject(dir string) string {
	out, _ := gitOutput(dir, "rev-parse", "--show-superproject-working-tree")
	return strings.TrimSpace(out)
}

func gitAbbrevRef(dir string) (string, bool) {
	out, ok := gitOutput(dir, "rev-parse", "--abbrev-ref", "HEAD")
	return strings.TrimSpace(out), ok
}

func isEmptyDir(dir string) bool {
	if !pathExists(dir) {
		return true
	}
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) == 0
}

func isGitSubmoduleMount(repoRoot, target string) bool {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	relative := relativeSlash(repoRoot, absolute)
	if relative == "" || strings.HasPrefix(relative, "..") {
		return false
	}
	for _, path := range gitModulePaths(repoRoot) {
		if path == relative {
			return true
		}
	}
	return gitStageMode(repoRoot, relative) == gitlinkMode
}

func isGitSubmoduleMountAnywhere(startOrRoot, target string) bool {
	items := []string{startOrRoot}
	items = append(items, collectGitRoots(target)...)
	items = append(items, collectGitRoots(startOrRoot)...)
	for _, root := range uniqueResolved(items...) {
		if isGitSubmoduleMount(root, target) {
			return true
		}
	}
	return false
}

func inspectCheckoutState(repoRoot, target string) string {
	resolved, err := filepath.Abs(target)
	if err != nil {
		return ""
	}
	relative, inRepo := "", false
	if repoRoot != "" {
		if root, err := filepath.Abs(repoRoot); err == nil {
			relative = relativeSlash(root, resolved)
		}
		inRepo = relative != "" && !strings.HasPrefix(relative, "..")
	}
	staged, listed := "", false
	if inRepo {
		staged = gitStageMode(repoRoot, relative)
		for _, path := range gitModulePaths(repoRoot) {
			listed = listed || path == relative
		}
	}
	hasGit := pathExists(filepath.Join(resolved, ".git"))
	superproject := ""
	if hasGit {
		superproject = gitSuperproject(resolved)
	}
	mount := staged == gitlinkMode || listed || superproject != ""
	switch {
	case mount && !hasGit:
		if isEmptyDir(resolved) {
			return "empty-gitlink"
		}
		return "uninitialized"
	case mount:
		if ref, _ := gitAbbrevRef(resolved); ref == "HEAD" {
			return "detached-head"
		}
		return "attached-branch"
	}
	return ""
}

type submoduleWorktree struct{ checkout string }

func findEnclosingSubmoduleWorktree(repoRoot, start string) *submoduleWorktree {
	current, err := filepath.Abs(start)
	if err != nil {
		return nil
	}
	stop := ""
	if repoRoot != "" {
		stop, _ = filepath.Abs(repoRoot)
	}
	for {
		for _, root := range uniqueResolved(append([]string{repoRoot}, collectGitRoots(current)...)...) {
			if root == current {
				continue
			}
			if isGitSubmoduleMount(root, current) {
				return &submoduleWorktree{checkout: inspectCheckoutState(root, current)}
			}
		}
		if pathExists(filepath.Join(current, ".git")) {
			if superproject := gitSuperproject(current); superproject != "" {
				checkout := inspectCheckoutState(superproject, current)
				if checkout == "" {
					if ref, _ := gitAbbrevRef(current); ref == "HEAD" {
						checkout = "detached-head"
					} else {
						checkout = "attached-branch"
					}
				}
				return &submoduleWorktree{checkout: checkout}
			}
		}
		if stop != "" && current == stop {
			return nil
		}
		next := filepath.Dir(current)
		if next == current {
			return nil
		}
		current = next
	}
}

func overlayMountViolation(repoRoot, target string, force bool) string {
	if !isGitSubmoduleMountAnywhere(repoRoot, target) {
		return ""
	}
	if force {
		return "--force 不得把 git-submodule 挂载点当成普通目录覆盖；gitlink 不得由脚手架覆盖"
	}
	return "git-submodule gitlink 不得由脚手架覆盖；先 git submodule update --init，在子仓附加分支的工作树内生成，或改用 external-repository / harness-apps"
}

func unsafeSubmoduleWorktreeViolation(repoRoot, target string) string {
	enclosing := findEnclosingSubmoduleWorktree(repoRoot, target)
	if enclosing == nil {
		return ""
	}
	switch enclosing.checkout {
	case "empty-gitlink", "uninitialized":
		return "空 gitlink 不得当成普通目录写入；gitlink 不得由脚手架覆盖"
	case "detached-head":
		return "detached HEAD 不得当成普通目录写入；gitlink 不得由脚手架覆盖"
	}
	return ""
}

func regularDirectoryMisreadViolation(repoRoot, target string, force bool) string {
	if repoRoot == "" || target == "" {
		return ""
	}
	switch inspectCheckoutState(repoRoot, target) {
	case "empty-gitlink", "uninitialized":
		return "empty gitlink must not be treated as a regular directory"
	case "detached-head":
		return "detached HEAD must not be treated as a regular directory"
	}
	if force && isGitSubmoduleMountAnywhere(repoRoot, target) {
		return "--force must not overlay a git-submodule mount as a regular directory"
	}
	return ""
}

// gitSubmoduleScaffoldViolation is gitSubmoduleScaffoldViolation(repositoryRoot,
// outputDir, projectName) of repository-scope-policy.mjs; it returns "" when the
// output location is safe.
func gitSubmoduleScaffoldViolation(repositoryRoot, outputDir, projectName string) string {
	output, _ := filepath.Abs(outputDir)
	project := filepath.Join(output, projectName)
	gitRoot := findGitRoot(project)
	if gitRoot == "" {
		gitRoot = findGitRoot(output)
	}
	if gitRoot == "" {
		gitRoot = repositoryRoot
	}
	for _, violation := range []func() string{
		func() string { return overlayMountViolation(gitRoot, project, false) },
		func() string { return unsafeSubmoduleWorktreeViolation(gitRoot, project) },
		func() string { return unsafeSubmoduleWorktreeViolation(gitRoot, output) },
		func() string { return regularDirectoryMisreadViolation(gitRoot, project, false) },
	} {
		if message := violation(); message != "" {
			return message
		}
	}
	return ""
}
