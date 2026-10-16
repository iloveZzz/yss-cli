package governance

import (
	"context"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/governance/reading"
)

// readingRun 提供 yss reading 的只读命令。会话与语义校验使用同一套权威加载，
// 因此输入、事务与 Context 的前置拒绝和 reading-transition 保持一致。
func readingRun(ctx context.Context, action, root string, args map[string]string) (any, error) {
	s := kernel.NewSemanticSession(ctx, root, args)
	if err := s.Guard(); err != nil {
		return nil, err
	}
	if err := s.Authorities(); err != nil {
		return nil, err
	}
	if _, err := s.ContextContract(); err != nil {
		return nil, err
	}
	switch action {
	case "check-views":
		ref := args["checkpoint"]
		if ref == "" {
			return nil, s.Unavailable("ARGUMENT", "check-views 需要 --checkpoint")
		}
		report, err := reading.CheckViews(s, ref, args["required"] == "true")
		if final := s.Finish(); final != nil {
			return nil, final
		}
		return report, err
	case "affected":
		ref, kind := args["asset"], args["kind"]
		if ref == "" || kind == "" {
			return nil, s.Unavailable("ARGUMENT", "affected 需要 --asset 与 --kind")
		}
		checkpoints, err := reading.AffectedCheckpoints(s, ref, kind)
		if err != nil {
			return nil, err
		}
		if final := s.Finish(); final != nil {
			return nil, final
		}
		return map[string]any{"asset_ref": ref, "kind": kind, "checkpoints": checkpoints, "read_only": true}, nil
	}
	return nil, s.Unavailable("ARGUMENT", "未知 reading 动作: "+action)
}
