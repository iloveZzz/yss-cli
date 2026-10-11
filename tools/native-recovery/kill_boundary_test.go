package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestBoundaryProducerHelper(t *testing.T) {
	root := os.Getenv("YSS_BOUNDARY_PRODUCER_ROOT")
	if root == "" {
		t.Skip("subprocess producer helper")
	}
	temporary := filepath.Join(root, "target.yss-txn-producer-0-apply.tmp")
	f, err := os.Create(temporary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	_, _ = os.Stdout.Write([]byte{1})
	for range 100 {
		if _, err = f.Write(bytes.Repeat([]byte("x"), 4096)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(temporary, filepath.Join(root, "target")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Second)
	t.Fatal("parent did not interrupt producer")
}

func TestKillWaitsForPublishedProducerBytes(t *testing.T) {
	root := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=^TestBoundaryProducerHelper$")
	child.Env = append(os.Environ(), "YSS_BOUNDARY_PRODUCER_ROOT="+root)
	configureChild(child)
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = child.Process.Kill()
			<-done
		}
	})
	ready := make(chan error, 1)
	go func() { _, err := io.ReadFull(output, make([]byte, 1)); ready <- err }()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("producer did not write partial temporary")
	}
	if err = killAtPublishedBoundary(child, root, time.Now().Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	err = <-done
	waited = true
	if err == nil {
		t.Fatal("producer was not interrupted")
	}
	raw, err := os.ReadFile(filepath.Join(root, "target"))
	want := append([]byte("first"), bytes.Repeat([]byte("x"), 100*4096)...)
	if err != nil || !bytes.Equal(raw, want) {
		t.Fatalf("producer killed during an unpublished write: %v, bytes=%d", err, len(raw))
	}
}
