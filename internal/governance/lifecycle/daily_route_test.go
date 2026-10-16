package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

const fullSHA = "0123456789abcdef0123456789abcdef01234567"

func routeArgs(overrides map[string]string) map[string]string {
	args := map[string]string{"root": "", "task": ".work/t/task.md", "implementation-root": "", "base": fullSHA}
	for k, v := range overrides {
		args[k] = v
	}
	return args
}

// 这些前置检查发生在读取项目身份与政策之前，所以只需要一个空目录。
func TestRouteRejectsIncompleteInputBeforeReadingProject(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for name, tc := range map[string]struct {
		action string
		args   map[string]string
		code   string
	}{
		"缺少任务":    {"route", routeArgs(map[string]string{"root": root, "implementation-root": root, "task": ""}), "ARGUMENT"},
		"缺少实现仓":   {"route", routeArgs(map[string]string{"root": root}), "ARGUMENT"},
		"短 SHA":   {"route", routeArgs(map[string]string{"root": root, "implementation-root": root, "base": "0123456"}), "ARGUMENT"},
		"任务非记录文件": {"route", routeArgs(map[string]string{"root": root, "implementation-root": root, "task": "FEAT-1"}), "NEEDS_INFO"},
		"未知动作":    {"promote", routeArgs(map[string]string{"root": root, "implementation-root": root}), "UNPORTED"},
	} {
		result, err := Run(context.Background(), tc.action, root, tc.args)
		var de *domain.Error
		if !errors.As(err, &de) || de.Code != tc.code || de.Exit != 2 {
			t.Errorf("%s: 期望不可判定错误 %s（退出码 2），得到 %v", name, tc.code, err)
			continue
		}
		report, ok := result.(*DailyReport)
		if !ok || report.DeliveryPath != "needs-info" {
			t.Errorf("%s: 缺事实时路径必须是 needs-info，不得猜成 daily: %+v", name, result)
		}
		if report != nil && report.ApprovalCreated {
			t.Errorf("%s: 路由不得创建批准", name)
		}
	}
}

func TestRouteAgentViewOnlyForRoute(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if _, err := Run(context.Background(), "verify-daily", root, routeArgs(map[string]string{"root": root, "view": "agent"})); err == nil || !strings.Contains(err.Error(), "agent") {
		t.Errorf("verify-daily 不支持 agent 视图: %v", err)
	}
	result, err := Run(context.Background(), "route", root, routeArgs(map[string]string{"root": root, "view": "agent"}))
	view, ok := result.(map[string]any)
	if err == nil || !ok || view["view"] != "agent" || view["route"] != "needs-info" {
		t.Errorf("agent 视图在失败时也应给出 needs-info 路由: %v, %v", result, err)
	}
}
