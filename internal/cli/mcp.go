package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"slices"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/mcp"
)

// Invoke 在进程内执行一条命令并返回 JSON envelope 与退出码；--json 由这里追加，
// 与命令行走同一条 RunWithTerminal 路径，因此 envelope、错误码和退出码完全一致。
func Invoke(ctx context.Context, args []string) (json.RawMessage, int) {
	var out bytes.Buffer
	at := slices.Index(args, "--")
	if at < 0 {
		at = len(args)
	}
	argv := append([]string{}, args[:at]...)
	argv = append(argv, "--json")
	argv = append(argv, args[at:]...)
	exit := RunWithTerminal(ctx, argv, &out, io.Discard, nil)
	return json.RawMessage(bytes.TrimSpace(out.Bytes())), exit
}

// ServeMCP 以固定项目根提供 MCP stdio 服务；工具参数不能改写 --root。
func ServeMCP(ctx context.Context, root string, in io.Reader, out io.Writer) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	server := &mcp.Server{Name: "yss", Version: domain.Version, Invoke: func(ctx context.Context, args []string) (json.RawMessage, int) {
		return Invoke(ctx, append(args, "--root", abs))
	}}
	return server.Serve(ctx, in, out)
}
