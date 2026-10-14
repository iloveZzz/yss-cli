package bundle

import (
	"context"
	"fmt"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

// kernelScope 是「内核 + 覆盖层」组合里由内核（同一仓库同一提交）提供的目录。
// 覆盖层目录之外的内容只能来自覆盖层；两边都有同一路径时覆盖层优先。
var kernelScope = []string{".agents/skills", "scripts", ".template-spec/process", ".template-spec/templates"}

func inKernelScope(ref string) bool {
	for _, prefix := range kernelScope {
		if beneath(ref, prefix) {
			return true
		}
	}
	return false
}

// ValidateOverlayPath applies the shared portable-path and kernel-boundary rules.
func ValidateOverlayPath(overlayPath string) error {
	if e := safefs.ValidateRef(overlayPath); e != nil {
		return e
	}
	if strings.HasSuffix(overlayPath, "/") || inKernelScope(overlayPath) {
		return fmt.Errorf("overlayPath 不合法: %s", overlayPath)
	}
	for _, prefix := range kernelScope {
		if beneath(prefix, overlayPath) {
			return fmt.Errorf("overlayPath 不能包含内核目录: %s", overlayPath)
		}
	}
	return nil
}

// readOverlaySource 按 Profile 的分发清单，从同一个提交里读取内核目录与覆盖层目录并合并。
// 清单同时约束两边：被排除的路径不会被读取或校验，所以覆盖层不需要为内核里不属于该
// Profile 的文件另写删除标记，只要在清单的 excludePaths 里列出即可。
func readOverlaySource(ctx context.Context, root, commit string, overlayPaths []string, manifest map[string]any) (map[string]sourceFile, error) {
	seen := map[string]bool{}
	for _, overlayPath := range overlayPaths {
		if e := ValidateOverlayPath(overlayPath); e != nil {
			return nil, e
		}
		if seen[overlayPath] {
			return nil, fmt.Errorf("overlayPaths 重复: %s", overlayPath)
		}
		seen[overlayPath] = true
	}
	include := func(ref string) bool { return includedByManifest(ref, manifest, false) }
	merged, e := readGitFilesUnder(ctx, root, commit, "", func(ref string) bool { return inKernelScope(ref) && include(ref) })
	if e != nil {
		return nil, e
	}
	for _, overlayPath := range overlayPaths {
		overlay, e := readGitFilesUnder(ctx, root, commit, overlayPath, include)
		if e != nil {
			return nil, e
		}
		for ref, f := range overlay {
			merged[ref] = f
		}
	}
	if _, exists := merged[schema.OrchestrationRef]; exists {
		_, refs, _, err := schema.LoadOrchestration(func(ref string) ([]byte, error) {
			file, ok := merged[ref]
			if !ok {
				return nil, fmt.Errorf("合同部件缺失: %s", ref)
			}
			return file.data, nil
		}, schema.OrchestrationRef)
		if err != nil {
			return nil, err
		}
		retained := map[string]bool{}
		for _, ref := range refs {
			retained[ref] = true
		}
		for ref := range merged {
			if strings.HasPrefix(ref, ".template-spec/process/orchestration/") && !retained[ref] {
				delete(merged, ref)
			}
		}
	}
	if e = expandProjectionLinks(merged); e != nil {
		return nil, e
	}
	return merged, nil
}
