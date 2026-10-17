//go:build !yss_nobundle

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// 端到端：MCP 工具与同参数命令行得到同一 envelope，且根目录固定为服务启动时的 --root。
func TestMCPToolMatchesCommandLine(t *testing.T) {
	project := checkProject(t)
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"explain","arguments":{"term":"gate.plan-approved"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"lifecycle_query","arguments":{"id":"work-unit.entry-triage"}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := ServeMCP(context.Background(), project, strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("应有 3 条响应: %s", out.String())
	}
	for i, args := range [][]string{{"explain", "gate.plan-approved"}, {"lifecycle", "query", "--view", "agent", "--id", "work-unit.entry-triage"}} {
		var resp struct {
			Result struct {
				IsError bool `json:"isError"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(lines[i+1]), &resp); err != nil {
			t.Fatal(err)
		}
		direct, exit := Invoke(context.Background(), append(args, "--root", project))
		if resp.Result.IsError || exit != 0 || resp.Result.Content[0].Text != string(direct) {
			t.Errorf("%v: MCP 结果与命令行不一致或失败（exit=%d）", args, exit)
		}
	}
}
