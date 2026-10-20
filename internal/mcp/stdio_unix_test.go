//go:build !windows

package mcp

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

func TestInheritedStdioCancelsInSubprocess(t *testing.T) {
	if os.Getenv("YSS_TEST_MCP_STDIO_CHILD") == "1" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
		defer stop()
		err := (&Server{}).Serve(ctx, os.Stdin, os.Stdout)
		if errors.Is(err, context.Canceled) {
			os.Exit(0)
		}
		os.Exit(2)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestInheritedStdioCancelsInSubprocess$", "-test.timeout=2s")
	cmd.Env = append(os.Environ(), "YSS_TEST_MCP_STDIO_CHILD=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer stdin.Close()
	defer func() { _ = cmd.Process.Kill() }()
	if _, err = io.WriteString(stdin, `{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	ping := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ping <- line }()
	select {
	case line := <-ping:
		if line == "" {
			t.Fatal("no ping response")
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("stdio did not become ready")
	}
	if err = cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatalf("cancelled child did not exit cleanly: %v", err)
		}
	case <-time.After(300 * time.Millisecond):
		_ = cmd.Process.Kill()
		<-done
		t.Fatal("inherited blocking stdin did not cancel")
	}
}
