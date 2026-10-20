package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type ownedInput struct {
	io.ReadCloser
	started            chan struct{}
	ended              chan struct{}
	startOnce, endOnce sync.Once
	closed             atomic.Bool
}

func (r *ownedInput) Read(p []byte) (int, error) {
	r.startOnce.Do(func() { close(r.started) })
	n, err := r.ReadCloser.Read(p)
	if err != nil {
		r.endOnce.Do(func() { close(r.ended) })
	}
	return n, err
}
func (r *ownedInput) Close() error { r.closed.Store(true); return r.ReadCloser.Close() }

func TestCancellationClosesIdleInputAndJoinsRead(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	input := &ownedInput{ReadCloser: r, started: make(chan struct{}), ended: make(chan struct{})}
	defer input.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (&Server{}).Serve(ctx, input, io.Discard) }()
	<-input.started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !input.closed.Load() {
			t.Fatalf("must close owned input and return cancellation: %v", err)
		}
		select {
		case <-input.ended:
		default:
			t.Fatal("read worker still running after Serve")
		}
	case <-time.After(100 * time.Millisecond):
		_ = input.Close()
		<-done
		t.Fatal("idle stdio cancellation blocked")
	}
}

func TestCancellationDuringInvocationWritesNoLateResponse(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	defer r.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := make(chan struct{})
	s := &Server{Invoke: func(ctx context.Context, _ []string) (json.RawMessage, int) {
		close(called)
		<-ctx.Done()
		return json.RawMessage(`{"status":"error"}`), 1
	}}
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, r, &out) }()
	if _, err := io.WriteString(w, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"explain","arguments":{"term":"plan-approved"}}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	<-called
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || out.Len() != 0 {
			t.Fatalf("cancelled call must not produce late output: %v %s", err, out.String())
		}
	case <-time.After(100 * time.Millisecond):
		_ = r.Close()
		<-done
		t.Fatal("cancelled invocation did not finish")
	}
}

type inputError struct{}

func (inputError) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (inputError) Close() error             { return nil }

func TestServeClosesInputOnEOFAndReadError(t *testing.T) {
	for _, bad := range []bool{false, true} {
		var reader io.ReadCloser = io.NopCloser(strings.NewReader(""))
		if bad {
			reader = inputError{}
		}
		input := &ownedInput{ReadCloser: reader, started: make(chan struct{}), ended: make(chan struct{})}
		err := (&Server{}).Serve(context.Background(), input, io.Discard)
		if !input.closed.Load() || bad && !errors.Is(err, io.ErrUnexpectedEOF) || !bad && err != nil {
			t.Fatalf("input must be owned through termination: bad=%v err=%v", bad, err)
		}
	}
}
