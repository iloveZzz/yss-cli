package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
)

type call struct{ args []string }

func serve(t *testing.T, invoke Invoke, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	s := &Server{Name: "yss", Version: "test", Invoke: invoke}
	if err := s.Serve(context.Background(), io.NopCloser(strings.NewReader(strings.Join(lines, "\n")+"\n")), &out); err != nil {
		t.Fatal(err)
	}
	var responses []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("响应不是 JSON: %s", line)
		}
		responses = append(responses, m)
	}
	return responses
}

func TestProtocolHandshakeAndErrors(t *testing.T) {
	r := serve(t, nil,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
		`not json`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/list"}`,
	)
	if len(r) != 5 {
		t.Fatalf("通知不应回复，其余各一条: %v", r)
	}
	if v := r[0]["result"].(map[string]any)["protocolVersion"]; v != "2024-11-05" {
		t.Errorf("受支持的版本应原样回显: %v", v)
	}
	if v := r[1]["result"].(map[string]any)["protocolVersion"]; v != SupportedProtocolVersions[0] {
		t.Errorf("不支持的版本回最新: %v", v)
	}
	for i, code := range map[int]float64{3: -32700, 4: -32601} {
		if got := r[i]["error"].(map[string]any)["code"]; got != code {
			t.Errorf("响应 %d 错误码 %v，期望 %v", i, got, code)
		}
	}
}

func TestToolsAreReadOnlyAndStrict(t *testing.T) {
	r := serve(t, nil, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	for _, raw := range r[0]["result"].(map[string]any)["tools"].([]any) {
		tool := raw.(map[string]any)
		if tool["annotations"].(map[string]any)["readOnlyHint"] != true {
			t.Errorf("%v 必须声明只读", tool["name"])
		}
		if tool["inputSchema"].(map[string]any)["additionalProperties"] != false {
			t.Errorf("%v 必须拒绝额外参数", tool["name"])
		}
	}
	for _, name := range []string{"init", "sync", "git", "stage_apply", "project_ci"} {
		if _, ok := toolByName(name); ok {
			t.Errorf("不得暴露写入类工具 %s", name)
		}
	}
}

func TestArgvConversion(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
		want []string
		bad  bool
	}{
		{"lifecycle_route", map[string]any{"task": "t.md", "implementation_root": "/r", "base": "HEAD"}, []string{"lifecycle", "route", "--view", "agent", "--task", "t.md", "--implementation-root", "/r", "--base", "HEAD"}, false},
		{"check", map[string]any{"files": []any{"a.yaml", "b.json"}, "checkpoint": "cp.json"}, []string{"check", "--checkpoint", "cp.json", "a.yaml", "b.json"}, false},
		{"check", map[string]any{"changed": true}, []string{"check", "--changed"}, false},
		{"explain", map[string]any{"term": "plan-approved"}, []string{"explain", "plan-approved"}, false},
		{"explain", map[string]any{"term": "--root=/etc"}, nil, true},
		{"check", map[string]any{"files": []any{"-x"}}, nil, true},
		{"contract_view", map[string]any{"file": "s.yaml", "view": "secret"}, nil, true},
		{"verify_gate", map[string]any{"gate": "plan-approved"}, nil, true},
		{"lifecycle_query", map[string]any{"root": "/elsewhere"}, nil, true},
		{"check", map[string]any{"changed": "yes"}, nil, true},
	}
	for _, c := range cases {
		tool, _ := toolByName(c.tool)
		got, err := tool.argv(c.args)
		if c.bad != (err != nil) || !c.bad && !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %v: got %v err %v", c.tool, c.args, got, err)
		}
	}
}

func TestToolCallPassesEnvelopeAndExit(t *testing.T) {
	var calls []call
	invoke := func(_ context.Context, args []string) (json.RawMessage, int) {
		calls = append(calls, call{args})
		return json.RawMessage(`{"status":"error","code":"CHECK_FAILED","result":{"status":"failed"}}`), 1
	}
	r := serve(t, invoke,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"check","arguments":{"changed":true}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"explain","arguments":{"term":"-x"}}}`,
	)
	result := r[0]["result"].(map[string]any)
	if result["isError"] != true || result["structuredContent"].(map[string]any)["code"] != "CHECK_FAILED" {
		t.Errorf("非零退出须带 envelope 并标记 isError: %v", result)
	}
	if r[1]["result"].(map[string]any)["isError"] != true || len(calls) != 1 {
		t.Errorf("非法参数不得调用 yss: calls=%v", calls)
	}
}
