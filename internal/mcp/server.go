// Package mcp 提供 yss 的 MCP stdio 服务：JSON-RPC 2.0，按行分隔。
// 只暴露只读治理查询与核验工具；每个工具转成固定的 yss 参数，交给注入的 Invoke 执行，
// 因而与命令行共用同一校验引擎与 envelope，不新增规则，也不暴露任何写入、提交或推送动作。
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Invoke 执行一条 yss 命令（调用方负责追加 --root 与 --json），返回 JSON envelope 与退出码。
type Invoke func(ctx context.Context, args []string) (json.RawMessage, int)

// SupportedProtocolVersions 按新到旧排列；客户端请求的版本受支持时原样回显，否则回最新版本。
var SupportedProtocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const maxLine = 4 << 20

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type Server struct {
	Name, Version string
	Invoke        Invoke
}

// Serve owns in and closes it on every exit. Close must unblock Read (as it
// does for stdio pipes and io.PipeReader). Tool calls remain sequential.
func (s *Server) Serve(ctx context.Context, in io.ReadCloser, out io.Writer) error {
	if ctx == nil || in == nil {
		return fmt.Errorf("MCP requires a context and owned input")
	}
	readCtx, stop := context.WithCancel(ctx)
	lines := make(chan inputLine, 1)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer close(lines)
		scanner := bufio.NewScanner(in)
		scanner.Buffer(make([]byte, 64<<10), maxLine)
		for scanner.Scan() {
			select {
			case lines <- inputLine{line: scanner.Text()}:
			case <-readCtx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
			select {
			case lines <- inputLine{err: err}:
			case <-readCtx.Done():
			}
		}
	}()
	defer func() { stop(); _ = in.Close(); <-readDone }()
	encoder := json.NewEncoder(out)
	for {
		var input inputLine
		select {
		case <-ctx.Done():
			return ctx.Err()
		case next, ok := <-lines:
			if err := ctx.Err(); err != nil {
				return err
			}
			if !ok {
				return nil
			}
			input = next
		}
		if input.err != nil {
			return input.err
		}
		line := strings.TrimSpace(input.line)
		if line == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil || req.JSONRPC != "2.0" || req.Method == "" {
			if err := encoder.Encode(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "无法解析的 JSON-RPC 请求"}}); err != nil {
				return err
			}
			continue
		}
		result, rerr := s.handle(ctx, req)
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(req.ID) == 0 {
			continue
		}
		if err := encoder.Encode(response{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rerr}); err != nil {
			return err
		}
	}
}

type inputLine struct {
	line string
	err  error
}

func (s *Server) handle(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := SupportedProtocolVersions[0]
		for _, v := range SupportedProtocolVersions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
			"instructions":    "YSS 治理只读工具：路由、状态、注册表与词汇查询、合同阅读、术语解释及核验。结果是 yss 的 JSON envelope；status=error 时读 result 的逐项诊断。工具不创建批准、不写文件、不授予执行。",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		list := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			list = append(list, map[string]any{"name": t.name, "description": t.description, "inputSchema": t.schema(), "annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}})
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{-32602, "tools/call 参数无效"}
		}
		tool, ok := toolByName(p.Name)
		if !ok {
			return nil, &rpcError{-32602, "未知工具: " + p.Name}
		}
		args, err := tool.argv(p.Arguments)
		if err != nil {
			return toolResult(nil, err.Error()), nil
		}
		envelope, exit := s.Invoke(ctx, args)
		return toolResult(envelope, exitText(exit)), nil
	}
	if strings.HasPrefix(req.Method, "notifications/") {
		return nil, nil
	}
	return nil, &rpcError{-32601, "不支持的方法: " + req.Method}
}

// toolResult：参数错误与非零退出都以 isError 返回给模型，而不是协议错误，便于其读诊断后修正。
func toolResult(envelope json.RawMessage, problem string) map[string]any {
	if envelope == nil {
		return map[string]any{"content": []map[string]any{{"type": "text", "text": problem}}, "isError": true}
	}
	result := map[string]any{"content": []map[string]any{{"type": "text", "text": string(envelope)}}, "isError": problem != ""}
	var structured map[string]any
	if json.Unmarshal(envelope, &structured) == nil {
		result["structuredContent"] = structured
	}
	return result
}

func exitText(exit int) string {
	if exit == 0 {
		return ""
	}
	return fmt.Sprintf("exit %d", exit)
}
