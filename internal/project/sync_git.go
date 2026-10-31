package project

import (
	"context"
	"fmt"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/gitops"
)

// syncGitHead requires a committed, clean Git worktree before project sync.
func syncGitHead(ctx context.Context, root string) (string, error) {
	runner, err := gitops.NewRunner(root)
	if err != nil {
		return "", err
	}
	status, err := runner.ReadStatus(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return "", domain.Wrap("CANCELLED", ctx.Err())
		}
		return "", domain.Fail("ROOT", "项目同步需要 Git 工作区")
	}
	if status.Branch.Commit == "" {
		return "", domain.Fail("BASELINE", "项目同步需要至少一个 Git 提交")
	}
	if len(status.Entries) != 0 {
		return "", domain.Fail("CONFLICT", "项目同步需要干净的 Git 工作区")
	}
	return status.Branch.Commit, nil
}

func syncCheckIgnored(ctx context.Context, root string, refs []string) error {
	runner, err := gitops.NewRunner(root)
	if err != nil {
		return err
	}
	ignored, err := runner.IgnoredPaths(ctx, refs)
	if err != nil {
		return err
	}
	if len(ignored) > 0 {
		return domain.Fail("CONFLICT", fmt.Sprintf("同步目标被 Git 忽略，拒绝覆盖: %q", ignored))
	}
	return nil
}
